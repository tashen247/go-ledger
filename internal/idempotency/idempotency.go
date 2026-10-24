// Package idempotency decides what to do when an Idempotency-Key is presented.
//
// The key is the primary key of idempotency_keys. The database rejects a
// second row. This package is the matching in-process rule: same body replays
// the stored response, a different body is a conflict, and an in-flight row
// is left alone.
package idempotency

import "crypto/subtle"

// Status is the lifecycle of a key row.
type Status string

const (
	// InProgress means the original request has not committed a response.
	InProgress Status = "in_progress"
	// Completed means the stored response can be replayed.
	Completed Status = "completed"
)

// Decision is the outcome of comparing a request with a stored key.
type Decision int

const (
	// Proceed means no row exists and the caller should post.
	Proceed Decision = iota
	// Replay means the same body was already committed.
	Replay
	// InFlight means the original request is still running.
	InFlight
	// Mismatch means the key was reused with a different body.
	Mismatch
)

// Decide compares a stored row with the hash of the current request body.
// storedHash and requestHash are raw SHA-256 digests.
func Decide(exists bool, status Status, storedHash, requestHash []byte) Decision {
	if !exists {
		return Proceed
	}
	if status != Completed {
		return InFlight
	}
	if len(storedHash) != len(requestHash) || subtle.ConstantTimeCompare(storedHash, requestHash) != 1 {
		return Mismatch
	}
	return Replay
}
