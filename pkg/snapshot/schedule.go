package snapshot

import "time"

// settle is how long a window that changed, or became viewable, is left to
// finish drawing before it is captured
const settle = 100 * time.Millisecond

// schedule is when a window is captured (specs/008-window-snapshots): once it
// changed — DAMAGE reports the first change after each snapshot — and has
// settled, and its last snapshot is an interval old
type schedule struct {
	dirty    bool      // changed since its snapshot, or never captured
	dirtyAt  time.Time // when it first changed since its snapshot
	lastShot time.Time // its last snapshot; zero: none
}

// change marks a change at now; a change on top of one not yet captured
// keeps the earlier time
func (s *schedule) change(now time.Time) {
	if !s.dirty {
		s.dirty, s.dirtyAt = true, now
	}
}

// due is when the window is to be captured; false when it has not changed
func (s *schedule) due(interval time.Duration) (time.Time, bool) {
	if !s.dirty {
		return time.Time{}, false
	}
	t := s.dirtyAt.Add(settle)
	if !s.lastShot.IsZero() {
		if u := s.lastShot.Add(interval); u.After(t) {
			t = u
		}
	}
	return t, true
}

// shot records a snapshot at now
func (s *schedule) shot(now time.Time) {
	s.dirty, s.lastShot = false, now
}
