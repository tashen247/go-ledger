// Package retry retries transient failures with exponential backoff and full jitter.
//
//	sleep = rand(0, min(cap, base * 2^n))
//
// n is the number of failures so far. The upper bound is exclusive.
// Only errors classified as transient are retried. Everything else returns
// immediately. When the attempt budget is spent, Do returns ErrExhausted.
package retry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	codeSerialization = "40001"
	codeDeadlock      = "40P01"
)

// ErrExhausted is returned when every attempt failed with a transient error.
var ErrExhausted = errors.New("retries exhausted")

// Config controls the attempt budget and the backoff curve.
// MaxAttempts counts the first try. Base and Cap bound the jitter window.
type Config struct {
	MaxAttempts int
	Base        time.Duration
	Cap         time.Duration
}

// Clock waits between attempts. Tests supply a clock that records delays
// instead of sleeping.
type Clock interface {
	After(ctx context.Context, d time.Duration) error
}

// Source supplies uniform randomness in [0, 1) for full jitter.
type Source interface {
	Float64() float64
}

// WallClock sleeps on the real clock and returns early when ctx is cancelled.
type WallClock struct{}

// After waits for d or ctx.Done.
func (WallClock) After(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// MathRand uses the global math/rand/v2 generator.
type MathRand struct{}

// Float64 returns a random number in [0, 1).
func (MathRand) Float64() float64 { return rand.Float64() }

// ExhaustedError is the error returned when the budget is spent.
// Attempts is the number of tries that ran. Last is the final transient error.
type ExhaustedError struct {
	Attempts int
	Last     error
}

func (e *ExhaustedError) Error() string {
	return fmt.Sprintf("%s after %d attempts: %v", ErrExhausted, e.Attempts, e.Last)
}

// Unwrap exposes the last transient error.
func (e *ExhaustedError) Unwrap() error { return e.Last }

// Is reports ErrExhausted so callers can use errors.Is.
func (e *ExhaustedError) Is(target error) bool { return target == ErrExhausted }

// Do calls fn until it succeeds, returns a permanent error, or the budget runs out.
func Do(ctx context.Context, cfg Config, clock Clock, rng Source, fn func(context.Context) error) error {
	attempts := cfg.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	if clock == nil {
		clock = WallClock{}
	}
	if rng == nil {
		rng = MathRand{}
	}
	var last error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		last = fn(ctx)
		if last == nil {
			return nil
		}
		if !IsTransient(last) {
			return last
		}
		if attempt == attempts-1 {
			break
		}
		delay := Backoff(cfg, attempt, rng)
		if err := clock.After(ctx, delay); err != nil {
			return err
		}
	}
	return &ExhaustedError{Attempts: attempts, Last: last}
}

// Backoff returns a full-jitter delay for a failure at attempt n (0-based).
// The window is [0, min(cap, base*2^n)).
func Backoff(cfg Config, n int, rng Source) time.Duration {
	ceil := cfg.Base
	if ceil < 0 {
		ceil = 0
	}
	for i := 0; i < n; i++ {
		if cfg.Cap > 0 && ceil >= cfg.Cap {
			ceil = cfg.Cap
			break
		}
		if ceil > time.Duration(math.MaxInt64/2) {
			ceil = cfg.Cap
			break
		}
		ceil *= 2
	}
	if cfg.Cap > 0 && ceil > cfg.Cap {
		ceil = cfg.Cap
	}
	if ceil <= 0 {
		return 0
	}
	f := rng.Float64()
	if f < 0 {
		f = 0
	}
	if f >= 1 {
		f = math.Nextafter(1, 0)
	}
	d := time.Duration(f * float64(ceil))
	if d < 0 || d >= ceil {
		return ceil - 1
	}
	return d
}

// IsTransient reports whether err is worth retrying.
// Serialization failures (40001), deadlocks (40P01), and lost connections are
// transient. Context cancellation and business errors are not.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case codeSerialization, codeDeadlock:
			return true
		default:
			return false
		}
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}
