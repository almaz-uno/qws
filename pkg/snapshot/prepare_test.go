package snapshot

import (
	"bytes"
	"errors"
	"image"
	"testing"

	"github.com/almaz-uno/qws/pkg/glx"
	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/jezek/xgb/xproto"
)

// TestPrepareLive checks K2 of specs/033-texture-warmup: told of the live
// thumbnails, a snapshot of a window taken from its frame — of any cause but
// an activation's; the windows of the tests are captured with "test" —
// makes its scaled pixmap of the thumbnail's size and binds it; the window's
// first live pass binds none, and its picture is, byte for byte, that of a
// pass whose pixmap the pass made; a snapshot of an activation makes none;
// not told, none, and the pass makes it, as before; at another size, the
// next snapshot makes it anew; a pixmap that does not bind at a snapshot
// keeps the snapshot and turns the passes from frames off. The windows are
// off the screen.
func TestPrepareLive(t *testing.T) {
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
	tw, th := thumbSize(size.X, size.Y)
	picture := windowImage(size.X, size.Y, 4)

	// Not told: the snapshot makes none, the first pass makes it
	plain, plainChild := frameWindow(t, s, conn, image.Pt(-3000, -3000), size, 0)
	defer s.forget(plain.id)
	if plain.scaled != nil || len(bound) != 0 {
		t.Fatalf("not told: scaled pixmap %v, %d bound at the snapshot; want none", plain.scaled != nil, len(bound))
	}
	fillDrawable(t, conn, xproto.Drawable(plainChild), 24, picture)
	want, ok := livePicture(t, s, plain)
	if !ok || len(bound) != 1 || plain.scaled == nil || bound[0] != plain.scaled.pixmap {
		t.Fatalf("not told: pass %v, %d bound; want the pass to bind its pixmap", ok, len(bound))
	}

	// Told: the snapshot makes and binds it, the first pass binds none
	s.PrepareLive()
	w, child := frameWindow(t, s, conn, image.Pt(-5000, -3000), size, 0)
	defer s.forget(w.id)
	sp := w.scaled
	if sp == nil || sp.bound == nil || len(bound) != 2 || bound[1] != sp.pixmap || sp.width != tw || sp.height != th {
		t.Fatalf("told: scaled pixmap %v, %d bound; want one of %d×%d bound at the snapshot", sp, len(bound), tw, th)
	}
	fillDrawable(t, conn, xproto.Drawable(child), 24, picture)
	got, ok := livePicture(t, s, w)
	if !ok {
		t.Fatal("told: no pass")
	}
	if len(bound) != 2 || w.scaled != sp {
		t.Errorf("told: %d bound after the first pass, the pixmap kept %v; want none bound by the pass", len(bound), w.scaled == sp)
	}
	if !bytes.Equal(got.Pix, want.Pix) {
		mean, worst := difference(got, want)
		t.Errorf("told: the picture of the first pass differs from that of a pass that made its pixmap: mean %.3f, worst %d", mean, worst)
	}

	// The snapshot of an activation makes none
	s.dropChain(w)
	s.dropScaled(w)
	s.capture(w, causeActivation)
	if w.scaled != nil || len(bound) != 2 {
		t.Errorf("activation: scaled pixmap %v, %d bound; want none made", w.scaled != nil, len(bound))
	}

	// At another size, made anew at the next snapshot
	s.capture(w, causeChange)
	small := image.Pt(1000, 500)
	if err := xproto.ConfigureWindowChecked(conn, w.id, xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{uint32(small.X), uint32(small.Y)}).Check(); err != nil {
		t.Fatal(err)
	}
	conn.Sync()
	w.stale = true // as the ConfigureNotify of the new size makes it
	s.capture(w, causeChange)
	sw, sh := thumbSize(small.X, small.Y)
	if sp := w.scaled; sp == nil || sp.width != sw || sp.height != sh || len(bound) != 4 {
		t.Errorf("another size: scaled pixmap %v, %d bound; want one of %d×%d made at the snapshot", sp, len(bound), sw, sh)
	}

	// A pixmap that does not bind: the snapshot kept, no pass from a frame
	bindScaled = func(*glx.Offscreen, xproto.Pixmap) (*glx.TexturePixmap, error) {
		return nil, errors.New("no bind in the test")
	}
	other, _ := frameWindow(t, s, conn, image.Pt(-3000, -1800), image.Pt(800, 500), 0)
	defer s.forget(other.id)
	if other.scaled != nil || !s.noScaled {
		t.Errorf("no bind: scaled pixmap %v, passes from frames off %v; want none and off", other.scaled != nil, s.noScaled)
	}
	if _, ok := s.Thumbnail(other.id); !ok {
		t.Error("no bind: the snapshot of the window lost")
	}
	if _, ok := livePicture(t, s, other); ok {
		t.Error("no bind: a pass made from a frame")
	}
}
