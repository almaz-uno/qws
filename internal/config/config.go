package config

import (
	"fmt"
	"os"
	"path/filepath"
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
	Thumbnail   Thumbnail `mapstructure:"thumbnail"`
	Spacing     float64   `mapstructure:"spacing"`
	Perspective float64   `mapstructure:"perspective"`
	Shadow      Shadow    `mapstructure:"shadow"`
	Font        Font      `mapstructure:"font"`
	Colors      Colors    `mapstructure:"colors"`
}

// Thumbnail contains thumbnail size configuration
type Thumbnail struct {
	Width  int `mapstructure:"width"`
	Height int `mapstructure:"height"`
}

// Shadow contains shadow effect configuration
type Shadow struct {
	Offset float64 `mapstructure:"offset"`
	Blur   float64 `mapstructure:"blur"`
}

// Font contains font configuration
type Font struct {
	Primary  string `mapstructure:"primary"`
	Fallback string `mapstructure:"fallback"`
	Size     int    `mapstructure:"size"`
}

// Colors contains theme and color configuration
type Colors struct {
	Theme string     `mapstructure:"theme"`
	Dark  ThemeColor `mapstructure:"dark"`
	Light ThemeColor `mapstructure:"light"`
}

// ThemeColor contains colors for a specific theme
type ThemeColor struct {
	Background     string `mapstructure:"background"`
	SelectionFrame string `mapstructure:"selection_frame"`
	Text           string `mapstructure:"text"`
	Shadow         string `mapstructure:"shadow"`
	InactiveFrame  string `mapstructure:"inactive_frame"`
}

// Behavior contains application behavior configuration
type Behavior struct {
	SnapshotInterval time.Duration `mapstructure:"snapshot_interval"`
	ShowDelay        time.Duration `mapstructure:"show_delay"`
}

// Windows contains window filtering configuration
type Windows struct {
	Desktop           string `mapstructure:"desktop"`
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
			Thumbnail: Thumbnail{
				Width:  256,
				Height: 256,
			},
			Spacing:     300,
			Perspective: 0.6,
			Shadow: Shadow{
				Offset: 10,
				Blur:   15,
			},
			Font: Font{
				Primary:  "/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf",
				Fallback: "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
				Size:     14,
			},
			Colors: Colors{
				Theme: "auto",
				Dark: ThemeColor{
					Background:     "#1a1a2e",
					SelectionFrame: "#4a9eff",
					Text:           "#ffffff",
					Shadow:         "rgba(0, 0, 0, 0.8)",
					InactiveFrame:  "#404050",
				},
				Light: ThemeColor{
					Background:     "#f5f5f5",
					SelectionFrame: "#0078d4",
					Text:           "#1a1a1a",
					Shadow:         "rgba(0, 0, 0, 0.3)",
					InactiveFrame:  "#cccccc",
				},
			},
		},
		Behavior: Behavior{
			SnapshotInterval: 10 * time.Second,
			ShowDelay:        0,
		},
		Windows: Windows{
			Desktop:           "all",
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

	return cfg, nil
}

// setDefaults sets default values in viper
func setDefaults(v *viper.Viper, cfg *Config) {
	v.SetDefault("keybindings.modifier", cfg.Keybindings.Modifier)
	v.SetDefault("keybindings.key", cfg.Keybindings.Key)
	v.SetDefault("keybindings.backward", cfg.Keybindings.Backward)
	v.SetDefault("keybindings.workspace_modifier", cfg.Keybindings.WorkspaceModifier)
	v.SetDefault("keybindings.cancel", cfg.Keybindings.Cancel)

	v.SetDefault("appearance.thumbnail.width", cfg.Appearance.Thumbnail.Width)
	v.SetDefault("appearance.thumbnail.height", cfg.Appearance.Thumbnail.Height)
	v.SetDefault("appearance.spacing", cfg.Appearance.Spacing)
	v.SetDefault("appearance.perspective", cfg.Appearance.Perspective)
	v.SetDefault("appearance.shadow.offset", cfg.Appearance.Shadow.Offset)
	v.SetDefault("appearance.shadow.blur", cfg.Appearance.Shadow.Blur)
	v.SetDefault("appearance.font.primary", cfg.Appearance.Font.Primary)
	v.SetDefault("appearance.font.fallback", cfg.Appearance.Font.Fallback)
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

	v.SetDefault("behavior.snapshot_interval", cfg.Behavior.SnapshotInterval)
	v.SetDefault("behavior.show_delay", cfg.Behavior.ShowDelay)

	v.SetDefault("windows.desktop", cfg.Windows.Desktop)
	v.SetDefault("windows.ignore_skip_taskbar", cfg.Windows.IgnoreSkipTaskbar)
	v.SetDefault("windows.sort_minimized_last", cfg.Windows.SortMinimizedLast)

	v.SetDefault("log.level", cfg.Log.Level)
	v.SetDefault("log.format", cfg.Log.Format)
}
