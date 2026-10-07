package ui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strconv"
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

// frameRun is a run of frame records of one kind in one activation, of a
// batch of the research
type frameRun struct {
	activation int
	kind       string
	intervals  []time.Duration
}

// readBatch reads a batch of the research from testdata: its refresh period
// and its runs of records, in their order
func readBatch(t *testing.T, name string) (time.Duration, []frameRun) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var period time.Duration
	var runs []frameRun
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 3 && fields[0] == "#" && fields[1] == "period_us" {
			us, err := strconv.ParseFloat(fields[2], 64)
			if err != nil {
				t.Fatal(err)
			}
			period = time.Duration(us * float64(time.Microsecond))
			continue
		}
		if len(fields) == 0 || fields[0] == "#" {
			continue
		}
		if len(fields) < 3 {
			t.Fatalf("%s: %q", name, sc.Text())
		}
		run := frameRun{kind: fields[1]}
		if run.activation, err = strconv.Atoi(fields[0]); err != nil {
			t.Fatal(err)
		}
		for _, s := range fields[2:] {
			us, err := strconv.Atoi(s)
			if err != nil {
				t.Fatal(err)
			}
			run.intervals = append(run.intervals, time.Duration(us)*time.Microsecond)
		}
		runs = append(runs, run)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if period == 0 || len(runs) == 0 {
		t.Fatalf("%s: period %v, %d runs", name, period, len(runs))
	}
	return period, runs
}

// replay is the count of a batch replayed into an autoAnimation, as the
// switcher counts it: begin at each activation, then the frames of the kinds
// counted. The clock stands still: no probe.
type replay struct {
	frames, late int // counted until it tripped, and the late among them
	activation   int // where it tripped, 0: never
	frame        int // the frame counted in that activation that tripped it
	most         int // the most late of slipWindow while it counted
}

func replayBatch(period time.Duration, runs []frameRun, counted func(string) bool) replay {
	start := time.Unix(1000, 0)
	a := autoAnimation{on: true, clock: func() time.Time { return start }}
	var r replay
	activation, n := 0, 0
	for _, run := range runs {
		if run.activation != activation {
			activation, n = run.activation, 0
			a.begin()
		}
		if !counted(run.kind) {
			continue
		}
		for _, interval := range run.intervals {
			if a.reason != stillNot {
				return r
			}
			n++
			r.frames++
			if float64(interval) > slipFactor*float64(period) {
				r.late++
			}
			tripped := a.frame(interval, period)
			r.most = max(r.most, a.count)
			if tripped {
				r.activation, r.frame = activation, n
			}
		}
	}
	return r
}

// TestSlipReplay checks K2: the frames of the batches of the research
// replayed into the count — batch A, with a VNC viewer connected, makes it
// still within its first three activations: in the first, at its 30th frame
// counted, in its second step; batch B, without, never: 2 late of 60 at the
// most. With the fade-out counted, as D5 was first written, B would go still
// in its fourth activation (O1).
func TestSlipReplay(t *testing.T) {
	quietLog(t)
	for _, c := range []struct {
		batch        string
		counted      func(string) bool
		frames, late int // counted, as research "The count replayed" has them
		want         replay
	}{
		{"vnc-a.txt", countsSlips, 30, 3, replay{activation: 1, frame: 30, most: 3}},
		{"vnc-b.txt", countsSlips, 1499, 4, replay{most: 2}},
		{"vnc-b.txt", func(kind string) bool { return countsSlips(kind) || kind == "fade-out" }, 388, 4,
			replay{activation: 4, frame: 17, most: 3}},
	} {
		period, runs := readBatch(t, c.batch)
		got := replayBatch(period, runs, c.counted)
		c.want.frames, c.want.late = c.frames, c.late
		t.Logf("%s, the fade-out counted %v: %d frames counted, %d late; tripped in activation %d at its frame %d; at most %d late of %d",
			c.batch, c.counted("fade-out"), got.frames, got.late, got.activation, got.frame, got.most, slipWindow)
		if got != c.want {
			t.Errorf("%s, the fade-out counted %v: %+v, want %+v", c.batch, c.counted("fade-out"), got, c.want)
		}
	}

	// Every frame of the steps, as the research counts them
	for _, c := range []struct {
		batch        string
		frames, late int
	}{{"vnc-a.txt", 1495, 141}, {"vnc-b.txt", 1499, 4}} {
		period, runs := readBatch(t, c.batch)
		frames, late := 0, 0
		for _, run := range runs {
			if run.kind != "carousel" {
				continue
			}
			for _, interval := range run.intervals {
				frames++
				if float64(interval) > slipFactor*float64(period) {
					late++
				}
			}
		}
		if frames != c.frames || late != c.late {
			t.Errorf("%s: %d intervals of the steps, %d late; want %d, %d", c.batch, frames, late, c.frames, c.late)
		}
	}
}

// TestSlipCount checks the edges of K2: a frame is late above 1.25 refresh
// periods; 2 late of the last 60 counted leave the activation animated, 3
// make it still; a late frame counts for 60 frames; the count is carried
// across activations; the fades are not counted (D5, O1); while still, or
// off, nothing is counted
func TestSlipCount(t *testing.T) {
	quietLog(t)
	const period = 8 * time.Millisecond
	onTime, late, edge := period, 11*time.Millisecond, 10*time.Millisecond // 1.25 periods: not above
	start := time.Unix(1000, 0)
	newCount := func() *autoAnimation {
		return &autoAnimation{on: true, clock: func() time.Time { return start }}
	}

	// frames counts the intervals; it reports the frame that tripped the
	// count, from 1, or 0
	frames := func(a *autoAnimation, intervals ...time.Duration) int {
		for i, d := range intervals {
			if a.frame(d, period) {
				return i + 1
			}
		}
		return 0
	}
	repeat := func(d time.Duration, n int) []time.Duration {
		s := make([]time.Duration, n)
		for i := range s {
			s[i] = d
		}
		return s
	}

	a := newCount()
	if n := frames(a, repeat(edge, 100)...); n != 0 || a.count != 0 {
		t.Errorf("intervals of 1.25 periods: tripped at %d, %d late; want none late", n, a.count)
	}
	a = newCount()
	if n := frames(a, late, onTime, late, onTime, edge+time.Nanosecond); n != 5 {
		t.Errorf("three intervals above 1.25 periods: tripped at %d, want 5", n)
	}

	// 2 late of 60, then a third 60 frames after the first: no; one more
	// within 60 of the second and third: yes
	a = newCount()
	run := append(append([]time.Duration{late}, repeat(onTime, 58)...), late) // 60 frames, 2 late
	if n := frames(a, run...); n != 0 || a.count != 2 {
		t.Fatalf("2 late of 60: tripped at %d, %d late", n, a.count)
	}
	if n := frames(a, late); n != 0 || a.count != 2 {
		t.Errorf("a third late 60 frames after the first: tripped at %d, %d late of the last 60; want 2, not tripped", n, a.count)
	}
	if n := frames(a, onTime, late); n != 2 || a.count != 3 || a.reason != stillFrames {
		t.Errorf("3 late of the last 60: tripped at %d, %d late, still for %q", n, a.count, a.reason)
	}
	if n := frames(a, late, late); n != 0 {
		t.Error("frames counted while still")
	}

	// Carried across activations: 2 late in one, a third in the next
	a = newCount()
	frames(a, late, onTime, late)
	if a.begin() {
		t.Fatal("2 late: the next activation still")
	}
	if n := frames(a, onTime, late); n != 2 {
		t.Errorf("a third late in the next activation: tripped at %d, want 2", n)
	}
	if !a.begin() {
		t.Error("the activation after the frames slipped animated")
	}

	// The kinds counted: the steps, the hover, the convergence; not the fades
	for kind, want := range map[string]bool{
		"carousel": true, "grid": true, "hover": true, "locate": true, "fade-in": false, "fade-out": false,
	} {
		if countsSlips(kind) != want {
			t.Errorf("frames of %s counted %v, want %v", kind, !want, want)
		}
	}

	// Off: never still, nothing counted
	off := &autoAnimation{viewer: func([]int) (int, bool) { return 5900, true }, ports: []int{5900}}
	if off.begin() || frames(off, repeat(late, 10)...) != 0 || off.count != 0 {
		t.Error("off: still, or frames counted")
	}
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

// autoSelector is the selector of keySelector on a sceneAnimator, its
// animations read from the appearance as NewSelector reads them, before its
// first activation; the live thumbnails on, the passes counted; the clock of
// the probe the test's, and no viewer: the machine of the test may have one
func autoSelector(t *testing.T, appearance config.Appearance, n int) (*Selector, *sceneAnimator, *passCounter, *time.Time) {
	t.Helper()
	s, _ := keySelector(t, appearance, "q", n)
	a := &sceneAnimator{livePresenter: livePresenter{&fakePresenter{}}}
	s.presenter, s.animator = a, a
	s.initAnimations(appearance.Animation)
	src := &passCounter{fakeSource: fakeSource{pics: map[xproto.Window]snapshot.Picture{}}}
	s.live = liveThumbnails{snap: src, presenter: &fakePresenter{}, atom: 77, drawn: map[xproto.Window]uint64{}}
	s.window = &carousel.Window{}
	clock := new(time.Time)
	*clock = time.Unix(1_000_000, 0)
	s.auto.clock = func() time.Time { return *clock }
	s.auto.viewer = func([]int) (int, bool) { return 0, false }
	return s, a, src, clock
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
	s, a, src, _ := autoSelector(t, config.Default().Appearance, 12)
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

// TestStillFrames checks K4 of specs/031-animation-auto, D5–D7: the third
// late frame of a step makes the activation still in the middle of the
// step: the step is at its target in the next frame, the next steps at once,
// the live passes stop; the next activation still, with no record; five
// minutes after, by the clock of the test, the probe: animated, its count
// started anew, and still again on 3 late. A fade-out with a step moving
// ends at once.
func TestStillFrames(t *testing.T) {
	const right = 0xFF53
	records := logRecords(t)
	s, a, src, clock := autoSelector(t, config.Default().Appearance, 12)
	activate(s)
	if s.still || src.on != 1 {
		t.Fatalf("the first activation: still %v, the passes asked for %d times; want animated, asked for", s.still, src.on)
	}

	// trip steps once with every frame late — intervals far above 1.25
	// periods of 1 ns — until the count trips; it returns the frames
	// presented
	trip := func() int {
		t.Helper()
		s.period = time.Nanosecond
		defer func() { s.period = 7 * time.Millisecond }()
		pressKey(t, s, right)
		n := 0
		for !s.still && n < 100 {
			s.frame()
			n++
		}
		return n
	}

	n := trip()
	// The first frame of the step has no interval: the third late is its
	// fourth
	if !s.still || n != 4 {
		t.Fatalf("every frame late: still %v after %d frames; want still after 4", s.still, n)
	}
	if !s.step.active || time.Since(s.step.pos.start) >= s.animated.step {
		t.Fatalf("tripped after the step: active %v, %v since it started", s.step.active, time.Since(s.step.pos.start))
	}
	from := len(records())
	s.frame()
	if s.step.active {
		t.Error("the frame after the trip: the step still moves")
	}
	loopIdle(s, func() bool { return false })
	stepAtOnce(t, "the step tripped", records()[from:])
	if src.overlay != 0 || src.interval != 0 {
		t.Error("the live passes not stopped")
	}

	from = len(records())
	pressKey(t, s, right)
	loopIdle(s, func() bool { return false })
	stepAtOnce(t, "the next step", records()[from:])
	deactivate(s)

	// A minute later: still, no fade-in, no live pass
	*clock = clock.Add(time.Minute)
	a.shown = nil
	from = len(records())
	activate(s)
	pressKey(t, s, right)
	loopIdle(s, func() bool { return false })
	if !s.still || a.shown[0].fade != carousel.Opaque || src.on != 1 {
		t.Errorf("the next activation: still %v, the first frame %+v, the passes asked for %d times; want still at once, none",
			s.still, a.shown[0], src.on)
	}
	stepAtOnce(t, "the next activation", records()[from:])
	deactivate(s)

	// Five minutes after the frames slipped: the probe
	*clock = clock.Add(4 * time.Minute)
	a.shown = nil
	activate(s)
	if s.still || a.shown[0].fade == carousel.Opaque || src.on != 2 || s.auto.frames != 0 {
		t.Errorf("the probe: still %v, the first frame %+v, the passes asked for %d times, %d frames counted; want a fade-in, the passes, a count anew",
			s.still, a.shown[0], src.on, s.auto.frames)
	}
	if n := trip(); !s.still || n != 4 {
		t.Errorf("the probe, every frame late: still %v after %d frames; want still after 4", s.still, n)
	}
	loopIdle(s, func() bool { return false })
	deactivate(s)

	all := records()
	if n := count(all, "Animation still: the frames slip"); n != 2 {
		t.Errorf("%d records of the frames, want 2", n)
	}
	for _, r := range all {
		if r["message"] == "Animation still: the frames slip" && (r["level"] != "info" || r["late"] != 3.0 || r["frames"] != 3.0) {
			t.Errorf("the record of the frames %v, want at info level with 3 late of 3", r)
		}
	}
	if n := count(all, "Animation back"); n != 1 {
		t.Errorf("%d records of the animation back, want 1", n)
	}

	// A step moving in the fade-out, tripped: the fade-out ends at once
	f, _, _, _ := autoSelector(t, config.Default().Appearance, 12)
	activate(f)
	f.period = time.Nanosecond
	pressKey(t, f, right)
	f.setLive(false)
	f.beginFade(true, time.Now(), time.Now())
	for i := 0; i < 100 && !f.still; i++ {
		f.frame()
	}
	if !f.still || f.fade.active {
		t.Errorf("the fade-out with a step: still %v, fading %v; want still and ended", f.still, f.fade.active)
	}
	f.period = 7 * time.Millisecond
	loopIdle(f, func() bool { return false })
}

// TestAnimationSettings checks K4 of specs/031-animation-auto for true and
// false, and D1: true never still — a viewer connected, every frame late —
// with no record; false as before, nothing moving, nothing looked for;
// under cpu no auto
func TestAnimationSettings(t *testing.T) {
	const right = 0xFF53
	records := logRecords(t)
	viewer := func(ports []int) (int, bool) { return 5900, true }

	on := config.Default().Appearance
	on.Animation.Enabled = "true"
	s, a, src, _ := autoSelector(t, on, 12)
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
	s, a, src, _ = autoSelector(t, off, 12)
	s.auto.ports, s.auto.viewer = []int{5900}, viewer
	activate(s)
	if s.still || s.anim != (animationOptions{}) || a.shown[0].fade != carousel.Opaque || src.on != 1 {
		t.Errorf("false: still %v, options %+v, the passes asked for %d times; want nothing moving, the live thumbnails as before",
			s.still, s.anim, src.on)
	}
	deactivate(s)

	if n := count(records(), "Animation still: a VNC viewer connected") + count(records(), "Animation still: the frames slip") +
		count(records(), "Animation back"); n != 0 {
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
	s, a, _, _ := autoSelector(t, config.Default().Appearance, 12)
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
