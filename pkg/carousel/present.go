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

// Animator is a presenter that composes frames of layers, for the animations
// of specs/007-animation. The GLX presenter is one; x11Presenter is not, and
// the switcher then changes its picture at once.
type Animator interface {
	// SetLayer makes img the layer id; its bounds are where it lies at rest
	SetLayer(id LayerID, img *image.RGBA) error

	// HasLayer reports whether the layer id is set
	HasLayer(id LayerID) bool

	// DropLayers forgets all layers
	DropLayers()

	// PresentScene draws the layer base over the whole window, then the items
	// over it in order, and presents the result with every channel scaled by
	// alpha
	PresentScene(base LayerID, items []SceneItem, alpha float64) error

	// PresentFaded presents img — or, when nil, the last frame presented —
	// with every channel scaled by alpha; at alpha 1 it is Present
	PresentFaded(img *image.RGBA, alpha float64) error

	// StageFrame uploads a part of img, about maxBytes, for PresentStaged:
	// the frame at rest goes to the GPU in the pauses of an animation, not
	// when it is due. It reports the bytes it uploaded and whether img is
	// staged in full. Frames presented meanwhile leave what is staged as it
	// is.
	StageFrame(img *image.RGBA, maxBytes int) (int, bool, error)

	// PresentStaged presents the frame staged in full with every channel
	// scaled by alpha; at alpha 1 as Present would
	PresentStaged(alpha float64) error
}

// LayerID names a layer of an Animator; 0 names none
type LayerID int

// Rect is a rectangle of the window in pixels, from its top-left corner
type Rect struct {
	X, Y, W, H float64
}

// SceneItem draws layer A at RectA and layer B at RectB, cross-faded: B with
// the weight WeightB, A with the rest; then scales the result by Alpha. A
// missing layer, or 0, is transparent.
type SceneItem struct {
	A, B         LayerID
	RectA, RectB Rect
	WeightB      float64
	Alpha        float64
}
