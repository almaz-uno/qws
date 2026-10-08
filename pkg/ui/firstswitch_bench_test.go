package ui

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// switchScript is an activation of BenchmarkE1FirstSwitch: a key, 0 for the
// activation itself, and the time from it to the next — q four times: the
// grid, the carousel, the grid, the carousel
var switchScript = []struct {
	keysym uint32
	wait   time.Duration
}{
	{0, 400 * time.Millisecond},
	{0x0071, 600 * time.Millisecond},
	{0x0071, 600 * time.Millisecond},
	{0x0071, 600 * time.Millisecond},
	{0x0071, 600 * time.Millisecond},
}

// frameRecords collects the debug records of the selector as JSON objects
type frameRecords struct {
	mu      sync.Mutex
	records []map[string]any
}

func (r *frameRecords) Write(p []byte) (int, error) {
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(p), &m); err == nil {
		r.mu.Lock()
		r.records = append(r.records, m)
		r.mu.Unlock()
	}
	return len(p), nil
}

// take returns the records collected since the last take
func (r *frameRecords) take() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.records
	r.records = nil
	return out
}

// BenchmarkE1FirstSwitch measures, off the screen, the first switch of the
// layout of an activation of E1 apart from the later ones
// (specs/033-texture-warmup): the selector of BenchmarkE1Switch with the
// live thumbnails of the fake snapshotter of BenchmarkE1Memory, its window
// mapped and unmapped as Show and FadeOut do; b.N activations of
// switchScript — the layers dropped, the first frame through the fade-in,
// the keys on a fixed schedule with the loop of handleEventsSync run between
// them as it runs while no event comes, the fade-out, the overlay unmapped
// and 500 ms. Each switch finds the layers of the layout it shows held. It
// reports, in ms, from the record "Frame" of cause key after each
// "Switching layout" — the records of the author's trials, at debug level —
// the p50 and the p95 over the activations of present_ms and total_ms of
// their first switch, and over the later switches:
//
//	go test ./pkg/ui -run '^$' -bench E1FirstSwitch -benchtime 20x
func BenchmarkE1FirstSwitch(b *testing.B) {
	records := &frameRecords{}
	level, logger := zerolog.GlobalLevel(), log.Logger
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	log.Logger = zerolog.New(records).Level(zerolog.DebugLevel)
	defer func() { zerolog.SetGlobalLevel(level); log.Logger = logger }()

	conn, err := xgb.NewConn()
	if err != nil {
		b.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	_, presenter, err := carousel.NewBackend("glx", nil)
	if err != nil {
		b.Fatal(err)
	}
	defer presenter.Close()
	animator, ok := presenter.(carousel.Animator)
	lp, live := presenter.(carousel.LivePresenter)
	if !ok || !live {
		b.Skip("no GLX presenter")
	}
	s := e1Selector(b, 33)
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	w, err := carousel.NewWindowAt(conn, root, -s.config.Width-100, 0, s.config.Width, s.config.Height, presenter.VisualID())
	if err != nil {
		b.Fatal(err)
	}
	defer w.Close()
	if err := presenter.Bind(w); err != nil {
		b.Fatal(err)
	}
	s.window, s.presenter, s.animator = w, presenter, animator
	src := newMemorySource(s, 4)
	defer src.close()
	s.live = liveThumbnails{snap: src, presenter: lp, interval: 33 * time.Millisecond, atom: 1, drawn: map[xproto.Window]uint64{}}

	var firstP, firstT, laterP, laterT []float64
	records.take()
	for range b.N {
		switchActivation(b, s, src)
		recs := records.take()
		n := 0
		for i, r := range recs {
			if r["message"] != "Switching layout" {
				continue
			}
			if r["layers"] != true {
				b.Fatal("a switch without the layers of its layout")
			}
			for _, f := range recs[i+1:] {
				if f["message"] != "Frame" || f["cause"] != causeKey {
					continue
				}
				p, t := f["present_ms"].(float64), f["total_ms"].(float64)
				if n == 0 {
					firstP, firstT = append(firstP, p), append(firstT, t)
				} else {
					laterP, laterT = append(laterP, p), append(laterT, t)
				}
				break
			}
			n++
		}
		if n != len(switchScript)-1 {
			b.Fatalf("%d switches recorded, want %d", n, len(switchScript)-1)
		}
	}
	b.ReportMetric(percentile(firstP, 50), "first_present_p50_ms")
	b.ReportMetric(percentile(firstP, 95), "first_present_p95_ms")
	b.ReportMetric(percentile(firstT, 50), "first_total_p50_ms")
	b.ReportMetric(percentile(firstT, 95), "first_total_p95_ms")
	b.ReportMetric(percentile(laterP, 50), "later_present_p50_ms")
	b.ReportMetric(percentile(laterP, 95), "later_present_p95_ms")
	b.ReportMetric(percentile(laterT, 50), "later_total_p50_ms")
	b.ReportMetric(percentile(laterT, 95), "later_total_p95_ms")
}

// switchActivation is an activation of switchScript, as Show and FadeOut make
// one, then the overlay unmapped for 500 ms
func switchActivation(b *testing.B, s *Selector, src *memorySource) {
	s.dropLayers()
	s.selectedIndex = 1
	if err := s.window.Show(); err != nil {
		b.Fatal(err)
	}
	s.mapped = true
	s.markFrameCause(causeActivation)
	at := time.Now()
	for _, k := range switchScript {
		if k.keysym == 0 {
			s.fade.pending = s.anim.show.any()
			s.render(s.prepareThumbnails())
			if !s.fade.active {
				s.setLive(true)
			}
			s.prefetch()
		} else {
			s.markFrameCause(causeKey)
			if s.handleKeyPressSimple(xproto.KeyPressEvent{Detail: benchKeycode(b, k.keysym)}, s.prepareThumbnails()) {
				b.Fatalf("key 0x%X cancelled the switcher", k.keysym)
			}
		}
		at = at.Add(k.wait)
		memoryLoop(s, src, at)
	}
	s.chosenAt = time.Now()
	s.setLive(false)
	s.beginFade(true, time.Now(), s.chosenAt)
	for s.fade.active {
		s.frame()
	}
	s.cancelStep()
	s.hide()
	memoryLoop(s, src, time.Now().Add(memoryPause))
}
