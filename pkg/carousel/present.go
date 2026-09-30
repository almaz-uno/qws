package carousel

import (
	"image"

	"github.com/jezek/xgb/xproto"
)

// Presenter shows finished frames in the overlay window. Frames are always
// drawn by the CPU renderer; the presenter only decides how they reach the
// window (specs/001-rendering-speed).
type Presenter interface {
	// VisualID is the visual the overlay window must be created with; 0 lets
	// the window choose an ARGB visual itself
	VisualID() xproto.Visualid

	// Bind attaches the presenter to a newly created overlay window
	Bind(w *Window) error

	// Present shows the frame in the bound window and returns once the frame
	// is in the window's drawable
	Present(img *image.RGBA) error

	// Refresh shows the last presented frame again, without the frame itself;
	// false when the bound window has none yet
	Refresh() (bool, error)

	// Close releases the presenter's resources
	Close()
}

// x11Presenter sends frames to the window with PutImage through its pixmap
type x11Presenter struct {
	window    *Window
	presented bool // the window's pixmap holds a frame
}

func (p *x11Presenter) VisualID() xproto.Visualid { return 0 }

func (p *x11Presenter) Bind(w *Window) error {
	p.window = w
	p.presented = false
	return nil
}

func (p *x11Presenter) Present(img *image.RGBA) error {
	err := p.window.DrawImage(img)
	p.presented = err == nil
	return err
}

func (p *x11Presenter) Refresh() (bool, error) {
	if !p.presented {
		return false, nil
	}
	return true, p.window.Refresh()
}

func (p *x11Presenter) Close() {}
