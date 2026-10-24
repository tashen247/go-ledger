// Package store is the Postgres boundary: repositories and transaction retry.
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tashen247/go-ledger/internal/money"
	"github.com/tashen247/go-ledger/internal/retry"
)

// Advisory lock classes. The numbers are arbitrary namespaces so idempotency,
// the hash chain, and settlement events do not collide with each other.
const (
	// LockClassIdempotency serializes requests that share an Idempotency-Key.
	LockClassIdempotency int32 = 8721
	// LockClassChain serializes appends to the global hash chain.
	LockClassChain int32 = 8722
	// LockClassSettlement serializes deliveries of one external id.
	LockClassSettlement int32 = 8723
)

// Store is a connection pool plus the HMAC key used to sign entries.
type Store struct {
	pool    *pgxpool.Pool
	hmacKey []byte
	retry   retry.Config
	clock   retry.Clock
	rand    retry.Source
}

// Open connects to Postgres and forces UTC on every connection so chain
// timestamps round-trip through timestamptz without a zone shift.
func Open(ctx context.Context, databaseURL string, hmacKey []byte) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 20
	cfg.MinConns = 1
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	cfg.ConnConfig.RuntimeParams["application_name"] = "go-ledger"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Store{
		pool:    pool,
		hmacKey: append([]byte(nil), hmacKey...),
		retry:   retry.Config{MaxAttempts: 5, Base: 10 * time.Millisecond, Cap: 200 * time.Millisecond},
		clock:   retry.WallClock{},
		rand:    retry.MathRand{},
	}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

// Pool exposes the pool for tests that assert triggers directly.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// HMACKey returns a copy of the signing key.
func (s *Store) HMACKey() []byte { return append([]byte(nil), s.hmacKey...) }

// SetRetry replaces the transaction retry policy. Tests use a short budget.
func (s *Store) SetRetry(cfg retry.Config, clock retry.Clock, rng retry.Source) {
	s.retry = cfg
	if clock != nil {
		s.clock = clock
	}
	if rng != nil {
		s.rand = rng
	}
}

// InTx runs fn inside a transaction and retries serialization failures and
// deadlocks. fn must be safe to call again after a rollback. The posting
// path uses Read Committed: a serializable snapshot is taken at the first
// statement, which would be the chain lock, and would then miss the commit
// it waited for. Settlement uses the same isolation; the state guard is the
// idempotency mechanism there.
func (s *Store) InTx(ctx context.Context, iso pgx.TxIsoLevel, fn func(tx pgx.Tx) error) error {
	return retry.Do(ctx, s.retry, s.clock, s.rand, func(ctx context.Context) error {
		tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: iso})
		if err != nil {
			return err
		}
		if err := fn(tx); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return nil
	})
}

func mapPG(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case "23505":
		switch pgErr.ConstraintName {
		case "accounts_name_key":
			return fmt.Errorf("%w: %s", ErrDuplicateAccount, pgErr.ConstraintName)
		case "idempotency_keys_pkey":
			return fmt.Errorf("%w: %s", ErrIdempotencyInProgress, pgErr.ConstraintName)
		default:
			return fmt.Errorf("unique violation on %s: %w", pgErr.ConstraintName, err)
		}
	case "23503":
		switch {
		case strings.Contains(pgErr.ConstraintName, "account"):
			return fmt.Errorf("%w: %s", ErrUnknownAccount, pgErr.ConstraintName)
		case strings.Contains(pgErr.ConstraintName, "transaction"):
			return fmt.Errorf("%w: %s", ErrTransactionNotFound, pgErr.ConstraintName)
		}
	case "23514":
		return &InvalidError{Msg: pgErr.Message}
	case "22003":
		return fmt.Errorf("%w: balance overflow", overflowError{})
	case "P0001":
		if from, to, ok := parseIllegalTransition(pgErr.Message); ok {
			return &TransitionError{From: from, To: to}
		}
	}
	return err
}

// overflowError is the Postgres numeric overflow mapped onto money.ErrOverflow.
type overflowError struct{}

func (overflowError) Error() string { return "amount overflow" }

// Unwrap lets errors.Is match money.ErrOverflow.
func (overflowError) Unwrap() error { return money.ErrOverflow }

func parseIllegalTransition(msg string) (string, string, bool) {
	const prefix = "illegal settlement transition "
	i := strings.Index(msg, prefix)
	if i < 0 {
		return "", "", false
	}
	rest := strings.TrimSpace(msg[i+len(prefix):])
	from, to, ok := strings.Cut(rest, " -> ")
	if !ok {
		return "", "", false
	}
	fields := strings.Fields(to)
	if len(fields) == 0 {
		return "", "", false
	}
	to = fields[0]
	from = strings.TrimSpace(from)
	if from == "" || to == "" {
		return "", "", false
	}
	return from, to, true
}
