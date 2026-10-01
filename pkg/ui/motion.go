package ui

import "time"

// motion is a value that moves from where it is to its target in its
// duration along ease-out cubic. A new target restarts the time from the
// value it has then, so the value is continuous (specs/007-animation). At a
// duration of 0 the value is at its target at once
// (specs/010-animation-options).
type motion struct {
	from, to float64
	start    time.Time // zero while the value is at rest
	d        time.Duration
}

// rest puts the value at v, not moving
func (m *motion) rest(v float64) {
	m.from, m.to, m.start = v, v, time.Time{}
}

// moveTo starts moving from the value at now to the target
func (m *motion) moveTo(now time.Time, target float64) {
	m.from = m.at(now)
	m.to = target
	m.start = now
}

// at is the value at now
func (m *motion) at(now time.Time) float64 {
	u := m.progress(now)
	if u >= 1 {
		return m.to
	}
	return m.from + (m.to-m.from)*easeOut(u)
}

// progress is how far the motion is at now, from 0 to 1
func (m *motion) progress(now time.Time) float64 {
	if m.start.IsZero() || m.d <= 0 {
		return 1
	}
	u := float64(now.Sub(m.start)) / float64(m.d)
	switch {
	case u <= 0:
		return 0
	case u >= 1:
		return 1
	}
	return u
}

// moving reports whether the value has not reached its target at now
func (m *motion) moving(now time.Time) bool {
	return m.progress(now) < 1
}

// easeOut is ease-out cubic on [0, 1]
func easeOut(u float64) float64 {
	v := 1 - u
	return 1 - v*v*v
}
