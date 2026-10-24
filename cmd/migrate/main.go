package main

import (
	"log/slog"
	"os"

	"github.com/tashen247/go-ledger/internal/schema"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	db := os.Getenv("DATABASE_URL")
	if db == "" {
		log.Error("config", "err", "DATABASE_URL is required")
		os.Exit(2)
	}
	if err := schema.Up(db); err != nil {
		log.Error("migrate", "err", err)
		os.Exit(1)
	}
	log.Info("migrations applied")
}
