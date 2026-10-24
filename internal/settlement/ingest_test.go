package settlement

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tashen247/go-ledger/internal/retry"
)

type fakeClock struct{ sleeps int }

func (f *fakeClock) After(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.sleeps++
	return nil
}

type zeroRand struct{}

func (zeroRand) Float64() float64 { return 0 }

type scriptedApply struct {
	err error
	n   int
}

func (s *scriptedApply) Apply(context.Context, Event) (Result, error) {
	s.n++
	if s.err != nil {
		return Result{}, s.err
	}
	return Result{ID: "stl_ok", ExternalID: "ext", State: "settled", Applied: true}, nil
}

type memPark struct {
	n        int
	attempts int
	payload  []byte
	id       string
}

func (m *memPark) Park(_ context.Context, _ string, payload []byte, _ string, attempts int) (string, error) {
	m.n++
	m.attempts = attempts
	m.payload = append([]byte(nil), payload...)
	m.id = "dl_test"
	return m.id, nil
}

func TestIngestParksAfterTransientExhaustion(t *testing.T) {
	app := &scriptedApply{err: &pgconn.PgError{Code: "40001", Message: "could not serialize access"}}
	park := &memPark{}
	clock := &fakeClock{}
	_, err := Ingest(context.Background(), app, park, retry.Config{MaxAttempts: 3, Base: time.Millisecond, Cap: time.Millisecond}, clock, zeroRand{}, Event{
		ExternalID: "psp-1", TransactionID: "txn_1", Status: "settled", Amount: 100, Currency: "ZAR",
	}, []byte(`{"external_id":"psp-1"}`))
	var parked *ParkedError
	if !errors.As(err, &parked) || !errors.Is(err, ErrParked) {
		t.Fatalf("err %v", err)
	}
	if parked.ID != "dl_test" || parked.Attempts != 3 {
		t.Fatalf("parked %+v", parked)
	}
	if app.n != 3 || park.n != 1 || park.attempts != 3 || clock.sleeps != 2 {
		t.Fatalf("calls app=%d park=%d sleeps=%d", app.n, park.n, clock.sleeps)
	}
	if string(park.payload) != `{"external_id":"psp-1"}` {
		t.Fatalf("payload %s", park.payload)
	}
}

func TestIngestDoesNotParkPermanentErrors(t *testing.T) {
	app := &scriptedApply{err: errors.New("illegal settlement transition settled -> failed")}
	park := &memPark{}
	_, err := Ingest(context.Background(), app, park, retry.Config{MaxAttempts: 4, Base: time.Second, Cap: time.Second}, &fakeClock{}, zeroRand{}, Event{}, nil)
	if err == nil || errors.Is(err, ErrParked) || park.n != 0 || app.n != 1 {
		t.Fatalf("err %v park %d calls %d", err, park.n, app.n)
	}
}

func TestIngestSuccess(t *testing.T) {
	res, err := Ingest(context.Background(), &scriptedApply{}, &memPark{}, retry.Config{MaxAttempts: 2, Base: time.Millisecond, Cap: time.Millisecond}, &fakeClock{}, zeroRand{}, Event{ExternalID: "ext"}, nil)
	if err != nil || !res.Applied || res.ID != "stl_ok" {
		t.Fatalf("%+v %v", res, err)
	}
}
