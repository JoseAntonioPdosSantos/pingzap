package whatsapp

import (
	"testing"
	"time"
)

func TestIsStale(t *testing.T) {
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		sent time.Time
		want bool
	}{
		{"just sent", now, false},
		{"sent 59 s ago", now.Add(-59 * time.Second), false},
		{"sent exactly 1 min ago", now.Add(-time.Minute), false},
		{"sent 61 s ago", now.Add(-61 * time.Second), true},
		{"sent while offline", now.Add(-40 * time.Minute), true},
		{"phone clock ahead", now.Add(30 * time.Second), false},
	}
	for _, tt := range tests {
		if got := isStale(tt.sent, now); got != tt.want {
			t.Errorf("%s: isStale = %v, want %v", tt.name, got, tt.want)
		}
	}
}
