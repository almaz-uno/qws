package ui

import (
	"time"

	"github.com/rs/zerolog/log"
)

// Frame causes, as logged in the "cause" field
const (
	causeActivation = "activation" // first frame of an activation (metric M1)
	causeKey        = "key"        // frame caused by a key press in the selector (metric M2)
	causeEvent      = "event"      // frame caused by any other event
)

// frameTiming tracks what caused the next frame and when, so that render can
// log the frame latency at debug level (specs/001-rendering-speed)
type frameTiming struct {
	activation int           // number of the current activation, from 1
	cause      string        // cause of the next frame
	start      time.Time     // when the cause was read from the X connection
	list       time.Duration // window list collection of the current activation (M1.list)
}

// BeginActivation marks the start of an activation: start is when the
// activating key press was read, list is how long the window list took
func (s *Selector) BeginActivation(start time.Time, list time.Duration) {
	s.timing.activation++
	s.timing.cause = causeActivation
	s.timing.start = start
	s.timing.list = list
}

// markFrameCause records that the event just read may cause a frame
func (s *Selector) markFrameCause(cause string) {
	s.timing.cause = cause
	s.timing.start = time.Now()
}

// logFrame logs the latency of the frame drawn between drawStart and drawEnd
// and presented at end
func (s *Selector) logFrame(drawStart, drawEnd, end time.Time) {
	t := &s.timing
	cause, start := t.cause, t.start
	t.cause = ""

	e := log.Debug()
	if !e.Enabled() {
		return
	}
	if cause == "" {
		cause, start = causeEvent, drawStart
	}

	e = e.Str("cause", cause).
		Int("activation", t.activation).
		Str("renderer", s.appearance.Renderer).
		Str("layout", s.config.LayoutMode).
		Int("windows", len(s.windows))
	if cause == causeActivation {
		e = e.Bool("cold", t.activation == 1).
			Dur("list_ms", t.list).
			Dur("show_ms", drawStart.Sub(start.Add(t.list)))
	}
	e.Dur("draw_ms", drawEnd.Sub(drawStart)).
		Dur("present_ms", end.Sub(drawEnd)).
		Dur("total_ms", end.Sub(start)).
		Msg("Frame")
}
