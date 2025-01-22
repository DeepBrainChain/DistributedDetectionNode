package dbc

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Contract address of the deployed AI contract
const contractAddressOnTestnet = "0xb616A0dad9af4cA23234b65D27176be2c09c720c"

const dbcTestNetChainID = 19850818

const dbcTestNetRPC = "https://rpc-testnet.dbcwallet.io"

// const privateKey = "bf1de667d99a5cb417a54eacdb5d5224dd3cf068d4e6700ef39d3e0270cb8ef6"
const privateKey = "346d6d6ff2fffa19cb153bf818b61dee2489a816d13c7710dd3f46ba6ebce17e"

// go test -v -timeout 30s -count=1 -run TestDbcContract DistributedDetectionNode/dbc
func TestDbcContract(t *testing.T) {
	// Load ABI from file
	abiFile := "ai_abi.json"
	abiData, err := os.ReadFile(abiFile)
	if err != nil {
		log.Fatalf("Failed to read ABI file: %v", err)
	}

	// Parse ABI
	parsedABI, err := abi.JSON(strings.NewReader(string(abiData)))
	if err != nil {
		log.Fatalf("Failed to parse ABI: %v", err)
	}

	// Connect to Ethereum node
	client, err := ethclient.Dial(dbcTestNetRPC)
	if err != nil {
		log.Fatalf("Failed to connect to the Ethereum client: %v", err)
	}

	cid, err := client.NetworkID(context.Background())
	if err != nil {
		log.Fatalf("Failed to get chain id: %v", err)
	}
	log.Printf("chain id: %v", cid.Int64())

	// Private key of the account that will sign the transaction
	privateKey, err := crypto.HexToECDSA(privateKey)
	if err != nil {
		log.Fatalf("Failed to load private key: %v", err)
	}

	// Create an authorized transactor
	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, big.NewInt(dbcTestNetChainID))
	if err != nil {
		log.Fatalf("Failed to create transactor: %v", err)
	}

	// Define the inputs for the report function
	notifyType := uint8(1) // Example: NotifyType.MachineRegister
	projectName := "ExampleProject"
	stakingType := uint8(0) // Example: StakingType.ShortTerm
	machineId := "example-machine-id"

	// Encode the function call
	data, err := parsedABI.Pack("report", notifyType, projectName, stakingType, machineId)
	if err != nil {
		log.Fatalf("Failed to pack data: %v", err)
	}

	publicAddress := crypto.PubkeyToAddress(privateKey.PublicKey)

	// Define the target contract address
	toAddress := common.HexToAddress(contractAddressOnTestnet)

	// Estimate gas limit
	callMsg := ethereum.CallMsg{
		From:  publicAddress,
		To:    &toAddress,
		Gas:   0,
		Value: big.NewInt(0),
		Data:  data,
	}
	gasLimit, err := client.EstimateGas(context.Background(), callMsg)
	if err != nil {
		log.Fatalf("Failed to estimate gas limit: %v", err)
	}

	// Get the gas price
	gasPrice, err := client.SuggestGasPrice(context.Background())
	if err != nil {
		log.Fatalf("Failed to suggest gas price: %v", err)
	}

	// Fetch the nonce for the public address
	nonce, err := client.PendingNonceAt(context.Background(), publicAddress)
	if err != nil {
		log.Fatalf("Failed to get nonce: %v", err)
	}

	// Create the transaction
	tx := types.NewTransaction(nonce, toAddress, big.NewInt(0), gasLimit, gasPrice, data)

	// Sign the transaction
	signedTx, err := auth.Signer(auth.From, tx)
	if err != nil {
		log.Fatalf("Failed to sign transaction: %v", err)
	}

	// Send the transaction
	err = client.SendTransaction(context.Background(), signedTx)
	if err != nil {
		log.Fatalf("Failed to send transaction: %v", err)
	}

	fmt.Printf("Transaction sent: %s", signedTx.Hash().Hex())
}
