package snapshot

import (
	"image"
	"runtime"
	"sort"
	"testing"
	"time"
	"unsafe"

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
	}
	g := &glThread{do: make(chan func())}
	ready := make(chan error)
	go func() {
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
		close(g.do)
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
