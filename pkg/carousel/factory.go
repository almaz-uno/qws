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

// NewRenderer creates a renderer based on backend name
// backend: "cpu" or "opengl"
func NewRenderer(backend string, width, height int) (Renderer, error) {
	switch backend {
	case "cpu":
		log.Info().Msg("Using CPU renderer")
		return NewCPURenderer(), nil

	case "glx":
		log.Info().Msg("Initializing OpenGL (GLX) renderer")
		renderer, err := NewOpenGLRenderer(width, height)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to initialize OpenGL renderer, falling back to CPU")
			return NewCPURenderer(), nil
		}
		log.Info().Msg("OpenGL renderer initialized successfully")
		return renderer, nil

	default:
		return nil, fmt.Errorf("unknown renderer backend: %s (supported: cpu, glx)", backend)
	}
}
