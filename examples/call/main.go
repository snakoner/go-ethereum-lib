package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	ethlib "github.com/snakoner/go-ethereum-lib"
)

func main() {
	client := ethlib.New(
		"https://eth-mainnet.g.alchemy.com/v2/",
		"0xcA11bde05977b3631167028862bE2a173976CA11",
	)

	price, err := getUniswapPrice(context.Background(), client, "0x1320483123658e2192CEb6c4150a759f4398c5e4")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(price)
}

func mustNewType(t string) abi.Type {
	typ, err := abi.NewType(t, "", nil)
	if err != nil {
		panic(err)
	}
	return typ
}

type Slot0 struct {
	SqrtPriceX96 *big.Int
}

func getUniswapPrice(ctx context.Context, client *ethlib.Client, poolAddress string) (float64, error) {
	calldata, err := ethlib.BuildETHFunctionData("slot0()")
	if err != nil {
		return 0, err
	}

	callObj := map[string]interface{}{
		"to":   poolAddress,
		"data": calldata,
	}

	res, err := client.Call(ctx, callObj)
	if err != nil {
		return 0, err
	}

	resBytes, err := hex.DecodeString(strings.TrimPrefix(res, "0x"))
	if err != nil {
		return 0, err
	}

	args := abi.Arguments{
		{Type: mustNewType("uint160")},
	}

	values, err := args.Unpack(resBytes)
	if err != nil {
		return 0, err
	}

	sqrtPriceX96, ok := values[0].(*big.Int)
	if !ok {
		return 0, fmt.Errorf("invalid sqrtPriceX96")
	}

	sqrtPrice := new(big.Float).SetInt(sqrtPriceX96)

	twoPow96Int := new(big.Int).Lsh(big.NewInt(1), 96)
	twoPow96 := new(big.Float).SetInt(twoPow96Int)

	ratio := new(big.Float).Quo(sqrtPrice, twoPow96)
	price := new(big.Float).Mul(ratio, ratio)

	priceFloat, _ := price.Float64()

	return priceFloat, nil
}
