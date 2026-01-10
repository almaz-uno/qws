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

	if version.MajorVersion < 1 || (version.MajorVersion == 0 && version.MinorVersion < 2) {
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

	// Create a pixmap ID for NameWindowPixmap
	pixmapID, err := xproto.NewPixmapId(c.conn)
	if err != nil {
		return nil, fmt.Errorf("failed to create pixmap ID: %w", err)
	}

	// Get window pixmap via XComposite
	// Note: This requires the window to be redirected by a compositor
	if err := composite.NameWindowPixmapChecked(c.conn, window, pixmapID).Check(); err != nil {
		return nil, fmt.Errorf("failed to get window pixmap: %w", err)
	}
	defer xproto.FreePixmap(c.conn, pixmapID)

	// Get image data from pixmap
	img, err := xproto.GetImage(c.conn,
		xproto.ImageFormatZPixmap,
		xproto.Drawable(pixmapID),
		0, 0,
		uint16(width), uint16(height),
		^uint32(0), // all planes
	).Reply()
	if err != nil {
		return nil, fmt.Errorf("failed to get image data: %w", err)
	}

	// Convert to image.Image
	rgba := image.NewRGBA(image.Rect(0, 0, width, height))

	// Parse pixel data based on depth
	depth := geom.Depth
	if depth == 24 || depth == 32 {
		// BGRA or BGR format (most common)
		bytesPerPixel := int(depth) / 8
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				srcOffset := (y*width + x) * bytesPerPixel
				if srcOffset+2 < len(img.Data) {
					b := img.Data[srcOffset]
					g := img.Data[srcOffset+1]
					r := img.Data[srcOffset+2]
					a := uint8(255)
					if bytesPerPixel == 4 && srcOffset+3 < len(img.Data) {
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
	} else {
		return nil, fmt.Errorf("unsupported color depth: %d", depth)
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
