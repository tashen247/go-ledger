// Package testkit creates a throwaway Postgres database for integration tests.
package testkit

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tashen247/go-ledger/internal/schema"
	"github.com/tashen247/go-ledger/internal/store"
)

// HMACKey is the signing key used by integration tests.
var HMACKey = []byte("test-hmac-key-32-bytes-long!!")

// NewEmptyDatabase creates a throwaway database with no schema and returns
// its URL. The database is dropped when the test finishes. Tests skip when
// Postgres cannot be reached.
func NewEmptyDatabase(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	adminCfg, err := pgx.ParseConfig(adminURL())
	if err != nil {
		t.Fatalf("admin url: %v", err)
	}
	adminCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, adminCfg)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	if err := conn.Ping(ctx); err != nil {
		conn.Close(ctx)
		t.Skipf("postgres unavailable: %v", err)
	}

	name := "lt_" + randomHex(t)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		conn.Close(ctx)
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		dropDB(conn, name)
		conn.Close(dropCtx)
	})

	return databaseURL(name)
}

// NewDatabase creates and migrates a throwaway database and returns its URL.
func NewDatabase(t *testing.T) string {
	t.Helper()
	dbURL := NewEmptyDatabase(t)
	if err := schema.Up(dbURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return dbURL
}

// UseDatabase points the process configuration at dbURL for the rest of the
// test: DATABASE_URL and LEDGER_HMAC_KEY are set, and the optional variables
// are cleared so the host environment cannot leak in.
func UseDatabase(t *testing.T, dbURL string) {
	t.Helper()
	t.Setenv("DATABASE_URL", dbURL)
	t.Setenv("LEDGER_HMAC_KEY", string(HMACKey))
	for _, name := range []string{"HTTP_ADDR", "LEDGER_FEED_FILE", "LEDGER_RETRY_MAX", "LEDGER_RETRY_BASE", "LEDGER_RETRY_CAP"} {
		t.Setenv(name, "")
	}
}

// OpenStore opens a store on an existing test database and closes it when the test finishes.
func OpenStore(t *testing.T, dbURL string) *store.Store {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, dbURL, HMACKey)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

// PostDeposit creates a ZAR asset and liability account and posts a balanced
// transaction of amount minor units between them. It returns the transaction id.
func PostDeposit(t *testing.T, st *store.Store, amount int64) string {
	t.Helper()
	ctx := context.Background()
	suffix := randomHex(t)[:6]
	cash, err := st.CreateAccount(ctx, "cash-"+suffix, "ZAR", "asset")
	if err != nil {
		t.Fatal(err)
	}
	wallet, err := st.CreateAccount(ctx, "wallet-"+suffix, "ZAR", "liability")
	if err != nil {
		t.Fatal(err)
	}
	key := "deposit-" + suffix
	sum := sha256.Sum256([]byte(key))
	res, err := st.Post(ctx, store.PostCommand{
		Key: key, RequestHash: sum[:], Reference: key,
		Entries: []store.Leg{{AccountID: cash.ID, Amount: amount}, {AccountID: wallet.ID, Amount: -amount}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var txn struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res.Body, &txn); err != nil {
		t.Fatal(err)
	}
	return txn.ID
}

// NewStore migrates a fresh database and returns a store pointed at it.
// The store is closed and the database dropped when the test finishes.
func NewStore(t *testing.T) *store.Store {
	t.Helper()
	return OpenStore(t, NewDatabase(t))
}

func dropDB(conn *pgx.Conn, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
}

func randomHex(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b[:])
}

func rootURL() string {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://ledger:ledger@127.0.0.1:5432/ledger?sslmode=disable"
}

func adminURL() string { return withDB(rootURL(), "postgres") }

func databaseURL(name string) string { return withDB(rootURL(), name) }

func withDB(raw, name string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Path = "/" + name
	return u.String()
}

// Pool is a convenience for tests that need the underlying pool.
func Pool(st *store.Store) *pgxpool.Pool { return st.Pool() }
