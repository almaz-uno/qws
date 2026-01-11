package composite

import (
	"fmt"
	"image"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/composite"
	"github.com/jezek/xgb/shm"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog/log"
	"golang.org/x/sys/unix"
)

// Capturer captures window thumbnails using XComposite extension.
type Capturer struct {
	conn         *xgb.Conn
	root         xproto.Window
	shmAvailable bool
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

	// Try to initialize SHM extension
	shmAvailable := false
	if err := shm.Init(conn); err == nil {
		// Query SHM version to verify it's available
		if _, err := shm.QueryVersion(conn).Reply(); err == nil {
			shmAvailable = true
			log.Debug().Msg("XSHM extension available")
		} else {
			log.Debug().Err(err).Msg("XSHM extension query failed")
		}
	} else {
		log.Debug().Err(err).Msg("XSHM extension unavailable")
	}

	return &Capturer{
		conn:         conn,
		root:         root,
		shmAvailable: shmAvailable,
	}, nil
}

// CaptureWindow captures a thumbnail of the specified window.
// Returns nil if the window cannot be captured (e.g., not redirected by compositor).
func (c *Capturer) CaptureWindow(window xproto.Window, maxWidth, maxHeight int) (image.Image, error) {
	// Skip root window or invalid windows
	if window == 0 || window == c.root {
		return nil, fmt.Errorf("cannot capture root or invalid window")
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

	// Define capture strategy chain
	type captureResult struct {
		img     *xproto.GetImageReply
		shmData []byte
		shmid   int
	}

	type captureFunc func() (*captureResult, error)

	// Build capture strategy chain
	strategies := []struct {
		name string
		fn   captureFunc
	}{}

	// Strategy 1: Try XComposite pixmap with XSHM
	pixmapID, err := xproto.NewPixmapId(c.conn)
	if err != nil {
		return nil, fmt.Errorf("failed to create pixmap ID: %w", err)
	}

	if err := composite.NameWindowPixmapChecked(c.conn, window, pixmapID).Check(); err == nil {
		// XComposite available - add pixmap-based strategies
		defer xproto.FreePixmap(c.conn, pixmapID)

		if c.shmAvailable {
			strategies = append(strategies, struct {
				name string
				fn   captureFunc
			}{"XComposite+XSHM", func() (*captureResult, error) {
				img, shmData, shmid, err := c.captureWithSHM(xproto.Drawable(pixmapID), width, height)
				if err != nil {
					return nil, err
				}
				return &captureResult{img, shmData, shmid}, nil
			}})
		}

		strategies = append(strategies, struct {
			name string
			fn   captureFunc
		}{"XComposite+XGetImage", func() (*captureResult, error) {
			img, err := xproto.GetImage(c.conn,
				xproto.ImageFormatZPixmap,
				xproto.Drawable(pixmapID),
				0, 0,
				uint16(width), uint16(height),
				^uint32(0),
			).Reply()
			if err != nil {
				return nil, err
			}
			return &captureResult{img: img}, nil
		}})
	} else {
		log.Debug().Err(err).Uint32("window", uint32(window)).Msg("XComposite unavailable")
	}

	// Strategy 2: Direct window capture with XSHM
	if c.shmAvailable {
		strategies = append(strategies, struct {
			name string
			fn   captureFunc
		}{"Direct+XSHM", func() (*captureResult, error) {
			img, shmData, shmid, err := c.captureWithSHM(xproto.Drawable(window), width, height)
			if err != nil {
				return nil, err
			}
			return &captureResult{img, shmData, shmid}, nil
		}})
	}

	// Strategy 3: Direct window capture with XGetImage (always available)
	strategies = append(strategies, struct {
		name string
		fn   captureFunc
	}{"Direct+XGetImage", func() (*captureResult, error) {
		img, err := xproto.GetImage(c.conn,
			xproto.ImageFormatZPixmap,
			xproto.Drawable(window),
			0, 0,
			uint16(width), uint16(height),
			^uint32(0),
		).Reply()
		if err != nil {
			return nil, err
		}
		return &captureResult{img: img}, nil
	}})

	// Execute strategy chain
	var result *captureResult
	for _, strategy := range strategies {
		res, err := strategy.fn()
		if err != nil {
			log.Debug().Err(err).Str("strategy", strategy.name).Msg("Capture strategy failed")
			continue
		}
		log.Debug().Str("strategy", strategy.name).Msg("Capture successful")
		result = res
		break
	}

	if result == nil {
		return nil, fmt.Errorf("all capture strategies failed")
	}

	img := result.img
	shmData := result.shmData
	shmid := result.shmid

	// Clean up shared memory if it was used
	if shmData != nil {
		defer c.cleanupSHM(shmData, shmid)
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
	switch depth {
	case 24, 32:
		bitsPerPixel = 32 // X11 typically uses 32 bpp for 24-bit depth
	default:
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
	// Note: We ignore alpha channel from X11 data and always set it to 255 (opaque)
	// because most windows don't use real transparency and have garbage/zero in alpha channel
	for y := 0; y < height; y++ {
		lineOffset := y * bytesPerLine
		for x := 0; x < width; x++ {
			srcOffset := lineOffset + x*bytesPerPixel
			if srcOffset+2 < len(img.Data) {
				// BGRA order (typical for X11 on little-endian)
				b := img.Data[srcOffset]
				g := img.Data[srcOffset+1]
				r := img.Data[srcOffset+2]
				// Always use opaque alpha - ignore X11 alpha channel
				a := uint8(255)

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

// captureWithSHM captures image using X Shared Memory extension for better performance.
// Returns GetImageReply, shared memory data slice, shmid, and error.
func (c *Capturer) captureWithSHM(drawable xproto.Drawable, width, height int) (*xproto.GetImageReply, []byte, int, error) {
	// Calculate required buffer size (assuming 32 bits per pixel)
	bytesPerPixel := 4
	imageSize := width * height * bytesPerPixel

	// Create shared memory segment
	shmid, err := unix.SysvShmGet(unix.IPC_PRIVATE, imageSize, unix.IPC_CREAT|0o600)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to create shared memory: %w", err)
	}

	// Attach shared memory
	shmData, err := unix.SysvShmAttach(shmid, 0, 0)
	if err != nil {
		unix.SysvShmCtl(shmid, unix.IPC_RMID, nil)
		return nil, nil, 0, fmt.Errorf("failed to attach shared memory: %w", err)
	}

	// Create SHM segment ID for X server
	seg, err := shm.NewSegId(c.conn)
	if err != nil {
		unix.SysvShmDetach(shmData)
		unix.SysvShmCtl(shmid, unix.IPC_RMID, nil)
		return nil, nil, 0, fmt.Errorf("failed to create SHM segment ID: %w", err)
	}

	// Attach SHM segment to X server
	if err := shm.AttachChecked(c.conn, seg, uint32(shmid), false).Check(); err != nil {
		unix.SysvShmDetach(shmData)
		unix.SysvShmCtl(shmid, unix.IPC_RMID, nil)
		return nil, nil, 0, fmt.Errorf("failed to attach SHM to X server: %w", err)
	}

	// Ensure we detach from X server when done
	defer shm.Detach(c.conn, seg)

	// Get image using SHM
	reply, err := shm.GetImage(c.conn,
		drawable,
		0, 0,
		uint16(width), uint16(height),
		^uint32(0), // all planes
		xproto.ImageFormatZPixmap,
		seg, 0, // offset 0 in shared memory
	).Reply()
	if err != nil {
		unix.SysvShmDetach(shmData)
		unix.SysvShmCtl(shmid, unix.IPC_RMID, nil)
		return nil, nil, 0, fmt.Errorf("failed to get SHM image: %w", err)
	}

	// Wait for X server to complete the operation
	c.conn.Sync()

	// Create a GetImageReply-like structure with data from shared memory
	// We need to copy the data because we'll detach the shared memory
	dataCopy := make([]byte, imageSize)
	copy(dataCopy, shmData[:imageSize])

	img := &xproto.GetImageReply{
		Depth:  reply.Depth,
		Visual: reply.Visual,
		Data:   dataCopy,
	}

	return img, shmData, shmid, nil
}

// cleanupSHM detaches and removes shared memory segment.
func (c *Capturer) cleanupSHM(shmData []byte, shmid int) {
	if shmData != nil {
		// Detach shared memory
		if err := unix.SysvShmDetach(shmData); err != nil {
			log.Debug().Err(err).Msg("Failed to detach shared memory")
		}
	}
	// Mark for removal (will be removed when all processes detach)
	if _, err := unix.SysvShmCtl(shmid, unix.IPC_RMID, nil); err != nil {
		log.Debug().Err(err).Msg("Failed to remove shared memory")
	}
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
