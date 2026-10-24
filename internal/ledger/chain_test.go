package ledger

import (
	"bytes"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/tashen247/go-ledger/internal/money"
)

func TestCanonicalStable(t *testing.T) {
	ts := time.Date(2026, 10, 24, 8, 0, 1, 250000000, time.FixedZone("SAST", 2*3600))
	e := Entry{
		Seq: 1041, TransactionID: "txn_a", AccountID: "acc_cash",
		Amount: 15000, Currency: "ZAR", CreatedAt: ts,
	}
	got := string(Canonical(e))
	want := "v1|1041|txn_a|acc_cash|15000|ZAR|2026-10-24T06:00:01.25Z"
	if got != want {
		t.Fatalf("canonical\n got %s\nwant %s", got, want)
	}
	if !bytes.Equal(Canonical(e), Canonical(e)) {
		t.Fatal("canonical not stable")
	}
}

func TestHashChainRoundTrip(t *testing.T) {
	key := []byte("test-hmac-key-32-bytes-long!!")
	ts := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	prev := Genesis()
	if len(prev) != 32 || !bytes.Equal(prev, make([]byte, 32)) {
		t.Fatal("genesis")
	}
	var entries []Entry
	legs := []struct {
		seq  int64
		acct string
		amt  int64
	}{
		{1, "acc_cash", 15000},
		{2, "acc_alice", -15000},
	}
	for _, leg := range legs {
		e := Entry{
			Seq: leg.seq, TransactionID: "txn_1", AccountID: leg.acct,
			Amount: leg.amt, Currency: "ZAR", CreatedAt: ts, PrevHash: append([]byte(nil), prev...),
		}
		e.Hash = Hash(key, e.PrevHash, Canonical(e))
		entries = append(entries, e)
		prev = e.Hash
	}
	n, br := Verify(key, entries)
	if br != nil || n != 2 {
		t.Fatalf("verify: n=%d break=%v", n, br)
	}

	tampered := append([]Entry(nil), entries...)
	tampered[1].Amount = -14999
	n, br = Verify(key, tampered)
	if br == nil || *br != 2 || n != 1 {
		t.Fatalf("amount tamper: n=%d break=%v", n, br)
	}

	relinked := append([]Entry(nil), entries...)
	relinked[1].PrevHash = Genesis()
	n, br = Verify(key, relinked)
	if br == nil || *br != 2 || n != 1 {
		t.Fatalf("prev tamper: n=%d break=%v", n, br)
	}

	wrongKey := VerifyKey(entries, []byte("other-key-other-key-other-key"))
	if wrongKey == nil {
		t.Fatal("expected break under a different key")
	}
}

func VerifyKey(entries []Entry, key []byte) *int64 {
	_, br := Verify(key, entries)
	return br
}

func TestVerifyEmpty(t *testing.T) {
	n, br := Verify([]byte("k"), nil)
	if n != 0 || br != nil {
		t.Fatalf("empty: %d %v", n, br)
	}
}

func TestValidate(t *testing.T) {
	ok := []Leg{{15000, "ZAR"}, {-15000, "ZAR"}}
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
	if err := Validate([]Leg{{100, "ZAR"}, {-40, "ZAR"}, {-60, "ZAR"}}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		legs []Leg
		err  error
	}{
		{"one", []Leg{{100, "ZAR"}}, ErrTooFewEntries},
		{"zero", []Leg{{0, "ZAR"}, {0, "ZAR"}}, ErrZeroAmount},
		{"mixed", []Leg{{100, "ZAR"}, {-100, "USD"}}, ErrMixedCurrency},
		{"unbalanced", []Leg{{100, "ZAR"}, {-90, "ZAR"}}, ErrUnbalanced},
		{"overflow", []Leg{{math.MaxInt64, "ZAR"}, {1, "ZAR"}}, money.ErrOverflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.legs)
			if !errors.Is(err, tc.err) {
				t.Fatalf("got %v want %v", err, tc.err)
			}
		})
	}
}

func TestGenesisNotShared(t *testing.T) {
	a := Genesis()
	a[0] = 1
	b := Genesis()
	if b[0] != 0 {
		t.Fatal("genesis slice was shared")
	}
}
