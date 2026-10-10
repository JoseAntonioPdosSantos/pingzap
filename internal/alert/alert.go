// Package alert tracks which numbers have an alert awaiting a reply, so the
// LED only turns off once all of them have been answered.
package alert

import "sync"

type Tracker struct {
	mu      sync.Mutex
	pending map[string]struct{}
}

func NewTracker() *Tracker {
	return &Tracker{pending: make(map[string]struct{})}
}

// Raise records a pending alert for the number.
func (t *Tracker) Raise(number string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending[number] = struct{}{}
}

// Ack marks the number as answered and returns how many alerts are still
// pending.
func (t *Tracker) Ack(number string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.pending, number)
	return len(t.pending)
}
