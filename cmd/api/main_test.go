package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tashen247/go-ledger/internal/testkit"
)

// syncBuffer is a bytes.Buffer that is safe to read while the server writes logs.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func waitHealthy(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		res, err := http.Get(base + "/healthz")
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("API did not become healthy")
}

func postJSON(t *testing.T, url string, headers map[string]string, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, raw
}

func TestRunServesAPIAndShutsDownCleanly(t *testing.T) {
	testkit.UseDatabase(t, testkit.NewDatabase(t))
	addr := freeAddr(t)
	t.Setenv("HTTP_ADDR", addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logs := &syncBuffer{}
	done := make(chan int, 1)
	go func() { done <- run(ctx, logs) }()

	base := "http://" + addr
	waitHealthy(t, base)
	if !strings.Contains(logs.String(), `"msg":"listening"`) || !strings.Contains(logs.String(), addr) {
		t.Fatalf("listening log missing: %s", logs.String())
	}

	ids := make([]string, 0, 2)
	for _, acct := range []string{
		`{"name":"cash","currency":"ZAR","type":"asset"}`,
		`{"name":"alice","currency":"ZAR","type":"liability"}`,
	} {
		code, raw := postJSON(t, base+"/accounts", nil, acct)
		if code != http.StatusCreated {
			t.Fatalf("create account: %d %s", code, raw)
		}
		var a struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
	}
	txn := `{"reference":"r","entries":[{"account_id":"` + ids[0] + `","amount":15000},{"account_id":"` + ids[1] + `","amount":-15000}]}`
	if code, raw := postJSON(t, base+"/transactions", map[string]string{"Idempotency-Key": "k1"}, txn); code != http.StatusCreated {
		t.Fatalf("post transaction: %d %s", code, raw)
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d: %s", code, logs.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not return after cancel")
	}
	if _, err := http.Get(base + "/healthz"); err == nil {
		t.Fatal("server still accepting connections after shutdown")
	}
}

func TestRunAddressInUse(t *testing.T) {
	testkit.UseDatabase(t, testkit.NewDatabase(t))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	t.Setenv("HTTP_ADDR", ln.Addr().String())

	logs := &syncBuffer{}
	if code := run(context.Background(), logs); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(logs.String(), `"msg":"listen"`) {
		t.Fatalf("logs %s", logs.String())
	}
}

func TestRunConfigError(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	logs := &syncBuffer{}
	if code := run(context.Background(), logs); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

func TestRunUnreachableDatabase(t *testing.T) {
	testkit.UseDatabase(t, "postgres://ledger:ledger@127.0.0.1:1/ledger?sslmode=disable&connect_timeout=2")
	logs := &syncBuffer{}
	if code := run(context.Background(), logs); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
}
