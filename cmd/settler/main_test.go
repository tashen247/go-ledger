package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tashen247/go-ledger/internal/store"
	"github.com/tashen247/go-ledger/internal/testkit"
)

type env struct {
	st    *store.Store
	txnID string
}

func setup(t *testing.T) env {
	t.Helper()
	dbURL := testkit.NewDatabase(t)
	testkit.UseDatabase(t, dbURL)
	st := testkit.OpenStore(t, dbURL)
	return env{st: st, txnID: testkit.PostDeposit(t, st, 900)}
}

func (e env) line(externalID, status string) string {
	return fmt.Sprintf(`{"external_id":%q,"transaction_id":%q,"status":%q,"amount":900,"currency":"ZAR"}`, externalID, e.txnID, status)
}

// settlement returns how many rows exist for externalID and the state of the latest.
func (e env) settlement(t *testing.T, externalID string) (int, string) {
	t.Helper()
	var n int
	var state *string
	err := e.st.Pool().QueryRow(context.Background(),
		`SELECT count(*), max(state::text) FROM settlements WHERE external_id = $1`, externalID).Scan(&n, &state)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil {
		return n, ""
	}
	return n, *state
}

func TestRunStdinAppliesAndDeduplicates(t *testing.T) {
	e := setup(t)
	feed := e.line("psp-1", "settled") + "\n\n" + e.line("psp-1", "settled") + "\n"
	var out bytes.Buffer
	if code := run(context.Background(), nil, strings.NewReader(feed), &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if n, state := e.settlement(t, "psp-1"); n != 1 || state != "settled" {
		t.Fatalf("rows %d state %q", n, state)
	}
	if strings.Count(out.String(), `"msg":"applied"`) != 2 {
		t.Fatalf("expected two applied log lines: %s", out.String())
	}
}

func TestRunFileFlag(t *testing.T) {
	e := setup(t)
	path := filepath.Join(t.TempDir(), "feed.jsonl")
	if err := os.WriteFile(path, []byte(e.line("psp-file", "failed")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := run(context.Background(), []string{"-file", path}, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if n, state := e.settlement(t, "psp-file"); n != 1 || state != "failed" {
		t.Fatalf("rows %d state %q", n, state)
	}
}

func TestRunFeedFileFromEnv(t *testing.T) {
	e := setup(t)
	path := filepath.Join(t.TempDir(), "feed.jsonl")
	if err := os.WriteFile(path, []byte(e.line("psp-env", "settled")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEDGER_FEED_FILE", path)
	var out bytes.Buffer
	if code := run(context.Background(), nil, strings.NewReader(""), &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if n, _ := e.settlement(t, "psp-env"); n != 1 {
		t.Fatalf("rows %d", n)
	}
}

func TestRunRejectedLinesExitNonZeroButOthersApply(t *testing.T) {
	e := setup(t)
	feed := strings.Join([]string{
		"not json",
		e.line("psp-ok", "settled"),
		e.line("psp-ok", "failed"), // illegal: settled -> failed
	}, "\n") + "\n"
	var out bytes.Buffer
	if code := run(context.Background(), nil, strings.NewReader(feed), &out); code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, out.String())
	}
	if n, state := e.settlement(t, "psp-ok"); n != 1 || state != "settled" {
		t.Fatalf("rows %d state %q", n, state)
	}
	for _, msg := range []string{`"msg":"skip line"`, `"msg":"event rejected"`, `"failed":2`} {
		if !strings.Contains(out.String(), msg) {
			t.Errorf("output missing %s: %s", msg, out.String())
		}
	}
}

func TestRunWatchTailsFile(t *testing.T) {
	e := setup(t)
	path := filepath.Join(t.TempDir(), "feed.jsonl")
	if err := os.WriteFile(path, []byte(e.line("psp-w1", "settled")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"-watch", "-file", path}, strings.NewReader(""), &bytes.Buffer{}) }()

	waitRows := func(externalID string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if n, _ := e.settlement(t, externalID); n == 1 {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("%s was not applied", externalID)
	}
	waitRows("psp-w1")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(e.line("psp-w2", "failed") + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	waitRows("psp-w2")

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("watch did not stop after cancel")
	}
}

func TestRunUsageAndSetupErrors(t *testing.T) {
	setup(t)
	missing := filepath.Join(t.TempDir(), "missing.jsonl")
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"help", []string{"-h"}, 0},
		{"unknown flag", []string{"-nope"}, 2},
		{"watch without a file", []string{"-watch"}, 2},
		{"missing file", []string{"-file", missing}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if code := run(context.Background(), tc.args, strings.NewReader(""), &out); code != tc.want {
				t.Fatalf("exit %d, want %d: %s", code, tc.want, out.String())
			}
		})
	}
}

func TestRunConfigError(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if code := run(context.Background(), nil, strings.NewReader(""), &bytes.Buffer{}); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

func TestRunUnreachableDatabase(t *testing.T) {
	testkit.UseDatabase(t, "postgres://ledger:ledger@127.0.0.1:1/ledger?sslmode=disable&connect_timeout=2")
	if code := run(context.Background(), nil, strings.NewReader(""), &bytes.Buffer{}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
}
