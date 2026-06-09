package monero

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type WalletClient struct {
	URL string
	cli *http.Client
}

type DaemonClient struct {
	URL string
	cli *http.Client
}

func NewWallet(url string) WalletClient {
	return WalletClient{URL: strings.TrimRight(url, "/"), cli: client()}
}

func NewDaemon(url string) DaemonClient {
	return DaemonClient{URL: strings.TrimRight(url, "/"), cli: client()}
}

func client() *http.Client {
	return &http.Client{Timeout: 10 * time.Minute}
}

func (c WalletClient) Call(method string, params any, result any) error {
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      "0",
		"method":  method,
		"params":  params,
	}
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  *RPCError       `json:"error"`
	}
	if err := c.post(c.URL+"/json_rpc", body, &out); err != nil {
		return err
	}
	if out.Error != nil {
		return out.Error
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(out.Result, result)
}

func (c DaemonClient) GetHeight() (uint64, error) {
	var out struct {
		Height uint64 `json:"height"`
		Status string `json:"status"`
	}
	if err := c.post(c.URL+"/get_height", map[string]any{}, &out); err != nil {
		return 0, err
	}
	if out.Status != "OK" {
		return 0, fmt.Errorf("daemon get_height status %q", out.Status)
	}
	return out.Height, nil
}

func (c DaemonClient) GetTransactions(txids []string, decode bool) (GetTransactionsResult, error) {
	var out GetTransactionsResult
	err := c.post(c.URL+"/gettransactions", map[string]any{
		"txs_hashes":     txids,
		"decode_as_json": decode,
	}, &out)
	return out, err
}

func (c DaemonClient) BlockHash(height uint64) (string, error) {
	block, err := c.Block(height)
	if err != nil {
		return "", err
	}
	return block.Hash, nil
}

func (c DaemonClient) Block(height uint64) (Block, error) {
	var out struct {
		Result struct {
			Status      string `json:"status"`
			BlockHeader struct {
				Hash string `json:"hash"`
			} `json:"block_header"`
			TxHashes []string `json:"tx_hashes"`
		} `json:"result"`
		Error *RPCError `json:"error"`
	}
	if err := c.post(c.URL+"/json_rpc", map[string]any{
		"jsonrpc": "2.0",
		"id":      "0",
		"method":  "get_block",
		"params":  map[string]any{"height": height},
	}, &out); err != nil {
		return Block{}, err
	}
	if out.Error != nil {
		return Block{}, out.Error
	}
	if out.Result.Status != "OK" {
		return Block{}, fmt.Errorf("daemon block status %q", out.Result.Status)
	}
	if out.Result.BlockHeader.Hash == "" {
		return Block{}, fmt.Errorf("daemon returned empty block hash at height %d", height)
	}
	return Block{Hash: out.Result.BlockHeader.Hash, TxHashes: out.Result.TxHashes}, nil
}

func (c WalletClient) post(url string, body any, out any) error { return doPost(c.cli, url, body, out) }
func (c DaemonClient) post(url string, body any, out any) error { return doPost(c.cli, url, body, out) }

func doPost(cli *http.Client, url string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := cli.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s: %s", resp.Status, string(data))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode rpc response: %w: %s", err, string(data))
	}
	return nil
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

type Transfer struct {
	TxID          string `json:"txid"`
	Height        uint64 `json:"height"`
	Timestamp     uint64 `json:"timestamp"`
	Amount        uint64 `json:"amount"`
	Confirmations uint64 `json:"confirmations"`
	Locked        bool   `json:"locked"`
	Type          string `json:"type"`
}

type GetTransfersResult struct {
	In []Transfer `json:"in"`
}

type Block struct {
	Hash     string
	TxHashes []string
}

type GetTransactionsResult struct {
	Status string `json:"status"`
	Txs    []Tx   `json:"txs"`
}

type Tx struct {
	Hash        string `json:"tx_hash"`
	AsJSON      string `json:"as_json"`
	InPool      bool   `json:"in_pool"`
	Relayed     bool   `json:"relayed"`
	BlockHeight uint64 `json:"block_height"`
}

type DecodedTx struct {
	Extra []byte `json:"-"`
}

func DecodeExtra(asJSON string) ([]byte, error) {
	var raw struct {
		Extra []int `json:"extra"`
	}
	if err := json.Unmarshal([]byte(asJSON), &raw); err != nil {
		return nil, err
	}
	extra := make([]byte, len(raw.Extra))
	for i, n := range raw.Extra {
		if n < 0 || n > 255 {
			return nil, fmt.Errorf("extra byte out of range at %d", i)
		}
		extra[i] = byte(n)
	}
	return extra, nil
}
