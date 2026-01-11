package carousel

import (
	"image"
	"image/color"
	"math"

	"github.com/fogleman/gg"
)

// WindowData contains window information for rendering
type WindowData struct {
	Thumbnail image.Image
	Icon      image.Image
	Title     string
}

// Config holds configuration for carousel rendering
type Config struct {
	Width             int     // Window width
	Height            int     // Window height
	ThumbWidth        int     // Thumbnail width
	ThumbHeight       int     // Thumbnail height
	Spacing           float64 // Spacing between thumbnails
	PerspectiveFactor float64 // Perspective distortion factor (0.0-1.0)
	ShadowOffset      float64 // Shadow offset
	ShadowBlur        float64 // Shadow blur radius
}

// DefaultConfig returns default carousel configuration
func DefaultConfig() Config {
	return Config{
		Width:             1200,
		Height:            400,
		ThumbWidth:        256,
		ThumbHeight:       256,
		Spacing:           300, // Increased to prevent overlap
		PerspectiveFactor: 0.6,
		ShadowOffset:      10,
		ShadowBlur:        15,
	}
}

// Draw3DCarousel renders a 2.5D carousel with perspective effect
// thumbnails: list of window thumbnails
// selected: index of currently selected window
// animOffset: animation offset for smooth transitions (0.0-1.0)
func Draw3DCarousel(thumbnails []image.Image, selected int, animOffset float64, cfg Config) *image.RGBA {
	dc := gg.NewContext(cfg.Width, cfg.Height)

	// Background - fully transparent
	dc.SetRGBA(0, 0, 0, 0)
	dc.Clear()

	centerX := float64(cfg.Width) / 2
	centerY := float64(cfg.Height) / 2

	// Draw each thumbnail with perspective transformation
	for i := range thumbnails {
		drawThumbnail(dc, thumbnails[i], i, selected, animOffset, centerX, centerY, cfg)
	}

	return getImageRGBA(dc)
}

// Draw3DCarouselWithData renders a 2.5D carousel with icons and titles
func Draw3DCarouselWithData(windowData []WindowData, selected int, animOffset float64, cfg Config) *image.RGBA {
	dc := gg.NewContext(cfg.Width, cfg.Height)

	// Background - fully transparent
	dc.SetRGBA(0, 0, 0, 0)
	dc.Clear()

	centerX := float64(cfg.Width) / 2
	centerY := float64(cfg.Height) / 2

	// Draw each window with icon, title, and thumbnail
	for i := range windowData {
		drawWindowWithData(dc, &windowData[i], i, selected, animOffset, centerX, centerY, cfg)
	}

	return getImageRGBA(dc)
}

// getImageRGBA converts gg.Context image to RGBA
func getImageRGBA(dc *gg.Context) *image.RGBA {
	img := dc.Image()
	rgba, ok := img.(*image.RGBA)
	if !ok {
		// Fallback: convert to RGBA
		bounds := img.Bounds()
		rgba = image.NewRGBA(bounds)
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				rgba.Set(x, y, img.At(x, y))
			}
		}
	}
	return rgba
}

// drawThumbnail draws a single thumbnail with perspective effect
func drawThumbnail(dc *gg.Context, thumb image.Image, index, selected int, animOffset, centerX, centerY float64, cfg Config) {
	if thumb == nil {
		return
	}

	// Position relative to center (with animation offset)
	offset := float64(index-selected) - animOffset

	// Don't draw items too far from center (performance optimization)
	if math.Abs(offset) > 5 {
		return
	}

	// Calculate transformation parameters
	var scale, x, y, alpha, rotation float64

	if math.Abs(offset) < 0.01 {
		// Central window — full size, no distortion
		scale = 1.0
		x = centerX
		y = centerY
		alpha = 1.0
		rotation = 0
	} else {
		// Side windows — reduced with perspective
		// Scale decreases with distance from center
		scale = cfg.PerspectiveFactor + (1.0-cfg.PerspectiveFactor)/(1.0+math.Abs(offset)*0.5)

		// Horizontal position with spacing
		x = centerX + offset*cfg.Spacing*scale

		// Vertical position (slight arc effect)
		arcHeight := math.Abs(offset) * 10
		y = centerY + arcHeight

		// Alpha transparency for distant items
		alpha = 0.5 + 0.5*scale

		// No rotation - keep cards flat
		rotation = 0
	}

	// Calculate thumbnail dimensions
	thumbBounds := thumb.Bounds()
	thumbW := float64(thumbBounds.Dx())
	thumbH := float64(thumbBounds.Dy())

	// Scale to fit within configured size
	scaleW := float64(cfg.ThumbWidth) / thumbW
	scaleH := float64(cfg.ThumbHeight) / thumbH
	scaleMin := math.Min(scaleW, scaleH)

	finalW := thumbW * scaleMin * scale
	finalH := thumbH * scaleMin * scale

	dc.Push()

	// Draw shadow first (behind the thumbnail)
	if math.Abs(offset) < 3 {
		drawShadow(dc, x, y, finalW, finalH, rotation, scale, cfg)
	}

	// Transform for thumbnail
	dc.Translate(x, y)
	dc.Rotate(rotation)
	dc.Scale(scaleMin*scale, scaleMin*scale)
	dc.Translate(-thumbW/2, -thumbH/2)

	// Draw thumbnail
	dc.SetRGBA(1, 1, 1, alpha)
	dc.DrawImage(thumb, 0, 0)

	// Draw border around thumbnail
	dc.SetRGBA(1, 1, 1, alpha*0.8)
	dc.SetLineWidth(2.0 / (scaleMin * scale)) // Adjust line width for scale
	dc.DrawRectangle(0, 0, thumbW, thumbH)
	dc.Stroke()

	dc.Pop()

	// Highlight selected item
	if math.Abs(offset) < 0.01 {
		drawSelectionIndicator(dc, x, y, finalW, finalH, cfg)
	}
}

// drawShadow draws a shadow behind the thumbnail
func drawShadow(dc *gg.Context, x, y, w, h, rotation, scale float64, cfg Config) {
	dc.Push()

	dc.Translate(x+cfg.ShadowOffset, y+cfg.ShadowOffset)
	dc.Rotate(rotation)

	// Shadow color with blur effect (approximated)
	shadowAlpha := 0.3 * scale
	dc.SetRGBA(0, 0, 0, shadowAlpha)
	dc.DrawRectangle(-w/2, -h/2, w, h)
	dc.Fill()

	dc.Pop()
}

// drawWindowWithData draws a window with icon, title, and thumbnail
func drawWindowWithData(dc *gg.Context, data *WindowData, index, selected int, animOffset, centerX, centerY float64, cfg Config) {
	if data == nil || data.Thumbnail == nil {
		return
	}

	// Position relative to center (with animation offset)
	offset := float64(index-selected) - animOffset

	// Don't draw items too far from center (performance optimization)
	if math.Abs(offset) > 5 {
		return
	}

	// Calculate transformation parameters
	var scale, x, y, alpha, rotation float64

	if math.Abs(offset) < 0.01 {
		// Central window — full size, no distortion
		scale = 1.0
		x = centerX
		y = centerY
		alpha = 1.0
		rotation = 0
	} else {
		// Side windows — reduced with perspective
		scale = cfg.PerspectiveFactor + (1.0-cfg.PerspectiveFactor)/(1.0+math.Abs(offset)*0.5)
		x = centerX + offset*cfg.Spacing*scale
		arcHeight := math.Abs(offset) * 10
		y = centerY + arcHeight
		alpha = 0.5 + 0.5*scale
		rotation = 0
	}

	// Calculate thumbnail dimensions
	thumbBounds := data.Thumbnail.Bounds()
	thumbW := float64(thumbBounds.Dx())
	thumbH := float64(thumbBounds.Dy())

	scaleW := float64(cfg.ThumbWidth) / thumbW
	scaleH := float64(cfg.ThumbHeight) / thumbH
	scaleMin := math.Min(scaleW, scaleH)

	finalW := thumbW * scaleMin * scale
	finalH := thumbH * scaleMin * scale

	// Icon size and position
	iconSize := 48.0 * scale
	iconY := y - finalH/2 - 80*scale // Above thumbnail

	// Title position
	titleY := y - finalH/2 - 30*scale // Between icon and thumbnail

	dc.Push()

	// Draw shadow
	if math.Abs(offset) < 3 {
		drawShadow(dc, x, y, finalW, finalH, rotation, scale, cfg)
	}

	// Draw icon (if available)
	if data.Icon != nil {
		iconBounds := data.Icon.Bounds()
		iconW := float64(iconBounds.Dx())
		iconH := float64(iconBounds.Dy())
		iconScale := iconSize / math.Max(iconW, iconH)

		dc.Push()
		dc.Translate(x, iconY)
		dc.Scale(iconScale, iconScale)
		dc.Translate(-iconW/2, -iconH/2)
		dc.SetRGBA(1, 1, 1, alpha)
		dc.DrawImage(data.Icon, 0, 0)
		dc.Pop()
	}

	// Draw title (if available)
	if data.Title != "" {
		fontSize := 16.0 * scale
		if err := dc.LoadFontFace("/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", fontSize); err == nil {
			title := data.Title
			// Truncate long titles by runes (Unicode characters), not bytes
			maxLen := int(30 / scale)
			if maxLen < 10 {
				maxLen = 10
			}
			runes := []rune(title)
			if len(runes) > maxLen {
				title = string(runes[:maxLen]) + "..."
			}

			// Measure text to draw background
			textWidth, textHeight := dc.MeasureString(title)
			padding := 8.0 * scale
			borderRadius := 6.0 * scale

			// Draw semi-transparent black rounded rectangle background
			dc.SetRGBA(0, 0, 0, 0.7*alpha)
			dc.DrawRoundedRectangle(
				x-textWidth/2-padding,
				titleY-textHeight/2-padding,
				textWidth+padding*2,
				textHeight+padding*2,
				borderRadius,
			)
			dc.Fill()

			// Draw title text
			dc.SetRGBA(1, 1, 1, alpha)
			dc.DrawStringAnchored(title, x, titleY, 0.5, 0.5)
		}
	}

	// Draw thumbnail
	dc.Translate(x, y)
	dc.Rotate(rotation)
	dc.Scale(scaleMin*scale, scaleMin*scale)
	dc.Translate(-thumbW/2, -thumbH/2)

	dc.SetRGBA(1, 1, 1, alpha)
	dc.DrawImage(data.Thumbnail, 0, 0)

	// Draw border around thumbnail
	dc.SetRGBA(1, 1, 1, alpha*0.8)
	dc.SetLineWidth(2.0 / (scaleMin * scale))
	dc.DrawRectangle(0, 0, thumbW, thumbH)
	dc.Stroke()

	dc.Pop()

	// Highlight selected item
	if math.Abs(offset) < 0.01 {
		drawSelectionIndicator(dc, x, y, finalW, finalH, cfg)
	}
}

// drawSelectionIndicator draws a highlight around selected thumbnail
func drawSelectionIndicator(dc *gg.Context, x, y, w, h float64, cfg Config) {
	dc.Push()

	dc.Translate(x, y)

	// Outer glow effect
	dc.SetRGBA(0.3, 0.6, 1.0, 0.5) // Blue glow
	dc.SetLineWidth(6)
	dc.DrawRectangle(-w/2-10, -h/2-10, w+20, h+20)
	dc.Stroke()

	// Inner highlight
	dc.SetRGBA(0.5, 0.8, 1.0, 0.8) // Lighter blue
	dc.SetLineWidth(3)
	dc.DrawRectangle(-w/2-5, -h/2-5, w+10, h+10)
	dc.Stroke()

	dc.Pop()
}

// DrawPlaceholder draws a placeholder image for missing thumbnails
func DrawPlaceholder(width, height int, title string) image.Image {
	dc := gg.NewContext(width, height)

	// Gradient background
	for y := 0; y < height; y++ {
		alpha := float64(y) / float64(height)
		dc.SetRGBA(0.2+alpha*0.3, 0.2+alpha*0.3, 0.3+alpha*0.3, 1.0)
		dc.DrawRectangle(0, float64(y), float64(width), 1)
		dc.Fill()
	}

	// Icon placeholder (window icon)
	centerX := float64(width) / 2
	centerY := float64(height) / 2

	// Draw simplified window icon
	dc.SetRGBA(0.6, 0.6, 0.7, 1.0)
	iconSize := 80.0
	dc.DrawRectangle(centerX-iconSize/2, centerY-iconSize/2, iconSize, iconSize)
	dc.Fill()

	// Window title bar
	dc.SetRGBA(0.4, 0.4, 0.5, 1.0)
	dc.DrawRectangle(centerX-iconSize/2, centerY-iconSize/2, iconSize, 20)
	dc.Fill()

	// Text label
	if title != "" {
		dc.SetRGBA(1, 1, 1, 0.9)
		if err := dc.LoadFontFace("/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", 14); err == nil {
			// Truncate long titles by runes (Unicode characters), not bytes
			runes := []rune(title)
			if len(runes) > 20 {
				title = string(runes[:20]) + "..."
			}
			dc.DrawStringAnchored(title, centerX, centerY+iconSize/2+20, 0.5, 0.5)
		}
	}

	return dc.Image()
}

// CreateGradientBackground creates a gradient background image
func CreateGradientBackground(width, height int, c1, c2 color.Color) image.Image {
	dc := gg.NewContext(width, height)

	r1, g1, b1, a1 := c1.RGBA()
	r2, g2, b2, a2 := c2.RGBA()

	for y := 0; y < height; y++ {
		t := float64(y) / float64(height)
		r := float64(r1)*(1-t) + float64(r2)*t
		g := float64(g1)*(1-t) + float64(g2)*t
		b := float64(b1)*(1-t) + float64(b2)*t
		a := float64(a1)*(1-t) + float64(a2)*t

		dc.SetRGBA(r/65535, g/65535, b/65535, a/65535)
		dc.DrawLine(0, float64(y), float64(width), float64(y))
		dc.Stroke()
	}

	return dc.Image()
}
