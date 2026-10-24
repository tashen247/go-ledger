package deadletter_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/tashen247/go-ledger/internal/deadletter"
	"github.com/tashen247/go-ledger/internal/store"
	"github.com/tashen247/go-ledger/internal/testkit"
)

func settlementPayload(t *testing.T, externalID, txnID, status string, amount int64) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"external_id": externalID, "transaction_id": txnID, "status": status, "amount": amount, "currency": "ZAR",
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParkListAndReplay(t *testing.T) {
	st := testkit.NewStore(t)
	svc := deadletter.Service{DB: st}
	ctx := context.Background()
	txnID := testkit.PostDeposit(t, st, 500)

	payload := settlementPayload(t, "psp-77", txnID, "settled", 500)
	id, err := svc.Park(ctx, "settlement_feed", payload, "connection reset by peer", 5)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("Park returned an empty id")
	}

	parked, err := svc.List(ctx, "parked")
	if err != nil {
		t.Fatal(err)
	}
	if len(parked) != 1 {
		t.Fatalf("parked %d, want 1", len(parked))
	}
	got := parked[0]
	if got.ID != id || got.Source != "settlement_feed" || got.Status != "parked" ||
		got.Attempts != 5 || got.LastError != "connection reset by peer" || got.CreatedAt.IsZero() {
		t.Fatalf("item %+v", got)
	}
	var back map[string]any
	if err := json.Unmarshal(got.Payload, &back); err != nil || back["external_id"] != "psp-77" {
		t.Fatalf("payload %s (%v)", got.Payload, err)
	}

	rep, err := svc.Replay(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		SettlementID string `json:"settlement_id"`
		Applied      bool   `json:"applied"`
	}
	if err := json.Unmarshal(rep.Result, &result); err != nil {
		t.Fatal(err)
	}
	if rep.ID != id || rep.Status != "replayed" || !result.Applied || result.SettlementID == "" {
		t.Fatalf("replay %+v result %+v", rep, result)
	}

	// A second replay returns the stored result and does not apply the event again.
	again, err := svc.Replay(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != "replayed" || string(again.Result) != string(rep.Result) {
		t.Fatalf("second replay %+v, want stored result %s", again, rep.Result)
	}
	var n int
	if err := st.Pool().QueryRow(ctx, `SELECT count(*) FROM settlements WHERE external_id = 'psp-77'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("settlements %d, want 1", n)
	}

	if parked, err := svc.List(ctx, "parked"); err != nil || len(parked) != 0 {
		t.Fatalf("parked after replay: %d %v", len(parked), err)
	}
	if replayed, err := svc.List(ctx, "replayed"); err != nil || len(replayed) != 1 {
		t.Fatalf("replayed: %d %v", len(replayed), err)
	}
	if all, err := svc.List(ctx, ""); err != nil || len(all) != 1 {
		t.Fatalf("all: %d %v", len(all), err)
	}
}

func TestParkDefaults(t *testing.T) {
	st := testkit.NewStore(t)
	svc := deadletter.Service{DB: st}
	ctx := context.Background()

	id, err := svc.Park(ctx, "", []byte(`{"external_id":"x"}`), "boom", 0)
	if err != nil {
		t.Fatal(err)
	}
	items, err := svc.List(ctx, "")
	if err != nil || len(items) != 1 {
		t.Fatalf("items %d %v", len(items), err)
	}
	if items[0].ID != id || items[0].Source != "settlement_feed" || items[0].Attempts != 1 {
		t.Fatalf("defaults not applied: %+v", items[0])
	}
}

func TestParkRejectsNonJSONPayload(t *testing.T) {
	svc := deadletter.Service{DB: testkit.NewStore(t)}
	if _, err := svc.Park(context.Background(), "settlement_feed", []byte("not json"), "boom", 1); err == nil {
		t.Fatal("expected an error for a payload that is not JSON")
	}
}

func TestListEmptyIsNotNil(t *testing.T) {
	svc := deadletter.Service{DB: testkit.NewStore(t)}
	items, err := svc.List(context.Background(), "parked")
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("items %#v err %v", items, err)
	}
}

func TestReplayUnknownID(t *testing.T) {
	svc := deadletter.Service{DB: testkit.NewStore(t)}
	if _, err := svc.Replay(context.Background(), "dl_does_not_exist"); err == nil {
		t.Fatal("expected an error for an unknown id")
	}
}

func TestReplayInvalidPayloadStaysParked(t *testing.T) {
	st := testkit.NewStore(t)
	svc := deadletter.Service{DB: st}
	ctx := context.Background()

	id, err := svc.Park(ctx, "settlement_feed", []byte(`{"unexpected_field":1}`), "boom", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Replay(ctx, id); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("err %v, want store.ErrInvalid", err)
	}
	parked, err := svc.List(ctx, "parked")
	if err != nil || len(parked) != 1 || parked[0].ID != id {
		t.Fatalf("letter should still be parked: %+v %v", parked, err)
	}
}

func TestReplayRejectedSettlementRollsBack(t *testing.T) {
	st := testkit.NewStore(t)
	svc := deadletter.Service{DB: st}
	ctx := context.Background()
	txnID := testkit.PostDeposit(t, st, 300)

	// Settle first, then park a contradicting "failed" event for the same payment.
	if _, err := st.ApplySettlement(ctx, "psp-9", txnID, "settled", "ZAR", 300); err != nil {
		t.Fatal(err)
	}
	id, err := svc.Park(ctx, "settlement_feed", settlementPayload(t, "psp-9", txnID, "failed", 300), "timeout", 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Replay(ctx, id); !errors.Is(err, store.ErrIllegalTransition) {
		t.Fatalf("err %v, want store.ErrIllegalTransition", err)
	}
	parked, err := svc.List(ctx, "parked")
	if err != nil || len(parked) != 1 {
		t.Fatalf("letter should still be parked after a rejected replay: %d %v", len(parked), err)
	}
}
