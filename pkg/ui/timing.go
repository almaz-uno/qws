package ui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
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
	dumpDir    string        // where the first frame of every activation is written, if set
}

// BeginActivation marks the start of an activation: start is when the
// activating key press was read, list is how long the window list took
func (s *Selector) BeginActivation(start time.Time, list time.Duration) {
	s.timing.activation++
	s.timing.cause = causeActivation
	s.timing.start = start
	s.timing.list = list
}

// SetFrameDump makes the selector write the first frame of every activation to
// dir as raw RGBA bytes, for comparison with a capture of the window
// (criterion K3 of specs/001-rendering-speed)
func (s *Selector) SetFrameDump(dir string) {
	s.timing.dumpDir = dir
}

// dumpFrame writes the frame if it is the first of an activation and dumping
// is on
func (s *Selector) dumpFrame(img *image.RGBA) {
	t := &s.timing
	if t.dumpDir == "" || t.cause != causeActivation {
		return
	}
	file := filepath.Join(t.dumpDir, fmt.Sprintf("frame-%d.rgba", t.activation))
	if err := os.WriteFile(file, img.Pix, 0o644); err != nil {
		log.Error().Err(err).Str("file", file).Msg("Failed to dump frame")
		return
	}
	log.Debug().
		Int("activation", t.activation).
		Uint32("window", uint32(s.window.GetWindowID())).
		Int("width", img.Bounds().Dx()).
		Int("height", img.Bounds().Dy()).
		Str("file", file).
		Msg("Frame dumped")
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
