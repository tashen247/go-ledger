package store

import (
	"context"
	"strings"

	"github.com/tashen247/go-ledger/internal/ledger"
)

// BalanceMismatch is a cached balance that disagrees with the entry sum.
type BalanceMismatch struct {
	AccountID string
	Cached    int64
	LedgerSum int64
}

// UnbalancedTxn is a transaction whose signed legs do not sum to zero.
type UnbalancedTxn struct {
	TransactionID string
	Sum           int64
}

// SettlementMismatch is a settled amount that disagrees with the positive legs.
type SettlementMismatch struct {
	SettlementID string
	Amount       int64
	LedgerSum    int64
}

// BalanceMismatches compares accounts.balance with SUM(ledger_entries.amount).
func (s *Store) BalanceMismatches(ctx context.Context) ([]BalanceMismatch, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.balance, COALESCE(SUM(e.amount), 0)::bigint
		FROM accounts a
		LEFT JOIN ledger_entries e ON e.account_id = a.id
		GROUP BY a.id, a.balance
		HAVING a.balance <> COALESCE(SUM(e.amount), 0)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BalanceMismatch
	for rows.Next() {
		var m BalanceMismatch
		if err := rows.Scan(&m.AccountID, &m.Cached, &m.LedgerSum); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if out == nil {
		out = []BalanceMismatch{}
	}
	return out, rows.Err()
}

// UnbalancedTransactions returns transactions that do not net to zero.
func (s *Store) UnbalancedTransactions(ctx context.Context) ([]UnbalancedTxn, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT transaction_id, SUM(amount)::bigint
		FROM ledger_entries
		GROUP BY transaction_id
		HAVING SUM(amount) <> 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnbalancedTxn
	for rows.Next() {
		var m UnbalancedTxn
		if err := rows.Scan(&m.TransactionID, &m.Sum); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if out == nil {
		out = []UnbalancedTxn{}
	}
	return out, rows.Err()
}

// SettlementMismatches compares settled amounts with the positive legs of the transaction.
func (s *Store) SettlementMismatches(ctx context.Context) ([]SettlementMismatch, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.id, s.amount, SUM(e.amount)::bigint
		FROM settlements s
		JOIN ledger_entries e ON e.transaction_id = s.transaction_id AND e.amount > 0
		WHERE s.state = 'settled'
		GROUP BY s.id, s.amount
		HAVING s.amount <> SUM(e.amount)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SettlementMismatch
	for rows.Next() {
		var m SettlementMismatch
		if err := rows.Scan(&m.SettlementID, &m.Amount, &m.LedgerSum); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if out == nil {
		out = []SettlementMismatch{}
	}
	return out, rows.Err()
}

// AccountCount is the number of accounts considered by reconciliation.
func (s *Store) AccountCount(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM accounts`).Scan(&n)
	return n, err
}

// ChainEntries loads the global chain in seq order.
func (s *Store) ChainEntries(ctx context.Context) ([]ledger.Entry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT seq, transaction_id, account_id, amount, currency, prev_hash, hash, created_at
		FROM ledger_entries
		ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.Entry
	for rows.Next() {
		var e ledger.Entry
		if err := rows.Scan(&e.Seq, &e.TransactionID, &e.AccountID, &e.Amount, &e.Currency, &e.PrevHash, &e.Hash, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Currency = strings.TrimRight(e.Currency, " ")
		e.CreatedAt = e.CreatedAt.UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
