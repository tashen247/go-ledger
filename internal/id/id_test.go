package id

import "testing"

func TestNewShape(t *testing.T) {
	got := New("acc_")
	if len(got) != 4+26 {
		t.Fatalf("len %d: %s", len(got), got)
	}
	if got[:4] != "acc_" {
		t.Fatalf("prefix: %s", got)
	}
	for _, c := range got[4:] {
		if !isCrockford(c) {
			t.Fatalf("bad char %q in %s", c, got)
		}
	}
	other := New("acc_")
	if other == got {
		t.Fatal("expected distinct ids")
	}
}

func isCrockford(c rune) bool {
	for i := 0; i < len(crockford); i++ {
		if rune(crockford[i]) == c {
			return true
		}
	}
	return false
}
