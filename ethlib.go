package ethlib

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	pollingInterval = 6 * time.Second
)

type Option func(*Client)

type Client struct {
	endpoint         string
	http             *http.Client
	multicallAddress string
	confirmations    int64
	gasBoostNum      *big.Int
	gasBoostDen      *big.Int
}

func New(
	endpoint string,
	multicallAddress string,
	options ...Option,
) *Client {
	c := &Client{
		endpoint:         endpoint,
		http:             http.DefaultClient,
		multicallAddress: multicallAddress,
		gasBoostNum:      big.NewInt(1),
		gasBoostDen:      big.NewInt(1),
		confirmations:    0,
	}

	for _, option := range options {
		option(c)
	}

	return c
}

func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		c.http = httpClient
	}
}

func WithConfirmations(confirmations int64) Option {
	return func(c *Client) {
		c.confirmations = confirmations
	}
}

func WithGasBoost(gasBoost float64) Option {
	return func(c *Client) {
		c.gasBoostNum, c.gasBoostDen = float64ToRational(gasBoost)
	}
}

type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      int           `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) BalanceAt(ctx context.Context, address string) (*big.Int, error) {
	blockTag, err := c.getBlock(ctx)
	if err != nil {
		return nil, err
	}

	var result string
	if err := c.rpcCall(ctx, "eth_getBalance", []interface{}{address, blockTag}, &result); err != nil {
		return nil, err
	}

	return parseHexBigInt(result)
}

func (c *Client) BalanceOf(ctx context.Context, tokenAddress string, address string) (*big.Int, error) {
	data, err := BuildETHFunctionData(balanceOfSignature, address)
	if err != nil {
		return nil, err
	}

	callObj := map[string]interface{}{
		"to":   tokenAddress,
		"data": data,
	}

	blockTag, err := c.getBlock(ctx)
	if err != nil {
		return nil, err
	}

	var result string
	if err := c.rpcCall(ctx, "eth_call", []interface{}{callObj, blockTag}, &result); err != nil {
		return nil, err
	}

	return parseHexBigInt(result)
}

func (c *Client) SendRawTransaction(ctx context.Context, rawHex string) (string, error) {
	if !strings.HasPrefix(rawHex, "0x") {
		rawHex = "0x" + rawHex
	}

	var txHashHex string
	if err := c.rpcCall(ctx, "eth_sendRawTransaction", []interface{}{rawHex}, &txHashHex); err != nil {
		return "", err
	}

	return txHashHex, nil
}

func (c *Client) GetChainID(ctx context.Context) (*big.Int, error) {
	var chainIDHex string
	if err := c.rpcCall(ctx, "eth_chainId", []interface{}{}, &chainIDHex); err != nil {
		return nil, err
	}

	return parseHexBigInt(chainIDHex)
}

func (c *Client) TransferNative(ctx context.Context, to string, amount *big.Int, privateKey string) (string, error) {
	privKey, err := crypto.HexToECDSA(trim0x(privateKey))
	if err != nil {
		return "", err
	}

	from := crypto.PubkeyToAddress(privKey.PublicKey)

	nonce, err := c.GetNonce(ctx, from.Hex())
	if err != nil {
		return "", err
	}

	gasPrice, err := c.GetGasPrice(ctx)
	if err != nil {
		return "", err
	}

	chainID, err := c.GetChainID(ctx)
	if err != nil {
		return "", err
	}

	gasLimit := uint64(21000)
	tx := types.NewTransaction(nonce.Uint64(), common.HexToAddress(to), amount, gasLimit, gasPrice, nil)
	signer := types.LatestSignerForChainID(chainID)
	signedTx, err := types.SignTx(tx, signer, privKey)
	if err != nil {
		return "", err
	}

	rawBytes, err := signedTx.MarshalBinary()
	if err != nil {
		return "", err
	}

	txHash, err := c.SendRawTransaction(ctx, hex.EncodeToString(rawBytes))
	if err != nil {
		return "", err
	}

	return txHash, nil
}

func (c *Client) SignTx(
	ctx context.Context,
	rawHex string,
	to string,
	privKey string,
	gasLimit uint64,
) (*types.Transaction, error) {
	privECDSA, err := crypto.HexToECDSA(trim0x(privKey))
	if err != nil {
		return nil, err
	}

	from := crypto.PubkeyToAddress(privECDSA.PublicKey)

	txBytes, err := hex.DecodeString(trim0x(rawHex))
	if err != nil {
		return nil, err
	}

	nonce, err := c.GetNonce(ctx, from.Hex())
	if err != nil {
		return nil, err
	}

	gasPrice, err := c.GetGasPrice(ctx)
	if err != nil {
		return nil, err
	}

	chainID, err := c.GetChainID(ctx)
	if err != nil {
		return nil, err
	}

	tx := types.NewTransaction(
		nonce.Uint64(),
		common.HexToAddress(to),
		big.NewInt(0),
		gasLimit,
		gasPrice,
		txBytes,
	)
	signer := types.LatestSignerForChainID(chainID)
	signedTx, err := types.SignTx(tx, signer, privECDSA)
	if err != nil {
		return nil, err
	}

	return signedTx, nil
}

func (c *Client) TransferToken(
	ctx context.Context,
	tokenAddress string,
	to string,
	amount *big.Int,
	gasPrice *big.Int,
	privateKey string,
) (string, error) {
	privKey, err := crypto.HexToECDSA(trim0x(privateKey))
	if err != nil {
		return "", err
	}

	from := crypto.PubkeyToAddress(privKey.PublicKey)

	data, err := BuildETHFunctionData(transferSignature, to, amount)
	if err != nil {
		return "", err
	}

	callObj := map[string]interface{}{
		"from": from.Hex(),
		"to":   tokenAddress,
		"data": data,
	}

	gasLimit, err := c.EstimateGas(ctx, callObj)
	if err != nil {
		return "", err
	}

	if gasPrice == nil {
		gasPrice, err = c.GetGasPrice(ctx)
		if err != nil {
			return "", err
		}
	}

	nonce, err := c.GetNonce(ctx, from.Hex())
	if err != nil {
		return "", err
	}

	txBytes, err := hex.DecodeString(trim0x(data))
	if err != nil {
		return "", err
	}

	chainID, err := c.GetChainID(ctx)
	if err != nil {
		return "", err
	}

	tx := types.NewTransaction(
		nonce.Uint64(),
		common.HexToAddress(tokenAddress),
		big.NewInt(0),
		gasLimit.Uint64(),
		gasPrice,
		txBytes,
	)
	signer := types.LatestSignerForChainID(chainID)
	signedTx, err := types.SignTx(tx, signer, privKey)
	if err != nil {
		return "", err
	}

	rawBytes, err := signedTx.MarshalBinary()
	if err != nil {
		return "", err
	}

	rawHex := "0x" + hex.EncodeToString(rawBytes)
	var txHashHex string
	if err := c.rpcCall(ctx, "eth_sendRawTransaction", []interface{}{rawHex}, &txHashHex); err != nil {
		return "", err
	}

	return txHashHex, nil
}

func (c *Client) WaitForStatusSuccess(
	ctx context.Context,
	txHash string,
	maxWaitTime time.Duration,
) error {
	ctx, cancel := context.WithTimeout(ctx, maxWaitTime)
	defer cancel()

	ticker := time.NewTicker(pollingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-ticker.C:
			var receipt struct {
				Status string `json:"status"`
			}

			err := c.rpcCall(ctx, "eth_getTransactionReceipt", []interface{}{txHash}, &receipt)
			if err != nil {
				return err
			}

			if receipt.Status == "" {
				continue
			}

			statusBig, err := parseHexBigInt(receipt.Status)
			if err != nil {
				return err
			}

			if statusBig.Cmp(big.NewInt(1)) == 0 {
				return nil
			}

			return errors.New("transaction failed (status != 1)")
		}
	}
}

func (c *Client) BalanceOfMulticall(
	ctx context.Context,
	tokenAddress string,
	accounts []string,
) ([]*big.Int, error) {
	parsedABI, err := abi.JSON(strings.NewReader(multicall3Aggregate3ABI))
	if err != nil {
		return nil, err
	}

	type call3 struct {
		Target       common.Address
		AllowFailure bool
		CallData     []byte
	}

	calls := make([]call3, 0, len(accounts))
	for _, account := range accounts {
		callDataStr, err := BuildETHFunctionData(balanceOfSignature, account)
		if err != nil {
			return nil, err
		}
		callDataBytes, err := hex.DecodeString(trim0x(callDataStr))
		if err != nil {
			return nil, err
		}

		calls = append(calls, call3{
			Target:       common.HexToAddress(tokenAddress),
			AllowFailure: false,
			CallData:     callDataBytes,
		})
	}

	data, err := parsedABI.Pack("aggregate3", calls)
	if err != nil {
		return nil, err
	}

	callObj := map[string]interface{}{
		"to":   c.multicallAddress,
		"data": "0x" + hex.EncodeToString(data),
	}

	blockTag, err := c.getBlock(ctx)
	if err != nil {
		return nil, err
	}

	var resultHex string
	if err := c.rpcCall(ctx, "eth_call", []interface{}{callObj, blockTag}, &resultHex); err != nil {
		return nil, err
	}

	resBytes, err := hex.DecodeString(trim0x(resultHex))
	if err != nil {
		return nil, err
	}

	var results []struct {
		Success    bool
		ReturnData []byte
	}
	if err := parsedABI.UnpackIntoInterface(&results, "aggregate3", resBytes); err != nil {
		return nil, err
	}

	balances := make([]*big.Int, len(results))
	for i, r := range results {
		if !r.Success {
			return nil, fmt.Errorf("failed to get balance of %s", accounts[i])
		}

		out := new(big.Int).SetBytes(r.ReturnData[len(r.ReturnData)-32:])
		balances[i] = out
	}

	return balances, nil
}

func (c *Client) BalanceAtMulticall(
	ctx context.Context,
	accounts []string,
) ([]*big.Int, error) {
	parsedABI, err := abi.JSON(strings.NewReader(multicall3Aggregate3ABI))
	if err != nil {
		return nil, err
	}

	type call3 struct {
		Target       common.Address
		AllowFailure bool
		CallData     []byte
	}

	calls := make([]call3, 0, len(accounts))
	for _, account := range accounts {
		callDataStr, err := BuildETHFunctionData("getEthBalance(address)", account)
		if err != nil {
			return nil, err
		}

		callDataBytes, err := hex.DecodeString(trim0x(callDataStr))
		if err != nil {
			return nil, err
		}

		calls = append(calls, call3{
			Target:       common.HexToAddress(c.multicallAddress),
			AllowFailure: false,
			CallData:     callDataBytes,
		})
	}

	data, err := parsedABI.Pack("aggregate3", calls)
	if err != nil {
		return nil, err
	}

	callObj := map[string]interface{}{
		"to":   c.multicallAddress,
		"data": "0x" + hex.EncodeToString(data),
	}

	blockTag, err := c.getBlock(ctx)
	if err != nil {
		return nil, err
	}

	var resultHex string
	if err := c.rpcCall(ctx, "eth_call", []interface{}{callObj, blockTag}, &resultHex); err != nil {
		return nil, err
	}

	resBytes, err := hex.DecodeString(trim0x(resultHex))
	if err != nil {
		return nil, err
	}

	var results []struct {
		Success    bool
		ReturnData []byte
	}
	if err := parsedABI.UnpackIntoInterface(&results, "aggregate3", resBytes); err != nil {
		return nil, err
	}

	balances := make([]*big.Int, len(results))
	for i, r := range results {
		if !r.Success {
			return nil, fmt.Errorf("failed to get native balance of %s", accounts[i])
		}

		out := new(big.Int).SetBytes(r.ReturnData[len(r.ReturnData)-32:])
		balances[i] = out
	}

	return balances, nil
}

func (c *Client) Call(ctx context.Context, callObj map[string]interface{}) (string, error) {
	blockTag, err := c.getBlock(ctx)
	if err != nil {
		return "", err
	}

	var result string
	if err := c.rpcCall(ctx, "eth_call", []interface{}{callObj, blockTag}, &result); err != nil {
		return "", err
	}

	return result, nil
}

func PrivateKeyToAddress(privateKey string) (string, error) {
	privKey, err := crypto.HexToECDSA(trim0x(privateKey))
	if err != nil {
		return "", err
	}

	return crypto.PubkeyToAddress(privKey.PublicKey).Hex(), nil
}

func ValidateAddress(address string) bool {
	return common.IsHexAddress(address)
}

func GenerateAddress() (string, string, error) {
	privKey, err := crypto.GenerateKey()
	if err != nil {
		return "", "", err
	}

	privKeyHex := hex.EncodeToString(crypto.FromECDSA(privKey))
	return privKeyHex, crypto.PubkeyToAddress(privKey.PublicKey).Hex(), nil
}
