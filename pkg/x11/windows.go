package x11

import (
	"fmt"

	"github.com/jezek/xgb/xproto"
)

// WindowInfo contains information about a window
type WindowInfo struct {
	ID   xproto.Window
	Name string
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
		result = append(result, WindowInfo{
			ID:   win,
			Name: name,
		})
	}

	return result, nil
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
