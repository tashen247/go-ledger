package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/tashen247/go-ledger/internal/id"
	"github.com/tashen247/go-ledger/internal/money"
)

// SettlementResult is the outcome of applying one feed event.
type SettlementResult struct {
	ID         string
	ExternalID string
	State      string
	Applied    bool
}

// ApplySettlement applies one at-least-once settlement event in its own transaction.
func (s *Store) ApplySettlement(ctx context.Context, externalID, transactionID, status, currency string, amount int64) (SettlementResult, error) {
	var res SettlementResult
	err := s.InTx(ctx, pgx.ReadCommitted, func(tx pgx.Tx) error {
		var err error
		res, err = s.ApplySettlementTx(ctx, tx, externalID, transactionID, status, currency, amount)
		return err
	})
	return res, err
}

// ApplySettlementTx applies an event on an existing transaction so a dead-letter
// replay can commit the state change and the replay mark together.
func (s *Store) ApplySettlementTx(ctx context.Context, tx pgx.Tx, externalID, transactionID, status, currency string, amount int64) (SettlementResult, error) {
	if err := validateSettlement(externalID, transactionID, status, currency, amount); err != nil {
		return SettlementResult{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, hashtext($2))`, LockClassSettlement, externalID); err != nil {
		return SettlementResult{}, err
	}

	var got SettlementResult
	var gotAmount int64
	var gotCurrency, gotTxn string
	err := tx.QueryRow(ctx, `
		SELECT id, external_id, state::text, amount, currency, transaction_id
		FROM settlements WHERE external_id = $1 FOR UPDATE`, externalID).
		Scan(&got.ID, &got.ExternalID, &got.State, &gotAmount, &gotCurrency, &gotTxn)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return SettlementResult{}, err
	}
	exists := err == nil
	if exists {
		gotCurrency = strings.TrimRight(gotCurrency, " ")
		if gotAmount != amount || gotCurrency != currency || gotTxn != transactionID {
			return SettlementResult{}, ErrEventMismatch
		}
		if got.State == status {
			return got, nil
		}
		if got.State != "pending" {
			return SettlementResult{}, &TransitionError{From: got.State, To: status}
		}
	} else {
		var txnCurrency string
		err = tx.QueryRow(ctx, `SELECT currency FROM transactions WHERE id = $1`, transactionID).Scan(&txnCurrency)
		if errors.Is(err, pgx.ErrNoRows) {
			return SettlementResult{}, ErrTransactionNotFound
		}
		if err != nil {
			return SettlementResult{}, err
		}
		if strings.TrimRight(txnCurrency, " ") != currency {
			return SettlementResult{}, ErrCurrencyMismatch
		}
		newID := id.New("stl_")
		if _, err := tx.Exec(ctx, `
			INSERT INTO settlements (id, external_id, transaction_id, amount, currency)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (external_id) DO NOTHING`,
			newID, externalID, transactionID, amount, currency); err != nil {
			return SettlementResult{}, mapPG(err)
		}
		if err := tx.QueryRow(ctx, `
			SELECT id, external_id, state::text
			FROM settlements WHERE external_id = $1`, externalID).
			Scan(&got.ID, &got.ExternalID, &got.State); err != nil {
			return SettlementResult{}, err
		}
	}

	tag, err := tx.Exec(ctx, `
		UPDATE settlements SET state = $1::settlement_state
		WHERE external_id = $2 AND state = 'pending'`, status, externalID)
	if err != nil {
		return SettlementResult{}, mapPG(err)
	}
	if tag.RowsAffected() == 0 {
		var state string
		if err := tx.QueryRow(ctx, `SELECT state::text FROM settlements WHERE external_id = $1`, externalID).Scan(&state); err != nil {
			return SettlementResult{}, err
		}
		if state == status {
			got.State = state
			return got, nil
		}
		return SettlementResult{}, &TransitionError{From: state, To: status}
	}
	got.State = status
	got.Applied = true
	return got, nil
}

func validateSettlement(externalID, transactionID, status, currency string, amount int64) error {
	if strings.TrimSpace(externalID) == "" || strings.TrimSpace(transactionID) == "" {
		return &InvalidError{Msg: "external_id and transaction_id are required"}
	}
	if len(externalID) > 200 || len(transactionID) > 80 {
		return &InvalidError{Msg: "external_id or transaction_id is too long"}
	}
	if status != "settled" && status != "failed" {
		return &InvalidError{Msg: "status must be settled or failed"}
	}
	if amount <= 0 {
		return &InvalidError{Msg: "amount must be a positive number of minor units"}
	}
	if _, err := money.ParseCurrency(currency); err != nil {
		return &InvalidError{Msg: "currency must be three uppercase letters"}
	}
	return nil
}
