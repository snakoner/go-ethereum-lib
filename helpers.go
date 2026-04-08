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

	"github.com/ethereum/go-ethereum/common"
	"golang.org/x/crypto/sha3"
)

func (c *Client) EstimateGas(ctx context.Context, callObj map[string]interface{}) (*big.Int, error) {
	var gasHex string
	if err := c.rpcCall(ctx, "eth_estimateGas", []interface{}{callObj}, &gasHex); err != nil {
		return nil, err
	}

	return parseHexBigInt(gasHex)
}

func (c *Client) GetNonce(ctx context.Context, account string) (*big.Int, error) {
	var nonceHex string
	if err := c.rpcCall(ctx, "eth_getTransactionCount", []interface{}{account, "pending"}, &nonceHex); err != nil {
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

func (c *Client) getCurrentBlock(ctx context.Context) (*big.Int, error) {
	var blockHex string
	if err := c.rpcCall(ctx, "eth_blockNumber", []interface{}{}, &blockHex); err != nil {
		return nil, err
	}

	return parseHexBigInt(blockHex)
}

func (c *Client) getBlock(ctx context.Context) (string, error) {
	if c.confirmations > 0 {
		currentBlock, err := c.getCurrentBlock(ctx)
		if err != nil {
			return "", err
		}

		return fmt.Sprintf("0x%x", currentBlock.Sub(currentBlock, big.NewInt(c.confirmations))), nil
	}

	return "latest", nil
}

func (c *Client) rpcCall(ctx context.Context, method string, params []interface{}, out interface{}) error {
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

	paramHex, err := buildETHABIParams(functionSig, params...)
	if err != nil {
		return "", err
	}

	selector := methodSelector(functionSig)
	return "0x" + hex.EncodeToString(selector) + paramHex, nil
}

func buildETHABIParams(functionSig string, params ...any) (string, error) {
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
	case "int128":
		return encodeETHABIInt128(v)
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

func encodeETHABIInt128(v any) (string, error) {
	n, err := toBigInt(v)
	if err != nil {
		return "", err
	}

	limit := new(big.Int).Lsh(big.NewInt(1), 127)

	if n.Cmp(limit) >= 0 || n.Cmp(new(big.Int).Neg(limit)) < 0 {
		return "", errors.New("int128 overflow")
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
