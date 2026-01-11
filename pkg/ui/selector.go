package ui

import (
	"fmt"
	"image"

	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// Selector provides a graphical carousel interface for window selection
type Selector struct {
	conn          *xgb.Conn
	root          xproto.Window
	windows       []x11.WindowInfo
	selectedIndex int
	window        *carousel.Window
	config        carousel.Config
	animOffset    float64
	animating     bool
	resultChan    chan *x11.WindowInfo
}

// NewSelector creates a new graphical window selector
func NewSelector(conn *xgb.Conn, root xproto.Window, windows []x11.WindowInfo) *Selector {
	return &Selector{
		conn:          conn,
		root:          root,
		windows:       windows,
		selectedIndex: 0,
		config:        carousel.DefaultConfig(),
		resultChan:    make(chan *x11.WindowInfo, 1),
	}
}

// UpdateWindows updates the window list, preserving the index
func (s *Selector) UpdateWindows(windows []x11.WindowInfo) {
	s.windows = windows
	// Reset index if it's out of bounds of the new list
	if s.selectedIndex >= len(windows) {
		s.selectedIndex = 0
	}
}

// Show displays the carousel UI and waits for user selection
// Returns the selected window or nil if cancelled
func (s *Selector) Show() (*x11.WindowInfo, error) {
	if len(s.windows) == 0 {
		return nil, fmt.Errorf("no available windows")
	}

	// Start with index 1 (second window in MRU) since Alt+Tab means "switch to next window"
	// Index 0 is the current active window
	if len(s.windows) > 1 {
		s.selectedIndex = 1
	} else {
		s.selectedIndex = 0
	}

	// Create window if it doesn't exist
	if s.window == nil {
		var err error
		s.window, err = carousel.NewWindow(s.conn, s.root, s.config.Width, s.config.Height)
		if err != nil {
			return nil, fmt.Errorf("failed to create window: %w", err)
		}
	}

	// Prepare thumbnails
	thumbnails := s.prepareThumbnails()

	// Show window
	if err := s.window.Show(); err != nil {
		return nil, fmt.Errorf("failed to show window: %w", err)
	}

	// Grab keyboard to receive all keyboard events
	xproto.GrabKeyboard(
		s.conn,
		false, // owner_events
		s.window.GetWindowID(),
		xproto.TimeCurrentTime,
		xproto.GrabModeAsync,
		xproto.GrabModeAsync,
	).Reply()
	defer xproto.UngrabKeyboard(s.conn, xproto.TimeCurrentTime)

	// Initial render
	s.render(thumbnails)

	// Event loop - wait for user input
	result := s.handleEventsSync(thumbnails)

	// Hide window
	s.window.Hide()

	return result, nil
}

// handleEventsSync processes keyboard events synchronously
func (s *Selector) handleEventsSync(thumbnails []image.Image) *x11.WindowInfo {
	for {
		event, err := s.conn.WaitForEvent()
		if err != nil {
			continue
		}

		switch e := event.(type) {
		case xproto.KeyPressEvent:
			if s.handleKeyPressSimple(e, thumbnails) {
				// ESC pressed
				return nil
			}

		case xproto.KeyReleaseEvent:
			// Check if Alt was released (keycode 64 = left Alt, 108 = right Alt)
			if e.Detail == 64 || e.Detail == 108 {
				// Return selected window when Alt is released
				if s.selectedIndex >= 0 && s.selectedIndex < len(s.windows) {
					return &s.windows[s.selectedIndex]
				}
				return nil
			}

		case xproto.ExposeEvent:
			if e.Window == s.window.GetWindowID() {
				s.render(thumbnails)
			}
		}
	}
}

// prepareThumbnails prepares thumbnail images from windows
func (s *Selector) prepareThumbnails() []image.Image {
	thumbnails := make([]image.Image, len(s.windows))
	for i, win := range s.windows {
		if win.Preview != nil {
			thumbnails[i] = win.Preview
		} else {
			// Use placeholder if no thumbnail available
			thumbnails[i] = carousel.DrawPlaceholder(256, 256, win.Name)
		}
	}
	return thumbnails
}

// render renders the carousel with current state
func (s *Selector) render(thumbnails []image.Image) {
	img := carousel.Draw3DCarousel(thumbnails, s.selectedIndex, s.animOffset, s.config)
	s.window.DrawImage(img)
}

// handleKeyPressSimple handles a key press event
// Returns true if ESC was pressed (cancel)
func (s *Selector) handleKeyPressSimple(e xproto.KeyPressEvent, thumbnails []image.Image) bool {
	keycode := e.Detail

	switch keycode {
	case 9: // Escape
		return true // Cancel

	case 23, 114: // Tab or Right Arrow - next window
		s.selectNext(thumbnails)

	case 113: // Left Arrow - previous window
		s.selectPrevious(thumbnails)
	}

	return false
}

// selectNext moves selection to next window with animation
func (s *Selector) selectNext(thumbnails []image.Image) {
	if len(s.windows) == 0 {
		return
	}

	targetIndex := (s.selectedIndex + 1) % len(s.windows)
	s.animateTransition(targetIndex, thumbnails)
}

// selectPrevious moves selection to previous window with animation
func (s *Selector) selectPrevious(thumbnails []image.Image) {
	if len(s.windows) == 0 {
		return
	}

	targetIndex := s.selectedIndex - 1
	if targetIndex < 0 {
		targetIndex = len(s.windows) - 1
	}
	s.animateTransition(targetIndex, thumbnails)
}

// animateTransition animates transition from current to target index
func (s *Selector) animateTransition(targetIndex int, thumbnails []image.Image) {
	if s.animating {
		return // Skip if already animating
	}

	s.animating = true
	defer func() { s.animating = false }()

	// No animation - instant switch
	s.animOffset = 0.0
	s.selectedIndex = targetIndex
	s.render(thumbnails)
}

// Close closes the selector window and frees resources
func (s *Selector) Close() error {
	if s.window != nil {
		return s.window.Close()
	}
	return nil
}

// GetWindowID returns the X11 window ID of the selector window
func (s *Selector) GetWindowID() xproto.Window {
	if s.window != nil {
		return s.window.GetWindowID()
	}
	return 0
}
