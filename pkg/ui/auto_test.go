package ui

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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
