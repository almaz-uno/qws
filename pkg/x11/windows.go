package x11

import (
	"fmt"
	"image"

	"github.com/jezek/xgb/xproto"
)

// WindowInfo contains information about a window
type WindowInfo struct {
	ID      xproto.Window
	Name    string
	Icon    image.Image // Window icon from _NET_WM_ICON
	Preview image.Image // Window thumbnail (to be implemented)
}

// GetClientList retrieves the list of client windows via EWMH _NET_CLIENT_LIST
func (c *Connection) GetClientList() ([]xproto.Window, error) {
	// Get _NET_CLIENT_LIST atom
	atom, err := c.InternAtom("_NET_CLIENT_LIST", true)
	if err != nil {
		return nil, err
	}

	// Read property
	prop, err := xproto.GetProperty(c.Conn, false, c.Root,
		atom,              // property
		xproto.AtomWindow, // type
		0,                 // offset
		^uint32(0),        // length (maximum)
	).Reply()
	if err != nil {
		return nil, fmt.Errorf("failed to read _NET_CLIENT_LIST: %w", err)
	}

	if prop.ValueLen == 0 {
		return []xproto.Window{}, nil
	}

	// Convert bytes to []Window
	windows := make([]xproto.Window, prop.ValueLen)
	for i := uint32(0); i < prop.ValueLen; i++ {
		windows[i] = xproto.Window(
			uint32(prop.Value[i*4]) |
				uint32(prop.Value[i*4+1])<<8 |
				uint32(prop.Value[i*4+2])<<16 |
				uint32(prop.Value[i*4+3])<<24,
		)
	}
	return windows, nil
}

// GetWindowName retrieves window name via _NET_WM_NAME (UTF-8) or WM_NAME (fallback)
func (c *Connection) GetWindowName(window xproto.Window) (string, error) {
	// Try _NET_WM_NAME (UTF-8) first
	netWmName, err := c.InternAtom("_NET_WM_NAME", true)
	if err == nil {
		utf8String, _ := c.InternAtom("UTF8_STRING", true)
		prop, err := xproto.GetProperty(c.Conn, false, window,
			netWmName,
			utf8String,
			0,
			^uint32(0),
		).Reply()
		if err == nil && prop.ValueLen > 0 {
			return string(prop.Value), nil
		}
	}

	// Fallback: WM_NAME
	prop, err := xproto.GetProperty(c.Conn, false, window,
		xproto.AtomWmName,
		xproto.AtomString,
		0,
		^uint32(0),
	).Reply()
	if err != nil {
		return "", fmt.Errorf("failed to get window name: %w", err)
	}

	if prop.ValueLen == 0 {
		return fmt.Sprintf("<unnamed 0x%x>", window), nil
	}

	return string(prop.Value), nil
}

// GetWindowIcon retrieves window icon from _NET_WM_ICON property.
// Returns nil if the icon is not available.
func (c *Connection) GetWindowIcon(window xproto.Window) (image.Image, error) {
	// Get _NET_WM_ICON atom
	netWmIcon, err := c.InternAtom("_NET_WM_ICON", true)
	if err != nil {
		return nil, fmt.Errorf("failed to get _NET_WM_ICON atom: %w", err)
	}

	// Read property
	prop, err := xproto.GetProperty(c.Conn, false, window,
		netWmIcon,
		xproto.AtomCardinal,
		0,
		^uint32(0), // read all
	).Reply()
	if err != nil || prop.ValueLen == 0 {
		return nil, nil // no icon available
	}

	// Parse icon data
	// Format: width, height, ARGB pixels...
	data := make([]uint32, prop.ValueLen)
	for i := uint32(0); i < prop.ValueLen; i++ {
		data[i] = uint32(prop.Value[i*4]) |
			uint32(prop.Value[i*4+1])<<8 |
			uint32(prop.Value[i*4+2])<<16 |
			uint32(prop.Value[i*4+3])<<24
	}

	if len(data) < 2 {
		return nil, nil // invalid icon data
	}

	width := int(data[0])
	height := int(data[1])

	if width <= 0 || height <= 0 || len(data) < 2+width*height {
		return nil, nil // invalid dimensions or insufficient data
	}

	// Create RGBA image
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Copy pixel data (ARGB format)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			pixel := data[2+y*width+x]
			a := uint8((pixel >> 24) & 0xFF)
			r := uint8((pixel >> 16) & 0xFF)
			g := uint8((pixel >> 8) & 0xFF)
			b := uint8(pixel & 0xFF)

			img.SetRGBA(x, y, image.NewRGBA(image.Rect(0, 0, 1, 1)).RGBAAt(0, 0))
			offset := img.PixOffset(x, y)
			img.Pix[offset+0] = r
			img.Pix[offset+1] = g
			img.Pix[offset+2] = b
			img.Pix[offset+3] = a
		}
	}

	return img, nil
}

// GetWindowList retrieves list of windows with names
func (c *Connection) GetWindowList() ([]WindowInfo, error) {
	windows, err := c.GetClientList()
	if err != nil {
		return nil, err
	}

	result := make([]WindowInfo, 0, len(windows))
	for _, win := range windows {
		name, err := c.GetWindowName(win)
		if err != nil {
			// Skip windows for which we couldn't get the name
			continue
		}

		// Try to get window icon (ignore errors)
		icon, _ := c.GetWindowIcon(win)

		result = append(result, WindowInfo{
			ID:   win,
			Name: name,
			Icon: icon,
		})
	}

	return result, nil
}

// SortWindowsByMRU sorts a list of WindowInfo by MRU order.
// Windows not in the MRU list are placed at the end.
func SortWindowsByMRU(windows []WindowInfo, mruOrder []xproto.Window) []WindowInfo {
	// Create a map for quick lookup of MRU position
	mruPos := make(map[xproto.Window]int)
	for i, win := range mruOrder {
		mruPos[win] = i
	}

	// Sort windows by MRU position
	sorted := make([]WindowInfo, len(windows))
	copy(sorted, windows)

	// Custom sort: MRU windows first, then others
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			posI, inMRUI := mruPos[sorted[i].ID]
			posJ, inMRUJ := mruPos[sorted[j].ID]

			// Both in MRU: compare positions
			if inMRUI && inMRUJ {
				if posI > posJ {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			} else if !inMRUI && inMRUJ {
				// Only J in MRU: move J before I
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
			// If only I in MRU or neither in MRU: keep order
		}
	}

	return sorted
}

// ActivateWindow activates the specified window via _NET_ACTIVE_WINDOW
func (c *Connection) ActivateWindow(window xproto.Window) error {
	netActiveWindow, err := c.InternAtom("_NET_ACTIVE_WINDOW", false)
	if err != nil {
		return err
	}

	// Create ClientMessage event
	event := xproto.ClientMessageEvent{
		Format: 32,
		Window: window,
		Type:   netActiveWindow,
		Data: xproto.ClientMessageDataUnionData32New([]uint32{
			2, // source indication: 2 = pager
			0, // timestamp
			0, // requestor's currently active window
			0, 0,
		}),
	}

	// Send event to root window
	mask := xproto.EventMaskSubstructureRedirect | xproto.EventMaskSubstructureNotify
	return xproto.SendEventChecked(c.Conn, false, c.Root, uint32(mask), string(event.Bytes())).Check()
}
