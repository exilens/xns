package indexer

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/exilens/xns/pkg/monero"
	"github.com/exilens/xns/pkg/store"
	"github.com/exilens/xns/pkg/xns"

	"golang.org/x/crypto/sha3"
)

type Config struct {
	Mainnet  bool
	Stagenet bool
	Node     string
	Listen   string
	DataDir  string
}

type State struct {
	Height  uint64               `json:"height"`
	Events  []xns.Event          `json:"events"`
	Names   map[string]xns.Entry `json:"names"`
	Updated time.Time            `json:"updated"`
}

type lookupResponse struct {
	Found            bool     `json:"found"`
	Name             string   `json:"name"`
	OwnerKey         string   `json:"owner_key,omitempty"`
	ExpirationHeight uint64   `json:"expiration_height,omitempty"`
	RemainingBlocks  uint64   `json:"remaining_blocks,omitempty"`
	Finalized        bool     `json:"finalized"`
	SourceTxIDs      []string `json:"source_txids,omitempty"`
}

type Server struct {
	cfg    Config
	wallet monero.WalletClient
	daemon monero.DaemonClient
	db     *store.DB

	mu     sync.RWMutex
	state  State
	ready  bool
	scanMu sync.Mutex

	walletRPC *rpcProcess
}

type rpcProcess struct {
	cmd      *exec.Cmd
	log      *os.File
	logPath  string
	done     chan struct{}
	stopOnce sync.Once
}

func Run(cfg Config) error {
	if cfg.Mainnet == cfg.Stagenet {
		return errors.New("specify exactly one of mainnet or stagenet")
	}
	if cfg.Node == "" {
		return errors.New("node is required")
	}
	if cfg.Listen == "" {
		return errors.New("listen address is required")
	}
	if cfg.DataDir == "" {
		return errors.New("data directory is required")
	}
	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()
	wallet, proc, err := protocolWallet(cfg)
	if err != nil {
		return err
	}
	defer proc.stop()
	db, err := store.Open(filepath.Join(cfg.DataDir, "xns.db"))
	if err != nil {
		return err
	}
	daemon := monero.NewDaemon(cfg.Node)
	snap, err := db.Load()
	if err != nil {
		_ = db.Close()
		return err
	}
	staleDB := snap.ProtocolAddress != xns.ProtocolAddress(cfg.Stagenet) &&
		(snap.ProtocolAddress != "" || snap.Height != 0 || len(snap.Names) != 0 || len(snap.Events) != 0)
	if staleDB {
		_ = db.Close()
		if err := removeDBFiles(filepath.Join(cfg.DataDir, "xns.db")); err != nil {
			return err
		}
		db, err = store.Open(filepath.Join(cfg.DataDir, "xns.db"))
		if err != nil {
			return err
		}
		snap = store.Snapshot{Names: make(map[string]xns.Entry)}
	}
	defer db.Close()
	if ok, err := snapshotMatchesChain(snap, daemon); err != nil {
		log.Printf("stored state not served: %v", err)
		snap = store.Snapshot{Names: make(map[string]xns.Entry)}
	} else if !ok {
		log.Print("stored state not served: durable block hash changed")
		snap = store.Snapshot{Names: make(map[string]xns.Entry)}
	}

	s := &Server{
		cfg:    cfg,
		wallet: wallet,
		daemon: daemon,
		db:     db,
		state:  State{Height: snap.Height, Events: snap.Events, Names: snap.Names, Updated: time.Now().UTC()},

		walletRPC: proc,
	}
	defer func() {
		s.walletRPC.stop()
	}()

	var scans sync.WaitGroup
	startScan := func() {
		scans.Add(1)
		go func() {
			defer scans.Done()
			if err := s.scanAndStore(); err != nil {
				log.Printf("scan: %v", err)
			}
		}()
	}
	startScan()
	go s.loop(ctx, startScan)

	mux := http.NewServeMux()
	mux.HandleFunc("/lookup", s.handleLookup)
	httpServer := &http.Server{Addr: cfg.Listen, Handler: mux}
	errs := make(chan error, 1)
	go func() {
		log.Printf("indexer listening on %s", cfg.Listen)
		errs <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errs:
		cancel()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		proc.stop()
		scans.Wait()
		return err
	case <-ctx.Done():
		proc.stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
		scans.Wait()
		return nil
	}
}

func snapshotMatchesChain(snap store.Snapshot, daemon monero.DaemonClient) (bool, error) {
	empty := snap.Height == 0 && len(snap.Names) == 0 && len(snap.Events) == 0
	if empty {
		return true, nil
	}
	if snap.Height == 0 || snap.BlockHash == "" {
		return false, errors.New("stored state has no durable block anchor")
	}
	hash, err := daemon.BlockHash(snap.Height)
	if err != nil {
		return false, err
	}
	return hash == snap.BlockHash, nil
}

func protocolWallet(cfg Config) (monero.WalletClient, *rpcProcess, error) {
	walletDir := filepath.Join(cfg.DataDir, "wallet")
	if err := os.MkdirAll(walletDir, 0o700); err != nil {
		return monero.WalletClient{}, nil, err
	}
	walletFile := filepath.Join(walletDir, "xns_protocol_view")
	port, err := freePort()
	if err != nil {
		return monero.WalletClient{}, nil, err
	}
	var proc *rpcProcess
	if _, err := os.Stat(walletFile + ".keys"); err == nil {
		proc, err = startWalletRPC(cfg, port, filepath.Join(cfg.DataDir, "wallet-rpc.log"), walletFile, "")
	} else {
		proc, err = startWalletRPC(cfg, port, filepath.Join(cfg.DataDir, "wallet-rpc.log"), "", walletDir)
	}
	if err != nil {
		return monero.WalletClient{}, nil, err
	}
	wallet := monero.NewWallet(fmt.Sprintf("http://127.0.0.1:%d", port))
	if err := waitWallet(wallet, proc); err != nil {
		proc.stop()
		return monero.WalletClient{}, nil, err
	}
	if _, err := os.Stat(walletFile + ".keys"); err == nil {
		ok, err := walletAddressMatches(wallet, xns.ProtocolAddress(cfg.Stagenet))
		if err != nil {
			proc.stop()
			return monero.WalletClient{}, nil, err
		}
		if !ok {
			proc.stop()
			if err := removeWalletFiles(walletFile); err != nil {
				return monero.WalletClient{}, nil, err
			}
			return protocolWallet(cfg)
		}
	}
	if _, err := os.Stat(walletFile + ".keys"); os.IsNotExist(err) {
		if err := wallet.Call("generate_from_keys", map[string]any{
			"restore_height": xns.ProtocolRestoreHeight(cfg.Stagenet),
			"filename":       "xns_protocol_view",
			"address":        xns.ProtocolAddress(cfg.Stagenet),
			"viewkey":        xns.ProtocolViewSecret,
			"password":       "",
			"language":       "English",
		}, nil); err != nil {
			proc.stop()
			return monero.WalletClient{}, nil, err
		}
	}
	return wallet, proc, nil
}

func walletAddressMatches(wallet monero.WalletClient, want string) (bool, error) {
	var out struct {
		Address string `json:"address"`
	}
	if err := wallet.Call("get_address", map[string]any{"account_index": 0}, &out); err != nil {
		return false, err
	}
	return out.Address == want, nil
}

func removeWalletFiles(walletFile string) error {
	for _, path := range []string{walletFile, walletFile + ".keys", walletFile + ".address.txt"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func removeDBFiles(dbFile string) error {
	for _, path := range []string{dbFile, dbFile + "-wal", dbFile + "-shm"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func startWalletRPC(cfg Config, port int, logPath, walletFile, walletDir string) (*rpcProcess, error) {
	args := []string{
		"--rpc-bind-ip", "127.0.0.1",
		"--rpc-bind-port", strconv.Itoa(port),
		"--disable-rpc-login",
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
		args = append(args, "--wallet-file", walletFile, "--password", "")
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
	p := &rpcProcess{cmd: cmd, log: f, logPath: logPath + ".stdout", done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()
	return p, nil
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

func waitWallet(rpc monero.WalletClient, proc *rpcProcess) error {
	deadline := time.Now().Add(30 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		select {
		case <-proc.done:
			return fmt.Errorf("wallet-rpc exited early; see %s", proc.logPath)
		default:
		}
		if err := rpc.Call("get_version", map[string]any{}, nil); err == nil {
			return nil
		} else {
			last = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("wallet-rpc did not become ready: %w", last)
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func (s *Server) loop(ctx context.Context, scan func()) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scan()
		}
	}
}

func (s *Server) scanAndStore() error {
	if !s.scanMu.TryLock() {
		log.Print("scan: skipped, previous scan still running")
		return nil
	}
	defer s.scanMu.Unlock()
	start := time.Now()
	log.Print("scan: starting")
	visible, durable, durableHash, err := s.scan()
	if err != nil {
		return err
	}
	if err := s.db.Save(store.Snapshot{
		Height:          durable.Height,
		BlockHash:       durableHash,
		ProtocolAddress: xns.ProtocolAddress(s.cfg.Stagenet),
		Names:           durable.Names,
		Events:          durable.Events,
	}); err != nil {
		return err
	}
	s.mu.Lock()
	s.state = visible
	s.ready = true
	s.mu.Unlock()
	log.Printf("scan: done in %s, visible_names=%d durable_names=%d", time.Since(start).Round(time.Second), len(visible.Names), len(durable.Names))
	return nil
}

func (s *Server) scan() (State, State, string, error) {
	visible, durable, hash, err := s.scanOnce()
	if err == nil || !isDeepReorg(err) {
		return visible, durable, hash, err
	}
	log.Print("scan: deep reorg detected, rebuilding protocol wallet cache")
	s.mu.Lock()
	s.ready = false
	s.mu.Unlock()
	if err := s.rebuildWallet(); err != nil {
		return State{}, State{}, "", err
	}
	return s.scanOnce()
}

func (s *Server) scanOnce() (State, State, string, error) {
	log.Print("scan: refreshing protocol wallet")
	if err := s.wallet.Call("refresh", map[string]any{}, nil); err != nil {
		return State{}, State{}, "", err
	}
	walletHeight, err := s.walletHeight()
	if err != nil {
		return State{}, State{}, "", err
	}
	daemonHeight, err := s.daemon.GetHeight()
	if err != nil {
		return State{}, State{}, "", err
	}
	if walletHeight == 0 || walletHeight > daemonHeight {
		return State{}, State{}, "", fmt.Errorf("wallet height %d is inconsistent with daemon height %d", walletHeight, daemonHeight)
	}
	tipHeight := walletHeight - 1
	tipHash, err := s.daemon.BlockHash(tipHeight)
	if err != nil {
		return State{}, State{}, "", err
	}
	log.Print("scan: loading wallet transfers")
	var tr monero.GetTransfersResult
	err = s.wallet.Call("get_transfers", map[string]any{
		"in":            true,
		"out":           false,
		"pending":       false,
		"failed":        false,
		"pool":          false,
		"account_index": 0,
	}, &tr)
	if err != nil {
		return State{}, State{}, "", err
	}
	log.Printf("scan: found %d incoming transfers", len(tr.In))
	log.Print("scan: ordering transfers")
	blocks, err := s.sortTransfers(tr.In, walletHeight)
	if err != nil {
		return State{}, State{}, "", err
	}
	visibleReg := xns.NewRegistry()
	durableReg := xns.NewRegistry()
	var visibleEvents, durableEvents []xns.Event
	durable := durableHeight(walletHeight, 10)

	log.Print("scan: replaying transfers")
	for _, t := range tr.In {
		payload, invalid, err := s.payloadForTx(t)
		if err != nil {
			return State{}, State{}, "", err
		}
		ev := visibleReg.Apply(t.TxID, t.Height, t.Amount, payload, invalid)
		visibleEvents = append(visibleEvents, ev)
		if t.Height <= durable {
			dev := durableReg.Apply(t.TxID, t.Height, t.Amount, payload, invalid)
			durableEvents = append(durableEvents, dev)
		}
	}

	durableHash := ""
	if durable > 0 {
		if block, ok := blocks[durable]; ok {
			durableHash = block.Hash
		} else {
			durableHash, err = s.daemon.BlockHash(durable)
			if err != nil {
				return State{}, State{}, "", err
			}
		}
	}
	endTipHash, err := s.daemon.BlockHash(tipHeight)
	if err != nil {
		return State{}, State{}, "", err
	}
	if endTipHash != tipHash {
		return State{}, State{}, "", errors.New("chain changed during scan")
	}
	now := time.Now().UTC()
	return State{Height: walletHeight, Events: visibleEvents, Names: visibleReg.Names, Updated: now},
		State{Height: durable, Events: durableEvents, Names: durableReg.Names, Updated: now},
		durableHash, nil
}

func isDeepReorg(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "reorg exceeds maximum allowed depth")
}

func (s *Server) rebuildWallet() error {
	s.walletRPC.stop()
	walletFile := filepath.Join(s.cfg.DataDir, "wallet", "xns_protocol_view")
	if err := removeWalletFiles(walletFile); err != nil {
		return err
	}
	wallet, proc, err := protocolWallet(s.cfg)
	if err != nil {
		return err
	}
	s.wallet = wallet
	s.walletRPC = proc
	return nil
}

func (s *Server) walletHeight() (uint64, error) {
	var out struct {
		Height uint64 `json:"height"`
	}
	if err := s.wallet.Call("get_height", map[string]any{}, &out); err != nil {
		return 0, err
	}
	return out.Height, nil
}

func (s *Server) sortTransfers(transfers []monero.Transfer, walletHeight uint64) (map[uint64]monero.Block, error) {
	blocks := make(map[uint64]monero.Block)
	keys := make(map[string][]byte)
	for _, t := range transfers {
		if t.Height == 0 || t.Height >= walletHeight {
			return nil, fmt.Errorf("transfer %s has invalid height %d at wallet height %d", t.TxID, t.Height, walletHeight)
		}
		block, ok := blocks[t.Height]
		if !ok {
			var err error
			block, err = s.daemon.Block(t.Height)
			if err != nil {
				return nil, err
			}
			blocks[t.Height] = block
		}
		if !contains(block.TxHashes, t.TxID) {
			return nil, fmt.Errorf("transfer %s is not in canonical block %d", t.TxID, t.Height)
		}
		raw, err := hex.DecodeString(block.Hash)
		if err != nil {
			return nil, err
		}
		if _, ok := keys[t.TxID]; !ok {
			keys[t.TxID] = sameBlockKey(raw, t.TxID)
		}
	}
	sort.Slice(transfers, func(i, j int) bool {
		if transfers[i].Height != transfers[j].Height {
			return transfers[i].Height < transfers[j].Height
		}
		cmp := bytes.Compare(keys[transfers[i].TxID], keys[transfers[j].TxID])
		if cmp != 0 {
			return cmp < 0
		}
		return transfers[i].TxID < transfers[j].TxID
	})
	return blocks, nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sameBlockKey(blockHash []byte, txid string) []byte {
	txHash, err := hex.DecodeString(txid)
	if err != nil {
		txHash = []byte(txid)
	}
	h := sha3.NewLegacyKeccak256()
	h.Write(blockHash)
	h.Write(txHash)
	return h.Sum(nil)
}

func (s *Server) payloadForTx(transfer monero.Transfer) (*xns.Payload, string, error) {
	txs, err := s.daemon.GetTransactions([]string{transfer.TxID}, true)
	if err != nil {
		return nil, "", err
	}
	if txs.Status != "OK" {
		return nil, "", fmt.Errorf("daemon returned %s for transaction %s", txs.Status, transfer.TxID)
	}
	if len(txs.Txs) != 1 {
		return nil, "", fmt.Errorf("daemon returned %d transactions for %s", len(txs.Txs), transfer.TxID)
	}
	tx := txs.Txs[0]
	if tx.Hash != transfer.TxID || tx.InPool || tx.BlockHeight != transfer.Height {
		return nil, "", fmt.Errorf("transaction %s does not match canonical wallet transfer", transfer.TxID)
	}
	extra, err := monero.DecodeExtra(tx.AsJSON)
	if err != nil {
		return nil, err.Error(), nil
	}
	payloads, err := xns.ExtractPayloads(extra)
	if err != nil {
		return nil, err.Error(), nil
	}
	if len(payloads) == 0 {
		return nil, "missing XNS payload", nil
	}
	if len(payloads) > 1 {
		return nil, "multiple XNS payloads", nil
	}
	return &payloads[0], "", nil
}

func durableHeight(height, confs uint64) uint64 {
	if height <= confs {
		return 0
	}
	return height - confs
}

func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/lookup" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if err := xns.ValidName(name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.RLock()
	ready := s.ready
	entry, ok := s.state.Names[name]
	height := s.state.Height
	s.mu.RUnlock()
	if !ready {
		writeError(w, http.StatusServiceUnavailable, "indexer is synchronizing")
		return
	}
	if !ok || entry.ExpirationHeight <= height {
		writeJSON(w, lookupResponse{Found: false, Name: name})
		return
	}
	writeJSON(w, lookupResponse{
		Found:            true,
		Name:             name,
		OwnerKey:         entry.OwnerKey,
		ExpirationHeight: entry.ExpirationHeight,
		RemainingBlocks:  entry.ExpirationHeight - height,
		Finalized:        height-entry.LastUpdateHeight >= 10,
		SourceTxIDs:      entry.SourceTxIDs,
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, map[string]string{"error": msg})
}
