// Package deadletter parks failed settlement events and replays them.
package deadletter

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tashen247/go-ledger/internal/settlement"
	"github.com/tashen247/go-ledger/internal/store"
)

// Item is one dead letter as returned by the list endpoint.
type Item struct {
	ID        string          `json:"id"`
	Source    string          `json:"source"`
	Payload   json.RawMessage `json:"payload"`
	LastError string          `json:"last_error"`
	Attempts  int             `json:"attempts"`
	Status    string          `json:"status"`
	CreatedAt time.Time       `json:"created_at"`
}

// Replay is the body of POST /dead-letters/{id}/replay.
type Replay struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
}

// Service is the dead-letter API used by HTTP handlers.
type Service struct {
	DB *store.Store
}

// Park stores a payload. The store generates the id.
func (s Service) Park(ctx context.Context, source string, payload []byte, lastError string, attempts int) (string, error) {
	return s.DB.Park(ctx, source, payload, lastError, attempts)
}

// List returns dead letters. An empty status returns every status.
func (s Service) List(ctx context.Context, status string) ([]Item, error) {
	rows, err := s.DB.ListDeadLetters(ctx, status)
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(rows))
	for _, row := range rows {
		items = append(items, Item{
			ID: row.ID, Source: row.Source, Payload: json.RawMessage(row.Payload),
			LastError: row.LastError, Attempts: row.Attempts, Status: row.Status, CreatedAt: row.CreatedAt,
		})
	}
	return items, nil
}

// Replay runs a parked settlement in the same transaction that marks the letter
// replayed. A letter that was already replayed returns the stored result and
// does not apply the event again.
func (s Service) Replay(ctx context.Context, id string) (Replay, error) {
	var out Replay
	err := s.DB.InTx(ctx, pgx.ReadCommitted, func(tx pgx.Tx) error {
		item, err := s.DB.LockDeadLetter(ctx, tx, id)
		if err != nil {
			return err
		}
		if item.Status == "replayed" {
			out = Replay{ID: item.ID, Status: item.Status, Result: json.RawMessage(item.Result)}
			return nil
		}
		ev, err := settlement.Parse(item.Payload)
		if err != nil {
			return err
		}
		res, err := s.DB.ApplySettlementTx(ctx, tx, ev.ExternalID, ev.TransactionID, ev.Status, ev.Currency, ev.Amount)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(struct {
			SettlementID string `json:"settlement_id"`
			Applied      bool   `json:"applied"`
		}{SettlementID: res.ID, Applied: res.Applied})
		if err != nil {
			return err
		}
		if _, err := s.DB.MarkDeadLetterReplayed(ctx, tx, id, raw); err != nil {
			return err
		}
		out = Replay{ID: item.ID, Status: "replayed", Result: raw}
		return nil
	})
	return out, err
}
