package settlement

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/tashen247/go-ledger/internal/retry"
	"github.com/tashen247/go-ledger/internal/store"
)

// Applier performs one settlement attempt.
type Applier interface {
	Apply(ctx context.Context, ev Event) (Result, error)
}

// Parker stores a message after retries are exhausted.
type Parker interface {
	Park(ctx context.Context, source string, payload []byte, lastError string, attempts int) (string, error)
}

// Service applies settlement events through the store.
type Service struct {
	DB *store.Store
}

// Apply runs one attempt. Transient errors are returned to the caller so
// Ingest can decide whether to retry.
func (s Service) Apply(ctx context.Context, ev Event) (Result, error) {
	res, err := s.DB.ApplySettlement(ctx, ev.ExternalID, ev.TransactionID, ev.Status, ev.Currency, ev.Amount)
	if err != nil {
		return Result{}, err
	}
	return Result{ID: res.ID, ExternalID: res.ExternalID, State: res.State, Applied: res.Applied}, nil
}

// Ingest retries transient failures with full jitter. When the budget is
// spent the payload is parked and ErrParked is returned. Permanent errors,
// including illegal transitions, are returned as-is and are not parked.
func Ingest(ctx context.Context, app Applier, park Parker, cfg retry.Config, clock retry.Clock, rng retry.Source, ev Event, payload []byte) (Result, error) {
	var res Result
	applied := false
	err := retry.Do(ctx, cfg, clock, rng, func(ctx context.Context) error {
		got, err := app.Apply(ctx, ev)
		if err != nil {
			return err
		}
		if got.Applied {
			applied = true
		}
		res = got
		return nil
	})
	if err == nil {
		if applied {
			res.Applied = true
		}
		return res, nil
	}
	var exhausted *retry.ExhaustedError
	if errors.As(err, &exhausted) {
		body := payload
		if len(body) == 0 {
			var merr error
			body, merr = json.Marshal(ev)
			if merr != nil {
				return Result{}, merr
			}
		}
		id, perr := park.Park(ctx, "settlement_feed", body, exhausted.Error(), exhausted.Attempts)
		if perr != nil {
			return Result{}, perr
		}
		return Result{}, &ParkedError{ID: id, Attempts: exhausted.Attempts, Cause: exhausted.Last}
	}
	return Result{}, err
}
