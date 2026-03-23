package dbc

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	aireport "DistributedDetectionNode/dbc/ai-report"
	machineinfos "DistributedDetectionNode/dbc/machine-infos"
	mt "DistributedDetectionNode/types"
)

var DbcChain *dbcChain = nil

type chainContract struct {
	abi             abi.ABI
	contractAddress common.Address
	chainId         *big.Int
}

// txWallet is an independent wallet with its own nonce tracking for parallel tx sending.
type txWallet struct {
	privateKey  *ecdsa.PrivateKey
	address     common.Address
	nonceMu     sync.Mutex
	nextNonce   uint64
	nonceInited bool
}

func (w *txWallet) allocateNonce(ctx context.Context, client *ethclient.Client) (uint64, error) {
	w.nonceMu.Lock()
	defer w.nonceMu.Unlock()
	if !w.nonceInited {
		pending, err := client.PendingNonceAt(ctx, w.address)
		if err != nil {
			return 0, err
		}
		w.nextNonce = pending
		w.nonceInited = true
	}
	nonce := w.nextNonce
	w.nextNonce++
	return nonce, nil
}

func (w *txWallet) resetNonce() {
	w.nonceMu.Lock()
	defer w.nonceMu.Unlock()
	w.nonceInited = false
}

type dbcChain struct {
	rpcEndpoints []string             // multiple RPC endpoints for failover
	rpcIdx       atomic.Uint64        // round-robin for RPC selection
	privateKey   *ecdsa.PrivateKey    // legacy single key (used for read-only / signing)
	wallets      []*txWallet          // wallet pool for parallel transactions
	walletIdx    atomic.Uint64        // round-robin counter for wallet selection
	report       *chainContract
	machineInfos *chainContract
	rent         *chainContract       // Rent 合约（可选，用于查询 isRented）
}

// dialRPC tries RPC endpoints in round-robin order with failover.
// Returns a connected client or error if all endpoints fail.
func (chain *dbcChain) dialRPC() (*ethclient.Client, error) {
	n := len(chain.rpcEndpoints)
	startIdx := chain.rpcIdx.Add(1) - 1
	var lastErr error
	for i := 0; i < n; i++ {
		rpc := chain.rpcEndpoints[(startIdx+uint64(i))%uint64(n)]
		client, err := ethclient.Dial(rpc)
		if err != nil {
			lastErr = err
			continue
		}
		return client, nil
	}
	return nil, fmt.Errorf("all RPC endpoints failed, last error: %v", lastErr)
}

func InitDbcChain(ctx context.Context, config mt.ChainConfig) error {
	reportContract, err := initChainContract(ctx, config.ReportContract, config.Rpc, config.ChainId)
	if err != nil {
		return err
	}
	machineInfoContract, err := initChainContract(ctx, config.MachineInfoContract, config.Rpc, config.ChainId)
	if err != nil {
		return err
	}

	// Build RPC endpoint list: use RpcEndpoints if configured, otherwise single Rpc
	rpcs := config.RpcEndpoints
	if len(rpcs) == 0 {
		rpcs = []string{config.Rpc}
	}
	fmt.Printf("[DDN] RPC endpoints: %v\n", rpcs)

	// Build wallet pool: use PrivateKeys if configured, otherwise single PrivateKey
	keys := config.PrivateKeys
	if len(keys) == 0 {
		keys = []string{config.PrivateKey}
	}

	var wallets []*txWallet
	for i, keyHex := range keys {
		pk, err := crypto.HexToECDSA(keyHex)
		if err != nil {
			return fmt.Errorf("failed to load private key #%d: %v", i, err)
		}
		addr := crypto.PubkeyToAddress(pk.PublicKey)
		wallets = append(wallets, &txWallet{privateKey: pk, address: addr})
		fmt.Printf("[DDN] Wallet #%d: %s\n", i, addr.Hex())
	}

	primaryKey, _ := crypto.HexToECDSA(keys[0])
	// Rent 合约（可选）— 用于查询 isRented 以监测租赁中离线
	var rentContract *chainContract
	if config.RentContract.ContractAddress != "" && config.RentContract.AbiFile != "" {
		rc, err := initChainContract(ctx, config.RentContract, config.Rpc, config.ChainId)
		if err != nil {
			fmt.Printf("[DDN] WARNING: Rent contract init failed: %v (rental offline detection disabled)\n", err)
		} else {
			rentContract = rc
			fmt.Printf("[DDN] Rent contract loaded: %s\n", config.RentContract.ContractAddress)
		}
	}

	DbcChain = &dbcChain{
		rpcEndpoints: rpcs,
		privateKey:   primaryKey,
		wallets:      wallets,
		report:       reportContract,
		machineInfos: machineInfoContract,
		rent:         rentContract,
	}
	fmt.Printf("[DDN] Initialized %d wallet(s), %d RPC(s)\n", len(wallets), len(rpcs))
	return nil
}

func initChainContract(ctx context.Context, config mt.ContractConfig, rpc string, chainId int64) (*chainContract, error) {
	file, err := os.Open(config.AbiFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read ABI file: %v", err)
	}
	defer file.Close()
	abi, err := abi.JSON(file)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ABI: %v", err)
	}
	client, err := ethclient.Dial(rpc)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to the Ethereum client: %v", err)
	}
	cid, err := client.NetworkID(ctx)
	if err != nil {
		if chainId != 0 {
			cid = big.NewInt(chainId)
		} else {
			return nil, fmt.Errorf("failed to get chain id: %v", err)
		}
	}
	if !(common.IsHexAddress(config.ContractAddress)) {
		return nil, fmt.Errorf("invalid contract address: %v", config.ContractAddress)
	}
	addr := common.HexToAddress(config.ContractAddress)
	return &chainContract{
		abi:             abi,
		contractAddress: addr,
		chainId:         cid,
	}, nil
}

// nextWallet picks the next wallet from the pool via round-robin.
func (chain *dbcChain) nextWallet() *txWallet {
	idx := chain.walletIdx.Add(1) - 1
	return chain.wallets[idx%uint64(len(chain.wallets))]
}

// sendTx sends on-chain transactions using a wallet pool for true parallelism.
// Each wallet has its own nonce, so multiple wallets can send concurrently
// without nonce conflicts. Round-robin distributes load evenly.
func (chain *dbcChain) sendTx(ctx context.Context, contract *chainContract, data []byte) (string, error) {
	wallet := chain.nextWallet()

	// Use independent context — caller's ctx may have short timeout from queuing.
	// The 30s timeout is only for the RPC calls themselves.
	txCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := chain.dialRPC()
	if err != nil {
		return "", err
	}
	defer client.Close()

	auth, err := bind.NewKeyedTransactorWithChainID(wallet.privateKey, contract.chainId)
	if err != nil {
		return "", fmt.Errorf("failed to create transactor: %v", err)
	}

	gasLimit, err := client.EstimateGas(txCtx, ethereum.CallMsg{
		From: wallet.address, To: &contract.contractAddress, Gas: 0, Value: big.NewInt(0), Data: data,
	})
	if err != nil {
		return "", fmt.Errorf("failed to estimate gas limit: %v", err)
	}

	gasPrice, err := client.SuggestGasPrice(txCtx)
	if err != nil {
		return "", fmt.Errorf("failed to suggest gas price: %v", err)
	}

	nonce, err := wallet.allocateNonce(txCtx, client)
	if err != nil {
		return "", fmt.Errorf("failed to allocate nonce: %v", err)
	}

	tx := types.NewTransaction(nonce, contract.contractAddress, big.NewInt(0), gasLimit, gasPrice, data)
	signedTx, err := auth.Signer(auth.From, tx)
	if err != nil {
		wallet.resetNonce()
		return "", fmt.Errorf("failed to sign transaction: %v", err)
	}

	if err = client.SendTransaction(txCtx, signedTx); err != nil {
		wallet.resetNonce()
		return signedTx.Hash().Hex(), fmt.Errorf("failed to send transaction: %v", err)
	}
	return signedTx.Hash().Hex(), nil
}

func (chain *dbcChain) Report(
	ctx context.Context,
	notifyType mt.NotifyType,
	stakingType mt.StakingType,
	projectName, machineId string,
) (string, error) {
	data, err := chain.report.abi.Pack("report", notifyType, projectName, stakingType, machineId)
	if err != nil {
		return "", fmt.Errorf("failed to pack data: %v", err)
	}
	return chain.sendTx(ctx, chain.report, data)
}

func (chain *dbcChain) GetMachineState(
	ctx context.Context,
	projectName, machineId string,
	stakingType mt.StakingType,
) (bool, bool, error) {
	// Connect to Ethereum node (with failover)
	client, err := chain.dialRPC()
	if err != nil {
		return false, false, err
	}
	defer client.Close()

	instance, err := aireport.NewAireport(chain.report.contractAddress, client)
	if err != nil {
		return false, false, fmt.Errorf("failed to new aireport instance: %v", err)
	}

	ms, err := instance.GetMachineState(nil, machineId, projectName, uint8(stakingType))
	return ms.IsOnline, ms.IsRegistered, err
}

func (chain *dbcChain) SetDeepLinkMachineInfoST(
	ctx context.Context,
	mk mt.MachineKey,
	mi mt.DeepLinkMachineInfoST,
	calcPoint int64,
	longitude, latitude float32,
	region string,
) (string, error) {
	info := machineinfos.MachineInfosMachineInfo{
		MachineOwner: common.HexToAddress(mi.Wallet),
		CalcPoint:    big.NewInt(calcPoint),
		CpuRate:      big.NewInt(int64(mi.CpuRate)),
		GpuType:      mi.GPUNames[0],
		GpuMem:       big.NewInt(int64(mi.GPUMemoryTotal[0])),
		CpuType:      mi.CpuType,
		GpuCount:     big.NewInt(int64(len(mi.GPUNames))),
		MachineId:    mk.ContainerId,
		Longitude:    fmt.Sprintf("%f", longitude),
		Latitude:     fmt.Sprintf("%f", latitude),
		MachineMem:   big.NewInt(mi.MemoryTotal),
		Region:       region,
		Model:        "",
	}

	data, err := chain.machineInfos.abi.Pack("setMachineInfo", mk.MachineId, info)
	if err != nil {
		return "", fmt.Errorf("failed to pack data: %v", err)
	}
	return chain.sendTx(ctx, chain.machineInfos, data)
}

func (chain *dbcChain) SetDeepLinkMachineInfoBandwidth(
	ctx context.Context,
	mk mt.MachineKey,
	mi mt.DeepLinkMachineInfoBandwidth,
	region string,
) (string, error) {
	info := machineinfos.MachineInfosBandWidthMintInfo{
		MachineOwner: common.HexToAddress(mi.Wallet),
		MachineId:    mk.ContainerId,
		CpuCores:     big.NewInt(int64(mi.CpuCores)),
		MachineMem:   big.NewInt(mi.MemoryTotal),
		Region:       region,
		Hdd:          big.NewInt(mi.Hdd),
		Bandwidth:    big.NewInt(int64(mi.Bandwidth)),
	}

	data, err := chain.machineInfos.abi.Pack("setBandWidthInfos", mk.MachineId, info)
	if err != nil {
		return "", fmt.Errorf("failed to pack data: %v", err)
	}
	return chain.sendTx(ctx, chain.machineInfos, data)
}

// HasRentContract returns true if Rent contract is configured.
func (chain *dbcChain) HasRentContract() bool {
	return chain.rent != nil
}

// IsRented queries the Rent contract to check if a machine is currently rented.
func (chain *dbcChain) IsRented(ctx context.Context, machineId string) (bool, error) {
	if chain.rent == nil {
		return false, fmt.Errorf("rent contract not configured")
	}

	client, err := chain.dialRPC()
	if err != nil {
		return false, err
	}
	defer client.Close()

	// Pack the call: isRented(string machineId) returns (bool)
	data, err := chain.rent.abi.Pack("isRented", machineId)
	if err != nil {
		return false, fmt.Errorf("failed to pack isRented call: %v", err)
	}

	to := chain.rent.contractAddress
	result, err := client.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
	if err != nil {
		return false, fmt.Errorf("isRented call failed: %v", err)
	}

	out, err := chain.rent.abi.Unpack("isRented", result)
	if err != nil {
		return false, fmt.Errorf("failed to unpack isRented result: %v", err)
	}
	if len(out) == 0 {
		return false, nil
	}
	rented, ok := out[0].(bool)
	if !ok {
		return false, fmt.Errorf("unexpected isRented return type")
	}
	return rented, nil
}
