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
// comes and goes, and the scales their zooms start from; and the convergence
// of the selection frame onto its tile after a switch to the grid
// (specs/028-grid-locate)

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
	overlayZoom, hoverZoom float64       // the scales the zooms of the overlay and of the hover start from
	locate                 time.Duration // of the selection frame converging onto its tile; 0: none
	locateZoom             float64       // the scale it converges from
}

// parseAnimation reads the animations of a configuration. It returns a
// warning for a setting of enabled that is not auto, true or false, taken as
// auto (specs/031-animation-auto), for each effect name it does not know, for
// a negative duration and for a zoom factor that is not a positive number —
// that of locate_zoom not above 1 —, which it ignores: a negative duration as
// 0, a factor as its default. With the animation off, or at a duration of 0,
// nothing moves; at a hover duration of 0 the hover takes the duration; at a
// locate duration of 0 the selection frame does not converge. Under auto, the
// animations are those of true: whether an activation is still is decided
// as it starts (autoAnimation).
func parseAnimation(a config.Animation) (animationOptions, []string) {
	var o animationOptions
	var warnings []string
	mode, warning := a.Mode()
	if warning != "" {
		warnings = append(warnings, warning)
	}
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
	locateZoom, w := parseLocateZoom(a.LocateZoom, def.LocateZoom)
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
	ld := a.LocateDuration
	if ld < 0 {
		warnings = append(warnings, fmt.Sprintf("appearance.animation.locate_duration %v is negative: the selection frame does not converge onto its tile", ld))
		ld = 0
	}
	if mode == config.AnimationOff || d == 0 {
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
	o.locate, o.locateZoom = ld, locateZoom
	return o, warnings
}

// parseLocateZoom reads appearance.animation.locate_zoom: a factor above 1,
// from which the selection frame shrinks onto its tile; anything else is its
// default
func parseLocateZoom(factor, def float64) (float64, []string) {
	if factor > 1 && !math.IsInf(factor, 1) {
		return factor, nil
	}
	return def, []string{fmt.Sprintf(
		"appearance.animation.locate_zoom %v is not above 1: %v is used", factor, def)}
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
