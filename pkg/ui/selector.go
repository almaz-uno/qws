package ui

import (
	"context"
	"fmt"
	"image"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/almaz-uno/qws/pkg/focus"
	"github.com/almaz-uno/qws/pkg/glx"
	"github.com/almaz-uno/qws/pkg/keygrab"
	"github.com/almaz-uno/qws/pkg/snapshot"
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
	layoutToggleKeysym    uint32 // Key that toggles the carousel and the grid; 0: none (specs/026-layout-keys)
	layoutToggleName      string // Its name as configured, for the hint of the header
}

// Selector provides a graphical carousel interface for window selection
type Selector struct {
	ctx                 context.Context
	conn                *xgb.Conn
	root                xproto.Window
	windows             []x11.WindowInfo
	allWindows          []x11.WindowInfo // All windows (unfiltered)
	selectedIndex       int
	hoverIndex          int // Index of window under mouse cursor (-1 if none)
	window              *carousel.Window
	renderer            carousel.Renderer  // Draws frames
	presenter           carousel.Presenter // Shows frames in the overlay window
	config              carousel.Config
	appearance          config.Appearance   // Appearance configuration for recalculating on monitor change
	monitorGeom         x11.MonitorGeometry // Current monitor geometry
	paddingX            int                 // Horizontal padding from screen edges
	paddingY            int                 // Vertical padding from screen edges
	animOffset          float64
	resultChan          chan *x11.WindowInfo
	keyConfig           keyConfig                       // Configured keybindings
	modifierPressed     bool                            // Track if primary modifier is currently pressed
	workspacePressed    bool                            // Track if workspace modifier is currently pressed
	initialWorkspaceOpt string                          // Initial workspace configuration ("all", "current", "all-except-current")
	initialLayoutMode   string                          // Initial layout mode ("carousel" or "grid") for restoration on exit
	lastMouseUpdate     time.Time                       // Last time mouse hover was processed
	watcher             *focus.Watcher                  // Focus watcher for getting active window
	timing              frameTiming                     // Cause and start of the next frame, for latency logging
	placeholders        map[string]image.Image          // Placeholders of windows without a thumbnail, by title
	keymap              *xproto.GetKeyboardMappingReply // Keyboard mapping of the activation; nil: to be read
	modmap              *xproto.GetModifierMappingReply // Modifier mapping of the activation; nil: to be read
	animator            carousel.Animator               // The presenter, when it composes layers (specs/007-animation)
	step                stepAnimation                   // The step of the carousel in progress
	rest                restDrawing                     // The frame at rest drawn in the background
	layers              layerCache                      // Layers the animator holds
	held                heldFrames                      // Frames the presenter may still read (specs/030-drawing-memory)
	period              time.Duration                   // Frame period of the monitor of the overlay
	fade                fadeAnimation                   // The appearance or disappearance in progress
	hover               hoverAnimation                  // The levels of the hover frames
	anim                animationOptions                // The animations of the activation (specs/010-animation-options): animated, or none while still
	animated            animationOptions                // The animations of the configuration
	still               bool                            // The activation is still under auto: a VNC viewer connected, or the frames slipped (specs/031-animation-auto)
	auto                autoAnimation                   // What decides it
	deciding            bool                            // The first frame of the activation decides whether it is still
	frameDue            time.Time                       // When the next frame of an animation is due
	uploaded            int                             // Bytes uploaded in the pause before that frame
	animations          int                             // Animations so far, for the frame records
	mapped              bool                            // The overlay is on the screen
	chosenAt            time.Time                       // When the event that ended the activation was read
	live                liveThumbnails                  // The live thumbnails (specs/020-live-thumbnails)
	locate              locateAnimation                 // The selection frame converging after a switch to the grid (specs/028-grid-locate)
}

// NewSelector creates a new graphical window selector; snap, when not nil, is
// the snapshotter whose pictures the live thumbnails show
// (specs/020-live-thumbnails)
func NewSelector(ctx context.Context, conn *xgb.Conn, root xproto.Window, windows []x11.WindowInfo, appearance config.Appearance, keybindings config.Keybindings, initialWorkspaceOpt string, watcher *focus.Watcher, snap *snapshot.Snapshotter) (*Selector, error) {
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

	// Calculate padding from screen edges (supports % and px)
	paddingX := config.ParsePadding(appearance.WindowPadding.Horizontal, monitor.Width)
	paddingY := config.ParsePadding(appearance.WindowPadding.Vertical, monitor.Height)

	// Calculate window dimensions with padding
	windowWidth := monitor.Width - 2*paddingX
	windowHeight := monitor.Height - 2*paddingY

	log.Debug().
		Str("padding_x_config", appearance.WindowPadding.Horizontal).
		Str("padding_y_config", appearance.WindowPadding.Vertical).
		Int("padding_x", paddingX).
		Int("padding_y", paddingY).
		Int("window_width", windowWidth).
		Int("window_height", windowHeight).
		Msg("Calculated window dimensions with padding")

	// Build carousel configuration from appearance settings
	carouselConfig := carousel.Config{
		Width:                   windowWidth,
		Height:                  windowHeight,
		ThumbWidth:              appearance.Thumbnail.Width,
		ThumbHeight:             appearance.Thumbnail.Height,
		Spacing:                 appearance.Spacing,
		PerspectiveFactor:       appearance.Perspective,
		ShadowOffset:            appearance.Shadow.Offset,
		ShadowBlur:              appearance.Shadow.Blur,
		FontPaths:               appearance.Font.Paths,
		FontSize:                appearance.Font.Size,
		WindowBackgroundEnabled: appearance.WindowBackground.Enabled,
		WindowBackgroundOpacity: appearance.WindowBackground.Opacity,
		WindowBackgroundRadius:  appearance.WindowBackground.BorderRadius,
		LayoutMode:              appearance.Layout,
		GridColumns:             appearance.Grid.Columns,
		GridSpacing:             appearance.Grid.Spacing,
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
	carouselConfig.UrgentTitleBackground = themeColors.UrgentTitleBackground

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

	// The keys of the switcher the layout key cannot be: Enter, the cancel and
	// main keys, c, g and the arrows
	keyConf.layoutToggleKeysym, keyConf.layoutToggleName = parseLayoutToggle(keybindings.LayoutToggle,
		0xFF0D, keyConf.cancelKeysym, keyConf.mainKeysym, 0x0063, 0x0067, 0xFF51, 0xFF52, 0xFF53, 0xFF54)

	s := &Selector{
		ctx:                 ctx,
		conn:                conn,
		root:                root,
		windows:             windows,
		allWindows:          windows,
		selectedIndex:       0,
		hoverIndex:          -1,
		config:              carouselConfig,
		appearance:          appearance,
		monitorGeom:         monitor,
		paddingX:            paddingX,
		paddingY:            paddingY,
		animOffset:          0,
		resultChan:          make(chan *x11.WindowInfo, 1),
		keyConfig:           keyConf,
		initialWorkspaceOpt: initialWorkspaceOpt,
		initialLayoutMode:   appearance.Layout, // Save initial layout mode
		watcher:             watcher,
	}
	s.setLayout(appearance.Layout)

	// Initialize renderer and presenter
	var share *glx.Share
	if snap != nil && appearance.Thumbnail.Live {
		share = snap.Share()
	}
	renderer, presenter, err := carousel.NewBackend(appearance.Renderer, share)
	if err != nil {
		return nil, fmt.Errorf("failed to create renderer: %w", err)
	}
	s.renderer = renderer
	s.presenter = presenter
	if a, ok := presenter.(carousel.Animator); ok {
		s.animator = a
	}
	s.initLive(snap, presenter)
	s.initAnimations(appearance.Animation)

	// Apply initial workspace filtering based on configuration
	if initialWorkspaceOpt == "current" {
		s.applyWorkspaceFilter()
	} else if initialWorkspaceOpt == "all-except-current" {
		s.applyAllExceptCurrentWorkspaceFilter()
	}
	// If "all", windows are already unfiltered

	return s, nil
}

// UpdateWindows updates the window list, preserving the index
func (s *Selector) UpdateWindows(windows []x11.WindowInfo) {
	s.allWindows = windows

	// Re-apply initial workspace filtering
	if s.initialWorkspaceOpt == "current" {
		s.applyWorkspaceFilter()
	} else if s.initialWorkspaceOpt == "all-except-current" {
		s.applyAllExceptCurrentWorkspaceFilter()
	} else {
		s.windows = windows
	}

	// Reset index if it's out of bounds of the new list
	if s.selectedIndex >= len(s.windows) {
		s.selectedIndex = 0
	}

	// Keep the placeholders of windows that are still there
	titles := make(map[string]bool, len(windows))
	for _, win := range windows {
		titles[win.Name] = true
	}
	for title := range s.placeholders {
		if !titles[title] {
			delete(s.placeholders, title)
		}
	}
}

// SetHeader sets what the header of the overlay shows: the hostname and the
// version of qws (specs/005-host-and-version) — nothing, and the grid takes
// the whole window, when appearance.header.enabled is false
// (specs/014-appearance-keys)
func (s *Selector) SetHeader(hostname, version string) {
	if !s.appearance.Header.Enabled {
		hostname, version = "", ""
	}
	s.config.Hostname = hostname
	s.config.Version = version
}

// placeholder is the thumbnail of a window that has none, drawn once per title
func (s *Selector) placeholder(title string) image.Image {
	if img, ok := s.placeholders[title]; ok {
		return img
	}
	img := s.renderer.DrawPlaceholder(256, 256, title)
	if s.placeholders == nil {
		s.placeholders = make(map[string]image.Image)
	}
	s.placeholders[title] = img
	return img
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

	// Check that the first window in the list is actually the active window
	// If not (e.g., when focus is on an ignored window), reset selectedIndex to 0
	// We check this by verifying that GetActiveWindow() matches s.windows[0].ID
	// If GetActiveWindow() returns 0 or doesn't match, it means the current focus
	// is on an ignored/unlisted window, so we should start from index 0
	if s.watcher != nil && len(s.windows) > 0 {
		activeWindow, err := s.watcher.GetActiveWindow()
		if err != nil {
			log.Warn().Err(err).Msg("Failed to get active window")
		} else {
			log.Debug().
				Uint32("active_window", uint32(activeWindow)).
				Uint32("first_in_list", uint32(s.windows[0].ID)).
				Bool("match", activeWindow == s.windows[0].ID).
				Int("initial_index", s.selectedIndex).
				Msg("Checking active window vs first in MRU list")

			// If active window is 0 (unknown) or doesn't match the first window in list,
			// it means current focus is on an ignored window - start from beginning
			if activeWindow == 0 || s.windows[0].ID != activeWindow {
				log.Info().
					Uint32("active", uint32(activeWindow)).
					Uint32("first_in_list", uint32(s.windows[0].ID)).
					Msg("Active window doesn't match first in list (focus on ignored window?), resetting to index 0")
				s.selectedIndex = 0
			}
		}
	}

	// Check current monitor before showing (may have changed since last invocation)
	currentMonitor, err := x11.GetCurrentMonitor(s.conn, s.root)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get current monitor")
		// Use previously cached monitor geometry
		currentMonitor = s.monitorGeom
	}

	// A new activation: the window list and the thumbnails may have changed,
	// and the monitor with them
	s.dropLayers()
	s.period = time.Second / 60
	if currentMonitor.Refresh > 0 {
		s.period = time.Duration(float64(time.Second) / currentMonitor.Refresh)
	}
	// Animated or still, decided before its first frame is presented, the
	// viewer looked for meanwhile (specs/031-animation-auto)
	s.beginAnimation()

	// Check if monitor has changed or window needs recreation
	needRecreate := s.window == nil ||
		currentMonitor.X != s.monitorGeom.X ||
		currentMonitor.Y != s.monitorGeom.Y ||
		currentMonitor.Width != s.monitorGeom.Width ||
		currentMonitor.Height != s.monitorGeom.Height

	if needRecreate {
		// Update monitor geometry and config
		s.monitorGeom = currentMonitor

		// Recalculate padding and window dimensions (supports % and px)
		s.paddingX = config.ParsePadding(s.appearance.WindowPadding.Horizontal, currentMonitor.Width)
		s.paddingY = config.ParsePadding(s.appearance.WindowPadding.Vertical, currentMonitor.Height)
		s.config.Width = currentMonitor.Width - 2*s.paddingX
		s.config.Height = currentMonitor.Height - 2*s.paddingY

		log.Debug().
			Int("x", currentMonitor.X).
			Int("y", currentMonitor.Y).
			Int("width", currentMonitor.Width).
			Int("height", currentMonitor.Height).
			Int("padding_x", s.paddingX).
			Int("padding_y", s.paddingY).
			Int("window_width", s.config.Width).
			Int("window_height", s.config.Height).
			Msg("Monitor changed, recreating selector window")

		// Destroy old window if exists
		if s.window != nil {
			s.window.Close()
		}

		// Create window at monitor position with padding
		s.window, err = carousel.NewWindowAt(s.conn, s.root,
			s.monitorGeom.X+s.paddingX, s.monitorGeom.Y+s.paddingY,
			s.config.Width, s.config.Height, s.presenter.VisualID())
		if err != nil {
			return nil, fmt.Errorf("failed to create window: %w", err)
		}
		// Listed, so that the snapshots of other instances pause while it is
		// shown (specs/011-snapshot-pause)
		if err := x11.ListSwitcher(s.conn, s.root, s.window.GetWindowID()); err != nil {
			log.Debug().Err(err).Msg("Switcher not listed for other instances")
		}
		if err := s.presenter.Bind(s.window); err != nil {
			return nil, fmt.Errorf("failed to bind presenter to window: %w", err)
		}
		s.framesUnbound()
	}

	// Prepare thumbnails
	thumbnails := s.prepareThumbnails()

	// Show window
	if err := s.window.Show(); err != nil {
		return nil, fmt.Errorf("failed to show window: %w", err)
	}
	s.mapped = true

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

	// If workspace modifier is pressed, invert workspace filter based on initial configuration
	if s.workspacePressed {
		switch s.initialWorkspaceOpt {
		case "all", "all-except-current":
			// If showing all or all-except-current, Ctrl filters to current workspace
			log.Debug().Msg("Workspace modifier pressed at startup, filtering to current workspace")
			s.applyWorkspaceFilter()
		case "current":
			// If showing current, Ctrl shows all workspaces
			log.Debug().Msg("Workspace modifier pressed at startup, showing all workspaces")
			s.removeWorkspaceFilter()
		}
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
		s.chosenAt = time.Now()
		if s.selectedIndex >= 0 && s.selectedIndex < len(s.windows) {
			s.hide()
			return &s.windows[s.selectedIndex], nil
		}
		s.hide()
		return nil, nil
	}

	s.showFirst(thumbnails)

	// Event loop - wait for user input
	result := s.handleEventsSync(thumbnails)
	s.chosenAt = s.timing.start

	// The overlay stays for FadeOut when it fades out: after the chosen window
	// is activated
	if !s.fadesOut() {
		// An appearance cut short ends with the overlay
		s.fade.active = false
		s.hide()
	}

	return result, nil
}

// showFirst presents the first frame of the activation, shown by the effects
// of show when the presenter composes and the activation is animated, and
// asks for the layers of the steps
func (s *Selector) showFirst(thumbnails []image.Image) {
	s.fade.pending = s.animator != nil
	s.render(thumbnails)
	if !s.fade.active {
		// Shown at once
		s.setLive(true)
	}
	s.prefetch()
}

// restoreInitialLayoutMode restores the initial layout mode as the overlay
// is unmapped, so that the next activation opens in it (specs/026-layout-keys,
// D2); the fade-out shows the layout shown (specs/028-grid-locate)
func (s *Selector) restoreInitialLayoutMode() {
	if s.config.LayoutMode != s.initialLayoutMode {
		log.Debug().
			Str("from", s.config.LayoutMode).
			Str("to", s.initialLayoutMode).
			Msg("Restoring initial layout mode")
		s.setLayout(s.initialLayoutMode)
	}
}

// parseLayoutToggle is the keysym of the layout key named by
// keybindings.layout_toggle and the name its hint shows; 0 and "" for no
// key: an empty name, one keygrab does not know, or one of the keys taken by
// the switcher, which keep their meaning — the last two reported
// (specs/026-layout-keys)
func parseLayoutToggle(name string, taken ...uint32) (uint32, string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, ""
	}
	keysym, err := keygrab.GetKeysym(name)
	if err != nil {
		log.Warn().Err(err).Str("layout_toggle", name).Msg("Failed to parse layout toggle key, no layout toggle")
		return 0, ""
	}
	for _, k := range taken {
		if keysym == k {
			log.Warn().Str("layout_toggle", name).Msg("Layout toggle key is another key of the switcher, no layout toggle")
			return 0, ""
		}
	}
	return keysym, name
}

// layoutHint is the hint at the top right of the header of what the layout
// key named key does in the layout mode: "Q — grid" in the carousel, "Q —
// carousel" in the grid, a name of one letter upper-cased, any other as
// written; none without the key or the header (specs/026-layout-keys)
func layoutHint(key, mode string, header bool) string {
	if key == "" || !header {
		return ""
	}
	if r := []rune(key); len(r) == 1 && unicode.IsLetter(r[0]) {
		key = strings.ToUpper(key)
	}
	if mode == "grid" {
		return key + " — carousel"
	}
	return key + " — grid"
}

// setLayout makes mode the layout mode, with the hint of the layout key for
// it in the header
func (s *Selector) setLayout(mode string) {
	s.config = s.layoutConfig(mode)
}

// layoutConfig is the configuration of the frames of the layout mode: the
// mode, and the hint of the layout key for it in the header
func (s *Selector) layoutConfig(mode string) carousel.Config {
	cfg := s.config
	cfg.LayoutMode = mode
	cfg.LayoutHint = layoutHint(s.keyConfig.layoutToggleName, mode, s.appearance.Header.Enabled)
	return cfg
}

// switchLayout shows the layout mode, when it is not shown, ending what moves.
// On a presenter that composes, with the layers of that layout held — drawn
// in the background while the other is shown — it is shown from them at
// once, and its frame at rest, drawn in the background, follows; otherwise
// its frame is drawn now. The layers of both layouts stay. In the grid the
// selection frame converges onto its tile from that first frame, or, without
// the layers, from when they come (specs/028-grid-locate).
func (s *Selector) switchLayout(mode string, thumbnails []image.Image) {
	if s.config.LayoutMode == mode {
		return
	}
	key := s.timing.start
	held := s.animates() && (mode == "grid" && s.gridReady() || mode != "grid" && s.carouselReady())
	log.Debug().Str("layout", mode).Bool("layers", held).Msg("Switching layout")
	s.cancelStep()
	s.setLayout(mode)
	locates := mode == "grid" && s.locates()
	if !held {
		s.render(thumbnails)
		s.prefetch()
		if locates {
			s.locate.key = key
		}
		return
	}
	start := time.Now()
	if locates {
		s.beginLocate(start, key)
	}
	// The frames of a fade show the scene
	if !s.fade.active {
		drawEnd, end := s.presentScene(start)
		if s.locate.active {
			// The first frame of the convergence, at its start: a record of
			// the kind locate too (L)
			s.logAnimationFrame(&s.locate.animationLog, "locate", 0, false, start, drawEnd, end)
			s.frameDue = start.Add(s.period)
		}
	}
	s.prefetch()
	s.requestRest(causeKey)
	s.rest.awaited, s.rest.stepEnd = true, false
}

// moving reports whether something moves — a step, a fade, the hover or the
// convergence of the selection frame: frames are due
func (s *Selector) moving() bool {
	return s.step.active || s.fade.active || s.hover.active || s.locate.active
}

// handleEventsSync processes keyboard events synchronously
func (s *Selector) handleEventsSync(thumbnails []image.Image) *x11.WindowInfo {
	// The mappings are read once an activation, not on every key: a request
	// waits for the X server, which a capture of a window can hold for
	// milliseconds (specs/007-animation, research)
	s.keymap, s.modmap = nil, nil

	// Get keycodes for modifiers
	modifierKeycodes := s.getModifierKeycodes(s.keyConfig.modifierMask)
	workspaceKeycodes := s.getModifierKeycodes(s.keyConfig.workspaceModifierMask)
	enterKeycode := s.keysymToKeycode(0xFF0D) // XK_Return

	for {
		var event xgb.Event
		switch {
		case s.moving():
			// A step, a fade, the hover or the selection frame is moving:
			// frames between the events
			if event, _ = s.conn.PollForEvent(); event == nil {
				s.frame()
				continue
			}
		case s.backgroundDue() || s.liveDue():
			// Layers for the next step, or the frame at rest of the last one,
			// or a frame for the live thumbnails: between the events
			if event, _ = s.conn.PollForEvent(); event == nil {
				if s.liveDue() {
					s.liveIdle()
				} else {
					s.uploadIdle()
				}
				continue
			}
		default:
			if event, _ = s.conn.WaitForEvent(); event == nil {
				// Connection closed or context cancelled
				return nil
			}
		}

		if e, ok := event.(xproto.ClientMessageEvent); ok && s.liveEvent(e) {
			// A live picture published: not a cause of the frames of keys
			continue
		}
		if _, ok := event.(xproto.KeyPressEvent); ok {
			s.markFrameCause(causeKey)
		} else {
			s.markFrameCause(causeEvent)
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
					// Restore original workspace filter when releasing Ctrl
					if s.initialWorkspaceOpt == "all" {
						// Was showing current (filtered), now restore to all
						s.removeWorkspaceFilter()
					} else if s.initialWorkspaceOpt == "current" {
						// Was showing all, now restore to current (filtered)
						s.applyWorkspaceFilter()
					} else if s.initialWorkspaceOpt == "all-except-current" {
						// Was showing current (filtered), now restore to all-except-current
						s.applyAllExceptCurrentWorkspaceFilter()
					}
					// Preserve selection if possible
					if s.selectedIndex >= len(s.windows) {
						s.selectedIndex = 0
					}
					s.dropLayers()
					s.render(s.prepareThumbnails())
					s.prefetch()
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

		case xproto.MappingNotifyEvent:
			s.keymap, s.modmap = nil, nil
			modifierKeycodes = s.getModifierKeycodes(s.keyConfig.modifierMask)
			workspaceKeycodes = s.getModifierKeycodes(s.keyConfig.workspaceModifierMask)
			enterKeycode = s.keysymToKeycode(0xFF0D)

		case xproto.ExposeEvent:
			// What moves presents a frame soon anyway, and so does what has
			// its frame at rest on its way
			if e.Window == s.window.GetWindowID() && !s.moving() && !s.rest.awaited {
				s.refresh(thumbnails)
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
				s.hoverChanged(thumbnails)
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
			thumbnails[i] = s.placeholder(win.Name)
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
			thumbnail = s.placeholder(win.Name)
		}
		data[i] = carousel.WindowData{
			Thumbnail: thumbnail,
			Icon:      win.Icon,
			Title:     win.Name,
			Workspace: win.Workspace,
			Urgent:    win.Urgent,
		}
	}
	return data
}

// render renders the carousel with current state
func (s *Selector) render(thumbnails []image.Image) {
	drawStart := time.Now()
	// The frame drawn here is newer than a frame at rest still on its way
	s.rest.awaited = false

	// Prepare window data with icons and titles
	windowData := s.prepareWindowData()

	var img *image.RGBA
	// Choose rendering mode based on configuration
	if s.config.LayoutMode == "grid" {
		img = s.renderer.DrawGridLayout(windowData, s.selectedIndex, s.hoverIndex, s.config)
	} else {
		// Default to carousel
		img = s.renderer.Draw3DCarouselWithData(windowData, s.selectedIndex, s.hoverIndex, s.animOffset, s.config)
	}
	drawEnd := time.Now()

	// The first frame of an activation starts its appearance, when the
	// activation is animated — decided now, the viewer looked for while the
	// frame was drawn (specs/031-animation-auto); while a fade runs, a frame
	// drawn in full is presented through it
	s.decideAnimation()
	appears := s.fade.pending && s.anim.show.any()
	s.fade.pending = false
	if appears {
		s.beginFade(false, drawEnd, s.timing.start)
	}
	s.liveBegin()
	s.liveRest()
	var err error
	if s.fade.active {
		err = s.animator.PresentFaded(img, s.fadeAt(drawEnd))
	} else {
		err = s.presenter.Present(img)
	}
	if err != nil {
		log.Error().Err(err).Msg("Failed to present frame")
	}
	end := time.Now()
	s.liveEnd(end)
	if appears {
		// The appearance runs from the end of its first frame, which uploads
		// a whole frame; the level of that frame stays
		s.fade.level.start = end.Add(-s.period)
	}
	s.dumpFrame(img)
	// The presenter's last frame from now, or, not presented, nobody's
	// (specs/030-drawing-memory, D3)
	if err == nil {
		s.framePresented(img)
	} else {
		s.recycleFrame(img)
	}
	if s.fade.active {
		s.logAnimationFrame(&s.fade.animationLog, s.fade.kind(), s.fade.level.progress(drawEnd), false, drawStart, drawEnd, end)
		s.frameDue = drawEnd.Add(s.period)
	}
	s.logFrame(drawStart, drawEnd, end)
}

// refresh shows the last frame again after an Expose: the presenter still
// holds it, so it is not drawn anew
func (s *Selector) refresh(thumbnails []image.Image) {
	start := time.Now()
	s.liveBegin()
	s.liveRest()
	ok, err := s.presenter.Refresh()
	if err != nil {
		log.Error().Err(err).Msg("Failed to refresh frame")
	}
	end := time.Now()
	s.liveEnd(end)
	if !ok {
		s.render(thumbnails)
		return
	}
	s.logRefresh(start, end)
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
			// Invert workspace filter based on initial configuration
			switch s.initialWorkspaceOpt {
			case "all", "all-except-current":
				// If showing all or all-except-current, Ctrl filters to current workspace
				s.applyWorkspaceFilter()
			case "current":
				// If showing current, Ctrl shows all workspaces
				s.removeWorkspaceFilter()
			}
			// Preserve selection if possible
			// Prefer index 1 (next window) over 0 (current window) for Alt+Tab logic
			if s.selectedIndex >= len(s.windows) {
				if len(s.windows) >= 2 {
					s.selectedIndex = 1
				} else {
					s.selectedIndex = 0
				}
			}
			s.dropLayers()
			s.render(s.prepareThumbnails())
			s.prefetch()
		}
		return false
	}

	// Check for cancel key (Escape by default)
	if s.isKeycode(keycode, s.keyConfig.cancelKeysym) {
		return true // Cancel
	}

	// Check for layout switching keys (c = carousel, g = grid)
	cKeysym := uint32(0x0063) // 'c'
	gKeysym := uint32(0x0067) // 'g'

	// Only allow layout switching without Ctrl modifier
	ctrlPressed := (state & s.keyConfig.workspaceModifierMask) != 0

	if !ctrlPressed && s.isKeycode(keycode, cKeysym) {
		s.switchLayout("carousel", thumbnails)
		return false
	}

	if !ctrlPressed && s.isKeycode(keycode, gKeysym) {
		s.switchLayout("grid", thumbnails)
		return false
	}

	// Check for Ctrl+C (emergency exit)
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

	// Up and Down move by a row in the grid; the carousel has none
	// (specs/026-layout-keys)
	if s.isKeycode(keycode, 0xFF54) { // XK_Down
		s.selectRow(1, thumbnails)
		return false
	}
	if s.isKeycode(keycode, 0xFF52) { // XK_Up
		s.selectRow(-1, thumbnails)
		return false
	}

	// The layout key, after every other key of the switcher, as c and g
	// without the workspace modifier: the other layout
	if !ctrlPressed && s.keyConfig.layoutToggleKeysym != 0 && s.isKeycode(keycode, s.keyConfig.layoutToggleKeysym) {
		if s.grid() {
			s.switchLayout("carousel", thumbnails)
		} else {
			s.switchLayout("grid", thumbnails)
		}
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

// animateTransition animates transition from current to target index; a
// step around the list, from the last window to the first or back, is not
// animated (specs/007-animation)
func (s *Selector) animateTransition(targetIndex int, thumbnails []image.Image) {
	wrap := targetIndex-s.selectedIndex > 1 || s.selectedIndex-targetIndex > 1
	s.stepTo(targetIndex, wrap, thumbnails)
}

// selectRow moves the selection of the grid a row down, dir 1, or up, -1,
// within its column and around it; nothing in the carousel. The selection
// frame slides to the tile above or below; around a column of three tiles or
// more it is at once, as a step around the list (specs/026-layout-keys).
func (s *Selector) selectRow(dir int, thumbnails []image.Image) {
	if !s.grid() || len(s.windows) == 0 {
		return
	}
	cols := carousel.GridColumns(len(s.windows), s.config)
	target := columnStep(s.selectedIndex, len(s.windows), cols, dir)
	if target == s.selectedIndex {
		return
	}
	s.stepTo(target, target-s.selectedIndex != cols && s.selectedIndex-target != cols, thumbnails)
}

// columnStep is the tile a row down, dir 1, or up, -1, from tile i of n laid
// out row after row in cols columns, the last row from the left: within the
// column of i and around it — down from its last tile to its top, up from
// its top to its last tile; i itself in a column of one tile
func columnStep(i, n, cols, dir int) int {
	c := i % cols
	if dir > 0 {
		if i+cols < n {
			return i + cols
		}
		return c
	}
	if i-cols >= 0 {
		return i - cols
	}
	return c + cols*((n-1-c)/cols)
}

// Close closes the selector window and frees resources
func (s *Selector) Close() error {
	if s.presenter != nil {
		s.presenter.Close()
	}
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
	mapping := s.keymap
	if mapping == nil {
		var err error
		mapping, err = xproto.GetKeyboardMapping(s.conn,
			setup.MinKeycode,
			byte(setup.MaxKeycode-setup.MinKeycode+1)).Reply()
		if err != nil {
			return 0
		}
		s.keymap = mapping
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
	modmap := s.modmap
	if modmap == nil {
		var err error
		modmap, err = xproto.GetModifierMapping(s.conn).Reply()
		if err != nil {
			return nil
		}
		s.modmap = modmap
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

	// Use different hit detection for grid vs carousel
	if s.config.LayoutMode == "grid" {
		return s.getWindowIndexAtPositionGrid(mouseX, mouseY)
	}
	return s.getWindowIndexAtPositionCarousel(mouseX, mouseY)
}

// getWindowIndexAtPositionCarousel calculates position for carousel layout
func (s *Selector) getWindowIndexAtPositionCarousel(mouseX, mouseY int) int {
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

// getWindowIndexAtPositionGrid is the tile of the grid under the mouse, or
// -1: each tile where the grid draws it, below the band of the header
// (specs/027-grid-mouse)
func (s *Selector) getWindowIndexAtPositionGrid(mouseX, mouseY int) int {
	px, py := float64(mouseX), float64(mouseY)
	for i := range s.windows {
		x, y, w, h := carousel.GridTile(len(s.windows), i, s.config)
		if px >= x && px <= x+w && py >= y && py <= y+h {
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

	// Use common filtering function
	filteredWindows := x11.FilterWindowsByWorkspace(s.allWindows, currentWorkspace, "current")

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

	// Use common filtering function with "all" option
	s.windows = x11.FilterWindowsByWorkspace(s.allWindows, "", "all")

	// Try to preserve selection by finding the same window in full list
	s.selectedIndex = 0
	for i, win := range s.windows {
		if win.ID == selectedID {
			s.selectedIndex = i
			break
		}
	}
}

// applyAllExceptCurrentWorkspaceFilter filters windows to show all except those in the current workspace
func (s *Selector) applyAllExceptCurrentWorkspaceFilter() {
	// Get current workspace
	currentWorkspace := s.getCurrentWorkspace()
	if currentWorkspace == "" {
		log.Warn().Msg("Cannot determine current workspace")
		return
	}

	log.Debug().Str("workspace", currentWorkspace).Msg("Filtering windows to all except current workspace")

	// Find currently selected window ID to preserve selection
	var selectedID xproto.Window
	if s.selectedIndex >= 0 && s.selectedIndex < len(s.windows) {
		selectedID = s.windows[s.selectedIndex].ID
	}

	// Use common filtering function
	filteredWindows := x11.FilterWindowsByWorkspace(s.allWindows, currentWorkspace, "all-except-current")

	if len(filteredWindows) == 0 {
		log.Warn().Msg("No windows outside current workspace")
		return
	}

	s.windows = filteredWindows

	// Try to preserve selection by finding the same window in filtered list
	// Default to index 0 for this case
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
