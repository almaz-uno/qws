package ui

import (
	"time"

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
