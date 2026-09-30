package carousel

import (
	"image"
	"math"
	"sync"

	"github.com/fogleman/gg"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
)

// Thumbnails are opaque — capture forces alpha to 255 — so gg's DrawImage
// resamples them with x/image/draw in its src mode: the pixels it writes do
// not depend on what is under them. prepareThumbnail does that resampling
// apart from the canvas, so that the thumbnails of a frame can be resampled in
// parallel, and drawTo later writes the same pixels at the point where gg
// would have (specs/001-rendering-speed, stage 3).
//
// The pixels are the same because x/image/draw computes each of them from the
// transform, the source and the affected rectangle — the destination bounds
// intersected with the transformed source — and the scratch image is given
// bounds that make that rectangle the one of the canvas.
type preparedThumbnail struct {
	img *image.RGBA // pixels DrawImage writes; alpha 0 where it writes none
}

// scratchBuffers recycles the pixels of prepared thumbnails between frames
var scratchBuffers sync.Pool

// prepareThumbnail resamples src as dc.DrawImage(src, 0, 0) does under the
// matrix m on a canvas with the given bounds. It returns nil when src is not
// opaque: then the result depends on the canvas, and src must be drawn in place.
func prepareThumbnail(canvas image.Rectangle, m gg.Matrix, src image.Image) *preparedThumbnail {
	if o, ok := src.(interface{ Opaque() bool }); !ok || !o.Opaque() {
		return nil
	}
	m = m.Translate(0, 0) // as DrawImageAnchored does
	s2d := f64.Aff3{m.XX, m.XY, m.X0, m.YX, m.YY, m.Y0}
	r := canvas.Intersect(outerBounds(&s2d, src.Bounds()))
	if r.Empty() {
		return &preparedThumbnail{}
	}

	img := scratchImage(r)
	xdraw.BiLinear.Transform(img, s2d, src, src.Bounds(), xdraw.Over, nil)
	return &preparedThumbnail{img: img}
}

// drawTo writes the prepared pixels into the canvas and releases the scratch
// image
func (p *preparedThumbnail) drawTo(dst *image.RGBA) {
	if p.img == nil {
		return
	}
	r := p.img.Rect
	width := 4 * r.Dx()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		src := p.img.Pix[p.img.PixOffset(r.Min.X, y):][:width]
		row := dst.Pix[dst.PixOffset(r.Min.X, y):][:width]
		for x := 0; x < width; {
			if src[x+3] == 0 {
				x += 4
				continue
			}
			start := x
			for x < width && src[x+3] != 0 {
				x += 4
			}
			copy(row[start:x], src[start:x])
		}
	}
	pix := p.img.Pix
	scratchBuffers.Put(&pix)
	p.img = nil
}

// scratchImage is a transparent image with bounds r
func scratchImage(r image.Rectangle) *image.RGBA {
	n := 4 * r.Dx() * r.Dy()
	var pix []byte
	if b, ok := scratchBuffers.Get().(*[]byte); ok && cap(*b) >= n {
		pix = (*b)[:n]
		clear(pix)
	} else {
		pix = make([]byte, n)
	}
	return &image.RGBA{Pix: pix, Stride: 4 * r.Dx(), Rect: r}
}

// outerBounds contains the destination rectangle x/image/draw computes for
// the source rectangle sr under s2d
func outerBounds(s2d *f64.Aff3, sr image.Rectangle) image.Rectangle {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, p := range [...]image.Point{sr.Min, {sr.Max.X, sr.Min.Y}, {sr.Min.X, sr.Max.Y}, sr.Max} {
		x := s2d[0]*float64(p.X) + s2d[1]*float64(p.Y) + s2d[2]
		y := s2d[3]*float64(p.X) + s2d[4]*float64(p.Y) + s2d[5]
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	return image.Rect(int(math.Floor(minX))-1, int(math.Floor(minY))-1, int(math.Ceil(maxX))+1, int(math.Ceil(maxY))+1)
}
