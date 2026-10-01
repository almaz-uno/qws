package carousel

import (
	"bytes"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fogleman/gg"
	"golang.org/x/image/font/gofont/goregular"
)

// gridCase is a grid drawn by TestGridHeads: a scene of Σ or a configuration
// that makes heads be drawn in place
type gridCase struct {
	name            string
	windows         []WindowData
	selected, hover int
	cfg             Config
	inPlace         bool // whether some head is drawn in place
}

// gridCases are the grid scenes of Σ and configurations beyond them: tiles
// too close for their heads, a shadow reaching the next tile, a workspace
// wider than its tile, translucent and empty thumbnails, tiles smaller than
// their thumbnails' margins, the corners of a large window radius, a
// transparent and a square background
func gridCases(fonts []string, host bool) []gridCase {
	var cases []gridCase
	for _, sc := range scenes() {
		if sc.layout != "grid" || sc.host && !host {
			continue
		}
		f := fonts
		if sc.host {
			f = append(f, fontFallback)
		}
		cases = append(cases, gridCase{sc.name, sc.windows, sc.selected, sc.hover, sceneConfig(sc, f), false})
	}

	base := scene{layout: "grid", theme: "dark", width: 1260, height: 700}
	many := portableWindows(24)
	add := func(name string, windows []WindowData, selected, hover int, inPlace bool, change func(*Config)) {
		c := sceneConfig(base, fonts)
		if change != nil {
			change(&c)
		}
		cases = append(cases, gridCase{name, windows, selected, hover, c, inPlace})
	}

	add("tight", many, 5, 6, true, func(c *Config) { c.GridSpacing = 2 })
	add("far-shadow", many, 5, 13, true, func(c *Config) { c.ShadowOffset = 30 })

	wide := append([]WindowData(nil), many[:12]...)
	wide[4].Workspace = strings.Repeat("a long workspace name ", 4)
	wide[7].Workspace = strings.Repeat("W", 60)
	add("wide-workspace", wide, 0, -1, true, nil)

	odd := append([]WindowData(nil), many[:8]...)
	translucent := image.NewNRGBA(image.Rect(0, 0, 400, 300))
	for i := range translucent.Pix {
		translucent.Pix[i] = uint8(i * 7)
	}
	odd[0].Thumbnail = translucent
	odd[1].Thumbnail = image.NewRGBA(image.Rect(0, 0, 0, 0))
	odd[2].Thumbnail = image.NewRGBA(image.Rect(0, 0, 0, 50))
	odd[3].Thumbnail = nil
	odd[4].Icon = image.NewNRGBA(image.Rect(0, 0, 0, 0))
	paletted := image.NewPaletted(image.Rect(0, 0, 40, 40), color.Palette{color.Transparent, color.RGBA{200, 10, 10, 200}})
	for i := range paletted.Pix {
		paletted.Pix[i] = uint8(i / 3 % 2)
	}
	odd[5].Icon = paletted
	odd[6].Urgent, odd[6].Title = true, "urgent"
	add("odd-images", odd, 6, 2, true, nil)

	add("small", many, 3, 4, true, func(c *Config) { c.Width, c.Height = 300, 200 })
	add("corners", many[:4], 0, 3, true, func(c *Config) { c.WindowBackgroundRadius = 250 })
	add("transparent", many, 1, 2, false, func(c *Config) { c.WindowBackgroundEnabled = false })
	add("square", many, 1, 2, false, func(c *Config) { c.WindowBackgroundRadius = 0 })
	return cases
}

// TestGridHeads checks the heads of the grid drawn apart from the canvas
// (specs/019-grid-speed): the grid and its tiles layer are drawn as with every
// head drawn in place, and a head writes no pixel outside its bounds
func TestGridHeads(t *testing.T) {
	goFont := filepath.Join(t.TempDir(), "goregular.ttf")
	if err := os.WriteFile(goFont, goregular.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	host := fileDigest(fontFallback) != ""

	for _, c := range gridCases([]string{goFont}, host) {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.cfg.Fonts = NewFontSet()
			// The frame and the tiles layer, against the tiles drawn one
			// after another with every head in place
			top := headerBand(c.cfg)
			tiles := startGridTiles(c.windows, top, c.selected, c.hover, c.cfg)
			dc := newCanvas(c.cfg)
			drawHeader(dc, c.cfg)
			inPlace := tiles.draw(dc)
			want := gridInPlace(newCanvas(c.cfg), true, c.windows, c.selected, c.hover, c.cfg)
			if !bytes.Equal(getImageRGBA(dc).Pix, want.Pix) {
				t.Errorf("the frame differs from the one with its heads drawn in place")
			}
			if c.inPlace != (inPlace > 0) {
				t.Errorf("%d heads drawn in place", inPlace)
			}
			if got := DrawGridLayout(c.windows, c.selected, c.hover, c.cfg); !bytes.Equal(got.Pix, want.Pix) {
				t.Errorf("DrawGridLayout differs from the frame with its heads drawn in place")
			}
			layer := gridInPlace(gg.NewContext(c.cfg.Width, c.cfg.Height), false, c.windows, -1, -1, c.cfg)
			got := GridTiles(c.windows, c.cfg)
			if b := opaqueBounds(layer); b.Empty() != (got == nil) || got != nil && !bytes.Equal(got.Pix, crop(layer, b).Pix) {
				t.Errorf("GridTiles differs from the tiles with their heads drawn in place")
			}

			// Every pixel a head changes, on the canvas of the frame and on
			// a transparent one, is within its bounds
			g := layoutGrid(len(c.windows), top, c.cfg)
			for _, base := range []*image.RGBA{getImageRGBA(newCanvas(c.cfg)), image.NewRGBA(image.Rect(0, 0, c.cfg.Width, c.cfg.Height))} {
				for i := range c.windows {
					x, y := g.tile(i)
					r, ok := gridHeadBounds(base.Rect, &c.windows[i], x, y, g.tileW, g.tileH, i == c.selected, i == c.hover, c.cfg)
					if !ok {
						continue
					}
					img := image.NewRGBA(base.Rect)
					copy(img.Pix, base.Pix)
					drawGridTileHead(gg.NewContextForRGBA(img), &c.windows[i], x, y, g.tileW, g.tileH, i == c.selected, i == c.hover, c.cfg, nil)
					if out := changedOutside(base, img, r); out > 0 {
						t.Errorf("tile %d: %d pixels changed outside %v", i, out, r)
					}
				}
			}
		})
	}
}

// gridInPlace draws the tiles on the canvas of dc one after another, every
// head in place, after the header if one is drawn, as DrawGridLayout and
// GridTiles drew them before specs/019-grid-speed
func gridInPlace(dc *gg.Context, header bool, windowData []WindowData, selected, hover int, cfg Config) *image.RGBA {
	top := headerBand(cfg)
	if header {
		top = drawHeader(dc, cfg)
	}
	g := layoutGrid(len(windowData), top, cfg)
	for i := range windowData {
		x, y := g.tile(i)
		drawGridTile(dc, &windowData[i], x, y, g.tileW, g.tileH, i == selected, i == hover, cfg, nil)
	}
	return getImageRGBA(dc)
}

// changedOutside counts the pixels outside r in which a and b differ
func changedOutside(a, b *image.RGBA, r image.Rectangle) int {
	n := 0
	count := func(p, q []byte) {
		if bytes.Equal(p, q) {
			return
		}
		for i := 0; i < len(p); i += 4 {
			if !bytes.Equal(p[i:i+4], q[i:i+4]) {
				n++
			}
		}
	}
	for y := a.Rect.Min.Y; y < a.Rect.Max.Y; y++ {
		p := a.Pix[a.PixOffset(a.Rect.Min.X, y):][:4*a.Rect.Dx()]
		q := b.Pix[b.PixOffset(b.Rect.Min.X, y):][:4*b.Rect.Dx()]
		if y < r.Min.Y || y >= r.Max.Y || r.Empty() {
			count(p, q)
			continue
		}
		left, right := 4*(r.Min.X-a.Rect.Min.X), 4*(r.Max.X-a.Rect.Min.X)
		count(p[:left], q[:left])
		count(p[right:], q[right:])
	}
	return n
}
