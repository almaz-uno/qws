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
	"github.com/almaz-uno/qws/pkg/snapshot"
	"github.com/almaz-uno/qws/pkg/ui"
	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/jezek/xgb"
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

	// frameDumpDir receives the first frame of every activation (hidden flag
	// --debug-dump-frames, specs/001-rendering-speed)
	frameDumpDir string
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
	rootCmd.PersistentFlags().String("debug-dump-frames", "", "write the first frame of every activation to this directory")
	_ = rootCmd.PersistentFlags().MarkHidden("debug-dump-frames")

	// Keybindings
	rootCmd.PersistentFlags().StringP("keybindings-modifier", "m", defaultCfg.Keybindings.Modifier, "main modifier key (Alt, Super, Ctrl)")
	rootCmd.PersistentFlags().StringP("keybindings-key", "k", defaultCfg.Keybindings.Key, "main trigger key (Tab, grave, space, etc.)")
	rootCmd.PersistentFlags().String("keybindings-backward", defaultCfg.Keybindings.Backward, "modifier for reverse navigation")
	rootCmd.PersistentFlags().String("keybindings-workspace-modifier", defaultCfg.Keybindings.WorkspaceModifier, "modifier to filter current workspace")
	rootCmd.PersistentFlags().String("keybindings-cancel", defaultCfg.Keybindings.Cancel, "key to cancel selection")
	rootCmd.PersistentFlags().String("keybindings-layout-toggle", defaultCfg.Keybindings.LayoutToggle, "key to toggle the carousel and the grid while the switcher is shown (empty = none)")

	// Appearance
	rootCmd.PersistentFlags().StringP("appearance-layout", "l", defaultCfg.Appearance.Layout, "layout mode (carousel, grid)")
	rootCmd.PersistentFlags().Bool("grid", false, "use grid layout (shortcut for --appearance-layout=grid)")
	rootCmd.PersistentFlags().StringP("appearance-renderer", "r", defaultCfg.Appearance.Renderer, "renderer backend (cpu, glx)")
	rootCmd.PersistentFlags().Int("appearance-thumbnail-width", defaultCfg.Appearance.Thumbnail.Width, "thumbnail width in pixels")
	rootCmd.PersistentFlags().Int("appearance-thumbnail-height", defaultCfg.Appearance.Thumbnail.Height, "thumbnail height in pixels")
	rootCmd.PersistentFlags().String("appearance-thumbnail-scaling-algorithm", defaultCfg.Appearance.Thumbnail.ScalingAlgorithm, "thumbnail scaling algorithm (nearest, bilinear, catmull-rom)")
	rootCmd.PersistentFlags().Bool("appearance-thumbnail-live", defaultCfg.Appearance.Thumbnail.Live, "thumbnails follow their windows while the switcher is shown (glx renderer)")
	rootCmd.PersistentFlags().Duration("appearance-thumbnail-live-interval", defaultCfg.Appearance.Thumbnail.LiveInterval, "a shown window that changed is averaged again at most this often (0 = once a refresh)")
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
	rootCmd.PersistentFlags().Bool("appearance-header-enabled", defaultCfg.Appearance.Header.Enabled, "show the hostname and the version at the top left of the switcher")
	rootCmd.PersistentFlags().String("appearance-animation-enabled", defaultCfg.Appearance.Animation.Enabled, "animate the switcher (glx renderer): auto, true or false; auto: still while a VNC viewer is connected or the frames slip; false: every change at once; alone: true")
	rootCmd.PersistentFlags().Lookup("appearance-animation-enabled").NoOptDefVal = "true" // alone, as the bool flag it was (specs/031-animation-auto)
	rootCmd.PersistentFlags().Duration("appearance-animation-duration", defaultCfg.Appearance.Animation.Duration, "duration of every animation (0 = at once)")
	rootCmd.PersistentFlags().Bool("appearance-animation-step", defaultCfg.Appearance.Animation.Step, "animate the step of the selection")
	rootCmd.PersistentFlags().StringSlice("appearance-animation-show", defaultCfg.Appearance.Animation.Show, "effects of the appearance (fade, zoom, none)")
	rootCmd.PersistentFlags().StringSlice("appearance-animation-hide", defaultCfg.Appearance.Animation.Hide, "effects of the disappearance (fade, zoom, none)")
	rootCmd.PersistentFlags().StringSlice("appearance-animation-hover", defaultCfg.Appearance.Animation.Hover, "effects of the hover frame (fade, zoom, none)")
	rootCmd.PersistentFlags().Duration("appearance-animation-hover-duration", defaultCfg.Appearance.Animation.HoverDuration, "duration of the hover animation (0 = that of --appearance-animation-duration)")
	rootCmd.PersistentFlags().Float64("appearance-animation-overlay-zoom", defaultCfg.Appearance.Animation.OverlayZoom, "scale the switcher zooms from as it appears and to as it disappears")
	rootCmd.PersistentFlags().Float64("appearance-animation-hover-zoom", defaultCfg.Appearance.Animation.HoverZoom, "scale the hover frame zooms from as it comes and to as it goes")
	rootCmd.PersistentFlags().Duration("appearance-animation-locate-duration", defaultCfg.Appearance.Animation.LocateDuration, "duration of the selection frame converging onto its tile after a switch to the grid (0 = none)")
	rootCmd.PersistentFlags().Float64("appearance-animation-locate-zoom", defaultCfg.Appearance.Animation.LocateZoom, "scale, above 1, the selection frame converges from onto its tile after a switch to the grid")
	rootCmd.PersistentFlags().IntSlice("appearance-animation-vnc-ports", defaultCfg.Appearance.Animation.VNCPorts, "local ports of a VNC server of the display: a viewer connected on one makes the animation still under auto")

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
	var warnings []string
	cfg, warnings, err = config.Load(cfgFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Apply command-line flags over config
	applyFlags()

	// Setup logging
	setupLogging()
	for _, w := range warnings {
		log.Warn().Msg(w)
	}
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
	if rootCmd.PersistentFlags().Changed("keybindings-layout-toggle") {
		cfg.Keybindings.LayoutToggle, _ = rootCmd.PersistentFlags().GetString("keybindings-layout-toggle")
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
	if rootCmd.PersistentFlags().Changed("appearance-thumbnail-live") {
		cfg.Appearance.Thumbnail.Live, _ = rootCmd.PersistentFlags().GetBool("appearance-thumbnail-live")
	}
	if rootCmd.PersistentFlags().Changed("appearance-thumbnail-live-interval") {
		cfg.Appearance.Thumbnail.LiveInterval, _ = rootCmd.PersistentFlags().GetDuration("appearance-thumbnail-live-interval")
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
	if rootCmd.PersistentFlags().Changed("appearance-header-enabled") {
		cfg.Appearance.Header.Enabled, _ = rootCmd.PersistentFlags().GetBool("appearance-header-enabled")
	}
	if rootCmd.PersistentFlags().Changed("appearance-animation-enabled") {
		cfg.Appearance.Animation.Enabled, _ = rootCmd.PersistentFlags().GetString("appearance-animation-enabled")
	}
	if rootCmd.PersistentFlags().Changed("appearance-animation-duration") {
		cfg.Appearance.Animation.Duration, _ = rootCmd.PersistentFlags().GetDuration("appearance-animation-duration")
	}
	if rootCmd.PersistentFlags().Changed("appearance-animation-step") {
		cfg.Appearance.Animation.Step, _ = rootCmd.PersistentFlags().GetBool("appearance-animation-step")
	}
	if rootCmd.PersistentFlags().Changed("appearance-animation-show") {
		cfg.Appearance.Animation.Show, _ = rootCmd.PersistentFlags().GetStringSlice("appearance-animation-show")
	}
	if rootCmd.PersistentFlags().Changed("appearance-animation-hide") {
		cfg.Appearance.Animation.Hide, _ = rootCmd.PersistentFlags().GetStringSlice("appearance-animation-hide")
	}
	if rootCmd.PersistentFlags().Changed("appearance-animation-hover") {
		cfg.Appearance.Animation.Hover, _ = rootCmd.PersistentFlags().GetStringSlice("appearance-animation-hover")
	}
	if rootCmd.PersistentFlags().Changed("appearance-animation-hover-duration") {
		cfg.Appearance.Animation.HoverDuration, _ = rootCmd.PersistentFlags().GetDuration("appearance-animation-hover-duration")
	}
	if rootCmd.PersistentFlags().Changed("appearance-animation-overlay-zoom") {
		cfg.Appearance.Animation.OverlayZoom, _ = rootCmd.PersistentFlags().GetFloat64("appearance-animation-overlay-zoom")
	}
	if rootCmd.PersistentFlags().Changed("appearance-animation-hover-zoom") {
		cfg.Appearance.Animation.HoverZoom, _ = rootCmd.PersistentFlags().GetFloat64("appearance-animation-hover-zoom")
	}
	if rootCmd.PersistentFlags().Changed("appearance-animation-locate-duration") {
		cfg.Appearance.Animation.LocateDuration, _ = rootCmd.PersistentFlags().GetDuration("appearance-animation-locate-duration")
	}
	if rootCmd.PersistentFlags().Changed("appearance-animation-locate-zoom") {
		cfg.Appearance.Animation.LocateZoom, _ = rootCmd.PersistentFlags().GetFloat64("appearance-animation-locate-zoom")
	}
	if rootCmd.PersistentFlags().Changed("appearance-animation-vnc-ports") {
		cfg.Appearance.Animation.VNCPorts, _ = rootCmd.PersistentFlags().GetIntSlice("appearance-animation-vnc-ports")
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

	frameDumpDir, _ = cmd.Flags().GetString("debug-dump-frames")

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

	// Thumbnails of every visible window, averaged on the GPU
	// (specs/008-window-snapshots); without that, the focus watcher's of the
	// active window, scaled on the CPU, as in 1.0.0
	var capturer *composite.Capturer
	snap, err := snapshot.New(cfg.Behavior.SnapshotInterval, cfg.Appearance.Thumbnail.ScalingAlgorithm)
	if err != nil {
		log.Info().Err(err).Msg("Snapshots on the GPU unavailable, the active window is captured on the CPU")
		capturer, err = composite.NewCapturer(conn.Conn, conn.Root, cfg.Appearance.Thumbnail.ScalingAlgorithm)
		if err != nil {
			log.Warn().Err(err).Msg("Composite unavailable, thumbnails will be disabled")
		}
	} else {
		defer snap.Close()
	}

	// The windows of an activation from a model kept by events
	// (specs/003-window-list); without it, collected at each activation
	model, err := x11.NewModel()
	if err != nil {
		log.Info().Err(err).Msg("Window model unavailable, the list is collected at each activation")
	} else {
		defer model.Close()
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

	// Main event loop; events read while the switcher faded out come first
	var pending []xgb.Event
	for {
		var event xgb.Event
		if len(pending) > 0 {
			event, pending = pending[0], pending[1:]
		} else {
			var err error
			event, err = conn.Conn.WaitForEvent()
			if event == nil {
				// Connection closed or error - exit gracefully
				log.Debug().Err(err).Msg("Event loop terminated")
				return nil
			}
		}

		switch e := event.(type) {
		case xproto.KeyPressEvent:
			var read []xgb.Event
			selector, read = handleKeyPress(ctx, conn, e, selector, mruList, watcher, snap, model)
			pending = append(read, pending...)
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

// handleKeyPress handles configured key press events for window switching.
// It returns the selector, to preserve its state, and the events read while
// the switcher faded out, for the main loop to handle.
func handleKeyPress(ctx context.Context, conn *x11.Connection, e xproto.KeyPressEvent, selector *ui.Selector,
	mruList *mru.MRUList, watcher *focus.Watcher, snap *snapshot.Snapshotter, model *x11.Model) (*ui.Selector, []xgb.Event) {
	start := time.Now()

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
	var windows []x11.WindowInfo
	var err error
	listStart := time.Now()
	if model != nil {
		windows, err = model.List(filterOpts)
	} else {
		windows, err = conn.GetWindowListFiltered(filterOpts)
	}
	log.Debug().
		Bool("model", model != nil).
		Int("windows", len(windows)).
		Dur("ms", time.Since(listStart)).
		Msg("Window list")
	if err != nil {
		return selector, nil
	}

	if len(windows) == 0 {
		return selector, nil
	}

	// Sort windows by MRU order
	mruOrder := mruList.GetOrder()
	if len(mruOrder) > 0 {
		windows = x11.SortWindowsByMRU(windows, mruOrder)
	}

	// Fill thumbnails: the visible windows changed since their snapshot are
	// captured first, waiting for them at most 20 ms (specs/008-window-snapshots)
	if snap != nil {
		snap.Refresh(20 * time.Millisecond)
	}
	for i := range windows {
		id := xproto.Window(windows[i].ID)
		if snap != nil {
			if img, ok := snap.Thumbnail(id); ok {
				windows[i].Preview = img
				continue
			}
		}
		if watcher != nil {
			if img, ok := watcher.GetThumbnail(id); ok {
				windows[i].Preview = img
			}
		}
	}
	list := time.Since(start)

	// Create or reuse selector
	if selector == nil {
		var err error
		selector, err = ui.NewSelector(ctx, conn.Conn, conn.Root, windows, cfg.Appearance, cfg.Keybindings, cfg.Windows.Workspace, watcher, snap)
		if err != nil {
			log.Error().Err(err).Msg("Failed to create selector")
			return selector, nil
		}
		if frameDumpDir != "" {
			selector.SetFrameDump(frameDumpDir)
		}
		hostname, _ := os.Hostname()
		selector.SetHeader(hostname, version)
	} else {
		// Update window list, preserving position
		selector.UpdateWindows(windows)
	}

	selector.BeginActivation(start, list)
	if watcher != nil {
		watcher.PauseSnapshots(true)
		defer watcher.PauseSnapshots(false)
	}
	// No snapshot on change while the switcher is shown: from here, so that
	// the snapshots of the cards stay those of the window list; the live
	// thumbnails of specs/020-live-thumbnails run meanwhile, from the end of
	// the fade-in to the start of the fade-out
	if snap != nil {
		snap.Pause(true)
		defer snap.Pause(false)
	}
	selected, err := selector.Show()

	// Register selector window in watcher after Show() (when window is created)
	if watcher != nil && selector != nil {
		if windowID := selector.GetWindowID(); windowID != 0 {
			watcher.SetSwitcherWindow(windowID)
		}
	}

	// The chosen window is activated first, then the switcher fades out
	// (specs/007-animation)
	if err == nil && selected != nil {
		if err := conn.ActivateWindow(selected.ID); err == nil {
			// Important: send all commands to X server
			conn.Conn.Sync()
			log.Debug().
				Dur("since_choice_ms", time.Since(selector.ChosenAt())).
				Msg("Window activated")
		} else {
			log.Warn().Err(err).Uint32("window", uint32(selected.ID)).Msg("Failed to activate the chosen window")
		}
	}
	return selector, selector.FadeOut()
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
