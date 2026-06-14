package claim

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"xns/pkg/monero"
	"xns/pkg/xns"
)

type Config struct {
	Mainnet           bool
	Stagenet          bool
	WalletFile        string
	WalletPassword    string
	WalletPasswordSet bool
	Name              string
	Owner             string
	Node              string
	Years             uint64
}

type Result struct {
	Network      string   `json:"network"`
	Name         string   `json:"name"`
	Owner        string   `json:"owner"`
	Years        uint64   `json:"years"`
	AmountAtomic uint64   `json:"amount_atomic"`
	TxHashList   []string `json:"tx_hash_list"`
}

type rpcProcess struct {
	cmd         *exec.Cmd
	log         *os.File
	logPath     string
	rpcUser     string
	rpcPassword string
	done        chan struct{}
	stopOnce    sync.Once
}

func Run(cfg Config) (Result, error) {
	if cfg.Mainnet == cfg.Stagenet {
		return Result{}, errors.New("specify exactly one of mainnet or stagenet")
	}
	if cfg.WalletFile == "" {
		return Result{}, errors.New("wallet file is required")
	}
	if !cfg.WalletPasswordSet {
		return Result{}, errors.New("wallet password must be specified")
	}
	if cfg.Years == 0 {
		return Result{}, errors.New("years must be at least 1")
	}
	if cfg.Node == "" {
		return Result{}, errors.New("node is required")
	}
	payload, err := xns.BuildPayload(cfg.Name, cfg.Owner)
	if err != nil {
		return Result{}, err
	}
	return run(cfg, payload)
}

func PrintJSON(r Result) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(raw))
	return nil
}

func run(cfg Config, payload [xns.PayloadSize]byte) (Result, error) {
	workDir, err := os.MkdirTemp("", "xns-claim-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(workDir)

	fullPort, err := freePort()
	if err != nil {
		return Result{}, err
	}
	watchPort, err := freePort()
	if err != nil {
		return Result{}, err
	}
	fullProc, err := startWalletRPC(cfg, fullPort, filepath.Join(workDir, "full-wallet-rpc.log"), cfg.WalletFile, "", cfg.WalletPassword)
	if err != nil {
		return Result{}, err
	}
	defer fullProc.stop()
	full := monero.NewWalletWithLogin(fmt.Sprintf("http://127.0.0.1:%d", fullPort), fullProc.rpcUser, fullProc.rpcPassword)
	if err := waitWallet(full, fullProc, "full"); err != nil {
		return Result{}, err
	}

	log.Print("refreshing wallet")
	if err := full.Call("auto_refresh", map[string]any{"enable": false}, nil); err != nil {
		return Result{}, err
	}
	if err := full.Call("refresh", map[string]any{}, nil); err != nil {
		return Result{}, err
	}
	address, viewSecret, err := sender(full)
	if err != nil {
		return Result{}, err
	}
	fullState, err := readWalletState(full)
	if err != nil {
		return Result{}, err
	}
	var keyImages struct {
		Offset          uint64 `json:"offset"`
		SignedKeyImages []any  `json:"signed_key_images"`
	}
	if err := full.Call("export_key_images", map[string]any{"all": true}, &keyImages); err != nil {
		return Result{}, err
	}
	if err := full.Call("store", map[string]any{}, nil); err != nil {
		return Result{}, err
	}

	log.Print("preparing claim transaction")
	watchDir := filepath.Join(workDir, "watch")
	if err := os.MkdirAll(watchDir, 0o700); err != nil {
		return Result{}, err
	}
	watchLog := filepath.Join(workDir, "watch-wallet-rpc.log")
	watchProc, err := startWalletRPC(cfg, watchPort, watchLog, "", watchDir, "")
	if err != nil {
		return Result{}, err
	}
	watch := monero.NewWalletWithLogin(fmt.Sprintf("http://127.0.0.1:%d", watchPort), watchProc.rpcUser, watchProc.rpcPassword)
	if err := waitWallet(watch, watchProc, "watch"); err != nil {
		watchProc.stop()
		return Result{}, err
	}
	watchFile := filepath.Join(watchDir, "sender_watch")
	if err := watch.Call("generate_from_keys", map[string]any{
		"restore_height": fullState.Height,
		"filename":       "sender_watch",
		"address":        address,
		"viewkey":        viewSecret,
		"password":       cfg.WalletPassword,
		"language":       "English",
	}, nil); err != nil {
		watchProc.stop()
		return Result{}, err
	}
	if err := watch.Call("store", map[string]any{}, nil); err != nil {
		watchProc.stop()
		return Result{}, err
	}
	watchProc.stop()

	if err := copyWalletCache(cfg.WalletFile, watchFile); err != nil {
		return Result{}, err
	}
	watchProc, err = startWalletRPC(cfg, watchPort, watchLog, watchFile, "", cfg.WalletPassword)
	if err != nil {
		return Result{}, err
	}
	defer watchProc.stop()
	watch = monero.NewWalletWithLogin(fmt.Sprintf("http://127.0.0.1:%d", watchPort), watchProc.rpcUser, watchProc.rpcPassword)
	if err := waitWallet(watch, watchProc, "watch"); err != nil {
		return Result{}, err
	}
	if err := watch.Call("auto_refresh", map[string]any{"enable": false}, nil); err != nil {
		return Result{}, err
	}
	watchAddress, _, err := sender(watch)
	if err != nil {
		return Result{}, err
	}
	if watchAddress != address {
		return Result{}, errors.New("temporary watch wallet address does not match full wallet")
	}
	if err := watch.Call("import_key_images", map[string]any{
		"offset":            keyImages.Offset,
		"signed_key_images": keyImages.SignedKeyImages,
	}, nil); err != nil {
		return Result{}, err
	}
	watchState, err := readWalletState(watch)
	if err != nil {
		return Result{}, err
	}
	if watchState != fullState {
		return Result{}, fmt.Errorf(
			"temporary watch wallet state does not match full wallet: full height=%d balance=%d unlocked=%d, watch height=%d balance=%d unlocked=%d",
			fullState.Height, fullState.Balance, fullState.UnlockedBalance,
			watchState.Height, watchState.Balance, watchState.UnlockedBalance,
		)
	}

	amount := cfg.Years * xns.YearAmount
	var transfer struct {
		UnsignedTxSet string `json:"unsigned_txset"`
	}
	if err := watch.Call("transfer", map[string]any{
		"destinations": []map[string]any{{
			"address": xns.ProtocolAddress(cfg.Stagenet),
			"amount":  amount,
		}},
		"account_index":   0,
		"subaddr_indices": []uint64{},
		"priority":        2,
		"ring_size":       0,
		"unlock_time":     0,
		"get_tx_key":      false,
		"do_not_relay":    true,
		"get_tx_hex":      false,
		"get_tx_metadata": false,
	}, &transfer); err != nil {
		return Result{}, err
	}
	if transfer.UnsignedTxSet == "" {
		return Result{}, errors.New("wallet did not return unsigned_txset")
	}
	patched, _, err := monero.PatchUnsignedTxSetHex(transfer.UnsignedTxSet, viewSecret, payload)
	if err != nil {
		return Result{}, err
	}

	log.Print("signing and broadcasting")
	var signed struct {
		SignedTxSet string   `json:"signed_txset"`
		TxHashList  []string `json:"tx_hash_list"`
	}
	if err := full.Call("sign_transfer", map[string]any{
		"unsigned_txset": patched,
		"export_raw":     true,
		"get_tx_keys":    false,
	}, &signed); err != nil {
		return Result{}, err
	}
	if err := full.Call("submit_transfer", map[string]any{"tx_data_hex": signed.SignedTxSet}, nil); err != nil {
		return Result{}, err
	}
	return Result{
		Network:      xns.NetworkName(cfg.Stagenet),
		Name:         cfg.Name,
		Owner:        cfg.Owner,
		Years:        cfg.Years,
		AmountAtomic: amount,
		TxHashList:   signed.TxHashList,
	}, nil
}

func startWalletRPC(cfg Config, port int, logPath, walletFile, walletDir, password string) (*rpcProcess, error) {
	rpcPassword, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	configPath := logPath + ".conf"
	var passwordFile string
	if walletFile != "" {
		passwordFile = logPath + ".wallet-password"
		if err := writePrivateFile(passwordFile, []byte(password)); err != nil {
			return nil, err
		}
	}
	if err := writeWalletRPCConfig(configPath, "xns", rpcPassword, passwordFile); err != nil {
		return nil, err
	}
	args := []string{
		"--config-file", configPath,
		"--rpc-bind-ip", "127.0.0.1",
		"--rpc-bind-port", strconv.Itoa(port),
		"--non-interactive",
		"--no-initial-sync",
		"--log-file", logPath,
		"--log-level", "0",
		"--daemon-address", cfg.Node,
		"--trusted-daemon",
	}
	if cfg.Stagenet {
		args = append(args, "--stagenet")
	}
	if walletFile != "" {
		args = append(args, "--wallet-file", walletFile)
	}
	if walletDir != "" {
		args = append(args, "--wallet-dir", walletDir)
	}
	f, err := os.OpenFile(logPath+".stdout", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("monero-wallet-rpc", args...)
	cmd.Stdout = f
	cmd.Stderr = f
	if err := cmd.Start(); err != nil {
		f.Close()
		return nil, err
	}
	p := &rpcProcess{cmd: cmd, log: f, logPath: logPath + ".stdout", rpcUser: "xns", rpcPassword: rpcPassword, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

func writeWalletRPCConfig(path, rpcUser, rpcPassword, passwordFile string) error {
	var b strings.Builder
	b.WriteString("rpc-login=")
	b.WriteString(rpcUser)
	b.WriteByte(':')
	b.WriteString(rpcPassword)
	b.WriteByte('\n')
	if passwordFile != "" {
		b.WriteString("password-file=")
		b.WriteString(filepath.ToSlash(passwordFile))
		b.WriteByte('\n')
	}
	return writePrivateFile(path, []byte(b.String()))
}

func writePrivateFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (p *rpcProcess) stop() {
	if p == nil || p.cmd == nil {
		return
	}
	p.stopOnce.Do(func() {
		select {
		case <-p.done:
		default:
			_ = p.cmd.Process.Signal(os.Interrupt)
		}
		select {
		case <-p.done:
		case <-time.After(10 * time.Second):
			_ = p.cmd.Process.Kill()
			<-p.done
		}
		if p.log != nil {
			_ = p.log.Close()
		}
	})
}

func waitWallet(rpc monero.WalletClient, proc *rpcProcess, label string) error {
	deadline := time.Now().Add(30 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		select {
		case <-proc.done:
			return fmt.Errorf("%s wallet-rpc exited early; see %s", label, proc.logPath)
		default:
		}
		if err := rpc.Call("get_version", map[string]any{}, nil); err == nil {
			return nil
		} else {
			last = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("%s wallet-rpc did not become ready: %w", label, last)
}

func sender(full monero.WalletClient) (string, string, error) {
	var address struct {
		Address string `json:"address"`
	}
	if err := full.Call("get_address", map[string]any{"account_index": 0}, &address); err != nil {
		return "", "", err
	}
	var view struct {
		Key string `json:"key"`
	}
	if err := full.Call("query_key", map[string]any{"key_type": "view_key"}, &view); err != nil {
		return "", "", err
	}
	if len(view.Key) != 64 {
		return "", "", errors.New("wallet returned invalid view key")
	}
	return address.Address, view.Key, nil
}

type walletState struct {
	Height          uint64
	Balance         uint64
	UnlockedBalance uint64
}

func readWalletState(wallet monero.WalletClient) (walletState, error) {
	var height struct {
		Height uint64 `json:"height"`
	}
	if err := wallet.Call("get_height", map[string]any{}, &height); err != nil {
		return walletState{}, err
	}
	var balance struct {
		Balance         uint64 `json:"balance"`
		UnlockedBalance uint64 `json:"unlocked_balance"`
	}
	if err := wallet.Call("get_balance", map[string]any{
		"account_index": 0,
		"all_accounts":  false,
		"strict":        true,
	}, &balance); err != nil {
		return walletState{}, err
	}
	return walletState{
		Height:          height.Height,
		Balance:         balance.Balance,
		UnlockedBalance: balance.UnlockedBalance,
	}, nil
}

func copyWalletCache(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open full wallet cache: %w", err)
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat full wallet cache: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("full wallet cache is not a regular file")
	}

	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open temporary watch wallet cache: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy full wallet cache: %w", err)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return fmt.Errorf("sync temporary watch wallet cache: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close temporary watch wallet cache: %w", err)
	}
	return nil
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}
