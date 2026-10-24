package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/tashen247/go-ledger/internal/config"
	"github.com/tashen247/go-ledger/internal/retry"
	"github.com/tashen247/go-ledger/internal/settlement"
	"github.com/tashen247/go-ledger/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	fileFlag := flag.String("file", os.Getenv("LEDGER_FEED_FILE"), "JSONL settlement feed (default stdin)")
	watch := flag.Bool("watch", false, "tail the file and apply appended lines")
	flag.Parse()

	cfg, err := config.FromEnv()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, cfg.DatabaseURL, cfg.HMACKey)
	if err != nil {
		log.Error("database", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	db.SetRetry(cfg.Retry, retry.WallClock{}, retry.MathRand{})

	app := settlement.Service{DB: db}
	failed := 0
	handle := func(ctx context.Context, line []byte) error {
		ev, err := settlement.Parse(line)
		if err != nil {
			failed++
			log.Error("skip line", "err", err)
			return nil
		}
		res, err := settlement.Ingest(ctx, app, db, cfg.Retry, retry.WallClock{}, retry.MathRand{}, ev, line)
		if err != nil {
			if errors.Is(err, settlement.ErrParked) {
				log.Warn("parked", "err", err, "external_id", ev.ExternalID)
				return nil
			}
			if retry.IsTransient(err) {
				return err
			}
			failed++
			log.Error("event rejected", "err", err, "external_id", ev.ExternalID)
			return nil
		}
		log.Info("applied", "external_id", res.ExternalID, "state", res.State, "applied", res.Applied, "id", res.ID)
		return nil
	}

	path := *fileFlag
	if path == "" {
		path = cfg.FeedFile
	}
	switch {
	case *watch:
		if path == "" {
			log.Error("watch requires -file or LEDGER_FEED_FILE")
			os.Exit(2)
		}
		if err := settlement.Follow(ctx, path, handle); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("follow", "err", err)
			os.Exit(1)
		}
	case path != "":
		f, err := os.Open(path)
		if err != nil {
			log.Error("open feed", "err", err)
			os.Exit(1)
		}
		defer f.Close()
		if _, err := settlement.Process(ctx, f, handle); err != nil {
			log.Error("process", "err", err)
			os.Exit(1)
		}
	default:
		if _, err := settlement.Process(ctx, os.Stdin, handle); err != nil && !errors.Is(err, io.EOF) {
			log.Error("process", "err", err)
			os.Exit(1)
		}
	}
	if failed > 0 {
		log.Error("feed finished with rejected events", "failed", failed)
		os.Exit(1)
	}
}
