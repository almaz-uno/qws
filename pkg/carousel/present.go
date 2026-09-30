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

	// Close releases the presenter's resources
	Close()
}

// x11Presenter sends frames to the window with PutImage through its pixmap
type x11Presenter struct {
	window *Window
}

func (p *x11Presenter) VisualID() xproto.Visualid { return 0 }

func (p *x11Presenter) Bind(w *Window) error {
	p.window = w
	return nil
}

func (p *x11Presenter) Present(img *image.RGBA) error {
	return p.window.DrawImage(img)
}

func (p *x11Presenter) Close() {}
