package snapshot

import (
	"testing"
	"time"
)

// TestSchedule checks the timing of K3 of specs/008-window-snapshots: a
// window is captured a settle after it changes, never more often than an
// interval, and not at all while it does not change
func TestSchedule(t *testing.T) {
	t0 := time.Unix(1000, 0)
	const interval = time.Second
	var s schedule

	if _, ok := s.due(interval); ok {
		t.Fatal("due without a change")
	}

	// Never captured: a settle after the change
	s.change(t0)
	if due, ok := s.due(interval); !ok || !due.Equal(t0.Add(settle)) {
		t.Errorf("first: due %v, %v; want %v", due.Sub(t0), ok, settle)
	}

	// A later change keeps the earlier time
	s.change(t0.Add(50 * time.Millisecond))
	if due, _ := s.due(interval); !due.Equal(t0.Add(settle)) {
		t.Errorf("a second change moved the capture to %v", due.Sub(t0))
	}

	// Captured: no capture until the next change
	shot := t0.Add(settle)
	s.shot(shot)
	if _, ok := s.due(interval); ok {
		t.Error("due after a capture, without a change")
	}

	// Changed soon after: an interval after the last capture
	s.change(shot.Add(10 * time.Millisecond))
	if due, _ := s.due(interval); !due.Equal(shot.Add(interval)) {
		t.Errorf("soon after: due %v after the capture, want %v", due.Sub(shot), interval)
	}

	// Changed long after: a settle after the change
	s.shot(shot.Add(interval))
	late := shot.Add(10 * interval)
	s.change(late)
	if due, _ := s.due(interval); !due.Equal(late.Add(settle)) {
		t.Errorf("long after: due %v after the change, want %v", due.Sub(late), settle)
	}
}
