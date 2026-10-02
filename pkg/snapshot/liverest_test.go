package snapshot

import (
	"image"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"github.com/almaz-uno/qws/pkg/glx"
	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/go-gl/gl/v4.6-core/gl"
	"github.com/jezek/xgb"
	xcomposite "github.com/jezek/xgb/composite"
	"github.com/jezek/xgb/xproto"
)

// TestLiveAtRest checks the live thumbnails of a switcher at rest
// (specs/020-live-thumbnails, K13), with the loop of the snapshotter as qws
// runs it: shown, presenting no frame but those the passes ask for by their
// ClientMessage, with a window changing every 5 ms, the passes keep coming at
// about the live interval, and each publication is followed by a frame that
// draws it. The windows of the test are children of a window of its own off
// the screen, whose children the X server redirects, and the snapshotter
// follows the windows listed on it: no window of the desktop is bound.
func TestLiveAtRest(t *testing.T) {
	conn, err := x11.NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	screen := xproto.Setup(conn).DefaultScreen(conn)
	parent := testWindow(t, conn, screen.Root, image.Rect(-5000, -5000, -3700, -4200), 0, 0)
	if err := xcomposite.RedirectSubwindowsChecked(conn, parent, xcomposite.RedirectAutomatic).Check(); err != nil {
		t.Skipf("no Composite: %v", err)
	}
	win := testWindow(t, conn, parent, image.Rect(0, 0, 1200, 700), 0, 0)

	sconn, err := x11.NewConn()
	if err != nil {
		t.Fatal(err)
	}
	s, err := start(sconn, parent, time.Second, "bilinear")
	if err != nil {
		t.Skipf("no snapshotter: %v", err)
	}
	defer s.Close()

	// Listed as the window manager lists its clients, and captured once
	name := "_NET_CLIENT_LIST"
	atom, err := xproto.InternAtom(conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]byte, 4)
	xgb.Put32(ids, uint32(win))
	if err := xproto.ChangePropertyChecked(conn, xproto.PropModeReplace, parent, atom.Atom,
		xproto.AtomWindow, 32, 1, ids).Check(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, ok := s.Thumbnail(win); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Skip("the window of the test not captured: no GPU path")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The switcher: its overlay, never mapped, to which the snapshotter sends
	// its ClientMessage, and a context sharing the snapshotter's on a display
	// and a thread of its own
	sel, err := x11.NewConn()
	if err != nil {
		t.Fatal(err)
	}
	defer sel.Close()
	overlay, err := xproto.NewWindowId(sel)
	if err != nil {
		t.Fatal(err)
	}
	if err := xproto.CreateWindowChecked(sel, 0, overlay, parent, 0, 0, 16, 16, 0,
		xproto.WindowClassInputOnly, 0, 0, nil).Check(); err != nil {
		t.Fatal(err)
	}
	defer xproto.DestroyWindow(sel, overlay)
	woken := make(chan struct{}, 64)
	go func() {
		for {
			ev, err := sel.WaitForEvent()
			if ev == nil && err == nil {
				return
			}
			if _, ok := ev.(xproto.ClientMessageEvent); ok {
				woken <- struct{}{}
			}
		}
	}()
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
	if err := ctx.MakeCurrent(uint32(offscreenWindow(t, sel, xproto.Setup(sel).DefaultScreen(sel), ctx.VisualID()))); err != nil {
		t.Fatal(err)
	}
	gl.PixelStorei(gl.PACK_ALIGNMENT, 1)

	// A frame as the selector presents one: the pictures taken, the window's
	// read, a fence after it for the snapshotter
	shown := []xproto.Window{win}
	var drawn uint64
	var pixels []byte
	frame := func() bool {
		pics := s.BeginFrame(shown)
		p, ok := pics[win]
		fresh := ok && p.Gen > drawn
		if fresh {
			drawn = p.Gen
			if len(pixels) < 4*p.Width*p.Height {
				pixels = make([]byte, 4*p.Width*p.Height)
			}
			gl.BindTexture(gl.TEXTURE_2D, p.Texture)
			gl.GetTexImage(gl.TEXTURE_2D, 0, gl.RGBA, gl.UNSIGNED_BYTE, unsafe.Pointer(&pixels[0]))
		}
		f := gl.FenceSync(gl.SYNC_GPU_COMMANDS_COMPLETE, 0)
		gl.Flush()
		if old := s.EndFrame(f); old != 0 {
			gl.DeleteSync(old)
		}
		return fresh
	}

	// Shown as qws shows it: paused, its first frame, then live
	s.Pause(true)
	defer s.Pause(false)
	frame()
	const interval = 33 * time.Millisecond
	s.SetLive(overlay, interval)
	defer s.SetLive(0, 0)

	// The window drawn anew every 5 ms, through a connection of its own
	draw, err := x11.NewConn()
	if err != nil {
		t.Fatal(err)
	}
	defer draw.Close()
	stop := make(chan struct{})
	drawing := make(chan struct{})
	go func() {
		defer close(drawing)
		gc, _ := xproto.NewGcontextId(draw)
		xproto.CreateGC(draw, gc, xproto.Drawable(win), 0, nil)
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for k := 0; ; k++ {
			select {
			case <-stop:
				return
			case <-tick.C:
			}
			xproto.ChangeGC(draw, gc, xproto.GcForeground, []uint32{uint32(0x203040 + k%2*0x404040)})
			xproto.PolyFillRectangle(draw, xproto.Drawable(win), gc, []xproto.Rectangle{{X: 0, Y: int16(k % 700), Width: 1200, Height: 20}})
			draw.Sync()
		}
	}()

	// At rest: a frame only when woken, no sooner than a refresh period
	// after the one before
	const length = 1500 * time.Millisecond
	end := time.Now().Add(length)
	frames, pictures := 0, 0
	var last time.Time
	for time.Now().Before(end) {
		select {
		case <-woken:
		case <-time.After(time.Until(end)):
			continue
		}
		if d := time.Until(last.Add(7 * time.Millisecond)); d > 0 {
			time.Sleep(d)
		}
		frames++
		if frame() {
			pictures++
		}
		last = time.Now()
	}
	close(stop)
	<-drawing

	// Some 45 passes in the time at the interval; a third of them at least,
	// for the load of a desktop in use
	if want := int(length / interval / 3); pictures < want {
		t.Errorf("%d pictures drawn in %v at rest, %d frames; want %d at least, a pass about every %v",
			pictures, length, frames, want, interval)
	}
	if frames > pictures+1 {
		t.Errorf("%d frames for %d pictures: a frame woken for no picture", frames, pictures)
	}
	t.Logf("%d pictures, %d frames in %v at rest", pictures, frames, length)
}
