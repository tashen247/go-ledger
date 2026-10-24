package store

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tashen247/go-ledger/internal/id"
	"github.com/tashen247/go-ledger/internal/money"
)

var accountName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

var accountTypes = map[string]struct{}{
	"asset": {}, "liability": {}, "equity": {}, "revenue": {}, "expense": {},
}

// Account is a ledger account. Balance is a cache of the signed entry sum.
type Account struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Currency  string    `json:"currency"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
}

// Balance is the cached minor-unit balance and the latest entry seq that
// contributed to it. AsOfSeq is 0 when the account has no entries.
type Balance struct {
	AccountID string `json:"account_id"`
	Currency  string `json:"currency"`
	Balance   int64  `json:"balance"`
	AsOfSeq   int64  `json:"as_of_seq"`
}

// CreateAccount inserts an account. The currency is fixed for the life of the account.
func (s *Store) CreateAccount(ctx context.Context, name, currency, typ string) (Account, error) {
	if !accountName.MatchString(name) {
		return Account{}, &InvalidError{Msg: "name must be 1-64 characters of letters, digits, '_', '.' or '-'"}
	}
	cur, err := money.ParseCurrency(currency)
	if err != nil {
		return Account{}, &InvalidError{Msg: "currency must be three uppercase letters"}
	}
	if _, ok := accountTypes[typ]; !ok {
		return Account{}, &InvalidError{Msg: "type must be asset, liability, equity, revenue, or expense"}
	}
	acct := Account{
		ID:       id.New("acc_"),
		Name:     name,
		Currency: string(cur),
		Type:     typ,
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO accounts (id, name, currency, type)
		VALUES ($1, $2, $3, $4)
		RETURNING created_at`, acct.ID, acct.Name, acct.Currency, acct.Type).Scan(&acct.CreatedAt)
	if err != nil {
		return Account{}, mapPG(err)
	}
	acct.Currency = strings.TrimRight(acct.Currency, " ")
	acct.CreatedAt = acct.CreatedAt.UTC()
	return acct, nil
}

// AccountBalance returns the cached balance for id.
func (s *Store) AccountBalance(ctx context.Context, accountID string) (Balance, error) {
	var bal Balance
	err := s.pool.QueryRow(ctx, `
		SELECT a.id, a.currency, a.balance, COALESCE(MAX(e.seq), 0)
		FROM accounts a
		LEFT JOIN ledger_entries e ON e.account_id = a.id
		WHERE a.id = $1
		GROUP BY a.id, a.currency, a.balance`, accountID).Scan(&bal.AccountID, &bal.Currency, &bal.Balance, &bal.AsOfSeq)
	if errors.Is(err, pgx.ErrNoRows) {
		return Balance{}, ErrAccountNotFound
	}
	if err != nil {
		return Balance{}, err
	}
	bal.Currency = strings.TrimRight(bal.Currency, " ")
	return bal, nil
}
