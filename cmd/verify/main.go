package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/tashen247/go-ledger/internal/config"
	"github.com/tashen247/go-ledger/internal/ledger"
	"github.com/tashen247/go-ledger/internal/store"
)

func main() {
	os.Exit(run(context.Background(), os.Stdout, os.Stderr))
}

// run verifies the hash chain and returns the process exit code. The result
// line goes to stdout; logs go to stderr.
func run(ctx context.Context, stdout, stderr io.Writer) int {
	log := slog.New(slog.NewJSONHandler(stderr, nil))
	cfg, err := config.FromEnv()
	if err != nil {
		log.Error("config", "err", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, cfg.DatabaseURL, cfg.HMACKey)
	if err != nil {
		log.Error("database", "err", err)
		return 1
	}
	defer db.Close()
	entries, err := db.ChainEntries(ctx)
	if err != nil {
		log.Error("load chain", "err", err)
		return 1
	}
	verified, first := ledger.Verify(db.HMACKey(), entries)
	fmt.Fprintln(stdout, ledger.ChainError(verified, first))
	if first != nil {
		return 1
	}
	return 0
}
