package config

import (
	"strings"
	"testing"
	"time"
)

const validKey = "0123456789abcdef"

// setEnv sets every config variable, so values from the host environment
// cannot leak into a test.
func setEnv(t *testing.T, vars map[string]string) {
	t.Helper()
	for _, name := range []string{
		"DATABASE_URL", "LEDGER_HMAC_KEY", "HTTP_ADDR", "LEDGER_FEED_FILE",
		"LEDGER_RETRY_MAX", "LEDGER_RETRY_BASE", "LEDGER_RETRY_CAP",
	} {
		t.Setenv(name, vars[name])
	}
}

func TestFromEnvDefaults(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://db", "LEDGER_HMAC_KEY": validKey})
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseURL != "postgres://db" || string(cfg.HMACKey) != validKey {
		t.Fatalf("cfg %+v", cfg)
	}
	if cfg.HTTPAddr != ":8080" || cfg.FeedFile != "" {
		t.Fatalf("defaults: addr %q feed %q", cfg.HTTPAddr, cfg.FeedFile)
	}
	if cfg.Retry.MaxAttempts != 5 || cfg.Retry.Base != 20*time.Millisecond || cfg.Retry.Cap != 500*time.Millisecond {
		t.Fatalf("retry defaults %+v", cfg.Retry)
	}
}

func TestFromEnvOverrides(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":      "postgres://db",
		"LEDGER_HMAC_KEY":   validKey,
		"HTTP_ADDR":         "127.0.0.1:9000",
		"LEDGER_FEED_FILE":  "feed/events.jsonl",
		"LEDGER_RETRY_MAX":  "9",
		"LEDGER_RETRY_BASE": "5ms",
		"LEDGER_RETRY_CAP":  "2s",
	})
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:9000" || cfg.FeedFile != "feed/events.jsonl" {
		t.Fatalf("cfg %+v", cfg)
	}
	if cfg.Retry.MaxAttempts != 9 || cfg.Retry.Base != 5*time.Millisecond || cfg.Retry.Cap != 2*time.Second {
		t.Fatalf("retry %+v", cfg.Retry)
	}
}

func TestFromEnvUnparseableValuesFallBack(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":      "postgres://db",
		"LEDGER_HMAC_KEY":   validKey,
		"LEDGER_RETRY_MAX":  "lots",
		"LEDGER_RETRY_BASE": "soon",
		"LEDGER_RETRY_CAP":  "10",
	})
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Retry.MaxAttempts != 5 || cfg.Retry.Base != 20*time.Millisecond || cfg.Retry.Cap != 500*time.Millisecond {
		t.Fatalf("retry %+v, want defaults", cfg.Retry)
	}
}

func TestFromEnvErrors(t *testing.T) {
	cases := []struct {
		name string
		vars map[string]string
		want string
	}{
		{"missing database url", map[string]string{"LEDGER_HMAC_KEY": validKey}, "DATABASE_URL"},
		{"missing hmac key", map[string]string{"DATABASE_URL": "postgres://db"}, "LEDGER_HMAC_KEY"},
		{"short hmac key", map[string]string{"DATABASE_URL": "postgres://db", "LEDGER_HMAC_KEY": "fifteen-bytes!!"}, "at least 16 bytes"},
		{"zero retries", map[string]string{"DATABASE_URL": "postgres://db", "LEDGER_HMAC_KEY": validKey, "LEDGER_RETRY_MAX": "0"}, "LEDGER_RETRY_MAX"},
		{"negative retries", map[string]string{"DATABASE_URL": "postgres://db", "LEDGER_HMAC_KEY": validKey, "LEDGER_RETRY_MAX": "-3"}, "LEDGER_RETRY_MAX"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, tc.vars)
			_, err := FromEnv()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want mention of %q", err, tc.want)
			}
		})
	}
}

func TestFromEnvAcceptsExactly16ByteKey(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://db", "LEDGER_HMAC_KEY": validKey})
	if len(validKey) != 16 {
		t.Fatal("test key must be 16 bytes")
	}
	if _, err := FromEnv(); err != nil {
		t.Fatalf("16-byte key rejected: %v", err)
	}
}
