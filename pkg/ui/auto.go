package ui

import (
	"fmt"
	"image"
	"strings"
	"time"
	"unicode"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog/log"
)

// The setting auto of appearance.animation.enabled (specs/031-animation-auto):
// an activation is animated, or still — as with enabled: false, and without
// the live thumbnails — while a VNC viewer is connected (D4); each change is
// said in the log once (D7). The frames that slip no longer make it still,
// and the key of keybindings.animation_toggle turns the animation on and off
// over the setting (specs/035-slip-by-frame-rate).

// Why an activation is still under auto
const (
	stillNot    = ""       // animated
	stillViewer = "viewer" // a VNC viewer connected
)

// The animation as the key of keybindings.animation_toggle left it
// (specs/035-slip-by-frame-rate, D2)
const (
	forcedNot = iota // as the setting says
	forcedOn         // animated, whatever the setting and the viewer
	forcedOff        // still
)

// autoAnimation decides whether an activation is animated or still under
// auto. The zero value is off: every activation animated.
type autoAnimation struct {
	on     bool                    // appearance.animation.enabled is auto, on a presenter that composes
	ports  []int                   // appearance.animation.vnc_ports; none: no viewer is looked for
	viewer func([]int) (int, bool) // the check for a viewer on the ports; nil: viewerConnected

	reason string          // why the activation is still: stillNot, stillViewer
	look   chan viewerLook // the viewer looked for in the background for the activation; nil: none
}

// viewerLook is a viewer looked for: its port, whether it is connected, and
// how long the look took
type viewerLook struct {
	port int
	ok   bool
	took time.Duration
}

// begin decides at the start of an activation, before its first frame,
// whether it is still: while a viewer is connected (D4 of 031) — the look of
// lookForViewer waited for, if one runs. Off, never.
func (a *autoAnimation) begin() bool {
	if !a.on {
		return false
	}
	if port, ok := a.viewerOn(); ok {
		a.become(stillViewer, port)
		return true
	}
	a.become(stillNot, 0)
	return false
}

// lookForViewer starts looking for a viewer connected on the ports, when
// there are any, in the background: the tables take some 5 ms to read on ws1,
// the kernel walking its hash of connections for each (research of 031), and
// they are read while the first frame of the activation is drawn
func (a *autoAnimation) lookForViewer() {
	a.look = nil
	if !a.on || len(a.ports) == 0 {
		return
	}
	check := a.viewer
	if check == nil {
		check = viewerConnected
	}
	ports, look := a.ports, make(chan viewerLook, 1)
	go func() {
		start := time.Now()
		port, ok := check(ports)
		look <- viewerLook{port, ok, time.Since(start)}
	}()
	a.look = look
}

// viewerOn is the viewer connected on the ports, when there are any: the
// look started by lookForViewer, waited for, or one made now
func (a *autoAnimation) viewerOn() (int, bool) {
	if len(a.ports) == 0 {
		return 0, false
	}
	if a.look == nil {
		a.lookForViewer()
	}
	if a.look == nil {
		return 0, false
	}
	r := <-a.look
	a.look = nil
	log.Debug().
		Ints("ports", a.ports).
		Bool("connected", r.ok).
		Dur("check_ms", r.took).
		Msg("VNC viewer")
	return r.port, r.ok
}

// become makes reason why the activation is still, and logs it when it
// changes, at info level (D7 of 031): the port of a viewer
func (a *autoAnimation) become(reason string, port int) {
	if reason == a.reason {
		return
	}
	was := a.reason
	a.reason = reason
	switch reason {
	case stillViewer:
		log.Info().Int("port", port).Msg("Animation still: a VNC viewer connected")
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
// that composes — under cpu nothing moves. Under false the animations are
// read as under true, for the key to turn them on
// (specs/035-slip-by-frame-rate, D2).
func (s *Selector) initAnimations(a config.Animation) {
	anim, warnings := parseAnimation(a)
	mode, _ := a.Mode() // its warning is among those of parseAnimation
	if mode == config.AnimationOff {
		on := a
		on.Enabled = string(config.AnimationOn)
		anim, _ = parseAnimation(on)
	}
	ports, w := parseVNCPorts(a.VNCPorts)
	warnings = append(warnings, w...)
	for _, w := range warnings {
		log.Warn().Msg(w)
	}
	s.animated = anim
	s.animMode = mode
	s.auto = autoAnimation{on: mode == config.AnimationAuto && s.animator != nil, ports: ports}
	s.setAnimation(s.settled())
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

// animState is what an activation is: animated; off — nothing moves, the
// live thumbnails as when animated: enabled: false —; or still — nothing
// moves, no live thumbnails: auto with a viewer (D3 of 031), or the key
type animState int

const (
	animOn animState = iota
	animOff
	animStill
)

// settled is the state the key and the setting give an activation before it
// is decided: the key's; the setting's; under auto the one the last
// activation was decided to — the viewer is looked for while the first frame
// is drawn, and that frame shows the hint of this state
func (s *Selector) settled() animState {
	switch {
	case s.forced == forcedOn:
		return animOn
	case s.forced == forcedOff:
		return animStill
	case s.animMode == config.AnimationOff:
		return animOff
	case s.auto.reason == stillViewer:
		return animStill
	}
	return animOn
}

// beginAnimation starts deciding, at the start of an activation, whether it
// is animated or still: the viewer is looked for while the first frame is
// drawn — not when the key decided —, and render decides before it presents
// it (decideAnimation). The animation key is up.
func (s *Selector) beginAnimation() {
	s.animKeyDown = false
	if s.forced == forcedNot {
		s.auto.lookForViewer()
	}
	s.deciding = true
	s.setAnimation(s.settled())
}

// decideAnimation decides, before the first frame of the activation is
// presented, whether the activation is animated or still: by the key, else
// by the setting and, under auto, the viewer. It reports whether the hint of
// the animation key changed: the frame drawn with the hint before is drawn
// again.
func (s *Selector) decideAnimation() bool {
	if !s.deciding {
		return false
	}
	s.deciding = false
	hint := s.config.AnimationHint
	if s.forced == forcedNot && s.auto.begin() {
		s.setAnimation(animStill)
	} else {
		s.setAnimation(s.settled())
	}
	return s.config.AnimationHint != hint
}

// setAnimation makes the activation animated, off or still: its animations
// those of the configuration, or none, with the durations of the motions of
// the step and of the fade; the hint of the animation key for it
func (s *Selector) setAnimation(state animState) {
	s.animState = state
	s.still = state == animStill
	o := s.animated
	if state != animOn {
		o = animationOptions{}
	}
	s.anim = o
	s.step.pos.d, s.step.gx.d, s.step.gy.d, s.step.sy.d = o.step, o.step, o.step, o.step
	s.fade.level.d = o.duration
	s.config.AnimationHint = s.animationHint()
}

// stillNow makes the activation still in its middle: what moves — the step,
// the hover levels, the convergence of the selection frame, a fade-in — is at
// its target in the next frame; a fade-out ends at once, the overlay
// unmapped; the live passes stop
func (s *Selector) stillNow() {
	s.setAnimation(animStill)
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

// parseAnimationToggle is the keysym of the animation key named by
// keybindings.animation_toggle and the name its hint shows; 0 and "" for no
// key: an empty name, one keygrab does not know, or one of the keys taken by
// the switcher, which keep their meaning — the last two reported
// (specs/035-slip-by-frame-rate, D2, as parseLayoutToggle of 026)
func parseAnimationToggle(name string, taken ...uint32) (uint32, string) {
	keysym, err := parseToggle(name, taken...)
	switch {
	case err == errToggleTaken:
		log.Warn().Str("animation_toggle", name).Msg("Animation toggle key is another key of the switcher, no animation toggle")
	case err != nil:
		log.Warn().Err(err).Str("animation_toggle", name).Msg("Failed to parse animation toggle key, no animation toggle")
	}
	if keysym == 0 {
		return 0, ""
	}
	return keysym, strings.TrimSpace(name)
}

// animationHint is the hint of the animation key in the header, the state
// the activation is in: "A — animation on", "A — animation off", a name of
// one letter upper-cased, any other as written; none without the key, the
// header or a presenter that composes (specs/035-slip-by-frame-rate, D3)
func (s *Selector) animationHint() string {
	key := s.keyConfig.animationToggleName
	if key == "" || !s.appearance.Header.Enabled || s.animator == nil {
		return ""
	}
	if r := []rune(key); len(r) == 1 && unicode.IsLetter(r[0]) {
		key = strings.ToUpper(key)
	}
	if s.animState == animOn {
		return key + " — animation on"
	}
	return key + " — animation off"
}

// pressAnimationKey takes a press of the animation key, without the
// workspace modifier, on a presenter that composes: the key is down. The
// presses the server repeats while it is held change nothing.
func (s *Selector) pressAnimationKey(keycode xproto.Keycode, state uint16) bool {
	if s.keyConfig.animationToggleKeysym == 0 || s.animator == nil ||
		state&s.keyConfig.workspaceModifierMask != 0 || !s.isKeycode(keycode, s.keyConfig.animationToggleKeysym) {
		return false
	}
	s.animKeyDown = true
	return true
}

// releaseAnimationKey takes a release of the animation key pressed while the
// switcher is shown and turns the animation over (D2). A release the server
// makes while the key is held, repeating it, is none: the key is down when
// the server is asked.
func (s *Selector) releaseAnimationKey(e xproto.KeyReleaseEvent, thumbnails []image.Image) bool {
	if !s.animKeyDown || !s.isKeycode(e.Detail, s.keyConfig.animationToggleKeysym) {
		return false
	}
	held := s.keyHeld
	if held == nil {
		held = s.keyDown
	}
	if held(e.Detail) {
		return true
	}
	s.animKeyDown = false
	s.toggleAnimation(thumbnails)
	return true
}

// keyDown reports whether the server has the key of keycode down
func (s *Selector) keyDown(keycode xproto.Keycode) bool {
	reply, err := xproto.QueryKeymap(s.conn).Reply()
	if err != nil {
		log.Warn().Err(err).Msg("Failed to query the keymap")
		return false
	}
	return reply.Keys[keycode/8]&(1<<(keycode%8)) != 0
}

// toggleAnimation turns the animation of the activation over, and of the
// next ones until qws restarts or the key is pressed again (D2): animated
// becomes still at once — what moves at its target, the live thumbnails
// off —; off or still becomes animated — the next steps move, the live
// thumbnails on. The frame is drawn anew, with the hint of the new state.
func (s *Selector) toggleAnimation(thumbnails []image.Image) {
	if s.animState == animOn {
		s.forced = forcedOff
		log.Info().Msg("Animation toggled off by the key")
		s.stillNow()
	} else {
		s.forced = forcedOn
		log.Info().Msg("Animation toggled on by the key")
		s.setAnimation(animOn)
	}
	s.dropLayers()
	s.render(thumbnails)
	s.prefetch()
	if s.animState == animOn && !s.fade.active {
		s.setLive(true)
	}
}
