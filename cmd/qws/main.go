package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/almaz-uno/qws/pkg/composite"
	"github.com/almaz-uno/qws/pkg/focus"
	"github.com/almaz-uno/qws/pkg/keygrab"
	"github.com/almaz-uno/qws/pkg/mru"
	"github.com/almaz-uno/qws/pkg/ui"
	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	// Setup zerolog with console writer
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	// Connect to X server
	conn, err := x11.Connect()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to X11")
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
	watcher, err := focus.NewWatcher(conn.Conn, conn.Root, mruList, capturer)
	if err != nil {
		log.Warn().Err(err).Msg("Focus watcher unavailable, MRU order will be disabled")
	}

	// Create key grabber
	grabber := keygrab.NewKeyGrabber(conn.Conn, conn.Root)

	// Grab Alt+Tab
	if err := grabber.GrabAltTab(); err != nil {
		log.Fatal().Err(err).Msg("Failed to grab Alt+Tab")
	}
	defer grabber.UngrabAll()

	// Handle signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Start goroutine to handle signals
	go func() {
		<-sigChan
		os.Exit(0)
	}()

	// Create selector once to preserve state between calls
	var selector *ui.Selector
	defer func() {
		if selector != nil {
			selector.Close()
		}
	}()

	// Main event loop
	for {
		event, err := conn.Conn.WaitForEvent()
		if err != nil {
			continue
		}

		switch e := event.(type) {
		case xproto.KeyPressEvent:
			selector = handleKeyPress(conn, e, selector, mruList, watcher)
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
func handleKeyPress(conn *x11.Connection, e xproto.KeyPressEvent, selector *ui.Selector,
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
			if img, ok := watcher.GetThumbnail(xproto.Window(windows[i].ID)); ok {
				windows[i].Preview = img
			}
		}
	}

	// Create or reuse selector
	if selector == nil {
		selector = ui.NewSelector(conn.Conn, conn.Root, windows)
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
