package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xns/pkg/claim"
)

func TestResolveWalletPasswordFromFlag(t *testing.T) {
	fs, cfg := passwordFlagSet("-wallet-password", "secret")
	if err := resolveWalletPassword(fs, cfg, "", false, strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
	if !cfg.WalletPasswordSet || cfg.WalletPassword != "secret" {
		t.Fatalf("unexpected password state: set=%v password=%q", cfg.WalletPasswordSet, cfg.WalletPassword)
	}
}

func TestResolveWalletPasswordFromStdin(t *testing.T) {
	fs, cfg := passwordFlagSet()
	if err := resolveWalletPassword(fs, cfg, "", true, strings.NewReader("secret with spaces\r\n")); err != nil {
		t.Fatal(err)
	}
	if cfg.WalletPassword != "secret with spaces" {
		t.Fatalf("unexpected password %q", cfg.WalletPassword)
	}
}

func TestResolveWalletPasswordFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallet.pass")
	if err := os.WriteFile(path, []byte("file secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs, cfg := passwordFlagSet()
	if err := resolveWalletPassword(fs, cfg, path, false, strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
	if cfg.WalletPassword != "file secret" {
		t.Fatalf("unexpected password %q", cfg.WalletPassword)
	}
}

func TestResolveWalletPasswordRejectsMultipleSources(t *testing.T) {
	fs, cfg := passwordFlagSet("-wallet-password", "secret")
	err := resolveWalletPassword(fs, cfg, "wallet.pass", false, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("expected exactly-one error, got %v", err)
	}
}

func TestResolveWalletPasswordRequiresOneSource(t *testing.T) {
	fs, cfg := passwordFlagSet()
	err := resolveWalletPassword(fs, cfg, "", false, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("expected exactly-one error, got %v", err)
	}
}

func TestTrimPasswordTerminatorPreservesOtherWhitespace(t *testing.T) {
	got := trimPasswordTerminator("  secret \t\r\n")
	if got != "  secret \t" {
		t.Fatalf("unexpected trimmed password %q", got)
	}
}

func passwordFlagSet(args ...string) (*flag.FlagSet, *claim.Config) {
	cfg := &claim.Config{}
	fs := flag.NewFlagSet("claim-test", flag.ContinueOnError)
	fs.StringVar(&cfg.WalletPassword, "wallet-password", "", "")
	if err := fs.Parse(args); err != nil {
		panic(err)
	}
	return fs, cfg
}
