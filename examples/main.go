package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"time"

	"github.com/google/uuid"
	ethlib "github.com/snakoner/go-ethereum-lib"
)

func UUIDToBytes32(id uuid.UUID) [32]byte {
	var out [32]byte
	copy(out[:16], id[:])
	return out
}

func main() {
	contractAddress := "0x4710fCb1e83bd593f734A6a4910A66DF3d940c5C"
	fromPrivateKey := "e230e23c4cd059377fa1d4cea5e83ed95acdf2faa49cca063a59326067199425"
	tokenAddress := "0xC55d61E9c41432eE19Ca0a823A82F1ef15998E58"
	client := ethlib.NewClient("https://eth-sepolia.g.alchemy.com/v2/<>", big.NewInt(11155111), false, "0xcA11bde05977b3631167028862bE2a173976CA11")

	balances, err := client.BalanceOfMulticall(context.Background(), tokenAddress, []string{
		"0x455E5AA18469bC6ccEF49594645666C587A3a71B",
		"0x4710fCb1e83bd593f734A6a4910A66DF3d940c5C",
		"0xcA11bde05977b3631167028862bE2a173976CA11",
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(len(balances), balances)

	balance, err := client.BalanceAt(context.Background(), "0x2cafae9981772c54167c9944f3c2869c30d70c91")
	if err != nil {
		log.Fatal(err)
	}

	balanceOf, err := client.BalanceOf(context.Background(), tokenAddress, "0x455E5AA18469bC6ccEF49594645666C587A3a71B")
	if err != nil {
		log.Fatal(err)
	}

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

	err = client.WaitForStatusSuccess(context.Background(), txHash, 30*time.Second)
	if err != nil {
		log.Fatal(err)
	}

	uuid := uuid.New()
	bytes32 := UUIDToBytes32(uuid)
	callData, err := ethlib.BuildETHFunctionData(
		"transferTokensWithNative(bytes32,address,uint256,uint256)",
		bytes32,
		"0x2cafae9981772c54167c9944f3c2869c30d70c91",
		big.NewInt(1000000),
		big.NewInt(1000000),
	)
	if err != nil {
		log.Fatal("BuildETHFunctionData error: ", err)
	}

	fmt.Println("callData: ", callData)

	signedTx, err := client.SignTx(context.Background(), callData, contractAddress, fromPrivateKey, 110_000)
	if err != nil {
		log.Fatal("signing tx error: ", err)
	}

	rawBytes, err := signedTx.MarshalBinary()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(rawBytes)

	rawHex := "0x" + hex.EncodeToString(rawBytes)
	txHash, err = client.SendRawTransaction(context.Background(), rawHex)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(txHash)

	fmt.Println(balance)
	fmt.Println(balanceOf)
}
