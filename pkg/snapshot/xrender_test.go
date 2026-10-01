package snapshot

import (
	"image"
	"math/rand"
	"testing"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// TestRenderThumbnail checks criterion K3 of
// specs/018-snapshot-bind-conflicts: a pixmap scaled in the X server by
// RENDER gives the area average of its pixels computed on the CPU within a
// mean of 1.2 per channel, on an image about as hard to scale as the windows
// of the research — a bound the scaling meets and each wrong variant tried
// there exceeds; a pixmap no larger than a thumbnail comes out as it is.
// Pixmaps of depth 24 and 32, of the visuals of windows; sides even and odd,
// halved on one side only, or not at all. Needs an X display with RENDER and
// MIT-SHM, not a GPU or a compositor: the pixmaps are made here.
func TestRenderThumbnail(t *testing.T) {
	conn, err := xgb.NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	screen := xproto.Setup(conn).DefaultScreen(conn)
	x, err := newXRender(conn, screen.Root)
	if err != nil {
		t.Skipf("no RENDER: %v", err)
	}
	defer x.close()

	for _, depth := range []int{24, 32} {
		visual := visualOf(screen, depth)
		if visual == 0 {
			t.Skipf("no visual of depth %d", depth)
		}
		for _, size := range []image.Point{{1500, 900}, {2560, 1381}, {2556, 1357}, {300, 200}, {511, 700}, {4000, 3}} {
			src := windowImage(size.X, size.Y, int64(depth*size.X))
			pixmap := newPixmap(t, conn, screen.Root, depth, size)
			fillPixmap(t, conn, pixmap, depth, src)
			got, err := x.thumbnail(pixmap, visual, size.X, size.Y)
			xproto.FreePixmap(conn, pixmap)
			if err != nil {
				t.Fatalf("depth %d, %v: %v", depth, size, err)
			}
			tw, th := thumbSize(size.X, size.Y)
			if got.Rect.Dx() != tw || got.Rect.Dy() != th {
				t.Fatalf("depth %d, %v: thumbnail %v, want %dx%d", depth, size, got.Rect.Size(), tw, th)
			}
			mean, worst := difference(got, areaAverage(src, tw, th))
			t.Logf("depth %d, %v → %v: mean %.3f, worst %d", depth, size, got.Rect.Size(), mean, worst)
			small := size.X <= maxSide && size.Y <= maxSide
			if mean > 1.2 || small && worst > 0 {
				t.Errorf("depth %d, %v → %v: mean %.3f, worst %d", depth, size, got.Rect.Size(), mean, worst)
			}
		}
	}
}

// visualOf is a TrueColor visual of the depth on the screen, 0 without one
func visualOf(screen *xproto.ScreenInfo, depth int) xproto.Visualid {
	for _, d := range screen.AllowedDepths {
		if int(d.Depth) != depth {
			continue
		}
		for _, v := range d.Visuals {
			if v.Class == xproto.VisualClassTrueColor {
				return v.VisualId
			}
		}
	}
	return 0
}

// difference is the mean and the worst absolute difference of the colour
// channels of two images of a size
func difference(a, b *image.RGBA) (float64, int) {
	sum, worst := 0, 0
	for i := range a.Pix {
		if i%4 == 3 {
			continue
		}
		d := int(a.Pix[i]) - int(b.Pix[i])
		if d < 0 {
			d = -d
		}
		sum += d
		worst = max(worst, d)
	}
	return float64(sum) / float64(len(a.Pix)/4*3), worst
}

// windowImage looks like a window: lines of text of random lengths — glyphs
// of a few strokes a pixel wide in cells of 10×20 — on a light page, beside a
// darker panel and under a bar with a gradient, opaque
func windowImage(w, h int, seed int64) *image.RGBA {
	r := rand.New(rand.NewSource(seed))
	const gw, gh = 10, 20
	glyphs := make([][gw * gh]bool, 64)
	for i := range glyphs {
		for range 2 + r.Intn(3) {
			if r.Intn(2) == 0 {
				x, y0, y1 := 2+r.Intn(6), 5+r.Intn(3), 12+r.Intn(4)
				for y := y0; y < y1; y++ {
					glyphs[i][y*gw+x] = true
				}
			} else {
				y, x0, x1 := 5+r.Intn(10), 2+r.Intn(2), 5+r.Intn(4)
				for x := x0; x < x1; x++ {
					glyphs[i][y*gw+x] = true
				}
			}
		}
	}
	cols, rows := w/gw+1, h/gh+1
	text := make([]int, cols*rows) // the glyph of each cell, -1 for none
	for row := range rows {
		length := r.Intn(cols * 2 / 3)
		if r.Intn(3) == 0 {
			length = 0 // a blank line
		}
		for col := range cols {
			text[row*cols+col] = -1
			if col < length && r.Intn(6) > 0 {
				text[row*cols+col] = r.Intn(len(glyphs))
			}
		}
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	panel, bar := w/5, min(h/10, 40)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var c [3]uint8
			switch {
			case y < bar:
				v := uint8(80 + x*120/w)
				c = [3]uint8{v, v, 160}
			case x < panel:
				c = [3]uint8{40, 44, 52}
			default:
				c = [3]uint8{246, 246, 240}
			}
			if y >= bar {
				if g := text[(y-bar)/gh*cols+x/gw]; g >= 0 && glyphs[g][(y-bar)%gh*gw+x%gw] {
					if x < panel {
						c = [3]uint8{200, 200, 190}
					} else {
						c = [3]uint8{30, 30, 120}
					}
				}
			}
			o := img.PixOffset(x, y)
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = c[0], c[1], c[2], 255
		}
	}
	return img
}
