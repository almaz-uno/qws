package carousel

import (
	"image"
	"math"
	"strings"

	"github.com/fogleman/gg"
	"golang.org/x/image/font"
	"golang.org/x/image/math/f64"
)

// Live thumbnails (specs/020-live-thumbnails): while the switcher is shown,
// the GLX presenter draws over each frame the latest pictures of the windows
// that changed, textures of the snapshotter's share group, where the frame
// has their thumbnails. A picture is drawn with the kernel of x/image/draw
// BiLinear, under the matrix the cpu renderer draws the thumbnail with, into
// the live rectangle: the pixels that are the thumbnail alone in the frame at
// rest — inside it, clear of its border, and of what is drawn over it later.
// Outside, the frame is the frame of 007.

// LiveItem is a live picture drawn over a frame
type LiveItem struct {
	Texture uint32            // the picture; its first row the top of the window
	Size    image.Point       // of the picture, the size of the thumbnail its card was drawn from
	Matrix  f64.Aff3          // from the texels of the picture to the frame, as DrawImage draws the thumbnail
	Rect    image.Rectangle   // the live rectangle, in pixels of the frame
	Holes   []image.Rectangle // pixels of Rect drawn over the thumbnail at rest
}

// LivePresenter is a presenter that draws live pictures over its frames: the
// GLX presenter, its context sharing the snapshotter's
type LivePresenter interface {
	// Live reports whether its context shares the objects of the snapshotter's
	Live() bool

	// SetLiveItems makes items drawn over the next frame presented that is not
	// a scene — a scene draws those its items carry — and over none after it
	SetLiveItems(items []LiveItem)

	// TakeLiveFence is a fence after the commands of the last frame that drew
	// a live picture, made since the last call, or 0; it is the caller's, to
	// be deleted by ReleaseFence once no longer needed
	TakeLiveFence() uintptr

	// ReleaseFence deletes a fence of TakeLiveFence; 0 is none
	ReleaseFence(fence uintptr)
}

// tileBorderHalf is the half width of the border of a tile's thumbnail, 1 px
// wide. The border of a card's is 2 px wide in the units of the thumbnail
// drawn at full size: gg strokes a path in pixels of the canvas, whatever the
// matrix, and drawWindowWithData divides the width by the scale it draws the
// thumbnail at (cardBorderHalf).
const tileBorderHalf = 0.5

// cardBorderHalf is the half width, in pixels of the frame, of the border of
// the thumbnail of a card
func cardBorderHalf(card cardGeometry) float64 {
	return 1 / (card.scaleMin * card.scale)
}

// coverMargin is how far around what a card draws its cover reaches, for the
// antialiasing of fills and strokes and the resampling of glyphs
const coverMargin = 2

// CardLive is the live item of the thumbnail of window index, but its
// texture, where the carousel draws the card at offset from the selection:
// integer at rest, fractional in a scene. Without holes: in a scene the
// cards after it are drawn after the item. False when the card is not drawn
// or has no live rectangle.
func CardLive(windowData []WindowData, index int, offset float64, cfg Config) (LiveItem, bool) {
	card, ok := carouselCard(&windowData[index], index, index, -offset,
		float64(cfg.Width)/2, float64(cfg.Height)/2, cfg)
	if !ok {
		return LiveItem{}, false
	}
	return liveItem(windowData[index].Thumbnail, card.thumbnailMatrix(), cardBorderHalf(card), cfg)
}

// CarouselLive is the live item, but its texture, of every card of the
// carousel at rest with selected at the centre and hover hovered, by window;
// a zero item where a card has none. Its holes are where the cards drawn after
// it draw over its live rectangle.
func CarouselLive(windowData []WindowData, selected, hover int, cfg Config) []LiveItem {
	items := make([]LiveItem, len(windowData))
	for k := range windowData {
		items[k], _ = CardLive(windowData, k, float64(k-selected), cfg)
	}
	for j := range windowData {
		covers := cardCover(windowData, j, selected, hover, cfg)
		for k := 0; k < j && len(covers) > 0; k++ {
			items[k].Holes = appendHoles(items[k].Holes, items[k].Rect, covers)
		}
	}
	return items
}

// GridLive is the live item, but its texture, of every tile of the grid, by
// window; a zero item where a tile has none. Its holes are where the icon,
// the title, the workspace and the urgent bar of the tile may lie over the
// thumbnail.
func GridLive(windowData []WindowData, cfg Config) []LiveItem {
	items := make([]LiveItem, len(windowData))
	g := layoutGrid(len(windowData), headerBand(cfg), cfg)
	canvas := image.Rect(0, 0, cfg.Width, cfg.Height)
	for i := range windowData {
		win := &windowData[i]
		if win.Thumbnail == nil {
			continue
		}
		x, y := g.tile(i)
		thumbX, thumbY, _, _, scale := gridThumbnail(win.Thumbnail.Bounds(), g.tileW, g.tileH)
		item, ok := liveItem(win.Thumbnail, gridImageMatrix(x, y, thumbX, thumbY, scale), tileBorderHalf, cfg)
		if !ok {
			continue
		}
		var covers []image.Rectangle
		if win.Icon != nil {
			if iconScale, has := gridIconScale(win.Icon.Bounds()); has {
				_, r := placement(canvas, gridImageMatrix(x, y, gridIconPadding, gridIconPadding, iconScale), win.Icon)
				covers = append(covers, r)
			}
		}
		titleY := g.tileH - gridTitleY
		if win.Title != "" {
			if face := cfg.face(float64(cfg.FontSize)); face != nil {
				title := truncateTitle(win.Title, g.tileW-20, face)
				covers = append(covers, textCover(face, title, x+g.tileW/2, y+titleY+8, 0.5, 0))
			}
			if win.Urgent {
				covers = append(covers, roundOut(x+5, y+titleY-5, g.tileW-10, 30, coverMargin))
			}
		}
		if win.Workspace != "" {
			if face := cfg.face(float64(cfg.FontSize - 2)); face != nil {
				covers = append(covers, textCover(face, win.Workspace, x+g.tileW/2, y+titleY+26, 0.5, 0))
			}
		}
		item.Holes = appendHoles(nil, item.Rect, covers)
		items[i] = item
	}
	return items
}

// liveItem is the item of a thumbnail drawn under the matrix m, its live
// rectangle the pixels of the frame inside it and clear of a border of half
// width border on its edges
func liveItem(thumb image.Image, m gg.Matrix, border float64, cfg Config) (LiveItem, bool) {
	b := thumb.Bounds()
	if b.Empty() {
		return LiveItem{}, false
	}
	// DrawImage draws the bounds of the image under the matrix; the texels
	// of the picture count from its corner
	m = m.Translate(float64(b.Min.X), float64(b.Min.Y))
	s2d := f64.Aff3{m.XX, m.XY, m.X0, m.YX, m.YY, m.Y0}
	x0, x1 := s2d[2], s2d[0]*float64(b.Dx())+s2d[2]
	y0, y1 := s2d[5], s2d[4]*float64(b.Dy())+s2d[5]
	for _, v := range [...]float64{x0, x1, y0, y1} {
		if math.IsNaN(v) || math.Abs(v) > 1<<24 {
			return LiveItem{}, false
		}
	}
	r := image.Rect(int(math.Ceil(x0+border)), int(math.Ceil(y0+border)),
		int(math.Floor(x1-border)), int(math.Floor(y1-border))).Intersect(image.Rect(0, 0, cfg.Width, cfg.Height))
	if r.Empty() {
		return LiveItem{}, false
	}
	return LiveItem{Size: b.Size(), Matrix: s2d, Rect: r}, true
}

// appendHoles appends the parts of rect the covers take
func appendHoles(holes []image.Rectangle, rect image.Rectangle, covers []image.Rectangle) []image.Rectangle {
	if rect.Empty() {
		return holes
	}
	for _, c := range covers {
		if h := c.Intersect(rect); !h.Empty() {
			holes = append(holes, h)
		}
	}
	return holes
}

// cardCover is rectangles of the canvas holding every pixel the card of
// window index draws in the carousel at rest with selected at the centre and
// hover hovered, as drawWindowWithData draws it; nil when it is not drawn
func cardCover(windowData []WindowData, index, selected, hover int, cfg Config) []image.Rectangle {
	data := &windowData[index]
	card, ok := carouselCard(data, index, selected, 0, float64(cfg.Width)/2, float64(cfg.Height)/2, cfg)
	if !ok {
		return nil
	}
	canvas := image.Rect(0, 0, cfg.Width, cfg.Height)
	x, y, scale, w, h := card.x, card.y, card.scale, card.finalW, card.finalH
	var covers []image.Rectangle
	if math.Abs(card.offset) < 3 {
		covers = append(covers, roundOut(x+cfg.ShadowOffset-w/2, y+cfg.ShadowOffset-h/2, w, h, coverMargin))
	}
	if data.Icon != nil {
		_, r := placement(canvas, card.iconMatrix(data.Icon), data.Icon)
		covers = append(covers, r)
	}
	if data.Title != "" {
		if face := cfg.face(float64(cfg.FontSize) * scale * 1.15); face != nil {
			title := strings.TrimSpace(data.Title)
			maxLen := max(int(30/scale), 10)
			if runes := []rune(title); len(runes) > maxLen {
				title = string(runes[:maxLen]) + "..."
			}
			covers = append(covers, labelCover(face, title, x, y-h/2-30*scale, 8*scale)...)
		}
	}
	if data.Workspace != "" {
		if face := cfg.face(float64(cfg.FontSize) * scale); face != nil {
			workspace := data.Workspace
			maxLen := max(int(20/scale), 8)
			if runes := []rune(workspace); len(runes) > maxLen {
				workspace = string(runes[:maxLen]) + "..."
			}
			covers = append(covers, labelCover(face, workspace, x, y+h/2+30*scale, 6*scale)...)
		}
	}
	// The thumbnail with its border, and the frames around it
	frame := cardBorderHalf(card)
	switch {
	case math.Abs(card.offset) < 0.01:
		frame = max(frame, 13) // the outer stroke of drawSelectionIndicator
	case index == hover && hover != selected:
		frame = max(frame, 10) // of drawHoverIndicator
	}
	covers = append(covers, roundOut(x-w/2, y-h/2, w, h, frame+coverMargin))
	return covers
}

// labelCover covers a label of the carousel: the text drawn centred at x, y
// on a rounded rectangle padded by padding, as drawWindowWithData draws a
// title or a workspace
func labelCover(face font.Face, text string, x, y, padding float64) []image.Rectangle {
	dc := gg.NewContext(1, 1)
	dc.SetFontFace(face)
	tw, th := dc.MeasureString(text)
	return []image.Rectangle{
		roundOut(x-tw/2-padding, y-th/2-padding, tw+2*padding, th+2*padding, coverMargin),
		textCover(face, text, x, y, 0.5, 0.5),
	}
}

// textCover covers the glyphs of text as gg's DrawStringAnchored draws them
// at x, y with the anchor ax, ay, under a matrix that translates only
func textCover(face font.Face, text string, x, y, ax, ay float64) image.Rectangle {
	dc := gg.NewContext(1, 1)
	dc.SetFontFace(face)
	tw, th := dc.MeasureString(text)
	x -= ax * tw
	y += ay * th
	b, _ := font.BoundString(face, text)
	return roundOut(x+float64(b.Min.X)/64, y+float64(b.Min.Y)/64,
		float64(b.Max.X-b.Min.X)/64, float64(b.Max.Y-b.Min.Y)/64, coverMargin)
}

// roundOut is the pixels within d of the rectangle at x, y of w×h
func roundOut(x, y, w, h, d float64) image.Rectangle {
	return image.Rect(int(math.Floor(x-d)), int(math.Floor(y-d)), int(math.Ceil(x+w+d)), int(math.Ceil(y+h+d)))
}
