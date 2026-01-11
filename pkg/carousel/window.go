package carousel

import (
	"fmt"
	"image"
	"image/color"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// Window represents a graphical window for carousel display
type Window struct {
	conn   *xgb.Conn
	root   xproto.Window
	window xproto.Window
	pixmap xproto.Pixmap
	gc     xproto.Gcontext
	width  uint16
	height uint16
	depth  byte // Depth of the pixmap/window
}

// NewWindow creates a new X11 window for carousel display
func NewWindow(conn *xgb.Conn, root xproto.Window, width, height int) (*Window, error) {
	w := &Window{
		conn:   conn,
		root:   root,
		width:  uint16(width),
		height: uint16(height),
	}

	// Create window ID
	w.window, _ = xproto.NewWindowId(conn)

	// Get screen info
	setup := xproto.Setup(conn)
	screen := setup.DefaultScreen(conn)

	// Try to use ARGB visual for transparency
	visualID, depth := findARGBVisual(conn)
	if visualID == 0 {
		// Fallback to default visual
		visualID = screen.RootVisual
		depth = screen.RootDepth
	}
	w.depth = depth

	// Create colormap for ARGB visual
	colormap, _ := xproto.NewColormapId(conn)
	xproto.CreateColormap(conn, xproto.ColormapAllocNone, colormap, root, visualID)

	// Create window
	mask := uint32(xproto.CwBackPixel | xproto.CwBorderPixel | xproto.CwOverrideRedirect | xproto.CwEventMask | xproto.CwColormap)
	values := []uint32{
		0, // Background pixel (transparent)
		0, // Border pixel
		1, // Override redirect (no WM decorations)
		xproto.EventMaskKeyPress | xproto.EventMaskKeyRelease | xproto.EventMaskExposure,
		uint32(colormap),
	}

	err := xproto.CreateWindowChecked(
		conn,
		depth,
		w.window,
		root,
		// Fullscreen position
		0,
		0,
		w.width,
		w.height,
		0, // Border width
		xproto.WindowClassInputOutput,
		visualID,
		mask,
		values,
	).Check()
	if err != nil {
		return nil, fmt.Errorf("failed to create window: %w", err)
	}

	// Create pixmap for double buffering with same depth as window
	w.pixmap, _ = xproto.NewPixmapId(conn)
	err = xproto.CreatePixmapChecked(
		conn,
		w.depth,
		w.pixmap,
		xproto.Drawable(w.window),
		w.width,
		w.height,
	).Check()

	if err != nil {
		return nil, fmt.Errorf("failed to create pixmap: %w", err)
	}

	// Create graphics context
	w.gc, _ = xproto.NewGcontextId(conn)
	err = xproto.CreateGCChecked(
		conn,
		w.gc,
		xproto.Drawable(w.pixmap),
		xproto.GcForeground|xproto.GcBackground,
		[]uint32{screen.BlackPixel, screen.WhitePixel},
	).Check()

	if err != nil {
		return nil, fmt.Errorf("failed to create GC: %w", err)
	}

	// Set window properties
	w.setWindowProperties()

	return w, nil
}

// setWindowProperties sets window manager properties
func (w *Window) setWindowProperties() {
	// Set window name
	xproto.ChangeProperty(
		w.conn,
		xproto.PropModeReplace,
		w.window,
		xproto.AtomWmName,
		xproto.AtomString,
		8,
		uint32(len("QWS - Window Switcher")),
		[]byte("QWS - Window Switcher"),
	)

	// Set window class
	xproto.ChangeProperty(
		w.conn,
		xproto.PropModeReplace,
		w.window,
		xproto.AtomWmClass,
		xproto.AtomString,
		8,
		uint32(len("qws\x00QWS")),
		[]byte("qws\x00QWS"),
	)

	// Set _NET_WM_WINDOW_TYPE to _NET_WM_WINDOW_TYPE_DIALOG
	typeAtom, err := getAtom(w.conn, "_NET_WM_WINDOW_TYPE")
	if err == nil {
		dialogAtom, err := getAtom(w.conn, "_NET_WM_WINDOW_TYPE_DIALOG")
		if err == nil {
			xproto.ChangeProperty(
				w.conn,
				xproto.PropModeReplace,
				w.window,
				typeAtom,
				xproto.AtomAtom,
				32,
				1,
				[]byte{
					byte(dialogAtom), byte(dialogAtom >> 8),
					byte(dialogAtom >> 16), byte(dialogAtom >> 24),
				},
			)
		}
	}

	// Set _NET_WM_STATE to _NET_WM_STATE_ABOVE (always on top)
	stateAtom, err := getAtom(w.conn, "_NET_WM_STATE")
	if err == nil {
		aboveAtom, err := getAtom(w.conn, "_NET_WM_STATE_ABOVE")
		if err == nil {
			xproto.ChangeProperty(
				w.conn,
				xproto.PropModeReplace,
				w.window,
				stateAtom,
				xproto.AtomAtom,
				32,
				1,
				[]byte{
					byte(aboveAtom), byte(aboveAtom >> 8),
					byte(aboveAtom >> 16), byte(aboveAtom >> 24),
				},
			)
		}
	}
}

// getAtom retrieves or creates an atom by name
func getAtom(conn *xgb.Conn, name string) (xproto.Atom, error) {
	reply, err := xproto.InternAtom(conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0, err
	}
	return reply.Atom, nil
}

// Show makes the window visible
func (w *Window) Show() error {
	err := xproto.MapWindowChecked(w.conn, w.window).Check()
	if err != nil {
		return fmt.Errorf("failed to map window: %w", err)
	}

	// Raise window to top
	err = xproto.ConfigureWindowChecked(
		w.conn,
		w.window,
		xproto.ConfigWindowStackMode,
		[]uint32{xproto.StackModeAbove},
	).Check()

	w.conn.Sync()
	return nil
}

// Hide makes the window invisible
func (w *Window) Hide() error {
	return xproto.UnmapWindowChecked(w.conn, w.window).Check()
}

// Close destroys the window and frees resources
func (w *Window) Close() error {
	if w.gc != 0 {
		xproto.FreeGC(w.conn, w.gc)
	}
	if w.pixmap != 0 {
		xproto.FreePixmap(w.conn, w.pixmap)
	}
	if w.window != 0 {
		xproto.DestroyWindow(w.conn, w.window)
	}
	w.conn.Sync()
	return nil
}

// DrawImage renders an image to the window
func (w *Window) DrawImage(img image.Image) error {
	if img == nil {
		return fmt.Errorf("image is nil")
	}

	// Convert image to raw bytes (BGRA format for X11)
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Ensure image fits in window
	if width > int(w.width) || height > int(w.height) {
		return fmt.Errorf("image too large: %dx%d (window: %dx%d)", width, height, w.width, w.height)
	}

	// Split image into horizontal strips to avoid X11 request size limits
	// X11 typically has a ~256KB limit per request
	const maxBytesPerRequest = 200000 // Conservative limit
	bytesPerRow := width * 4
	maxRowsPerRequest := maxBytesPerRequest / bytesPerRow
	if maxRowsPerRequest < 1 {
		maxRowsPerRequest = 1
	}

	// Process image in strips
	for startY := 0; startY < height; startY += maxRowsPerRequest {
		endY := startY + maxRowsPerRequest
		if endY > height {
			endY = height
		}
		stripHeight := endY - startY

		// Convert this strip to bytes (format depends on depth)
		var data []byte
		if w.depth == 32 {
			// 32-bit: BGRA format
			data = make([]byte, width*stripHeight*4)
			i := 0
			for y := bounds.Min.Y + startY; y < bounds.Min.Y+endY; y++ {
				for x := bounds.Min.X; x < bounds.Max.X; x++ {
					c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
					data[i] = c.B
					data[i+1] = c.G
					data[i+2] = c.R
					data[i+3] = c.A
					i += 4
				}
			}
		} else {
			// 24-bit: BGR format (no alpha)
			data = make([]byte, width*stripHeight*4) // Still need padding
			i := 0
			for y := bounds.Min.Y + startY; y < bounds.Min.Y+endY; y++ {
				for x := bounds.Min.X; x < bounds.Max.X; x++ {
					c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
					data[i] = c.B
					data[i+1] = c.G
					data[i+2] = c.R
					data[i+3] = 0 // Padding byte
					i += 4
				}
			}
		}

		// Put this strip to pixmap
		err := xproto.PutImageChecked(
			w.conn,
			xproto.ImageFormatZPixmap,
			xproto.Drawable(w.pixmap),
			w.gc,
			uint16(width),
			uint16(stripHeight),
			0, int16(startY), // dst x, y
			0,       // left pad
			w.depth, // Use actual depth
			data,
		).Check()
		if err != nil {
			return fmt.Errorf("failed to put image strip at y=%d: %w", startY, err)
		}
	}

	// Copy pixmap to window
	err := xproto.CopyAreaChecked(
		w.conn,
		xproto.Drawable(w.pixmap),
		xproto.Drawable(w.window),
		w.gc,
		0, 0, // src x, y
		0, 0, // dst x, y
		w.width,
		w.height,
	).Check()
	if err != nil {
		return fmt.Errorf("failed to copy area: %w", err)
	}

	w.conn.Sync()
	return nil
}

// GetWindowID returns the X11 window ID
func (w *Window) GetWindowID() xproto.Window {
	return w.window
}

// SetGrabKeys sets up key grabbing for the window
func (w *Window) SetGrabKeys() error {
	// Grab Escape key
	err := xproto.GrabKeyChecked(
		w.conn,
		false, // owner events
		w.window,
		0, // modifiers
		9, // keycode for Escape
		xproto.GrabModeAsync,
		xproto.GrabModeAsync,
	).Check()
	if err != nil {
		return fmt.Errorf("failed to grab Escape key: %w", err)
	}

	// Grab Left/Right arrow keys
	leftArrow := xproto.Keycode(113)
	rightArrow := xproto.Keycode(114)

	xproto.GrabKey(w.conn, false, w.window, 0, leftArrow, xproto.GrabModeAsync, xproto.GrabModeAsync)
	xproto.GrabKey(w.conn, false, w.window, 0, rightArrow, xproto.GrabModeAsync, xproto.GrabModeAsync)

	// Grab Tab key
	tabKey := xproto.Keycode(23)
	xproto.GrabKey(w.conn, false, w.window, 0, tabKey, xproto.GrabModeAsync, xproto.GrabModeAsync)

	// Grab Return key
	returnKey := xproto.Keycode(36)
	xproto.GrabKey(w.conn, false, w.window, 0, returnKey, xproto.GrabModeAsync, xproto.GrabModeAsync)

	w.conn.Sync()
	return nil
}

// findARGBVisual finds a 32-bit ARGB visual for transparency support
func findARGBVisual(conn *xgb.Conn) (xproto.Visualid, byte) {
	setup := xproto.Setup(conn)
	screen := setup.DefaultScreen(conn)

	// Look for a 32-bit depth visual
	for _, depth := range screen.AllowedDepths {
		if depth.Depth == 32 {
			// Return first visual with 32-bit depth
			if len(depth.Visuals) > 0 {
				return depth.Visuals[0].VisualId, 32
			}
		}
	}

	// No ARGB visual found
	return 0, 0
}
