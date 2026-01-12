package main

import (
	"context"
	"fmt"
	"os"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/almaz-uno/qws/pkg/composite"
	"github.com/almaz-uno/qws/pkg/focus"
	"github.com/almaz-uno/qws/pkg/keygrab"
	"github.com/almaz-uno/qws/pkg/mru"
	"github.com/almaz-uno/qws/pkg/ui"
	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var (
	cfgFile string
	cfg     *config.Config
	version = "dev" // Set by build flags
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:     "qws",
	Short:   "Quick Window Switcher - A fast and beautiful window switcher for X11",
	Version: version,
	Long: `QWS (Quick Window Switcher) is a modern window switcher for X11 window managers.
It provides a visually appealing carousel interface for switching between windows
with thumbnails and MRU (Most Recently Used) ordering.`,
	RunE: run,
}

func init() {
	cobra.OnInitialize(initConfig)

	// Define flags
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file (default is $HOME/.config/qws/config.yaml)")
	rootCmd.PersistentFlags().StringP("log-level", "", "info", "log level (trace, debug, info, warn, error)")
	rootCmd.PersistentFlags().CountP("verbose", "v", "verbose output (use -v for debug, -vv for trace)")
	rootCmd.PersistentFlags().Bool("keysym-list", false, "print list of supported key names and exit")

	// Keybindings
	rootCmd.PersistentFlags().StringP("keybindings-modifier", "m", "", "main modifier key (Alt, Super, Ctrl)")
	rootCmd.PersistentFlags().StringP("keybindings-key", "k", "", "main trigger key (Tab, grave, space, etc.)")
	rootCmd.PersistentFlags().String("keybindings-backward", "", "modifier for reverse navigation")
	rootCmd.PersistentFlags().String("keybindings-workspace-modifier", "", "modifier to filter current workspace")
	rootCmd.PersistentFlags().String("keybindings-cancel", "", "key to cancel selection")

	// Appearance
	rootCmd.PersistentFlags().Int("appearance-thumbnail-width", 0, "thumbnail width in pixels")
	rootCmd.PersistentFlags().Int("appearance-thumbnail-height", 0, "thumbnail height in pixels")
	rootCmd.PersistentFlags().Float64("appearance-spacing", 0, "distance between carousel items")
	rootCmd.PersistentFlags().Float64("appearance-perspective", 0, "perspective effect factor (0.0-1.0)")
	rootCmd.PersistentFlags().StringP("appearance-colors-theme", "t", "", "color theme (auto, dark, light)")

	// Behavior
	rootCmd.PersistentFlags().Duration("behavior-snapshot-interval", 0, "background thumbnail refresh interval")
	rootCmd.PersistentFlags().Duration("behavior-show-delay", 0, "delay before showing UI")

	// Windows
	rootCmd.PersistentFlags().String("windows-desktop", "", "desktop filter (current, all, all-except-current)")
	rootCmd.PersistentFlags().Bool("windows-ignore-skip-taskbar", false, "ignore _NET_WM_STATE_SKIP_TASKBAR hint")
	rootCmd.PersistentFlags().Bool("windows-sort-minimized-last", false, "sort minimized windows last")
}

// initConfig reads in config file and ENV variables if set
func initConfig() {
	var err error
	cfg, err = config.Load(cfgFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Apply command-line flags over config
	applyFlags()

	// Setup logging
	setupLogging()
}

// applyFlags applies command-line flags over configuration
func applyFlags() {
	// Handle verbose flag
	if v, _ := rootCmd.PersistentFlags().GetCount("verbose"); v > 0 {
		if v == 1 {
			cfg.Log.Level = "debug"
		} else if v >= 2 {
			cfg.Log.Level = "trace"
		}
	}

	// Log level
	if rootCmd.PersistentFlags().Changed("log-level") {
		cfg.Log.Level, _ = rootCmd.PersistentFlags().GetString("log-level")
	}

	// Keybindings
	if rootCmd.PersistentFlags().Changed("keybindings-modifier") {
		cfg.Keybindings.Modifier, _ = rootCmd.PersistentFlags().GetString("keybindings-modifier")
	}
	if rootCmd.PersistentFlags().Changed("keybindings-key") {
		cfg.Keybindings.Key, _ = rootCmd.PersistentFlags().GetString("keybindings-key")
	}
	if rootCmd.PersistentFlags().Changed("keybindings-backward") {
		cfg.Keybindings.Backward, _ = rootCmd.PersistentFlags().GetString("keybindings-backward")
	}
	if rootCmd.PersistentFlags().Changed("keybindings-workspace-modifier") {
		cfg.Keybindings.WorkspaceModifier, _ = rootCmd.PersistentFlags().GetString("keybindings-workspace-modifier")
	}
	if rootCmd.PersistentFlags().Changed("keybindings-cancel") {
		cfg.Keybindings.Cancel, _ = rootCmd.PersistentFlags().GetString("keybindings-cancel")
	}

	// Appearance
	if rootCmd.PersistentFlags().Changed("appearance-thumbnail-width") {
		cfg.Appearance.Thumbnail.Width, _ = rootCmd.PersistentFlags().GetInt("appearance-thumbnail-width")
	}
	if rootCmd.PersistentFlags().Changed("appearance-thumbnail-height") {
		cfg.Appearance.Thumbnail.Height, _ = rootCmd.PersistentFlags().GetInt("appearance-thumbnail-height")
	}
	if rootCmd.PersistentFlags().Changed("appearance-spacing") {
		cfg.Appearance.Spacing, _ = rootCmd.PersistentFlags().GetFloat64("appearance-spacing")
	}
	if rootCmd.PersistentFlags().Changed("appearance-perspective") {
		cfg.Appearance.Perspective, _ = rootCmd.PersistentFlags().GetFloat64("appearance-perspective")
	}
	if rootCmd.PersistentFlags().Changed("appearance-colors-theme") {
		cfg.Appearance.Colors.Theme, _ = rootCmd.PersistentFlags().GetString("appearance-colors-theme")
	}

	// Behavior
	if rootCmd.PersistentFlags().Changed("behavior-snapshot-interval") {
		cfg.Behavior.SnapshotInterval, _ = rootCmd.PersistentFlags().GetDuration("behavior-snapshot-interval")
	}
	if rootCmd.PersistentFlags().Changed("behavior-show-delay") {
		cfg.Behavior.ShowDelay, _ = rootCmd.PersistentFlags().GetDuration("behavior-show-delay")
	}

	// Windows
	if rootCmd.PersistentFlags().Changed("windows-desktop") {
		cfg.Windows.Desktop, _ = rootCmd.PersistentFlags().GetString("windows-desktop")
	}
	if rootCmd.PersistentFlags().Changed("windows-ignore-skip-taskbar") {
		cfg.Windows.IgnoreSkipTaskbar, _ = rootCmd.PersistentFlags().GetBool("windows-ignore-skip-taskbar")
	}
	if rootCmd.PersistentFlags().Changed("windows-sort-minimized-last") {
		cfg.Windows.SortMinimizedLast, _ = rootCmd.PersistentFlags().GetBool("windows-sort-minimized-last")
	}
}

// setupLogging configures zerolog based on configuration
func setupLogging() {
	// Set log level
	level, err := zerolog.ParseLevel(cfg.Log.Level)
	if err != nil {
		level = zerolog.InfoLevel
		log.Warn().Str("level", cfg.Log.Level).Msg("Invalid log level, using 'info'")
	}
	zerolog.SetGlobalLevel(level)

	// Set log format
	if cfg.Log.Format == "console" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	} else {
		log.Logger = zerolog.New(os.Stderr).With().Timestamp().Logger()
	}
}

// printKeysymList prints all supported key names and exits
func printKeysymList() {
	db := keygrab.GetKeysymDB()

	fmt.Println("Supported key names (case-insensitive, as shown by xev):")
	fmt.Println()

	// Navigation
	fmt.Println("Navigation:")
	for key := range db.Navigation {
		fmt.Printf("  %s\n", key)
	}
	fmt.Println()

	// Editing
	fmt.Println("Editing:")
	for key := range db.Editing {
		fmt.Printf("  %s\n", key)
	}
	fmt.Println()

	// Special keys
	fmt.Println("Special keys:")
	for key := range db.Special {
		fmt.Printf("  %s\n", key)
	}
	fmt.Println()

	// Function keys
	fmt.Println("Function keys:")
	for key := range db.Function {
		fmt.Printf("  %s\n", key)
	}
	fmt.Println()

	// Letters
	fmt.Println("Letters:")
	fmt.Println("  a-z (any lowercase letter)")
	fmt.Println()

	// Numbers
	fmt.Println()

	fmt.Println("Examples:")
	fmt.Println("  qws -k F10")
	fmt.Println("  qws -k Page_Down")
	fmt.Println("  qws -k home")
	fmt.Println("  qws -k grave")
}

// run is the main execution function
func run(cmd *cobra.Command, args []string) error {
	// Check if user wants to list supported keysyms
	if showList, _ := cmd.Flags().GetBool("keysym-list"); showList {
		printKeysymList()
		return nil
	}

	// Create root context
	ctx := cmd.Context()

	// Connect to X server
	conn, err := x11.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to X11: %w", err)
	}
	defer conn.Close()

	// Create MRU list
	mruList := mru.NewMRUList()

	// Initialize Composite for thumbnail capture
	capturer, err := composite.NewCapturer(conn.Conn, conn.Root)
	if err != nil {
		log.Warn().Err(err).Msg("Composite unavailable, thumbnails will be disabled")
	}

	// Create Focus Watcher to track active windows
	watcher, err := focus.NewWatcher(ctx, conn.Conn, conn.Root, mruList, capturer)
	if err != nil {
		log.Warn().Err(err).Msg("Focus watcher unavailable, MRU order will be disabled")
	}
	if watcher != nil {
		defer watcher.Stop()
	}

	// Create key grabber
	grabber := keygrab.NewKeyGrabber(conn.Conn, conn.Root)

	// Grab keys with configured keybindings
	log.Info().
		Str("modifier", cfg.Keybindings.Modifier).
		Str("key", cfg.Keybindings.Key).
		Str("backward", cfg.Keybindings.Backward).
		Str("workspace_modifier", cfg.Keybindings.WorkspaceModifier).
		Msg("Grabbing configured key combination")

	if err := grabber.GrabKeys(
		cfg.Keybindings.Modifier,
		cfg.Keybindings.Key,
		cfg.Keybindings.Backward,
		cfg.Keybindings.WorkspaceModifier,
	); err != nil {
		return fmt.Errorf("failed to grab keys: %w", err)
	}
	defer grabber.UngrabAll()

	// Create selector once to preserve state between calls
	var selector *ui.Selector
	defer func() {
		if selector != nil {
			selector.Close()
		}
	}()

	log.Info().Msg("QWS started, waiting for events...")

	// Monitor context cancellation and close connection when cancelled
	go func() {
		<-ctx.Done()
		log.Info().Msg("Received termination signal, shutting down...")
		conn.Close()
	}()

	// Main event loop
	for {
		event, err := conn.Conn.WaitForEvent()
		if event == nil {
			// Connection closed or error - exit gracefully
			log.Debug().Err(err).Msg("Event loop terminated")
			return nil
		}

		switch e := event.(type) {
		case xproto.KeyPressEvent:
			selector = handleKeyPress(ctx, conn, e, selector, mruList, watcher)
		case xproto.PropertyNotifyEvent:
			// Handle focus changes via PropertyNotify
			if watcher != nil {
				watcher.HandlePropertyNotify(e)
			}
		case xproto.FocusInEvent:
			// Fallback: handle FocusIn events
			if watcher != nil {
				watcher.HandleFocusIn(e)
			}
		}
	}
}

// handleKeyPress handles Alt+Tab key press
// Returns updated selector to preserve state
func handleKeyPress(ctx context.Context, conn *x11.Connection, e xproto.KeyPressEvent, selector *ui.Selector,
	mruList *mru.MRUList, watcher *focus.Watcher) *ui.Selector {
	// Get window list
	windows, err := conn.GetWindowList()
	if err != nil {
		return selector
	}

	if len(windows) == 0 {
		return selector
	}

	// Sort windows by MRU order
	mruOrder := mruList.GetOrder()
	if len(mruOrder) > 0 {
		windows = x11.SortWindowsByMRU(windows, mruOrder)
	}

	// Fill thumbnails from watcher cache
	if watcher != nil {
		for i := range windows {
			// Try to get from cache first
			if img, ok := watcher.GetThumbnail(xproto.Window(windows[i].ID)); ok {
				windows[i].Preview = img
			}
		}
	}

	// Create or reuse selector
	if selector == nil {
		selector = ui.NewSelector(ctx, conn.Conn, conn.Root, windows, cfg.Keybindings)
	} else {
		// Update window list, preserving position
		selector.UpdateWindows(windows)
	}

	selected, err := selector.Show()

	// Register selector window in watcher after Show() (when window is created)
	if watcher != nil && selector != nil {
		if windowID := selector.GetWindowID(); windowID != 0 {
			watcher.SetSwitcherWindow(windowID)
		}
	}

	if err != nil {
		return selector
	}

	if selected == nil {
		return selector
	}

	// Activate selected window
	if err := conn.ActivateWindow(selected.ID); err != nil {
		return selector
	}

	// Important: send all commands to X server
	conn.Conn.Sync()

	return selector
}
