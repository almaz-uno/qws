package snapshot

import (
	"errors"
	"image"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/go-gl/gl/v4.6-core/gl"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog"
)

// framesBeside presents frames on a presenter of pkg/carousel sharing the
// snapshotter's context, on a thread of its own, as the switcher presents
// them: on a window of 2520×1400 off the screen, the last frame again with
// glFinish after it, which the timings of -v add
type framesBeside struct {
	reqs chan framesReq
	quit chan struct{}
	done chan struct{}
}

// framesReq asks for frames one after another, a millisecond apart, until
// until; each frame's time from its start to the end of glFinish comes back
type framesReq struct {
	until time.Time
	times chan []time.Duration
}

// newFramesBeside starts the presenter's thread; it skips the test without a
// GLX presenter sharing the snapshotter's context
func newFramesBeside(tb testing.TB, s *Snapshotter, conn *xgb.Conn) *framesBeside {
	f := &framesBeside{reqs: make(chan framesReq), quit: make(chan struct{}), done: make(chan struct{})}
	ready := make(chan error)
	go func() {
		defer close(f.done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		_, presenter, err := carousel.NewBackend("glx", s.Share())
		if err != nil {
			ready <- err
			return
		}
		defer presenter.Close()
		if lp, ok := presenter.(carousel.LivePresenter); !ok || !lp.Live() {
			ready <- errNoSharing
			return
		}
		screen := xproto.Setup(conn).DefaultScreen(conn)
		win, err := carousel.NewWindowAt(conn, screen.Root, -2620, 0, 2520, 1400, presenter.VisualID())
		if err != nil {
			ready <- err
			return
		}
		defer win.Close()
		if err = win.Show(); err == nil {
			err = presenter.Bind(win)
		}
		if err == nil {
			err = presenter.Present(windowImage(2520, 1400, 5))
		}
		if err != nil {
			ready <- err
			return
		}
		gl.Finish()
		ready <- nil
		for {
			select {
			case r := <-f.reqs:
				var times []time.Duration
				for time.Now().Before(r.until) {
					start := time.Now()
					if _, err := presenter.Refresh(); err != nil {
						tb.Error(err)
					}
					gl.Finish()
					times = append(times, time.Since(start))
					time.Sleep(time.Millisecond)
				}
				r.times <- times
			case <-f.quit:
				return
			}
		}
	}()
	if err := <-ready; err != nil {
		<-f.done
		tb.Skipf("no GLX presenter sharing the snapshotter's context: %v", err)
	}
	// The presenter's context is destroyed before the snapshotter's
	tb.Cleanup(func() { close(f.quit); <-f.done })
	return f
}

// errNoSharing: the presenter's context does not share the snapshotter's
var errNoSharing = errors.New("the presenter's context does not share the snapshotter's")

// start asks for frames until until; the times come on the channel
func (f *framesBeside) start(until time.Time) chan []time.Duration {
	r := framesReq{until, make(chan []time.Duration, 1)}
	f.reqs <- r
	return r.times
}

// longest is the longest of the times, in ms
func longest(times []time.Duration) float64 {
	var m time.Duration
	for _, t := range times {
		m = max(m, t)
	}
	return float64(m) / float64(time.Millisecond)
}

// BenchmarkFirstPassBeside measures, off the screen, the first live pass of
// an activation of a window taken from its frame (specs/022, 023) and the
// frames of the presenter beside it (specs/033-texture-warmup): a client
// window of 2556×1392, the size of the window of the trial of 028 on ws2,
// of depth 24 in a frame, as on the desktop; each of b.N rounds forgets
// what its passes made — the pixmap it is scaled into, its chain, its live
// textures, as for a window new to the process — then captures it as a
// snapshot on change, while no frame is presented, and makes its first live
// pass on the snapshotter's thread while the presenter, sharing the
// snapshotter's context on a thread of its own, presents frames one after
// another, a millisecond apart, from 5 ms before the pass to 60 ms after.
// It reports, at p50 and p95 over the rounds, in ms: the snapshot
// (snapshot_*), the pass on the snapshotter's thread (pass_*), the longest
// frame beside it (beside_*), and the longest frame of as long a stretch
// just before, with no pass (alone_*):
//
//	go test -run '^$' -bench FirstPassBeside -benchtime 20x ./pkg/snapshot
func BenchmarkFirstPassBeside(b *testing.B) {
	conn, err := x11.NewConn()
	if err != nil {
		b.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	level := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	defer zerolog.SetGlobalLevel(level)
	s := frameSnapshotter(b, conn)
	frames := newFramesBeside(b, s, conn)
	w, _ := frameWindow(b, s, conn, image.Pt(-4000, -4000), image.Pt(2556, 1392), 0)
	defer s.forget(w.id)

	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	var snapshot, pass, beside, alone []float64
	b.ResetTimer()
	for range b.N {
		// New to the process
		s.dropLive(w)
		s.dropChain(w)
		s.dropScaled(w)
		w.liveDue = liveSchedule{}
		w.schedule.change(time.Now())
		start := time.Now()
		s.capture(w, causeChange)
		snapshot = append(snapshot, ms(time.Since(start)))
		time.Sleep(50 * time.Millisecond)

		alone = append(alone, longest(<-frames.start(time.Now().Add(65*time.Millisecond))))
		times := frames.start(time.Now().Add(65 * time.Millisecond))
		time.Sleep(5 * time.Millisecond)
		if _, ok := livePicture(b, s, w); !ok {
			b.Fatal("no pass")
		}
		pass = append(pass, ms(w.live.cpu))
		beside = append(beside, longest(<-times))
		time.Sleep(50 * time.Millisecond)
	}
	b.StopTimer()
	for _, m := range []struct {
		name string
		v    []float64
	}{{"snapshot", snapshot}, {"pass", pass}, {"beside", beside}, {"alone", alone}} {
		sort.Float64s(m.v)
		b.ReportMetric(m.v[(len(m.v)*50+99)/100-1], m.name+"_p50_ms")
		b.ReportMetric(m.v[(len(m.v)*95+99)/100-1], m.name+"_p95_ms")
	}
	b.Logf("beside, sorted: %.2f", beside)
	b.Logf("pass, sorted: %.2f", pass)
}
