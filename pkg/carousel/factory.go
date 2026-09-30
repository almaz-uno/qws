package carousel

import (
	"fmt"
	"image"

	"github.com/rs/zerolog/log"
)

// CPURenderer is a wrapper that uses the original CPU-based rendering functions
type CPURenderer struct{}

// NewCPURenderer creates a new CPU-based renderer
func NewCPURenderer() *CPURenderer {
	return &CPURenderer{}
}

// Draw3DCarousel renders using CPU
func (r *CPURenderer) Draw3DCarousel(thumbnails []image.Image, selected int, animOffset float64, cfg Config) *image.RGBA {
	return Draw3DCarousel(thumbnails, selected, animOffset, cfg)
}

// Draw3DCarouselWithData renders using CPU
func (r *CPURenderer) Draw3DCarouselWithData(windowData []WindowData, selected int, hover int, animOffset float64, cfg Config) *image.RGBA {
	return Draw3DCarouselWithData(windowData, selected, hover, animOffset, cfg)
}

// DrawGridLayout renders using CPU
func (r *CPURenderer) DrawGridLayout(windowData []WindowData, selected int, hover int, cfg Config) *image.RGBA {
	return DrawGridLayout(windowData, selected, hover, cfg)
}

// DrawPlaceholder renders using CPU
func (r *CPURenderer) DrawPlaceholder(width, height int, text string) image.Image {
	return DrawPlaceholder(width, height, text)
}

// NewBackend returns the renderer that draws frames and the presenter that
// shows them, by backend name. Frames are always drawn by the CPU renderer:
// "cpu" presents them with PutImage, "glx" through GLX on the overlay window,
// and falls back to "cpu" when GLX cannot be initialised.
func NewBackend(backend string) (Renderer, Presenter, error) {
	switch backend {
	case "cpu":
		log.Info().Msg("Using CPU renderer")
		return NewCPURenderer(), &x11Presenter{}, nil

	case "glx":
		log.Info().Msg("Initializing GLX presenter")
		presenter, err := newGLXPresenter()
		if err != nil {
			log.Warn().Err(err).Msg("Failed to initialize GLX presenter, falling back to CPU")
			return NewCPURenderer(), &x11Presenter{}, nil
		}
		log.Info().Msg("GLX presenter initialized successfully")
		return NewCPURenderer(), presenter, nil

	default:
		return nil, nil, fmt.Errorf("unknown renderer backend: %s (supported: cpu, glx)", backend)
	}
}
