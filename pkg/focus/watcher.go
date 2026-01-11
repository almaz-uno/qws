package focus

import (
	"fmt"
	"image"
	"sync"

	"github.com/almaz-uno/qws/pkg/composite"
	"github.com/almaz-uno/qws/pkg/mru"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// Watcher tracks focus changes and maintains MRU list.
type Watcher struct {
	conn            *xgb.Conn
	root            xproto.Window
	netActiveWindow xproto.Atom
	mru             *mru.MRUList
	switcherWindow  xproto.Window // our switcher window to ignore
	capturer        *composite.Capturer
	thumbnails      map[xproto.Window]image.Image
	thumbMutex      sync.RWMutex
}

// NewWatcher creates a new focus watcher.
// It subscribes to PropertyNotify events on the root window to track _NET_ACTIVE_WINDOW changes.
func NewWatcher(conn *xgb.Conn, root xproto.Window, mru *mru.MRUList, capturer *composite.Capturer) (*Watcher, error) {
	// Get _NET_ACTIVE_WINDOW atom
	atomReply, err := xproto.InternAtom(conn, false,
		uint16(len("_NET_ACTIVE_WINDOW")),
		"_NET_ACTIVE_WINDOW").Reply()
	if err != nil {
		return nil, fmt.Errorf("failed to get _NET_ACTIVE_WINDOW atom: %w", err)
	}

	fw := &Watcher{
		conn:            conn,
		root:            root,
		netActiveWindow: atomReply.Atom,
		mru:             mru,
		capturer:        capturer,
		thumbnails:      make(map[xproto.Window]image.Image),
	}

	// Subscribe to PropertyChange events on root window
	mask := xproto.EventMaskPropertyChange
	if err := xproto.ChangeWindowAttributesChecked(
		conn,
		root,
		xproto.CwEventMask,
		[]uint32{uint32(mask)},
	).Check(); err != nil {
		return nil, fmt.Errorf("failed to subscribe to PropertyChange events: %w", err)
	}

	// Initialize MRU with current active window
	if activeWin, err := fw.GetActiveWindow(); err == nil && activeWin != 0 {
		mru.Touch(activeWin)
	}

	return fw, nil
}

// SetSwitcherWindow sets the window ID of our switcher window.
// This window will be ignored in focus tracking.
func (fw *Watcher) SetSwitcherWindow(window xproto.Window) {
	fw.switcherWindow = window
}

// HandlePropertyNotify handles PropertyNotify events.
// It should be called from the main event loop when a PropertyNotify event is received.
func (fw *Watcher) HandlePropertyNotify(e xproto.PropertyNotifyEvent) {
	// Only interested in _NET_ACTIVE_WINDOW changes on root window
	if e.Window != fw.root || e.Atom != fw.netActiveWindow {
		return
	}

	// Get the new active window
	activeWin, err := fw.GetActiveWindow()
	if err != nil {
		return
	}

	// Ignore our switcher window, root window, or no window
	if activeWin == fw.switcherWindow || activeWin == 0 || activeWin == fw.root {
		return
	}

	// Update MRU list
	fw.mru.Touch(activeWin)

	// Capture thumbnail for the newly focused window
	if fw.capturer != nil {
		go fw.captureThumbnail(activeWin)
	}
}

// HandleFocusIn handles FocusIn events as a fallback.
// This is used for WMs that don't properly support _NET_ACTIVE_WINDOW.
func (fw *Watcher) HandleFocusIn(e xproto.FocusInEvent) {
	// Ignore our switcher window and root
	if e.Event == fw.switcherWindow || e.Event == fw.root || e.Event == 0 {
		return
	}

	// Update MRU list
	fw.mru.Touch(e.Event)
}

// GetActiveWindow returns the currently active window via _NET_ACTIVE_WINDOW.
func (fw *Watcher) GetActiveWindow() (xproto.Window, error) {
	prop, err := xproto.GetProperty(fw.conn, false, fw.root,
		fw.netActiveWindow,
		xproto.AtomWindow,
		0,
		1, // only need first window
	).Reply()
	if err != nil {
		return 0, fmt.Errorf("failed to read _NET_ACTIVE_WINDOW: %w", err)
	}

	if prop.ValueLen == 0 {
		return 0, nil
	}

	// Parse window ID from property value
	activeWin := xproto.Window(
		uint32(prop.Value[0]) |
			uint32(prop.Value[1])<<8 |
			uint32(prop.Value[2])<<16 |
			uint32(prop.Value[3])<<24,
	)

	return activeWin, nil
}

// SubscribeToFocusEvents subscribes to FocusIn events for fallback focus tracking.
// This should be called for each client window to track focus changes on WMs
// that don't properly support _NET_ACTIVE_WINDOW PropertyNotify events.
func (fw *Watcher) SubscribeToFocusEvents(window xproto.Window) error {
	// Get current event mask
	attrs, err := xproto.GetWindowAttributes(fw.conn, window).Reply()
	if err != nil {
		return fmt.Errorf("failed to get window attributes: %w", err)
	}

	// Add FocusChange to event mask
	newMask := uint32(attrs.YourEventMask) | uint32(xproto.EventMaskFocusChange)

	return xproto.ChangeWindowAttributesChecked(
		fw.conn,
		window,
		xproto.CwEventMask,
		[]uint32{newMask},
	).Check()
}

// captureThumbnail captures and caches a thumbnail for the given window
func (fw *Watcher) captureThumbnail(window xproto.Window) {
	// Capture thumbnail (always refresh to get latest window content)
	img, err := fw.capturer.CaptureWindow(window, 512, 512)
	if err != nil {
		return
	}

	// Cache thumbnail
	fw.thumbMutex.Lock()
	fw.thumbnails[window] = img
	fw.thumbMutex.Unlock()
}

// GetThumbnail returns cached thumbnail for the given window
func (fw *Watcher) GetThumbnail(window xproto.Window) (image.Image, bool) {
	fw.thumbMutex.RLock()
	defer fw.thumbMutex.RUnlock()
	img, ok := fw.thumbnails[window]
	return img, ok
}

// ClearThumbnail removes cached thumbnail for the given window
func (fw *Watcher) ClearThumbnail(window xproto.Window) {
	fw.thumbMutex.Lock()
	defer fw.thumbMutex.Unlock()
	delete(fw.thumbnails, window)
}
