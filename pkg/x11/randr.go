package x11

import (
	"fmt"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog/log"
)

// MonitorGeometry represents the geometry of a physical monitor
type MonitorGeometry struct {
	X      int
	Y      int
	Width  int
	Height int
}

// GetMonitors returns a list of all active monitors using XRandR
func GetMonitors(conn *xgb.Conn, root xproto.Window) ([]MonitorGeometry, error) {
	// Initialize RandR extension
	if err := randr.Init(conn); err != nil {
		return nil, fmt.Errorf("failed to initialize RandR extension: %w", err)
	}

	// Check RandR version (we need at least 1.3 for GetScreenResourcesCurrent)
	versionReply, err := randr.QueryVersion(conn, 1, 3).Reply()
	if err != nil {
		return nil, fmt.Errorf("failed to query RandR version: %w", err)
	}

	log.Debug().
		Uint32("major", versionReply.MajorVersion).
		Uint32("minor", versionReply.MinorVersion).
		Msg("RandR version")

	if versionReply.MajorVersion < 1 || (versionReply.MajorVersion == 1 && versionReply.MinorVersion < 3) {
		return nil, fmt.Errorf("RandR version 1.3+ required, got %d.%d",
			versionReply.MajorVersion, versionReply.MinorVersion)
	}

	// Get screen resources
	resources, err := randr.GetScreenResourcesCurrent(conn, root).Reply()
	if err != nil {
		return nil, fmt.Errorf("failed to get screen resources: %w", err)
	}

	var monitors []MonitorGeometry

	// Iterate through all outputs
	for _, output := range resources.Outputs {
		outputInfo, err := randr.GetOutputInfo(conn, output, 0).Reply()
		if err != nil {
			log.Warn().Err(err).Msg("Failed to get output info")
			continue
		}

		// Skip disconnected outputs or outputs without CRTC
		if outputInfo.Connection != randr.ConnectionConnected || outputInfo.Crtc == 0 {
			continue
		}

		// Get CRTC info to get actual geometry
		crtcInfo, err := randr.GetCrtcInfo(conn, outputInfo.Crtc, 0).Reply()
		if err != nil {
			log.Warn().Err(err).Msg("Failed to get CRTC info")
			continue
		}

		// Skip if no mode is set
		if crtcInfo.Width == 0 || crtcInfo.Height == 0 {
			continue
		}

		monitor := MonitorGeometry{
			X:      int(crtcInfo.X),
			Y:      int(crtcInfo.Y),
			Width:  int(crtcInfo.Width),
			Height: int(crtcInfo.Height),
		}

		log.Debug().
			Int("x", monitor.X).
			Int("y", monitor.Y).
			Int("width", monitor.Width).
			Int("height", monitor.Height).
			Msg("Found monitor")

		monitors = append(monitors, monitor)
	}

	if len(monitors) == 0 {
		return nil, fmt.Errorf("no active monitors found")
	}

	return monitors, nil
}

// GetPointerPosition returns the current position of the mouse pointer
func GetPointerPosition(conn *xgb.Conn, root xproto.Window) (x, y int, err error) {
	pointer, err := xproto.QueryPointer(conn, root).Reply()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to query pointer: %w", err)
	}

	return int(pointer.RootX), int(pointer.RootY), nil
}

// GetCurrentMonitor returns the monitor that contains the mouse pointer
// If multiple monitors contain the pointer, returns the first match
// Falls back to the first monitor if pointer is outside all monitors
func GetCurrentMonitor(conn *xgb.Conn, root xproto.Window) (MonitorGeometry, error) {
	monitors, err := GetMonitors(conn, root)
	if err != nil {
		return MonitorGeometry{}, err
	}

	if len(monitors) == 1 {
		log.Debug().Msg("Single monitor configuration")
		return monitors[0], nil
	}

	// Get pointer position
	x, y, err := GetPointerPosition(conn, root)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get pointer position, using first monitor")
		return monitors[0], nil
	}

	log.Debug().
		Int("pointer_x", x).
		Int("pointer_y", y).
		Msg("Pointer position")

	// Find monitor containing the pointer
	for _, monitor := range monitors {
		if x >= monitor.X && x < monitor.X+monitor.Width &&
			y >= monitor.Y && y < monitor.Y+monitor.Height {
			log.Debug().
				Int("monitor_x", monitor.X).
				Int("monitor_y", monitor.Y).
				Int("monitor_width", monitor.Width).
				Int("monitor_height", monitor.Height).
				Msg("Found monitor containing pointer")
			return monitor, nil
		}
	}

	// Pointer is outside all monitors, use first monitor
	log.Debug().Msg("Pointer outside all monitors, using first monitor")
	return monitors[0], nil
}

// GetMonitorForWindow returns the monitor that contains the largest part of the given window
// Falls back to the current monitor (by pointer) if window geometry cannot be determined
func GetMonitorForWindow(conn *xgb.Conn, root xproto.Window, window xproto.Window) (MonitorGeometry, error) {
	monitors, err := GetMonitors(conn, root)
	if err != nil {
		return MonitorGeometry{}, err
	}

	if len(monitors) == 1 {
		return monitors[0], nil
	}

	// Get window geometry
	geom, err := xproto.GetGeometry(conn, xproto.Drawable(window)).Reply()
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get window geometry, falling back to pointer-based monitor")
		return GetCurrentMonitor(conn, root)
	}

	// Translate coordinates to root window
	translate, err := xproto.TranslateCoordinates(conn, window, root, 0, 0).Reply()
	if err != nil {
		log.Warn().Err(err).Msg("Failed to translate coordinates, falling back to pointer-based monitor")
		return GetCurrentMonitor(conn, root)
	}

	windowX := int(translate.DstX)
	windowY := int(translate.DstY)
	windowW := int(geom.Width)
	windowH := int(geom.Height)

	log.Debug().
		Int("window_x", windowX).
		Int("window_y", windowY).
		Int("window_width", windowW).
		Int("window_height", windowH).
		Msg("Window geometry")

	// Find monitor with largest intersection
	var bestMonitor MonitorGeometry
	largestArea := 0

	for _, monitor := range monitors {
		// Calculate intersection rectangle
		x1 := max(windowX, monitor.X)
		y1 := max(windowY, monitor.Y)
		x2 := min(windowX+windowW, monitor.X+monitor.Width)
		y2 := min(windowY+windowH, monitor.Y+monitor.Height)

		if x2 > x1 && y2 > y1 {
			area := (x2 - x1) * (y2 - y1)
			if area > largestArea {
				largestArea = area
				bestMonitor = monitor
			}
		}
	}

	// If no intersection found, fall back to pointer-based monitor
	if largestArea == 0 {
		log.Debug().Msg("No monitor intersection with window, using pointer-based monitor")
		return GetCurrentMonitor(conn, root)
	}

	log.Debug().
		Int("monitor_x", bestMonitor.X).
		Int("monitor_y", bestMonitor.Y).
		Int("monitor_width", bestMonitor.Width).
		Int("monitor_height", bestMonitor.Height).
		Int("intersection_area", largestArea).
		Msg("Found monitor with largest window intersection")

	return bestMonitor, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
