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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout)
	stop()
	os.Exit(code)
}

// run applies a JSONL settlement feed from a file or stdin, or tails a file
// with -watch. It returns the process exit code: 0 when every line applied
// or was parked, 1 when any line was rejected or a fatal error occurred, and
// 2 for usage or configuration errors.
func run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) int {
	log := slog.New(slog.NewJSONHandler(stdout, nil))
	fs := flag.NewFlagSet("settler", flag.ContinueOnError)
	fileFlag := fs.String("file", os.Getenv("LEDGER_FEED_FILE"), "JSONL settlement feed (default stdin)")
	watch := fs.Bool("watch", false, "tail the file and apply appended lines")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	cfg, err := config.FromEnv()
	if err != nil {
		log.Error("config", "err", err)
		return 2
	}

	db, err := store.Open(ctx, cfg.DatabaseURL, cfg.HMACKey)
	if err != nil {
		log.Error("database", "err", err)
		return 1
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
			return 2
		}
		if err := settlement.Follow(ctx, path, handle); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("follow", "err", err)
			return 1
		}
	case path != "":
		f, err := os.Open(path)
		if err != nil {
			log.Error("open feed", "err", err)
			return 1
		}
		defer f.Close()
		if _, err := settlement.Process(ctx, f, handle); err != nil {
			log.Error("process", "err", err)
			return 1
		}
	default:
		if _, err := settlement.Process(ctx, stdin, handle); err != nil && !errors.Is(err, io.EOF) {
			log.Error("process", "err", err)
			return 1
		}
	}
	if failed > 0 {
		log.Error("feed finished with rejected events", "failed", failed)
		return 1
	}
	return 0
}
