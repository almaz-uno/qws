package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/almaz-uno/qws/pkg/snapshot"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Criteria of specs/031-animation-auto

// quietLog drops the records of the test
func quietLog(t *testing.T) {
	saved := log.Logger
	log.Logger = zerolog.Nop()
	t.Cleanup(func() { log.Logger = saved })
}

// passCounter is a snapshotter of the live thumbnails that counts the times
// the passes are asked for
type passCounter struct {
	fakeSource
	on int
}

func (p *passCounter) SetLive(overlay xproto.Window, interval time.Duration) {
	if interval > 0 {
		p.on++
	}
	p.fakeSource.SetLive(overlay, interval)
}

// autoSelector is the selector of keySelector on a sceneAnimator, with the
// animation key a (specs/035-slip-by-frame-rate), its animations read from
// the appearance as NewSelector reads them, before its first activation; the
// live thumbnails on, the passes counted; no viewer — the machine of the
// test may have one —, and no key held down when asked
func autoSelector(t *testing.T, appearance config.Appearance, n int) (*Selector, *sceneAnimator, *passCounter) {
	t.Helper()
	s, _ := keySelector(t, appearance, "q", n)
	a := &sceneAnimator{livePresenter: livePresenter{&fakePresenter{}}}
	s.presenter, s.animator = a, a
	s.keyConfig.animationToggleKeysym, s.keyConfig.animationToggleName = parseAnimationToggle("a")
	s.keyHeld = func(xproto.Keycode) bool { return false }
	s.initAnimations(appearance.Animation)
	src := &passCounter{fakeSource: fakeSource{pics: map[xproto.Window]snapshot.Picture{}}}
	s.live = liveThumbnails{snap: src, presenter: &fakePresenter{}, atom: 77, drawn: map[xproto.Window]uint64{}}
	s.window = &carousel.Window{}
	s.auto.viewer = func([]int) (int, bool) { return 0, false }
	return s, a, src
}

// activate starts an activation as Show does, without its window — the
// layers dropped, animated or still decided, the first frame — and runs the
// loop until nothing moves
func activate(s *Selector) {
	s.BeginActivation(time.Now(), 0)
	s.dropLayers()
	s.beginAnimation()
	s.showFirst(s.prepareThumbnails())
	loopIdle(s, func() bool { return false })
}

// deactivate ends an activation as hide does, without its window
func deactivate(s *Selector) {
	s.setLive(false)
	s.restoreInitialLayoutMode()
}

// syncBuffer is a buffer the log writes to from any goroutine
type syncBuffer struct {
	sync.Mutex
	bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(p)
}

// logRecords captures the log at debug level for the test; the function
// returned gives its records so far
func logRecords(t *testing.T) func() []map[string]any {
	t.Helper()
	buf := &syncBuffer{}
	level, logger := zerolog.GlobalLevel(), log.Logger
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	log.Logger = zerolog.New(buf).Level(zerolog.DebugLevel)
	t.Cleanup(func() { zerolog.SetGlobalLevel(level); log.Logger = logger })
	return func() []map[string]any {
		buf.Lock()
		defer buf.Unlock()
		var records []map[string]any
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			var r map[string]any
			if err := json.Unmarshal([]byte(line), &r); err != nil {
				t.Fatalf("%q: %v", line, err)
			}
			records = append(records, r)
		}
		return records
	}
}

// count is the number of records of the message
func count(records []map[string]any, message string) int {
	n := 0
	for _, r := range records {
		if r["message"] == message {
			n++
		}
	}
	return n
}

// stepAtOnce checks that the records have frames of a step and that each
// is at its target, and that no fade-in is among them
func stepAtOnce(t *testing.T, name string, records []map[string]any) {
	t.Helper()
	steps := 0
	for _, r := range records {
		if r["message"] != "Animation frame" {
			continue
		}
		switch r["kind"] {
		case "fade-in":
			t.Errorf("%s: a frame of a fade-in %v", name, r)
		case "carousel":
			steps++
			if r["progress"] != 1.0 {
				t.Errorf("%s: a frame of the step on its way %v", name, r)
			}
		}
	}
	if steps == 0 {
		t.Errorf("%s: no frame of the step", name)
	}
}

// TestStillViewer checks K4 of specs/031-animation-auto, D4 and D7: a VNC
// viewer connected at the start of an activation makes it still from its
// first frame — no fade-in, the steps at once, no live pass —, the next ones
// as well while it stays, with one record; gone, the next activation is
// animated again, with one record
func TestStillViewer(t *testing.T) {
	const right = 0xFF53
	records := logRecords(t)
	s, a, src := autoSelector(t, config.Default().Appearance, 12)
	connected := true
	s.auto.ports = []int{5900}
	s.auto.viewer = func(ports []int) (int, bool) { return ports[0], connected }

	for i := 1; i <= 2; i++ {
		name := fmt.Sprintf("activation %d with a viewer", i)
		a.shown = nil
		from := len(records())
		activate(s)
		if !s.still || len(a.shown) == 0 || a.shown[0].scene || a.shown[0].fade != carousel.Opaque {
			t.Errorf("%s: still %v, the first frame %+v; want still, a frame opaque at once", name, s.still, a.shown)
		}
		before := s.selectedIndex
		pressKey(t, s, right)
		loopIdle(s, func() bool { return false })
		if s.selectedIndex != before+1 {
			t.Errorf("%s: the selection at %d, want %d", name, s.selectedIndex, before+1)
		}
		stepAtOnce(t, name, records()[from:])
		if src.on != 0 {
			t.Errorf("%s: the live passes asked for %d times, want none", name, src.on)
		}
		deactivate(s)
	}

	connected = false
	a.shown = nil
	activate(s)
	if s.still || len(a.shown) == 0 || a.shown[0].fade == carousel.Opaque || src.on != 1 {
		t.Errorf("the viewer gone: still %v, the first frame %+v, the passes asked for %d times; want a fade-in and the passes",
			s.still, a.shown, src.on)
	}
	deactivate(s)

	all := records()
	if n := count(all, "Animation still: a VNC viewer connected"); n != 1 {
		t.Errorf("%d records of the viewer, want 1", n)
	}
	for _, r := range all {
		if r["message"] == "Animation still: a VNC viewer connected" && (r["level"] != "info" || r["port"] != 5900.0) {
			t.Errorf("the record of the viewer %v, want at info level with the port", r)
		}
	}
	if n := count(all, "Animation back"); n != 1 {
		t.Errorf("%d records of the animation back, want 1", n)
	}
}

// TestAnimationSettings checks K4 of specs/031-animation-auto for true and
// false, and D1: true never still — a viewer connected, every frame far
// slower than the refresh rate — with no record; false as before, nothing
// moving, nothing looked for; under cpu no auto
func TestAnimationSettings(t *testing.T) {
	const right = 0xFF53
	records := logRecords(t)
	viewer := func(ports []int) (int, bool) { return 5900, true }

	on := config.Default().Appearance
	on.Animation.Enabled = "true"
	s, a, src := autoSelector(t, on, 12)
	s.auto.ports, s.auto.viewer = []int{5900}, viewer
	activate(s)
	s.period = time.Nanosecond
	pressKey(t, s, right)
	loopIdle(s, func() bool { return false })
	s.period = 7 * time.Millisecond
	if s.still || s.anim != s.animated || a.shown[0].fade == carousel.Opaque || src.on != 1 {
		t.Errorf("true: still %v, options %+v; want animated", s.still, s.anim)
	}
	deactivate(s)

	off := config.Default().Appearance
	off.Animation.Enabled = "false"
	s, a, src = autoSelector(t, off, 12)
	s.auto.ports, s.auto.viewer = []int{5900}, viewer
	activate(s)
	if s.still || s.anim != (animationOptions{}) || a.shown[0].fade != carousel.Opaque || src.on != 1 {
		t.Errorf("false: still %v, options %+v, the passes asked for %d times; want nothing moving, the live thumbnails as before",
			s.still, s.anim, src.on)
	}
	deactivate(s)

	if n := count(records(), "Animation still: a VNC viewer connected") + count(records(), "Animation back"); n != 0 {
		t.Errorf("%d records of the setting under true and false, want none", n)
	}

	cpu, _ := keySelector(t, config.Default().Appearance, "q", 12)
	cpu.initAnimations(config.Default().Appearance.Animation)
	if cpu.auto.on {
		t.Error("auto on without a presenter that composes")
	}
}

// drawSignal is a renderer that says when it has drawn a frame of the
// carousel
type drawSignal struct {
	*frameRecorder
	drawn chan struct{}
	once  sync.Once
}

func (d *drawSignal) Draw3DCarouselWithData(data []carousel.WindowData, selected, hover int, offset float64, cfg carousel.Config) *image.RGBA {
	defer d.once.Do(func() { close(d.drawn) })
	return d.frameRecorder.Draw3DCarouselWithData(data, selected, hover, offset, cfg)
}

// TestViewerWhileDrawn checks the look of D4 of specs/031-animation-auto as
// the plan makes it: in the background while the first frame of the
// activation is drawn, and waited for before it is presented
func TestViewerWhileDrawn(t *testing.T) {
	quietLog(t)
	s, a, _ := autoSelector(t, config.Default().Appearance, 12)
	d := &drawSignal{frameRecorder: s.renderer.(*frameRecorder), drawn: make(chan struct{})}
	s.renderer = d
	s.auto.ports = []int{5900}
	waited := false
	s.auto.viewer = func(ports []int) (int, bool) {
		select {
		case <-d.drawn:
		case <-time.After(5 * time.Second):
			waited = true
		}
		return ports[0], true
	}
	activate(s)
	if waited || !s.still || len(a.shown) == 0 || a.shown[0].fade != carousel.Opaque {
		t.Errorf("the look waited for the drawing %v; still %v, the first frame %+v; want the look while it is drawn, still from it",
			!waited, s.still, a.shown)
	}
}
