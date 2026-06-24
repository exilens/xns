package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/exilens/xns/pkg/claim"
	"github.com/exilens/xns/pkg/indexer"
	"github.com/exilens/xns/pkg/lookup"
)

func main() {
	log.SetFlags(0)
	log.SetOutput(os.Stderr)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "claim":
		err = runClaim(os.Args[2:])
	case "lookup":
		err = runLookup(os.Args[2:])
	case "indexer":
		err = runIndexer(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: xns <command> [options]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  claim    claim or renew a name")
	fmt.Fprintln(os.Stderr, "  lookup   resolve a name through an indexer")
	fmt.Fprintln(os.Stderr, "  indexer  run an XNS indexer")
}

func runClaim(args []string) error {
	cfg := claim.Config{}
	fs := flag.NewFlagSet("claim", flag.ExitOnError)
	fs.BoolVar(&cfg.Mainnet, "mainnet", false, "use mainnet")
	fs.BoolVar(&cfg.Stagenet, "stagenet", false, "use stagenet")
	fs.StringVar(&cfg.WalletFile, "wallet-file", "", "Monero wallet file")
	fs.StringVar(&cfg.WalletPassword, "wallet-password", "", "Monero wallet password")
	fs.StringVar(&cfg.Name, "name", "", "XNS name")
	fs.StringVar(&cfg.Owner, "owner", "", "32-byte Ed25519 owner public key as hex")
	fs.StringVar(&cfg.Node, "node", "", "Monero daemon RPC URL")
	fs.Uint64Var(&cfg.Years, "years", 0, "claim duration in years")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireFlags(fs, "wallet-file", "wallet-password", "name", "owner", "node", "years"); err != nil {
		fs.Usage()
		return err
	}
	cfg.WalletPasswordSet = true
	out, err := claim.Run(cfg)
	if err != nil {
		return err
	}
	return claim.PrintJSON(out)
}

func runLookup(args []string) error {
	cfg := lookup.Config{}
	fs := flag.NewFlagSet("lookup", flag.ExitOnError)
	fs.StringVar(&cfg.Indexer, "indexer", "", "indexer HTTP URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireFlags(fs, "indexer"); err != nil {
		fs.Usage()
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("lookup needs exactly one name")
	}
	cfg.Name = fs.Arg(0)
	out, err := lookup.Run(cfg)
	if err != nil {
		return err
	}
	return lookup.PrintJSON(out)
}

func runIndexer(args []string) error {
	cfg := indexer.Config{}
	fs := flag.NewFlagSet("indexer", flag.ExitOnError)
	fs.BoolVar(&cfg.Mainnet, "mainnet", false, "use mainnet")
	fs.BoolVar(&cfg.Stagenet, "stagenet", false, "use stagenet")
	fs.StringVar(&cfg.Node, "node", "", "Monero daemon RPC URL")
	fs.StringVar(&cfg.Listen, "listen", "", "HTTP listen address")
	fs.StringVar(&cfg.DataDir, "data-dir", "", "state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := requireFlags(fs, "node", "listen", "data-dir"); err != nil {
		fs.Usage()
		return err
	}
	return indexer.Run(cfg)
}

func requireFlags(fs *flag.FlagSet, names ...string) error {
	set := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		set[f.Name] = true
	})
	for _, name := range names {
		if !set[name] {
			return fmt.Errorf("--%s is required", name)
		}
	}
	return nil
}
