package money

import (
	"errors"
	"math"
	"testing"
)

func TestParseCurrency(t *testing.T) {
	c, err := ParseCurrency("ZAR")
	if err != nil || c != "ZAR" {
		t.Fatalf("ZAR: %v %v", c, err)
	}
	for _, bad := range []string{"", "za", "ZARR", "zar", "Z1R", "Z$R", "US"} {
		if _, err := ParseCurrency(bad); !errors.Is(err, ErrInvalidCurrency) {
			t.Fatalf("%q: got %v", bad, err)
		}
	}
}

func TestAddOverflow(t *testing.T) {
	sum, err := Add(math.MaxInt64, 1)
	if !errors.Is(err, ErrOverflow) || sum != 0 {
		t.Fatalf("max+1: %d %v", sum, err)
	}
	sum, err = Add(math.MinInt64, -1)
	if !errors.Is(err, ErrOverflow) || sum != 0 {
		t.Fatalf("min-1: %d %v", sum, err)
	}
	sum, err = Add(math.MaxInt64, -1)
	if err != nil || sum != math.MaxInt64-1 {
		t.Fatalf("max-1: %d %v", sum, err)
	}
	sum, err = Add(math.MinInt64, 1)
	if err != nil || sum != math.MinInt64+1 {
		t.Fatalf("min+1: %d %v", sum, err)
	}
	if _, err := Sum(Amount(math.MaxInt64), 1, -1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("sum overflow: %v", err)
	}
	got, err := Sum(15000, -4000, -11000)
	if err != nil || got != 0 {
		t.Fatalf("balanced sum: %d %v", got, err)
	}
}

func TestAddMoneyCurrency(t *testing.T) {
	a := Money{Amount: 100, Currency: "ZAR"}
	b := Money{Amount: -40, Currency: "USD"}
	if _, err := AddMoney(a, b); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("mismatch: %v", err)
	}
	got, err := AddMoney(a, Money{Amount: -25, Currency: "ZAR"})
	if err != nil || got.Amount != 75 || got.Currency != "ZAR" {
		t.Fatalf("add: %+v %v", got, err)
	}
	if _, err := AddMoney(Money{}, Money{Amount: 1, Currency: "ZAR"}); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("empty currency: %v", err)
	}
}
