package snapshot

import (
	"image"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/almaz-uno/qws/pkg/composite"
	"github.com/almaz-uno/qws/pkg/glx"
	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/jezek/xgb"
	xcomposite "github.com/jezek/xgb/composite"
	"github.com/jezek/xgb/damage"
	"github.com/jezek/xgb/xfixes"
	"github.com/jezek/xgb/xproto"
)

// frameSnapshotter is a snapshotter on conn, a connection of x11.NewConn,
// without its loop, its GL context current on this thread, with RENDER and
// the CPU path; it skips the test without a compositing manager, RENDER or
// offscreen GLX
func frameSnapshotter(t testing.TB, conn *xgb.Conn) *Snapshotter {
	t.Helper()
	if !compositing(conn) {
		t.Skip("no compositing manager: no window of the test is redirected")
	}
	for _, init := range []func() error{
		func() error { _, err := xcomposite.QueryVersion(conn, 0, 4).Reply(); return err },
		func() error { _, err := xfixes.QueryVersion(conn, 5, 0).Reply(); return err },
		func() error { _, err := damage.QueryVersion(conn, 1, 1).Reply(); return err },
	} {
		if err := init(); err != nil {
			t.Skipf("no Composite, XFIXES or DAMAGE: %v", err)
		}
	}
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	off, err := glx.NewOffscreen()
	if err != nil {
		t.Skipf("no offscreen GLX: %v", err)
	}
	t.Cleanup(off.Destroy)
	g, err := newGPU()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.close)
	x, err := newXRender(conn, root)
	if err != nil {
		t.Skipf("no RENDER: %v", err)
	}
	t.Cleanup(x.close)
	cpu, err := composite.NewCapturer(conn, root, "bilinear")
	if err != nil {
		t.Fatal(err)
	}
	return &Snapshotter{
		conn:    conn,
		root:    root,
		thumbs:  map[xproto.Window]image.Image{},
		windows: map[xproto.Window]*window{},
		frames:  map[xproto.Window]xproto.Window{},
		off:     off,
		gpu:     g,
		render:  x,
		cpu:     cpu,
	}
}

// compositing reports whether a compositing manager owns the screen
func compositing(conn *xgb.Conn) bool {
	name := "_NET_WM_CM_S0"
	atom, err := xproto.InternAtom(conn, true, uint16(len(name)), name).Reply()
	if err != nil || atom.Atom == 0 {
		return false
	}
	owner, err := xproto.GetSelectionOwner(conn, atom.Atom).Reply()
	return err == nil && owner.Owner != 0
}

// testWindow makes a mapped window in parent, of the visual and depth, or
// of its parent's with depth 0; override-redirect if a child of the root
func testWindow(t testing.TB, c *xgb.Conn, parent xproto.Window, r image.Rectangle, depth byte, visual xproto.Visualid) xproto.Window {
	t.Helper()
	id, err := xproto.NewWindowId(c)
	if err != nil {
		t.Fatal(err)
	}
	screen := xproto.Setup(c).DefaultScreen(c)
	mask := uint32(xproto.CwBackPixel | xproto.CwBorderPixel)
	values := []uint32{0x404040, 0}
	if parent == screen.Root {
		mask |= xproto.CwOverrideRedirect
		values = append(values, 1)
	}
	if visual != 0 {
		cm, _ := xproto.NewColormapId(c)
		xproto.CreateColormap(c, xproto.ColormapAllocNone, cm, screen.Root, visual)
		mask |= xproto.CwColormap
		values = append(values, uint32(cm))
	} else {
		depth = 0
	}
	if err := xproto.CreateWindowChecked(c, depth, id, parent, int16(r.Min.X), int16(r.Min.Y),
		uint16(r.Dx()), uint16(r.Dy()), 0, xproto.WindowClassInputOutput, visual, mask, values).Check(); err != nil {
		t.Fatal(err)
	}
	if err := xproto.MapWindowChecked(c, id).Check(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { xproto.DestroyWindow(c, id) })
	return id
}

// otherVisual is a TrueColor visual of the depth other than the default one
func otherVisual(screen *xproto.ScreenInfo, depth int) xproto.Visualid {
	for _, d := range screen.AllowedDepths {
		if int(d.Depth) != depth {
			continue
		}
		for _, v := range d.Visuals {
			if v.Class == xproto.VisualClassTrueColor && v.VisualId != screen.RootVisual {
				return v.VisualId
			}
		}
	}
	return 0
}

// TestCaptureFromFrame checks K1–K3 of specs/022-uncaptured-windows: a client
// window that the X server does not redirect — of depth 24 in a frame of its
// visual, or of another visual of depth 24, or two levels below the window
// the compositor redirects — whose picture is in a child of another client
// or of its own, is captured by RENDER from its ancestor's pixmap, within a
// mean of 1.2 per channel of the area average of what it shows, and drawn
// anew, captured anew; its ancestor's pixmap is not bound on the GPU. Drawing
// into the child reports damage of the client window. A window of depth 32
// in a frame of depth 24, which the X server redirects, is captured on the
// GPU from its own pixmap, as before. The windows are off the screen.
func TestCaptureFromFrame(t *testing.T) {
	conn, err := x11.NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	s := frameSnapshotter(t, conn)
	other, err := x11.NewConn()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	screen := xproto.Setup(conn).DefaultScreen(conn)
	v24, v32 := otherVisual(screen, 24), visualOf(screen, 32)

	size := image.Pt(800, 500)
	frameRect := image.Rect(-3000, -3000, -3000+size.X+4, -3000+size.Y+24)
	inFrame := image.Rect(2, 22, 2+size.X, 22+size.Y)
	cases := []struct {
		name   string
		depth  int
		visual xproto.Visualid // of the client window; 0: its frame's
		nested bool            // the frame inside the window the compositor redirects
		child  *xgb.Conn       // the client of the child that shows the picture
		viaGPU bool            // the window has a pixmap of its own
	}{
		{"depth 24 in a frame of its visual, its child of another client", 24, 0, false, other, false},
		{"depth 24 in a frame of its visual, its child of its own", 24, 0, false, conn, false},
		{"depth 24 of another visual than its frame's", 24, v24, false, other, false},
		{"depth 24 two levels below the frame redirected", 24, 0, true, other, false},
		{"depth 32 in a frame of depth 24", 32, v32, false, conn, true},
	}
	for i, c := range cases {
		if (c.depth == 32 || i == 2) && c.visual == 0 {
			t.Logf("%s: skipped, no such visual", c.name)
			continue
		}
		top := testWindow(t, conn, screen.Root, frameRect, 0, 0)
		frame := top
		if c.nested {
			frame = testWindow(t, conn, top, image.Rect(0, 0, frameRect.Dx(), frameRect.Dy()), 0, 0)
		}
		client := testWindow(t, conn, frame, inFrame, byte(c.depth), c.visual)
		child := testWindow(t, c.child, client, image.Rectangle{Max: size}, 0, 0)
		c.child.Sync()

		w := &window{id: client, frame: frame, mapped: true, frameMapped: true, visual: c.visual, stale: true}
		if w.visual == 0 {
			w.visual = screen.RootVisual
		}
		s.windows[client], s.frames[frame] = w, client
		d, _ := damage.NewDamageId(conn)
		if err := damage.CreateChecked(conn, d, xproto.Drawable(client), damage.ReportLevelNonEmpty).Check(); err != nil {
			t.Fatal(err)
		}
		// The compositor redirects the frame: until then nothing is named
		waitRedirected(t, conn, top)

		for k, seed := range []int64{1, 2} {
			picture := windowImage(size.X, size.Y, int64(10*i)+seed)
			damage.Subtract(conn, d, 0, 0)
			fillDrawable(t, c.child, xproto.Drawable(child), c.depth, picture)
			if k > 0 && !damaged(conn, d) {
				t.Errorf("%s: drawing into the child reported no damage of the client window", c.name)
			}
			s.mu.Lock()
			delete(s.thumbs, client)
			s.mu.Unlock()
			s.capture(w, "test")
			img, ok := s.Thumbnail(client)
			if !ok {
				t.Fatalf("%s, picture %d: no thumbnail", c.name, k+1)
			}
			got, isRGBA := img.(*image.RGBA)
			if !isRGBA {
				t.Fatalf("%s, picture %d: thumbnail %T, taken on the CPU", c.name, k+1, img)
			}
			tw, th := thumbSize(size.X, size.Y)
			mean, worst := difference(got, areaAverage(picture, tw, th))
			t.Logf("%s, picture %d: via 0x%x, bound %v, mean %.3f, worst %d", c.name, k+1, w.via, w.bound != nil, mean, worst)
			if mean > 1.2 {
				t.Errorf("%s, picture %d: mean %.3f from the area average", c.name, k+1, mean)
			}
			switch {
			case c.viaGPU && (w.via != 0 || w.bound == nil):
				t.Errorf("%s: via 0x%x, bound %v; want its own pixmap on the GPU", c.name, w.via, w.bound != nil)
			case !c.viaGPU && (w.via != top || w.bound != nil):
				t.Errorf("%s: via 0x%x, bound %v; want the pixmap of 0x%x by RENDER", c.name, w.via, w.bound != nil, top)
			}
		}
		damage.Destroy(conn, d)
		s.forget(client)
	}
}

// waitRedirected waits, a second at most, until the window's pixmap can be
// named
func waitRedirected(t testing.TB, conn *xgb.Conn, win xproto.Window) {
	t.Helper()
	for deadline := time.Now().Add(time.Second); ; {
		p, _ := xproto.NewPixmapId(conn)
		if xcomposite.NameWindowPixmapChecked(conn, win, p).Check() == nil {
			xproto.FreePixmap(conn, p)
			return
		}
		if time.Now().After(deadline) {
			t.Skip("the compositing manager does not redirect a window of the test")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// damaged reports whether the damage reported a change within 300 ms
func damaged(conn *xgb.Conn, d damage.Damage) bool {
	for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline); {
		ev, _ := conn.PollForEvent()
		if n, ok := ev.(damage.NotifyEvent); ok && n.Damage == d {
			return true
		}
		if ev == nil {
			time.Sleep(5 * time.Millisecond)
		}
	}
	return false
}

// fillDrawable draws the image into a drawable of the depth, BGRA as the X
// server keeps 24- and 32-bit pixels on this machine, opaque
func fillDrawable(t testing.TB, c *xgb.Conn, d xproto.Drawable, depth int, img *image.RGBA) {
	t.Helper()
	w, h := img.Rect.Dx(), img.Rect.Dy()
	gc, _ := xproto.NewGcontextId(c)
	xproto.CreateGC(c, gc, d, 0, nil)
	defer xproto.FreeGC(c, gc)
	rows := max(1, 200000/(4*w))
	for y0 := 0; y0 < h; y0 += rows {
		y1 := min(y0+rows, h)
		data := make([]byte, 4*w*(y1-y0))
		for y := y0; y < y1; y++ {
			for x := 0; x < w; x++ {
				s, o := img.PixOffset(x, y), 4*((y-y0)*w+x)
				data[o], data[o+1], data[o+2], data[o+3] = img.Pix[s+2], img.Pix[s+1], img.Pix[s], 255
			}
		}
		if err := xproto.PutImageChecked(c, xproto.ImageFormatZPixmap, d, gc,
			uint16(w), uint16(y1-y0), 0, int16(y0), 0, byte(depth), data).Check(); err != nil {
			t.Fatal(err)
		}
	}
	c.Sync()
}

// BenchmarkFrameRender is the cost of a snapshot from a frame of
// specs/022-uncaptured-windows: a client window of 2556×1357, of the
// frame's visual, at 2,22 in a frame of 2560×1381, off the screen — its
// pixmap named once, then each capture its rectangle copied and scaled by
// RENDER and read back — at p50 and p95. b.N is the number of captures:
//
//	go test -run '^$' -bench FrameRender -benchtime 200x ./pkg/snapshot
func BenchmarkFrameRender(b *testing.B) {
	conn, err := x11.NewConn()
	if err != nil {
		b.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	s := frameSnapshotter(b, conn)
	screen := xproto.Setup(conn).DefaultScreen(conn)
	size := image.Pt(2556, 1357)
	top := testWindow(b, conn, screen.Root, image.Rect(-4000, -4000, -4000+size.X+4, -4000+size.Y+24), 0, 0)
	client := testWindow(b, conn, top, image.Rect(2, 22, 2+size.X, 22+size.Y), 0, 0)
	fillDrawable(b, conn, xproto.Drawable(client), 24, windowImage(size.X, size.Y, 1))
	waitRedirected(b, conn, top)
	w := &window{id: client, frame: top, mapped: true, frameMapped: true, visual: screen.RootVisual, stale: true}
	s.windows[client], s.frames[top] = w, client
	s.capture(w, "test")
	if w.via == 0 {
		b.Fatal("not taken from the frame")
	}
	var ms []float64
	b.ResetTimer()
	for range b.N {
		start := time.Now()
		s.capture(w, "test")
		ms = append(ms, float64(time.Since(start))/float64(time.Millisecond))
	}
	b.StopTimer()
	sort.Float64s(ms)
	b.ReportMetric(ms[(len(ms)*50+99)/100-1], "p50-ms")
	b.ReportMetric(ms[(len(ms)*95+99)/100-1], "p95-ms")
	b.Logf("%d captures of 0x%x via 0x%x", len(ms), client, w.via)
	s.forget(client)
}
