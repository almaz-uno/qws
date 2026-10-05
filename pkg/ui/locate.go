package ui

import (
	"time"

	"github.com/almaz-uno/qws/pkg/carousel"
)

// The selection frame converging onto its tile after a switch to the grid
// (specs/028-grid-locate): the selection frame of the grid and the shadow of
// the selected tile appear scaled up about the tile's centre by locate_zoom
// and transparent, and in locate_duration shrink onto the tile and become
// opaque, eased out — a level from 0 to 1, which gridItems shows through the
// effects fade and zoom, as the hover frames. A step and a switch end it; the
// hover does not.

// locateAnimation is the convergence in progress, or one waiting for the
// layers of the grid
type locateAnimation struct {
	animationLog
	active bool
	level  motion    // 0: at locate_zoom and transparent; 1: on the tile, opaque
	key    time.Time // a switch made before the grid's layers were held: when its key was read; zero: none waits
}

// locates reports whether a switch to the grid would converge the selection
// frame: on a presenter that composes, at a locate duration above 0 — none
// under cpu or with the animations off (D5)
func (s *Selector) locates() bool {
	return s.animates() && s.anim.locate > 0
}

// beginLocate starts the convergence at now, for the key of a switch read at
// key; its frames are due at once unless others run
func (s *Selector) beginLocate(now, key time.Time) {
	s.animations++
	s.locate.begin(s.animations, key)
	s.locate.active, s.locate.key = true, time.Time{}
	s.locate.level.d = s.anim.locate
	s.locate.level.rest(0)
	s.locate.level.moveTo(now, 1)
	if !s.step.active && !s.fade.active && !s.hover.active {
		s.frameDue = time.Time{}
	}
}

// endLocate ends the convergence at once, or the wait for the layers of one
func (s *Selector) endLocate() {
	s.locate.active, s.locate.key = false, time.Time{}
}

// locateWhenHeld starts the convergence of a switch made before the grid's
// layers were held, now that they are, if within the locate duration of its
// key — else none — with the frame at rest drawn anew for its end
func (s *Selector) locateWhenHeld() {
	if s.locate.key.IsZero() || !s.gridReady() {
		return
	}
	key := s.locate.key
	s.locate.key = time.Time{}
	now := time.Now()
	if !s.grid() || now.Sub(key) >= s.anim.locate {
		return
	}
	s.beginLocate(now, key)
	s.requestRest(causeKey)
	s.rest.causeAt = key
	s.rest.awaited, s.rest.stepEnd = true, false
}

// locateLook is how the selection frame and the shadow of the selected tile
// are drawn at now, and whether they converge: faded by the level and zoomed
// from locate_zoom while it moves, as at rest once it is 1
func (s *Selector) locateLook(now time.Time) (carousel.Fade, bool) {
	if !s.locate.active || !s.locate.level.moving(now) {
		return carousel.Opaque, false
	}
	return effects{fade: true, zoom: true}.look(s.locate.level.at(now), s.anim.locateZoom), true
}
