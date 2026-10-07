package ui

import (
	"fmt"
	"time"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/rs/zerolog/log"
)

// The setting auto of appearance.animation.enabled (specs/031-animation-auto):
// an activation is animated, or still — as with enabled: false, and without
// the live thumbnails — while a VNC viewer is connected (D4) or once the
// frames slip (D5), and then animated again at a probe five minutes later
// (D6); each change is said in the log once (D7).

const (
	slipWindow = 60              // the last frames counted that the count looks at (D5)
	slipLate   = 3               // late frames among them that make it still
	slipFactor = 1.25            // a frame is late above this many refresh periods
	probeAfter = 5 * time.Minute // still for the frames this long, the next activation is animated again (D6)
)

// Why an activation is still
const (
	stillNot    = ""       // animated
	stillViewer = "viewer" // a VNC viewer connected
	stillFrames = "frames" // the frames slipped
)

// countsSlips reports whether the frames of an animation of kind, as the frame
// records name it, are counted: those of the steps of the carousel and of the
// grid, of the hover and of the convergence of the selection frame; not those
// of the fades, which miss without a viewer too (D5, O1)
func countsSlips(kind string) bool {
	switch kind {
	case "carousel", "grid", "hover", "locate":
		return true
	}
	return false
}

// autoAnimation decides whether an activation is animated or still. The zero
// value is off: every activation animated.
type autoAnimation struct {
	on     bool                    // appearance.animation.enabled is auto, on a presenter that composes
	ports  []int                   // appearance.animation.vnc_ports; none: no viewer is looked for
	viewer func([]int) (int, bool) // the check for a viewer on the ports; nil: viewerConnected
	clock  func() time.Time        // the time of the probe; nil: time.Now

	late   [slipWindow]bool // the last frames counted, a ring: whether each was late
	frames int              // frames in the ring, up to slipWindow
	next   int              // where the next frame counted goes in it
	count  int              // late frames in it

	slipped time.Time // when the frames slipped; zero: they did not, or a probe started since
	reason  string    // why the activation is still: stillNot, stillViewer, stillFrames
}

// now is the time of the clock
func (a *autoAnimation) now() time.Time {
	if a.clock != nil {
		return a.clock()
	}
	return time.Now()
}

// begin decides at the start of an activation, before its first frame,
// whether it is still: while a viewer is connected (D4); else within
// probeAfter of when the frames slipped — past it the activation is animated,
// its count started anew: the probe (D6). Off, never.
func (a *autoAnimation) begin() bool {
	if !a.on {
		return false
	}
	if port, ok := a.viewerOn(); ok {
		a.become(stillViewer, port)
		return true
	}
	if !a.slipped.IsZero() {
		if a.now().Sub(a.slipped) < probeAfter {
			a.become(stillFrames, 0)
			return true
		}
		a.slipped = time.Time{}
		a.late, a.frames, a.next, a.count = [slipWindow]bool{}, 0, 0, 0
	}
	a.become(stillNot, 0)
	return false
}

// viewerOn looks for a viewer connected on the ports, when there are any
func (a *autoAnimation) viewerOn() (int, bool) {
	if len(a.ports) == 0 {
		return 0, false
	}
	check := a.viewer
	if check == nil {
		check = viewerConnected
	}
	start := time.Now()
	port, ok := check(a.ports)
	log.Debug().
		Ints("ports", a.ports).
		Bool("connected", ok).
		Dur("check_ms", time.Since(start)).
		Msg("VNC viewer")
	return port, ok
}

// frame counts a frame of an animation presented interval after the frame
// before it of its animation, at the refresh period period, while the
// activation is animated. It reports whether the frames now slip — the
// third late of the last slipWindow counted, across activations (D5) —
// and the activation is still from then.
func (a *autoAnimation) frame(interval, period time.Duration) bool {
	if !a.on || a.reason != stillNot {
		return false
	}
	late := float64(interval) > slipFactor*float64(period)
	if a.frames == slipWindow {
		if a.late[a.next] {
			a.count--
		}
	} else {
		a.frames++
	}
	a.late[a.next] = late
	if late {
		a.count++
	}
	a.next = (a.next + 1) % slipWindow
	if a.count < slipLate {
		return false
	}
	a.slipped = a.now()
	a.become(stillFrames, 0)
	return true
}

// become makes reason why the activation is still, and logs it when it
// changes, at info level (D7): the port of a viewer, the late frames of the
// count and the frames counted
func (a *autoAnimation) become(reason string, port int) {
	if reason == a.reason {
		return
	}
	was := a.reason
	a.reason = reason
	switch reason {
	case stillViewer:
		log.Info().Int("port", port).Msg("Animation still: a VNC viewer connected")
	case stillFrames:
		log.Info().Int("late", a.count).Int("frames", a.frames).Msg("Animation still: the frames slip")
	default:
		log.Info().Str("was", was).Msg("Animation back")
	}
}

// parseVNCPorts reads appearance.animation.vnc_ports: TCP ports, 1 to 65535;
// it returns a warning for any other, which it leaves out
func parseVNCPorts(ports []int) ([]int, []string) {
	var kept []int
	var warnings []string
	for _, p := range ports {
		if p < 1 || p > 65535 {
			warnings = append(warnings, fmt.Sprintf("appearance.animation.vnc_ports: %d is not a TCP port, left out", p))
			continue
		}
		kept = append(kept, p)
	}
	return kept, warnings
}

// initAnimations reads the animations of the configuration, and the setting
// that decides whether an activation is animated: auto only on a presenter
// that composes — under cpu nothing moves
func (s *Selector) initAnimations(a config.Animation) {
	anim, warnings := parseAnimation(a)
	mode, _ := a.Mode() // its warning is among those of parseAnimation
	ports, w := parseVNCPorts(a.VNCPorts)
	warnings = append(warnings, w...)
	for _, w := range warnings {
		log.Warn().Msg(w)
	}
	s.animated = anim
	s.auto = autoAnimation{on: mode == config.AnimationAuto && s.animator != nil, ports: ports}
	s.setStill(false)
	log.Debug().
		Bool("composes", s.animator != nil).
		Str("enabled", string(mode)).
		Ints("vnc_ports", ports).
		Dur("duration", anim.duration).
		Dur("step", anim.step).
		Dur("hover_duration", anim.hoverDuration).
		Interface("show", anim.show).
		Interface("hide", anim.hide).
		Interface("hover", anim.hover).
		Float64("overlay_zoom", anim.overlayZoom).
		Float64("hover_zoom", anim.hoverZoom).
		Dur("locate_duration", anim.locate).
		Float64("locate_zoom", anim.locateZoom).
		Msg("Animations")
}

// beginAnimation decides, at the start of an activation and before its
// first frame, whether it is animated or still
func (s *Selector) beginAnimation() {
	s.setStill(s.auto.begin())
}

// setStill makes the animations of the activation those of the
// configuration, or none while still — as enabled: false gives —, with the
// durations of the motions of the step and of the fade
func (s *Selector) setStill(still bool) {
	s.still = still
	o := s.animated
	if still {
		o = animationOptions{}
	}
	s.anim = o
	s.step.pos.d, s.step.gx.d, s.step.gy.d = o.step, o.step, o.step
	s.fade.level.d = o.duration
}

// stillNow makes the activation still in its middle, the frames having
// slipped (D5): what moves — the step, the hover levels, the convergence of
// the selection frame, a fade-in — is at its target in the next frame; a
// fade-out ends at once, the overlay unmapped; the live passes stop
func (s *Selector) stillNow() {
	s.setStill(true)
	s.locate.level.d = 0
	for i, m := range s.hover.levels {
		m.d = 0
		s.hover.levels[i] = m
	}
	if s.fade.active && s.fade.out {
		s.fade.active = false
	}
	s.setLive(false)
}
