// Package config reads process configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/tashen247/go-ledger/internal/retry"
)

// Config is everything the processes need to start.
type Config struct {
	DatabaseURL string
	HMACKey     []byte
	HTTPAddr    string
	FeedFile    string
	Retry       retry.Config
}

// FromEnv loads configuration. LEDGER_HMAC_KEY and DATABASE_URL are required.
// The HMAC key is the raw string, used as the HMAC-SHA256 key, and must be
// at least 16 bytes.
func FromEnv() (Config, error) {
	db := os.Getenv("DATABASE_URL")
	if db == "" {
		return Config{}, errorsNew("DATABASE_URL is required")
	}
	key := os.Getenv("LEDGER_HMAC_KEY")
	if len(key) < 16 {
		return Config{}, errorsNew("LEDGER_HMAC_KEY must be at least 16 bytes")
	}
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	retryCfg := retry.Config{
		MaxAttempts: envInt("LEDGER_RETRY_MAX", 5),
		Base:        envDuration("LEDGER_RETRY_BASE", 20*time.Millisecond),
		Cap:         envDuration("LEDGER_RETRY_CAP", 500*time.Millisecond),
	}
	if retryCfg.MaxAttempts < 1 {
		return Config{}, errorsNew("LEDGER_RETRY_MAX must be >= 1")
	}
	return Config{
		DatabaseURL: db,
		HMACKey:     []byte(key),
		HTTPAddr:    addr,
		FeedFile:    os.Getenv("LEDGER_FEED_FILE"),
		Retry:       retryCfg,
	}, nil
}

func envInt(name string, fallback int) int {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envDuration(name string, fallback time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func errorsNew(msg string) error { return fmt.Errorf("%s", msg) }
