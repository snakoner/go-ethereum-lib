package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	ethlib "github.com/snakoner/go-ethereum-lib"
)

const (
	sepoliaRPCURL           = "https://eth-sepolia.g.alchemy.com/v2/UtjFRzFoEQd533NSskUCCCKEpW7z93t2"
	sepoliaMulticallAddress = "0xcA11bde05977b3631167028862bE2a173976CA11"
)

var (
	sepoliaChainID = big.NewInt(11155111)
)

func UUIDToBytes32(id uuid.UUID) [32]byte {
	var out [32]byte
	copy(out[:16], id[:])
	return out
}

func main() {
	// contractAddress := "0x4710fCb1e83bd593f734A6a4910A66DF3d940c5C"
	// fromPrivateKey := "e230e23c4cd059377fa1d4cea5e83ed95acdf2faa49cca063a59326067199425"
	tokenAddress := "0xC55d61E9c41432eE19Ca0a823A82F1ef15998E58"
	client := ethlib.New(
		sepoliaRPCURL,
		sepoliaChainID,
		sepoliaMulticallAddress,
	)

	solidClient := ethlib.NewSolid(
		sepoliaRPCURL,
		sepoliaChainID,
		sepoliaMulticallAddress,
	)

	balancesOf, err := client.BalanceOfMulticall(
		context.Background(),
		tokenAddress,
		[]string{
			"0x455E5AA18469bC6ccEF49594645666C587A3a71B",
			"0x455E5AA18469bC6ccEF49594645666C587A3a71B",
			"0x455E5AA18469bC6ccEF49594645666C587A3a71B",
			"0x455E5AA18469bC6ccEF49594645666C587A3a71B",
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	balancesSolidOf, err := solidClient.BalanceOfMulticall(
		context.Background(),
		tokenAddress,
		[]string{
			"0x455E5AA18469bC6ccEF49594645666C587A3a71B",
			"0x455E5AA18469bC6ccEF49594645666C587A3a71B",
			"0x455E5AA18469bC6ccEF49594645666C587A3a71B",
			"0x455E5AA18469bC6ccEF49594645666C587A3a71B",
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(balancesOf)
	fmt.Println(balancesSolidOf)

	/*
		txHash, err := client.TransferToken(
			context.Background(),
			tokenAddress,
			"0x455E5AA18469bC6ccEF49594645666C587A3a71B",
			big.NewInt(1000000),
			fromPrivateKey,
		)
		if err != nil {
			log.Fatal(err)
		}

		fmt.Println(txHash)
	*/

	/*
		txHash, err := TransferTokensWithNative(
			client,
			fromPrivateKey,
			contractAddress,
			UUIDToBytes32(uuid.New()),
			"0x455E5AA18469bC6ccEF49594645666C587A3a71B",
			big.NewInt(1000000),
			big.NewInt(1000000),
		)
		if err != nil {
			log.Fatal(err)
		}

		err = client.WaitForStatusSuccess(context.Background(), txHash, 30*time.Second)
		if err != nil {
			log.Fatal(err)
		}
	*/
}

func TransferTokensWithNative(
	client *ethlib.Client,
	fromPrivateKey string,
	contractAddress string,
	transactionID [32]byte,
	toAddress string,
	tokenAmount *big.Int,
	nativeAmount *big.Int,
) (string, error) {
	fromAddressPrivKey, err := crypto.HexToECDSA(fromPrivateKey)
	if err != nil {
		return "", err
	}

	fromAddress := crypto.PubkeyToAddress(fromAddressPrivKey.PublicKey)

	callData, err := ethlib.BuildETHFunctionData(
		"transferTokensWithNative(bytes32,address,uint256,uint256)",
		transactionID,
		toAddress,
		tokenAmount,
		nativeAmount,
	)
	if err != nil {
		return "", err
	}

	callObj := map[string]interface{}{
		"from": fromAddress.Hex(),
		"to":   contractAddress,
		"data": callData,
	}

	gasLimit, err := client.EstimateGas(context.Background(), callObj)
	if err != nil {
		return "", err
	}

	signedTx, err := client.SignTx(context.Background(), callData, contractAddress, fromPrivateKey, gasLimit.Uint64())
	if err != nil {
		return "", err
	}

	rawBytes, err := signedTx.MarshalBinary()
	if err != nil {
		return "", err
	}

	return client.SendRawTransaction(context.Background(), "0x"+hex.EncodeToString(rawBytes))
}
