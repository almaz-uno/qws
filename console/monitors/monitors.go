package main

import (
	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/rs/zerolog/log"
)

func main() {
	// Connect to X11
	conn, err := x11.Connect()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to X11")
	}
	defer conn.Close()

	// Get all monitors
	monitors, err := x11.GetMonitors(conn.Conn, conn.Root)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to get monitors")
	}

	log.Info().Int("count", len(monitors)).Msg("Found monitors")
	for i, mon := range monitors {
		log.Info().
			Int("index", i).
			Int("x", mon.X).
			Int("y", mon.Y).
			Int("width", mon.Width).
			Int("height", mon.Height).
			Msg("Monitor")
	}

	// Get current monitor (by pointer)
	current, err := x11.GetCurrentMonitor(conn.Conn, conn.Root)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to get current monitor")
	}

	log.Info().
		Int("x", current.X).
		Int("y", current.Y).
		Int("width", current.Width).
		Int("height", current.Height).
		Msg("Current monitor (by pointer position)")

	// Get pointer position
	x, y, err := x11.GetPointerPosition(conn.Conn, conn.Root)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to get pointer position")
	}

	log.Info().
		Int("x", x).
		Int("y", y).
		Msg("Pointer position")
}
