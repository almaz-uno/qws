package main

import (
	"image"
	"math"
	"strings"

	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/fogleman/gg"
	"golang.org/x/image/font"
)

// kit is the fonts the windows are drawn with, each a fallback chain of
// carousel.MultiFallbackFace: the fonts of the overlay, and before them the
// monospace or the bold font
type kit struct {
	sans, mono, bold []string
}

func (k *kit) ui(size float64) font.Face     { return carousel.NewMultiFallbackFace(k.sans, size) }
func (k *kit) code(size float64) font.Face   { return carousel.NewMultiFallbackFace(k.mono, size) }
func (k *kit) strong(size float64) font.Face { return carousel.NewMultiFallbackFace(k.bold, size) }

// painter draws on a gg context with hex colours
type painter struct {
	*gg.Context
	k *kit
}

// newPainter is a painter of a canvas of w×h filled with bg
func newPainter(k *kit, w, h int, bg string) *painter {
	p := &painter{gg.NewContext(w, h), k}
	p.SetHexColor(bg)
	p.Clear()
	return p
}

// rect fills a rectangle
func (p *painter) rect(x, y, w, h float64, c string) {
	p.SetHexColor(c)
	p.DrawRectangle(x, y, w, h)
	p.Fill()
}

// rrect fills a rounded rectangle
func (p *painter) rrect(x, y, w, h, r float64, c string) {
	p.SetHexColor(c)
	p.DrawRoundedRectangle(x, y, w, h, r)
	p.Fill()
}

// frame strokes a rounded rectangle
func (p *painter) frame(x, y, w, h, r, width float64, c string) {
	p.SetHexColor(c)
	p.SetLineWidth(width)
	p.DrawRoundedRectangle(x, y, w, h, r)
	p.Stroke()
}

// circle fills a circle
func (p *painter) circle(x, y, r float64, c string) {
	p.SetHexColor(c)
	p.DrawCircle(x, y, r)
	p.Fill()
}

// line strokes a line
func (p *painter) line(x0, y0, x1, y1, width float64, c string) {
	p.SetHexColor(c)
	p.SetLineWidth(width)
	p.DrawLine(x0, y0, x1, y1)
	p.Stroke()
}

// text draws s with its baseline at y from x and returns where it ends
func (p *painter) text(s string, x, y float64, f font.Face, c string) float64 {
	p.SetFontFace(f)
	p.SetHexColor(c)
	p.DrawString(s, x, y)
	w, _ := p.MeasureString(s)
	return x + w
}

// textC draws s centred on x, its baseline at y
func (p *painter) textC(s string, x, y float64, f font.Face, c string) {
	p.SetFontFace(f)
	w, _ := p.MeasureString(s)
	p.text(s, x-w/2, y, f, c)
}

// textR draws s ending at x, its baseline at y
func (p *painter) textR(s string, x, y float64, f font.Face, c string) {
	p.SetFontFace(f)
	w, _ := p.MeasureString(s)
	p.text(s, x-w, y, f, c)
}

// width is the width of s in the face
func (p *painter) width(s string, f font.Face) float64 {
	p.SetFontFace(f)
	w, _ := p.MeasureString(s)
	return w
}

// para draws text wrapped to the width from x, the first baseline at y, and
// returns the baseline after its last line
func (p *painter) para(text string, x, y, width, leading float64, f font.Face, c string) float64 {
	for _, line := range p.wrap(text, width, f) {
		p.text(line, x, y, f, c)
		y += leading
	}
	return y
}

// wrap breaks text into lines no wider than width, at spaces
func (p *painter) wrap(text string, width float64, f font.Face) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		next := word
		if line != "" {
			next = line + " " + word
		}
		if line != "" && p.width(next, f) > width {
			lines = append(lines, line)
			next = word
		}
		line = next
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// span is a piece of a line of a terminal or an editor in its colour
type span struct {
	text  string
	color string
}

// spans draws the pieces one after the other from x, the baseline at y, tabs
// expanded to stops of 8 columns of the advance col
func (p *painter) spans(ss []span, x, y, col float64, f font.Face) {
	column := 0
	for _, s := range ss {
		var b strings.Builder
		for _, r := range s.text {
			if r == '\t' {
				n := 8 - column%8
				b.WriteString(strings.Repeat(" ", n))
				column += n
				continue
			}
			b.WriteRune(r)
			column++
		}
		t := b.String()
		p.text(t, x, y, f, s.color)
		x += col * float64(len([]rune(t)))
	}
}

// image is what was drawn
func (p *painter) image() *image.RGBA {
	return p.Image().(*image.RGBA)
}

// maxSide bounds a thumbnail, as in pkg/snapshot
const maxSide = 512

// thumbnail averages the window into its thumbnail as the snapshots of
// pkg/snapshot do on the GPU (gpu.go): scaled down, never up, to fit
// maxSide×maxSide, its aspect kept, each pixel the area average of the
// rectangle of the window it covers, opaque
func thumbnail(win *image.RGBA) *image.RGBA {
	w, h := win.Rect.Dx(), win.Rect.Dy()
	scale := min(1, float64(maxSide)/float64(w), float64(maxSide)/float64(h))
	tw, th := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	sx, sy := float64(w)/float64(tw), float64(h)/float64(th)
	out := image.NewRGBA(image.Rect(0, 0, tw, th))
	for ty := range th {
		loY := float64(ty) * sy
		hiY := loY + sy
		for tx := range tw {
			loX := float64(tx) * sx
			hiX := loX + sx
			var sum [3]float64
			area := 0.0
			for y := int(loY); float64(y) < hiY && y < h; y++ {
				wy := math.Min(float64(y+1), hiY) - math.Max(float64(y), loY)
				row := win.Pix[y*win.Stride:]
				for x := int(loX); float64(x) < hiX && x < w; x++ {
					wx := math.Min(float64(x+1), hiX) - math.Max(float64(x), loX)
					px := row[4*x:]
					for c := range 3 {
						sum[c] += wx * wy * float64(px[c])
					}
					area += wx * wy
				}
			}
			o := out.Pix[ty*out.Stride+4*tx:]
			for c := range 3 {
				o[c] = toByte(sum[c] / area / 255)
			}
			o[3] = 255
		}
	}
	return out
}
