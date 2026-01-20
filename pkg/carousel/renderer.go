package carousel

import "image"

// Renderer defines the interface for carousel rendering backends
type Renderer interface {
	// Draw3DCarousel renders a 2.5D carousel with perspective effect
	Draw3DCarousel(thumbnails []image.Image, selected int, animOffset float64, cfg Config) *image.RGBA

	// Draw3DCarouselWithData renders carousel with window data including icons and titles
	Draw3DCarouselWithData(windowData []WindowData, selected int, hover int, animOffset float64, cfg Config) *image.RGBA

	// DrawGridLayout renders windows in a grid layout
	DrawGridLayout(windowData []WindowData, selected int, hover int, cfg Config) *image.RGBA

	// DrawPlaceholder creates a placeholder thumbnail with window name
	DrawPlaceholder(width, height int, text string) image.Image
}
