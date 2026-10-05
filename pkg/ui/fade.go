package ui

import (
	"time"

	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog/log"
)

// The appearance and the disappearance of the overlay (specs/007-animation,
// specs/010-animation-options): its level moves from 0 to 1 or back in the
// duration of the animation, ease-out, a frame each refresh period, and the
// effects of show or hide make of it the alpha and the scale of what the
// overlay shows. The last frame of an appearance is at level 1, the frame as
// it is.

// fadeAnimation is the fade in progress
type fadeAnimation struct {
	animationLog
	active  bool
	out     bool   // the overlay goes
	pending bool   // an appearance starts with the first frame of the activation
	level   motion // 0: gone, 1: shown
}

// kind is the kind of the fade in the frame records
func (f *fadeAnimation) kind() string {
	if f.out {
		return "fade-out"
	}
	return "fade-in"
}

// beginFade starts a fade at the time at, caused by an event read at cause:
// from the level of a fade that runs, or from the end the fade starts at. Its
// first frame is a period into it, so that the frame shown at once is not
// blank.
func (s *Selector) beginFade(out bool, at, cause time.Time) {
	from := s.fade.level.at(at)
	if !s.fade.active {
		from = 0
		if out {
			from = 1
		}
	}
	to := 1.0
	if out {
		to = 0
	}
	s.animations++
	s.fade.begin(s.animations, cause)
	s.fade.active, s.fade.out, s.fade.pending = true, out, false
	s.fade.level.rest(from)
	s.fade.level.moveTo(at.Add(-s.period), to)
	if !s.step.active {
		s.frameDue = time.Time{}
	}
}

// fadeAt is how the overlay shows its frame at now: through the effects of
// the appearance or the disappearance in progress
func (s *Selector) fadeAt(now time.Time) carousel.Fade {
	if !s.fade.active {
		return carousel.Opaque
	}
	e := s.anim.show
	if s.fade.out {
		e = s.anim.hide
	}
	return e.look(s.fade.level.at(now), s.anim.overlayZoom)
}

// fadesOut reports whether the overlay disappears by an animation
func (s *Selector) fadesOut() bool {
	return s.animator != nil && s.anim.hide.any()
}

// FadeOut makes the overlay disappear — by the effects of hide when the
// presenter composes, at once otherwise — and unmaps it. It returns the
// events it read meanwhile, in order, for the caller to handle; a key press,
// as of a new activation, cuts it short.
func (s *Selector) FadeOut() []xgb.Event {
	if !s.mapped {
		return nil
	}
	defer s.hide()
	if !s.fadesOut() {
		return nil
	}

	s.setLive(false)
	s.beginFade(true, time.Now(), s.chosenAt)
	var events []xgb.Event
	for s.fade.active {
		event, err := s.conn.PollForEvent()
		if err != nil {
			log.Debug().Err(err).Msg("X error during the fade-out")
			continue
		}
		if event == nil {
			s.frame()
			continue
		}
		events = append(events, event)
		if _, ok := event.(xproto.KeyPressEvent); ok {
			break
		}
	}
	s.fade.active = false
	s.cancelStep()
	return events
}

// ChosenAt is when the event that ended the last activation was read: the
// release of the modifier, Enter, a click or the cancel key
func (s *Selector) ChosenAt() time.Time {
	return s.chosenAt
}

// hide unmaps the overlay, ends its live thumbnails and restores the layout
// of appearance.layout for the next activation
func (s *Selector) hide() {
	s.window.Hide()
	s.mapped = false
	s.setLive(false)
	s.restoreInitialLayoutMode()
}
