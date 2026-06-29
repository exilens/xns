package monero

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type WalletClient struct {
	URL  string
	cli  *http.Client
	auth *digestAuth
}

type DaemonClient struct {
	URL string
	cli *http.Client
}

func NewWallet(url string) WalletClient {
	return WalletClient{URL: strings.TrimRight(url, "/"), cli: client()}
}

func NewWalletWithLogin(url, username, password string) WalletClient {
	return WalletClient{
		URL:  strings.TrimRight(url, "/"),
		cli:  client(),
		auth: &digestAuth{Username: username, Password: password},
	}
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

func (c WalletClient) post(url string, body any, out any) error {
	return doPost(c.cli, url, body, out, c.auth)
}
func (c DaemonClient) post(url string, body any, out any) error {
	return doPost(c.cli, url, body, out, nil)
}

func doPost(cli *http.Client, rawURL string, body any, out any, auth *digestAuth) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, data, err := postJSON(cli, rawURL, raw, "")
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized && auth != nil {
		authz, err := auth.authorization("POST", rawURL, resp.Header.Get("WWW-Authenticate"))
		if err != nil {
			return err
		}
		resp, data, err = postJSON(cli, rawURL, raw, authz)
		if err != nil {
			return err
		}
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

func postJSON(cli *http.Client, rawURL string, body []byte, authorization string) (*http.Response, []byte, error) {
	req, err := http.NewRequest(http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	return resp, data, nil
}

type digestAuth struct {
	Username string
	Password string

	mu sync.Mutex
	nc uint32
}

func (a *digestAuth) authorization(method, rawURL, challenge string) (string, error) {
	fields, err := parseDigestChallenge(challenge)
	if err != nil {
		return "", err
	}
	realm := fields["realm"]
	nonce := fields["nonce"]
	if realm == "" || nonce == "" {
		return "", fmt.Errorf("digest challenge missing realm or nonce")
	}
	algorithm := strings.ToUpper(fields["algorithm"])
	if algorithm != "" && algorithm != "MD5" {
		return "", fmt.Errorf("unsupported digest algorithm %q", fields["algorithm"])
	}
	qop := "auth"
	if values := strings.Split(fields["qop"], ","); fields["qop"] != "" {
		qop = ""
		for _, value := range values {
			if strings.TrimSpace(value) == "auth" {
				qop = "auth"
				break
			}
		}
		if qop == "" {
			return "", fmt.Errorf("digest challenge does not support qop=auth")
		}
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	uri := parsed.RequestURI()
	cnonce, err := randomHex(16)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.nc++
	nc := fmt.Sprintf("%08x", a.nc)
	a.mu.Unlock()

	ha1 := md5Hex(a.Username + ":" + realm + ":" + a.Password)
	ha2 := md5Hex(method + ":" + uri)
	var response string
	if qop == "" {
		response = md5Hex(ha1 + ":" + nonce + ":" + ha2)
	} else {
		response = md5Hex(ha1 + ":" + nonce + ":" + nc + ":" + cnonce + ":" + qop + ":" + ha2)
	}

	parts := []string{
		`Digest username=` + strconv.Quote(a.Username),
		`realm=` + strconv.Quote(realm),
		`nonce=` + strconv.Quote(nonce),
		`uri=` + strconv.Quote(uri),
		`response=` + strconv.Quote(response),
	}
	if opaque := fields["opaque"]; opaque != "" {
		parts = append(parts, `opaque=`+strconv.Quote(opaque))
	}
	if qop != "" {
		parts = append(parts, `qop=`+qop, `nc=`+nc, `cnonce=`+strconv.Quote(cnonce))
	}
	if algorithm != "" {
		parts = append(parts, `algorithm=MD5`)
	}
	return strings.Join(parts, ", "), nil
}

func parseDigestChallenge(challenge string) (map[string]string, error) {
	challenge = strings.TrimSpace(challenge)
	if !strings.HasPrefix(strings.ToLower(challenge), "digest ") {
		return nil, fmt.Errorf("unsupported authentication challenge")
	}
	challenge = strings.TrimSpace(challenge[len("Digest "):])
	out := make(map[string]string)
	for len(challenge) > 0 {
		challenge = strings.TrimLeft(challenge, " ,")
		if challenge == "" {
			break
		}
		eq := strings.IndexByte(challenge, '=')
		if eq < 0 {
			return nil, fmt.Errorf("malformed digest challenge")
		}
		key := strings.ToLower(strings.TrimSpace(challenge[:eq]))
		challenge = strings.TrimSpace(challenge[eq+1:])
		var value string
		if strings.HasPrefix(challenge, `"`) {
			var err error
			value, challenge, err = readQuoted(challenge)
			if err != nil {
				return nil, err
			}
		} else {
			next := strings.IndexByte(challenge, ',')
			if next < 0 {
				value = strings.TrimSpace(challenge)
				challenge = ""
			} else {
				value = strings.TrimSpace(challenge[:next])
				challenge = challenge[next+1:]
			}
		}
		out[key] = value
	}
	return out, nil
}

func readQuoted(s string) (string, string, error) {
	for i := 1; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '"' {
			value, err := strconv.Unquote(s[:i+1])
			if err != nil {
				return "", "", err
			}
			return value, s[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("unterminated quoted digest value")
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
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
