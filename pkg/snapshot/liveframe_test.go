package snapshot

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/almaz-uno/qws/pkg/glx"
	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/go-gl/gl/v4.6-core/gl"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog"
)

// frameWindow is a client window of depth 24 at 2,22 in a frame off the
// screen, which the compositor redirects and the window is drawn into — of
// the frame's visual, or of the visual given, another of depth 24, as
// ilovlya on ws1 — a child of the window showing the picture, and the
// snapshotter's window for it, captured once from the frame by RENDER
func frameWindow(t testing.TB, s *Snapshotter, conn *xgb.Conn, at image.Point, size image.Point, visual xproto.Visualid) (*window, xproto.Window) {
	t.Helper()
	screen := xproto.Setup(conn).DefaultScreen(conn)
	frame := testWindow(t, conn, screen.Root, image.Rectangle{Min: at, Max: at.Add(size).Add(image.Pt(4, 24))}, 0, 0)
	client := testWindow(t, conn, frame, image.Rect(2, 22, 2+size.X, 22+size.Y), 24, visual)
	child := testWindow(t, conn, client, image.Rectangle{Max: size}, 0, 0)
	waitRedirected(t, conn, frame)
	w := &window{id: client, frame: frame, mapped: true, frameMapped: true, visual: visual, stale: true}
	if visual == 0 {
		w.visual = screen.RootVisual
	}
	s.windows[client], s.frames[frame] = w, client
	fillDrawable(t, conn, xproto.Drawable(child), 24, windowImage(size.X, size.Y, 99))
	s.capture(w, "test")
	if w.via != frame || w.bound != nil {
		t.Fatalf("via 0x%x, bound %v; want the pixmap of the frame 0x%x by RENDER", w.via, w.bound != nil, frame)
	}
	return w, child
}

// livePicture makes a live pass of the window on this thread, as the loop
// would, waits for it to be published, and reads its picture; false when no
// pass is made
func livePicture(t testing.TB, s *Snapshotter, w *window) (*image.RGBA, bool) {
	t.Helper()
	w.liveDue.change(time.Now())
	if !s.livePass(w, time.Now()) {
		return nil, false
	}
	for deadline := time.Now().Add(time.Second); w.live.pending != 0; {
		if time.Now().After(deadline) {
			t.Fatal("a pass not done in a second")
		}
		time.Sleep(200 * time.Microsecond)
		s.publishDone()
	}
	p := s.pics[w.id]
	img := image.NewRGBA(image.Rect(0, 0, p.Width, p.Height))
	gl.PixelStorei(gl.PACK_ALIGNMENT, 1)
	gl.BindTexture(gl.TEXTURE_2D, p.Texture)
	gl.GetTexImage(gl.TEXTURE_2D, 0, gl.RGBA, gl.UNSIGNED_BYTE, unsafe.Pointer(&img.Pix[0]))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255 // the alpha of a window is ignored
	}
	return img, true
}

// TestLiveFromFrame checks K14 of specs/020-live-thumbnails: a window taken
// from its frame's pixmap (specs/022-uncaptured-windows) is passed live by
// RENDER into a pixmap of qws's own bound on the GPU — the only pixmap bound,
// never the frame's — its picture within 1 of the snapshot of the same
// picture, which RENDER scales alike: the filter of RENDER, a mean of 0.7–1.3
// per channel from the area average for these pictures, 2 at most here;
// drawn anew, its picture is the new one, of the size of the thumbnail.
// Should a pixmap of qws's own not bind, no pass is made, the window keeps
// its snapshot, and none is tried again. The windows are off the screen.
func TestLiveFromFrame(t *testing.T) {
	conn, err := x11.NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	s := frameSnapshotter(t, conn)
	var bound []xproto.Pixmap
	bind := bindScaled
	bindScaled = func(off *glx.Offscreen, p xproto.Pixmap) (*glx.TexturePixmap, error) {
		bound = append(bound, p)
		return bind(off, p)
	}
	defer func() { bindScaled = bind }()

	size := image.Pt(1500, 900)
	w, child := frameWindow(t, s, conn, image.Pt(-3000, -3000), size, 0)
	defer s.forget(w.id)
	tw, th := thumbSize(size.X, size.Y)
	for k, seed := range []int64{1, 2, 3} {
		picture := windowImage(size.X, size.Y, seed)
		fillDrawable(t, conn, xproto.Drawable(child), 24, picture)
		got, ok := livePicture(t, s, w)
		if !ok {
			t.Fatalf("picture %d: no pass", k+1)
		}
		mean, worst := difference(got, areaAverage(picture, tw, th))
		s.capture(w, "test")
		img, _ := s.Thumbnail(w.id)
		snap, _ := img.(*image.RGBA)
		var sMean float64
		sWorst := -1
		if snap != nil {
			sMean, sWorst = difference(got, snap)
		}
		t.Logf("picture %d: from the area average mean %.3f, worst %d; from the snapshot mean %.3f, worst %d",
			k+1, mean, worst, sMean, sWorst)
		if mean > 2 {
			t.Errorf("picture %d: mean %.3f from the area average", k+1, mean)
		}
		if sWorst < 0 || sWorst > 1 {
			t.Errorf("picture %d: %d from its snapshot, want 1 at most", k+1, sWorst)
		}
		if w.stale {
			t.Fatalf("picture %d: the window stale after a pass", k+1)
		}
	}
	if w.bound != nil || len(bound) != 1 || bound[0] == w.pixmap || w.scaled == nil || bound[0] != w.scaled.pixmap {
		t.Errorf("bound %v of the pixmap 0x%x of the frame, the window's own %v; want one pixmap of qws's own",
			bound, w.pixmap, w.bound != nil)
	}
	if sp := w.scaled; sp.width != tw || sp.height != th {
		t.Errorf("scaled pixmap %d×%d, want %d×%d", sp.width, sp.height, tw, th)
	}

	// Of another visual of depth 24 than its frame's
	if v := otherVisual(xproto.Setup(conn).DefaultScreen(conn), 24); v != 0 {
		vw, vchild := frameWindow(t, s, conn, image.Pt(-1400, -3000), image.Pt(800, 500), v)
		picture := windowImage(800, 500, 5)
		fillDrawable(t, conn, xproto.Drawable(vchild), 24, picture)
		got, ok := livePicture(t, s, vw)
		s.capture(vw, "test")
		img, _ := s.Thumbnail(vw.id)
		snap, _ := img.(*image.RGBA)
		if !ok || snap == nil {
			t.Fatalf("of visual 0x%x: pass %v, snapshot %v", v, ok, snap != nil)
		}
		mean, worst := difference(got, snap)
		t.Logf("of visual 0x%x in a frame of 0x%x: via 0x%x, from the snapshot mean %.3f, worst %d",
			v, xproto.Setup(conn).DefaultScreen(conn).RootVisual, vw.via, mean, worst)
		if vw.via == 0 || worst > 1 {
			t.Errorf("of visual 0x%x: via 0x%x, %d from its snapshot; want its frame and 1 at most", v, vw.via, worst)
		}
		s.forget(vw.id)
	}

	// Should the pixmap of qws not bind, the window keeps its snapshot
	other, otherChild := frameWindow(t, s, conn, image.Pt(-3000, -1800), image.Pt(800, 500), 0)
	defer s.forget(other.id)
	bindScaled = func(*glx.Offscreen, xproto.Pixmap) (*glx.TexturePixmap, error) {
		return nil, errors.New("no bind in the test")
	}
	fillDrawable(t, conn, xproto.Drawable(otherChild), 24, windowImage(800, 500, 7))
	if _, ok := livePicture(t, s, other); ok {
		t.Error("a pass made with no pixmap bound")
	}
	if _, ok := s.pics[other.id]; ok || other.scaled != nil || !s.noScaled {
		t.Errorf("picture %v, scaled pixmap %v, off %v; want none, none and live passes from frames off",
			ok, other.scaled != nil, s.noScaled)
	}
	if _, ok := s.Thumbnail(other.id); !ok {
		t.Error("the snapshot of the window lost")
	}
	other.stale = false
	other.liveDue.change(time.Now())
	if other.livePassable(s.render != nil && !s.noScaled) {
		t.Error("a window taken from its frame passable after the bind failed")
	}
}

// procCPU is the CPU time of the process so far, user and system, from
// /proc/<pid>/stat, in the ticks of 10 ms of Linux; false when unreadable
func procCPU(pid string) (time.Duration, bool) {
	data, err := os.ReadFile("/proc/" + pid + "/stat")
	if err != nil {
		return 0, false
	}
	// The name in parentheses may hold spaces: the fields after its end
	f := strings.Fields(string(data[bytes.LastIndexByte(data, ')')+1:]))
	if len(f) < 13 {
		return 0, false
	}
	utime, err1 := strconv.ParseInt(f[11], 10, 64)
	stime, err2 := strconv.ParseInt(f[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return time.Duration(utime+stime) * 10 * time.Millisecond, true
}

// xorgPID is the process of the X server, "" when not found
func xorgPID() string {
	dirs, _ := os.ReadDir("/proc")
	for _, d := range dirs {
		if comm, err := os.ReadFile("/proc/" + d.Name() + "/comm"); err == nil &&
			(strings.TrimSpace(string(comm)) == "Xorg" || strings.TrimSpace(string(comm)) == "X") {
			return d.Name()
		}
	}
	return ""
}

// cpuMeter is the CPU time of the X server and of this process over a
// stretch, less that of the X server over a stretch as long before, idle
type cpuMeter struct {
	xorg                   string
	idleX, x0, self0, wall time.Duration
	start                  time.Time
}

// newCPUMeter measures the X server idle for d, then starts
func newCPUMeter(d time.Duration) *cpuMeter {
	m := &cpuMeter{xorg: xorgPID()}
	before, _ := procCPU(m.xorg)
	time.Sleep(d)
	after, _ := procCPU(m.xorg)
	m.idleX = after - before
	m.wall = d
	m.x0, _ = procCPU(m.xorg)
	m.self0, _ = procCPU("self")
	m.start = time.Now()
	return m
}

// report reports the CPU time of the X server, less its idle share, and of
// this process, per op of n
func (m *cpuMeter) report(b *testing.B, n int) {
	x, okX := procCPU(m.xorg)
	self, okSelf := procCPU("self")
	wall := time.Since(m.start)
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	if okX && m.xorg != "" {
		idle := time.Duration(float64(m.idleX) * float64(wall) / float64(m.wall))
		b.ReportMetric(ms(x-m.x0-idle)/float64(n), "xorg-cpu-ms/pass")
		b.ReportMetric(ms(m.idleX)*1000/ms(m.wall), "xorg-idle-cpu-ms/s")
	}
	if okSelf {
		b.ReportMetric(ms(self-m.self0)/float64(n), "qws-cpu-ms/pass")
	}
}

// BenchmarkLivePassFrame is the cost of a live pass of a window taken from
// its frame (K14 of specs/020-live-thumbnails): a client window of 2556×1357
// of depth 24 at 2,22 in a frame of its visual, off the screen, scaled out of
// the frame's pixmap by RENDER into a pixmap of qws's own and copied on the
// GPU into the live texture, 30 passes a second — on the snapshotter's thread
// (pass), from its start to its publication (done), at p50 and p95; and the
// CPU of the X server a pass, less its own over as long a stretch before, and
// of the test's process. b.N is the number of passes; LIVE_FRAME_SIZE the
// window's size, 2556x1357 by default — 604x394 is the xterm of S5:
//
//	go test -run '^$' -bench LivePassFrame -benchtime 300x ./pkg/snapshot
//	LIVE_FRAME_SIZE=604x394 go test -run '^$' -bench LivePassFrame -benchtime 300x ./pkg/snapshot
func BenchmarkLivePassFrame(b *testing.B) {
	conn, err := x11.NewConn()
	if err != nil {
		b.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	level := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	defer zerolog.SetGlobalLevel(level)
	s := frameSnapshotter(b, conn)
	size := image.Pt(2556, 1357)
	if v := os.Getenv("LIVE_FRAME_SIZE"); v != "" {
		if _, err := fmt.Sscanf(v, "%dx%d", &size.X, &size.Y); err != nil {
			b.Fatalf("LIVE_FRAME_SIZE %q: %v", v, err)
		}
	}
	w, _ := frameWindow(b, s, conn, image.Pt(-4000, -4000), size, 0)
	defer s.forget(w.id)
	if _, ok := livePicture(b, s, w); !ok {
		b.Fatal("no pass")
	}

	var pass, done []float64
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	m := newCPUMeter(time.Duration(b.N) * time.Second / 30)
	next := time.Now()
	b.ResetTimer()
	for range b.N {
		time.Sleep(time.Until(next))
		next = next.Add(time.Second / 30)
		w.liveDue.change(time.Now())
		if !s.livePass(w, time.Now()) {
			b.Fatal("no pass")
		}
		for w.live.pending != 0 {
			// As the loop polls, not spinning
			time.Sleep(livePoll)
			s.publishDone()
		}
		pass = append(pass, ms(w.live.cpu))
		done = append(done, ms(time.Since(w.live.start)))
	}
	b.StopTimer()
	m.report(b, b.N)
	for _, v := range []struct {
		name string
		v    []float64
	}{{"pass", pass}, {"done", done}} {
		sort.Float64s(v.v)
		b.ReportMetric(v.v[(len(v.v)*50+99)/100-1], v.name+"-p50-ms")
		b.ReportMetric(v.v[(len(v.v)*95+99)/100-1], v.name+"-p95-ms")
	}
}

// TestFrameCopyQuiet checks D3 of specs/023-frame-pass-cost: the copy of a
// window out of its frame's pixmap, for a snapshot and for a live pass,
// sends no NoExposure event to the snapshotter's connection
func TestFrameCopyQuiet(t *testing.T) {
	conn, err := x11.NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	s := frameSnapshotter(t, conn)
	w, _ := frameWindow(t, s, conn, image.Pt(-4000, -4000), image.Pt(604, 394), 0)
	defer s.forget(w.id)
	for k := range 4 {
		if k%2 == 0 {
			s.capture(w, "test")
		} else if _, ok := livePicture(t, s, w); !ok {
			t.Fatal("no pass")
		}
	}
	xproto.GetInputFocus(conn).Reply()
	for ev, _ := conn.PollForEvent(); ev != nil; ev, _ = conn.PollForEvent() {
		if _, ok := ev.(xproto.NoExposureEvent); ok {
			t.Fatal("a NoExposure event of the copy")
		}
	}
}

// framePictures are two pictures of a window's size in pixmaps of the test,
// copied into the window's child in turn, so that it is drawn anew in the X
// server, not uploaded at each pass
type framePictures struct {
	conn    *xgb.Conn
	child   xproto.Window
	gc      xproto.Gcontext
	pixmaps [2]xproto.Pixmap
	size    image.Point
}

func newFramePictures(t testing.TB, conn *xgb.Conn, child xproto.Window, size image.Point, seed int64) *framePictures {
	t.Helper()
	f := &framePictures{conn: conn, child: child, size: size}
	screen := xproto.Setup(conn).DefaultScreen(conn)
	for i := range f.pixmaps {
		f.pixmaps[i] = newPixmap(t, conn, screen.Root, 24, size)
		t.Cleanup(func() { xproto.FreePixmap(conn, f.pixmaps[i]) })
		fillDrawable(t, conn, xproto.Drawable(f.pixmaps[i]), 24, windowImage(size.X, size.Y, seed+int64(i)))
	}
	f.gc, _ = xproto.NewGcontextId(conn)
	xproto.CreateGC(conn, f.gc, xproto.Drawable(child), xproto.GcGraphicsExposures, []uint32{0})
	t.Cleanup(func() { xproto.FreeGC(conn, f.gc) })
	return f
}

// show draws picture i into the window
func (f *framePictures) show(i int) {
	xproto.CopyArea(f.conn, xproto.Drawable(f.pixmaps[i]), xproto.Drawable(f.child), f.gc,
		0, 0, 0, 0, uint16(f.size.X), uint16(f.size.Y))
	f.conn.Sync()
}

// snapshot is the snapshot of picture i, taken by RENDER as 022 takes it
func (f *framePictures) snapshot(t testing.TB, s *Snapshotter, w *window, i int) *image.RGBA {
	t.Helper()
	f.show(i)
	s.capture(w, "test")
	img, _ := s.Thumbnail(w.id)
	snap, ok := img.(*image.RGBA)
	if !ok {
		t.Fatalf("snapshot %T, not taken by RENDER", img)
	}
	return snap
}

// TestFramePassPictures checks K1 of specs/023-frame-pass-cost: the passes
// from the frame, through the chain kept for the activation, give the
// pictures of 020's, byte for byte — each live picture equal, worst 0, to
// the snapshot of the same picture, which 020's passes equalled (K14 of 020)
// — for windows of depth 24 in frames of their visual off the screen, of
// 2556×1357 (two halvings), 1279×677 (one, both sides odd) and 604×394
// (none), one of another visual of depth 24, and one two levels below the
// window the compositor redirects; each passed 200 times, drawn anew from
// two pictures in turn between two passes: no pass reads the scaled pixmap
// as it was before
func TestFramePassPictures(t *testing.T) {
	conn, err := x11.NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	s := frameSnapshotter(t, conn)
	screen := xproto.Setup(conn).DefaultScreen(conn)
	passes := 200
	if testing.Short() {
		passes = 20
	}
	for _, c := range []struct {
		name   string
		size   image.Point
		visual xproto.Visualid
		nested bool
	}{
		{"2556×1357", image.Pt(2556, 1357), 0, false},
		{"1279×677", image.Pt(1279, 677), 0, false},
		{"604×394", image.Pt(604, 394), 0, false},
		{"of another visual", image.Pt(800, 500), otherVisual(screen, 24), false},
		{"two levels below", image.Pt(800, 500), 0, true},
	} {
		if c.name == "of another visual" && c.visual == 0 {
			t.Logf("%s: skipped, no such visual", c.name)
			continue
		}
		var w *window
		var child xproto.Window
		if c.nested {
			w, child = nestedFrameWindow(t, s, conn, image.Pt(-4000, -4000), c.size)
		} else {
			w, child = frameWindow(t, s, conn, image.Pt(-4000, -4000), c.size, c.visual)
		}
		pics := newFramePictures(t, conn, child, c.size, 40)
		snaps := [2]*image.RGBA{pics.snapshot(t, s, w, 0), pics.snapshot(t, s, w, 1)}
		worst := 0
		for k := range passes {
			pics.show(k % 2)
			got, ok := livePicture(t, s, w)
			if !ok {
				t.Fatalf("%s: pass %d not made", c.name, k)
			}
			if _, d := difference(got, snaps[k%2]); d > worst {
				worst = d
			}
		}
		t.Logf("%s: %d passes, worst %d from the snapshot of the same picture", c.name, passes, worst)
		if worst != 0 {
			t.Errorf("%s: worst %d from the snapshots, want 0", c.name, worst)
		}
		if w.chain == nil {
			t.Errorf("%s: no chain kept", c.name)
		}
		s.forget(w.id)
	}
}

// nestedFrameWindow is a client window of depth 24 at 2,22 in a window at
// 0,0 of a frame off the screen, which the compositor redirects: two levels
// below it, as 022's case
func nestedFrameWindow(t testing.TB, s *Snapshotter, conn *xgb.Conn, at image.Point, size image.Point) (*window, xproto.Window) {
	t.Helper()
	screen := xproto.Setup(conn).DefaultScreen(conn)
	r := image.Rectangle{Min: at, Max: at.Add(size).Add(image.Pt(4, 24))}
	top := testWindow(t, conn, screen.Root, r, 0, 0)
	frame := testWindow(t, conn, top, image.Rectangle{Max: r.Size()}, 0, 0)
	client := testWindow(t, conn, frame, image.Rect(2, 22, 2+size.X, 22+size.Y), 0, 0)
	child := testWindow(t, conn, client, image.Rectangle{Max: size}, 0, 0)
	waitRedirected(t, conn, top)
	w := &window{id: client, frame: frame, mapped: true, frameMapped: true, visual: screen.RootVisual, stale: true}
	s.windows[client], s.frames[frame] = w, client
	fillDrawable(t, conn, xproto.Drawable(child), 24, windowImage(size.X, size.Y, 98))
	s.capture(w, "test")
	if w.via != top || w.bound != nil {
		t.Fatalf("via 0x%x, bound %v; want the pixmap of 0x%x by RENDER", w.via, w.bound != nil, top)
	}
	return w, child
}

// TestFramePassChain checks K4 of specs/023-frame-pass-cost: the chain of a
// window's passes from the frame is freed at the end of the live passes and
// with the window — GetGeometry of each of its pixmaps answers BadDrawable —
// and at another size made of that size, the pictures of K1 at it
func TestFramePassChain(t *testing.T) {
	conn, err := x11.NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	s := frameSnapshotter(t, conn)
	s.live = liveSession{overlay: 7, interval: 33 * time.Millisecond}
	gone := func(what string, c *chain) {
		t.Helper()
		ids := append([]xproto.Pixmap{c.copy}, c.pixmaps...)
		for _, p := range ids {
			if _, err := xproto.GetGeometry(conn, xproto.Drawable(p)).Reply(); err == nil {
				t.Errorf("%s: pixmap 0x%x of the chain still there", what, p)
			}
		}
	}
	w, child := frameWindow(t, s, conn, image.Pt(-4000, -4000), image.Pt(1279, 677), 0)
	if _, ok := livePicture(t, s, w); !ok {
		t.Fatal("no pass")
	}
	c := w.chain
	if c == nil || c.copy == 0 || len(c.pixmaps) != 1 {
		t.Fatalf("chain %+v, want a copy and one halving", c)
	}
	// The end of the live passes
	s.liveWant = liveSession{}
	s.setLive()
	if w.chain != nil {
		t.Error("a chain kept past the end of the live passes")
	}
	gone("live ended", c)

	// Another size: the window resized in its frame, named anew
	s.live = liveSession{overlay: 7, interval: 33 * time.Millisecond}
	if _, ok := livePicture(t, s, w); !ok {
		t.Fatal("no pass")
	}
	size := image.Pt(800, 500)
	xproto.ConfigureWindow(conn, w.id, xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{uint32(size.X), uint32(size.Y)})
	xproto.ConfigureWindow(conn, child, xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{uint32(size.X), uint32(size.Y)})
	conn.Sync()
	w.stale = true
	pics := newFramePictures(t, conn, child, size, 60)
	snap := pics.snapshot(t, s, w, 0)
	old := w.chain
	got, ok := livePicture(t, s, w)
	if !ok {
		t.Fatal("no pass at the new size")
	}
	if w.chain == nil || w.chain.size != size {
		t.Errorf("chain of %v, want %v", w.chain.size, size)
	}
	if _, worst := difference(got, snap); worst != 0 {
		t.Errorf("at the new size: worst %d from the snapshot, want 0", worst)
	}
	if old != nil {
		gone("another size", old)
	}

	// The window forgotten
	c = w.chain
	s.forget(w.id)
	if w.chain != nil {
		t.Error("a chain kept with the window forgotten")
	}
	gone("forgotten", c)
}

// passRequests makes a pass of the window and counts what it sends on the
// snapshotter's connection, which nothing else sends on — the difference of
// the sequence numbers of two GetInputFocus around it, less one — and the
// NoExposure events the connection receives meanwhile; false when no pass is
// made
func passRequests(t testing.TB, s *Snapshotter, conn *xgb.Conn, w *window) (requests, noExposures int, ok bool) {
	t.Helper()
	for ev, _ := conn.PollForEvent(); ev != nil; ev, _ = conn.PollForEvent() {
	}
	before := xproto.GetInputFocus(conn)
	if _, err := before.Reply(); err != nil {
		t.Fatal(err)
	}
	if _, ok = livePicture(t, s, w); !ok {
		return 0, 0, false
	}
	after := xproto.GetInputFocus(conn)
	if _, err := after.Reply(); err != nil {
		t.Fatal(err)
	}
	for ev, _ := conn.PollForEvent(); ev != nil; ev, _ = conn.PollForEvent() {
		if _, isNoExposure := ev.(xproto.NoExposureEvent); isNoExposure {
			noExposures++
		}
	}
	return int(after.Sequence-before.Sequence) - 1, noExposures, true
}

// TestFramePassRequests checks K3 of specs/023-frame-pass-cost: a pass from
// the frame after the first of an activation sends at most 6 requests on
// the snapshotter's connection for a window of 2556×1357, two halvings, and
// 4 for one of 604×394, none — 020's passes sent 28 and 14 — and brings no
// NoExposure event; the first, which makes what the passes keep, no more
// requests than a pass of 020. The windows of the test have no DAMAGE, so
// their passes send no DamageSubtract, one request of qws's passes: 5 and
// 3 at most here, 27 and 13 for 020's. The windows are off the screen.
func TestFramePassRequests(t *testing.T) {
	conn, err := x11.NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	s := frameSnapshotter(t, conn)
	for _, c := range []struct {
		size        image.Point
		most, of020 int
	}{
		{image.Pt(2556, 1357), 6 - 1, 28 - 1},
		{image.Pt(604, 394), 4 - 1, 14 - 1},
	} {
		w, _ := frameWindow(t, s, conn, image.Pt(-4000, -4000), c.size, 0)
		// The scaled pixmap of 020 made, as at a window's first pass of all
		if _, ok := livePicture(t, s, w); !ok {
			t.Fatalf("%v: no pass", c.size)
		}
		s.dropChains()
		first, firstEvents, ok := passRequests(t, s, conn, w)
		if !ok {
			t.Fatalf("%v: no first pass of the activation", c.size)
		}
		var counts []int
		events := firstEvents
		for range 3 {
			n, e, ok := passRequests(t, s, conn, w)
			if !ok {
				t.Fatalf("%v: no pass", c.size)
			}
			counts, events = append(counts, n), events+e
		}
		t.Logf("%v: the first pass of an activation %d requests, the next %v; %d NoExposure events",
			c.size, first, counts, events)
		for _, n := range counts {
			if n > c.most {
				t.Errorf("%v: %d requests a pass, want %d at most (020: %d)", c.size, n, c.most, c.of020)
			}
		}
		if first > c.of020 {
			t.Errorf("%v: the first pass %d requests, more than 020's %d", c.size, first, c.of020)
		}
		if events != 0 {
			t.Errorf("%v: %d NoExposure events", c.size, events)
		}
		s.forget(w.id)
	}
}

// TestFramePassMoved checks K2 of specs/023-frame-pass-cost: a window moved
// in its frame between two passes — as i3 moves one, showing or hiding a
// title bar — and one two levels below the window the compositor redirects,
// moved with its parent: the next live picture equal byte for byte to the
// snapshot at the new place, the pass made again once — 2(k + 3) requests,
// with no DamageSubtract, as TestFramePassRequests counts — and the offset
// kept for the passes after it
func TestFramePassMoved(t *testing.T) {
	conn, err := x11.NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	s := frameSnapshotter(t, conn)
	size := image.Pt(604, 394)
	for _, nested := range []bool{false, true} {
		var w *window
		var child xproto.Window
		if nested {
			w, child = nestedFrameWindow(t, s, conn, image.Pt(-4000, -4000), size)
		} else {
			w, child = frameWindow(t, s, conn, image.Pt(-4000, -4000), size, 0)
		}
		pics := newFramePictures(t, conn, child, size, 80)
		pics.show(0)
		if _, ok := livePicture(t, s, w); !ok {
			t.Fatal("no pass")
		}
		// Up by 20 pixels: the window itself, or the window it lies in
		moved := w.id
		if nested {
			tree, err := xproto.QueryTree(conn, w.id).Reply()
			if err != nil {
				t.Fatal(err)
			}
			moved = tree.Parent
		}
		geom, err := xproto.GetGeometry(conn, xproto.Drawable(moved)).Reply()
		if err != nil {
			t.Fatal(err)
		}
		xproto.ConfigureWindow(conn, moved, xproto.ConfigWindowY, []uint32{uint32(int(geom.Y) - 20)})
		conn.Sync()
		before := w.at
		n, _, ok := passRequests(t, s, conn, w)
		if !ok {
			t.Fatalf("nested %v: no pass after the move", nested)
		}
		got := s.pics[w.id]
		img := image.NewRGBA(image.Rect(0, 0, got.Width, got.Height))
		gl.BindTexture(gl.TEXTURE_2D, got.Texture)
		gl.GetTexImage(gl.TEXTURE_2D, 0, gl.RGBA, gl.UNSIGNED_BYTE, unsafe.Pointer(&img.Pix[0]))
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = 255
		}
		snap := pics.snapshot(t, s, w, 0)
		_, worst := difference(img, snap)
		t.Logf("nested %v: at %v, then %v; %d requests; worst %d from the snapshot at the new place",
			nested, before, w.at, n, worst)
		if worst != 0 {
			t.Errorf("nested %v: worst %d from the snapshot at the new place", nested, worst)
		}
		if w.at != before.Add(image.Pt(0, -20)) {
			t.Errorf("nested %v: offset %v kept, want %v", nested, w.at, before.Add(image.Pt(0, -20)))
		}
		if n != 2*(0+3) {
			t.Errorf("nested %v: %d requests, want 6, the pass made again once", nested, n)
		}
		if again, _, _ := passRequests(t, s, conn, w); again != 0+3 {
			t.Errorf("nested %v: %d requests the pass after, want 3", nested, again)
		}
		s.forget(w.id)
	}
}
