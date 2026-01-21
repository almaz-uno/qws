package main

import (
	"context"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"runtime/pprof"
	"time"

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
	cfgFile    string
	cfg        *config.Config
	version    = "dev" // Set by build flags
	defaultCfg = config.Default()
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "qws",
	Short: "Quick Window Switcher - A fast and beautiful window switcher for X11",
	Long: `QWS (Quick Window Switcher) is a modern window switcher for X11 window managers.
It provides a visually appealing carousel interface for switching between windows
with thumbnails and MRU (Most Recently Used) ordering.`,
	RunE: run,
}

func init() {
	cobra.OnInitialize(initConfig)

	// Define flags
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file (default is $HOME/.config/qws/config.yaml)")
	rootCmd.PersistentFlags().StringP("log-level", "", defaultCfg.Log.Level, "log level (trace, debug, info, warn, error)")
	rootCmd.PersistentFlags().CountP("verbose", "v", "verbose output (use -v for debug, -vv for trace)")
	rootCmd.PersistentFlags().Bool("keysym-list", false, "print list of supported key names and exit")

	// Profiling flags
	rootCmd.PersistentFlags().String("cpuprofile", "", "write cpu profile to file")
	rootCmd.PersistentFlags().String("memprofile", "", "write memory profile to file")
	rootCmd.PersistentFlags().String("pprof", "", "start pprof HTTP server on address (e.g. localhost:6060)")

	// Keybindings
	rootCmd.PersistentFlags().StringP("keybindings-modifier", "m", defaultCfg.Keybindings.Modifier, "main modifier key (Alt, Super, Ctrl)")
	rootCmd.PersistentFlags().StringP("keybindings-key", "k", defaultCfg.Keybindings.Key, "main trigger key (Tab, grave, space, etc.)")
	rootCmd.PersistentFlags().String("keybindings-backward", defaultCfg.Keybindings.Backward, "modifier for reverse navigation")
	rootCmd.PersistentFlags().String("keybindings-workspace-modifier", defaultCfg.Keybindings.WorkspaceModifier, "modifier to filter current workspace")
	rootCmd.PersistentFlags().String("keybindings-cancel", defaultCfg.Keybindings.Cancel, "key to cancel selection")

	// Appearance
	rootCmd.PersistentFlags().StringP("appearance-layout", "l", defaultCfg.Appearance.Layout, "layout mode (carousel, grid)")
	rootCmd.PersistentFlags().Bool("grid", false, "use grid layout (shortcut for --appearance-layout=grid)")
	rootCmd.PersistentFlags().StringP("appearance-renderer", "r", defaultCfg.Appearance.Renderer, "renderer backend (cpu, glx)")
	rootCmd.PersistentFlags().Int("appearance-thumbnail-width", defaultCfg.Appearance.Thumbnail.Width, "thumbnail width in pixels")
	rootCmd.PersistentFlags().Int("appearance-thumbnail-height", defaultCfg.Appearance.Thumbnail.Height, "thumbnail height in pixels")
	rootCmd.PersistentFlags().String("appearance-thumbnail-scaling-algorithm", defaultCfg.Appearance.Thumbnail.ScalingAlgorithm, "thumbnail scaling algorithm (nearest, bilinear, catmull-rom)")
	rootCmd.PersistentFlags().Float64("appearance-spacing", defaultCfg.Appearance.Spacing, "distance between carousel items")
	rootCmd.PersistentFlags().Float64("appearance-perspective", defaultCfg.Appearance.Perspective, "perspective effect factor (0.0-1.0)")
	rootCmd.PersistentFlags().Int("appearance-grid-columns", defaultCfg.Appearance.Grid.Columns, "number of columns in grid layout (0 = auto)")
	rootCmd.PersistentFlags().Float64("appearance-grid-spacing", defaultCfg.Appearance.Grid.Spacing, "spacing between tiles in grid layout")
	rootCmd.PersistentFlags().Float64("appearance-shadow-offset", defaultCfg.Appearance.Shadow.Offset, "shadow offset in pixels")
	rootCmd.PersistentFlags().Float64("appearance-shadow-blur", defaultCfg.Appearance.Shadow.Blur, "shadow blur radius")
	rootCmd.PersistentFlags().StringSlice("appearance-font-paths", defaultCfg.Appearance.Font.Paths, "font paths (primary first, then fallbacks)")
	rootCmd.PersistentFlags().Int("appearance-font-size", defaultCfg.Appearance.Font.Size, "font size")
	rootCmd.PersistentFlags().StringP("appearance-colors-theme", "t", defaultCfg.Appearance.Colors.Theme, "color theme (auto, dark, light)")
	rootCmd.PersistentFlags().String("appearance-colors-dark-background", defaultCfg.Appearance.Colors.Dark.Background, "dark theme background color")
	rootCmd.PersistentFlags().String("appearance-colors-dark-selection-frame", defaultCfg.Appearance.Colors.Dark.SelectionFrame, "dark theme selection frame color")
	rootCmd.PersistentFlags().String("appearance-colors-dark-text", defaultCfg.Appearance.Colors.Dark.Text, "dark theme text color")
	rootCmd.PersistentFlags().String("appearance-colors-dark-shadow", defaultCfg.Appearance.Colors.Dark.Shadow, "dark theme shadow color")
	rootCmd.PersistentFlags().String("appearance-colors-dark-inactive-frame", defaultCfg.Appearance.Colors.Dark.InactiveFrame, "dark theme inactive frame color")
	rootCmd.PersistentFlags().String("appearance-colors-light-background", defaultCfg.Appearance.Colors.Light.Background, "light theme background color")
	rootCmd.PersistentFlags().String("appearance-colors-light-selection-frame", defaultCfg.Appearance.Colors.Light.SelectionFrame, "light theme selection frame color")
	rootCmd.PersistentFlags().String("appearance-colors-light-text", defaultCfg.Appearance.Colors.Light.Text, "light theme text color")
	rootCmd.PersistentFlags().String("appearance-colors-light-shadow", defaultCfg.Appearance.Colors.Light.Shadow, "light theme shadow color")
	rootCmd.PersistentFlags().String("appearance-colors-light-inactive-frame", defaultCfg.Appearance.Colors.Light.InactiveFrame, "light theme inactive frame color")
	rootCmd.PersistentFlags().Bool("appearance-window-background-enabled", defaultCfg.Appearance.WindowBackground.Enabled, "enable semi-transparent background for entire window")
	rootCmd.PersistentFlags().Float64("appearance-window-background-opacity", defaultCfg.Appearance.WindowBackground.Opacity, "window background opacity (0.0-1.0)")
	rootCmd.PersistentFlags().Float64("appearance-window-background-border-radius", defaultCfg.Appearance.WindowBackground.BorderRadius, "window background corner radius in pixels")
	rootCmd.PersistentFlags().String("appearance-window-padding-horizontal", defaultCfg.Appearance.WindowPadding.Horizontal, "horizontal padding from screen edges (e.g., \"5%\" or \"50px\")")
	rootCmd.PersistentFlags().String("appearance-window-padding-vertical", defaultCfg.Appearance.WindowPadding.Vertical, "vertical padding from screen edges (e.g., \"5%\" or \"50px\")")

	// Behavior
	rootCmd.PersistentFlags().Duration("behavior-snapshot-interval", defaultCfg.Behavior.SnapshotInterval, "background thumbnail refresh interval")
	rootCmd.PersistentFlags().Duration("behavior-show-delay", defaultCfg.Behavior.ShowDelay, "delay before showing UI")

	// Windows
	rootCmd.PersistentFlags().String("windows-workspace", defaultCfg.Windows.Workspace, "workspace filter (current, all, all-except-current)")
	rootCmd.PersistentFlags().Bool("windows-ignore-skip-taskbar", defaultCfg.Windows.IgnoreSkipTaskbar, "ignore _NET_WM_STATE_SKIP_TASKBAR hint")
	rootCmd.PersistentFlags().Bool("windows-sort-minimized-last", defaultCfg.Windows.SortMinimizedLast, "sort minimized windows last")
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
	if rootCmd.PersistentFlags().Changed("grid") {
		gridMode, _ := rootCmd.PersistentFlags().GetBool("grid")
		if gridMode {
			cfg.Appearance.Layout = "grid"
		}
	}
	if rootCmd.PersistentFlags().Changed("appearance-layout") {
		cfg.Appearance.Layout, _ = rootCmd.PersistentFlags().GetString("appearance-layout")
	}
	if rootCmd.PersistentFlags().Changed("appearance-renderer") {
		cfg.Appearance.Renderer, _ = rootCmd.PersistentFlags().GetString("appearance-renderer")
	}
	if rootCmd.PersistentFlags().Changed("appearance-thumbnail-width") {
		cfg.Appearance.Thumbnail.Width, _ = rootCmd.PersistentFlags().GetInt("appearance-thumbnail-width")
	}
	if rootCmd.PersistentFlags().Changed("appearance-thumbnail-height") {
		cfg.Appearance.Thumbnail.Height, _ = rootCmd.PersistentFlags().GetInt("appearance-thumbnail-height")
	}
	if rootCmd.PersistentFlags().Changed("appearance-thumbnail-scaling-algorithm") {
		cfg.Appearance.Thumbnail.ScalingAlgorithm, _ = rootCmd.PersistentFlags().GetString("appearance-thumbnail-scaling-algorithm")
	}
	if rootCmd.PersistentFlags().Changed("appearance-spacing") {
		cfg.Appearance.Spacing, _ = rootCmd.PersistentFlags().GetFloat64("appearance-spacing")
	}
	if rootCmd.PersistentFlags().Changed("appearance-perspective") {
		cfg.Appearance.Perspective, _ = rootCmd.PersistentFlags().GetFloat64("appearance-perspective")
	}
	if rootCmd.PersistentFlags().Changed("appearance-grid-columns") {
		cfg.Appearance.Grid.Columns, _ = rootCmd.PersistentFlags().GetInt("appearance-grid-columns")
	}
	if rootCmd.PersistentFlags().Changed("appearance-grid-spacing") {
		cfg.Appearance.Grid.Spacing, _ = rootCmd.PersistentFlags().GetFloat64("appearance-grid-spacing")
	}
	if rootCmd.PersistentFlags().Changed("appearance-shadow-offset") {
		cfg.Appearance.Shadow.Offset, _ = rootCmd.PersistentFlags().GetFloat64("appearance-shadow-offset")
	}
	if rootCmd.PersistentFlags().Changed("appearance-shadow-blur") {
		cfg.Appearance.Shadow.Blur, _ = rootCmd.PersistentFlags().GetFloat64("appearance-shadow-blur")
	}
	if rootCmd.PersistentFlags().Changed("appearance-font-paths") {
		cfg.Appearance.Font.Paths, _ = rootCmd.PersistentFlags().GetStringSlice("appearance-font-paths")
	}
	if rootCmd.PersistentFlags().Changed("appearance-font-size") {
		cfg.Appearance.Font.Size, _ = rootCmd.PersistentFlags().GetInt("appearance-font-size")
	}
	if rootCmd.PersistentFlags().Changed("appearance-colors-theme") {
		cfg.Appearance.Colors.Theme, _ = rootCmd.PersistentFlags().GetString("appearance-colors-theme")
	}
	if rootCmd.PersistentFlags().Changed("appearance-colors-dark-background") {
		cfg.Appearance.Colors.Dark.Background, _ = rootCmd.PersistentFlags().GetString("appearance-colors-dark-background")
	}
	if rootCmd.PersistentFlags().Changed("appearance-colors-dark-selection-frame") {
		cfg.Appearance.Colors.Dark.SelectionFrame, _ = rootCmd.PersistentFlags().GetString("appearance-colors-dark-selection-frame")
	}
	if rootCmd.PersistentFlags().Changed("appearance-colors-dark-text") {
		cfg.Appearance.Colors.Dark.Text, _ = rootCmd.PersistentFlags().GetString("appearance-colors-dark-text")
	}
	if rootCmd.PersistentFlags().Changed("appearance-colors-dark-shadow") {
		cfg.Appearance.Colors.Dark.Shadow, _ = rootCmd.PersistentFlags().GetString("appearance-colors-dark-shadow")
	}
	if rootCmd.PersistentFlags().Changed("appearance-colors-dark-inactive-frame") {
		cfg.Appearance.Colors.Dark.InactiveFrame, _ = rootCmd.PersistentFlags().GetString("appearance-colors-dark-inactive-frame")
	}
	if rootCmd.PersistentFlags().Changed("appearance-colors-light-background") {
		cfg.Appearance.Colors.Light.Background, _ = rootCmd.PersistentFlags().GetString("appearance-colors-light-background")
	}
	if rootCmd.PersistentFlags().Changed("appearance-colors-light-selection-frame") {
		cfg.Appearance.Colors.Light.SelectionFrame, _ = rootCmd.PersistentFlags().GetString("appearance-colors-light-selection-frame")
	}
	if rootCmd.PersistentFlags().Changed("appearance-colors-light-text") {
		cfg.Appearance.Colors.Light.Text, _ = rootCmd.PersistentFlags().GetString("appearance-colors-light-text")
	}
	if rootCmd.PersistentFlags().Changed("appearance-colors-light-shadow") {
		cfg.Appearance.Colors.Light.Shadow, _ = rootCmd.PersistentFlags().GetString("appearance-colors-light-shadow")
	}
	if rootCmd.PersistentFlags().Changed("appearance-colors-light-inactive-frame") {
		cfg.Appearance.Colors.Light.InactiveFrame, _ = rootCmd.PersistentFlags().GetString("appearance-colors-light-inactive-frame")
	}
	if rootCmd.PersistentFlags().Changed("appearance-window-background-enabled") {
		cfg.Appearance.WindowBackground.Enabled, _ = rootCmd.PersistentFlags().GetBool("appearance-window-background-enabled")
	}
	if rootCmd.PersistentFlags().Changed("appearance-window-background-opacity") {
		cfg.Appearance.WindowBackground.Opacity, _ = rootCmd.PersistentFlags().GetFloat64("appearance-window-background-opacity")
	}
	if rootCmd.PersistentFlags().Changed("appearance-window-background-border-radius") {
		cfg.Appearance.WindowBackground.BorderRadius, _ = rootCmd.PersistentFlags().GetFloat64("appearance-window-background-border-radius")
	}
	if rootCmd.PersistentFlags().Changed("appearance-window-padding-horizontal") {
		cfg.Appearance.WindowPadding.Horizontal, _ = rootCmd.PersistentFlags().GetString("appearance-window-padding-horizontal")
	}
	if rootCmd.PersistentFlags().Changed("appearance-window-padding-vertical") {
		cfg.Appearance.WindowPadding.Vertical, _ = rootCmd.PersistentFlags().GetString("appearance-window-padding-vertical")
	}

	// Behavior
	if rootCmd.PersistentFlags().Changed("behavior-snapshot-interval") {
		cfg.Behavior.SnapshotInterval, _ = rootCmd.PersistentFlags().GetDuration("behavior-snapshot-interval")
	}
	if rootCmd.PersistentFlags().Changed("behavior-show-delay") {
		cfg.Behavior.ShowDelay, _ = rootCmd.PersistentFlags().GetDuration("behavior-show-delay")
	}

	// Windows
	if rootCmd.PersistentFlags().Changed("windows-workspace") {
		cfg.Windows.Workspace, _ = rootCmd.PersistentFlags().GetString("windows-workspace")
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

	// Setup profiling if requested
	if err := setupProfiling(cmd); err != nil {
		log.Error().Err(err).Msg("Failed to setup profiling")
	}
	defer cleanupProfiling(cmd)

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
	capturer, err := composite.NewCapturer(conn.Conn, conn.Root, cfg.Appearance.Thumbnail.ScalingAlgorithm)
	if err != nil {
		log.Warn().Err(err).Msg("Composite unavailable, thumbnails will be disabled")
	}

	// Create Focus Watcher to track active windows
	watcher, err := focus.NewWatcher(ctx, conn.Conn, conn.Root, mruList, capturer, cfg.Behavior.SnapshotInterval)
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

// handleKeyPress handles configured key press events for window switching
// Returns updated selector to preserve state
func handleKeyPress(ctx context.Context, conn *x11.Connection, e xproto.KeyPressEvent, selector *ui.Selector,
	mruList *mru.MRUList, watcher *focus.Watcher) *ui.Selector {
	// Apply show delay if configured
	if cfg.Behavior.ShowDelay > 0 {
		time.Sleep(cfg.Behavior.ShowDelay)
	}

	// Get full window list without workspace filtering (selector will handle it)
	// Only apply skip_taskbar and minimized sorting here
	filterOpts := x11.WindowFilterOptions{
		Workspace:         "all", // Always get all windows, selector will filter by workspace
		IgnoreSkipTaskbar: cfg.Windows.IgnoreSkipTaskbar,
		SortMinimizedLast: cfg.Windows.SortMinimizedLast,
	}
	windows, err := conn.GetWindowListFiltered(filterOpts)
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
		selector = ui.NewSelector(ctx, conn.Conn, conn.Root, windows, cfg.Appearance, cfg.Keybindings, cfg.Windows.Workspace, watcher)
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

// setupProfiling initializes CPU profiling and/or starts pprof HTTP server
func setupProfiling(cmd *cobra.Command) error {
	// Start CPU profiling if requested
	cpuprofile, _ := cmd.Flags().GetString("cpuprofile")
	if cpuprofile != "" {
		f, err := os.Create(cpuprofile)
		if err != nil {
			return fmt.Errorf("could not create CPU profile: %w", err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			f.Close()
			return fmt.Errorf("could not start CPU profile: %w", err)
		}
		log.Info().Str("file", cpuprofile).Msg("Started CPU profiling")
	}

	// Start pprof HTTP server if requested
	pprofAddr, _ := cmd.Flags().GetString("pprof")
	if pprofAddr != "" {
		go func() {
			log.Info().
				Str("addr", pprofAddr).
				Str("url", fmt.Sprintf("http://%s/debug/pprof/", pprofAddr)).
				Msg("Starting pprof HTTP server")

			if err := http.ListenAndServe(pprofAddr, nil); err != nil {
				log.Error().Err(err).Msg("pprof HTTP server failed")
			}
		}()
	}

	return nil
}

// cleanupProfiling writes memory profile if requested and stops CPU profiling
func cleanupProfiling(cmd *cobra.Command) {
	// Stop CPU profiling
	cpuprofile, _ := cmd.Flags().GetString("cpuprofile")
	if cpuprofile != "" {
		pprof.StopCPUProfile()
		log.Info().Str("file", cpuprofile).Msg("Stopped CPU profiling")
	}

	// Write memory profile if requested
	memprofile, _ := cmd.Flags().GetString("memprofile")
	if memprofile != "" {
		f, err := os.Create(memprofile)
		if err != nil {
			log.Error().Err(err).Str("file", memprofile).Msg("Could not create memory profile")
			return
		}
		defer f.Close()

		// Force GC before capturing heap profile for accurate results
		runtime.GC()

		if err := pprof.WriteHeapProfile(f); err != nil {
			log.Error().Err(err).Msg("Could not write memory profile")
			return
		}
		log.Info().Str("file", memprofile).Msg("Wrote memory profile")
	}
}
