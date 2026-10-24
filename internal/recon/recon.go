// Package recon compares cached balances, transaction nets, settlements, and
// the hash chain. Every comparison is exact. There is no tolerance.
package recon

import (
	"context"
	"time"

	"github.com/tashen247/go-ledger/internal/ledger"
	"github.com/tashen247/go-ledger/internal/store"
)

// Report is the body of GET /reconciliation.
type Report struct {
	OK                     bool                 `json:"ok"`
	CheckedAt              time.Time            `json:"checked_at"`
	AccountsChecked        int                  `json:"accounts_checked"`
	BalanceMismatches      []BalanceMismatch    `json:"balance_mismatches"`
	UnbalancedTransactions []Unbalanced         `json:"unbalanced_transactions"`
	SettlementMismatches   []SettlementMismatch `json:"settlement_mismatches"`
	Chain                  Chain                `json:"chain"`
}

// BalanceMismatch is one account whose cache disagrees with the ledger.
type BalanceMismatch struct {
	AccountID string `json:"account_id"`
	Cached    int64  `json:"cached"`
	LedgerSum int64  `json:"ledger_sum"`
}

// Unbalanced is one transaction that does not net to zero.
type Unbalanced struct {
	TransactionID string `json:"transaction_id"`
	Sum           int64  `json:"sum"`
}

// SettlementMismatch is one settled amount that disagrees with the positive legs.
type SettlementMismatch struct {
	SettlementID string `json:"settlement_id"`
	Amount       int64  `json:"amount"`
	LedgerSum    int64  `json:"ledger_sum"`
}

// Chain is the hash-chain verification summary.
type Chain struct {
	VerifiedEntries int64  `json:"verified_entries"`
	FirstBreakSeq   *int64 `json:"first_break_seq"`
}

// Run executes every check against the current database.
func Run(ctx context.Context, db *store.Store) (Report, error) {
	report := Report{
		CheckedAt:              time.Now().UTC(),
		BalanceMismatches:      []BalanceMismatch{},
		UnbalancedTransactions: []Unbalanced{},
		SettlementMismatches:   []SettlementMismatch{},
	}
	n, err := db.AccountCount(ctx)
	if err != nil {
		return Report{}, err
	}
	report.AccountsChecked = n

	balances, err := db.BalanceMismatches(ctx)
	if err != nil {
		return Report{}, err
	}
	for _, m := range balances {
		report.BalanceMismatches = append(report.BalanceMismatches, BalanceMismatch{
			AccountID: m.AccountID, Cached: m.Cached, LedgerSum: m.LedgerSum,
		})
	}
	unbalanced, err := db.UnbalancedTransactions(ctx)
	if err != nil {
		return Report{}, err
	}
	for _, m := range unbalanced {
		report.UnbalancedTransactions = append(report.UnbalancedTransactions, Unbalanced{
			TransactionID: m.TransactionID, Sum: m.Sum,
		})
	}
	settlements, err := db.SettlementMismatches(ctx)
	if err != nil {
		return Report{}, err
	}
	for _, m := range settlements {
		report.SettlementMismatches = append(report.SettlementMismatches, SettlementMismatch{
			SettlementID: m.SettlementID, Amount: m.Amount, LedgerSum: m.LedgerSum,
		})
	}
	entries, err := db.ChainEntries(ctx)
	if err != nil {
		return Report{}, err
	}
	verified, first := ledger.Verify(db.HMACKey(), entries)
	report.Chain = Chain{VerifiedEntries: verified, FirstBreakSeq: first}
	report.OK = len(report.BalanceMismatches) == 0 &&
		len(report.UnbalancedTransactions) == 0 &&
		len(report.SettlementMismatches) == 0 &&
		report.Chain.FirstBreakSeq == nil
	return report, nil
}
