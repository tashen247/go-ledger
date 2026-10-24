package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tashen247/go-ledger/internal/id"
	"github.com/tashen247/go-ledger/internal/idempotency"
	"github.com/tashen247/go-ledger/internal/ledger"
	"github.com/tashen247/go-ledger/internal/money"
)

// Leg is one side of a transaction, in minor units.
type Leg struct {
	AccountID string
	Amount    int64
}

// PostCommand is an idempotent double-entry post.
// RequestHash is the SHA-256 of the raw HTTP body.
type PostCommand struct {
	Key         string
	RequestHash []byte
	Reference   string
	Entries     []Leg
}

// PostResult is the HTTP payload to write. Body is the exact JSON that was
// stored, so a replay cannot drift from the original response.
type PostResult struct {
	Status   int
	Body     []byte
	Replayed bool
}

type txResponse struct {
	ID        string          `json:"id"`
	Reference string          `json:"reference"`
	Currency  string          `json:"currency"`
	Entries   []entryResponse `json:"entries"`
	CreatedAt time.Time       `json:"created_at"`
}

type entryResponse struct {
	Seq       int64  `json:"seq"`
	AccountID string `json:"account_id"`
	Amount    int64  `json:"amount"`
	Hash      string `json:"hash"`
}

type lockedAccount struct {
	currency string
	balance  int64
}

// Post writes a balanced transaction and its idempotency row in one
// Read Committed transaction. The chain advisory lock is taken before the
// head hash is read, so the new links extend the latest committed head.
func (s *Store) Post(ctx context.Context, cmd PostCommand) (PostResult, error) {
	if strings.TrimSpace(cmd.Key) == "" {
		return PostResult{}, ErrIdempotencyRequired
	}
	if len(cmd.Key) > 200 {
		return PostResult{}, &InvalidError{Msg: "idempotency key must be at most 200 characters"}
	}
	ref := strings.TrimSpace(cmd.Reference)
	if ref == "" || len(ref) > 256 {
		return PostResult{}, &InvalidError{Msg: "reference is required and must be at most 256 characters"}
	}
	if len(cmd.Entries) < 2 {
		return PostResult{}, ledger.ErrTooFewEntries
	}
	for _, leg := range cmd.Entries {
		if strings.TrimSpace(leg.AccountID) == "" {
			return PostResult{}, &InvalidError{Msg: "account_id is required"}
		}
	}

	var out PostResult
	err := s.InTx(ctx, pgx.ReadCommitted, func(tx pgx.Tx) error {
		var locked bool
		if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1, hashtext($2))`, LockClassIdempotency, cmd.Key).Scan(&locked); err != nil {
			return err
		}
		if !locked {
			return ErrIdempotencyInProgress
		}

		row, err := loadIdempotency(ctx, tx, cmd.Key)
		if err != nil {
			return err
		}
		switch idempotency.Decide(row.exists, idempotency.Status(row.status), row.hash, cmd.RequestHash) {
		case idempotency.Replay:
			out = PostResult{Status: row.code, Body: append([]byte(nil), row.body...), Replayed: true}
			return nil
		case idempotency.Mismatch:
			return ErrIdempotencyReuse
		case idempotency.InFlight:
			return ErrIdempotencyInProgress
		}

		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, 1)`, LockClassChain); err != nil {
			return err
		}

		ids := uniqueAccountIDs(cmd.Entries)
		accounts, err := lockAccounts(ctx, tx, ids)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, ok := accounts[id]; !ok {
				return &UnknownAccountError{ID: id}
			}
		}

		legs := make([]ledger.Leg, len(cmd.Entries))
		for i, leg := range cmd.Entries {
			legs[i] = ledger.Leg{Amount: money.Amount(leg.Amount), Currency: accounts[leg.AccountID].currency}
		}
		if err := ledger.Validate(legs); err != nil {
			return err
		}
		currency := legs[0].Currency

		deltas := map[string]money.Amount{}
		for _, leg := range cmd.Entries {
			next, err := money.Add(deltas[leg.AccountID], money.Amount(leg.Amount))
			if err != nil {
				return err
			}
			deltas[leg.AccountID] = next
		}
		for acct, delta := range deltas {
			if _, err := money.Add(money.Amount(accounts[acct].balance), delta); err != nil {
				return err
			}
		}

		prev, err := chainHead(ctx, tx)
		if err != nil {
			return err
		}
		ts := time.Now().UTC().Truncate(time.Microsecond)
		txnID := id.New("txn_")
		if _, err := tx.Exec(ctx, `
			INSERT INTO transactions (id, reference, currency, created_at)
			VALUES ($1, $2, $3, $4)`, txnID, ref, currency, ts); err != nil {
			return err
		}

		entries := make([]entryResponse, 0, len(cmd.Entries))
		for _, leg := range cmd.Entries {
			var seq int64
			if err := tx.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('ledger_entries', 'seq'))`).Scan(&seq); err != nil {
				return err
			}
			entry := ledger.Entry{
				Seq:           seq,
				TransactionID: txnID,
				AccountID:     leg.AccountID,
				Amount:        leg.Amount,
				Currency:      currency,
				CreatedAt:     ts,
				PrevHash:      prev,
			}
			sum := ledger.Hash(s.hmacKey, entry.PrevHash, ledger.Canonical(entry))
			entry.Hash = sum
			if _, err := tx.Exec(ctx, `
				INSERT INTO ledger_entries
					(seq, transaction_id, account_id, amount, currency, prev_hash, hash, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				entry.Seq, entry.TransactionID, entry.AccountID, entry.Amount, entry.Currency,
				entry.PrevHash, entry.Hash, entry.CreatedAt); err != nil {
				return err
			}
			prev = append([]byte(nil), sum...)
			entries = append(entries, entryResponse{
				Seq: seq, AccountID: leg.AccountID, Amount: leg.Amount, Hash: hex.EncodeToString(sum),
			})
		}
		for acct, delta := range deltas {
			if _, err := tx.Exec(ctx, `UPDATE accounts SET balance = balance + $2 WHERE id = $1`, acct, int64(delta)); err != nil {
				return mapPG(err)
			}
		}

		body, err := json.Marshal(txResponse{
			ID: txnID, Reference: ref, Currency: currency, Entries: entries, CreatedAt: ts,
		})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO idempotency_keys (key, request_hash, status, response_code, response_body, transaction_id)
			VALUES ($1, $2, 'completed', 201, to_jsonb($3::text), $4)`,
			cmd.Key, cmd.RequestHash, string(body), txnID); err != nil {
			return err
		}
		out = PostResult{Status: 201, Body: body, Replayed: false}
		return nil
	})
	return out, err
}

type idempotencyRow struct {
	exists bool
	hash   []byte
	status string
	code   int
	body   []byte
}

func loadIdempotency(ctx context.Context, tx pgx.Tx, key string) (idempotencyRow, error) {
	var row idempotencyRow
	var code *int
	var body []byte
	err := tx.QueryRow(ctx, `
		SELECT request_hash, status, response_code, response_body #>> '{}'
		FROM idempotency_keys WHERE key = $1`, key).Scan(&row.hash, &row.status, &code, &body)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return idempotencyRow{}, nil
		}
		return idempotencyRow{}, err
	}
	row.exists = true
	row.body = body
	if code != nil {
		row.code = *code
	}
	return row, nil
}

func uniqueAccountIDs(legs []Leg) []string {
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(legs))
	for _, leg := range legs {
		if _, ok := seen[leg.AccountID]; ok {
			continue
		}
		seen[leg.AccountID] = struct{}{}
		ids = append(ids, leg.AccountID)
	}
	return ids
}

func lockAccounts(ctx context.Context, tx pgx.Tx, ids []string) (map[string]lockedAccount, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, currency, balance
		FROM accounts
		WHERE id = ANY($1)
		ORDER BY id
		FOR UPDATE`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]lockedAccount, len(ids))
	for rows.Next() {
		var id, currency string
		var balance int64
		if err := rows.Scan(&id, &currency, &balance); err != nil {
			return nil, err
		}
		out[id] = lockedAccount{currency: strings.TrimRight(currency, " "), balance: balance}
	}
	return out, rows.Err()
}

func chainHead(ctx context.Context, tx pgx.Tx) ([]byte, error) {
	var hash []byte
	err := tx.QueryRow(ctx, `SELECT hash FROM ledger_entries ORDER BY seq DESC LIMIT 1`).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ledger.Genesis(), nil
	}
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), hash...), nil
}
