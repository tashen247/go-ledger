package idempotency

import "testing"

func TestDecide(t *testing.T) {
	hash := []byte{1, 2, 3, 4}
	other := []byte{1, 2, 3, 5}
	if Decide(false, "", nil, hash) != Proceed {
		t.Fatal("missing row")
	}
	if Decide(true, InProgress, hash, hash) != InFlight {
		t.Fatal("in progress")
	}
	if Decide(true, Completed, hash, hash) != Replay {
		t.Fatal("replay")
	}
	if Decide(true, Completed, hash, other) != Mismatch {
		t.Fatal("mismatch")
	}
	if Decide(true, Completed, hash, nil) != Mismatch {
		t.Fatal("short hash")
	}
	if Decide(true, "other", hash, hash) != InFlight {
		t.Fatal("unknown status")
	}
}
