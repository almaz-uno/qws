package config

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// detectGSettingsTheme reads theme preference from gsettings (GNOME/GTK)
func detectGSettingsTheme(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Try modern color-scheme setting (GNOME 42+)
	cmd := exec.CommandContext(ctx, "gsettings", "get", "org.gnome.desktop.interface", "color-scheme")
	if output, err := cmd.Output(); err == nil {
		value := strings.TrimSpace(string(output))
		// Remove quotes if present
		value = strings.Trim(value, "'\"")

		if strings.Contains(value, "prefer-dark") {
			return "dark"
		}
		if strings.Contains(value, "prefer-light") {
			return "light"
		}
	}

	// Try gtk-theme setting (older systems)
	cmd = exec.CommandContext(ctx, "gsettings", "get", "org.gnome.desktop.interface", "gtk-theme")
	if output, err := cmd.Output(); err == nil {
		value := strings.TrimSpace(string(output))
		// Remove quotes if present
		value = strings.Trim(value, "'\"")

		if strings.Contains(strings.ToLower(value), "dark") {
			return "dark"
		}
	}

	return ""
}

// DetectTheme detects the current system theme (dark or light)
// Returns "dark" or "light"
func DetectTheme(ctx context.Context) string {
	// Method 1: Check gsettings (modern GNOME/GTK systems)
	if theme := detectGSettingsTheme(ctx); theme != "" {
		log.Debug().Str("source", "gsettings").Str("theme", theme).Msg("Detected theme")
		return theme
	}

	// Method 2: Check GTK_THEME environment variable
	if gtkTheme := os.Getenv("GTK_THEME"); gtkTheme != "" {
		if strings.Contains(strings.ToLower(gtkTheme), "dark") {
			log.Debug().Str("source", "GTK_THEME").Str("theme", "dark").Msg("Detected dark theme")
			return "dark"
		}
		log.Debug().Str("source", "GTK_THEME").Str("theme", "light").Msg("Detected light theme")
		return "light"
	}

	// Method 3: Check GTK 3.0 settings file
	if theme := detectGTK3Theme(); theme != "" {
		log.Debug().Str("source", "GTK settings").Str("theme", theme).Msg("Detected theme")
		return theme
	}

	// Fallback: use light theme as default
	log.Debug().Str("theme", "light").Msg("No theme detection method worked, using default")
	return "light"
}

// detectGTK3Theme reads GTK 3.0 settings to detect dark theme preference
func detectGTK3Theme() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	// Try GTK 3.0 settings
	settingsPath := filepath.Join(home, ".config", "gtk-3.0", "settings.ini")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return ""
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Check for gtk-application-prefer-dark-theme setting
		if strings.HasPrefix(line, "gtk-application-prefer-dark-theme") {
			if strings.Contains(line, "=") {
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					value := strings.TrimSpace(parts[1])
					if value == "1" || strings.ToLower(value) == "true" {
						return "dark"
					}
				}
			}
		}

		// Check for gtk-theme-name with "dark" in the name
		if strings.HasPrefix(line, "gtk-theme-name") {
			if strings.Contains(line, "=") {
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					value := strings.TrimSpace(parts[1])
					if strings.Contains(strings.ToLower(value), "dark") {
						return "dark"
					}
				}
			}
		}
	}

	return ""
}

// ResolveTheme resolves the theme setting to actual theme name
// If theme is "auto", detects system theme; otherwise returns the specified theme
func ResolveTheme(ctx context.Context, theme string) string {
	theme = strings.ToLower(strings.TrimSpace(theme))

	switch theme {
	case "dark":
		return "dark"
	case "light":
		return "light"
	case "auto", "":
		return DetectTheme(ctx)
	default:
		log.Warn().Str("theme", theme).Msg("Unknown theme setting, using auto-detection")
		return DetectTheme(ctx)
	}
}

// GetActiveTheme returns the active theme configuration based on the theme setting
func (c *Config) GetActiveTheme(ctx context.Context) ThemeColor {
	resolvedTheme := ResolveTheme(ctx, c.Appearance.Colors.Theme)

	if resolvedTheme == "dark" {
		return c.Appearance.Colors.Dark
	}
	return c.Appearance.Colors.Light
}
