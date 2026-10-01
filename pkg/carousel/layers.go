package carousel

import (
	"image"
	"math"

	"github.com/fogleman/gg"
)

// Layers of the animation of specs/007-animation: pieces of a frame that the
// CPU draws once, with the drawing code of both renderers, and the GLX
// presenter moves. Each layer is an image whose bounds are its place in the
// frame at rest.

// CarouselBase is the canvas of the carousel without its cards: the window
// background and the header
func CarouselBase(cfg Config) *image.RGBA {
	dc := newCanvas(cfg)
	drawHeader(dc, cfg)
	return getImageRGBA(dc)
}

// CardLayer is the card of window index as Draw3DCarouselWithData draws it
// at the integer offset from the selection — with the selection frame at
// offset 0, without the hover — on a transparent image; nil when the card is
// not drawn at that offset
func CardLayer(windowData []WindowData, index, offset int, cfg Config) *image.RGBA {
	centerX := float64(cfg.Width) / 2
	centerY := float64(cfg.Height) / 2
	card, ok := carouselCard(&windowData[index], index, index-offset, 0, centerX, centerY, cfg)
	if !ok {
		return nil
	}
	canvas := image.Rect(0, 0, cfg.Width, cfg.Height)

	// A band around the card, wide enough for its title; a card that reaches
	// its edge is drawn again on the whole canvas
	halfW := math.Max(card.finalW/2, 420) + cfg.ShadowOffset + 40
	band := image.Rect(
		int(card.x-halfW), int(card.y-card.finalH/2-130*card.scale-40),
		int(card.x+halfW)+1, int(card.y+card.finalH/2+70*card.scale+cfg.ShadowOffset+40)+1,
	).Intersect(canvas)
	img := drawCard(windowData, index, offset, band, cfg)
	if b := opaqueBounds(img); b.Empty() {
		return nil
	} else if b.Min.X == band.Min.X && band.Min.X > 0 || b.Min.Y == band.Min.Y && band.Min.Y > 0 ||
		b.Max.X == band.Max.X && band.Max.X < canvas.Max.X || b.Max.Y == band.Max.Y && band.Max.Y < canvas.Max.Y {
		img = drawCard(windowData, index, offset, canvas, cfg)
	}
	return crop(img, opaqueBounds(img))
}

// CardCenter is the centre and the scale of the card of window index at a
// fractional offset from the selection, as the carousel places it; false when
// the carousel does not draw it there
func CardCenter(windowData []WindowData, index int, offset float64, cfg Config) (x, y, scale float64, ok bool) {
	card, ok := carouselCard(&windowData[index], index, index, -offset,
		float64(cfg.Width)/2, float64(cfg.Height)/2, cfg)
	return card.x, card.y, card.scale, ok
}

// drawCard draws the card onto a transparent image with bounds r
func drawCard(windowData []WindowData, index, offset int, r image.Rectangle, cfg Config) *image.RGBA {
	dc := gg.NewContext(r.Dx(), r.Dy())
	dc.Translate(float64(-r.Min.X), float64(-r.Min.Y))
	drawWindowWithData(dc, &windowData[index], index, index-offset, -1, 0,
		float64(cfg.Width)/2, float64(cfg.Height)/2, cfg, nil, nil)
	img := getImageRGBA(dc)
	img.Rect = r
	return img
}

// GridTiles is the tiles of the grid, none selected, tile hover hovered, as
// DrawGridLayout draws them, on a transparent image; nil without tiles. Over
// CarouselBase, with GridShadow under it and GridSelection over it at a tile,
// it is the grid with that tile selected — but for the frame of the tile
// under the selection frame (specs/007-animation).
func GridTiles(windowData []WindowData, hover int, cfg Config) *image.RGBA {
	dc := gg.NewContext(cfg.Width, cfg.Height)
	g := layoutGrid(len(windowData), headerBand(cfg), cfg)
	for i := range windowData {
		x, y := g.tile(i)
		drawGridTile(dc, &windowData[i], x, y, g.tileW, g.tileH, false, i == hover, cfg)
	}
	img := getImageRGBA(dc)
	b := opaqueBounds(img)
	if b.Empty() {
		return nil
	}
	return crop(img, b)
}

// GridShadow is the shadow of a selected grid tile of size w×h, on a
// transparent image whose bounds are relative to the tile's top-left corner
func GridShadow(w, h float64, cfg Config) *image.RGBA {
	const margin = 2
	o := cfg.ShadowOffset
	r := image.Rect(int(math.Floor(o))-margin, int(math.Floor(o))-margin,
		int(math.Ceil(o+w))+margin, int(math.Ceil(o+h))+margin)
	dc := gg.NewContext(r.Dx(), r.Dy())
	dc.Translate(o-float64(r.Min.X), o-float64(r.Min.Y))
	setColor(dc, cfg.ShadowColor, 0.6)
	dc.DrawRoundedRectangle(0, 0, w, h, 8)
	dc.Fill()
	img := getImageRGBA(dc)
	img.Rect = r
	return img
}

// GridTile is the rectangle of tile i in the grid: its top-left corner and
// size
func GridTile(n, i int, cfg Config) (x, y, w, h float64) {
	g := layoutGrid(n, headerBand(cfg), cfg)
	x, y = g.tile(i)
	return x, y, g.tileW, g.tileH
}

// GridSelection is the selection frame of a grid tile of size w×h, on a
// transparent image whose bounds are relative to the tile's top-left corner
func GridSelection(w, h float64, cfg Config) *image.RGBA {
	const margin = 8
	r := image.Rect(-margin, -margin, int(math.Ceil(w))+margin, int(math.Ceil(h))+margin)
	dc := gg.NewContext(r.Dx(), r.Dy())
	dc.Translate(margin, margin)
	drawGridSelection(dc, w, h, cfg)
	img := getImageRGBA(dc)
	img.Rect = r
	return img
}

// opaqueBounds is the smallest rectangle holding every pixel of img that is
// not fully transparent
func opaqueBounds(img *image.RGBA) image.Rectangle {
	r := img.Rect
	minX, minY, maxX, maxY := r.Max.X, r.Max.Y, r.Min.X, r.Min.Y
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := img.Pix[img.PixOffset(r.Min.X, y):][:4*r.Dx()]
		for x := 0; x < len(row); x += 4 {
			if row[x+3] == 0 {
				continue
			}
			px := r.Min.X + x/4
			minX, maxX = min(minX, px), max(maxX, px+1)
			minY, maxY = min(minY, y), max(maxY, y+1)
		}
	}
	if minX >= maxX {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX, maxY)
}

// crop copies the part r of img into an image of its own
func crop(img *image.RGBA, r image.Rectangle) *image.RGBA {
	out := image.NewRGBA(r)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		copyRow(out.Pix[out.PixOffset(r.Min.X, y):][:4*r.Dx()], img.Pix[img.PixOffset(r.Min.X, y):])
	}
	return out
}
