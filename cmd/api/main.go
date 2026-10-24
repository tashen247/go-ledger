package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tashen247/go-ledger/internal/config"
	"github.com/tashen247/go-ledger/internal/httpapi"
	"github.com/tashen247/go-ledger/internal/retry"
	"github.com/tashen247/go-ledger/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Stdout)
	stop()
	os.Exit(code)
}

// run serves the HTTP API until ctx is cancelled, then shuts down gracefully.
// It returns the process exit code.
func run(ctx context.Context, stdout io.Writer) int {
	log := slog.New(slog.NewJSONHandler(stdout, nil))
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

	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		log.Error("listen", "err", err)
		return 1
	}
	srv := &http.Server{
		Handler:           httpapi.New(httpapi.Deps{Store: db, Logger: log, Retry: cfg.Retry}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", ln.Addr().String())
		errCh <- srv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			log.Error("shutdown", "err", err)
			return 1
		}
		return 0
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server", "err", err)
			return 1
		}
		return 0
	}
}
