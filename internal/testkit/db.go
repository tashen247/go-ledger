// Package testkit creates a throwaway Postgres database for integration tests.
package testkit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// NewStore migrates a fresh database and returns a store pointed at it.
// The database is dropped when the test finishes. Tests skip when Postgres
// cannot be reached.
func NewStore(t *testing.T) *store.Store {
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

	dbURL := databaseURL(name)
	if err := schema.Up(dbURL); err != nil {
		dropDB(conn, name)
		conn.Close(ctx)
		t.Fatalf("migrate: %v", err)
	}
	st, err := store.Open(ctx, dbURL, HMACKey)
	if err != nil {
		dropDB(conn, name)
		conn.Close(ctx)
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		st.Close()
		dropCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		dropDB(conn, name)
		conn.Close(dropCtx)
	})
	return st
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
