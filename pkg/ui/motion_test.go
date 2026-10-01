package ui

import (
	"math"
	"testing"
	"time"
)

// animationDuration is the duration of specs/007-animation
const animationDuration = 150 * time.Millisecond

// TestMotion checks criterion K8 of specs/007-animation, and K9 of
// specs/010-animation-options at 150 ms: the value reaches its target after
// its duration and stays there, and it is continuous when a new target is
// set mid-way
func TestMotion(t *testing.T) {
	t0 := time.Unix(1000, 0)
	m := motion{d: animationDuration}
	m.rest(3)
	if m.moving(t0) || m.at(t0) != 3 {
		t.Fatalf("at rest: %v, moving %v", m.at(t0), m.moving(t0))
	}

	m.moveTo(t0, 4)
	if m.at(t0) != 3 {
		t.Errorf("at the start %v, want 3", m.at(t0))
	}
	half := t0.Add(animationDuration / 2)
	if v := m.at(half); v <= 3.5 || v >= 4 {
		t.Errorf("half way %v: ease-out is past the middle", v)
	}

	// A new target half way: the value does not jump
	before := m.at(half)
	m.moveTo(half, 6)
	if after := m.at(half); math.Abs(after-before) > 1e-12 {
		t.Errorf("retarget jumps from %v to %v", before, after)
	}
	end := half.Add(animationDuration)
	if !m.moving(end.Add(-time.Millisecond)) {
		t.Error("still moving just before the end expected")
	}
	for _, at := range []time.Time{end, end.Add(time.Second)} {
		if m.moving(at) || m.at(at) != 6 {
			t.Errorf("at %v after the end: %v, moving %v", at.Sub(end), m.at(at), m.moving(at))
		}
	}

	// Monotone on the way
	m.moveTo(end, 0)
	prev := m.at(end)
	for d := time.Millisecond; d <= animationDuration; d += time.Millisecond {
		v := m.at(end.Add(d))
		if v > prev {
			t.Fatalf("not monotone at %v: %v after %v", d, v, prev)
		}
		prev = v
	}
}

// TestMotionAtOnce checks K9 of specs/010-animation-options at 0: the value
// is at its target at once, and does not move
func TestMotionAtOnce(t *testing.T) {
	t0 := time.Unix(1000, 0)
	var m motion
	m.rest(3)
	m.moveTo(t0, 4)
	if m.moving(t0) || m.at(t0) != 4 || m.progress(t0) != 1 {
		t.Errorf("at the start %v, moving %v, progress %v; want 4, at rest", m.at(t0), m.moving(t0), m.progress(t0))
	}
}
