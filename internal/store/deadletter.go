package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tashen247/go-ledger/internal/id"
)

// DeadLetter is a parked feed message.
type DeadLetter struct {
	ID         string
	Source     string
	Payload    []byte
	LastError  string
	Attempts   int
	Status     string
	Result     []byte
	CreatedAt  time.Time
	ReplayedAt *time.Time
}

// Park inserts a dead letter and returns its id.
func (s *Store) Park(ctx context.Context, source string, payload []byte, lastError string, attempts int) (string, error) {
	if source == "" {
		source = "settlement_feed"
	}
	if attempts < 1 {
		attempts = 1
	}
	dlID := id.New("dl_")
	_, err := s.pool.Exec(ctx, `
		INSERT INTO dead_letters (id, source, payload, last_error, attempts)
		VALUES ($1, $2, $3::jsonb, $4, $5)`, dlID, source, string(payload), lastError, attempts)
	if err != nil {
		return "", err
	}
	return dlID, nil
}

// ListDeadLetters returns dead letters, optionally filtered by status.
func (s *Store) ListDeadLetters(ctx context.Context, status string) ([]DeadLetter, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, source, payload, last_error, attempts, status, created_at
		FROM dead_letters
		WHERE ($1 = '' OR status = $2)
		ORDER BY created_at, id`, status, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeadLetter
	for rows.Next() {
		var item DeadLetter
		if err := rows.Scan(&item.ID, &item.Source, &item.Payload, &item.LastError, &item.Attempts, &item.Status, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.CreatedAt = item.CreatedAt.UTC()
		out = append(out, item)
	}
	if out == nil {
		out = []DeadLetter{}
	}
	return out, rows.Err()
}

// LockDeadLetter loads one letter and holds it until the transaction ends.
func (s *Store) LockDeadLetter(ctx context.Context, tx pgx.Tx, dlID string) (DeadLetter, error) {
	var item DeadLetter
	var replayedAt *time.Time
	err := tx.QueryRow(ctx, `
		SELECT id, source, payload, last_error, attempts, status, replay_result #>> '{}', created_at, replayed_at
		FROM dead_letters
		WHERE id = $1
		FOR UPDATE`, dlID).Scan(
		&item.ID, &item.Source, &item.Payload, &item.LastError, &item.Attempts,
		&item.Status, &item.Result, &item.CreatedAt, &replayedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return DeadLetter{}, ErrDeadLetterNotFound
	}
	if err != nil {
		return DeadLetter{}, err
	}
	item.CreatedAt = item.CreatedAt.UTC()
	if replayedAt != nil {
		at := replayedAt.UTC()
		item.ReplayedAt = &at
	}
	return item, nil
}

// MarkDeadLetterReplayed stores the replay result. The letter must still be parked.
func (s *Store) MarkDeadLetterReplayed(ctx context.Context, tx pgx.Tx, dlID string, result []byte) (time.Time, error) {
	var at time.Time
	err := tx.QueryRow(ctx, `
		UPDATE dead_letters
		SET status = 'replayed', replay_result = to_jsonb($2::text), replayed_at = now()
		WHERE id = $1 AND status = 'parked'
		RETURNING replayed_at`, dlID, string(result)).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrDeadLetterNotFound
	}
	if err != nil {
		return time.Time{}, err
	}
	return at.UTC(), nil
}
