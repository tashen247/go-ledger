// Package ledger implements double-entry validation and the HMAC hash chain.
//
// Each entry is bound to its predecessor:
//
//	hash = HMAC-SHA256(key, prev_hash || canonical(entry))
//
// canonical is the UTF-8 string
//
//	v1|{seq}|{transaction_id}|{account_id}|{amount}|{currency}|{created_at}
//
// created_at is RFC3339Nano in UTC. The genesis prev_hash is 32 zero bytes.
// One global chain is kept; writers take a transaction-scoped advisory lock
// so the predecessor they read is the latest committed head.
package ledger

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/tashen247/go-ledger/internal/money"
)

var (
	// ErrTooFewEntries means a transaction did not carry at least two legs.
	ErrTooFewEntries = errors.New("at least two entries are required")
	// ErrZeroAmount means a leg was zero. Zero legs are rejected so a
	// transaction cannot hide an empty posting.
	ErrZeroAmount = errors.New("amount must be non-zero")
	// ErrMixedCurrency means the accounts in one transaction do not share a currency.
	ErrMixedCurrency = errors.New("mixed currencies")
	// ErrUnbalanced means the signed legs do not sum to zero.
	ErrUnbalanced = errors.New("entries do not net to zero")
)

// Entry is one link in the chain. Hashes are raw HMAC-SHA256 bytes.
type Entry struct {
	Seq           int64
	TransactionID string
	AccountID     string
	Amount        int64
	Currency      string
	CreatedAt     time.Time
	PrevHash      []byte
	Hash          []byte
}

// Genesis is the prev_hash of the first entry: 32 zero bytes.
func Genesis() []byte {
	return make([]byte, sha256.Size)
}

// FormatTime renders a timestamp the way the chain canonical form expects.
func FormatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// Canonical returns the byte string covered by the HMAC.
func Canonical(e Entry) []byte {
	var buf []byte
	buf = append(buf, "v1|"...)
	buf = strconv.AppendInt(buf, e.Seq, 10)
	buf = append(buf, '|')
	buf = append(buf, e.TransactionID...)
	buf = append(buf, '|')
	buf = append(buf, e.AccountID...)
	buf = append(buf, '|')
	buf = strconv.AppendInt(buf, e.Amount, 10)
	buf = append(buf, '|')
	buf = append(buf, e.Currency...)
	buf = append(buf, '|')
	buf = append(buf, FormatTime(e.CreatedAt)...)
	return buf
}

// Hash returns HMAC-SHA256(key, prevHash || canonical).
func Hash(key, prevHash, canonical []byte) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write(prevHash)
	_, _ = m.Write(canonical)
	return m.Sum(nil)
}

// Leg is one side of a posting, with the account currency already resolved.
type Leg struct {
	Amount   money.Amount
	Currency string
}

// Validate checks double-entry rules: at least two non-zero legs, one
// currency, and a sum of exactly zero. Overflow is money.ErrOverflow.
func Validate(legs []Leg) error {
	if len(legs) < 2 {
		return ErrTooFewEntries
	}
	cur := legs[0].Currency
	if cur == "" {
		return ErrMixedCurrency
	}
	var sum money.Amount
	for _, leg := range legs {
		if leg.Currency != cur {
			return ErrMixedCurrency
		}
		if leg.Amount == 0 {
			return ErrZeroAmount
		}
		next, err := money.Add(sum, leg.Amount)
		if err != nil {
			return err
		}
		sum = next
	}
	if sum != 0 {
		return ErrUnbalanced
	}
	return nil
}

// Verify recomputes the chain in seq order.
// verified is how many leading entries matched.
// firstBreak is the seq of the first entry that did not, or nil when the
// chain is intact (including when it is empty).
func Verify(key []byte, entries []Entry) (verified int64, firstBreak *int64) {
	prev := Genesis()
	for i := range entries {
		e := entries[i]
		if !hmacEqual(e.PrevHash, prev) || !hmacEqual(e.Hash, Hash(key, e.PrevHash, Canonical(e))) {
			seq := e.Seq
			return int64(i), &seq
		}
		prev = e.Hash
	}
	return int64(len(entries)), nil
}

func hmacEqual(a, b []byte) bool {
	// hmac.Equal requires equal lengths; treat a length mismatch as a break.
	if len(a) != len(b) {
		return false
	}
	return hmac.Equal(a, b)
}

// ChainError is a human-readable description of a break. cmd/verify prints it.
func ChainError(verified int64, firstBreak *int64) string {
	if firstBreak == nil {
		return fmt.Sprintf("ok verified_entries=%d", verified)
	}
	return fmt.Sprintf("chain broken at seq %d after %d verified entries", *firstBreak, verified)
}
