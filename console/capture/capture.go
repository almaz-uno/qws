package main

import (
	"context"
	"fmt"
	"image/png"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/almaz-uno/qws/pkg/composite"
	"github.com/almaz-uno/qws/pkg/mru"
	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

func main() {
	// Connect to X11
	conn, err := x11.Connect()
	if err != nil {
		log.Fatalf("Failed to connect to X11: %v", err)
	}
	defer conn.Close()

	// Initialize composite capturer
	capturer, err := composite.NewCapturer(conn.Conn, conn.Root)
	if err != nil {
		log.Fatalf("Failed to initialize composite capturer: %v", err)
	}

	// Create thumbnails directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Failed to get home directory: %v", err)
	}
	thumbnailsDir := filepath.Join(homeDir, "twd", "thumbnails")
	if err := os.MkdirAll(thumbnailsDir, 0755); err != nil {
		log.Fatalf("Failed to create thumbnails directory: %v", err)
	}

	// Create MRU list (not really used here, but required for focus watcher)
	mruList := mru.NewMRUList()

	// Get _NET_ACTIVE_WINDOW atom
	netActiveWindow, err := conn.InternAtom("_NET_ACTIVE_WINDOW", true)
	if err != nil {
		log.Fatalf("Failed to get _NET_ACTIVE_WINDOW atom: %v", err)
	}

	// Subscribe to PropertyChange events on root window
	if err := xproto.ChangeWindowAttributesChecked(
		conn.Conn,
		conn.Root,
		xproto.CwEventMask,
		[]uint32{uint32(xproto.EventMaskPropertyChange)},
	).Check(); err != nil {
		log.Fatalf("Failed to subscribe to PropertyChange events: %v", err)
	}

	// Setup signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		cancel()
	}()

	// Event loop
	var lastActiveWindow xproto.Window

	for {
		select {
		case <-ctx.Done():
			return

		default:
			// Check for X11 events
			event, err := conn.Conn.PollForEvent()
			if err != nil {
				continue
			}

			if event == nil {
				// No events available, continue
				continue
			}

			// Handle PropertyNotify events
			switch e := event.(type) {
			case xproto.PropertyNotifyEvent:
				// Only interested in _NET_ACTIVE_WINDOW changes on root window
				if e.Window != conn.Root || e.Atom != netActiveWindow {
					continue
				}

				// Get the new active window
				activeWin, err := getActiveWindow(conn.Conn, conn.Root, netActiveWindow)
				if err != nil {
					continue
				}

				// Skip if same as last or if no window is active
				if activeWin == 0 || activeWin == lastActiveWindow {
					continue
				}

				lastActiveWindow = activeWin

				// Update MRU list
				mruList.Touch(activeWin)

				// Capture and save thumbnail
				captureThumbnail(capturer, activeWin, thumbnailsDir)
			}
		}
	}
}

// getActiveWindow returns the currently active window via _NET_ACTIVE_WINDOW
func getActiveWindow(conn *xgb.Conn, root xproto.Window, netActiveWindow xproto.Atom) (xproto.Window, error) {
	prop, err := xproto.GetProperty(conn, false, root,
		netActiveWindow,
		xproto.AtomWindow,
		0,
		1, // only need first window
	).Reply()
	if err != nil {
		return 0, fmt.Errorf("failed to read _NET_ACTIVE_WINDOW: %w", err)
	}

	if prop.ValueLen == 0 {
		return 0, nil
	}

	// Parse window ID from property value
	activeWin := xproto.Window(
		uint32(prop.Value[0]) |
			uint32(prop.Value[1])<<8 |
			uint32(prop.Value[2])<<16 |
			uint32(prop.Value[3])<<24,
	)

	return activeWin, nil
}

// captureThumbnail captures window image and saves it to disk
func captureThumbnail(capturer *composite.Capturer, window xproto.Window, dir string) error {
	// Capture window thumbnail
	img, err := capturer.CaptureWindow(window, 512, 512)
	if err != nil {
		return fmt.Errorf("capture failed: %w", err)
	}

	// Create output file
	filename := filepath.Join(dir, fmt.Sprintf("%d.png", window))
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
