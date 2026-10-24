// Package httpapi is the ledger HTTP surface.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/tashen247/go-ledger/internal/deadletter"
	"github.com/tashen247/go-ledger/internal/retry"
	"github.com/tashen247/go-ledger/internal/settlement"
	"github.com/tashen247/go-ledger/internal/store"
)

// Deps are the process-wide dependencies of the HTTP server.
type Deps struct {
	Store  *store.Store
	Logger *slog.Logger
	Retry  retry.Config
	Clock  retry.Clock
	Rand   retry.Source
}

type handler struct {
	store      *store.Store
	log        *slog.Logger
	retry      retry.Config
	clock      retry.Clock
	rand       retry.Source
	settlement settlement.Service
	dead       deadletter.Service
}

// New returns the HTTP handler. It does not listen.
func New(d Deps) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Clock == nil {
		d.Clock = retry.WallClock{}
	}
	if d.Rand == nil {
		d.Rand = retry.MathRand{}
	}
	if d.Retry.MaxAttempts < 1 {
		d.Retry = retry.Config{MaxAttempts: 5, Base: 20 * time.Millisecond, Cap: 500 * time.Millisecond}
	}
	h := &handler{
		store:      d.Store,
		log:        d.Logger,
		retry:      d.Retry,
		clock:      d.Clock,
		rand:       d.Rand,
		settlement: settlement.Service{DB: d.Store},
		dead:       deadletter.Service{DB: d.Store},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /accounts", h.createAccount)
	mux.HandleFunc("POST /transactions", h.postTransaction)
	mux.HandleFunc("GET /accounts/{id}/balance", h.balance)
	mux.HandleFunc("POST /settlements", h.postSettlement)
	mux.HandleFunc("GET /reconciliation", h.reconciliation)
	mux.HandleFunc("GET /dead-letters", h.listDeadLetters)
	mux.HandleFunc("POST /dead-letters/{id}/replay", h.replayDeadLetter)
	mux.HandleFunc("GET /healthz", h.health)
	return logMiddleware(d.Logger, recoverMiddleware(d.Logger, mux))
}

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.store.Pool().Ping(ctx); err != nil {
		h.writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
