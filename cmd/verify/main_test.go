package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tashen247/go-ledger/internal/testkit"
)

func TestRunEmptyChain(t *testing.T) {
	testkit.UseDatabase(t, testkit.NewDatabase(t))
	var out, logs bytes.Buffer
	if code := run(context.Background(), &out, &logs); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out.String(), logs.String())
	}
	if got := strings.TrimSpace(out.String()); got != "ok verified_entries=0" {
		t.Fatalf("output %q", got)
	}
}

func TestRunValidChain(t *testing.T) {
	dbURL := testkit.NewDatabase(t)
	testkit.UseDatabase(t, dbURL)
	st := testkit.OpenStore(t, dbURL)
	testkit.PostDeposit(t, st, 1000)
	testkit.PostDeposit(t, st, 250)

	var out, logs bytes.Buffer
	if code := run(context.Background(), &out, &logs); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out.String(), logs.String())
	}
	if got := strings.TrimSpace(out.String()); got != "ok verified_entries=4" {
		t.Fatalf("output %q", got)
	}
}

func TestRunDetectsTamperedChain(t *testing.T) {
	dbURL := testkit.NewDatabase(t)
	testkit.UseDatabase(t, dbURL)
	st := testkit.OpenStore(t, dbURL)
	txnID := testkit.PostDeposit(t, st, 1000)

	// History cannot be edited, but a forged row can be appended. Its hash will not verify.
	ctx := context.Background()
	_, err := st.Pool().Exec(ctx, `
		INSERT INTO ledger_entries (transaction_id, account_id, amount, currency, prev_hash, hash)
		SELECT transaction_id, account_id, 1, currency, decode(repeat('00', 32), 'hex'), decode(repeat('ab', 32), 'hex')
		FROM ledger_entries WHERE transaction_id = $1 LIMIT 1`, txnID)
	if err != nil {
		t.Fatal(err)
	}

	var out, logs bytes.Buffer
	if code := run(ctx, &out, &logs); code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, out.String())
	}
	if strings.HasPrefix(out.String(), "ok") {
		t.Fatalf("tampered chain reported ok: %s", out.String())
	}
}

func TestRunConfigError(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	var out, logs bytes.Buffer
	if code := run(context.Background(), &out, &logs); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if out.Len() != 0 || !strings.Contains(logs.String(), "DATABASE_URL") {
		t.Fatalf("stdout %q logs %q", out.String(), logs.String())
	}
}

func TestRunUnreachableDatabase(t *testing.T) {
	testkit.UseDatabase(t, "postgres://ledger:ledger@127.0.0.1:1/ledger?sslmode=disable&connect_timeout=2")
	var out, logs bytes.Buffer
	if code := run(context.Background(), &out, &logs); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
}

func TestRunMissingSchema(t *testing.T) {
	testkit.UseDatabase(t, testkit.NewEmptyDatabase(t))
	var out, logs bytes.Buffer
	if code := run(context.Background(), &out, &logs); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(logs.String(), "load chain") {
		t.Fatalf("logs %s", logs.String())
	}
}
