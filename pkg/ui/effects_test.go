package ui

import (
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
		duration: 150 * time.Millisecond, step: 150 * time.Millisecond,
		show: both, hide: both, hover: both,
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
		duration: 150 * time.Millisecond,
		show:     effects{fade: true, zoom: true}, hover: effects{zoom: true},
	}
	if o != want {
		t.Errorf("options %+v, want %+v", o, want)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"blink"`) || !strings.Contains(warnings[0], "hover") {
		t.Errorf("warnings %q, want one about blink in hover", warnings)
	}

	for _, off := range []config.Animation{
		{Enabled: false, Duration: 150 * time.Millisecond, Step: true, Show: []string{"fade"}},
		{Enabled: true, Duration: 0, Step: true, Show: []string{"fade"}},
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
