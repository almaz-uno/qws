package ui

import (
	"testing"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/jezek/xgb/xproto"
)

// Criteria of specs/035-slip-by-frame-rate: the animation key, a by default,
// on the presenter of the tests of pkg/ui

const keyA, keyRight = 0x0061, 0xFF53

// releaseA hands the selector a release of the animation key; it reports
// whether the release was the key's
func releaseA(t *testing.T, s *Selector) bool {
	t.Helper()
	return s.releaseAnimationKey(xproto.KeyReleaseEvent{Detail: testKeycode(t, keyA)}, s.prepareThumbnails())
}

// toggleA presses and releases the animation key, and runs the loop until
// nothing moves
func toggleA(t *testing.T, s *Selector) {
	t.Helper()
	press(t, s, keyA, 0)
	if !releaseA(t, s) {
		t.Fatal("the release of the animation key not taken")
	}
	loopIdle(s, func() bool { return false })
}

// lastHint is the hint of the animation key in the last frame drawn
func lastHint(s *Selector) string {
	f, _ := s.renderer.(*frameRecorder).last()
	return f.animHint
}

// stepMoves checks that the records have frames of a step on its way
func stepMoves(t *testing.T, name string, records []map[string]any) {
	t.Helper()
	for _, r := range records {
		if r["message"] == "Animation frame" && r["kind"] == "carousel" && r["progress"] != 1.0 {
			return
		}
	}
	t.Errorf("%s: no frame of a step on its way", name)
}

// isAnimated checks the activation animated — the animations of the
// configuration, the live thumbnails asked for — with its hint
func isAnimated(t *testing.T, name string, s *Selector) {
	t.Helper()
	if s.still || s.animState != animOn || s.anim != s.animated || s.anim == (animationOptions{}) ||
		s.config.AnimationHint != "A — animation on" || lastHint(s) != "A — animation on" {
		t.Errorf("%s: still %v, state %v, the hint %q, drawn %q; want animated, A — animation on",
			name, s.still, s.animState, s.config.AnimationHint, lastHint(s))
	}
}

// isStill checks the activation still — nothing moves, no live pass — with
// its hint
func isStill(t *testing.T, name string, s *Selector, src *passCounter) {
	t.Helper()
	if !s.still || s.animState != animStill || s.anim != (animationOptions{}) || src.interval != 0 ||
		s.config.AnimationHint != "A — animation off" || lastHint(s) != "A — animation off" {
		t.Errorf("%s: still %v, state %v, the live interval %v, the hint %q, drawn %q; want still, A — animation off",
			name, s.still, s.animState, src.interval, s.config.AnimationHint, lastHint(s))
	}
}

// TestAnimationKeyViewer checks K2: under auto with a VNC viewer connected,
// the key turns the animation on at once — the steps move, the live
// thumbnails on — and it stays on in the next activations, the viewer not
// looked for; the key again turns it off at once, and it stays off with the
// viewer gone. One record a change.
func TestAnimationKeyViewer(t *testing.T) {
	records := logRecords(t)
	s, a, src := autoSelector(t, config.Default().Appearance, 12)
	connected, looks := true, 0
	s.auto.ports = []int{5900}
	s.auto.viewer = func(ports []int) (int, bool) { looks++; return ports[0], connected }

	activate(s)
	isStill(t, "a viewer", s, src)
	toggleA(t, s)
	isAnimated(t, "a viewer, the key", s)
	if src.on != 1 {
		t.Errorf("a viewer, the key: the live passes asked for %d times, want 1", src.on)
	}
	from := len(records())
	pressKey(t, s, keyRight)
	loopIdle(s, func() bool { return false })
	stepMoves(t, "a viewer, the key", records()[from:])
	deactivate(s)

	a.shown = nil
	activate(s)
	isAnimated(t, "the next activation", s)
	if looks != 1 || len(a.shown) == 0 || a.shown[0].fade == carousel.Opaque {
		t.Errorf("the next activation: %d looks for a viewer, the first frame %+v; want 1, a fade-in", looks, a.shown)
	}

	toggleA(t, s)
	isStill(t, "the key again", s, src)
	deactivate(s)
	connected = false
	a.shown = nil
	activate(s)
	isStill(t, "the viewer gone", s, src)
	if looks != 1 || a.shown[0].fade != carousel.Opaque {
		t.Errorf("the viewer gone: %d looks for a viewer, the first frame %+v; want 1, at once", looks, a.shown[0])
	}
	deactivate(s)

	all := records()
	for msg, want := range map[string]int{
		"Animation toggled on by the key":         1,
		"Animation toggled off by the key":        1,
		"Animation still: a VNC viewer connected": 1,
		"Animation back":                          0,
	} {
		if n := count(all, msg); n != want {
			t.Errorf("%d records %q, want %d", n, msg, want)
		}
	}
	for _, r := range all {
		if m, _ := r["message"].(string); (m == "Animation toggled on by the key" || m == "Animation toggled off by the key") && r["level"] != "info" {
			t.Errorf("the record %v, want at info level", r)
		}
	}
}

// TestAnimationKeyStep checks K2: under auto without a viewer, the key in the
// middle of a step makes it still at once — the step at its target, the next
// steps at once, the live passes stopped —, and the next activation still;
// the key again: animated, the next activation too
func TestAnimationKeyStep(t *testing.T) {
	records := logRecords(t)
	s, a, src := autoSelector(t, config.Default().Appearance, 12)
	activate(s)
	isAnimated(t, "no viewer", s)

	pressKey(t, s, keyRight)
	s.frame()
	s.frame()
	if !s.step.active {
		t.Fatal("the step not moving")
	}
	toggleA(t, s)
	if s.step.active || s.selectedIndex != 2 {
		t.Errorf("the key in a step: moving %v, the selection %d; want at its target, 2", s.step.active, s.selectedIndex)
	}
	isStill(t, "the key in a step", s, src)
	from := len(records())
	pressKey(t, s, keyRight)
	loopIdle(s, func() bool { return false })
	stepAtOnce(t, "the next step", records()[from:])
	deactivate(s)

	a.shown = nil
	activate(s)
	isStill(t, "the next activation", s, src)
	if a.shown[0].fade != carousel.Opaque {
		t.Errorf("the next activation: the first frame %+v, want at once", a.shown[0])
	}
	toggleA(t, s)
	isAnimated(t, "the key again", s)
	from = len(records())
	pressKey(t, s, keyRight)
	loopIdle(s, func() bool { return false })
	stepMoves(t, "the key again", records()[from:])
	deactivate(s)
	activate(s)
	isAnimated(t, "the activation after", s)
	deactivate(s)
}

// TestAnimationKeySettings checks K2: the key over true — animated, then
// still — and over false — nothing moving and the live thumbnails as before,
// the hint off; then animated, the steps moving
func TestAnimationKeySettings(t *testing.T) {
	quietLog(t)
	on := config.Default().Appearance
	on.Animation.Enabled = "true"
	s, _, src := autoSelector(t, on, 12)
	activate(s)
	isAnimated(t, "true", s)
	toggleA(t, s)
	isStill(t, "true, the key", s, src)
	deactivate(s)

	off := config.Default().Appearance
	off.Animation.Enabled = "false"
	s, _, src = autoSelector(t, off, 12)
	activate(s)
	if s.still || s.anim != (animationOptions{}) || src.on != 1 || lastHint(s) != "A — animation off" {
		t.Errorf("false: still %v, options %+v, the passes asked for %d times, the hint %q; want nothing moving, the passes, off",
			s.still, s.anim, src.on, lastHint(s))
	}
	toggleA(t, s)
	isAnimated(t, "false, the key", s)
	records := logRecords(t)
	pressKey(t, s, keyRight)
	loopIdle(s, func() bool { return false })
	stepMoves(t, "false, the key", records())
	deactivate(s)
}

// TestAnimationKeyRepeat checks K2 and D2: the key acts as it is released —
// the presses the server repeats while it is held change nothing, nor do the
// releases it makes then; a release of a key not pressed while shown, or
// pressed with the workspace modifier, changes nothing
func TestAnimationKeyRepeat(t *testing.T) {
	quietLog(t)
	s, _, src := autoSelector(t, config.Default().Appearance, 12)
	activate(s)
	held := true
	s.keyHeld = func(xproto.Keycode) bool { return held }
	for i := 0; i < 5; i++ {
		press(t, s, keyA, 0)
		if !releaseA(t, s) || s.animState != animOn {
			t.Fatalf("a repeat %d: the release not taken, or the state %v", i, s.animState)
		}
	}
	held = false
	press(t, s, keyA, 0)
	if !releaseA(t, s) {
		t.Fatal("the release not taken")
	}
	loopIdle(s, func() bool { return false })
	isStill(t, "held, then released", s, src)
	if releaseA(t, s) {
		t.Error("a second release taken")
	}

	ctrl := uint16(s.keyConfig.workspaceModifierMask)
	press(t, s, keyA, ctrl)
	if releaseA(t, s) || s.animState != animStill {
		t.Errorf("with the workspace modifier: taken, the state %v", s.animState)
	}

	// Pressed in one activation, released in the next
	press(t, s, keyA, 0)
	deactivate(s)
	activate(s)
	if releaseA(t, s) || s.animState != animStill {
		t.Errorf("a press of the last activation: taken, the state %v", s.animState)
	}
	deactivate(s)
}

// TestAnimationKeyHint checks K3 and the key's edges: the hint of the key's
// name — a letter upper-cased, any other name as written —; none with the
// header off, with no key, under cpu, where the key does nothing either; a
// name taken by another key of the switcher, or unknown, reported, no key;
// the first frame drawn again when the viewer, come since the last
// activation, changes the hint
func TestAnimationKeyHint(t *testing.T) {
	records := logRecords(t)
	s, _, _ := autoSelector(t, config.Default().Appearance, 12)
	s.keyConfig.animationToggleKeysym, s.keyConfig.animationToggleName = parseAnimationToggle("F2")
	activate(s)
	if s.config.AnimationHint != "F2 — animation on" {
		t.Errorf("F2: the hint %q", s.config.AnimationHint)
	}
	deactivate(s)

	noHeader := config.Default().Appearance
	noHeader.Header.Enabled = false
	s, _, _ = autoSelector(t, noHeader, 12)
	activate(s)
	toggleA(t, s)
	if s.config.AnimationHint != "" || lastHint(s) != "" || !s.still {
		t.Errorf("no header: the hint %q, drawn %q, still %v; want none, still by the key", s.config.AnimationHint, lastHint(s), s.still)
	}
	deactivate(s)

	s, _, _ = autoSelector(t, config.Default().Appearance, 12)
	s.keyConfig.animationToggleKeysym, s.keyConfig.animationToggleName = parseAnimationToggle("")
	s.setAnimation(s.settled())
	activate(s)
	press(t, s, keyA, 0)
	if releaseA(t, s) || s.config.AnimationHint != "" || s.still {
		t.Errorf("no key: the hint %q, still %v", s.config.AnimationHint, s.still)
	}
	deactivate(s)

	cpu, _ := keySelector(t, config.Default().Appearance, "q", 12)
	cpu.keyConfig.animationToggleKeysym, cpu.keyConfig.animationToggleName = parseAnimationToggle("a")
	cpu.initAnimations(config.Default().Appearance.Animation)
	press(t, cpu, keyA, 0)
	if releaseA(t, cpu) || cpu.config.AnimationHint != "" {
		t.Errorf("cpu: the release taken, or the hint %q", cpu.config.AnimationHint)
	}

	for _, c := range []struct {
		name    string
		taken   []uint32
		keysym  uint32
		message string
	}{
		{"a", []uint32{0x0071}, 0x0061, ""},
		{"q", []uint32{0x0071}, 0, "Animation toggle key is another key of the switcher, no animation toggle"},
		{"Tab", []uint32{0xFF09}, 0, "Animation toggle key is another key of the switcher, no animation toggle"},
		{"nosuchkey", nil, 0, "Failed to parse animation toggle key, no animation toggle"},
		{"", nil, 0, ""},
	} {
		n := len(records())
		keysym, name := parseAnimationToggle(c.name, c.taken...)
		got := records()[n:]
		if keysym != c.keysym || (keysym != 0) != (name != "") {
			t.Errorf("%q: 0x%X %q, want 0x%X", c.name, keysym, name, c.keysym)
		}
		if c.message == "" && len(got) != 0 || c.message != "" && (len(got) != 1 || got[0]["message"] != c.message || got[0]["level"] != "warn") {
			t.Errorf("%q: the records %v, want %q", c.name, got, c.message)
		}
	}

	// The first frame drawn with the hint of the last activation, and again
	// once the look for a viewer says otherwise
	s, _, _ = autoSelector(t, config.Default().Appearance, 12)
	connected := false
	s.auto.ports = []int{5900}
	s.auto.viewer = func(ports []int) (int, bool) { return ports[0], connected }
	rec := s.renderer.(*frameRecorder)
	for _, c := range []struct {
		connected bool
		draws     int
		hint      string
	}{{false, 1, "A — animation on"}, {true, 2, "A — animation off"}, {true, 1, "A — animation off"}, {false, 2, "A — animation on"}} {
		connected = c.connected
		s.BeginActivation(s.timing.start, 0)
		s.dropLayers()
		s.beginAnimation()
		_, before := rec.last()
		s.render(s.prepareThumbnails())
		if f, after := rec.last(); after-before != c.draws || f.animHint != c.hint {
			t.Errorf("a viewer %v: the first frame drawn %d times, the hint %q; want %d, %q", c.connected, after-before, f.animHint, c.draws, c.hint)
		}
		loopIdle(s, func() bool { return false })
		deactivate(s)
	}
}
