// Package settlement turns at-least-once feed events into state transitions.
package settlement

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tashen247/go-ledger/internal/store"
)

// Event is one delivery from a payment feed.
type Event struct {
	ExternalID    string `json:"external_id"`
	TransactionID string `json:"transaction_id"`
	Status        string `json:"status"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
}

// Result is returned to the caller of an ingest or apply.
type Result struct {
	ID         string `json:"id"`
	ExternalID string `json:"external_id"`
	State      string `json:"state"`
	Applied    bool   `json:"applied"`
}

// ErrParked means retries were exhausted and the event was written to dead_letters.
var ErrParked = errors.New("settlement parked")

// ParkedError carries the dead-letter id created for an exhausted event.
type ParkedError struct {
	ID       string
	Attempts int
	Cause    error
}

func (e *ParkedError) Error() string {
	return fmt.Sprintf("parked in dead letter %s after %d attempts: %v", e.ID, e.Attempts, e.Cause)
}

// Unwrap returns the last transient error.
func (e *ParkedError) Unwrap() error { return e.Cause }

// Is matches ErrParked.
func (e *ParkedError) Is(target error) bool { return target == ErrParked }

// Parse decodes a settlement event. Unknown fields are rejected so a client
// cannot believe the server stored something it ignored.
func Parse(body []byte) (Event, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var ev Event
	if err := dec.Decode(&ev); err != nil {
		return Event{}, fmt.Errorf("%w: %s", store.ErrInvalid, "settlement body is not valid json")
	}
	if dec.More() {
		return Event{}, fmt.Errorf("%w: trailing data", store.ErrInvalid)
	}
	return ev, nil
}
