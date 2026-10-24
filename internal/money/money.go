// Package money represents currency amounts as integer minor units.
// Floating point never enters a balance.
package money

import (
	"errors"
	"fmt"
	"math"
	"unicode"
)

// Amount is a signed quantity of minor units (cents, for currencies with
// exponent 2). The type carries no currency; callers pair it with one.
type Amount int64

// Currency is an ISO-4217 alphabetic code.
type Currency string

var (
	// ErrOverflow is returned when adding two amounts would exceed int64.
	ErrOverflow = errors.New("amount overflow")
	// ErrCurrencyMismatch is returned when arithmetic mixes currencies.
	ErrCurrencyMismatch = errors.New("currency mismatch")
	// ErrInvalidCurrency is returned when a code is not three ASCII letters A-Z.
	ErrInvalidCurrency = errors.New("invalid currency")
)

// Money is an amount bound to a single currency.
type Money struct {
	Amount   Amount
	Currency Currency
}

// ParseCurrency checks an ISO-4217 alphabetic code. It does not consult a
// table of currently assigned codes; the ledger accepts any three letters
// so tests and new currencies do not need a code change.
func ParseCurrency(s string) (Currency, error) {
	if len(s) != 3 {
		return "", fmt.Errorf("%w: %q", ErrInvalidCurrency, s)
	}
	for _, r := range s {
		if r > unicode.MaxASCII || r < 'A' || r > 'Z' {
			return "", fmt.Errorf("%w: %q", ErrInvalidCurrency, s)
		}
	}
	return Currency(s), nil
}

// Add returns a+b or ErrOverflow. It does not wrap.
func Add(a, b Amount) (Amount, error) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, ErrOverflow
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, ErrOverflow
	}
	return a + b, nil
}

// Sum adds amounts in order.
func Sum(amounts ...Amount) (Amount, error) {
	var total Amount
	for _, a := range amounts {
		next, err := Add(total, a)
		if err != nil {
			return 0, err
		}
		total = next
	}
	return total, nil
}

// AddMoney adds two money values. Currencies must match.
func AddMoney(a, b Money) (Money, error) {
	if a.Currency == "" || a.Currency != b.Currency {
		return Money{}, ErrCurrencyMismatch
	}
	sum, err := Add(a.Amount, b.Amount)
	if err != nil {
		return Money{}, err
	}
	return Money{Amount: sum, Currency: a.Currency}, nil
}
