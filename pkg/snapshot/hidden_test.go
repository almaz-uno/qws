package snapshot

import (
	"image"
	"testing"
	"time"

	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// The last snapshot of a window hidden with a change not yet captured
// (specs/016-unviewable-thumbnails). The windows of the tests are off the
// screen, under the compositor running: a frame, an override-redirect child
// of the root that the compositor redirects as it does the frames of i3, made
// by the connection of a "window manager"; in it a client window, made by
// the connection of a "client", with a child of the client's that shows the
// pictures. The snapshotter follows the window through follow and handle, on
// the events the X server sends its connection, and its loop's turn is run
// here, through tick and captureChanged, on the thread of its GL context.

// hiddenEnv is a snapshotter of the tests with an interval, and the
// connections of the window manager and of the client
type hiddenEnv struct {
	s          *Snapshotter
	wm, client *xgb.Conn
	screen     *xproto.ScreenInfo
	size       image.Point
}

func newHiddenEnv(t *testing.T) *hiddenEnv {
	t.Helper()
	conn, err := x11.NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	t.Cleanup(conn.Close)
	s := frameSnapshotter(t, conn)
	s.interval = time.Second
	e := &hiddenEnv{s: s, screen: xproto.Setup(conn).DefaultScreen(conn), size: image.Pt(800, 500)}
	for _, c := range []**xgb.Conn{&e.wm, &e.client} {
		if *c, err = x11.NewConn(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup((*c).Close)
	}
	return e
}

// hiddenWindow is a window of the tests: its frame, the client window, and
// the child that shows its pictures, a picture and its negative in turn
type hiddenWindow struct {
	frame, id, child xproto.Window
	depth            int
	pictures         [2]*image.RGBA
	shown            int // the picture drawn last
}

// newWindow makes a window of the depth and visual — 0, its frame's — in a
// frame at at, follows it as the snapshotter follows a window of the client
// list, draws its first picture and, with capture, captures it as an
// activation would
func (e *hiddenEnv) newWindow(t *testing.T, at image.Point, depth int, visual xproto.Visualid, seed int64, capture bool) *hiddenWindow {
	t.Helper()
	frame := testWindow(t, e.wm, e.screen.Root, image.Rectangle{Min: at, Max: at.Add(e.size).Add(image.Pt(4, 24))}, 0, 0)
	id := testWindow(t, e.client, frame, image.Rect(2, 22, 2+e.size.X, 22+e.size.Y), byte(depth), visual)
	child := testWindow(t, e.client, id, image.Rectangle{Max: e.size}, 0, 0)
	e.client.Sync()
	e.wm.Sync()
	waitRedirected(t, e.s.conn, frame)
	first := windowImage(e.size.X, e.size.Y, seed)
	h := &hiddenWindow{frame: frame, id: id, child: child, depth: depth, pictures: [2]*image.RGBA{first, negative(first)}}
	e.s.follow(id, time.Now())
	if e.s.windows[id] == nil {
		t.Fatal("the window is not followed")
	}
	fillDrawable(t, e.client, xproto.Drawable(child), depth, first)
	e.pump(t)
	if capture {
		e.s.captureChanged(time.Now(), causeActivation, true)
		if _, ok := e.s.Thumbnail(id); !ok {
			t.Fatal("the window not captured")
		}
	}
	return h
}

// draw draws the window's other picture
func (e *hiddenEnv) draw(t *testing.T, h *hiddenWindow) {
	t.Helper()
	h.shown = 1 - h.shown
	fillDrawable(t, e.client, xproto.Drawable(h.child), h.depth, h.pictures[h.shown])
}

// hide unmaps the window's frame, as i3 hides a window, or, self, the
// client unmaps the window
func (e *hiddenEnv) hide(t *testing.T, h *hiddenWindow, self bool) {
	t.Helper()
	if self {
		xproto.UnmapWindow(e.client, h.id)
		e.client.Sync()
	} else {
		xproto.UnmapWindow(e.wm, h.frame)
		e.wm.Sync()
	}
}

// pump hands the snapshotter the events its connection has, as its loop
// does, once the X server has answered what was asked before
func (e *hiddenEnv) pump(t *testing.T) {
	t.Helper()
	e.s.conn.Sync()
	for {
		ev, err := e.s.conn.PollForEvent()
		if ev == nil && err == nil {
			return
		}
		if ev != nil {
			e.s.handle(ev)
		}
	}
}

// gen is the generation of the window's thumbnail, 0 for none
func (e *hiddenEnv) gen(h *hiddenWindow) uint64 {
	e.s.mu.RLock()
	defer e.s.mu.RUnlock()
	return e.s.thumbGen[h.id]
}

// dueNow reports whether the snapshotter's next capture is due at once
func (e *hiddenEnv) dueNow() bool {
	due, ok := e.s.nextDue()
	return ok && !due.After(time.Now())
}

// checkShown checks that the window's thumbnail is of a generation after
// before and shows its last picture: within 1 per channel on the GPU, a
// mean of 1.2 by RENDER
func (e *hiddenEnv) checkShown(t *testing.T, name string, h *hiddenWindow, before uint64, gpu bool) {
	t.Helper()
	if g := e.gen(h); g <= before {
		t.Errorf("%s: no snapshot after the unmap, generation %d", name, g)
		return
	}
	img, _ := e.s.Thumbnail(h.id)
	got, ok := img.(*image.RGBA)
	if !ok {
		t.Errorf("%s: thumbnail %T, taken on the CPU", name, img)
		return
	}
	tw, th := thumbSize(e.size.X, e.size.Y)
	mean, worst := difference(got, areaAverage(h.pictures[h.shown], tw, th))
	older, _ := difference(got, areaAverage(h.pictures[1-h.shown], tw, th))
	t.Logf("%s: mean %.3f, worst %d from the last picture, %.1f from the one before", name, mean, worst, older)
	if gpu && worst > 1 || mean > 1.2 {
		t.Errorf("%s: mean %.3f, worst %d from the last picture; %.1f from the one before", name, mean, worst, older)
	}
}

// negative is the picture with its colours inverted, opaque
func negative(img *image.RGBA) *image.RGBA {
	out := image.NewRGBA(img.Rect)
	for i, v := range img.Pix {
		out.Pix[i] = 255 - v
		if i%4 == 3 {
			out.Pix[i] = 255
		}
	}
	return out
}

// TestHiddenSnapshot checks K1 and K2 of specs/016-unviewable-thumbnails: a
// window captured, drawn anew and hidden at once is due at once and, at the
// loop's turn, gets a snapshot of its last picture from the pixmap the
// snapshotter holds, which it frees then — on the GPU, a window with a
// pixmap of its own, its frame unmapped or the window unmapped by its
// client; by RENDER, a window drawn into its frame, the frame unmapped, also
// right after the frame is restacked at its size, as i3 restacks the frames
// of a workspace it leaves; and a window with a pixmap of its own that the
// GPU did not bind, from that pixmap.
func TestHiddenSnapshot(t *testing.T) {
	e := newHiddenEnv(t)
	v32 := visualOf(e.screen, 32)
	cases := []struct {
		name     string
		depth    int
		visual   xproto.Visualid
		self     bool // the client unmaps the window; else the window manager the frame
		restack  bool // the frame restacked at its size before the unmap
		unbound  bool // the GPU did not bind the window's pixmap
		gpu, via bool // taken on the GPU; from the frame
	}{
		{"own pixmap, frame unmapped", 32, v32, false, false, false, true, false},
		{"own pixmap, the window unmapped by its client", 32, v32, true, false, false, true, false},
		{"own pixmap not bound, frame unmapped", 32, v32, false, false, true, false, false},
		{"from its frame, frame unmapped", 24, 0, false, false, false, false, true},
		{"from its frame, frame restacked and unmapped", 24, 0, false, true, false, false, true},
	}
	for i, c := range cases {
		if c.depth == 32 && c.visual == 0 {
			t.Logf("%s: skipped, no visual of depth 32", c.name)
			continue
		}
		h := e.newWindow(t, image.Pt(-4000+20*i, -4000), c.depth, c.visual, int64(10+i), true)
		w := e.s.windows[h.id]
		if c.via != (w.via == h.frame) || c.via == (w.bound != nil) {
			t.Fatalf("%s: via 0x%x, bound %v after the first capture", c.name, w.via, w.bound != nil)
		}
		if c.unbound {
			// As captureGPU leaves a pixmap the GPU would not bind: named,
			// not bound (specs/018-snapshot-bind-conflicts)
			w.bound.Release()
			w.bound = nil
		}
		before := e.gen(h)
		e.draw(t, h)
		if c.restack {
			other := testWindow(t, e.wm, e.screen.Root, image.Rect(-4000, -4000, -3990, -3990), 0, 0)
			xproto.ConfigureWindow(e.wm, h.frame, xproto.ConfigWindowSibling|xproto.ConfigWindowStackMode,
				[]uint32{uint32(other), xproto.StackModeAbove})
			e.wm.Sync()
		}
		e.hide(t, h, c.self)
		e.pump(t)
		if !e.dueNow() {
			t.Errorf("%s: not due at once after the unmap", c.name)
		}
		e.s.tick(time.Now())
		e.checkShown(t, c.name, h, before, c.gpu)
		if w.pixmap != 0 || w.bound != nil {
			t.Errorf("%s: the pixmap still held after the last snapshot", c.name)
		}
		if e.dueNow() {
			t.Errorf("%s: due again after the last snapshot", c.name)
		}
		e.s.forget(h.id)
	}
}

// TestHiddenNoSnapshot checks K3 and K4 of specs/016-unviewable-thumbnails.
// No snapshot after the unmap, the pixmap freed at once, for a window not
// drawn since its last snapshot, and for a window drawn into its frame that
// is itself unmapped, the frame still mapped; none for a window hidden before
// its first capture, or after a change of size not yet captured. A viewable
// window drawn anew within the interval is not captured before an interval
// after its last snapshot, while another window is hidden and captured; a
// frame restacked at its size leaves its window neither stale nor changed,
// moved not stale — changed by the damage the X server reports of the move —
// and one resized makes it stale and changed, as before.
func TestHiddenNoSnapshot(t *testing.T) {
	e := newHiddenEnv(t)
	at := func(i int) image.Point { return image.Pt(-4000+20*i, -4000) }

	// Unchanged since its last snapshot, either path
	for i, depth := range []int{32, 24} {
		visual := xproto.Visualid(0)
		if depth == 32 {
			if visual = visualOf(e.screen, 32); visual == 0 {
				continue
			}
		}
		h := e.newWindow(t, at(i), depth, visual, int64(20+i), true)
		w, before := e.s.windows[h.id], e.gen(h)
		e.hide(t, h, false)
		e.pump(t)
		if w.pixmap != 0 || w.bound != nil {
			t.Errorf("depth %d, unchanged: its pixmap held after the unmap", depth)
		}
		e.s.tick(time.Now())
		if g := e.gen(h); g != before {
			t.Errorf("depth %d, unchanged: a snapshot after the unmap", depth)
		}
		e.s.forget(h.id)
	}

	// Drawn into its frame, the window unmapped inside its frame: the frame
	// paints over it
	h := e.newWindow(t, at(2), 24, 0, 22, true)
	w, before := e.s.windows[h.id], e.gen(h)
	e.draw(t, h)
	e.hide(t, h, true)
	e.pump(t)
	if w.pixmap != 0 {
		t.Error("drawn into its frame, unmapped itself: the frame's pixmap held after the unmap")
	}
	e.s.tick(time.Now())
	if e.gen(h) != before {
		t.Error("drawn into its frame, unmapped itself: a snapshot after the unmap")
	}
	e.s.forget(h.id)

	// Hidden before its first capture
	h = e.newWindow(t, at(3), 24, 0, 23, false)
	e.draw(t, h)
	e.hide(t, h, false)
	e.pump(t)
	e.s.tick(time.Now().Add(time.Second))
	if _, ok := e.s.Thumbnail(h.id); ok {
		t.Error("hidden before its first capture: a snapshot")
	}
	e.s.forget(h.id)

	// Hidden after a change of size not yet captured
	h = e.newWindow(t, at(4), 24, 0, 24, true)
	before = e.gen(h)
	xproto.ConfigureWindow(e.client, h.id, xproto.ConfigWindowWidth, []uint32{uint32(e.size.X - 10)})
	e.client.Sync()
	e.draw(t, h)
	e.hide(t, h, false)
	e.pump(t)
	e.s.tick(time.Now().Add(time.Second))
	if e.gen(h) != before {
		t.Error("hidden after a change of size not yet captured: a snapshot")
	}
	e.s.forget(h.id)

	// A viewable window changed within the interval waits for it, while
	// another is hidden and captured
	a := e.newWindow(t, at(5), 24, 0, 25, true)
	b := e.newWindow(t, at(6), 24, 0, 26, true)
	wa := e.s.windows[a.id]
	shot, beforeA, beforeB := wa.schedule.lastShot, e.gen(a), e.gen(b)
	e.draw(t, a)
	e.draw(t, b)
	e.hide(t, b, false)
	e.pump(t)
	e.s.tick(time.Now())
	if e.gen(b) == beforeB {
		t.Error("the window hidden: no snapshot")
	}
	if e.gen(a) != beforeA {
		t.Error("the viewable window changed within the interval: captured at another's unmap")
	}
	due, ok := wa.schedule.due(e.s.interval)
	if next, nok := e.s.nextDue(); !ok || !nok || !next.Equal(due) || due.Before(shot.Add(e.s.interval)) {
		t.Errorf("the viewable window due %v (%v), next %v (%v); want an interval after its snapshot, %v",
			due, ok, next, nok, shot.Add(e.s.interval))
	}
	e.s.tick(due)
	if e.gen(a) == beforeA {
		t.Error("the viewable window not captured when due")
	}
	e.s.forget(a.id)
	e.s.forget(b.id)

	// The frame of a window taken from it: restacked, moved, resized
	h = e.newWindow(t, at(7), 24, 0, 27, true)
	w = e.s.windows[h.id]
	other := testWindow(t, e.wm, e.screen.Root, image.Rect(-4000, -4000, -3990, -3990), 0, 0)
	// Moved, the X server reports damage of the whole window, which makes it
	// changed, as before
	steps := []struct {
		name           string
		mask           uint16
		values         []uint32
		stale, changed bool
	}{
		{"restacked", xproto.ConfigWindowSibling | xproto.ConfigWindowStackMode, []uint32{uint32(other), xproto.StackModeAbove}, false, false},
		{"moved", xproto.ConfigWindowY, []uint32{uint32(0xffffffff - 3989)}, false, true}, // −3990
		{"resized", xproto.ConfigWindowWidth, []uint32{uint32(e.size.X + 14)}, true, true},
	}
	for _, st := range steps {
		xproto.ConfigureWindow(e.wm, h.frame, st.mask, st.values)
		e.wm.Sync()
		e.pump(t)
		if w.stale != st.stale || w.schedule.dirty != st.changed {
			t.Errorf("frame %s: stale %v, changed %v; want %v, %v", st.name, w.stale, w.schedule.dirty, st.stale, st.changed)
		}
		if !w.stale {
			e.s.captureChanged(time.Now(), causeActivation, true)
		}
	}
	e.s.forget(h.id)
}

// TestHiddenPaused checks K5 of specs/016-unviewable-thumbnails: a window
// hidden with a change while the snapshots are paused gets no snapshot while
// paused, its pixmap held, and its last one at the first turn of the loop
// once the pause ends; another, hidden alike, at an activation's capture of
// the windows changed, paused as the activation is.
func TestHiddenPaused(t *testing.T) {
	e := newHiddenEnv(t)
	v32 := visualOf(e.screen, 32)
	if v32 == 0 {
		t.Skip("no visual of depth 32")
	}
	a := e.newWindow(t, image.Pt(-4000, -4000), 32, v32, 30, true)
	b := e.newWindow(t, image.Pt(-3980, -4000), 24, 0, 31, true)
	wa := e.s.windows[a.id]
	beforeA, beforeB := e.gen(a), e.gen(b)

	e.s.Pause(true)
	e.draw(t, a)
	e.draw(t, b)
	e.hide(t, a, false)
	e.hide(t, b, false)
	e.pump(t)
	e.s.tick(time.Now().Add(2 * time.Second))
	if e.gen(a) != beforeA || e.gen(b) != beforeB {
		t.Error("a snapshot while paused")
	}
	if wa.pixmap == 0 || wa.bound == nil {
		t.Error("the pixmap of the window hidden not held while paused")
	}

	// The activation, paused
	e.s.captureChanged(time.Now(), causeActivation, true)
	e.checkShown(t, "at an activation", a, beforeA, true)
	e.checkShown(t, "at an activation, from its frame", b, beforeB, false)

	// Hidden again with a change, while paused; the pause ends
	xproto.MapWindow(e.wm, a.frame)
	e.wm.Sync()
	e.pump(t)
	e.s.captureChanged(time.Now(), causeActivation, true) // shown: captured as it becomes viewable
	beforeA = e.gen(a)
	e.draw(t, a)
	e.hide(t, a, false)
	e.pump(t)
	e.s.tick(time.Now())
	if e.gen(a) != beforeA {
		t.Error("a snapshot while paused")
	}
	e.s.Pause(false)
	if !e.dueNow() {
		t.Error("not due at once when the pause ends")
	}
	e.s.tick(time.Now())
	e.checkShown(t, "when the pause ends", a, beforeA, true)
	e.s.forget(a.id)
	e.s.forget(b.id)
}
