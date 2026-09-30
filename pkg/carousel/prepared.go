package carousel

import (
	"bytes"
	"image"
	"math"
	"sync"

	"github.com/fogleman/gg"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
)

// Images resampled apart from the canvas (specs/001-rendering-speed, stage 3).
//
// gg's DrawImage resamples an image with x/image/draw into the canvas. Each
// pixel it writes is a function of the transform, the source, the affected
// rectangle — the canvas bounds intersected with the transformed source — and,
// for a translucent source, of the canvas pixel under it. A scratch image
// whose bounds give the same affected rectangle, with the same pixels under
// the source, gets the same pixels, and the images of a frame can be resampled
// in parallel into such scratch images before the cards are drawn:
//
//   - a thumbnail is opaque — capture forces alpha to 255 — so it is resampled
//     in src mode and does not read the canvas: its scratch starts transparent,
//     and drawTo copies the pixels it wrote;
//   - an icon is translucent, but lies on the window background, uniform
//     there: its scratch starts filled with that colour, and drawTo copies it
//     only if the canvas under it still is that colour everywhere.
type preparedImage struct {
	img   *image.RGBA // pixels DrawImage writes, or nil when it writes none
	under []byte      // for a translucent image, the pixel under it
}

// scratchBuffers recycles the pixels of prepared images between frames
var scratchBuffers sync.Pool

// prepareOpaque resamples src as dc.DrawImage(src, 0, 0) does under the
// matrix m on a canvas with the given bounds. It returns nil when src is not
// opaque.
func prepareOpaque(canvas image.Rectangle, m gg.Matrix, src image.Image) *preparedImage {
	if o, ok := src.(interface{ Opaque() bool }); !ok || !o.Opaque() {
		return nil
	}
	s2d, r := placement(canvas, m, src)
	if r.Empty() {
		return &preparedImage{}
	}
	img := scratchImage(r)
	xdraw.BiLinear.Transform(img, s2d, src, src.Bounds(), xdraw.Over, nil)
	return &preparedImage{img: img}
}

// prepareOnUniform resamples src as dc.DrawImage(src, 0, 0) does under the
// matrix m on the canvas, provided the canvas is of one colour where src
// goes; nil otherwise. The canvas is only read.
func prepareOnUniform(canvas *image.RGBA, m gg.Matrix, src image.Image) *preparedImage {
	s2d, r := placement(canvas.Bounds(), m, src)
	if r.Empty() {
		return &preparedImage{}
	}
	under := uniformPixel(canvas, r)
	if under == nil {
		return nil
	}
	img := scratchImage(r)
	for i := 0; i < len(img.Pix); i += 4 {
		copy(img.Pix[i:i+4], under)
	}
	xdraw.BiLinear.Transform(img, s2d, src, src.Bounds(), xdraw.Over, nil)
	return &preparedImage{img: img, under: under}
}

// drawTo writes the prepared pixels into the canvas and releases the scratch
// image. It returns false, writing nothing, when the canvas under a
// translucent image is no longer the colour it was prepared on: then the image
// must be drawn in place.
func (p *preparedImage) drawTo(dst *image.RGBA) bool {
	if p.img == nil {
		return true
	}
	defer p.release()

	r := p.img.Rect
	width := 4 * r.Dx()
	if p.under != nil {
		if u := uniformPixel(dst, r); u == nil || !bytes.Equal(u, p.under) {
			return false
		}
		for y := r.Min.Y; y < r.Max.Y; y++ {
			copy(dst.Pix[dst.PixOffset(r.Min.X, y):][:width], p.img.Pix[p.img.PixOffset(r.Min.X, y):])
		}
		return true
	}

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
	return true
}

func (p *preparedImage) release() {
	pix := p.img.Pix
	scratchBuffers.Put(&pix)
	p.img = nil
}

// placement is the transform DrawImage(src, 0, 0) uses under m and the
// bounds of a scratch image that give it the affected rectangle of the canvas
func placement(canvas image.Rectangle, m gg.Matrix, src image.Image) (f64.Aff3, image.Rectangle) {
	m = m.Translate(0, 0) // as DrawImageAnchored does
	s2d := f64.Aff3{m.XX, m.XY, m.X0, m.YX, m.YY, m.Y0}
	return s2d, canvas.Intersect(outerBounds(&s2d, src.Bounds()))
}

// uniformPixel is the pixel every pixel of img in r equals, or nil
func uniformPixel(img *image.RGBA, r image.Rectangle) []byte {
	first := img.Pix[img.PixOffset(r.Min.X, r.Min.Y):][:4]
	width := 4 * r.Dx()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := img.Pix[img.PixOffset(r.Min.X, y):][:width]
		for x := 0; x < width; x += 4 {
			if !bytes.Equal(row[x:x+4], first) {
				return nil
			}
		}
	}
	return bytes.Clone(first)
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
