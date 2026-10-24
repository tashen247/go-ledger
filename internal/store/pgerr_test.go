package store

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tashen247/go-ledger/internal/money"
)

func TestMapPGOverflowWrapsMoney(t *testing.T) {
	err := mapPG(&pgconn.PgError{Code: "22003", Message: "bigint out of range"})
	if !errors.Is(err, money.ErrOverflow) {
		t.Fatalf("overflow: %v", err)
	}
	var overflow overflowError
	if !errors.As(err, &overflow) {
		t.Fatalf("expected overflowError, got %T", err)
	}
}

func TestMapPGConstraintAndTransition(t *testing.T) {
	dup := mapPG(&pgconn.PgError{Code: "23505", ConstraintName: "accounts_name_key"})
	if !errors.Is(dup, ErrDuplicateAccount) {
		t.Fatalf("dup: %v", dup)
	}
	other := mapPG(&pgconn.PgError{Code: "23505", ConstraintName: "ledger_entries_hash_key"})
	if errors.Is(other, ErrDuplicateAccount) {
		t.Fatalf("hash unique mapped as duplicate account: %v", other)
	}
	fk := mapPG(&pgconn.PgError{Code: "23503", ConstraintName: "ledger_entries_account_id_fkey"})
	if !errors.Is(fk, ErrUnknownAccount) {
		t.Fatalf("fk: %v", fk)
	}
	tr := mapPG(&pgconn.PgError{Code: "P0001", Message: "illegal settlement transition settled -> failed"})
	var te *TransitionError
	if !errors.As(tr, &te) || te.From != "settled" || te.To != "failed" || !errors.Is(tr, ErrIllegalTransition) {
		t.Fatalf("transition: %v", tr)
	}
	if errors.Is(mapPG(&pgconn.PgError{Code: "40001"}), money.ErrOverflow) {
		t.Fatal("serialization failure was rewritten")
	}
}
