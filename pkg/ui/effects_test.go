package ui

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/almaz-uno/qws/internal/config"
)

// TestParseAnimation checks K3 of specs/010-animation-options: an unknown
// effect name and a negative duration give a warning each and are ignored;
// none and nothing are no effect; with the animation off nothing moves
func TestParseAnimation(t *testing.T) {
	a := config.Default().Appearance.Animation
	o, warnings := parseAnimation(a)
	both := effects{fade: true, zoom: true}
	want := animationOptions{
		duration: 150 * time.Millisecond, step: 150 * time.Millisecond, hoverDuration: 150 * time.Millisecond,
		show: both, hide: both, hover: both, overlayZoom: 0.92, hoverZoom: 1.05,
		locate: 400 * time.Millisecond, locateZoom: 1.6,
	}
	if o != want || len(warnings) != 0 {
		t.Errorf("defaults: %+v, %v; want %+v, no warning", o, warnings, want)
	}

	a.Show = []string{"zoom", " fade"}
	a.Hide = []string{"none"}
	a.Hover = []string{"blink", "zoom"}
	a.Step = false
	o, warnings = parseAnimation(a)
	want = animationOptions{
		duration: 150 * time.Millisecond, hoverDuration: 150 * time.Millisecond,
		show: effects{fade: true, zoom: true}, hover: effects{zoom: true},
		overlayZoom: 0.92, hoverZoom: 1.05, locate: 400 * time.Millisecond, locateZoom: 1.6,
	}
	if o != want {
		t.Errorf("options %+v, want %+v", o, want)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"blink"`) || !strings.Contains(warnings[0], "hover") {
		t.Errorf("warnings %q, want one about blink in hover", warnings)
	}

	for _, off := range []config.Animation{
		{Enabled: "false", Duration: 150 * time.Millisecond, Step: true, Show: []string{"fade"}, OverlayZoom: 0.92, HoverZoom: 1.05, LocateDuration: time.Second, LocateZoom: 1.6},
		{Enabled: "true", Duration: 0, Step: true, Show: []string{"fade"}, OverlayZoom: 0.92, HoverZoom: 1.05, LocateDuration: time.Second, LocateZoom: 1.6},
	} {
		if o, warnings := parseAnimation(off); o != (animationOptions{}) || len(warnings) != 0 {
			t.Errorf("%+v: %+v, %v; want nothing moving, no warning", off, o, warnings)
		}
	}

	a = config.Default().Appearance.Animation
	a.Duration = -time.Second
	o, warnings = parseAnimation(a)
	if o != (animationOptions{}) || len(warnings) != 1 || !strings.Contains(warnings[0], "negative") {
		t.Errorf("negative duration: %+v, %v; want nothing moving, one warning", o, warnings)
	}
}

// TestLook checks the effects at the ends of a level: gone and shown
func TestLook(t *testing.T) {
	const overlayZoom, hoverZoom = 0.92, 1.05
	both := effects{fade: true, zoom: true}
	if f := both.look(0, overlayZoom); f.Alpha != 0 || f.Scale != overlayZoom {
		t.Errorf("both at 0: %+v", f)
	}
	for _, e := range []effects{{}, {fade: true}, {zoom: true}, both} {
		if f := e.look(1, hoverZoom); f.Alpha != 1 || f.Scale != 1 {
			t.Errorf("%+v at 1: %+v, want opaque", e, f)
		}
	}
	if f := (effects{zoom: true}).look(0.5, hoverZoom); f.Alpha != 1 || f.Scale <= 1 || f.Scale >= hoverZoom {
		t.Errorf("zoom alone half way: %+v", f)
	}
}

// Criteria of specs/014-appearance-keys

// TestHoverDuration checks K3: the hover takes the duration of every
// animation by default and its own when set; a negative one gives a warning
// and is ignored; with the animation off the hover does not move either
func TestHoverDuration(t *testing.T) {
	a := config.Default().Appearance.Animation
	a.Duration = 300 * time.Millisecond
	if o, _ := parseAnimation(a); o.hoverDuration != 300*time.Millisecond {
		t.Errorf("by default %v, want that of duration, 300ms", o.hoverDuration)
	}

	a.HoverDuration = 80 * time.Millisecond
	o, warnings := parseAnimation(a)
	if o.hoverDuration != 80*time.Millisecond || o.duration != 300*time.Millisecond || o.step != 300*time.Millisecond {
		t.Errorf("hover %v, duration %v, step %v; want 80ms, 300ms, 300ms", o.hoverDuration, o.duration, o.step)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings %v, want none", warnings)
	}

	a.HoverDuration = -time.Second
	o, warnings = parseAnimation(a)
	if o.hoverDuration != 300*time.Millisecond || len(warnings) != 1 || !strings.Contains(warnings[0], "hover_duration") {
		t.Errorf("negative: %v, %v; want that of duration and one warning", o.hoverDuration, warnings)
	}

	a.HoverDuration = 80 * time.Millisecond
	for _, off := range []func(*config.Animation){
		func(a *config.Animation) { a.Enabled = "false" },
		func(a *config.Animation) { a.Duration = 0 },
	} {
		b := a
		off(&b)
		if o, _ := parseAnimation(b); o != (animationOptions{}) {
			t.Errorf("%+v: %+v, want nothing moving", b, o)
		}
	}

	// The levels of the hover move by it
	s := &Selector{anim: animationOptions{hoverDuration: 80 * time.Millisecond}, hoverIndex: 2}
	now := time.Unix(1000, 0)
	s.setHover(now, true)
	if m := s.hover.levels[2]; m.d != 80*time.Millisecond || !m.moving(now.Add(79*time.Millisecond)) || m.moving(now.Add(80*time.Millisecond)) {
		t.Errorf("level of the hovered: %+v, want a motion of 80ms", m)
	}
}

// Criteria of specs/028-grid-locate

// TestLocateOptions checks K5: the selection frame converges in
// locate_duration from locate_zoom; a factor not above 1 gives a warning and
// is its default; a negative duration gives a warning and is none, as 0;
// with the animation off, none
func TestLocateOptions(t *testing.T) {
	a := config.Default().Appearance.Animation
	a.LocateDuration, a.LocateZoom = 250*time.Millisecond, 2.5
	if o, warnings := parseAnimation(a); o.locate != 250*time.Millisecond || o.locateZoom != 2.5 || len(warnings) != 0 {
		t.Errorf("250ms, 2.5: %v, %v, warnings %v", o.locate, o.locateZoom, warnings)
	}
	for _, bad := range []float64{1, 0.9, 0, -2, math.NaN(), math.Inf(1)} {
		a.LocateZoom = bad
		o, warnings := parseAnimation(a)
		if o.locateZoom != 1.6 || len(warnings) != 1 || !strings.Contains(warnings[0], "locate_zoom") || !strings.Contains(warnings[0], "above 1") {
			t.Errorf("factor %v: %v, warnings %q; want 1.6 and one warning", bad, o.locateZoom, warnings)
		}
	}
	a.LocateZoom = 1.6
	for _, d := range []time.Duration{0, -time.Second} {
		a.LocateDuration = d
		o, warnings := parseAnimation(a)
		if o.locate != 0 || o.step == 0 || len(warnings) != map[bool]int{true: 1, false: 0}[d < 0] {
			t.Errorf("duration %v: %v, warnings %q; want none, the rest moving, a warning if negative", d, o.locate, warnings)
		}
	}
	a = config.Default().Appearance.Animation
	a.Enabled = "false"
	if o, warnings := parseAnimation(a); o.locate != 0 || len(warnings) != 0 {
		t.Errorf("the animations off: %v, warnings %v; want none", o.locate, warnings)
	}
}

// TestZoomFactors checks K4: the factors of the keys are the scales the zoom
// of the overlay and of the hover start from; a factor that is not a
// positive number gives a warning and is its default
func TestZoomFactors(t *testing.T) {
	a := config.Default().Appearance.Animation
	a.OverlayZoom, a.HoverZoom = 0.5, 1.5
	o, warnings := parseAnimation(a)
	if o.overlayZoom != 0.5 || o.hoverZoom != 1.5 || len(warnings) != 0 {
		t.Errorf("factors %v, %v, warnings %v; want 0.5, 1.5, none", o.overlayZoom, o.hoverZoom, warnings)
	}
	s := &Selector{anim: o}
	if f := s.hoverLook(0); f.Scale != 1.5 || f.Alpha != 0 {
		t.Errorf("hover at 0: %+v, want scale 1.5, alpha 0", f)
	}
	now := time.Unix(1000, 0)
	s.fade.active = true
	s.fade.level.rest(0)
	if f := s.fadeAt(now); f.Scale != 0.5 || f.Alpha != 0 {
		t.Errorf("appearance at 0: %+v, want scale 0.5, alpha 0", f)
	}
	s.fade.out = true
	s.fade.level.rest(0.5)
	if f := s.fadeAt(now); f.Scale != 0.75 || f.Alpha != 0.5 {
		t.Errorf("disappearance half way: %+v, want scale 0.75, alpha 0.5", f)
	}

	for _, bad := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		a.OverlayZoom, a.HoverZoom = bad, bad
		o, warnings := parseAnimation(a)
		if o.overlayZoom != 0.92 || o.hoverZoom != 1.05 || len(warnings) != 2 ||
			!strings.Contains(warnings[0], "overlay_zoom") || !strings.Contains(warnings[1], "hover_zoom") {
			t.Errorf("factor %v: %v, %v, warnings %q; want the defaults and two warnings", bad, o.overlayZoom, o.hoverZoom, warnings)
		}
	}
}
