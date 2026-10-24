package store

import "errors"

var (
	// ErrIdempotencyRequired means the Idempotency-Key header was missing.
	ErrIdempotencyRequired = errors.New("idempotency key required")
	// ErrIdempotencyInProgress means another request with this key is in flight.
	ErrIdempotencyInProgress = errors.New("idempotency key in progress")
	// ErrIdempotencyReuse means the key was reused with a different body.
	ErrIdempotencyReuse = errors.New("idempotency key reuse")
	// ErrAccountNotFound means a balance lookup missed.
	ErrAccountNotFound = errors.New("account not found")
	// ErrUnknownAccount means a posting named an account that does not exist.
	ErrUnknownAccount = errors.New("unknown account")
	// ErrDuplicateAccount means the account name is already taken.
	ErrDuplicateAccount = errors.New("account already exists")
	// ErrInvalid is a request that failed validation.
	ErrInvalid = errors.New("invalid request")
	// ErrTransactionNotFound means a settlement named a missing transaction.
	ErrTransactionNotFound = errors.New("transaction not found")
	// ErrCurrencyMismatch means a settlement currency differs from the transaction.
	ErrCurrencyMismatch = errors.New("settlement currency does not match transaction")
	// ErrIllegalTransition means a settlement left a terminal state.
	ErrIllegalTransition = errors.New("illegal settlement transition")
	// ErrEventMismatch means the same external id arrived with a different payload.
	ErrEventMismatch = errors.New("settlement external id reused with a different event")
	// ErrDeadLetterNotFound means a replay targeted a missing id.
	ErrDeadLetterNotFound = errors.New("dead letter not found")
)

// InvalidError is a validation failure with a client-safe detail.
type InvalidError struct {
	Msg string
}

func (e *InvalidError) Error() string { return e.Msg }

// Unwrap lets errors.Is match ErrInvalid.
func (e *InvalidError) Unwrap() error { return ErrInvalid }

// TransitionError describes a rejected settlement state change.
type TransitionError struct {
	From string
	To   string
}

func (e *TransitionError) Error() string {
	return "illegal settlement transition " + e.From + " -> " + e.To
}

// Unwrap lets errors.Is match ErrIllegalTransition.
func (e *TransitionError) Unwrap() error { return ErrIllegalTransition }
