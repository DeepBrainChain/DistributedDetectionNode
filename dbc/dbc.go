package dbc

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"os"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	mt "DistributedDetectionNode/types"
)

type DbcChain struct {
	rpc             string
	abi             abi.ABI
	contractAddress common.Address
	privateKey      *ecdsa.PrivateKey
	chainId         *big.Int
}

func InitDbcChain(ctx context.Context, abifile, rpc, address, hexkey string, chainId int64) (*DbcChain, error) {
	file, err := os.Open(abifile)
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
	if !(common.IsHexAddress(address)) {
		return nil, fmt.Errorf("invalid contract address: %v", address)
	}
	addr := common.HexToAddress(address)
	privateKey, err := crypto.HexToECDSA(hexkey)
	if err != nil {
		return nil, fmt.Errorf("failed to load private key: %v", err)
	}
	return &DbcChain{
		rpc:             rpc,
		abi:             abi,
		contractAddress: addr,
		privateKey:      privateKey,
		chainId:         cid,
	}, nil
}

func (chain *DbcChain) Report(ctx context.Context, notifyType mt.NotifyType, stakingType mt.StakingType, projectName, machineId string) (string, error) {
	// Connect to Ethereum node
	client, err := ethclient.Dial(chain.rpc)
	if err != nil {
		return "", fmt.Errorf("failed to connect to the Ethereum client: %v", err)
	}

	// Create an authorized transactor
	auth, err := bind.NewKeyedTransactorWithChainID(chain.privateKey, chain.chainId)
	if err != nil {
		return "", fmt.Errorf("failed to create transactor: %v", err)
	}

	// Define the inputs for the report function
	// notifyType := uint8(1) // Example: NotifyType.MachineRegister
	// projectName := "ExampleProject"
	// stakingType := uint8(0) // Example: StakingType.ShortTerm
	// machineId := "example-machine-id"

	// Encode the function call
	data, err := chain.abi.Pack("report", notifyType, projectName, stakingType, machineId)
	if err != nil {
		return "", fmt.Errorf("failed to pack data: %v", err)
	}

	publicAddress := crypto.PubkeyToAddress(chain.privateKey.PublicKey)

	// Estimate gas limit
	callMsg := ethereum.CallMsg{
		From:  publicAddress,
		To:    &chain.contractAddress,
		Gas:   0,
		Value: big.NewInt(0),
		Data:  data,
	}
	gasLimit, err := client.EstimateGas(ctx, callMsg)
	if err != nil {
		return "", fmt.Errorf("failed to estimate gas limit: %v", err)
	}

	// Get the gas price
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to suggest gas price: %v", err)
	}

	// Fetch the nonce for the public address
	nonce, err := client.PendingNonceAt(ctx, publicAddress)
	if err != nil {
		return "", fmt.Errorf("failed to get nonce: %v", err)
	}

	// Create the transaction
	tx := types.NewTransaction(nonce, chain.contractAddress, big.NewInt(0), gasLimit, gasPrice, data)

	// Sign the transaction
	signedTx, err := auth.Signer(auth.From, tx)
	if err != nil {
		return "", fmt.Errorf("failed to sign transaction: %v", err)
	}

	// Send the transaction
	err = client.SendTransaction(ctx, signedTx)
	if err != nil {
		return signedTx.Hash().Hex(), fmt.Errorf("failed to send transaction: %v", err)
	}
	return signedTx.Hash().Hex(), nil
}
