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
	solidClient := ethlib.NewSolid(
		"https://eth-sepolia.g.alchemy.com/v2/<>",
		"0xcA11bde05977b3631167028862bE2a173976CA11",
		100,
	)

	ethBalances, err := solidClient.BalanceAtMulticall(context.Background(), []string{
		"0xDf8F2FA7F54277E802D04cbdDFab6DCEACAb672a",
		"0x6323a138fee57a4d68cf9e79d7ac08e4069fd860",
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(ethBalances)
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

func getCurvePrice(
	ctx context.Context,
	client *ethlib.Client,
	pool string,
	i int64,
	j int64,
	decimalsIn int,
	decimalsOut int,
) (float64, error) {
	dx := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimalsIn)), nil)

	callData, err := ethlib.BuildETHFunctionData(
		"get_dy(int128,int128,uint256)",
		big.NewInt(i),
		big.NewInt(j),
		dx,
	)
	if err != nil {
		return 0, err
	}

	callObj := map[string]interface{}{
		"to":   pool,
		"data": callData,
	}

	resp, err := client.Call(ctx, callObj)
	if err != nil {
		return 0, err
	}

	resBytes, err := hex.DecodeString(strings.TrimPrefix(resp, "0x"))
	if err != nil {
		return 0, err
	}

	args := abi.Arguments{
		{Type: mustNewType("uint256")},
	}

	values, err := args.Unpack(resBytes)
	if err != nil {
		return 0, err
	}

	dy, ok := values[0].(*big.Int)
	if !ok {
		return 0, fmt.Errorf("invalid dy")
	}

	num := new(big.Float).SetInt(dy)
	den := new(big.Float).SetInt(
		new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimalsOut)), nil),
	)

	price, _ := new(big.Float).Quo(num, den).Float64()
	return price, nil
}
