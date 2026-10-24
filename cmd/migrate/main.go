package main

import (
	"io"
	"log/slog"
	"os"

	"github.com/tashen247/go-ledger/internal/schema"
)

func main() {
	os.Exit(run(os.Stdout))
}

// run applies pending migrations and returns the process exit code.
func run(stdout io.Writer) int {
	log := slog.New(slog.NewJSONHandler(stdout, nil))
	db := os.Getenv("DATABASE_URL")
	if db == "" {
		log.Error("config", "err", "DATABASE_URL is required")
		return 2
	}
	if err := schema.Up(db); err != nil {
		log.Error("migrate", "err", err)
		return 1
	}
	log.Info("migrations applied")
	return 0
}
