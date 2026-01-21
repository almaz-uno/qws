package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config represents the complete application configuration
type Config struct {
	Keybindings Keybindings `mapstructure:"keybindings"`
	Appearance  Appearance  `mapstructure:"appearance"`
	Behavior    Behavior    `mapstructure:"behavior"`
	Windows     Windows     `mapstructure:"windows"`
	Log         Log         `mapstructure:"log"`
}

// Keybindings contains keyboard shortcut configuration
type Keybindings struct {
	Modifier          string `mapstructure:"modifier"`
	Key               string `mapstructure:"key"`
	Backward          string `mapstructure:"backward"`
	WorkspaceModifier string `mapstructure:"workspace_modifier"`
	Cancel            string `mapstructure:"cancel"`
}

// Appearance contains visual configuration
type Appearance struct {
	Layout           string           `mapstructure:"layout"`   // Layout mode: "carousel" or "grid"
	Renderer         string           `mapstructure:"renderer"` // Renderer backend: "cpu" or "glx"
	Thumbnail        Thumbnail        `mapstructure:"thumbnail"`
	Spacing          float64          `mapstructure:"spacing"`
	Perspective      float64          `mapstructure:"perspective"`
	Grid             Grid             `mapstructure:"grid"` // Grid-specific configuration
	Shadow           Shadow           `mapstructure:"shadow"`
	Font             Font             `mapstructure:"font"`
	Colors           Colors           `mapstructure:"colors"`
	WindowBackground WindowBackground `mapstructure:"window_background"`
	WindowPadding    WindowPadding    `mapstructure:"window_padding"`
}

// Thumbnail contains thumbnail size configuration
type Thumbnail struct {
	Width            int    `mapstructure:"width"`
	Height           int    `mapstructure:"height"`
	ScalingAlgorithm string `mapstructure:"scaling_algorithm"` // Scaling algorithm: "nearest", "bilinear", "catmull-rom"
}

// Grid contains grid layout configuration
type Grid struct {
	Columns int     `mapstructure:"columns"` // Number of columns (0 = auto)
	Spacing float64 `mapstructure:"spacing"` // Spacing between tiles in grid mode
}

// Shadow contains shadow effect configuration
type Shadow struct {
	Offset float64 `mapstructure:"offset"`
	Blur   float64 `mapstructure:"blur"`
}

// Font contains font configuration
type Font struct {
	Paths []string `mapstructure:"paths"` // Font paths (primary first, then fallbacks)
	Size  int      `mapstructure:"size"`
}

// Colors contains theme and color configuration
type Colors struct {
	Theme string     `mapstructure:"theme"`
	Dark  ThemeColor `mapstructure:"dark"`
	Light ThemeColor `mapstructure:"light"`
}

// ThemeColor contains colors for a specific theme
type ThemeColor struct {
	Background            string `mapstructure:"background"`
	SelectionFrame        string `mapstructure:"selection_frame"`
	Text                  string `mapstructure:"text"`
	Shadow                string `mapstructure:"shadow"`
	InactiveFrame         string `mapstructure:"inactive_frame"`
	UrgentTitleBackground string `mapstructure:"urgent_title_background"`
}

// WindowBackground contains window background configuration
type WindowBackground struct {
	Enabled      bool    `mapstructure:"enabled"`       // Enable semi-transparent background for entire window
	Opacity      float64 `mapstructure:"opacity"`       // Background opacity (0.0-1.0)
	BorderRadius float64 `mapstructure:"border_radius"` // Corner radius in pixels
}

// WindowPadding contains window padding configuration
type WindowPadding struct {
	Horizontal string `mapstructure:"horizontal"` // Horizontal padding from screen edges (e.g., "5%" or "50px")
	Vertical   string `mapstructure:"vertical"`   // Vertical padding from screen edges (e.g., "5%" or "50px")
}

// ParsePadding parses padding string and returns value in pixels
// Supports formats: "5%" (percentage of dimension) or "50px" (absolute pixels)
// Returns 0 on parse error
func ParsePadding(paddingStr string, dimension int) int {
	paddingStr = strings.TrimSpace(paddingStr)

	// Check for percentage
	if strings.HasSuffix(paddingStr, "%") {
		percentStr := strings.TrimSuffix(paddingStr, "%")
		percent, err := strconv.ParseFloat(percentStr, 64)
		if err != nil {
			return 0
		}
		return int(float64(dimension) * percent / 100.0)
	}

	// Check for pixels
	if strings.HasSuffix(paddingStr, "px") {
		pxStr := strings.TrimSuffix(paddingStr, "px")
		pixels, err := strconv.Atoi(pxStr)
		if err != nil {
			return 0
		}
		return pixels
	}

	// Try to parse as plain number (assume pixels)
	pixels, err := strconv.Atoi(paddingStr)
	if err != nil {
		return 0
	}
	return pixels
}

// Behavior contains application behavior configuration
type Behavior struct {
	SnapshotInterval time.Duration `mapstructure:"snapshot_interval"`
	ShowDelay        time.Duration `mapstructure:"show_delay"`
}

// Windows contains window filtering configuration
type Windows struct {
	Workspace         string `mapstructure:"workspace"`
	IgnoreSkipTaskbar bool   `mapstructure:"ignore_skip_taskbar"`
	SortMinimizedLast bool   `mapstructure:"sort_minimized_last"`
}

// Log contains logging configuration
type Log struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

// Default returns a configuration with default values
func Default() *Config {
	return &Config{
		Keybindings: Keybindings{
			Modifier:          "Alt",
			Key:               "Tab",
			Backward:          "Shift",
			WorkspaceModifier: "Ctrl",
			Cancel:            "Escape",
		},
		Appearance: Appearance{
			Layout:   "carousel", // Default to carousel mode
			Renderer: "cpu",      // Default to CPU renderer
			Thumbnail: Thumbnail{
				Width:            256,
				Height:           256,
				ScalingAlgorithm: "bilinear", // Balance between speed and quality
			},
			Spacing:     300,
			Perspective: 0.6,
			Grid: Grid{
				Columns: 0,  // Auto-calculate
				Spacing: 20, // Default grid spacing
			},
			Shadow: Shadow{
				Offset: 10,
				Blur:   15,
			},
			Font: Font{
				Paths: []string{
					"/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf",
					"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
				},
				Size: 14,
			},
			Colors: Colors{
				Theme: "auto",
				Dark: ThemeColor{
					Background:            "#1a1a2e",
					SelectionFrame:        "#4a9eff",
					Text:                  "#ffffff",
					Shadow:                "rgba(0, 0, 0, 0.8)",
					InactiveFrame:         "#404050",
					UrgentTitleBackground: "#d32f2f",
				},
				Light: ThemeColor{
					Background:            "#f5f5f5",
					SelectionFrame:        "#0078d4",
					Text:                  "#1a1a1a",
					Shadow:                "rgba(0, 0, 0, 0.3)",
					InactiveFrame:         "#cccccc",
					UrgentTitleBackground: "#e53935",
				},
			},
			WindowBackground: WindowBackground{
				Enabled:      true,
				Opacity:      0.85,
				BorderRadius: 20,
			},
			WindowPadding: WindowPadding{
				Horizontal: "20px",
				Vertical:   "20px",
			},
		},
		Behavior: Behavior{
			SnapshotInterval: 10 * time.Second,
			ShowDelay:        0,
		},
		Windows: Windows{
			Workspace:         "all",
			IgnoreSkipTaskbar: false,
			SortMinimizedLast: false,
		},
		Log: Log{
			Level:  "info",
			Format: "console",
		},
	}
}

// Load loads configuration from file, environment variables, and command-line flags
func Load(cfgFile string) (*Config, error) {
	v := viper.New()

	// Set default config file locations
	if cfgFile != "" {
		// Use config file from the flag
		v.SetConfigFile(cfgFile)
	} else {
		// Search config in home directory and XDG config directory
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}

		v.AddConfigPath(filepath.Join(home, ".config", "qws"))
		v.AddConfigPath(home)
		v.SetConfigType("yaml")
		v.SetConfigName("config")
	}

	// Set environment variable prefix
	v.SetEnvPrefix("QWS")
	v.AutomaticEnv()

	// Set defaults
	cfg := Default()
	setDefaults(v, cfg)

	// Read config file (optional)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		// Config file not found; using defaults
	}

	// Unmarshal config
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Ensure we have at least the defaults if nothing was configured
	if len(cfg.Appearance.Font.Paths) == 0 {
		defCfg := Default()
		cfg.Appearance.Font.Paths = defCfg.Appearance.Font.Paths
	}

	return cfg, nil
}

// setDefaults sets default values in viper
func setDefaults(v *viper.Viper, cfg *Config) {
	v.SetDefault("keybindings.modifier", cfg.Keybindings.Modifier)
	v.SetDefault("keybindings.key", cfg.Keybindings.Key)
	v.SetDefault("keybindings.backward", cfg.Keybindings.Backward)
	v.SetDefault("keybindings.workspace_modifier", cfg.Keybindings.WorkspaceModifier)
	v.SetDefault("keybindings.cancel", cfg.Keybindings.Cancel)

	v.SetDefault("appearance.layout", cfg.Appearance.Layout)
	v.SetDefault("appearance.renderer", cfg.Appearance.Renderer)
	v.SetDefault("appearance.thumbnail.width", cfg.Appearance.Thumbnail.Width)
	v.SetDefault("appearance.thumbnail.height", cfg.Appearance.Thumbnail.Height)
	v.SetDefault("appearance.spacing", cfg.Appearance.Spacing)
	v.SetDefault("appearance.perspective", cfg.Appearance.Perspective)
	v.SetDefault("appearance.shadow.offset", cfg.Appearance.Shadow.Offset)
	v.SetDefault("appearance.shadow.blur", cfg.Appearance.Shadow.Blur)
	v.SetDefault("appearance.font.paths", cfg.Appearance.Font.Paths)
	v.SetDefault("appearance.font.size", cfg.Appearance.Font.Size)

	v.SetDefault("appearance.colors.theme", cfg.Appearance.Colors.Theme)
	v.SetDefault("appearance.colors.dark.background", cfg.Appearance.Colors.Dark.Background)
	v.SetDefault("appearance.colors.dark.selection_frame", cfg.Appearance.Colors.Dark.SelectionFrame)
	v.SetDefault("appearance.colors.dark.text", cfg.Appearance.Colors.Dark.Text)
	v.SetDefault("appearance.colors.dark.shadow", cfg.Appearance.Colors.Dark.Shadow)
	v.SetDefault("appearance.colors.dark.inactive_frame", cfg.Appearance.Colors.Dark.InactiveFrame)
	v.SetDefault("appearance.colors.light.background", cfg.Appearance.Colors.Light.Background)
	v.SetDefault("appearance.colors.light.selection_frame", cfg.Appearance.Colors.Light.SelectionFrame)
	v.SetDefault("appearance.colors.light.text", cfg.Appearance.Colors.Light.Text)
	v.SetDefault("appearance.colors.light.shadow", cfg.Appearance.Colors.Light.Shadow)
	v.SetDefault("appearance.colors.light.inactive_frame", cfg.Appearance.Colors.Light.InactiveFrame)

	v.SetDefault("appearance.window_background.enabled", cfg.Appearance.WindowBackground.Enabled)
	v.SetDefault("appearance.window_background.opacity", cfg.Appearance.WindowBackground.Opacity)
	v.SetDefault("appearance.window_background.border_radius", cfg.Appearance.WindowBackground.BorderRadius)
	v.SetDefault("appearance.window_padding.horizontal", cfg.Appearance.WindowPadding.Horizontal)
	v.SetDefault("appearance.window_padding.vertical", cfg.Appearance.WindowPadding.Vertical)

	v.SetDefault("behavior.snapshot_interval", cfg.Behavior.SnapshotInterval)
	v.SetDefault("behavior.show_delay", cfg.Behavior.ShowDelay)

	v.SetDefault("windows.workspace", cfg.Windows.Workspace)
	v.SetDefault("windows.ignore_skip_taskbar", cfg.Windows.IgnoreSkipTaskbar)
	v.SetDefault("windows.sort_minimized_last", cfg.Windows.SortMinimizedLast)

	v.SetDefault("log.level", cfg.Log.Level)
	v.SetDefault("log.format", cfg.Log.Format)
}
