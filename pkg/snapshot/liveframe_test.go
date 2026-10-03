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
