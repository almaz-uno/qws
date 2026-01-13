package ui

import (
	"context"
	"fmt"
	"image"
	"math"
	"time"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/almaz-uno/qws/pkg/keygrab"
	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog/log"
)

// keyConfig holds runtime keycode information for configured keys
type keyConfig struct {
	modifierMask          uint16 // Primary modifier (Alt, Super, etc.)
	backwardMask          uint16 // Backward modifier (Shift)
	workspaceModifierMask uint16 // Workspace filter modifier (Ctrl)
	mainKeysym            uint32 // Main trigger key keysym (Tab, F10, etc.)
	cancelKeysym          uint32 // Cancel key keysym (Escape)
}

// Selector provides a graphical carousel interface for window selection
type Selector struct {
	ctx              context.Context
	conn             *xgb.Conn
	root             xproto.Window
	windows          []x11.WindowInfo
	allWindows       []x11.WindowInfo // All windows (unfiltered)
	selectedIndex    int
	hoverIndex       int // Index of window under mouse cursor (-1 if none)
	window           *carousel.Window
	config           carousel.Config
	monitorGeom      x11.MonitorGeometry // Current monitor geometry
	animOffset       float64
	animating        bool
	resultChan       chan *x11.WindowInfo
	keyConfig        keyConfig // Configured keybindings
	modifierPressed  bool      // Track if primary modifier is currently pressed
	workspacePressed bool      // Track if workspace modifier is currently pressed
	lastMouseUpdate  time.Time // Last time mouse hover was processed
}

// NewSelector creates a new graphical window selector
func NewSelector(ctx context.Context, conn *xgb.Conn, root xproto.Window, windows []x11.WindowInfo, appearance config.Appearance, keybindings config.Keybindings) *Selector {
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

	// Build carousel configuration from appearance settings
	carouselConfig := carousel.Config{
		Width:             monitor.Width,
		Height:            monitor.Height,
		ThumbWidth:        appearance.Thumbnail.Width,
		ThumbHeight:       appearance.Thumbnail.Height,
		Spacing:           appearance.Spacing,
		PerspectiveFactor: appearance.Perspective,
		ShadowOffset:      appearance.Shadow.Offset,
		ShadowBlur:        appearance.Shadow.Blur,
		FontPrimary:       appearance.Font.Primary,
		FontFallback:      appearance.Font.Fallback,
		FontSize:          appearance.Font.Size,
	}

	// Resolve theme and apply colors
	activeTheme := config.ResolveTheme(ctx, appearance.Colors.Theme)
	var themeColors config.ThemeColor
	if activeTheme == "dark" {
		themeColors = appearance.Colors.Dark
		log.Debug().Str("theme", "dark").Msg("Using dark theme")
	} else {
		themeColors = appearance.Colors.Light
		log.Debug().Str("theme", "light").Msg("Using light theme")
	}

	carouselConfig.BackgroundColor = themeColors.Background
	carouselConfig.SelectionFrame = themeColors.SelectionFrame
	carouselConfig.TextColor = themeColors.Text
	carouselConfig.ShadowColor = themeColors.Shadow
	carouselConfig.InactiveFrame = themeColors.InactiveFrame

	// Parse keybindings to runtime key configuration
	keyConf := keyConfig{}

	if mask, err := keygrab.GetModifierMask(keybindings.Modifier); err != nil {
		log.Warn().Err(err).Str("modifier", keybindings.Modifier).Msg("Failed to parse modifier, using Alt")
		keyConf.modifierMask = uint16(keygrab.ModAlt)
	} else {
		keyConf.modifierMask = mask
	}

	if mask, err := keygrab.GetModifierMask(keybindings.Backward); err != nil {
		log.Warn().Err(err).Str("backward", keybindings.Backward).Msg("Failed to parse backward modifier, using Shift")
		keyConf.backwardMask = uint16(keygrab.ModShift)
	} else {
		keyConf.backwardMask = mask
	}

	if mask, err := keygrab.GetModifierMask(keybindings.WorkspaceModifier); err != nil {
		log.Warn().Err(err).Str("workspace_modifier", keybindings.WorkspaceModifier).Msg("Failed to parse workspace modifier, using Ctrl")
		keyConf.workspaceModifierMask = uint16(keygrab.ModControl)
	} else {
		keyConf.workspaceModifierMask = mask
	}

	if keysym, err := keygrab.GetKeysym(keybindings.Key); err != nil {
		log.Warn().Err(err).Str("key", keybindings.Key).Msg("Failed to parse main key, using Tab")
		keyConf.mainKeysym = 0xFF09 // XK_Tab
	} else {
		keyConf.mainKeysym = keysym
	}

	if keysym, err := keygrab.GetKeysym(keybindings.Cancel); err != nil {
		log.Warn().Err(err).Str("cancel", keybindings.Cancel).Msg("Failed to parse cancel key, using Escape")
		keyConf.cancelKeysym = 0xFF1B // XK_Escape
	} else {
		keyConf.cancelKeysym = keysym
	}

	return &Selector{
		ctx:           ctx,
		conn:          conn,
		root:          root,
		windows:       windows,
		allWindows:    windows,
		selectedIndex: 0,
		hoverIndex:    -1,
		config:        carouselConfig,
		monitorGeom:   monitor,
		resultChan:    make(chan *x11.WindowInfo, 1),
		keyConfig:     keyConf,
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

	// Check if primary modifier is currently pressed when showing the selector
	s.modifierPressed = s.isModifierPressed(s.keyConfig.modifierMask)

	// Check if workspace modifier is currently pressed when showing the selector
	s.workspacePressed = s.isModifierPressed(s.keyConfig.workspaceModifierMask)

	// If workspace modifier is pressed, apply workspace filter immediately
	if s.workspacePressed {
		log.Debug().Msg("Workspace modifier pressed at startup, filtering by workspace")
		s.applyWorkspaceFilter()
		// Update selected index if it went out of bounds
		// Prefer index 1 (next window) over 0 (current window) for Alt+Tab logic
		if s.selectedIndex >= len(s.windows) {
			if len(s.windows) >= 2 {
				s.selectedIndex = 1
			} else {
				s.selectedIndex = 0
			}
		}
		// Re-prepare thumbnails after filtering
		thumbnails = s.prepareThumbnails()
	}

	// If primary modifier is not pressed (quick key press), close immediately after rendering
	if !s.modifierPressed {
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
	// Get keycodes for modifiers
	modifierKeycodes := s.getModifierKeycodes(s.keyConfig.modifierMask)
	workspaceKeycodes := s.getModifierKeycodes(s.keyConfig.workspaceModifierMask)
	enterKeycode := s.keysymToKeycode(0xFF0D) // XK_Return

	for {
		event, _ := s.conn.WaitForEvent()
		if event == nil {
			// Connection closed or context cancelled
			return nil
		}

		switch e := event.(type) {
		case xproto.KeyPressEvent:
			// Track primary modifier presses
			if s.isModifierKeycode(e.Detail, modifierKeycodes) {
				s.modifierPressed = true
			}

			// Handle Enter key - select current window
			if enterKeycode != 0 && e.Detail == enterKeycode {
				if s.selectedIndex >= 0 && s.selectedIndex < len(s.windows) {
					return &s.windows[s.selectedIndex]
				}
				return nil
			}

			if s.handleKeyPressSimple(e, thumbnails) {
				// Cancel key pressed
				return nil
			}

		case xproto.KeyReleaseEvent:
			// Check if workspace modifier was released
			if s.isModifierKeycode(e.Detail, workspaceKeycodes) {
				if s.workspacePressed {
					s.workspacePressed = false
					s.removeWorkspaceFilter()
					// Preserve selection if possible
					if s.selectedIndex >= len(s.windows) {
						s.selectedIndex = 0
					}
					s.render(s.prepareThumbnails())
				}
			}

			// Check if primary modifier was released
			if s.isModifierKeycode(e.Detail, modifierKeycodes) {
				// Only react to modifier release if modifier was pressed while selector was open
				if s.modifierPressed {
					s.modifierPressed = false
					// Return selected window when modifier is released
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
// Returns true if cancel key was pressed
func (s *Selector) handleKeyPressSimple(e xproto.KeyPressEvent, thumbnails []image.Image) bool {
	keycode := e.Detail
	state := e.State

	// Check for backward modifier
	backwardPressed := (state & s.keyConfig.backwardMask) != 0

	// Track workspace modifier key press
	workspaceKeycodes := s.getModifierKeycodes(s.keyConfig.workspaceModifierMask)
	if s.isModifierKeycode(keycode, workspaceKeycodes) {
		if !s.workspacePressed {
			s.workspacePressed = true
			s.applyWorkspaceFilter()
			// Preserve selection if possible
			// Prefer index 1 (next window) over 0 (current window) for Alt+Tab logic
			if s.selectedIndex >= len(s.windows) {
				if len(s.windows) >= 2 {
					s.selectedIndex = 1
				} else {
					s.selectedIndex = 0
				}
			}
			s.render(s.prepareThumbnails())
		}
		return false
	}

	// Check for cancel key (Escape by default)
	if s.isKeycode(keycode, s.keyConfig.cancelKeysym) {
		return true // Cancel
	}

	// Check for Ctrl+C (emergency exit)
	ctrlPressed := (state & s.keyConfig.workspaceModifierMask) != 0
	cKeysym := uint32(0x0063) // 'c'
	if ctrlPressed && s.isKeycode(keycode, cKeysym) {
		log.Debug().Msg("Ctrl+C pressed, cancelling")
		return true // Cancel
	}

	// Get configured main key and arrow keys
	mainKeysym := s.keyConfig.mainKeysym
	leftKeysym := uint32(0xFF51)  // XK_Left
	rightKeysym := uint32(0xFF53) // XK_Right

	// Handle main key (configured trigger key)
	if s.isKeycode(keycode, mainKeysym) {
		if backwardPressed {
			// Modifier+Backward+Key - previous window
			s.selectPrevious(thumbnails)
		} else {
			// Modifier+Key - next window
			s.selectNext(thumbnails)
		}
		return false
	}

	// Handle arrow keys
	if s.isKeycode(keycode, rightKeysym) {
		s.selectNext(thumbnails)
		return false
	}

	if s.isKeycode(keycode, leftKeysym) {
		s.selectPrevious(thumbnails)
		return false
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

// keysymToKeycode converts keysym to keycode for current keyboard layout
func (s *Selector) keysymToKeycode(keysym uint32) xproto.Keycode {
	setup := xproto.Setup(s.conn)
	mapping, err := xproto.GetKeyboardMapping(s.conn,
		setup.MinKeycode,
		byte(setup.MaxKeycode-setup.MinKeycode+1)).Reply()
	if err != nil {
		return 0
	}

	for keycode := setup.MinKeycode; keycode <= setup.MaxKeycode; keycode++ {
		for i := byte(0); i < mapping.KeysymsPerKeycode; i++ {
			idx := int(keycode-setup.MinKeycode)*int(mapping.KeysymsPerKeycode) + int(i)
			if idx < len(mapping.Keysyms) && uint32(mapping.Keysyms[idx]) == keysym {
				return keycode
			}
		}
	}
	return 0
}

// isKeycode checks if the given detail matches any keycode for the keysym
func (s *Selector) isKeycode(detail xproto.Keycode, keysym uint32) bool {
	expectedKeycode := s.keysymToKeycode(keysym)
	return expectedKeycode != 0 && detail == expectedKeycode
}

// getModifierKeycodes returns all keycodes that produce the given modifier mask
func (s *Selector) getModifierKeycodes(mask uint16) []xproto.Keycode {
	modmap, err := xproto.GetModifierMapping(s.conn).Reply()
	if err != nil {
		return nil
	}

	// Determine which modifier position corresponds to our mask
	// Shift=0, Lock=1, Control=2, Mod1=3, Mod2=4, Mod3=5, Mod4=6, Mod5=7
	modIndex := -1
	switch mask {
	case uint16(keygrab.ModShift):
		modIndex = 0
	case uint16(keygrab.ModCapsLock):
		modIndex = 1
	case uint16(keygrab.ModControl):
		modIndex = 2
	case uint16(keygrab.ModAlt): // Mod1
		modIndex = 3
	case uint16(keygrab.ModNumLock): // Mod2
		modIndex = 4
	case uint16(keygrab.Mod3):
		modIndex = 5
	case uint16(keygrab.ModSuper): // Mod4
		modIndex = 6
	case uint16(keygrab.Mod5):
		modIndex = 7
	}

	if modIndex < 0 {
		return nil
	}

	keycodes := []xproto.Keycode{}
	for i := byte(0); i < modmap.KeycodesPerModifier; i++ {
		idx := modIndex*int(modmap.KeycodesPerModifier) + int(i)
		if idx < len(modmap.Keycodes) && modmap.Keycodes[idx] != 0 {
			keycodes = append(keycodes, modmap.Keycodes[idx])
		}
	}
	return keycodes
}

// isModifierKeycode checks if detail matches any of the given modifier keycodes
func (s *Selector) isModifierKeycode(detail xproto.Keycode, keycodes []xproto.Keycode) bool {
	for _, kc := range keycodes {
		if detail == kc {
			return true
		}
	}
	return false
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
	// Default to index 1 (next window) instead of 0 (current window) for Alt+Tab logic
	if len(s.windows) >= 2 {
		s.selectedIndex = 1
	} else {
		s.selectedIndex = 0
	}
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
