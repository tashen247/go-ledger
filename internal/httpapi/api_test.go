package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"log/slog"

	"github.com/tashen247/go-ledger/internal/retry"
	"github.com/tashen247/go-ledger/internal/settlement"
	"github.com/tashen247/go-ledger/internal/store"
	"github.com/tashen247/go-ledger/internal/testkit"
)

func newServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st := testkit.NewStore(t)
	h := New(Deps{
		Store:  st,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Retry:  retry.Config{MaxAttempts: 3, Base: time.Millisecond, Cap: 5 * time.Millisecond},
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, st
}

func do(t *testing.T, method, url string, headers map[string]string, body any) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		switch b := body.(type) {
		case []byte:
			rdr = bytes.NewReader(b)
		default:
			raw, err := json.Marshal(b)
			if err != nil {
				t.Fatal(err)
			}
			rdr = bytes.NewReader(raw)
		}
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, raw
}

func createAccount(t *testing.T, srv *httptest.Server, name, currency, typ string) string {
	t.Helper()
	res, raw := do(t, http.MethodPost, srv.URL+"/accounts", nil, map[string]any{
		"name": name, "currency": currency, "type": typ,
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %s: %d %s", name, res.StatusCode, raw)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out.ID
}

func TestPostBalanceReplayAndRecon(t *testing.T) {
	srv, _ := newServer(t)
	cash := createAccount(t, srv, "cash-box", "ZAR", "asset")
	alice := createAccount(t, srv, "alice-wallet", "ZAR", "liability")

	body := []byte(`{"reference":"deposit-123","entries":[{"account_id":"` + cash + `","amount":15000},{"account_id":"` + alice + `","amount":-15000}]}`)
	res, raw := do(t, http.MethodPost, srv.URL+"/transactions", map[string]string{"Idempotency-Key": "deposit-123"}, body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("post: %d %s", res.StatusCode, raw)
	}
	if res.Header.Get("Idempotent-Replayed") != "" {
		t.Fatal("first response was marked replayed")
	}
	var txn struct {
		ID       string `json:"id"`
		Currency string `json:"currency"`
		Entries  []struct {
			Seq    int64  `json:"seq"`
			Amount int64  `json:"amount"`
			Hash   string `json:"hash"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &txn); err != nil {
		t.Fatal(err)
	}
	if txn.Currency != "ZAR" || len(txn.Entries) != 2 || len(txn.Entries[0].Hash) != 64 {
		t.Fatalf("txn %+v", txn)
	}

	res, again := do(t, http.MethodPost, srv.URL+"/transactions", map[string]string{"Idempotency-Key": "deposit-123"}, body)
	if res.StatusCode != http.StatusCreated || res.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay: %d header %q %s", res.StatusCode, res.Header.Get("Idempotent-Replayed"), again)
	}
	if !bytes.Equal(raw, again) {
		t.Fatalf("replay body differs\n%s\n%s", raw, again)
	}

	other := bytes.Replace(body, []byte("deposit-123"), []byte("deposit-999"), 1)
	res, raw = do(t, http.MethodPost, srv.URL+"/transactions", map[string]string{"Idempotency-Key": "deposit-123"}, other)
	if res.StatusCode != http.StatusUnprocessableEntity || !bytes.Contains(raw, []byte("idempotency-key-reuse")) {
		t.Fatalf("reuse: %d %s", res.StatusCode, raw)
	}

	res, raw = do(t, http.MethodGet, srv.URL+"/accounts/"+alice+"/balance", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("balance: %d %s", res.StatusCode, raw)
	}
	var bal struct {
		Balance int64 `json:"balance"`
		AsOfSeq int64 `json:"as_of_seq"`
	}
	if err := json.Unmarshal(raw, &bal); err != nil {
		t.Fatal(err)
	}
	if bal.Balance != -15000 || bal.AsOfSeq == 0 {
		t.Fatalf("balance %+v", bal)
	}

	res, raw = do(t, http.MethodPost, srv.URL+"/settlements", nil, map[string]any{
		"external_id": "psp-evt-8812", "transaction_id": txn.ID, "status": "settled", "amount": 15000, "currency": "ZAR",
	})
	if res.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte(`"applied":true`)) {
		t.Fatalf("settle: %d %s", res.StatusCode, raw)
	}
	res, raw = do(t, http.MethodPost, srv.URL+"/settlements", nil, map[string]any{
		"external_id": "psp-evt-8812", "transaction_id": txn.ID, "status": "settled", "amount": 15000, "currency": "ZAR",
	})
	if res.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte(`"applied":false`)) {
		t.Fatalf("duplicate settle: %d %s", res.StatusCode, raw)
	}
	res, raw = do(t, http.MethodPost, srv.URL+"/settlements", nil, map[string]any{
		"external_id": "psp-evt-8812", "transaction_id": txn.ID, "status": "failed", "amount": 15000, "currency": "ZAR",
	})
	if res.StatusCode != http.StatusConflict || !bytes.Contains(raw, []byte("illegal-settlement-transition")) {
		t.Fatalf("illegal: %d %s", res.StatusCode, raw)
	}
	res, raw = do(t, http.MethodPost, srv.URL+"/settlements", nil, map[string]any{
		"external_id": "psp-evt-8812", "transaction_id": txn.ID, "status": "settled", "amount": 1, "currency": "ZAR",
	})
	if res.StatusCode != http.StatusConflict || !bytes.Contains(raw, []byte("settlement-event-mismatch")) {
		t.Fatalf("mismatch: %d %s", res.StatusCode, raw)
	}

	res, raw = do(t, http.MethodGet, srv.URL+"/reconciliation", nil, nil)
	if res.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte(`"ok":true`)) {
		t.Fatalf("recon: %d %s", res.StatusCode, raw)
	}
}

func TestValidation(t *testing.T) {
	srv, _ := newServer(t)
	zar := createAccount(t, srv, "cash", "ZAR", "asset")
	usd := createAccount(t, srv, "usd-cash", "USD", "asset")
	alice := createAccount(t, srv, "alice", "ZAR", "liability")

	res, raw := do(t, http.MethodPost, srv.URL+"/transactions", nil, map[string]any{
		"reference": "x",
		"entries":   []map[string]any{{"account_id": zar, "amount": 10}, {"account_id": alice, "amount": -10}},
	})
	if res.StatusCode != http.StatusBadRequest || !bytes.Contains(raw, []byte("idempotency-key-required")) {
		t.Fatalf("missing key: %d %s", res.StatusCode, raw)
	}

	res, raw = do(t, http.MethodPost, srv.URL+"/transactions", map[string]string{"Idempotency-Key": "unbalanced"}, map[string]any{
		"reference": "x",
		"entries":   []map[string]any{{"account_id": zar, "amount": 10}, {"account_id": alice, "amount": -9}},
	})
	if res.StatusCode != http.StatusBadRequest || !bytes.Contains(raw, []byte("unbalanced-transaction")) {
		t.Fatalf("unbalanced: %d %s", res.StatusCode, raw)
	}
	res, raw = do(t, http.MethodPost, srv.URL+"/transactions", map[string]string{"Idempotency-Key": "unbalanced"}, map[string]any{
		"reference": "x",
		"entries":   []map[string]any{{"account_id": zar, "amount": 10}, {"account_id": alice, "amount": -10}},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("key burned after validation failure: %d %s", res.StatusCode, raw)
	}

	res, raw = do(t, http.MethodPost, srv.URL+"/transactions", map[string]string{"Idempotency-Key": "mixed"}, map[string]any{
		"reference": "x",
		"entries":   []map[string]any{{"account_id": zar, "amount": 10}, {"account_id": usd, "amount": -10}},
	})
	if res.StatusCode != http.StatusBadRequest || !bytes.Contains(raw, []byte("mixed-currency")) {
		t.Fatalf("mixed: %d %s", res.StatusCode, raw)
	}

	res, raw = do(t, http.MethodPost, srv.URL+"/transactions", map[string]string{"Idempotency-Key": "overflow"}, map[string]any{
		"reference": "x",
		"entries": []map[string]any{
			{"account_id": zar, "amount": int64(9223372036854775807)},
			{"account_id": alice, "amount": int64(1)},
		},
	})
	if res.StatusCode != http.StatusBadRequest || !bytes.Contains(raw, []byte("amount-overflow")) {
		t.Fatalf("overflow: %d %s", res.StatusCode, raw)
	}

	res, raw = do(t, http.MethodPost, srv.URL+"/accounts", nil, map[string]any{"name": "cash", "currency": "ZAR", "type": "asset"})
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("dup account: %d %s", res.StatusCode, raw)
	}
	res, raw = do(t, http.MethodGet, srv.URL+"/accounts/acc_missing/balance", nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("missing balance: %d %s", res.StatusCode, raw)
	}
}

func TestIdempotencyInFlight(t *testing.T) {
	srv, st := newServer(t)
	cash := createAccount(t, srv, "cash", "ZAR", "asset")
	alice := createAccount(t, srv, "alice", "ZAR", "liability")
	key := "inflight-key"
	ctx := context.Background()
	tx, err := st.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1, hashtext($2))`, store.LockClassIdempotency, key).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	if !locked {
		t.Fatal("expected to take the idempotency lock")
	}
	body := map[string]any{
		"reference": "x",
		"entries":   []map[string]any{{"account_id": cash, "amount": 100}, {"account_id": alice, "amount": -100}},
	}
	res, raw := do(t, http.MethodPost, srv.URL+"/transactions", map[string]string{"Idempotency-Key": key}, body)
	if res.StatusCode != http.StatusConflict || !bytes.Contains(raw, []byte("idempotency-in-progress")) {
		t.Fatalf("inflight: %d %s", res.StatusCode, raw)
	}
}

func TestConcurrentSameKey(t *testing.T) {
	srv, st := newServer(t)
	cash := createAccount(t, srv, "cash", "ZAR", "asset")
	alice := createAccount(t, srv, "alice", "ZAR", "liability")
	body := map[string]any{
		"reference": "storm",
		"entries":   []map[string]any{{"account_id": cash, "amount": 500}, {"account_id": alice, "amount": -500}},
	}
	const n = 50
	codes := make([]int, n)
	bodies := make([][]byte, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			codes[i], bodies[i], _, errs[i] = call(http.MethodPost, srv.URL+"/transactions", map[string]string{"Idempotency-Key": "same-key"}, body)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	var created [][]byte
	for i, code := range codes {
		switch code {
		case http.StatusCreated:
			created = append(created, bodies[i])
		case http.StatusConflict:
		default:
			t.Fatalf("status %d body %s", code, bodies[i])
		}
	}
	if len(created) == 0 {
		t.Fatal("no request created the transaction")
	}
	for _, b := range created[1:] {
		if !bytes.Equal(b, created[0]) {
			t.Fatalf("created bodies differ")
		}
	}
	var count int
	if err := st.Pool().QueryRow(context.Background(), `SELECT count(*) FROM transactions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("transactions = %d", count)
	}
	res, raw := do(t, http.MethodPost, srv.URL+"/transactions", map[string]string{"Idempotency-Key": "same-key"}, body)
	if res.StatusCode != http.StatusCreated || res.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("follow-up replay: %d %s", res.StatusCode, raw)
	}
}

func TestConcurrentDistinctKeysStayConsistent(t *testing.T) {
	srv, _ := newServer(t)
	cash := createAccount(t, srv, "cash", "ZAR", "asset")
	alice := createAccount(t, srv, "alice", "ZAR", "liability")
	const n = 12
	var wg sync.WaitGroup
	errCh := make(chan string, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			code, raw, _, err := call(http.MethodPost, srv.URL+"/transactions", map[string]string{"Idempotency-Key": "k-" + string(rune('a'+i))}, map[string]any{
				"reference": "r",
				"entries":   []map[string]any{{"account_id": cash, "amount": 100}, {"account_id": alice, "amount": -100}},
			})
			if err != nil {
				errCh <- err.Error()
				return
			}
			if code != http.StatusCreated {
				errCh <- string(raw)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Fatal(msg)
	}
	res, raw := do(t, http.MethodGet, srv.URL+"/reconciliation", nil, nil)
	if res.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte(`"ok":true`)) || !bytes.Contains(raw, []byte(`"verified_entries":24`)) {
		t.Fatalf("recon: %d %s", res.StatusCode, raw)
	}
	res, raw = do(t, http.MethodGet, srv.URL+"/accounts/"+alice+"/balance", nil, nil)
	if !bytes.Contains(raw, []byte(`"balance":-1200`)) {
		t.Fatalf("balance: %d %s", res.StatusCode, raw)
	}
}

func TestAppendOnlyTriggerAndChainTamper(t *testing.T) {
	srv, st := newServer(t)
	cash := createAccount(t, srv, "cash", "ZAR", "asset")
	alice := createAccount(t, srv, "alice", "ZAR", "liability")
	res, raw := do(t, http.MethodPost, srv.URL+"/transactions", map[string]string{"Idempotency-Key": "one"}, map[string]any{
		"reference": "r",
		"entries":   []map[string]any{{"account_id": cash, "amount": 40}, {"account_id": alice, "amount": -40}},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("post: %d %s", res.StatusCode, raw)
	}
	ctx := context.Background()
	pool := st.Pool()
	for _, stmt := range []string{
		`UPDATE ledger_entries SET amount = amount`,
		`DELETE FROM ledger_entries`,
		`TRUNCATE ledger_entries`,
	} {
		if _, err := pool.Exec(ctx, stmt); err == nil {
			t.Fatalf("%s was allowed", stmt)
		}
	}

	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	id := parsed.ID
	_, err := pool.Exec(ctx, `
		INSERT INTO ledger_entries (transaction_id, account_id, amount, currency, prev_hash, hash)
		VALUES ($1, $2, 1, 'ZAR', decode(repeat('00', 32), 'hex'), decode(repeat('ab', 32), 'hex'))`, id, cash)
	if err != nil {
		t.Fatal(err)
	}
	res, raw = do(t, http.MethodGet, srv.URL+"/reconciliation", nil, nil)
	if res.StatusCode != http.StatusOK || bytes.Contains(raw, []byte(`"ok":true`)) || bytes.Contains(raw, []byte(`"first_break_seq":null`)) {
		t.Fatalf("expected chain break: %d %s", res.StatusCode, raw)
	}

	res, raw = do(t, http.MethodPost, srv.URL+"/settlements", nil, map[string]any{
		"external_id": "ext-1", "transaction_id": id, "status": "settled", "amount": 40, "currency": "ZAR",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("settle: %d %s", res.StatusCode, raw)
	}
	if _, err := pool.Exec(ctx, `UPDATE settlements SET state = 'failed' WHERE external_id = 'ext-1'`); err == nil {
		t.Fatal("trigger allowed settled -> failed")
	}
}

func TestReconDetectsCacheAndSettlementDrift(t *testing.T) {
	srv, st := newServer(t)
	cash := createAccount(t, srv, "cash", "ZAR", "asset")
	alice := createAccount(t, srv, "alice", "ZAR", "liability")
	res, raw := do(t, http.MethodPost, srv.URL+"/transactions", map[string]string{"Idempotency-Key": "d"}, map[string]any{
		"reference": "r",
		"entries":   []map[string]any{{"account_id": cash, "amount": 80}, {"account_id": alice, "amount": -80}},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatal(string(raw))
	}
	var txn struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &txn); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool().Exec(context.Background(), `UPDATE accounts SET balance = balance + 5 WHERE id = $1`, cash); err != nil {
		t.Fatal(err)
	}
	res, raw = do(t, http.MethodGet, srv.URL+"/reconciliation", nil, nil)
	if res.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte(`"ok":false`)) || !bytes.Contains(raw, []byte(cash)) {
		t.Fatalf("balance drift: %d %s", res.StatusCode, raw)
	}
	if _, err := st.Pool().Exec(context.Background(), `UPDATE accounts SET balance = balance - 5 WHERE id = $1`, cash); err != nil {
		t.Fatal(err)
	}
	res, raw = do(t, http.MethodPost, srv.URL+"/settlements", nil, map[string]any{
		"external_id": "bad-amt", "transaction_id": txn.ID, "status": "settled", "amount": 1, "currency": "ZAR",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("settle wrong amount: %d %s", res.StatusCode, raw)
	}
	res, raw = do(t, http.MethodGet, srv.URL+"/reconciliation", nil, nil)
	if res.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte(`"ok":false`)) || !bytes.Contains(raw, []byte("settlement_mismatches")) {
		t.Fatalf("settlement drift: %d %s", res.StatusCode, raw)
	}
	if bytes.Contains(raw, []byte(`"settlement_mismatches":[]`)) {
		t.Fatalf("expected a settlement mismatch: %s", raw)
	}
}

func TestDeadLetterReplay(t *testing.T) {
	srv, st := newServer(t)
	cash := createAccount(t, srv, "cash", "ZAR", "asset")
	alice := createAccount(t, srv, "alice", "ZAR", "liability")
	res, raw := do(t, http.MethodPost, srv.URL+"/transactions", map[string]string{"Idempotency-Key": "dl"}, map[string]any{
		"reference": "r",
		"entries":   []map[string]any{{"account_id": cash, "amount": 25}, {"account_id": alice, "amount": -25}},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatal(string(raw))
	}
	var txn struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &txn); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{
		"external_id": "from-dlq", "transaction_id": txn.ID, "status": "failed", "amount": 25, "currency": "ZAR",
	})
	id, err := st.Park(context.Background(), "settlement_feed", payload, "connection reset", 5)
	if err != nil {
		t.Fatal(err)
	}
	res, raw = do(t, http.MethodGet, srv.URL+"/dead-letters?status=parked", nil, nil)
	if res.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte(id)) {
		t.Fatalf("list: %d %s", res.StatusCode, raw)
	}
	res, raw = do(t, http.MethodPost, srv.URL+"/dead-letters/"+id+"/replay", nil, nil)
	if res.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte(`"status":"replayed"`)) || !bytes.Contains(raw, []byte(`"applied":true`)) {
		t.Fatalf("replay: %d %s", res.StatusCode, raw)
	}
	first := append([]byte(nil), raw...)
	res, raw = do(t, http.MethodPost, srv.URL+"/dead-letters/"+id+"/replay", nil, nil)
	if res.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte(`"status":"replayed"`)) {
		t.Fatalf("second replay: %d %s", res.StatusCode, raw)
	}
	if !bytes.Contains(first, []byte(`"applied":true`)) {
		t.Fatalf("stored result: %s", first)
	}
	var n int
	if err := st.Pool().QueryRow(context.Background(), `SELECT count(*) FROM settlements WHERE external_id = 'from-dlq' AND state = 'failed'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("settlements %d", n)
	}
}

func TestReversingEntry(t *testing.T) {
	srv, _ := newServer(t)
	cash := createAccount(t, srv, "cash", "ZAR", "asset")
	alice := createAccount(t, srv, "alice", "ZAR", "liability")
	post := func(key string, cashAmt, aliceAmt int) {
		t.Helper()
		res, raw := do(t, http.MethodPost, srv.URL+"/transactions", map[string]string{"Idempotency-Key": key}, map[string]any{
			"reference": key,
			"entries":   []map[string]any{{"account_id": cash, "amount": cashAmt}, {"account_id": alice, "amount": aliceAmt}},
		})
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("%s: %d %s", key, res.StatusCode, raw)
		}
	}
	post("in", 1500, -1500)
	post("out", -1500, 1500)
	res, raw := do(t, http.MethodGet, srv.URL+"/accounts/"+alice+"/balance", nil, nil)
	if !bytes.Contains(raw, []byte(`"balance":0`)) {
		t.Fatalf("balance %d %s", res.StatusCode, raw)
	}
	res, raw = do(t, http.MethodGet, srv.URL+"/reconciliation", nil, nil)
	if res.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte(`"ok":true`)) {
		t.Fatalf("recon %d %s", res.StatusCode, raw)
	}
}

func TestSettlementFeedFile(t *testing.T) {
	srv, st := newServer(t)
	cash := createAccount(t, srv, "cash", "ZAR", "asset")
	alice := createAccount(t, srv, "alice", "ZAR", "liability")
	res, raw := do(t, http.MethodPost, srv.URL+"/transactions", map[string]string{"Idempotency-Key": "feed"}, map[string]any{
		"reference": "r",
		"entries":   []map[string]any{{"account_id": cash, "amount": 9}, {"account_id": alice, "amount": -9}},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatal(string(raw))
	}
	var txn struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &txn); err != nil {
		t.Fatal(err)
	}
	line := []byte(`{"external_id":"file-1","transaction_id":"` + txn.ID + `","status":"settled","amount":9,"currency":"ZAR"}`)
	feed := append(append(append([]byte{}, line...), '\n'), line...)
	feed = append(feed, '\n')
	app := settlement.Service{DB: st}
	n, err := settlement.Process(context.Background(), bytes.NewReader(feed), func(ctx context.Context, line []byte) error {
		ev, err := settlement.Parse(line)
		if err != nil {
			return err
		}
		_, err = settlement.Ingest(ctx, app, st, retry.Config{MaxAttempts: 2, Base: time.Millisecond, Cap: time.Millisecond}, retry.WallClock{}, retry.MathRand{}, ev, line)
		return err
	})
	if err != nil || n != 2 {
		t.Fatalf("process n=%d err=%v", n, err)
	}
	var applied int
	var state string
	if err := st.Pool().QueryRow(context.Background(), `SELECT count(*), max(state::text) FROM settlements WHERE external_id = 'file-1'`).Scan(&applied, &state); err != nil {
		t.Fatal(err)
	}
	if applied != 1 || state != "settled" {
		t.Fatalf("rows %d state %s", applied, state)
	}
}

func call(method, url string, headers map[string]string, body any) (int, []byte, http.Header, error) {
	rawBody, err := json.Marshal(body)
	if err != nil {
		return 0, nil, nil, err
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(rawBody))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, nil, nil, err
	}
	return res.StatusCode, raw, res.Header, nil
}
