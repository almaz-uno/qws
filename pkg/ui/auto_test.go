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

// newCount is the count of autoAnimation on, its clock standing still: no
// probe
func newCount() *autoAnimation {
	start := time.Unix(1000, 0)
	return &autoAnimation{on: true, clock: func() time.Time { return start }}
}

// fillWindow fills the window of the count with frames on time at the
// period, until they span it: a window full at the refresh rate
func fillWindow(a *autoAnimation, period time.Duration) {
	for a.span < slipSpan {
		a.frame(period, period)
	}
}

// share is the mean rate of the window of the count as a share of the
// refresh rate of period
func share(a *autoAnimation, period time.Duration) float64 {
	return float64(a.frames) * float64(period) / float64(a.span)
}

// replay is the count of a batch replayed into an autoAnimation, as the
// switcher counts it: begin at each activation, then the frames of the kinds
// counted.
type replay struct {
	frames     int     // counted until it went still, or all
	activation int     // where it went still, 0: never
	frame      int     // the frame counted in that activation that made it still
	lowest     float64 // the lowest share of the refresh rate of a full window; 0: none full
}

func replayBatch(a *autoAnimation, period time.Duration, runs []frameRun) replay {
	var r replay
	activation, n := 0, 0
	for _, run := range runs {
		if run.activation != activation {
			activation, n = run.activation, 0
			a.begin()
		}
		if !countsSlips(run.kind) {
			continue
		}
		for _, interval := range run.intervals {
			if a.reason != stillNot {
				return r
			}
			n++
			r.frames++
			still := a.frame(interval, period)
			if a.span >= slipSpan {
				if s := share(a, period); r.lowest == 0 || s < r.lowest {
					r.lowest = s
				}
			}
			if still {
				r.activation, r.frame = activation, n
			}
		}
	}
	return r
}

// TestSlipReplay checks K2 of specs/035-slip-by-frame-rate: the frames
// recorded replayed into the count never make it still — batches A and B
// of 031 at 165 Hz, A with a VNC viewer connected no more than B (D4 of
// 035); the frames of ws1 at 144 Hz, as recorded and after a window of
// frames on time; B scaled to 60 Hz —, their lowest windows as the research
// of 035 has them; B followed by a load of 30 ms is still within 0.2 s of
// the load
func TestSlipReplay(t *testing.T) {
	quietLog(t)
	// scaled is a batch with its intervals and its period scaled to the
	// refresh rate hz
	scaled := func(name string, hz float64) (time.Duration, []frameRun) {
		period, runs := readBatch(t, name)
		to := time.Duration(float64(time.Second) / hz)
		k := float64(to) / float64(period)
		var out []frameRun
		for _, run := range runs {
			r := frameRun{activation: run.activation, kind: run.kind}
			for _, d := range run.intervals {
				r.intervals = append(r.intervals, time.Duration(float64(d)*k))
			}
			out = append(out, r)
		}
		return to, out
	}
	for _, c := range []struct {
		name   string
		batch  func() (time.Duration, []frameRun)
		full   bool    // the window full of frames on time before the batch
		frames int     // counted, as research "The count replayed" of 031 has them for A and B
		lowest float64 // the lowest window, research "The rules replayed" of 035; 0: none full
	}{
		{"A", func() (time.Duration, []frameRun) { return readBatch(t, "vnc-a.txt") }, false, 1495, 0.987},
		{"B", func() (time.Duration, []frameRun) { return readBatch(t, "vnc-b.txt") }, false, 1499, 0.998},
		{"ws1", func() (time.Duration, []frameRun) { return readBatch(t, "fast-ws1.txt") }, false, 46, 0},
		{"ws1, the window full before", func() (time.Duration, []frameRun) { return readBatch(t, "fast-ws1.txt") }, true, 46, 0.970},
		{"B scaled to 60 Hz", func() (time.Duration, []frameRun) { return scaled("vnc-b.txt", 60) }, false, 1499, 0.993},
	} {
		period, runs := c.batch()
		a := newCount()
		if c.full {
			fillWindow(a, period)
		}
		got := replayBatch(a, period, runs)
		t.Logf("%s: %d frames counted; still in activation %d at its frame %d; the lowest window %.2f %% of the refresh rate",
			c.name, got.frames, got.activation, got.frame, 100*got.lowest)
		if got.activation != 0 || got.frames != c.frames || got.lowest < c.lowest-0.0005 || got.lowest > c.lowest+0.0005 {
			t.Errorf("%s: %+v; want never still, %d frames, the lowest window %.3f", c.name, got, c.frames, c.lowest)
		}
	}

	// B, then a load: the window full of B's frames, every frame of the load
	// 30 ms
	period, runs := readBatch(t, "vnc-b.txt")
	a := newCount()
	replayBatch(a, period, runs)
	a.begin()
	var load time.Duration
	for i := 0; i < 100 && a.reason == stillNot; i++ {
		load += 30 * time.Millisecond
		a.frame(30*time.Millisecond, period)
	}
	t.Logf("B, then a load of 30 ms: still after %v of the load", load)
	if a.reason != stillFrames || load > 200*time.Millisecond {
		t.Errorf("B, then a load of 30 ms: still for %q after %v of the load, want the frames within 0.2 s", a.reason, load)
	}
}

// TestSlipCount checks the edges of K1 of specs/035-slip-by-frame-rate, at
// 60, 144 and 165 Hz: a window of 0.5 s, its mean rate below 75 % of the
// refresh rate, makes the activation still, at 75 % not; a pause among
// frames on time on either side of the edge, one of 100 ms not still, one of
// 150 ms still; a load of 30 ms still at the frame that fills an empty
// window, a window not yet full never, nor one of 512 frames in less than
// 0.5 s. And: the window is carried across activations; the probe starts it
// anew; the fades are not counted (D5 of 031, O1); while still, or off,
// nothing is counted.
func TestSlipCount(t *testing.T) {
	quietLog(t)
	repeat := func(d time.Duration, n int) []time.Duration {
		s := make([]time.Duration, n)
		for i := range s {
			s[i] = d
		}
		return s
	}

	for _, hz := range []float64{60, 144, 165} {
		period := time.Duration(float64(time.Second) / hz)
		// frames counts the intervals; it reports the frame that made it
		// still, from 1, or 0
		frames := func(a *autoAnimation, intervals ...time.Duration) int {
			for i, d := range intervals {
				if a.frame(d, period) {
					return i + 1
				}
			}
			return 0
		}
		onTime := repeat(period, int(time.Second/period))

		// On time; at 75 % — every interval 4/3 of the period, to the
		// nanosecond below —: never; a nanosecond longer: still at the frame
		// that fills the window
		a := newCount()
		if n := frames(a, onTime...); n != 0 || a.span < slipSpan {
			t.Errorf("%v Hz, frames on time: still at %d, the window %v", hz, n, a.span)
		}
		d := period * 4 / 3
		a = newCount()
		if n := frames(a, repeat(d, 1000)...); n != 0 {
			t.Errorf("%v Hz, frames at 75 %% of the refresh rate, %v: still at %d", hz, d, n)
		}
		a = newCount()
		if n, want := frames(a, repeat(d+1, 1000)...), int((slipSpan+d)/(d+1)); n != want {
			t.Errorf("%v Hz, frames below 75 %% of the refresh rate, %v: still at %d, want %d", hz, d+1, n, want)
		}

		// A pause among frames on time, the window full before it
		for _, c := range []struct {
			pause time.Duration
			still bool
		}{{100 * time.Millisecond, false}, {150 * time.Millisecond, true}} {
			a = newCount()
			fillWindow(a, period)
			n := frames(a, append([]time.Duration{c.pause}, onTime...)...)
			if (n == 1) != c.still || (!c.still && n != 0) {
				t.Errorf("%v Hz, a pause of %v: still at %d, want still %v at the pause", hz, c.pause, n, c.still)
			}
		}

		// The edge: the window exactly 0.5 s, m frames on time and a pause;
		// still while its m + 1 frames are fewer than 75 % of the refresh
		// periods of 0.5 s
		m := 0
		for time.Duration(m+1)*period < slipSpan*3/4 {
			m++
		}
		for _, c := range []struct {
			m     int
			still bool
		}{{m, false}, {m - 1, true}} {
			pause := slipSpan - time.Duration(c.m)*period
			a = newCount()
			fillWindow(a, period)
			if n := frames(a, pause); (n == 1) != c.still || a.span != slipSpan || a.frames != c.m+1 {
				t.Errorf("%v Hz, a pause of %v after %d frames on time: still %v, the window %d frames in %v; want still %v, %d frames in %v",
					hz, pause, c.m, n == 1, a.frames, a.span, c.still, c.m+1, slipSpan)
			}
		}

		// A load from an empty window: never until the window is full, then
		// at once — 17 frames of 30 ms are 0.51 s
		a = newCount()
		if n := frames(a, repeat(30*time.Millisecond, 100)...); n != 17 || a.reason != stillFrames {
			t.Errorf("%v Hz, a load of 30 ms: still at %d for %q, want 17", hz, n, a.reason)
		}
		if n := frames(a, repeat(time.Second, 3)...); n != 0 {
			t.Errorf("%v Hz: frames counted while still", hz)
		}

		// 512 frames in less than 0.5 s: not full, never still
		a = newCount()
		if n := frames(a, repeat(100*time.Microsecond, 2000)...); n != 0 || a.frames != slipFrames || a.span >= slipSpan {
			t.Errorf("%v Hz, frames of 0.1 ms: still at %d, the window %d frames in %v", hz, n, a.frames, a.span)
		}
	}

	const period = 8 * time.Millisecond
	frames := func(a *autoAnimation, intervals ...time.Duration) int {
		for i, d := range intervals {
			if a.frame(d, period) {
				return i + 1
			}
		}
		return 0
	}

	// Carried across activations: half a window in one, the rest in the next
	a := newCount()
	frames(a, repeat(11*time.Millisecond, 23)...)
	if a.begin() {
		t.Fatal("half a window: the next activation still")
	}
	if n := frames(a, repeat(11*time.Millisecond, 23)...); n != 23 {
		t.Errorf("the rest of the window in the next activation: still at %d, want 23", n)
	}
	if !a.begin() {
		t.Error("the activation after the frames slipped animated")
	}

	// The probe: five minutes after, the window anew — half a window of slow
	// frames is not enough
	now := time.Unix(1000, 0)
	a = &autoAnimation{on: true, clock: func() time.Time { return now }}
	frames(a, repeat(11*time.Millisecond, 46)...)
	now = now.Add(probeAfter - time.Second)
	if !a.begin() {
		t.Error("before the probe: animated")
	}
	now = now.Add(time.Second)
	if a.begin() || a.frames != 0 || a.span != 0 {
		t.Errorf("the probe: still %v, the window %d frames in %v; want animated, the window empty", a.reason, a.frames, a.span)
	}
	if n := frames(a, repeat(11*time.Millisecond, 45)...); n != 0 {
		t.Errorf("the probe, 45 slow frames: still at %d", n)
	}
	if n := frames(a, 11*time.Millisecond); n != 1 {
		t.Error("the probe, the window full of slow frames: animated")
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
	if off.begin() || frames(off, repeat(time.Second, 10)...) != 0 || off.frames != 0 {
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

// TestStillFrames checks K4 of specs/031-animation-auto, D5–D7, and K3 of
// specs/035-slip-by-frame-rate: the frames of a step slipping — the window
// full, filled with frames on time before it, and the step's frames far
// slower than the refresh rate — make the activation still in the middle of
// the step: the step is at its target in the next frame, the next steps at
// once, the live passes stop; the next activation still, with no record;
// five minutes after, by the clock of the test, the probe: animated, its
// window started anew, and still again as its frames slip. A fade-out with a
// step moving ends at once.
func TestStillFrames(t *testing.T) {
	const right = 0xFF53
	records := logRecords(t)
	s, a, src, clock := autoSelector(t, config.Default().Appearance, 12)
	activate(s)
	if s.still || src.on != 1 {
		t.Fatalf("the first activation: still %v, the passes asked for %d times; want animated, asked for", s.still, src.on)
	}

	// trip fills the window with frames on time, then steps once with
	// every frame far slower than the refresh rate — a period of 1 ns —
	// until the count trips; it returns the frames presented
	trip := func() int {
		t.Helper()
		fillWindow(&s.auto, 7*time.Millisecond)
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
	// The first frame of the step has no interval: the first counted is
	// its second
	if !s.still || n != 2 {
		t.Fatalf("every frame slow: still %v after %d frames; want still after 2", s.still, n)
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
	if n := trip(); !s.still || n != 2 {
		t.Errorf("the probe, every frame slow: still %v after %d frames; want still after 2", s.still, n)
	}
	loopIdle(s, func() bool { return false })
	deactivate(s)

	all := records()
	if n := count(all, "Animation still: the frames slip"); n != 2 {
		t.Errorf("%d records of the frames, want 2", n)
	}
	for _, r := range all {
		if r["message"] != "Animation still: the frames slip" {
			continue
		}
		// The window of 0.5 s at some 144 fps, against a refresh rate of 1 GHz
		fps, hz, frames, window := r["fps"].(float64), r["refresh_hz"].(float64), r["frames"].(float64), r["window_ms"].(float64)
		if r["level"] != "info" || hz != 1e9 || fps < 100 || fps > 150 || frames < 70 || window < 500 || window > 520 {
			t.Errorf("the record of the frames %v, want at info level with the window, its rate and the refresh rate", r)
		}
	}
	if n := count(all, "Animation back"); n != 1 {
		t.Errorf("%d records of the animation back, want 1", n)
	}

	// A step moving in the fade-out, tripped: the fade-out ends at once
	f, _, _, _ := autoSelector(t, config.Default().Appearance, 12)
	activate(f)
	fillWindow(&f.auto, 7*time.Millisecond)
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
