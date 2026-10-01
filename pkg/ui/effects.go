package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/almaz-uno/qws/pkg/carousel"
)

// The options of the animations (specs/010-animation-options,
// specs/014-appearance-keys): how long they take, whether the selection moves,
// the effects by which the overlay appears and disappears and the hover frame
// comes and goes, and the scales their zooms start from

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
	duration               time.Duration // of the appearance and the disappearance
	step                   time.Duration // of a step; 0 when the selection does not move
	hoverDuration          time.Duration // of the hover
	show, hide, hover      effects
	overlayZoom, hoverZoom float64 // the scales the zooms of the overlay and of the hover start from
}

// parseAnimation reads the animations of a configuration. It returns a
// warning for each effect name it does not know, for a negative duration and
// for a zoom factor that is not a positive number, which it ignores: a
// negative duration as 0, a factor as its default. With the animation off,
// or at a duration of 0, nothing moves; at a hover duration of 0 the hover
// takes the duration.
func parseAnimation(a config.Animation) (animationOptions, []string) {
	var o animationOptions
	var warnings []string
	show, w := parseEffects("show", a.Show)
	warnings = append(warnings, w...)
	hide, w := parseEffects("hide", a.Hide)
	warnings = append(warnings, w...)
	hover, w := parseEffects("hover", a.Hover)
	warnings = append(warnings, w...)
	def := config.Default().Appearance.Animation
	overlayZoom, w := parseZoom("overlay_zoom", a.OverlayZoom, def.OverlayZoom)
	warnings = append(warnings, w...)
	hoverZoom, w := parseZoom("hover_zoom", a.HoverZoom, def.HoverZoom)
	warnings = append(warnings, w...)

	d := a.Duration
	if d < 0 {
		warnings = append(warnings, fmt.Sprintf("appearance.animation.duration %v is negative: nothing moves", d))
		d = 0
	}
	hd := a.HoverDuration
	if hd < 0 {
		warnings = append(warnings, fmt.Sprintf("appearance.animation.hover_duration %v is negative: the hover takes appearance.animation.duration", hd))
		hd = 0
	}
	if !a.Enabled || d == 0 {
		return o, warnings
	}
	o.duration = d
	if a.Step {
		o.step = d
	}
	o.hoverDuration = d
	if hd > 0 {
		o.hoverDuration = hd
	}
	o.show, o.hide, o.hover = show, hide, hover
	o.overlayZoom, o.hoverZoom = overlayZoom, hoverZoom
	return o, warnings
}

// parseZoom reads the zoom factor of the key appearance.animation.<key>: a
// positive number; anything else is its default
func parseZoom(key string, factor, def float64) (float64, []string) {
	if factor > 0 && !math.IsInf(factor, 1) {
		return factor, nil
	}
	return def, []string{fmt.Sprintf(
		"appearance.animation.%s %v is not a positive number: %v is used", key, factor, def)}
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
