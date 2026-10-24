package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tashen247/go-ledger/internal/ledger"
	"github.com/tashen247/go-ledger/internal/money"
	"github.com/tashen247/go-ledger/internal/settlement"
	"github.com/tashen247/go-ledger/internal/store"
)

const problemBase = "https://github.com/tashen247/go-ledger/problems/"

type problem struct {
	status int
	typ    string
	title  string
	detail string
	extra  map[string]any
}

func writeProblem(w http.ResponseWriter, p problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.status)
	body := map[string]any{
		"type":   problemBase + p.typ,
		"title":  p.title,
		"status": p.status,
		"detail": p.detail,
	}
	for k, v := range p.extra {
		body[k] = v
	}
	_ = json.NewEncoder(w).Encode(body)
}

func (h *handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var parked *settlement.ParkedError
	if errors.As(err, &parked) {
		writeProblem(w, problem{
			status: http.StatusServiceUnavailable,
			typ:    "settlement-parked",
			title:  "Settlement parked",
			detail: "transient errors were exhausted and the event was parked for replay",
			extra:  map[string]any{"dead_letter_id": parked.ID},
		})
		return
	}
	switch {
	case errors.Is(err, store.ErrIdempotencyRequired):
		writeProblem(w, problem{http.StatusBadRequest, "idempotency-key-required", "Idempotency key required", "POST /transactions requires an Idempotency-Key header", nil})
	case errors.Is(err, store.ErrIdempotencyReuse):
		writeProblem(w, problem{http.StatusUnprocessableEntity, "idempotency-key-reuse", "Idempotency key reuse", "this Idempotency-Key was already used with a different request body", nil})
	case errors.Is(err, store.ErrIdempotencyInProgress):
		writeProblem(w, problem{http.StatusConflict, "idempotency-in-progress", "Idempotency key in progress", "a request with this Idempotency-Key is still in flight", nil})
	case errors.Is(err, store.ErrDuplicateAccount):
		writeProblem(w, problem{http.StatusConflict, "account-exists", "Account exists", "an account with that name already exists", nil})
	case errors.Is(err, store.ErrAccountNotFound):
		writeProblem(w, problem{http.StatusNotFound, "account-not-found", "Account not found", "no account has that id", nil})
	case errors.Is(err, store.ErrUnknownAccount):
		writeProblem(w, problem{http.StatusBadRequest, "unknown-account", "Unknown account", err.Error(), nil})
	case errors.Is(err, store.ErrTransactionNotFound):
		writeProblem(w, problem{http.StatusNotFound, "transaction-not-found", "Transaction not found", "no transaction has that id", nil})
	case errors.Is(err, store.ErrCurrencyMismatch):
		writeProblem(w, problem{http.StatusBadRequest, "currency-mismatch", "Currency mismatch", "settlement currency does not match the transaction", nil})
	case errors.Is(err, store.ErrIllegalTransition):
		writeProblem(w, problem{http.StatusConflict, "illegal-settlement-transition", "Illegal settlement transition", err.Error(), nil})
	case errors.Is(err, store.ErrEventMismatch):
		writeProblem(w, problem{http.StatusConflict, "settlement-event-mismatch", "Settlement event mismatch", "this external_id was already used with a different amount, currency, or transaction", nil})
	case errors.Is(err, store.ErrDeadLetterNotFound):
		writeProblem(w, problem{http.StatusNotFound, "dead-letter-not-found", "Dead letter not found", "no dead letter has that id", nil})
	case errors.Is(err, ledger.ErrUnbalanced):
		writeProblem(w, problem{http.StatusBadRequest, "unbalanced-transaction", "Unbalanced transaction", "entries must sum to zero", nil})
	case errors.Is(err, ledger.ErrMixedCurrency):
		writeProblem(w, problem{http.StatusBadRequest, "mixed-currency", "Mixed currency", "every account in a transaction must share one currency", nil})
	case errors.Is(err, ledger.ErrTooFewEntries):
		writeProblem(w, problem{http.StatusBadRequest, "too-few-entries", "Too few entries", "a transaction needs at least two entries", nil})
	case errors.Is(err, ledger.ErrZeroAmount):
		writeProblem(w, problem{http.StatusBadRequest, "zero-amount", "Zero amount", "entry amounts must be non-zero minor units", nil})
	case errors.Is(err, money.ErrOverflow):
		writeProblem(w, problem{http.StatusBadRequest, "amount-overflow", "Amount overflow", "the amounts overflow int64 minor units", nil})
	case errors.Is(err, store.ErrInvalid):
		writeProblem(w, problem{http.StatusBadRequest, "invalid-request", "Invalid request", err.Error(), nil})
	default:
		h.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		writeProblem(w, problem{http.StatusInternalServerError, "internal-error", "Internal error", "the request could not be completed", nil})
	}
}
