package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/tashen247/go-ledger/internal/config"
	"github.com/tashen247/go-ledger/internal/ledger"
	"github.com/tashen247/go-ledger/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg, err := config.FromEnv()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, cfg.DatabaseURL, cfg.HMACKey)
	if err != nil {
		log.Error("database", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	entries, err := db.ChainEntries(ctx)
	if err != nil {
		log.Error("load chain", "err", err)
		os.Exit(1)
	}
	verified, first := ledger.Verify(db.HMACKey(), entries)
	fmt.Println(ledger.ChainError(verified, first))
	if first != nil {
		os.Exit(1)
	}
}
