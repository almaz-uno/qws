package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/almaz-uno/qws/pkg/composite"
	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog/log"
)

func main() {
	// Connect to X11
	conn, err := x11.Connect()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to X11")
	}
	defer conn.Close()

	// Initialize composite capturer
	capturer, err := composite.NewCapturer(conn.Conn, conn.Root)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize composite capturer")
	}

	// Create snapshot directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to get home directory")
	}
	snapshotDir := filepath.Join(homeDir, "twd", "snapshot")
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		log.Fatal().Err(err).Msg("Failed to create snapshot directory")
	}

	// Get list of all client windows
	windows, err := conn.GetClientList()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to get client list")
	}

	log.Info().Int("count", len(windows)).Msg("Found windows")

	// Capture all windows
	for i, window := range windows {
		if err := captureWindow(conn.Conn, capturer, window, snapshotDir); err != nil {
			log.Warn().
				Err(err).
				Uint32("window", uint32(window)).
				Msg("Failed to capture window")
			continue
		}
		log.Info().
			Int("index", i+1).
			Int("total", len(windows)).
			Uint32("window", uint32(window)).
			Msg("Captured window")
	}

	log.Info().Msg("All windows captured, exiting")
}

// captureWindow captures window image and saves it to disk
func captureWindow(conn *xgb.Conn, capturer *composite.Capturer, window xproto.Window, dir string) error {
	// Capture window thumbnail
	img, err := capturer.CaptureWindow(window, 512, 512)
	if err != nil {
		return fmt.Errorf("capture failed: %w", err)
	}

	// Get window title
	title := getWindowTitle(conn, window)
	if title == "" {
		title = "untitled"
	}

	// Sanitize title for filename
	sanitizedTitle := sanitizeFilename(title)

	// Create output file
	filename := filepath.Join(dir, fmt.Sprintf("%d-%s.png", window, sanitizedTitle))
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	// Encode and save as PNG
	if err := png.Encode(file, img); err != nil {
		return fmt.Errorf("failed to encode PNG: %w", err)
	}

	return nil
}

// getWindowTitle gets the window title from _NET_WM_NAME or WM_NAME
func getWindowTitle(conn *xgb.Conn, window xproto.Window) string {
	// Try _NET_WM_NAME first (UTF8_STRING)
	netWMName, err := xproto.InternAtom(conn, true, uint16(len("_NET_WM_NAME")), "_NET_WM_NAME").Reply()
	if err == nil {
		utf8String, err := xproto.InternAtom(conn, true, uint16(len("UTF8_STRING")), "UTF8_STRING").Reply()
		if err == nil {
			prop, err := xproto.GetProperty(conn, false, window, netWMName.Atom, utf8String.Atom, 0, 1024).Reply()
			if err == nil && prop.ValueLen > 0 {
				return string(prop.Value)
			}
		}
	}

	// Fallback to WM_NAME (STRING)
	prop, err := xproto.GetProperty(conn, false, window, xproto.AtomWmName, xproto.AtomString, 0, 1024).Reply()
	if err == nil && prop.ValueLen > 0 {
		return string(prop.Value)
	}

	return ""
}

// sanitizeFilename removes characters that are invalid in filenames
func sanitizeFilename(s string) string {
	// Replace invalid characters with underscore
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	sanitized := replacer.Replace(s)

	// Trim spaces and limit length
	sanitized = strings.TrimSpace(sanitized)
	if len(sanitized) > 100 {
		sanitized = sanitized[:100]
	}

	return sanitized
}
