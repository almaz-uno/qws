package snapshot

import (
	"image"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/almaz-uno/qws/pkg/glx"
	"github.com/go-gl/gl/v4.6-core/gl"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog"
)

// TestLiveSchedule checks criterion K5 of specs/020-live-thumbnails: while
// shown, a change is averaged without the settle of a snapshot, a window at
// most once a live interval, of those due the window that has waited longest
// first, and no pass closer to the one before than the interval divided by
// the windows taking passes; a window not viewable, or whose size changed, is
// not passed; while live no snapshot is taken on change, and hidden the
// snapshots of 008 are (TestSchedule)
func TestLiveSchedule(t *testing.T) {
	t0 := time.Unix(1000, 0)
	const interval = 33 * time.Millisecond
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }

	// Never passed: at the change, no settle
	var a liveSchedule
	a.change(t0)
	if i, due, ok := nextPass([]liveSchedule{a}, 1, time.Time{}, interval); !ok || i != 0 || !due.Equal(t0) {
		t.Errorf("first change: %d, %v, %v; want 0 at the change", i, due.Sub(t0), ok)
	}

	// Passed, then changed soon after: an interval after the pass
	a.passed(t0)
	a.change(t0.Add(ms(5)))
	if _, due, _ := nextPass([]liveSchedule{a}, 1, t0, interval); !due.Equal(t0.Add(interval)) {
		t.Errorf("soon after a pass: due %v, want %v", due.Sub(t0), interval)
	}

	// Changed long after its pass: at the change
	late := t0.Add(10 * interval)
	b := liveSchedule{last: t0}
	b.change(late)
	if _, due, _ := nextPass([]liveSchedule{b}, 1, t0, interval); !due.Equal(late) {
		t.Errorf("long after a pass: due %v, want %v", due.Sub(t0), late.Sub(t0))
	}

	// Two due: the one that has waited longest first, whatever their order
	var first, second liveSchedule
	first.change(t0.Add(ms(1)))
	second.change(t0.Add(ms(2)))
	for _, order := range [][]liveSchedule{{first, second}, {second, first}} {
		i, _, _ := nextPass(order, 2, time.Time{}, interval)
		if !order[i].dirtyAt.Equal(first.dirtyAt) {
			t.Errorf("of two due, the one changed at %v first; want the one at 1 ms", order[i].dirtyAt.Sub(t0))
		}
	}

	// One that waited longer but is not yet due waits; one due goes
	held := liveSchedule{last: t0.Add(ms(30))}
	held.change(t0.Add(ms(31)))
	ready := liveSchedule{}
	ready.change(t0.Add(ms(40)))
	if i, due, _ := nextPass([]liveSchedule{held, ready}, 2, t0.Add(ms(30)), interval); i != 1 || !due.Equal(t0.Add(ms(30)+interval/2)) {
		t.Errorf("held and ready: %d at %v; want the ready one at %v", i, due.Sub(t0), ms(30)+interval/2)
	}

	// Spread: the interval divided by the windows taking passes, among them
	// one passed within the interval that does not wait now
	w := make([]liveSchedule, 4)
	for i := range w {
		w[i].change(t0)
	}
	if _, due, _ := nextPass(w, 4, t0.Add(ms(1)), interval); !due.Equal(t0.Add(ms(1) + interval/4)) {
		t.Errorf("four waiting: due %v after the last pass, want %v", due.Sub(t0.Add(ms(1))), interval/4)
	}
	if _, due, _ := nextPass(w[:1], 2, t0.Add(ms(1)), interval); !due.Equal(t0.Add(ms(1) + interval/2)) {
		t.Errorf("one waiting, one passed: due %v after the last pass, want %v", due.Sub(t0.Add(ms(1))), interval/2)
	}
	if _, _, ok := nextPass(nil, 0, t0, interval); ok {
		t.Error("a pass with none waiting")
	}

	// Which windows take passes
	viewable := window{mapped: true, bound: &glx.TexturePixmap{}}
	viewable.liveDue.change(t0)
	if !viewable.livePassable() {
		t.Error("a viewable window that changed takes no pass")
	}
	for name, w := range map[string]window{
		"unmapped":       {mapped: false, bound: viewable.bound, liveDue: viewable.liveDue},
		"frame unmapped": {mapped: true, frame: 2, bound: viewable.bound, liveDue: viewable.liveDue},
		"size changed":   {mapped: true, stale: true, bound: viewable.bound, liveDue: viewable.liveDue},
		"not bound":      {mapped: true, liveDue: viewable.liveDue},
		"unchanged":      {mapped: true, bound: viewable.bound},
		"pass under way": {mapped: true, bound: viewable.bound, liveDue: viewable.liveDue, live: &liveTextures{pending: 1}},
	} {
		if w.livePassable() {
			t.Errorf("%s: takes a pass", name)
		}
	}

	// While frames come one after another, a pass due waits for the end of
	// one, liveHold at most: the timer is set for then, and a tick before it
	// that is not the end of a frame makes no pass — a pass here would need
	// the GPU, which the test has not
	hs := &Snapshotter{windows: map[xproto.Window]*window{3: &viewable}, hold: liveHold,
		live: liveSession{overlay: 7, interval: interval}}
	now := t0.Add(ms(5))
	hs.lastEnd = now.Add(-ms(1))
	if d := hs.liveWait(now); d != liveHold-ms(5) {
		t.Errorf("frames flowing: the timer in %v, want %v: the hold after the pass was due", d, liveHold-ms(5))
	}
	hs.liveTick(now, false)
	if !viewable.liveDue.dirty {
		t.Error("a pass made while frames flow, before the end of one or the hold")
	}
	hs.lastEnd = now.Add(-liveHold)
	if d := hs.liveWait(now); d != 0 {
		t.Errorf("no frame for a hold: the timer in %v, want at once", d)
	}

	// The timer: while live, no snapshot of a window due one; hidden, it is
	// taken. Not viewable, the capture ends before any X request, and marks
	// the window captured.
	s := &Snapshotter{interval: time.Second, windows: map[xproto.Window]*window{}}
	due := &window{id: 1}
	due.schedule.change(t0)
	s.windows[1] = due
	s.live = liveSession{overlay: 7, interval: interval}
	s.tick(t0.Add(time.Hour))
	if !due.schedule.dirty {
		t.Error("a snapshot on change while live")
	}
	s.live = liveSession{}
	s.tick(t0.Add(time.Hour))
	if due.schedule.dirty {
		t.Error("no snapshot on change once hidden")
	}
}

// glThread runs functions on a goroutine locked to its thread, where the
// offscreen context of a snapshotter is current, as in its loop
type glThread struct {
	do chan func()
}

func (g *glThread) run(fn func()) {
	done := make(chan struct{})
	g.do <- func() { fn(); close(done) }
	<-done
}

// newLiveSnapshotter is a snapshotter with its GL objects, without its X
// connection or loop, its context current on a thread of its own; it skips
// the test without offscreen GLX
func newLiveSnapshotter(t testing.TB, conn *xgb.Conn) (*Snapshotter, *glThread) {
	s := &Snapshotter{
		conn:     conn,
		thumbs:   map[xproto.Window]image.Image{},
		thumbGen: map[xproto.Window]uint64{},
		pics:     map[xproto.Window]Picture{},
		windows:  map[xproto.Window]*window{},
		frameEnd: make(chan struct{}, 1),
		hold:     liveHold,
	}
	g := &glThread{do: make(chan func())}
	ready := make(chan error)
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		off, err := glx.NewOffscreen()
		if err != nil {
			ready <- err
			return
		}
		defer off.Destroy()
		gp, err := newGPU()
		if err != nil {
			ready <- err
			return
		}
		defer gp.close()
		s.off, s.gpu, s.share = off, gp, off.Share()
		ready <- nil
		for fn := range g.do {
			fn()
		}
	}()
	if err := <-ready; err != nil {
		t.Skipf("no offscreen GLX: %v", err)
	}
	t.Cleanup(func() {
		g.run(func() {
			for id := range s.windows {
				s.forget(id)
			}
			s.emptyTrash()
		})
		// The context is destroyed and its display closed before the test
		// goes on: GLX and Xlib on two threads at once creating and
		// destroying contexts corrupted the heap of a benchmark run twice
		close(g.do)
		<-exited
	})
	return s, g
}

// liveWindow is a window of the snapshotter whose pixmap — of depth 24, as
// the test's pixmaps — is bound as its texture, viewable
func liveWindow(t testing.TB, s *Snapshotter, g *glThread, id xproto.Window, pixmap xproto.Pixmap, size image.Point) *window {
	w := &window{id: id, mapped: true, width: size.X, height: size.Y, depth: 24, pixmap: pixmap}
	var err error
	g.run(func() {
		w.texture = newWindowTexture()
		w.bound, err = s.off.BindPixmap(uint32(pixmap), 24)
	})
	if err != nil {
		t.Fatal(err)
	}
	s.windows[id] = w
	return w
}

// pass makes a live pass of the window on the snapshotter's thread, as its
// loop would, and waits for it to be published; false when it is not made
func pass(t testing.TB, s *Snapshotter, g *glThread, w *window) bool {
	var made bool
	g.run(func() {
		w.liveDue.change(time.Now())
		made = s.livePass(w, time.Now())
	})
	if !made {
		return false
	}
	deadline := time.Now().Add(time.Second)
	for {
		var pending bool
		g.run(func() {
			s.publishDone()
			pending = w.live.pending != 0
		})
		if !pending {
			return true
		}
		if time.Now().After(deadline) {
			t.Fatal("a pass not done in a second")
		}
		time.Sleep(200 * time.Microsecond)
	}
}

// TestSharedLive checks criterion K4 of specs/020-live-thumbnails: a context
// created sharing the snapshotter's, on a display of its own and a thread of
// its own, as the presenter's is, reads a window's live picture — the area
// average of its pixmap, within 1 per channel — and, after the pixmap is drawn
// anew and averaged again, the new picture. A pass that would write the
// texture a frame being drawn took is not made until the frame ends; a live
// picture older than the window's snapshot is not given. Needs an X display
// with GLX_EXT_texture_from_pixmap and a driver that shares objects between
// two displays.
func TestSharedLive(t *testing.T) {
	conn, err := xgb.NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	s, g := newLiveSnapshotter(t, conn)

	// The presenter's side, on this thread
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ctx, err := glx.NewContext(s.Share())
	if err != nil {
		t.Skipf("no GLX context: %v", err)
	}
	defer ctx.Destroy()
	if err := ctx.ShareError(); err != nil {
		t.Skipf("no context sharing the snapshotter's: %v", err)
	}
	screen := xproto.Setup(conn).DefaultScreen(conn)
	if err := ctx.MakeCurrent(uint32(offscreenWindow(t, conn, screen, ctx.VisualID()))); err != nil {
		t.Fatal(err)
	}
	gl.PixelStorei(gl.PACK_ALIGNMENT, 1)

	size := image.Pt(1500, 900)
	pixmap := newPixmap(t, conn, screen.Root, 24, size)
	defer xproto.FreePixmap(conn, pixmap)
	images := make([]*image.RGBA, 3)
	for i := range images {
		images[i] = testImage(size.X, size.Y, int64(i+1))
	}
	const id = 1
	w := liveWindow(t, s, g, id, pixmap, size)

	// read takes the live picture for a frame, reads it in this context and
	// checks it is the average of img; the frame stays open
	read := func(what string, img *image.RGBA) {
		t.Helper()
		pics := s.BeginFrame([]xproto.Window{id})
		p, ok := pics[id]
		if !ok {
			t.Fatalf("%s: no live picture", what)
		}
		got := image.NewRGBA(image.Rect(0, 0, p.Width, p.Height))
		gl.BindTexture(gl.TEXTURE_2D, p.Texture)
		gl.GetTexImage(gl.TEXTURE_2D, 0, gl.RGBA, gl.UNSIGNED_BYTE, unsafe.Pointer(&got.Pix[0]))
		checkThumbnail(t, got, img, what, 24, size)
	}
	end := func() {
		f := gl.FenceSync(gl.SYNC_GPU_COMMANDS_COMPLETE, 0)
		gl.Flush()
		if old := s.EndFrame(f); old != 0 {
			gl.DeleteSync(old)
		}
	}

	fillPixmap(t, conn, pixmap, 24, images[0])
	if !pass(t, s, g, w) {
		t.Fatal("the first pass not made")
	}
	read("first", images[0])

	// The frame stays open: the second pass writes the other texture
	fillPixmap(t, conn, pixmap, 24, images[1])
	if !pass(t, s, g, w) {
		t.Fatal("the second pass not made while a frame draws the first picture")
	}
	// The third would write the texture the open frame took
	fillPixmap(t, conn, pixmap, 24, images[2])
	if pass(t, s, g, w) {
		t.Error("a pass made into the texture of a frame being drawn")
	}
	end()

	read("drawn anew", images[1])
	end()
	if !pass(t, s, g, w) {
		t.Fatal("the third pass not made once the frame ended")
	}
	read("drawn anew twice", images[2])
	end()

	// A snapshot taken since: the live picture is not newer than it
	g.run(func() { s.store(w, images[2]) })
	if pics := s.BeginFrame([]xproto.Window{id}); len(pics) != 0 {
		t.Error("a live picture older than the snapshot given")
	}
	end()
}

// offscreenWindow is a window of the visual, never mapped, for a context to
// be made current on
func offscreenWindow(t testing.TB, conn *xgb.Conn, screen *xproto.ScreenInfo, visual uint32) xproto.Window {
	cmap, err := xproto.NewColormapId(conn)
	if err != nil {
		t.Fatal(err)
	}
	xproto.CreateColormap(conn, xproto.ColormapAllocNone, cmap, screen.Root, xproto.Visualid(visual))
	win, err := xproto.NewWindowId(conn)
	if err != nil {
		t.Fatal(err)
	}
	if err := xproto.CreateWindowChecked(conn, 32, win, screen.Root, -100, -100, 16, 16, 0,
		xproto.WindowClassInputOutput, xproto.Visualid(visual),
		xproto.CwBorderPixel|xproto.CwColormap, []uint32{0, uint32(cmap)}).Check(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		xproto.DestroyWindow(conn, win)
		xproto.FreeColormap(conn, cmap)
	})
	return win
}

// BenchmarkLivePass is the cost of a live pass of specs/020-live-thumbnails
// of a window of the size of those of E1, 2556×1357, one 30 times a second:
// the binding again and the area average into 512×271 with its fence, on the
// snapshotter's thread (pass-cpu), on the GPU by a timer query (pass-gpu),
// and from the start of the pass to its publication (done), at p50 and p95.
// b.N is the number of passes:
//
//	go test -run '^$' -bench LivePass -benchtime 300x ./pkg/snapshot
func BenchmarkLivePass(b *testing.B) {
	conn, err := xgb.NewConn()
	if err != nil {
		b.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	// The record of each pass is not part of its cost
	level := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	defer zerolog.SetGlobalLevel(level)
	s, g := newLiveSnapshotter(b, conn)
	screen := xproto.Setup(conn).DefaultScreen(conn)
	size := image.Pt(2556, 1357)
	pixmap := newPixmap(b, conn, screen.Root, 24, size)
	defer xproto.FreePixmap(conn, pixmap)
	fillPixmap(b, conn, pixmap, 24, windowImage(size.X, size.Y, 1))
	w := liveWindow(b, s, g, 1, pixmap, size)
	var query uint32
	g.run(func() { gl.GenQueries(1, &query) })
	defer g.run(func() { gl.DeleteQueries(1, &query) })

	var cpu, gpu, done []float64
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	next := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		time.Sleep(time.Until(next))
		next = next.Add(time.Second / 30)
		var ns uint64
		made := false
		g.run(func() {
			w.liveDue.change(time.Now())
			gl.BeginQuery(gl.TIME_ELAPSED, query)
			made = s.livePass(w, time.Now())
			gl.EndQuery(gl.TIME_ELAPSED)
		})
		if !made {
			b.Fatal("no pass")
		}
		start := w.live.start
		for pending := true; pending; {
			g.run(func() {
				s.publishDone()
				pending = w.live.pending != 0
			})
		}
		done = append(done, ms(time.Since(start)))
		cpu = append(cpu, ms(w.live.cpu))
		g.run(func() { gl.GetQueryObjectui64v(query, gl.QUERY_RESULT, &ns) })
		gpu = append(gpu, float64(ns)/1e6)
	}
	b.StopTimer()
	for _, m := range []struct {
		name string
		v    []float64
	}{{"pass-cpu", cpu}, {"pass-gpu", gpu}, {"done", done}} {
		sort.Float64s(m.v)
		b.ReportMetric(m.v[(len(m.v)*50+99)/100-1], m.name+"-p50-ms")
		b.ReportMetric(m.v[(len(m.v)*95+99)/100-1], m.name+"-p95-ms")
	}
}

// BenchmarkLiveBeside is the off-screen measurement of the live thumbnails of
// specs/020-live-thumbnails beside paced frames, with the code of qws: the
// probe of the research, "Beside the frames", done again. A presenter of
// pkg/carousel presents to a window off the screen, of 2520×1400, a frame at
// rest of the grid of 36 every 1/144 s — slept to 3 ms before, then spun, as
// the selector waits — and glFinish after it; N pixmaps of 2556×1357 change R
// times a second, a band painted over and strokes, as a terminal printing. In
// blocks of frames in turn: the changes alone ("clients"), and the
// snapshotter's live passes of them — its schedule and passes, its loop fed
// the changes as DAMAGE would — with each frame drawing the pictures
// published ("live"). Per mode, at p95, the time a frame takes from its start
// to the end of glFinish (presented) and on the GPU by a timer query, and the
// share of frames presented in more than 2 ms. b.N is the number of frames;
// N and R are LIVE_N and LIVE_R, 4 and 30 by default:
//
//	go test -run '^$' -bench LiveBeside -benchtime 12000x ./pkg/snapshot
func BenchmarkLiveBeside(b *testing.B) {
	const (
		width, height = 2520, 1400
		block         = 400
		warmUp        = 20
		period        = time.Second / 144
		spinAhead     = 3 * time.Millisecond
	)
	n, rate := envInt("LIVE_N", 4), envInt("LIVE_R", 30)
	conn, err := xgb.NewConn()
	if err != nil {
		b.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	level := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	defer zerolog.SetGlobalLevel(level)
	s, g := newLiveSnapshotter(b, conn)
	s.live = liveSession{interval: 33 * time.Millisecond}
	if v := os.Getenv("LIVE_HOLD"); v != "" {
		s.hold, _ = time.ParseDuration(v)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	_, presenter, err := carousel.NewBackend("glx", s.Share())
	if err != nil {
		b.Fatal(err)
	}
	defer presenter.Close()
	lp, ok := presenter.(carousel.LivePresenter)
	if !ok || !lp.Live() {
		b.Skip("no GLX presenter sharing the snapshotter's context")
	}
	screen := xproto.Setup(conn).DefaultScreen(conn)
	win, err := carousel.NewWindowAt(conn, screen.Root, -width-100, 0, width, height, presenter.VisualID())
	if err != nil {
		b.Fatal(err)
	}
	defer win.Close()
	if err := win.Show(); err != nil {
		b.Fatal(err)
	}
	if err := presenter.Bind(win); err != nil {
		b.Fatal(err)
	}

	// The windows, their tiles in a grid of 36
	size := image.Pt(2556, 1357)
	tw, th := thumbSize(size.X, size.Y)
	data := make([]carousel.WindowData, 36)
	for i := range data {
		data[i] = carousel.WindowData{Thumbnail: image.NewRGBA(image.Rect(0, 0, tw, th)), Title: "Terminal"}
	}
	cfg := carousel.Config{
		Width: width, Height: height, ThumbWidth: 512, ThumbHeight: 512, Spacing: 600,
		PerspectiveFactor: 0.6, ShadowOffset: 10, FontSize: 20, LayoutMode: "grid", GridSpacing: 20,
		WindowBackgroundEnabled: true, WindowBackgroundOpacity: 0.85, WindowBackgroundRadius: 20,
		BackgroundColor: "#1a1a2e", InactiveFrame: "#404050", TextColor: "#ffffff", ShadowColor: "rgba(0, 0, 0, 0.8)",
	}
	tiles := carousel.GridLive(data, cfg)
	if err := presenter.Present(carousel.DrawGridLayout(data, -1, -1, cfg)); err != nil {
		b.Fatal(err)
	}
	ids := make([]xproto.Window, n)
	pixmaps := make([]xproto.Pixmap, n)
	for i := range ids {
		ids[i] = xproto.Window(100 + i)
		pixmaps[i] = newPixmap(b, conn, screen.Root, 24, size)
		defer xproto.FreePixmap(conn, pixmaps[i])
		fillPixmap(b, conn, pixmaps[i], 24, windowImage(size.X, size.Y, int64(i)))
		liveWindow(b, s, g, ids[i], pixmaps[i], size)
	}

	// The clients: each pixmap drawn R times a second, through a connection
	// of their own; the changes go to the snapshotter's loop while it is live
	changes := make(chan xproto.Window, 1024)
	stop := make(chan struct{})
	var passing atomic.Bool
	clients, err := xgb.NewConn()
	if err != nil {
		b.Fatal(err)
	}
	defer clients.Close()
	go func() {
		gc, _ := xproto.NewGcontextId(clients)
		xproto.CreateGC(clients, gc, xproto.Drawable(pixmaps[0]), 0, nil)
		r := rand.New(rand.NewSource(1))
		tick := time.NewTicker(time.Second / time.Duration(rate))
		defer tick.Stop()
		for k := 0; ; k++ {
			select {
			case <-stop:
				return
			case <-tick.C:
			}
			for i, p := range pixmaps {
				y := int16((k * 20) % size.Y)
				xproto.ChangeGC(clients, gc, xproto.GcForeground, []uint32{uint32(0xf0f0e8 - k%2*0x101010)})
				xproto.PolyFillRectangle(clients, xproto.Drawable(p), gc, []xproto.Rectangle{{X: 0, Y: y, Width: uint16(size.X), Height: 20}})
				xproto.ChangeGC(clients, gc, xproto.GcForeground, []uint32{uint32(r.Intn(0x404040))})
				strokes := make([]xproto.Rectangle, 150)
				for j := range strokes {
					strokes[j] = xproto.Rectangle{X: int16(r.Intn(size.X)), Y: y + int16(r.Intn(18)), Width: 2, Height: 6}
				}
				xproto.PolyFillRectangle(clients, xproto.Drawable(p), gc, strokes)
				if passing.Load() {
					select {
					case changes <- ids[i]:
					default:
					}
				}
			}
			clients.Sync()
		}
	}()
	loopDone := make(chan struct{})
	go g.run(func() {
		defer close(loopDone)
		timer := time.NewTimer(time.Hour)
		defer timer.Stop()
		for {
			timer.Reset(s.liveWait(time.Now()))
			select {
			case id := <-changes:
				s.windows[id].liveDue.change(time.Now())
			case <-s.frameEnd:
				s.liveTick(time.Now(), true)
			case <-timer.C:
				s.liveTick(time.Now(), false)
			case <-stop:
				return
			}
		}
	})

	var queries [2]uint32
	gl.GenQueries(2, &queries[0])
	defer gl.DeleteQueries(2, &queries[0])
	type sample struct{ presented, gpu time.Duration }
	modes := []string{"clients", "live"}
	samples := map[string][]sample{}
	due := time.Now()
	b.ResetTimer()
	for f := 0; f < b.N; f++ {
		mode := modes[f/block%len(modes)]
		passing.Store(mode == "live")
		for d := time.Until(due) - spinAhead; d > 0; d = time.Until(due) - spinAhead {
			time.Sleep(d)
		}
		for time.Now().Before(due) {
		}
		start := time.Now()
		gl.QueryCounter(queries[0], gl.TIMESTAMP)
		pics := s.BeginFrame(ids)
		var items []carousel.LiveItem
		for i, id := range ids {
			if p, ok := pics[id]; ok && !tiles[i].Rect.Empty() {
				it := tiles[i]
				it.Texture = p.Texture
				items = append(items, it)
			}
		}
		lp.SetLiveItems(items)
		if _, err := presenter.Refresh(); err != nil {
			b.Fatal(err)
		}
		gl.QueryCounter(queries[1], gl.TIMESTAMP)
		gl.Finish()
		presented := time.Since(start)
		lp.ReleaseFence(s.EndFrame(lp.TakeLiveFence()))
		var t0, t1 uint64
		gl.GetQueryObjectui64v(queries[0], gl.QUERY_RESULT, &t0)
		gl.GetQueryObjectui64v(queries[1], gl.QUERY_RESULT, &t1)
		if f%block >= warmUp {
			samples[mode] = append(samples[mode], sample{presented, time.Duration(t1 - t0)})
		}
		if time.Since(due) > period {
			due = start
		}
		due = due.Add(period)
	}
	b.StopTimer()
	close(stop)
	<-loopDone

	for _, mode := range modes {
		v := samples[mode]
		if len(v) == 0 {
			continue
		}
		presented, gpu := make([]float64, len(v)), make([]float64, len(v))
		over := 0
		for i, x := range v {
			presented[i], gpu[i] = float64(x.presented)/1e6, float64(x.gpu)/1e6
			if x.presented > 2*time.Millisecond {
				over++
			}
		}
		sort.Float64s(presented)
		sort.Float64s(gpu)
		p := func(v []float64, q int) float64 { return v[(len(v)*q+99)/100-1] }
		b.ReportMetric(p(presented, 95), mode+"-presented-p95-ms")
		b.ReportMetric(p(presented, 99), mode+"-presented-p99-ms")
		b.ReportMetric(p(gpu, 95), mode+"-gpu-p95-ms")
		b.ReportMetric(100*float64(over)/float64(len(v)), mode+"-over-2ms-%")
	}
	var passes int
	g.run(func() {
		for _, w := range s.windows {
			if w.live != nil {
				passes += int(w.pictures)
			}
		}
	})
	b.Logf("N %d, R %d: %d passes published in the live blocks", n, rate, passes)
}

// envInt is the integer of the environment variable, or def
func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}
