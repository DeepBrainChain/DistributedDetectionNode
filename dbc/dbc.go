package dbc

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"math/big"
	"os"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"DistributedDetectionNode/log"
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
		log.Log.Errorf("Failed to read ABI file: %v", err)
		return nil, err
	}
	defer file.Close()
	abi, err := abi.JSON(file)
	if err != nil {
		log.Log.Errorf("Failed to parse ABI: %v", err)
		return nil, err
	}
	client, err := ethclient.Dial(rpc)
	if err != nil {
		log.Log.Errorf("Failed to connect to the Ethereum client: %v", err)
		return nil, err
	}
	cid, err := client.NetworkID(ctx)
	if err != nil {
		if chainId != 0 {
			cid = big.NewInt(chainId)
		} else {
			log.Log.Errorf("Failed to get chain id: %v", err)
			return nil, err
		}
	}
	if !(common.IsHexAddress(address)) {
		log.Log.Errorf("Invalid contract address: %v", address)
		return nil, errors.New("invalid contract address")
	}
	addr := common.HexToAddress(address)
	privateKey, err := crypto.HexToECDSA(hexkey)
	if err != nil {
		log.Log.Errorf("Failed to load private key: %v", err)
		return nil, err
	}
	return &DbcChain{
		rpc:             rpc,
		abi:             abi,
		contractAddress: addr,
		privateKey:      privateKey,
		chainId:         cid,
	}, nil
}

func (chain *DbcChain) Report(notifyType mt.NotifyType, stakingType mt.StakingType, projectName, machineId string) error {
	// Connect to Ethereum node
	client, err := ethclient.Dial(chain.rpc)
	if err != nil {
		log.Log.Errorf("Failed to connect to the Ethereum client: %v", err)
		return err
	}

	// Create an authorized transactor
	auth, err := bind.NewKeyedTransactorWithChainID(chain.privateKey, chain.chainId)
	if err != nil {
		log.Log.Errorf("Failed to create transactor: %v", err)
		return err
	}

	// Define the inputs for the report function
	// notifyType := uint8(1) // Example: NotifyType.MachineRegister
	// projectName := "ExampleProject"
	// stakingType := uint8(0) // Example: StakingType.ShortTerm
	// machineId := "example-machine-id"

	// Encode the function call
	data, err := chain.abi.Pack("report", notifyType, projectName, stakingType, machineId)
	if err != nil {
		log.Log.Errorf("Failed to pack data: %v", err)
		return err
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
	gasLimit, err := client.EstimateGas(context.Background(), callMsg)
	if err != nil {
		log.Log.Errorf("Failed to estimate gas limit: %v", err)
		return err
	}

	// Get the gas price
	gasPrice, err := client.SuggestGasPrice(context.Background())
	if err != nil {
		log.Log.Errorf("Failed to suggest gas price: %v", err)
		return err
	}

	// Fetch the nonce for the public address
	nonce, err := client.PendingNonceAt(context.Background(), publicAddress)
	if err != nil {
		log.Log.Errorf("Failed to get nonce: %v", err)
		return err
	}

	// Create the transaction
	tx := types.NewTransaction(nonce, chain.contractAddress, big.NewInt(0), gasLimit, gasPrice, data)

	// Sign the transaction
	signedTx, err := auth.Signer(auth.From, tx)
	if err != nil {
		log.Log.Errorf("Failed to sign transaction: %v", err)
		return err
	}

	// Send the transaction
	err = client.SendTransaction(context.Background(), signedTx)
	if err != nil {
		log.Log.Errorf("Failed to send transaction: %v", err)
		return err
	}
	return nil
}
