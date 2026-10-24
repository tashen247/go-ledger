package retry

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type fakeClock struct {
	sleeps []time.Duration
}

func (f *fakeClock) After(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.sleeps = append(f.sleeps, d)
	return nil
}

type scripted struct {
	v []float64
	i int
}

func (s *scripted) Float64() float64 {
	if s.i >= len(s.v) {
		return 0
	}
	x := s.v[s.i]
	s.i++
	return x
}

func transient() error { return &pgconn.PgError{Code: "40001", Message: "serialization failure"} }

func TestDoRetriesTransientThenExhausts(t *testing.T) {
	clock := &fakeClock{}
	rng := &scripted{v: []float64{0, 0.5, 0.999}}
	cfg := Config{MaxAttempts: 3, Base: 100 * time.Millisecond, Cap: 150 * time.Millisecond}
	calls := 0
	err := Do(context.Background(), cfg, clock, rng, func(context.Context) error {
		calls++
		return transient()
	})
	var ex *ExhaustedError
	if !errors.As(err, &ex) || !errors.Is(err, ErrExhausted) {
		t.Fatalf("err %v", err)
	}
	if ex.Attempts != 3 || calls != 3 {
		t.Fatalf("attempts %d calls %d", ex.Attempts, calls)
	}
	if len(clock.sleeps) != 2 {
		t.Fatalf("sleeps %v", clock.sleeps)
	}
	// n=0 -> rand(0, 100ms) with u=0 -> 0
	if clock.sleeps[0] != 0 {
		t.Fatalf("sleep0 %s", clock.sleeps[0])
	}
	// n=1 -> min(150ms, 200ms)=150ms, u=0.5 -> 75ms
	if clock.sleeps[1] != 75*time.Millisecond {
		t.Fatalf("sleep1 %s", clock.sleeps[1])
	}
}

func TestDoPermanentNoSleep(t *testing.T) {
	clock := &fakeClock{}
	perm := errors.New("unbalanced")
	err := Do(context.Background(), Config{MaxAttempts: 5, Base: time.Second, Cap: time.Second}, clock, &scripted{v: []float64{0.2}}, func(context.Context) error {
		return perm
	})
	if !errors.Is(err, perm) || errors.Is(err, ErrExhausted) {
		t.Fatalf("err %v", err)
	}
	if len(clock.sleeps) != 0 {
		t.Fatalf("slept %v", clock.sleeps)
	}
}

func TestDoSucceedsOnSecondAttempt(t *testing.T) {
	clock := &fakeClock{}
	n := 0
	err := Do(context.Background(), Config{MaxAttempts: 4, Base: 10 * time.Millisecond, Cap: time.Second}, clock, &scripted{v: []float64{0.25}}, func(context.Context) error {
		n++
		if n == 1 {
			return &pgconn.PgError{Code: "40P01"}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || len(clock.sleeps) != 1 {
		t.Fatalf("n=%d sleeps=%v", n, clock.sleeps)
	}
	if clock.sleeps[0] != 2500*time.Microsecond {
		t.Fatalf("delay %s", clock.sleeps[0])
	}
}

func TestDoContextCanceledBeforeAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	clock := &fakeClock{}
	calls := 0
	err := Do(ctx, Config{MaxAttempts: 3, Base: time.Millisecond, Cap: time.Millisecond}, clock, &scripted{}, func(context.Context) error {
		calls++
		return transient()
	})
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("err %v calls %d", err, calls)
	}
}

func TestDoContextCanceledDuringSleep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	clock := cancelClock{cancel: cancel}
	err := Do(ctx, Config{MaxAttempts: 3, Base: time.Second, Cap: time.Second}, clock, &scripted{v: []float64{0.1}}, func(context.Context) error {
		return transient()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}

type cancelClock struct{ cancel context.CancelFunc }

func (c cancelClock) After(ctx context.Context, d time.Duration) error {
	c.cancel()
	return ctx.Err()
}

func TestBackoffCapped(t *testing.T) {
	cfg := Config{Base: 100 * time.Millisecond, Cap: 150 * time.Millisecond}
	d := Backoff(cfg, 10, &scripted{v: []float64{0}})
	if d != 0 {
		t.Fatalf("u=0 should be 0, got %s", d)
	}
	d = Backoff(cfg, 10, &scripted{v: []float64{0.5}})
	if d != 75*time.Millisecond {
		t.Fatalf("capped jitter: %s", d)
	}
}

func TestIsTransient(t *testing.T) {
	if !IsTransient(&pgconn.PgError{Code: "40001"}) || !IsTransient(&pgconn.PgError{Code: "40P01"}) {
		t.Fatal("expected sqlstate retries")
	}
	if IsTransient(&pgconn.PgError{Code: "23505"}) || IsTransient(errors.New("nope")) || IsTransient(nil) {
		t.Fatal("permanent classified as transient")
	}
	if !IsTransient(io.ErrUnexpectedEOF) || !IsTransient(io.EOF) {
		t.Fatal("connection loss")
	}
	if IsTransient(context.DeadlineExceeded) || IsTransient(context.Canceled) {
		t.Fatal("context errors are not retried")
	}
	if !IsTransient(netErr{}) {
		t.Fatal("net error")
	}
	wrapped := errors.Join(errors.New("begin"), &pgconn.PgError{Code: "40001"})
	if !IsTransient(wrapped) {
		t.Fatal("wrapped pg error")
	}
}

type netErr struct{}

func (netErr) Error() string   { return "read timeout" }
func (netErr) Timeout() bool   { return true }
func (netErr) Temporary() bool { return true }

var _ net.Error = netErr{}
