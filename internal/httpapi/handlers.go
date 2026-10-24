package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/tashen247/go-ledger/internal/recon"
	"github.com/tashen247/go-ledger/internal/settlement"
	"github.com/tashen247/go-ledger/internal/store"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(w http.ResponseWriter, r *http.Request, dst any) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		return nil, &store.InvalidError{Msg: "request body is too large"}
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return body, &store.InvalidError{Msg: "could not decode request body"}
	}
	if dec.More() {
		return body, &store.InvalidError{Msg: "request body has trailing data"}
	}
	return body, nil
}

func (h *handler) createAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Currency string `json:"currency"`
		Type     string `json:"type"`
	}
	if _, err := readJSON(w, r, &req); err != nil {
		h.writeErr(w, r, err)
		return
	}
	acct, err := h.store.CreateAccount(r.Context(), req.Name, req.Currency, req.Type)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, acct)
}

func (h *handler) postTransaction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reference string `json:"reference"`
		Entries   []struct {
			AccountID string `json:"account_id"`
			Amount    int64  `json:"amount"`
		} `json:"entries"`
	}
	body, err := readJSON(w, r, &req)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	sum := sha256.Sum256(body)
	legs := make([]store.Leg, len(req.Entries))
	for i, e := range req.Entries {
		legs[i] = store.Leg{AccountID: e.AccountID, Amount: e.Amount}
	}
	res, err := h.store.Post(r.Context(), store.PostCommand{
		Key:         key,
		RequestHash: sum[:],
		Reference:   req.Reference,
		Entries:     legs,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if res.Replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	w.WriteHeader(res.Status)
	_, _ = w.Write(res.Body)
}

func (h *handler) balance(w http.ResponseWriter, r *http.Request) {
	bal, err := h.store.AccountBalance(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, bal)
}

func (h *handler) postSettlement(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		h.writeErr(w, r, &store.InvalidError{Msg: "request body is too large"})
		return
	}
	ev, err := settlement.Parse(body)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	res, err := settlement.Ingest(r.Context(), h.settlement, h.dead, h.retry, h.clock, h.rand, ev, body)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handler) reconciliation(w http.ResponseWriter, r *http.Request) {
	report, err := recon.Run(r.Context(), h.store)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (h *handler) listDeadLetters(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && status != "parked" && status != "replayed" {
		h.writeErr(w, r, &store.InvalidError{Msg: "status must be parked or replayed"})
		return
	}
	items, err := h.dead.List(r.Context(), status)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) replayDeadLetter(w http.ResponseWriter, r *http.Request) {
	res, err := h.dead.Replay(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
