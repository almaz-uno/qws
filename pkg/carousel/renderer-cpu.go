package carousel

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/fogleman/gg"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

const (
	// fontSystem is the primary system font for rendering
	fontSystem = "/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf"
	// fontFallback is the fallback font if system font is unavailable
	fontFallback = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
)

// WindowData contains window information for rendering
type WindowData struct {
	Thumbnail image.Image
	Icon      image.Image
	Title     string
	Workspace string
	Urgent    bool // Urgency hint set (window demands attention)
}

// Config holds configuration for carousel rendering
type Config struct {
	Width                   int      // Window width
	Height                  int      // Window height
	ThumbWidth              int      // Thumbnail width
	ThumbHeight             int      // Thumbnail height
	Spacing                 float64  // Spacing between thumbnails
	PerspectiveFactor       float64  // Perspective distortion factor (0.0-1.0)
	ShadowOffset            float64  // Shadow offset
	ShadowBlur              float64  // Shadow blur radius
	FontPaths               []string // Font paths (primary first, then fallbacks)
	FontSize                int      // Font size
	BackgroundColor         string   // Background color (hex or rgba)
	SelectionFrame          string   // Selection frame color
	TextColor               string   // Text color
	ShadowColor             string   // Shadow color
	InactiveFrame           string   // Inactive frame color
	UrgentTitleBackground   string   // Urgent window title background color
	WindowBackgroundEnabled bool     // Enable semi-transparent background for entire window
	WindowBackgroundOpacity float64  // Background opacity (0.0-1.0)
	WindowBackgroundRadius  float64  // Corner radius in pixels
	LayoutMode              string   // Layout mode: "carousel" or "grid"
	GridColumns             int      // Number of columns for grid layout (0 = auto)
	GridSpacing             float64  // Spacing between tiles in grid mode
	GridMinThumbWidth       int      // The least width of a thumbnail of the grid, in the proportions of the window; past it the grid scrolls; 0: none (specs/036-grid-scroll)
	GridScroll              int      // How far the rows of a grid that scrolls are scrolled up, in pixels (specs/036-grid-scroll)
	Hostname                string   // Drawn large at the top left (empty draws none)
	Version                 string   // Drawn after the hostname (empty draws none)
	LayoutHint              string   // Drawn at the top right of the header, what the layout key does (specs/026-layout-keys); empty draws none
	AnimationHint           string   // Drawn left of LayoutHint, whether the animation is on (specs/035-slip-by-frame-rate); empty draws none
	Fonts                   *FontSet // Faces of a drawing that runs in parallel with others; nil: the shared cache
}

// face is the face of the configured fonts at size, from Fonts when set
func (c Config) face(size float64) *MultiFallbackFace {
	if c.Fonts != nil {
		return c.Fonts.face(c.FontPaths, size)
	}
	return NewMultiFallbackFace(c.FontPaths, size)
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
		FontPaths:         []string{fontSystem, fontFallback},
		FontSize:          14,
		BackgroundColor:   "#1a1a2e",
		SelectionFrame:    "#4a9eff",
		TextColor:         "#ffffff",
		ShadowColor:       "rgba(0, 0, 0, 0.8)",
		InactiveFrame:     "#404050",
	}
}

// parseColor parses color string in various formats: #RGB, #RRGGBB, rgba(r,g,b,a)
// Returns r, g, b, a in range 0.0-1.0
func parseColor(colorStr string) (float64, float64, float64, float64) {
	colorStr = strings.TrimSpace(colorStr)

	// Parse rgba(r, g, b, a) or rgb(r, g, b)
	if strings.HasPrefix(colorStr, "rgba(") && strings.HasSuffix(colorStr, ")") {
		inner := colorStr[5 : len(colorStr)-1]
		parts := strings.Split(inner, ",")
		if len(parts) == 4 {
			r, _ := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			g, _ := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			b, _ := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
			a, _ := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
			return r / 255.0, g / 255.0, b / 255.0, a
		}
	}

	if strings.HasPrefix(colorStr, "rgb(") && strings.HasSuffix(colorStr, ")") {
		inner := colorStr[4 : len(colorStr)-1]
		parts := strings.Split(inner, ",")
		if len(parts) == 3 {
			r, _ := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			g, _ := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			b, _ := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
			return r / 255.0, g / 255.0, b / 255.0, 1.0
		}
	}

	// Parse hex color #RRGGBB or #RGB
	if strings.HasPrefix(colorStr, "#") {
		hex := colorStr[1:]

		// #RGB format -> expand to #RRGGBB
		if len(hex) == 3 {
			hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
		}

		if len(hex) == 6 {
			r, _ := strconv.ParseUint(hex[0:2], 16, 8)
			g, _ := strconv.ParseUint(hex[2:4], 16, 8)
			b, _ := strconv.ParseUint(hex[4:6], 16, 8)
			return float64(r) / 255.0, float64(g) / 255.0, float64(b) / 255.0, 1.0
		}
	}

	// Default: white
	return 1.0, 1.0, 1.0, 1.0
}

// setColor sets drawing color from color string
func setColor(dc *gg.Context, colorStr string, alphaMultiplier float64) {
	r, g, b, a := parseColor(colorStr)
	dc.SetRGBA(r, g, b, a*alphaMultiplier)
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
func Draw3DCarouselWithData(windowData []WindowData, selected int, hoverIndex int, animOffset float64, cfg Config) *image.RGBA {
	// Background - semi-transparent if enabled, fully transparent otherwise
	dc := newCanvas(cfg)
	drawHeader(dc, cfg)

	centerX := float64(cfg.Width) / 2
	centerY := float64(cfg.Height) / 2

	// Resample the thumbnails and icons of the visible cards in parallel
	canvas := dc.Image().(*image.RGBA)
	thumbs := make([]*preparedImage, len(windowData))
	icons := make([]*preparedImage, len(windowData))
	var wg sync.WaitGroup
	for i := range windowData {
		card, ok := carouselCard(&windowData[i], i, selected, animOffset, centerX, centerY, cfg)
		if !ok {
			continue
		}
		wg.Go(func() {
			thumbs[i] = prepareOpaque(canvas.Bounds(), card.thumbnailMatrix(), windowData[i].Thumbnail)
		})
		if icon := windowData[i].Icon; icon != nil {
			wg.Go(func() {
				icons[i] = prepareOnUniform(canvas, card.iconMatrix(icon), icon)
			})
		}
	}
	wg.Wait()

	// Draw each window with icon, title, and thumbnail, the nearest to the
	// centre last (specs/038-carousel-cards)
	for _, i := range carouselOrder(len(windowData), float64(selected)+animOffset) {
		drawWindowWithData(dc, &windowData[i], i, selected, hoverIndex, animOffset, centerX, centerY, cfg, thumbs[i], icons[i])
	}

	return getImageRGBA(dc)
}

// The header: the hostname, large, and the version after it, at the top left
// of the overlay (specs/005-host-and-version)
const (
	headerMargin = 24.0 // from the left and top edges of the overlay
	headerGap    = 16.0 // between the hostname and the version, and below the band
	headerScale  = 2.5  // size of the hostname relative to the font size
	hintGap      = 32.0 // between the hints of the header, twice the gap (specs/035-slip-by-frame-rate)
)

// drawHeader draws the header and returns the bottom of its band, or 0 when
// there is nothing to draw. The hint of the layout key is a part of the
// header, drawn only with it: it never changes the band.
func drawHeader(dc *gg.Context, cfg Config) float64 {
	if cfg.Hostname == "" && cfg.Version == "" {
		return 0
	}
	large := cfg.face(float64(cfg.FontSize) * headerScale)
	small := cfg.face(float64(cfg.FontSize))
	if large == nil || small == nil {
		return 0
	}
	metrics := large.Metrics()
	baseline := headerMargin + float64(metrics.Ascent)/64

	x, end := headerMargin, headerMargin // where the next text starts; where the last ends
	if cfg.Hostname != "" {
		dc.SetFontFace(large)
		setColor(dc, cfg.TextColor, 0.9)
		dc.DrawString(cfg.Hostname, x, baseline)
		width, _ := dc.MeasureString(cfg.Hostname)
		end = x + width
		x = end + headerGap
	}
	if cfg.Version != "" {
		dc.SetFontFace(small)
		setColor(dc, cfg.TextColor, 0.6)
		dc.DrawString(cfg.Version, x, baseline)
		width, _ := dc.MeasureString(cfg.Version)
		end = x + width
	}
	drawHints(dc, cfg, small, baseline, end)
	return headerMargin + float64(metrics.Height)/64 + headerGap
}

// drawHints draws the hints of the header in a row at its top right, from
// the right: the hint of the layout key ending at the margin of the header
// from the right edge (specs/026-layout-keys), then that of the animation key
// ending hintGap before it, or at the margin without it
// (specs/035-slip-by-frame-rate). Each as the version — its face, its colour,
// its baseline. A hint is not drawn where it would come closer than the gap
// of the header to end, where the hostname or the version ends, and then no
// hint left of it either.
func drawHints(dc *gg.Context, cfg Config, face font.Face, baseline, end float64) {
	right := float64(cfg.Width) - headerMargin
	for _, hint := range []string{cfg.LayoutHint, cfg.AnimationHint} {
		if hint == "" {
			continue
		}
		dc.SetFontFace(face)
		width, _ := dc.MeasureString(hint)
		x := right - width
		if x < end+headerGap {
			return
		}
		setColor(dc, cfg.TextColor, 0.6)
		dc.DrawString(hint, x, baseline)
		right = x - hintGap
	}
}

// headerBand is the bottom of the band drawHeader takes, without drawing it
func headerBand(cfg Config) float64 {
	if cfg.Hostname == "" && cfg.Version == "" {
		return 0
	}
	large := cfg.face(float64(cfg.FontSize) * headerScale)
	small := cfg.face(float64(cfg.FontSize))
	if large == nil || small == nil {
		return 0
	}
	return headerMargin + float64(large.Metrics().Height)/64 + headerGap
}

// backgroundKey is what the rounded window background depends on
type backgroundKey struct {
	width, height   int
	color           string
	opacity, radius float64
}

// background keeps the last rounded window background drawn. It is the first
// thing drawn on a transparent canvas, so its pixels depend on its key alone,
// and a copy of it is the canvas it would have made.
var background struct {
	sync.Mutex
	key backgroundKey
	img *image.RGBA
}

// newCanvas is a canvas of the window size with the window background on it,
// its pixels from the free list of canvases: every one of them written
func newCanvas(cfg Config) *gg.Context {
	if !cfg.WindowBackgroundEnabled || cfg.WindowBackgroundRadius <= 0 {
		img, _ := takeImage(image.Rect(0, 0, cfg.Width, cfg.Height))
		dc := gg.NewContextForRGBA(img)
		if cfg.WindowBackgroundEnabled {
			// Draw regular rectangle
			setColor(dc, cfg.BackgroundColor, cfg.WindowBackgroundOpacity)
		} else {
			// Fully transparent
			dc.SetRGBA(0, 0, 0, 0)
		}
		dc.Clear()
		return dc
	}

	key := backgroundKey{cfg.Width, cfg.Height, cfg.BackgroundColor, cfg.WindowBackgroundOpacity, cfg.WindowBackgroundRadius}
	background.Lock()
	if background.img == nil || background.key != key {
		// Draw rounded rectangle
		dc := gg.NewContext(cfg.Width, cfg.Height)
		setColor(dc, cfg.BackgroundColor, cfg.WindowBackgroundOpacity)
		dc.DrawRoundedRectangle(0, 0, float64(cfg.Width), float64(cfg.Height), cfg.WindowBackgroundRadius)
		dc.Fill()
		background.key, background.img = key, getImageRGBA(dc)
	}
	img, _ := takeImage(background.img.Rect)
	for y := 0; y < len(img.Pix); y += img.Stride {
		copyRow(img.Pix[y:y+img.Stride], background.img.Pix[y:])
	}
	background.Unlock()

	// Leave the context as drawing the background does: its colour set, and
	// no current point
	dc := gg.NewContextForRGBA(img)
	setColor(dc, cfg.BackgroundColor, cfg.WindowBackgroundOpacity)
	return dc
}

// copyRow copies a row of pixels; copies of whole images go through it a row
// at a time. One copy of an image is one memmove, which the scheduler cannot
// preempt — the less so when it faults fresh pages in — and a collection of
// the garbage stops every goroutine and waits for it; the call per row checks
// for preemption (specs/007-animation, research).
//
//go:noinline
func copyRow(dst, src []byte) {
	copy(dst, src)
}

// getImageRGBA converts gg.Context image to RGBA
func getImageRGBA(dc *gg.Context) *image.RGBA {
	img := dc.Image()
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba
	}
	bounds := img.Bounds()
	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)
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
	setColor(dc, cfg.ShadowColor, scale)
	dc.DrawRectangle(-w/2, -h/2, w, h)
	dc.Fill()

	dc.Pop()
}

// The cards of the carousel (specs/039-carousel-row). The centre card is at
// scale 1, a card a cards from it at cardRatio^a; the cards stand in a row,
// cardGap apart in their nominal widths, and do not overlap. A card between
// two places moves straight from one to the other. The icon of a card stands
// above its thumbnail, centred across it.
const (
	cardRatio     = 0.7  // the scale a card keeps of the scale of the one before it
	cardGap       = 24.0 // between the nominal widths of two cards
	cardIconSize  = 72.0 // the icon of the centre card; the others at the square root of their scale
	cardIconGap   = 8.0  // between the icon and the top of its thumbnail
	cardMaxOffset = 5    // the cards further from the centre are not drawn
)

// cardIcon is the size of the icon of a card at scale s
func cardIcon(s float64) float64 { return cardIconSize * math.Sqrt(s) }

// cardScale is the scale of a card at the distance a ≥ 0 from the centre,
// in cards
func cardScale(a float64) float64 { return math.Pow(cardRatio, a) }

// cardPlace is how far from the centre of the overlay the centre of the card
// at the integer distance j ≥ 0 lies, for cards w wide at scale 1, in whole
// pixels
func cardPlace(j int, w float64) float64 {
	c := 0.0
	for i := 1; i <= j; i++ {
		c += (cardScale(float64(i-1))+cardScale(float64(i)))*w/2 + cardGap
	}
	return math.Round(c)
}

// cardGeometry is where the carousel puts the card of a window
type cardGeometry struct {
	offset, scale, x, y, alpha, rotation     float64
	thumbW, thumbH, scaleMin, finalW, finalH float64
	seenX, seenW                             float64 // the centre and the width of what is seen of the card
}

// carouselCard computes the geometry of the card of the window at index;
// false when the card is not drawn
func carouselCard(data *WindowData, index, selected int, animOffset, centerX, centerY float64, cfg Config) (cardGeometry, bool) {
	if data == nil || data.Thumbnail == nil {
		return cardGeometry{}, false
	}

	// Position relative to center (with animation offset)
	offset := float64(index-selected) - animOffset
	a := math.Abs(offset)
	if a > cardMaxOffset {
		return cardGeometry{}, false
	}
	sign := 1.0
	if offset < 0 {
		sign = -1
	}

	// Calculate thumbnail dimensions
	thumbBounds := data.Thumbnail.Bounds()
	thumbW := float64(thumbBounds.Dx())
	thumbH := float64(thumbBounds.Dy())
	scaleW := float64(cfg.ThumbWidth) / thumbW
	scaleH := float64(cfg.ThumbHeight) / thumbH
	scaleMin := math.Min(scaleW, scaleH)
	baseW := thumbW * scaleMin

	scale := cardScale(a)
	finalW := baseW * scale
	finalH := thumbH * scaleMin * scale

	nominal := float64(cfg.ThumbWidth)
	j := math.Floor(a)
	c0 := cardPlace(int(j), nominal)
	mid := c0 + (a-j)*(cardPlace(int(j)+1, nominal)-c0)
	x := centerX + sign*mid
	y := centerY
	alpha := 1.0
	if a >= 0.01 {
		alpha = 0.5 + 0.5*scale
	}
	if a < 0.01 {
		x = centerX
	}
	return cardGeometry{offset, scale, x, y, alpha, 0, thumbW, thumbH, scaleMin, finalW, finalH,
		x, finalW}, true
}

// carouselOrder is the indices of n cards with the selection at p, in the
// order they are drawn: the furthest from the centre first
func carouselOrder(n int, p float64) []int {
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		return math.Abs(float64(order[i])-p) > math.Abs(float64(order[j])-p)
	})
	return order
}

// CarouselOrder is carouselOrder for pkg/ui
func CarouselOrder(n int, p float64) []int { return carouselOrder(n, p) }

// CarouselIndexAt is the window whose card is under the point x, y of the
// carousel at rest with selected at the centre — the topmost of the cards
// there —, or -1
func CarouselIndexAt(windowData []WindowData, selected int, x, y float64, cfg Config) int {
	order := carouselOrder(len(windowData), float64(selected))
	for k := len(order) - 1; k >= 0; k-- {
		i := order[k]
		card, ok := carouselCard(&windowData[i], i, selected, 0, float64(cfg.Width)/2, float64(cfg.Height)/2, cfg)
		if ok && math.Abs(x-card.x) <= card.finalW/2 && math.Abs(y-card.y) <= card.finalH/2 {
			return i
		}
	}
	return -1
}

// thumbnailMatrix is the matrix drawWindowWithData draws the thumbnail under
func (c cardGeometry) thumbnailMatrix() gg.Matrix {
	return gg.Identity().
		Translate(c.x, c.y).
		Rotate(c.rotation).
		Scale(c.scaleMin*c.scale, c.scaleMin*c.scale).
		Translate(-c.thumbW/2, -c.thumbH/2)
}

// iconMatrix is the matrix drawWindowWithData draws the icon under: centred
// across the card, above its thumbnail (specs/039-carousel-row)
func (c cardGeometry) iconMatrix(icon image.Image) gg.Matrix {
	x, y, scale, w, h := c.iconPlace(icon)
	return gg.Identity().Translate(x, y).Scale(scale, scale).Translate(-w/2, -h/2)
}

// iconPlace is the centre of the icon, its scale and its size in pixels
func (c cardGeometry) iconPlace(icon image.Image) (x, y, scale, w, h float64) {
	b := icon.Bounds()
	w, h = float64(b.Dx()), float64(b.Dy())
	size := math.Min(cardIcon(c.scale), c.seenW)
	return c.seenX, c.y - c.finalH/2 - cardIconGap - size/2, size / math.Max(w, h), w, h
}

// cardLabel is a label of a card: a title or a workspace, its text cut to
// what is seen of the card, centred at x, y on a rounded rectangle
type cardLabel struct {
	text                  string
	size                  float64 // of the font
	x, y, padding, radius float64
}

// half is half the height of the background of a label of one line
func (l cardLabel) half(cfg Config) float64 {
	face := cfg.face(l.size)
	if face == nil {
		return l.padding
	}
	return float64(face.Metrics().Height)/64/2 + l.padding
}

// labels are the title and the workspace of the card under its thumbnail;
// an empty text for none
func (c cardGeometry) labels(data *WindowData, cfg Config) (title, workspace cardLabel) {
	title = cardLabel{size: float64(cfg.FontSize) * c.scale * 1.15, x: c.seenX, padding: 8 * c.scale, radius: 6 * c.scale}
	workspace = cardLabel{size: float64(cfg.FontSize) * c.scale, x: c.seenX, padding: 6 * c.scale, radius: 4 * c.scale}
	// 28 and 34 px at scale 1; on the smallest cards no less than keeps the
	// backgrounds off the thumbnail and off each other (specs/039-carousel-row)
	th, wh := title.half(cfg), workspace.half(cfg)
	title.y = c.y + c.finalH/2 + math.Max(28*c.scale, th+2)
	workspace.y = title.y + math.Max(34*c.scale, th+wh+2)
	if t := strings.TrimSpace(data.Title); t != "" {
		if face := cfg.face(title.size); face != nil {
			title.text = truncateTitle(t, c.seenW-2*title.padding, face)
		}
	}
	if data.Workspace != "" {
		if face := cfg.face(workspace.size); face != nil {
			workspace.text = truncateTitle(data.Workspace, c.seenW-2*workspace.padding, face)
		}
	}
	return title, workspace
}

// drawLabel draws a label on its background of colour bg at the opacity
// bgAlpha, its text at textAlpha
func drawLabel(dc *gg.Context, l cardLabel, bg string, bgAlpha, textAlpha float64, cfg Config) {
	if l.text == "" {
		return
	}
	face := cfg.face(l.size)
	if face == nil {
		return
	}
	dc.SetFontFace(face)
	textWidth, textHeight := dc.MeasureString(l.text)
	setColor(dc, bg, bgAlpha)
	dc.DrawRoundedRectangle(l.x-textWidth/2-l.padding, l.y-textHeight/2-l.padding, textWidth+l.padding*2, textHeight+l.padding*2, l.radius)
	dc.Fill()
	setColor(dc, cfg.TextColor, textAlpha)
	dc.DrawStringAnchored(l.text, l.x, l.y, 0.5, 0.5)
}

// drawWindowWithData draws a window with icon, title, and thumbnail; thumb and
// icon are its thumbnail and icon resampled in advance, or nil to draw them in
// place
func drawWindowWithData(dc *gg.Context, data *WindowData, index, selected, hoverIndex int, animOffset, centerX, centerY float64, cfg Config, thumb, icon *preparedImage) {
	card, ok := carouselCard(data, index, selected, animOffset, centerX, centerY, cfg)
	if !ok {
		return
	}
	offset, scale, x, y, alpha, rotation := card.offset, card.scale, card.x, card.y, card.alpha, card.rotation
	thumbW, thumbH, scaleMin, finalW, finalH := card.thumbW, card.thumbH, card.scaleMin, card.finalW, card.finalH

	dc.Push()

	// Draw shadow
	if math.Abs(offset) < 3 {
		drawShadow(dc, x, y, finalW, finalH, rotation, scale, cfg)
	}

	// The title and the workspace under the thumbnail
	title, workspace := card.labels(data, cfg)
	bg := cfg.BackgroundColor
	if data.Urgent {
		bg = cfg.UrgentTitleBackground
	}
	drawLabel(dc, title, bg, 0.7*alpha, alpha, cfg)
	drawLabel(dc, workspace, cfg.InactiveFrame, 0.6*alpha, alpha*0.9, cfg)

	// Draw thumbnail
	dc.Push()
	dc.Translate(x, y)
	dc.Rotate(rotation)
	dc.Scale(scaleMin*scale, scaleMin*scale)
	dc.Translate(-thumbW/2, -thumbH/2)

	dc.SetRGBA(1, 1, 1, alpha)
	if thumb == nil || !thumb.drawTo(dc.Image().(*image.RGBA)) {
		dc.DrawImage(data.Thumbnail, 0, 0)
	}

	// Draw border around thumbnail
	dc.SetRGBA(1, 1, 1, alpha*0.8)
	dc.SetLineWidth(2 * cardBorderHalf(card))
	dc.DrawRectangle(0, 0, thumbW, thumbH)
	dc.Stroke()
	dc.Pop()

	// Highlight selected item
	if math.Abs(offset) < 0.01 {
		drawSelectionIndicator(dc, x, y, finalW, finalH, cfg)
	}

	// Highlight hovered item (if different from selected)
	if index == hoverIndex && hoverIndex != selected {
		drawHoverIndicator(dc, x, y, finalW, finalH, cfg)
	}

	// The icon over all of it
	if data.Icon != nil {
		dc.Push()
		ix, iy, is, iw, ih := card.iconPlace(data.Icon)
		dc.Translate(ix, iy)
		dc.Scale(is, is)
		dc.Translate(-iw/2, -ih/2)
		dc.SetRGBA(1, 1, 1, alpha)
		if icon == nil || !icon.drawTo(dc.Image().(*image.RGBA)) {
			dc.DrawImage(data.Icon, 0, 0)
		}
		dc.Pop()
	}

	dc.Pop()
}

// drawSelectionIndicator draws a highlight around selected thumbnail
func drawSelectionIndicator(dc *gg.Context, x, y, w, h float64, cfg Config) {
	dc.Push()

	dc.Translate(x, y)

	// Outer glow effect
	setColor(dc, cfg.SelectionFrame, 0.5)
	dc.SetLineWidth(6)
	dc.DrawRectangle(-w/2-10, -h/2-10, w+20, h+20)
	dc.Stroke()

	// Inner highlight
	setColor(dc, cfg.SelectionFrame, 0.8)
	dc.SetLineWidth(3)
	dc.DrawRectangle(-w/2-5, -h/2-5, w+10, h+10)
	dc.Stroke()

	dc.Pop()
}

// drawHoverIndicator draws a highlight around hovered thumbnail
func drawHoverIndicator(dc *gg.Context, x, y, w, h float64, cfg Config) {
	dc.Push()

	dc.Translate(x, y)

	// Outer glow effect - yellow/orange for hover
	dc.SetRGBA(1.0, 0.7, 0.2, 0.4) // Orange glow
	dc.SetLineWidth(4)
	dc.DrawRectangle(-w/2-8, -h/2-8, w+16, h+16)
	dc.Stroke()

	// Inner highlight
	dc.SetRGBA(1.0, 0.85, 0.4, 0.7) // Lighter orange
	dc.SetLineWidth(2)
	dc.DrawRectangle(-w/2-4, -h/2-4, w+8, h+8)
	dc.Stroke()

	dc.Pop()
}

// DrawPlaceholder draws a placeholder image for missing thumbnails
func DrawPlaceholder(width, height int, title string) image.Image {
	dc := gg.NewContext(width, height)

	// Gradient background - optimized with LinearGradient
	gradient := gg.NewLinearGradient(0, 0, 0, float64(height))
	gradient.AddColorStop(0, color.RGBA{51, 51, 77, 255})    // RGB(0.2, 0.2, 0.3)
	gradient.AddColorStop(1, color.RGBA{128, 128, 153, 255}) // RGB(0.5, 0.5, 0.6)
	dc.SetFillStyle(gradient)
	dc.DrawRectangle(0, 0, float64(width), float64(height))
	dc.Fill()

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
		if err := dc.LoadFontFace(fontFallback, 14); err == nil {
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

	// Optimized gradient using LinearGradient
	gradient := gg.NewLinearGradient(0, 0, 0, float64(height))
	gradient.AddColorStop(0, c1)
	gradient.AddColorStop(1, c2)
	dc.SetFillStyle(gradient)
	dc.DrawRectangle(0, 0, float64(width), float64(height))
	dc.Fill()

	return dc.Image()
}

// DrawGridLayout renders windows in a grid layout (like Windows task switcher)
func DrawGridLayout(windowData []WindowData, selected int, hoverIndex int, cfg Config) *image.RGBA {
	// The heads of the tiles start before the canvas is made
	// (specs/019-grid-speed)
	var tiles *gridTiles
	if len(windowData) > 0 {
		tiles = startGridTiles(windowData, headerBand(cfg), selected, hoverIndex, cfg)
	}

	// Background - semi-transparent if enabled, fully transparent otherwise
	dc := newCanvas(cfg)
	drawHeader(dc, cfg)

	if tiles == nil {
		return getImageRGBA(dc)
	}
	g := tiles.grid
	if g.maxScroll == 0 {
		tiles.draw(dc)
		return getImageRGBA(dc)
	}

	// A grid that scrolls: the track of its scrollbar, the tiles in the view
	// at the scroll — under the header, which the rows of its band drawn
	// back cover —, the thumb (specs/036-grid-scroll)
	track, thumb := g.bar(cfg)
	drawGridBar(dc, track, cfg, gridTrackAlpha)
	img := dc.Image().(*image.RGBA)
	band := make([]byte, g.top*img.Stride)
	copy(band, img.Pix)
	tiles.draw(dc)
	copy(img.Pix, band)
	drawGridBar(dc, thumb, cfg, gridThumbAlpha)
	return getImageRGBA(dc)
}

// drawGridSelection draws the selection frame of a tile of size w×h whose
// top-left corner is the origin
func drawGridSelection(dc *gg.Context, w, h float64, cfg Config) {
	setColor(dc, cfg.SelectionFrame, 0.9)
	dc.SetLineWidth(4)
	dc.DrawRoundedRectangle(-2, -2, w+4, h+4, 10)
	dc.Stroke()

	// Inner glow
	setColor(dc, cfg.SelectionFrame, 0.4)
	dc.SetLineWidth(2)
	dc.DrawRoundedRectangle(0, 0, w, h, 8)
	dc.Stroke()
}

// drawGridHover draws the hover frame of a tile of size w×h whose top-left
// corner is the origin: an orange-yellow tint
func drawGridHover(dc *gg.Context, w, h float64) {
	dc.SetRGBA(1.0, 0.7, 0.2, 0.6)
	dc.SetLineWidth(3)
	dc.DrawRoundedRectangle(-1, -1, w+2, h+2, 9)
	dc.Stroke()
}

// gridLayout is where DrawGridLayout puts its tiles: in the window, at the
// scroll of its view
type gridLayout struct {
	cols                                    int
	tileW, tileH, offsetX, offsetY, spacing float64
	rows                                    int
	top                                     int // the top of the view: the bottom of the header, whole pixels down
	scroll                                  int // how far the rows are scrolled up, from 0 to maxScroll
	maxScroll                               int // 0: the grid fits on one screen (specs/036-grid-scroll)
}

// GridColumns is the number of columns the grid lays n tiles out in, row
// after row: appearance.grid.columns, or when it is 0 a number that suits n
// — at most 6, or with a least thumbnail as many as fit across at it
// (specs/036-grid-scroll, D3)
func GridColumns(n int, cfg Config) int {
	cols := cfg.GridColumns
	if cols <= 0 {
		// Auto-calculate columns based on window count and aspect ratio
		cols = int(math.Ceil(math.Sqrt(float64(n) * 1.5)))
		if cols < 2 {
			cols = 2
		}
		if least := gridLeast(cfg); least > 0 {
			cols = min(cols, gridFitColumns(least, cfg))
		} else if cols > 6 {
			cols = 6
		}
	}
	return cols
}

// gridSpacing is the spacing between and around the tiles
func gridSpacing(cfg Config) float64 {
	if cfg.GridSpacing == 0 {
		return 20 // Default spacing
	}
	return cfg.GridSpacing
}

// gridLeast is the least width of a thumbnail box of the grid: the least of
// the configuration, but no more than a tile may be — a box of
// (ThumbWidth + 20) × ThumbHeight, its height in the proportions of the
// window; 0: none (specs/036-grid-scroll, D1)
func gridLeast(cfg Config) float64 {
	if cfg.GridMinThumbWidth <= 0 || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0
	}
	least := float64(cfg.GridMinThumbWidth)
	least = math.Min(least, float64(cfg.ThumbWidth)+40-2*gridThumbPadding)
	least = math.Min(least, float64(cfg.ThumbHeight)*float64(cfg.Width)/float64(cfg.Height))
	return math.Max(least, 0)
}

// gridFitColumns is how many tiles of a thumbnail least wide fit across the
// window with their spacing, at least one
func gridFitColumns(least float64, cfg Config) int {
	spacing := gridSpacing(cfg)
	return max(1, int(math.Floor((float64(cfg.Width)-spacing)/(least+2*gridThumbPadding+spacing))))
}

// layoutGrid lays out n tiles in the window below top, the bottom of the
// header: on one screen when their thumbnails come out at least the least
// size; past that, the rows run below it, at the scroll of the view
// (specs/036-grid-scroll, D2)
func layoutGrid(n int, top float64, cfg Config) gridLayout {
	// Calculate grid dimensions
	cols := GridColumns(n, cfg)
	rows := (n + cols - 1) / cols

	spacing := gridSpacing(cfg)

	// Calculate tile size to fit all tiles in the window, below the header
	availableWidth := float64(cfg.Width) - spacing*(float64(cols)+1)
	availableHeight := float64(cfg.Height) - top - spacing*(float64(rows)+1)

	tileW := availableWidth / float64(cols)
	tileH := availableHeight / float64(rows)

	// Respect max thumbnail size
	maxTileW := float64(cfg.ThumbWidth) + 40          // Add padding for borders
	maxTileH := float64(cfg.ThumbHeight) + gridChrome // Add room for the icon and the title
	if tileW > maxTileW {
		tileW = maxTileW
	}
	if tileH > maxTileH {
		tileH = maxTileH
	}

	g := gridLayout{cols: cols, tileW: tileW, tileH: tileH, spacing: spacing, rows: rows,
		top: min(int(math.Ceil(top)), max(cfg.Height, 0))}
	least := gridLeast(cfg)
	aspect := 0.0
	if cfg.Width > 0 {
		aspect = float64(cfg.Height) / float64(cfg.Width)
	}
	totalGridW := float64(cols)*tileW + (float64(cols)+1)*spacing
	g.offsetX = (float64(cfg.Width) - totalGridW) / 2
	if least <= 0 || n == 0 || tileW-2*gridThumbPadding >= least && tileH-gridChrome >= least*aspect {
		// Center the grid in the window
		totalGridH := float64(rows)*tileH + (float64(rows)+1)*spacing
		g.offsetY = top + (float64(cfg.Height)-top-totalGridH)/2
		return g
	}

	// The thumbnail box in the proportions of the window, as wide as the
	// columns make it; the rows from the header down, centred when they
	// leave room
	g.tileH = math.Min((tileW-2*gridThumbPadding)*aspect, float64(cfg.ThumbHeight)) + gridChrome
	totalGridH := float64(rows)*g.tileH + (float64(rows)+1)*spacing
	g.offsetY = top + math.Max(0, (float64(cfg.Height)-top-totalGridH)/2)
	g.maxScroll = max(0, int(math.Ceil(g.offsetY+totalGridH-float64(cfg.Height))))
	g.scroll = min(max(cfg.GridScroll, 0), g.maxScroll)
	return g
}

// tile is the top-left corner of tile i in the window, at the scroll
func (g gridLayout) tile(i int) (float64, float64) {
	row := i / g.cols
	col := i % g.cols
	x := g.offsetX + g.spacing + float64(col)*(g.tileW+g.spacing)
	y := g.offsetY + g.spacing + float64(row)*(g.tileH+g.spacing) - float64(g.scroll)
	return x, y
}

// The scrollbar of a grid that scrolls (specs/036-grid-scroll, D6): its
// width, its distance from the right edge of the grid, the least length of
// its thumb, the opacity of the text colour the track and the thumb are
// drawn in
const (
	gridBarWidth   = 4
	gridBarMargin  = 4
	gridBarLeast   = 24
	gridTrackAlpha = 0.12
	gridThumbAlpha = 0.45
	gridBarRadius  = 2.0
	maxGridLayer   = 16384 // the largest side of a texture of OpenGL 4.6 core at least: no tiles layer is taller (research, D9)
)

// bar is the track of the scrollbar, beside the view, and its thumb at the
// scroll: the view's share of the rows, at the scroll's share of the
// largest, in whole pixels
func (g gridLayout) bar(cfg Config) (track, thumb image.Rectangle) {
	right := int(math.Round(g.offsetX + float64(g.cols)*g.tileW + float64(g.cols+1)*g.spacing))
	x1 := right - gridBarMargin
	s := int(math.Round(g.spacing))
	y0, y1 := g.top+s, max(cfg.Height-s, g.top+s)
	track = image.Rect(x1-gridBarWidth, y0, x1, y1)
	length := y1 - y0
	rows := float64(g.rows)*g.tileH + float64(g.rows+1)*g.spacing
	view := float64(cfg.Height - g.top)
	tl := min(max(gridBarLeast, int(math.Round(float64(length)*view/rows))), length)
	ty := y0
	if g.maxScroll > 0 {
		ty += int(math.Round(float64(length-tl) * float64(g.scroll) / float64(g.maxScroll)))
	}
	thumb = image.Rect(x1-gridBarWidth, ty, x1, ty+tl)
	return track, thumb
}

// drawGridBar draws the track or the thumb of the scrollbar, r, in the text
// colour at the opacity alpha
func drawGridBar(dc *gg.Context, r image.Rectangle, cfg Config, alpha float64) {
	if r.Empty() {
		return
	}
	setColor(dc, cfg.TextColor, alpha)
	dc.DrawRoundedRectangle(float64(r.Min.X), float64(r.Min.Y), float64(r.Dx()), float64(r.Dy()), gridBarRadius)
	dc.Fill()
}

// drawGridTile draws a single tile in grid layout under the identity matrix:
// its head, or the head drawn in advance if it can be used, then its tail. It
// returns whether the head drawn in advance was used.
func drawGridTile(dc *gg.Context, win *WindowData, x, y, w, h float64, isSelected bool, isHovered bool, cfg Config, head *tileHead) bool {
	if win == nil {
		return false
	}
	used := head != nil && head.drawTo(dc.Image().(*image.RGBA))
	if !used {
		drawGridTileHead(dc, win, x, y, w, h, isSelected, isHovered, cfg, nil)
	}
	drawGridTileTail(dc, win, x, y, w, h, isSelected, isHovered, cfg)
	return used
}

// The thumbnail, the icon and the title of a grid tile
const (
	gridThumbPadding = 10.0                          // around the thumbnail
	gridTitleSpace   = 60.0                          // below it, for the title (increased from 50 to 60)
	gridIconSize     = 72.0                          // the icon, at the top centre, above the thumbnail box (specs/037-grid-icons)
	gridIconBand     = gridIconSize * 2 / 3          // above the thumbnail box: the lower third of the icon lies on the thumbnail
	gridChrome       = gridTitleSpace + gridIconBand // the height of a tile but its thumbnail box
	gridTitleY       = 40.0                          // the top of the title, from the bottom of the tile (moved down from 35 to 40 for better spacing)
)

// drawGridTileHead draws the part of a tile under its title: the shadow of a
// selected or hovered tile, the background, the thumbnail with its border, the
// icon and the bar under an urgent title (specs/019-grid-speed); thumb is the
// thumbnail resampled in advance, or nil to draw it in place
func drawGridTileHead(dc *gg.Context, win *WindowData, x, y, w, h float64, isSelected bool, isHovered bool, cfg Config, thumb *preparedImage) {
	dc.Push()

	// Draw shadow
	if isSelected || isHovered {
		dc.Push()
		dc.Translate(x+gridShadowOffset(isSelected, cfg), y+gridShadowOffset(isSelected, cfg))
		setColor(dc, cfg.ShadowColor, 0.6)
		dc.DrawRoundedRectangle(0, 0, w, h, 8)
		dc.Fill()
		dc.Pop()
	}

	// Draw tile background
	dc.Translate(x, y)

	// Background with slight gradient
	setColor(dc, cfg.BackgroundColor, 0.3)
	dc.DrawRoundedRectangle(0, 0, w, h, 8)
	dc.Fill()

	// Draw thumbnail
	if win.Thumbnail != nil {
		thumbX, thumbY, scaledW, scaledH, scale := gridThumbnail(win.Thumbnail.Bounds(), w, h)

		dc.Push()
		dc.Translate(thumbX, thumbY)
		dc.Scale(scale, scale)
		if thumb == nil || !thumb.drawTo(dc.Image().(*image.RGBA)) {
			dc.DrawImage(win.Thumbnail, 0, 0)
		}
		dc.Pop()

		// Draw border around thumbnail
		setColor(dc, cfg.InactiveFrame, 0.5)
		dc.SetLineWidth(1)
		dc.DrawRectangle(thumbX, thumbY, scaledW, scaledH)
		dc.Stroke()
	}

	// Draw icon (if available) in top-left corner
	if win.Icon != nil {
		if iconX, iconY, iconScale, ok := gridIcon(win.Icon.Bounds(), w, h); ok {
			dc.Push()
			dc.Translate(iconX, iconY)
			dc.Scale(iconScale, iconScale)
			dc.DrawImage(win.Icon, 0, 0)
			dc.Pop()
		}
	}

	// Draw title background if urgent
	if win.Title != "" && win.Urgent {
		titleY := h - gridTitleY
		setColor(dc, cfg.UrgentTitleBackground, 0.9)
		dc.DrawRoundedRectangle(5, titleY-5, w-10, 30, 4)
		dc.Fill()
	}

	dc.Pop()
}

// drawGridTileTail draws the part of a tile over its head: the title, the
// workspace and the frame
func drawGridTileTail(dc *gg.Context, win *WindowData, x, y, w, h float64, isSelected bool, isHovered bool, cfg Config) {
	dc.Push()
	dc.Translate(x, y)

	// Draw title at bottom
	titleY := h - gridTitleY
	titleMaxWidth := w - 20

	if win.Title != "" {
		// Load font with fallback support
		fontFace := cfg.face(float64(cfg.FontSize))
		if fontFace != nil {
			dc.SetFontFace(fontFace)
			setColor(dc, cfg.TextColor, 1.0)

			// Truncate title if too long
			title := truncateTitle(win.Title, titleMaxWidth, fontFace)
			dc.DrawStringAnchored(title, w/2, titleY+8, 0.5, 0)
		}
	}

	// Draw workspace indicator (if present)
	if win.Workspace != "" {
		fontFace := cfg.face(float64(cfg.FontSize - 2))
		if fontFace != nil {
			dc.SetFontFace(fontFace)
			setColor(dc, cfg.TextColor, 0.6)
			dc.DrawStringAnchored(win.Workspace, w/2, titleY+26, 0.5, 0) // Moved from +22 to +26
		}
	}

	// Draw selection/hover frame
	if isSelected {
		drawGridSelection(dc, w, h, cfg)
	} else if isHovered {
		drawGridHover(dc, w, h)
	} else {
		// Normal frame
		setColor(dc, cfg.InactiveFrame, 0.3)
		dc.SetLineWidth(1)
		dc.DrawRoundedRectangle(0, 0, w, h, 8)
		dc.Stroke()
	}

	dc.Pop()
}

// gridShadowOffset is the offset of the shadow of a selected or a hovered
// tile
func gridShadowOffset(isSelected bool, cfg Config) float64 {
	if !isSelected {
		return cfg.ShadowOffset * 0.5 // Smaller shadow for hover
	}
	return cfg.ShadowOffset
}

// gridThumbnail is where drawGridTileHead puts a thumbnail of bounds b in a
// tile of size w×h: its top-left corner, relative to the tile's, its size and
// its scale
func gridThumbnail(b image.Rectangle, w, h float64) (x, y, scaledW, scaledH, scale float64) {
	thumbW := w - 2*gridThumbPadding
	thumbH := h - gridChrome // Reserve the band of the icon at top and space for the title at bottom

	// Scale thumbnail to fit
	imgW := float64(b.Dx())
	imgH := float64(b.Dy())
	scale = math.Min(thumbW/imgW, thumbH/imgH)
	scaledW = imgW * scale
	scaledH = imgH * scale

	// Centre the thumbnail across the tile
	x = gridThumbPadding + (thumbW-scaledW)/2
	y = gridThumbPadding + gridIconBand // at the top of the box, under the icon (specs/037-grid-icons)
	return x, y, scaledW, scaledH, scale
}

// gridIcon is where drawGridTileHead puts an icon of bounds b in a tile of
// size w×h: its top-left corner, relative to the tile's, and its scale; false
// when it draws none. The icon is centred across the tile at the top of the
// thumbnail box's band: its lower third lies on the thumbnail, which
// gridThumbnail puts at the top of its box. It is at most as wide as the box
// (specs/037-grid-icons).
func gridIcon(b image.Rectangle, w, h float64) (x, y, scale float64, ok bool) {
	imgW := float64(b.Dx())
	imgH := float64(b.Dy())
	if imgW <= 0 || imgH <= 0 {
		return 0, 0, 0, false
	}
	size := math.Max(1, math.Min(gridIconSize, w-2*gridThumbPadding))
	scale = math.Min(size/imgW, size/imgH)
	return (w - imgW*scale) / 2, gridThumbPadding, scale, true
}

// truncateTitle truncates title to fit within maxWidth, as measured by
// MeasureString of a gg.Context with the face: the title, or else the longest
// of its prefixes that fits with "..." after it.
//
// MeasureString sums the advances of the runes and the kerning of each pair
// in 26.6 fixed point and takes the whole pixels of the sum. The sum for a
// prefix with the dots is therefore the sum for the prefix, the kerning to the
// first dot and the sum for the dots: each rune is measured once rather than
// once for each prefix tried (specs/019-grid-speed).
func truncateTitle(title string, maxWidth float64, face font.Face) string {
	runes := []rune(title)
	if len(runes) == 0 {
		return title
	}
	// width is the width MeasureString gives for a sum of advances
	width := func(advance fixed.Int26_6) float64 { return float64(advance >> 6) }

	// sums[k] is the advance of runes[:k]
	sums := make([]fixed.Int26_6, len(runes)+1)
	for k, c := range runes {
		sums[k+1] = sums[k]
		if k > 0 {
			sums[k+1] += face.Kern(runes[k-1], c)
		}
		advance, _ := face.GlyphAdvance(c)
		sums[k+1] += advance
	}

	// Measure full title
	if width(sums[len(runes)]) <= maxWidth {
		return title
	}

	// The longest prefix that fits with the dots
	dot, _ := face.GlyphAdvance('.')
	dots := 3*dot + 2*face.Kern('.', '.')
	for length := len(runes) - 1; length > 0; length-- {
		if width(sums[length]+face.Kern(runes[length-1], '.')+dots) <= maxWidth {
			return string(runes[:length]) + "..."
		}
	}

	return "..."
}
