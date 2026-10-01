package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/almaz-uno/qws/pkg/carousel"
)

// The options of the animations (specs/010-animation-options): how long they
// take, whether the selection moves, and the effects by which the overlay
// appears and disappears and the hover frame comes and goes

// Scales a zoom starts from as what it zooms appears, and ends at as it goes
const (
	overlayZoom = 0.92 // the overlay grows from it to its size
	hoverZoom   = 1.05 // the hover frame closes in from it on its tile
)

// effects are the effects of a key
type effects struct{ fade, zoom bool }

// any reports whether something moves
func (e effects) any() bool {
	return e.fade || e.zoom
}

// look is how what the effects show is shown at the level v, from 0, gone,
// to 1, shown: faded by v, zoomed from the scale from at 0 to 1 at 1
func (e effects) look(v, from float64) carousel.Fade {
	f := carousel.Opaque
	if e.fade {
		f.Alpha = v
	}
	if e.zoom {
		f.Scale = from + (1-from)*v
	}
	return f
}

// animationOptions are the animations a configuration asks for; the zero
// value has none
type animationOptions struct {
	duration          time.Duration // of the appearance, the disappearance and the hover
	step              time.Duration // of a step; 0 when the selection does not move
	show, hide, hover effects
}

// parseAnimation reads the animations of a configuration. It returns a
// warning for each effect name it does not know and for a negative duration,
// which it ignores; with the animation off, or at a duration of 0, nothing
// moves.
func parseAnimation(a config.Animation) (animationOptions, []string) {
	var o animationOptions
	var warnings []string
	show, w := parseEffects("show", a.Show)
	warnings = append(warnings, w...)
	hide, w := parseEffects("hide", a.Hide)
	warnings = append(warnings, w...)
	hover, w := parseEffects("hover", a.Hover)
	warnings = append(warnings, w...)

	d := a.Duration
	if d < 0 {
		warnings = append(warnings, fmt.Sprintf("appearance.animation.duration %v is negative: nothing moves", d))
		d = 0
	}
	if !a.Enabled || d == 0 {
		return o, warnings
	}
	o.duration = d
	if a.Step {
		o.step = d
	}
	o.show, o.hide, o.hover = show, hide, hover
	return o, warnings
}

// parseEffects reads the effect names of the key appearance.animation.<key>:
// fade and zoom; none, or nothing, is no effect
func parseEffects(key string, names []string) (effects, []string) {
	var e effects
	var warnings []string
	for _, name := range names {
		switch strings.TrimSpace(name) {
		case "fade":
			e.fade = true
		case "zoom":
			e.zoom = true
		case "none", "":
		default:
			warnings = append(warnings, fmt.Sprintf(
				"appearance.animation.%s: unknown effect %q ignored; the effects are fade and zoom, or none", key, name))
		}
	}
	return e, warnings
}
