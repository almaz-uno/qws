package ui

import (
	"context"
	"fmt"
	"image"
	"math"
	"time"

	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog/log"
)

// Selector provides a graphical carousel interface for window selection
type Selector struct {
	ctx             context.Context
	conn            *xgb.Conn
	root            xproto.Window
	windows         []x11.WindowInfo
	allWindows      []x11.WindowInfo // All windows (unfiltered)
	selectedIndex   int
	hoverIndex      int // Index of window under mouse cursor (-1 if none)
	window          *carousel.Window
	config          carousel.Config
	monitorGeom     x11.MonitorGeometry // Current monitor geometry
	animOffset      float64
	animating       bool
	resultChan      chan *x11.WindowInfo
	altPressed      bool      // Track if Alt is currently pressed
	ctrlPressed     bool      // Track if Ctrl is currently pressed
	lastMouseUpdate time.Time // Last time mouse hover was processed
}

// NewSelector creates a new graphical window selector
func NewSelector(ctx context.Context, conn *xgb.Conn, root xproto.Window, windows []x11.WindowInfo) *Selector {
	// Try to get current monitor geometry, fallback to full screen on error
	monitor, err := x11.GetCurrentMonitor(conn, root)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get current monitor, falling back to full screen")
		// Fallback to full screen
		setup := xproto.Setup(conn)
		screen := setup.DefaultScreen(conn)
		monitor = x11.MonitorGeometry{
			X:      0,
			Y:      0,
			Width:  int(screen.WidthInPixels),
			Height: int(screen.HeightInPixels),
		}
	}

	log.Info().
		Int("x", monitor.X).
		Int("y", monitor.Y).
		Int("width", monitor.Width).
		Int("height", monitor.Height).
		Msg("Using monitor geometry for selector")

	config := carousel.DefaultConfig()
	config.Width = monitor.Width
	config.Height = monitor.Height

	return &Selector{
		ctx:           ctx,
		conn:          conn,
		root:          root,
		windows:       windows,
		allWindows:    windows,
		selectedIndex: 0,
		hoverIndex:    -1,
		config:        config,
		monitorGeom:   monitor,
		resultChan:    make(chan *x11.WindowInfo, 1),
	}
}

// UpdateWindows updates the window list, preserving the index
func (s *Selector) UpdateWindows(windows []x11.WindowInfo) {
	s.allWindows = windows
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

	// Check current monitor before showing (may have changed since last invocation)
	currentMonitor, err := x11.GetCurrentMonitor(s.conn, s.root)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get current monitor")
		// Use previously cached monitor geometry
		currentMonitor = s.monitorGeom
	}

	// Check if monitor has changed or window needs recreation
	needRecreate := s.window == nil ||
		currentMonitor.X != s.monitorGeom.X ||
		currentMonitor.Y != s.monitorGeom.Y ||
		currentMonitor.Width != s.monitorGeom.Width ||
		currentMonitor.Height != s.monitorGeom.Height

	if needRecreate {
		// Update monitor geometry and config
		s.monitorGeom = currentMonitor
		s.config.Width = currentMonitor.Width
		s.config.Height = currentMonitor.Height

		log.Debug().
			Int("x", currentMonitor.X).
			Int("y", currentMonitor.Y).
			Int("width", currentMonitor.Width).
			Int("height", currentMonitor.Height).
			Msg("Monitor changed, recreating selector window")

		// Destroy old window if exists
		if s.window != nil {
			s.window.Close()
		}

		// Create window at new monitor position with monitor size
		s.window, err = carousel.NewWindowAt(s.conn, s.root,
			s.monitorGeom.X, s.monitorGeom.Y,
			s.config.Width, s.config.Height)
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

	// Check if Alt is currently pressed when showing the selector
	// Mod1Mask is Alt modifier
	s.altPressed = s.isModifierPressed(xproto.ModMask1) // Mod1 is Alt

	// If Alt is not pressed (quick Alt+Tab), close immediately after rendering
	if !s.altPressed {
		// Initial render
		s.render(thumbnails)
		// Small delay to show the selection
		s.conn.Sync()
		// Return the selected window immediately
		if s.selectedIndex >= 0 && s.selectedIndex < len(s.windows) {
			s.window.Hide()
			return &s.windows[s.selectedIndex], nil
		}
		s.window.Hide()
		return nil, nil
	}

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
		event, _ := s.conn.WaitForEvent()
		if event == nil {
			// Connection closed or context cancelled
			return nil
		}

		switch e := event.(type) {
		case xproto.KeyPressEvent:
			// Track Alt presses
			if e.Detail == 64 || e.Detail == 108 {
				s.altPressed = true
			}

			// Handle Enter key - select current window
			if e.Detail == 36 { // Return/Enter
				if s.selectedIndex >= 0 && s.selectedIndex < len(s.windows) {
					return &s.windows[s.selectedIndex]
				}
				return nil
			}

			if s.handleKeyPressSimple(e, thumbnails) {
				// ESC pressed
				return nil
			}

		case xproto.KeyReleaseEvent:
			// Check if Ctrl was released (keycode 37 = left Ctrl, 105 = right Ctrl)
			if e.Detail == 37 || e.Detail == 105 {
				if s.ctrlPressed {
					s.ctrlPressed = false
					s.removeWorkspaceFilter()
					// Preserve selection if possible
					if s.selectedIndex >= len(s.windows) {
						s.selectedIndex = 0
					}
					s.render(s.prepareThumbnails())
				}
			}

			// Check if Alt was released (keycode 64 = left Alt, 108 = right Alt)
			if e.Detail == 64 || e.Detail == 108 {
				// Only react to Alt release if Alt was pressed while selector was open
				if s.altPressed {
					s.altPressed = false
					// Return selected window when Alt is released
					if s.selectedIndex >= 0 && s.selectedIndex < len(s.windows) {
						return &s.windows[s.selectedIndex]
					}
					return nil
				}
			}

		case xproto.ExposeEvent:
			if e.Window == s.window.GetWindowID() {
				s.render(thumbnails)
			}

		case xproto.MotionNotifyEvent:
			// Throttle mouse events to ~60fps (16ms) to avoid excessive redraws
			now := time.Now()
			if now.Sub(s.lastMouseUpdate) < 16*time.Millisecond {
				continue
			}
			s.lastMouseUpdate = now

			// Update hover index based on mouse position
			newHoverIndex := s.getWindowIndexAtPosition(int(e.EventX), int(e.EventY))
			if newHoverIndex != s.hoverIndex {
				s.hoverIndex = newHoverIndex
				s.render(thumbnails)
			}

		case xproto.ButtonPressEvent:
			// Left mouse button (button 1)
			if e.Detail == 1 {
				windowIndex := s.getWindowIndexAtPosition(int(e.EventX), int(e.EventY))
				if windowIndex >= 0 && windowIndex < len(s.windows) {
					// Select and return the clicked window
					return &s.windows[windowIndex]
				}
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

// prepareWindowData prepares window data for rendering with icons and titles
func (s *Selector) prepareWindowData() []carousel.WindowData {
	data := make([]carousel.WindowData, len(s.windows))
	for i, win := range s.windows {
		thumbnail := win.Preview
		if thumbnail == nil {
			// Use placeholder if no thumbnail available
			thumbnail = carousel.DrawPlaceholder(256, 256, win.Name)
		}
		data[i] = carousel.WindowData{
			Thumbnail: thumbnail,
			Icon:      win.Icon,
			Title:     win.Name,
			Workspace: win.Workspace,
		}
	}
	return data
}

// render renders the carousel with current state
func (s *Selector) render(thumbnails []image.Image) {
	// Use new rendering with icons and titles
	windowData := s.prepareWindowData()
	img := carousel.Draw3DCarouselWithData(windowData, s.selectedIndex, s.hoverIndex, s.animOffset, s.config)
	s.window.DrawImage(img)
}

// handleKeyPressSimple handles a key press event
// Returns true if ESC was pressed (cancel)
func (s *Selector) handleKeyPressSimple(e xproto.KeyPressEvent, thumbnails []image.Image) bool {
	keycode := e.Detail
	state := e.State

	// Check for Shift modifier (Shift = 0x1)
	shiftPressed := (state & xproto.ModMaskShift) != 0

	// Track Ctrl key press (keycode 37 = left Ctrl, 105 = right Ctrl)
	if keycode == 37 || keycode == 105 {
		if !s.ctrlPressed {
			s.ctrlPressed = true
			s.applyWorkspaceFilter()
			// Preserve selection if possible
			if s.selectedIndex >= len(s.windows) {
				s.selectedIndex = 0
			}
			s.render(s.prepareThumbnails())
		}
		return false
	}

	switch keycode {
	case 9: // Escape
		return true // Cancel

	case 23: // Tab
		if shiftPressed {
			// Alt+Shift+Tab - previous window
			s.selectPrevious(thumbnails)
		} else {
			// Alt+Tab - next window
			s.selectNext(thumbnails)
		}

	case 114: // Right Arrow - next window
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

// isModifierPressed checks if a modifier key is currently pressed
func (s *Selector) isModifierPressed(mask uint16) bool {
	reply, err := xproto.QueryPointer(s.conn, s.root).Reply()
	if err != nil {
		return false
	}
	return (reply.Mask & mask) != 0
}

// getWindowIndexAtPosition calculates which window card is at the given mouse position
// Returns -1 if no window is at that position
func (s *Selector) getWindowIndexAtPosition(mouseX, mouseY int) int {
	if len(s.windows) == 0 {
		return -1
	}

	centerX := float64(s.config.Width) / 2
	centerY := float64(s.config.Height) / 2

	// Check each window's position
	for i := range s.windows {
		offset := float64(i - s.selectedIndex)

		// Skip windows too far from center
		if math.Abs(offset) > 5 {
			continue
		}

		// Calculate card position and size (same logic as in renderer)
		var scale, x, y float64

		if math.Abs(offset) < 0.01 {
			// Central window
			scale = 1.0
			x = centerX
			y = centerY
		} else {
			// Side windows
			scale = s.config.PerspectiveFactor + (1.0-s.config.PerspectiveFactor)/(1.0+math.Abs(offset)*0.5)
			x = centerX + offset*s.config.Spacing*scale
			arcHeight := math.Abs(offset) * 10
			y = centerY + arcHeight
		}

		// Calculate final dimensions
		finalW := float64(s.config.ThumbWidth) * scale
		finalH := float64(s.config.ThumbHeight) * scale

		// Check if mouse is within card bounds
		if float64(mouseX) >= x-finalW/2 && float64(mouseX) <= x+finalW/2 &&
			float64(mouseY) >= y-finalH/2 && float64(mouseY) <= y+finalH/2 {
			return i
		}
	}

	return -1
}

// applyWorkspaceFilter filters windows to show only those in the current workspace
func (s *Selector) applyWorkspaceFilter() {
	// Get current workspace
	currentWorkspace := s.getCurrentWorkspace()
	if currentWorkspace == "" {
		log.Warn().Msg("Cannot determine current workspace")
		return
	}

	log.Debug().Str("workspace", currentWorkspace).Msg("Filtering windows by workspace")

	// Find currently selected window ID to preserve selection
	var selectedID xproto.Window
	if s.selectedIndex >= 0 && s.selectedIndex < len(s.windows) {
		selectedID = s.windows[s.selectedIndex].ID
	}

	// Filter windows by workspace
	filteredWindows := make([]x11.WindowInfo, 0)
	for _, win := range s.allWindows {
		if win.Workspace == currentWorkspace {
			filteredWindows = append(filteredWindows, win)
		}
	}

	if len(filteredWindows) == 0 {
		log.Warn().Msg("No windows in current workspace")
		return
	}

	s.windows = filteredWindows

	// Try to preserve selection by finding the same window in filtered list
	s.selectedIndex = 0
	for i, win := range s.windows {
		if win.ID == selectedID {
			s.selectedIndex = i
			break
		}
	}
}

// removeWorkspaceFilter removes workspace filter and shows all windows
func (s *Selector) removeWorkspaceFilter() {
	log.Debug().Msg("Removing workspace filter")

	// Find currently selected window ID to preserve selection
	var selectedID xproto.Window
	if s.selectedIndex >= 0 && s.selectedIndex < len(s.windows) {
		selectedID = s.windows[s.selectedIndex].ID
	}

	s.windows = s.allWindows

	// Try to preserve selection by finding the same window in full list
	s.selectedIndex = 0
	for i, win := range s.windows {
		if win.ID == selectedID {
			s.selectedIndex = i
			break
		}
	}
}

// getCurrentWorkspace returns the name of the current active workspace
func (s *Selector) getCurrentWorkspace() string {
	// Create temporary connection wrapper to use existing methods
	connWrapper := &x11.Connection{
		Conn: s.conn,
		Root: s.root,
	}

	desktop, err := connWrapper.GetCurrentDesktop()
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get current desktop")
		return ""
	}

	names, err := connWrapper.GetDesktopNames()
	if err != nil {
		// Fallback to desktop number
		return fmt.Sprintf("%d", desktop+1)
	}

	if int(desktop) < len(names) {
		return names[desktop]
	}

	// Fallback to desktop number
	return fmt.Sprintf("%d", desktop+1)
}
