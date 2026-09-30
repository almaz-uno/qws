package carousel

import (
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-gl/gl/v4.6-core/gl"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"golang.org/x/image/font/gofont/goregular"
)

// TestGLXPresenterMatchesCPU checks criterion K2 of specs/001-rendering-speed:
// for every scene of Σ, what the GLX presenter draws into the back buffer of
// the window equals the cpu frame byte for byte.
//
// The windows are mapped off the screen. Their pixels are owned, and so
// defined, only when a compositing manager redirects them, so the test needs
// one, besides an X display and GLX. All GL calls stay in this goroutine: the
// context is current on its thread only.
func TestGLXPresenterMatchesCPU(t *testing.T) {
	conn, err := xgb.NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	if !compositing(conn) {
		t.Skip("no compositing manager: the pixels of an off-screen window are undefined")
	}

	presenter, err := newGLXPresenter()
	if err != nil {
		t.Skipf("no GLX: %v", err)
	}
	defer presenter.Close()
	p := presenter.(*glxPresenter)

	goFont := filepath.Join(t.TempDir(), "goregular.ttf")
	if err := os.WriteFile(goFont, goregular.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	hostFont := fileDigest(fontFallback) != ""

	root := xproto.Setup(conn).DefaultScreen(conn).Root
	windows := map[image.Point]*Window{}
	defer func() {
		for _, w := range windows {
			w.Close()
		}
	}()

	var bound *Window
	for _, sc := range scenes() {
		fonts := []string{goFont}
		if sc.host {
			if !hostFont {
				t.Logf("%s: skipped, no %s", sc.name, fontFallback)
				continue
			}
			fonts = append(fonts, fontFallback)
		}

		// Scenes of one size follow each other in one window, so that all but
		// the first upload only the rows that changed
		size := image.Pt(sc.width, sc.height)
		w := windows[size]
		if w == nil {
			w, err = NewWindowAt(conn, root, -sc.width-100, 0, sc.width, sc.height, p.VisualID())
			if err != nil {
				t.Fatal(err)
			}
			windows[size] = w
			if err := w.Show(); err != nil {
				t.Fatal(err)
			}
		}
		if w != bound {
			if err := p.Bind(w); err != nil {
				t.Fatal(err)
			}
			bound = w
		}

		frame := drawScene(sc, fonts)
		if err := p.draw(frame); err != nil {
			t.Fatal(err)
		}
		got := p.readBack()
		if n := differentPixels(got, frame); n != 0 {
			t.Errorf("%s: %d of %d pixels differ from the cpu frame", sc.name, n, sc.width*sc.height)
		}
	}
}

// readBack reads the back buffer after draw, before the swap — the test hook
// of K2. GL returns rows bottom up; they are flipped to the frame's order.
func (p *glxPresenter) readBack() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, p.width, p.height))
	gl.ReadBuffer(gl.BACK)
	gl.PixelStorei(gl.PACK_ALIGNMENT, 1)
	gl.ReadPixels(0, 0, int32(p.width), int32(p.height), gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(img.Pix))

	row := make([]byte, img.Stride)
	for top, bottom := 0, p.height-1; top < bottom; top, bottom = top+1, bottom-1 {
		a := img.Pix[top*img.Stride : (top+1)*img.Stride]
		b := img.Pix[bottom*img.Stride : (bottom+1)*img.Stride]
		copy(row, a)
		copy(a, b)
		copy(b, row)
	}
	return img
}

// differentPixels counts the pixels in which two frames of one size differ
func differentPixels(a, b *image.RGBA) int {
	n := 0
	for i := 0; i+3 < len(a.Pix) && i+3 < len(b.Pix); i += 4 {
		if a.Pix[i] != b.Pix[i] || a.Pix[i+1] != b.Pix[i+1] || a.Pix[i+2] != b.Pix[i+2] || a.Pix[i+3] != b.Pix[i+3] {
			n++
		}
	}
	return n
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
