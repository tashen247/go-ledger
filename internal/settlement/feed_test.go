package settlement

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder collects handled lines so a test can wait for them.
type recorder struct {
	mu    sync.Mutex
	lines []string
	fail  string
}

var errBadLine = errors.New("bad line")

func (r *recorder) handle(_ context.Context, line []byte) error {
	if r.fail != "" && string(line) == r.fail {
		return errBadLine
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, string(line))
	return nil
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}

// waitFor polls until the recorded lines equal want or the deadline passes.
func (r *recorder) waitFor(t *testing.T, want ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if reflect.DeepEqual(r.snapshot(), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("lines %q, want %q", r.snapshot(), want)
}

// follow runs Follow in the background. It returns a cancel func and a wait
// func that returns Follow's result; the result is cached so the test and the
// cleanup can both call wait.
func follow(t *testing.T, path string, r *recorder) (context.CancelFunc, func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Follow(ctx, path, r.handle) }()
	var (
		once   sync.Once
		result error
	)
	wait := func() error {
		once.Do(func() {
			select {
			case result = <-done:
			case <-time.After(5 * time.Second):
				t.Error("Follow did not return")
			}
		})
		return result
	}
	t.Cleanup(func() {
		cancel()
		wait()
	})
	return cancel, wait
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(s); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFollowAppliesExistingThenAppendedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.jsonl")
	if err := os.WriteFile(path, []byte("a\n\n  b  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &recorder{}
	cancel, wait := follow(t, path, r)
	r.waitFor(t, "a", "b")

	appendFile(t, path, "c\n")
	r.waitFor(t, "a", "b", "c")

	cancel()
	if err := wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v, want context.Canceled", err)
	}
}

func TestFollowHoldsPartialLineUntilNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.jsonl")
	if err := os.WriteFile(path, []byte("a\n{\"half\":"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &recorder{}
	follow(t, path, r)
	r.waitFor(t, "a")

	// Two poll intervals pass without the line being terminated.
	time.Sleep(700 * time.Millisecond)
	if got := r.snapshot(); len(got) != 1 {
		t.Fatalf("partial line was handled early: %q", got)
	}

	appendFile(t, path, "true}\n")
	r.waitFor(t, "a", `{"half":true}`)
}

func TestFollowWaitsForMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "later.jsonl")
	r := &recorder{}
	follow(t, path, r)

	time.Sleep(400 * time.Millisecond)
	if err := os.WriteFile(path, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.waitFor(t, "first")
}

func TestFollowRereadsTruncatedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.jsonl")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &recorder{}
	follow(t, path, r)
	r.waitFor(t, "one", "two")

	// A rotated file is shorter than the saved offset, so it is read from the start.
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.waitFor(t, "one", "two", "x")
}

func TestFollowStopsOnHandlerError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.jsonl")
	if err := os.WriteFile(path, []byte("ok\nbad\nnever\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &recorder{fail: "bad"}
	_, wait := follow(t, path, r)
	if err := wait(); !errors.Is(err, errBadLine) {
		t.Fatalf("err %v, want errBadLine", err)
	}
	if got := r.snapshot(); !reflect.DeepEqual(got, []string{"ok"}) {
		t.Fatalf("lines %q", got)
	}
}

func TestFollowReturnsReadError(t *testing.T) {
	// A directory opens but cannot be read as a file.
	r := &recorder{}
	_, wait := follow(t, t.TempDir(), r)
	err := wait()
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("err %v, want a read error", err)
	}
}

func TestFollowCancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Follow(ctx, filepath.Join(t.TempDir(), "x"), (&recorder{}).handle)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}

func TestFollowCancelledWhileWaitingForFile(t *testing.T) {
	r := &recorder{}
	cancel, wait := follow(t, filepath.Join(t.TempDir(), "missing"), r)
	time.Sleep(100 * time.Millisecond)
	cancel()
	if err := wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}

func TestProcessSkipsBlankLinesAndCounts(t *testing.T) {
	r := &recorder{}
	n, err := Process(context.Background(), strings.NewReader("a\n\n   \nb\r\nc"), r.handle)
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if got := r.snapshot(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("lines %q", got)
	}
}

func TestProcessStopsOnHandlerError(t *testing.T) {
	r := &recorder{fail: "bad"}
	n, err := Process(context.Background(), strings.NewReader("a\nbad\nc\n"), r.handle)
	if !errors.Is(err, errBadLine) || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestProcessHonoursCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n, err := Process(ctx, strings.NewReader("a\n"), (&recorder{}).handle)
	if !errors.Is(err, context.Canceled) || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestProcessRejectsOversizedLine(t *testing.T) {
	huge := bytes.Repeat([]byte("x"), 2<<20)
	_, err := Process(context.Background(), bytes.NewReader(huge), (&recorder{}).handle)
	if err == nil {
		t.Fatal("expected a scanner error for a line over 1 MiB")
	}
}

func TestSleep(t *testing.T) {
	if !sleep(context.Background(), time.Millisecond) {
		t.Fatal("sleep returned false without cancellation")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if sleep(ctx, time.Hour) {
		t.Fatal("sleep returned true after cancellation")
	}
	if time.Since(start) > time.Second {
		t.Fatal("sleep did not return promptly on cancellation")
	}
}
