package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/tashen247/go-ledger/internal/testkit"
)

func TestRunAppliesMigrationsAndIsRepeatable(t *testing.T) {
	dbURL := testkit.NewEmptyDatabase(t)
	t.Setenv("DATABASE_URL", dbURL)

	for i := 1; i <= 2; i++ {
		var out bytes.Buffer
		if code := run(&out); code != 0 {
			t.Fatalf("run %d: exit %d: %s", i, code, out.String())
		}
		if !strings.Contains(out.String(), "migrations applied") {
			t.Fatalf("run %d: output %s", i, out.String())
		}
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	for _, table := range []string{"accounts", "transactions", "ledger_entries", "idempotency_keys", "settlements", "dead_letters"} {
		var exists bool
		if err := conn.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("table %s was not created", table)
		}
	}
}

func TestRunRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	var out bytes.Buffer
	if code := run(&out); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(out.String(), "DATABASE_URL is required") {
		t.Fatalf("output %s", out.String())
	}
}

func TestRunReportsUnreachableDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://ledger:ledger@127.0.0.1:1/ledger?sslmode=disable&connect_timeout=2")
	var out bytes.Buffer
	if code := run(&out); code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, out.String())
	}
}
