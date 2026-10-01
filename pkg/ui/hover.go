package ui

import (
	"math"
	"sort"
	"time"

	"github.com/almaz-uno/qws/pkg/carousel"
)

// The hover of specs/010-animation-options: each tile or card has a level —
// 1 for the one under the pointer, 0 for the rest — moved by a motion of the
// duration of the animations, and the scenes draw its hover frame at the look
// the effects of hover give the level. Without them, or while the scene
// cannot show them, the levels are at their targets at once: the hover of
// 007.

// hoverAnimation is the motion of the hover frames
type hoverAnimation struct {
	animationLog
	active bool           // the levels move: frames are due
	levels map[int]motion // by window index: the levels above 0, or moving
}

// hoverLevel is the level of the hover frame of window index
type hoverLevel struct {
	index int
	v     float64
}

// setHover moves the levels towards the hover of s.hoverIndex from now:
// animated, or at once
func (s *Selector) setHover(now time.Time, animated bool) {
	var d time.Duration
	if animated {
		d = s.anim.duration
	}
	if s.hover.levels == nil {
		s.hover.levels = map[int]motion{}
	}
	move := func(i int, m motion, target float64) {
		// From where it is now, at the duration it moves by now
		m.moveTo(now, target)
		m.d = d
		s.hover.levels[i] = m
	}
	for i, m := range s.hover.levels {
		if i != s.hoverIndex {
			move(i, m, 0)
		}
	}
	if s.hoverIndex >= 0 {
		m, ok := s.hover.levels[s.hoverIndex]
		if !ok {
			m.rest(0)
		}
		move(s.hoverIndex, m, 1)
	}
	s.pruneHover(now)
}

// beginHover counts a change of the hover that moves the levels: a new
// animation, or a new target of the one that runs
func (s *Selector) beginHover(now time.Time) {
	if s.hover.active {
		s.hover.retargets++
		s.hover.cause = s.timing.start
		s.hover.fresh = true
		return
	}
	s.animations++
	s.hover.begin(s.animations, s.timing.start)
	s.hover.active = true
	if !s.step.active && !s.fade.active {
		s.frameDue = time.Time{}
	}
}

// stopHover puts the levels at the hover as it is, at once
func (s *Selector) stopHover() {
	s.hover.active = false
	s.hover.levels = map[int]motion{}
	if s.hoverIndex >= 0 {
		var m motion
		m.rest(1)
		s.hover.levels[s.hoverIndex] = m
	}
}

// pruneHover forgets the levels at rest at 0
func (s *Selector) pruneHover(now time.Time) {
	for i, m := range s.hover.levels {
		if !m.moving(now) && m.at(now) == 0 {
			delete(s.hover.levels, i)
		}
	}
}

// hoverMoving reports whether a level moves at now
func (s *Selector) hoverMoving(now time.Time) bool {
	for _, m := range s.hover.levels {
		if m.moving(now) {
			return true
		}
	}
	return false
}

// hoverProgress is how far the motion of the levels is at now: every level
// moving was set moving by the same change
func (s *Selector) hoverProgress(now time.Time) float64 {
	p := 1.0
	for _, m := range s.hover.levels {
		p = math.Min(p, m.progress(now))
	}
	return p
}

// hoverLevels are the hover frames to draw at now, by index: those above 0
// of the windows shown, but the selected one, as at rest
func (s *Selector) hoverLevels(now time.Time) []hoverLevel {
	var levels []hoverLevel
	for i, m := range s.hover.levels {
		if v := m.at(now); v > 0 && i != s.selectedIndex && i < len(s.windows) {
			levels = append(levels, hoverLevel{i, v})
		}
	}
	sort.Slice(levels, func(a, b int) bool { return levels[a].index < levels[b].index })
	return levels
}

// hoverLook is how a hover frame is drawn at the level v
func (s *Selector) hoverLook(v float64) carousel.Fade {
	return s.anim.hover.look(v, hoverZoom)
}

// requestHoverLayers draws in the background the hover layers of the
// carousel the hovered card may need: at its offset and either side of it
func (s *Selector) requestHoverLayers() {
	if !s.animates() || s.grid() || !s.anim.hover.any() || s.hoverIndex < 0 || s.hoverIndex >= len(s.windows) {
		return
	}
	n := s.hoverIndex - s.selectedIndex
	var keys []cardKey
	for o := n - 1; o <= n+1; o++ {
		if o >= -5 && o <= 5 {
			keys = append(keys, cardKey{index: s.hoverIndex, offset: o, hover: true})
		}
	}
	s.requestLayers(keys)
}

// zoomRect is r scaled by f about the point cx, cy
func zoomRect(r carousel.Rect, cx, cy, f float64) carousel.Rect {
	if f == 1 {
		return r
	}
	return carousel.Rect{X: cx + (r.X-cx)*f, Y: cy + (r.Y-cy)*f, W: r.W * f, H: r.H * f}
}
