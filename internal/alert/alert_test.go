package alert

import "testing"

func TestTracker(t *testing.T) {
	tr := NewTracker()

	// With no pending alert (e.g. right after a restart), a reply clears at once.
	if got := tr.Ack("a"); got != 0 {
		t.Errorf("Ack with nothing pending = %d, want 0", got)
	}

	tr.Raise("a")
	tr.Raise("a")
	tr.Raise("b")
	if got := tr.Ack("a"); got != 1 {
		t.Errorf("Ack(a) = %d, want 1 (b still pending)", got)
	}
	if got := tr.Ack("b"); got != 0 {
		t.Errorf("Ack(b) = %d, want 0", got)
	}
}
