package ethlib

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/crypto/sha3"
)

const (
	pollingInterval = 6 * time.Second
)

type Client struct {
	endpoint         string
	http             *http.Client
	chainID          *big.Int
	timeout          time.Duration
	multicallAddress string
	solid            bool
}

func NewClient(endpoint string, chainID *big.Int, solid bool, multicallAddress string) *Client {
	return &Client{
		endpoint:         endpoint,
		http:             http.DefaultClient,
		chainID:          new(big.Int).Set(chainID),
		multicallAddress: multicallAddress,
		solid:            solid,
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

func (c *Client) getBlock() string {
	if !c.solid {
		return "latest"
	}

	return "safe"
}

func (c *Client) rpcCall(ctx context.Context, method string, params []interface{}, out interface{}) error {
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	reqBody, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var rpcResp rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return err
	}

	if rpcResp.Error != nil {
		return fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	if out == nil {
		return nil
	}
	return json.Unmarshal(rpcResp.Result, out)
}

func toHexQuantity(n *big.Int) string {
	if n == nil {
		return "0x0"
	}
	return "0x" + n.Text(16)
}

func parseHexBigInt(hexStr string) (*big.Int, error) {
	if len(hexStr) >= 2 && hexStr[0:2] == "0x" {
		hexStr = hexStr[2:]
	}
	if hexStr == "" {
		return big.NewInt(0), nil
	}
	n := new(big.Int)
	_, ok := n.SetString(hexStr, 16)
	if !ok {
		return nil, fmt.Errorf("invalid hex quantity: %s", hexStr)
	}
	return n, nil
}

func trim0x(s string) string {
	if len(s) >= 2 && (s[0:2] == "0x" || s[0:2] == "0X") {
		return s[2:]
	}
	return s
}

func (c *Client) BalanceAt(ctx context.Context, addr string) (*big.Int, error) {
	blockTag := c.getBlock()
	var result string
	if err := c.rpcCall(ctx, "eth_getBalance", []interface{}{addr, blockTag}, &result); err != nil {
		return nil, err
	}

	return parseHexBigInt(result)
}

func (c *Client) BalanceOf(ctx context.Context, tokenAddr string, address string) (*big.Int, error) {
	selector := methodSelector("balanceOf(address)")

	var data [4 + 32]byte
	copy(data[0:4], selector)
	copy(data[4+12:], common.HexToAddress(address).Bytes())

	callObj := map[string]interface{}{
		"to":   tokenAddr,
		"data": "0x" + hex.EncodeToString(data[:]),
	}

	blockTag := c.getBlock()
	var result string
	if err := c.rpcCall(ctx, "eth_call", []interface{}{callObj, blockTag}, &result); err != nil {
		return nil, err
	}

	return parseHexBigInt(result)
}

func (c *Client) GetNonce(ctx context.Context, addr string) (*big.Int, error) {
	var nonceHex string
	if err := c.rpcCall(ctx, "eth_getTransactionCount", []interface{}{addr, "pending"}, &nonceHex); err != nil {
		return nil, err
	}
	return parseHexBigInt(nonceHex)
}

func (c *Client) GetGasPrice(ctx context.Context) (*big.Int, error) {
	var gasPriceHex string
	if err := c.rpcCall(ctx, "eth_gasPrice", []interface{}{}, &gasPriceHex); err != nil {
		return nil, err
	}

	return parseHexBigInt(gasPriceHex)
}

func (c *Client) SendRawTransaction(ctx context.Context, rawHex string) (string, error) {
	var txHashHex string
	if err := c.rpcCall(ctx, "eth_sendRawTransaction", []interface{}{rawHex}, &txHashHex); err != nil {
		return "", err
	}
	return txHashHex, nil
}

func (c *Client) TransferNative(ctx context.Context, to string, amountWei *big.Int, privKeyHex string) (string, error) {
	privKey, err := crypto.HexToECDSA(trim0x(privKeyHex))
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

	gasLimit := uint64(21000)

	tx := types.NewTransaction(nonce.Uint64(), common.HexToAddress(to), amountWei, gasLimit, gasPrice, nil)

	signer := types.LatestSignerForChainID(c.chainID)
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

func (c *Client) SignTx(
	ctx context.Context,
	rawHex string,
	contractAddress string,
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

	tx := types.NewTransaction(nonce.Uint64(), common.HexToAddress(contractAddress), big.NewInt(0), gasLimit, gasPrice, txBytes)

	signer := types.LatestSignerForChainID(c.chainID)
	signedTx, err := types.SignTx(tx, signer, privECDSA)
	if err != nil {
		return nil, err
	}

	return signedTx, nil
}

func (c *Client) TransferToken(ctx context.Context, tokenAddr string, to string, amount *big.Int, privKeyHex string) (string, error) {
	privKey, err := crypto.HexToECDSA(trim0x(privKeyHex))
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

	selector := methodSelector("transfer(address,uint256)")

	data := make([]byte, 4+32+32)
	copy(data[0:4], selector)
	copy(data[4+12:4+32], common.HexToAddress(to).Bytes())
	amountBytes := amount.Bytes()
	copy(data[4+32+32-len(amountBytes):], amountBytes)

	callObj := map[string]interface{}{
		"from": from.Hex(),
		"to":   tokenAddr,
		"data": "0x" + hex.EncodeToString(data),
	}

	var gasHex string
	if err := c.rpcCall(ctx, "eth_estimateGas", []interface{}{callObj}, &gasHex); err != nil {
		return "", err
	}
	gasLimitBig, err := parseHexBigInt(gasHex)
	if err != nil {
		return "", err
	}

	tx := types.NewTransaction(nonce.Uint64(), common.HexToAddress(tokenAddr), big.NewInt(0), gasLimitBig.Uint64(), gasPrice, data)

	signer := types.LatestSignerForChainID(c.chainID)
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
func methodSelector(signature string) []byte {
	hash := sha3.NewLegacyKeccak256()
	hash.Write([]byte(signature))
	full := hash.Sum(nil)
	return full[:4]
}

func BuildETHFunctionData(functionSig string, params ...any) (string, error) {
	if strings.TrimSpace(functionSig) == "" {
		return "", errors.New("empty function signature")
	}

	paramHex, err := BuildETHABIParams(functionSig, params...)
	if err != nil {
		return "", err
	}

	selector := methodSelector(functionSig)
	return "0x" + hex.EncodeToString(selector) + paramHex, nil
}

func BuildETHABIParams(functionSig string, params ...any) (string, error) {
	types, err := parseETHFunctionSignature(functionSig)
	if err != nil {
		return "", err
	}
	if len(types) != len(params) {
		return "", fmt.Errorf("params count mismatch: expected %d, got %d", len(types), len(params))
	}

	encoded := make([]string, 0, len(params))
	for i, typ := range types {
		part, err := encodeETHABIParamByType(typ, params[i])
		if err != nil {
			return "", fmt.Errorf("param %d (%s): %w", i, typ, err)
		}
		encoded = append(encoded, part)
	}

	return strings.Join(encoded, ""), nil
}

func parseETHFunctionSignature(sig string) ([]string, error) {
	sig = strings.TrimSpace(sig)
	open := strings.Index(sig, "(")
	close := strings.LastIndex(sig, ")")
	if open < 0 || close < 0 || close < open {
		return nil, fmt.Errorf("invalid function signature: %s", sig)
	}

	inside := strings.TrimSpace(sig[open+1 : close])
	if inside == "" {
		return nil, nil
	}

	rawTypes := strings.Split(inside, ",")
	types := make([]string, 0, len(rawTypes))
	for _, t := range rawTypes {
		tt := normalizeETHABIType(t)
		if tt == "" {
			return nil, fmt.Errorf("empty type in signature: %s", sig)
		}
		types = append(types, tt)
	}
	return types, nil
}

func normalizeETHABIType(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)

	switch s {
	case "uint":
		return "uint256"
	case "int":
		return "int256"
	default:
		return s
	}
}

func encodeETHABIParamByType(typ string, v any) (string, error) {
	switch normalizeETHABIType(typ) {
	case "address":
		return encodeETHABIAddress(v)
	case "uint256":
		return encodeETHABIUint256(v)
	case "int256":
		return encodeETHABIInt256(v)
	case "bool":
		return encodeETHABIBool(v)
	case "bytes32":
		return encodeETHABIBytes32(v)
	default:
		return "", fmt.Errorf("unsupported abi type: %s", typ)
	}
}

func encodeETHABIAddress(v any) (string, error) {
	switch x := v.(type) {
	case string:
		addr := common.HexToAddress(x)
		return leftPad64(hex.EncodeToString(addr.Bytes())), nil
	case common.Address:
		return leftPad64(hex.EncodeToString(x.Bytes())), nil
	default:
		return "", fmt.Errorf("address must be string or common.Address, got %T", v)
	}
}

func encodeETHABIBytes32(v any) (string, error) {
	bytes32, ok := v.([32]byte)
	if !ok {
		return "", fmt.Errorf("bytes32 must be [32]byte, got %T", v)
	}
	return leftPad64(hex.EncodeToString(bytes32[:])), nil
}

func encodeETHABIUint256(v any) (string, error) {
	n, err := toBigInt(v)
	if err != nil {
		return "", err
	}
	if n.Sign() < 0 {
		return "", errors.New("uint256 cannot be negative")
	}
	return leftPad64(strings.TrimLeft(n.Text(16), "0")), nil
}

func encodeETHABIInt256(v any) (string, error) {
	n, err := toBigInt(v)
	if err != nil {
		return "", err
	}

	limit := new(big.Int).Lsh(big.NewInt(1), 255)
	if n.Cmp(limit) >= 0 || n.Cmp(new(big.Int).Neg(limit)) < 0 {
		return "", errors.New("int256 overflow")
	}

	if n.Sign() >= 0 {
		return leftPad64(strings.TrimLeft(n.Text(16), "0")), nil
	}

	mod := new(big.Int).Lsh(big.NewInt(1), 256)
	twosComplement := new(big.Int).Add(mod, n)
	return leftPad64(strings.TrimLeft(twosComplement.Text(16), "0")), nil
}

func encodeETHABIBool(v any) (string, error) {
	var b bool

	switch x := v.(type) {
	case bool:
		b = x
	case string:
		parsed, err := strconv.ParseBool(x)
		if err != nil {
			return "", fmt.Errorf("invalid bool string: %q", x)
		}
		b = parsed
	default:
		return "", fmt.Errorf("bool must be bool or string, got %T", v)
	}

	if b {
		return leftPad64("1"), nil
	}
	return leftPad64("0"), nil
}

func toBigInt(v any) (*big.Int, error) {
	switch x := v.(type) {
	case *big.Int:
		if x == nil {
			return nil, errors.New("nil *big.Int")
		}
		return new(big.Int).Set(x), nil
	case big.Int:
		return new(big.Int).Set(&x), nil
	case int:
		return big.NewInt(int64(x)), nil
	case int8:
		return big.NewInt(int64(x)), nil
	case int16:
		return big.NewInt(int64(x)), nil
	case int32:
		return big.NewInt(int64(x)), nil
	case int64:
		return big.NewInt(x), nil
	case uint:
		z := new(big.Int)
		z.SetUint64(uint64(x))
		return z, nil
	case uint8:
		z := new(big.Int)
		z.SetUint64(uint64(x))
		return z, nil
	case uint16:
		z := new(big.Int)
		z.SetUint64(uint64(x))
		return z, nil
	case uint32:
		z := new(big.Int)
		z.SetUint64(uint64(x))
		return z, nil
	case uint64:
		z := new(big.Int)
		z.SetUint64(x)
		return z, nil
	case string:
		x = strings.TrimSpace(x)
		if x == "" {
			return nil, errors.New("empty numeric string")
		}

		z := new(big.Int)
		if strings.HasPrefix(x, "0x") || strings.HasPrefix(x, "0X") {
			_, ok := z.SetString(x[2:], 16)
			if !ok {
				return nil, fmt.Errorf("invalid hex integer: %s", x)
			}
			return z, nil
		}

		_, ok := z.SetString(x, 10)
		if !ok {
			return nil, fmt.Errorf("invalid integer string: %s", x)
		}

		return z, nil
	default:
		return nil, fmt.Errorf("unsupported numeric type: %T", v)
	}
}

func leftPad64(hexNoPrefix string) string {
	hexNoPrefix = strings.TrimPrefix(strings.ToLower(hexNoPrefix), "0x")
	if hexNoPrefix == "" {
		hexNoPrefix = "0"
	}
	if len(hexNoPrefix) > 64 {
		return hexNoPrefix[len(hexNoPrefix)-64:]
	}
	return strings.Repeat("0", 64-len(hexNoPrefix)) + hexNoPrefix
}

const multicall3Aggregate3ABI = `[
	{
		"inputs": [
			{
				"components": [
					{ "internalType": "address", "name": "target", "type": "address" },
					{ "internalType": "bool", "name": "allowFailure", "type": "bool" },
					{ "internalType": "bytes", "name": "callData", "type": "bytes" }
				],
				"internalType": "struct Multicall3.Call3[]",
				"name": "calls",
				"type": "tuple[]"
			}
		],
		"name": "aggregate3",
		"outputs": [
			{
				"components": [
					{ "internalType": "bool", "name": "success", "type": "bool" },
					{ "internalType": "bytes", "name": "returnData", "type": "bytes" }
				],
				"internalType": "struct Multicall3.Result[]",
				"name": "returnData",
				"type": "tuple[]"
			}
		],
		"stateMutability": "payable",
		"type": "function"
	}
]`

func (c *Client) BalanceOfMulticall(
	ctx context.Context,
	tokenAddr string,
	users []string,
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

	calls := make([]call3, 0, len(users))
	for _, user := range users {
		callDataStr, err := BuildETHFunctionData("balanceOf(address)", user)
		if err != nil {
			return nil, err
		}
		callDataBytes, err := hex.DecodeString(trim0x(callDataStr))
		if err != nil {
			return nil, err
		}

		calls = append(calls, call3{
			Target:       common.HexToAddress(tokenAddr),
			AllowFailure: false,
			CallData:     callDataBytes,
		})
	}

	fmt.Println(calls)

	data, err := parsedABI.Pack("aggregate3", calls)
	if err != nil {
		return nil, err
	}

	callObj := map[string]interface{}{
		"to":   c.multicallAddress,
		"data": "0x" + hex.EncodeToString(data),
	}

	blockTag := c.getBlock()
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
		fmt.Println(r.Success, (r.ReturnData))
		if !r.Success {
			return nil, fmt.Errorf("failed to get balance of %s", users[i])
		}

		out := new(big.Int).SetBytes(r.ReturnData[len(r.ReturnData)-32:])
		balances[i] = out
	}

	return balances, nil
}
