package composite

import (
	"fmt"
	"image"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/composite"
	"github.com/jezek/xgb/xproto"
)

// Capturer captures window thumbnails using XComposite extension.
type Capturer struct {
	conn *xgb.Conn
	root xproto.Window
}

// NewCapturer creates a new thumbnail capturer.
// It initializes the XComposite extension.
func NewCapturer(conn *xgb.Conn, root xproto.Window) (*Capturer, error) {
	// Initialize composite extension
	if err := composite.Init(conn); err != nil {
		return nil, fmt.Errorf("failed to initialize Composite extension: %w", err)
	}

	// Query composite version
	version, err := composite.QueryVersion(conn, 0, 4).Reply()
	if err != nil {
		return nil, fmt.Errorf("failed to query Composite version: %w", err)
	}

	// Check version: need at least 0.2
	if version.MajorVersion == 0 && version.MinorVersion < 2 {
		return nil, fmt.Errorf("Composite extension version too old: %d.%d (need at least 0.2)",
			version.MajorVersion, version.MinorVersion)
	}

	return &Capturer{
		conn: conn,
		root: root,
	}, nil
}

// CaptureWindow captures a thumbnail of the specified window.
// Returns nil if the window cannot be captured (e.g., not redirected by compositor).
func (c *Capturer) CaptureWindow(window xproto.Window, maxWidth, maxHeight int) (image.Image, error) {
	// Skip root window or invalid windows
	if window == 0 || window == c.root {
		return nil, fmt.Errorf("cannot capture root or invalid window")
	}

	// Check if window exists and is mapped
	attrs, err := xproto.GetWindowAttributes(c.conn, window).Reply()
	if err != nil {
		return nil, fmt.Errorf("failed to get window attributes: %w", err)
	}

	// Skip unmapped windows
	if attrs.MapState != xproto.MapStateViewable {
		return nil, fmt.Errorf("window is not viewable (map_state=%d)", attrs.MapState)
	}

	// Get window geometry
	geom, err := xproto.GetGeometry(c.conn, xproto.Drawable(window)).Reply()
	if err != nil {
		return nil, fmt.Errorf("failed to get window geometry: %w", err)
	}

	width := int(geom.Width)
	height := int(geom.Height)

	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid window dimensions: %dx%d", width, height)
	}

	// Calculate scaled dimensions while preserving aspect ratio
	scale := 1.0
	if width > maxWidth || height > maxHeight {
		scaleW := float64(maxWidth) / float64(width)
		scaleH := float64(maxHeight) / float64(height)
		if scaleW < scaleH {
			scale = scaleW
		} else {
			scale = scaleH
		}
	}

	scaledWidth := int(float64(width) * scale)
	scaledHeight := int(float64(height) * scale)

	// Try to get window pixmap via XComposite first
	pixmapID, err := xproto.NewPixmapId(c.conn)
	if err != nil {
		return nil, fmt.Errorf("failed to create pixmap ID: %w", err)
	}

	// Get window pixmap via XComposite
	// Note: This requires the window to be redirected by a compositor
	var img *xproto.GetImageReply
	if err := composite.NameWindowPixmapChecked(c.conn, window, pixmapID).Check(); err != nil {
		// Composite failed - try direct capture from window
		img, err = xproto.GetImage(c.conn,
			xproto.ImageFormatZPixmap,
			xproto.Drawable(window), // Direct from window, not pixmap
			0, 0,
			uint16(width), uint16(height),
			^uint32(0), // all planes
		).Reply()
		if err != nil {
			return nil, fmt.Errorf("failed to capture window directly: %w", err)
		}
	} else {
		defer xproto.FreePixmap(c.conn, pixmapID)

		// Get image data from pixmap
		img, err = xproto.GetImage(c.conn,
			xproto.ImageFormatZPixmap,
			xproto.Drawable(pixmapID),
			0, 0,
			uint16(width), uint16(height),
			^uint32(0), // all planes
		).Reply()
		if err != nil {
			return nil, fmt.Errorf("failed to get image data: %w", err)
		}
	}

	// Convert to image.Image
	rgba := image.NewRGBA(image.Rect(0, 0, width, height))

	// According to X11 documentation, ZPixmap format returns pixels with:
	// - bits_per_pixel field indicating bits per pixel (usually 32 for 24-bit depth)
	// - bytes_per_line indicating actual scanline width including padding
	// - byte_order indicating LSBFirst or MSBFirst

	// The GetImageReply from xgb doesn't expose all XImage fields,
	// so we need to infer the format from the data size and depth

	depth := img.Depth // Use depth from GetImageReply, not geometry
	dataSize := len(img.Data)

	// Calculate expected bytes per pixel
	// For depth 24, X usually uses 32 bits per pixel (4 bytes) with padding
	var bitsPerPixel int
	if depth == 24 {
		bitsPerPixel = 32 // X11 typically uses 32 bpp for 24-bit depth
	} else if depth == 32 {
		bitsPerPixel = 32
	} else {
		return nil, fmt.Errorf("unsupported color depth: %d", depth)
	}
	bytesPerPixel := bitsPerPixel / 8

	// Calculate bytes_per_line from actual data size
	// XGetImage returns data with scanline padding aligned to bitmap_pad (typically 32 bits)
	expectedMinSize := width * height * bytesPerPixel
	bytesPerLine := dataSize / height

	if dataSize < expectedMinSize {
		return nil, fmt.Errorf("insufficient image data: got %d bytes, expected at least %d", dataSize, expectedMinSize)
	}

	// Parse pixel data
	// X11 on little-endian systems typically uses BGRA byte order for 32bpp
	for y := 0; y < height; y++ {
		lineOffset := y * bytesPerLine
		for x := 0; x < width; x++ {
			srcOffset := lineOffset + x*bytesPerPixel
			if srcOffset+3 < len(img.Data) {
				// Try BGRA order (typical for X11 on little-endian)
				b := img.Data[srcOffset]
				g := img.Data[srcOffset+1]
				r := img.Data[srcOffset+2]
				a := uint8(255)
				if bytesPerPixel == 4 {
					a = img.Data[srcOffset+3]
				}

				dstOffset := rgba.PixOffset(x, y)
				rgba.Pix[dstOffset+0] = r
				rgba.Pix[dstOffset+1] = g
				rgba.Pix[dstOffset+2] = b
				rgba.Pix[dstOffset+3] = a
			}
		}
	}

	// Scale down if necessary
	if scale < 1.0 {
		scaled := image.NewRGBA(image.Rect(0, 0, scaledWidth, scaledHeight))
		// Simple nearest-neighbor scaling
		for y := 0; y < scaledHeight; y++ {
			for x := 0; x < scaledWidth; x++ {
				srcX := int(float64(x) / scale)
				srcY := int(float64(y) / scale)
				if srcX < width && srcY < height {
					scaled.Set(x, y, rgba.At(srcX, srcY))
				}
			}
		}
		return scaled, nil
	}

	return rgba, nil
}

// IsCompositorRunning checks if a compositor is running.
// This is a heuristic check by looking for _NET_WM_CM_S0 selection owner.
func (c *Capturer) IsCompositorRunning() bool {
	atom, err := xproto.InternAtom(c.conn, false,
		uint16(len("_NET_WM_CM_S0")),
		"_NET_WM_CM_S0").Reply()
	if err != nil {
		return false
	}

	owner, err := xproto.GetSelectionOwner(c.conn, atom.Atom).Reply()
	if err != nil {
		return false
	}

	return owner.Owner != 0
}
