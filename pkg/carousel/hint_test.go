package carousel

import (
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/fogleman/gg"
	"golang.org/x/image/font/gofont/goregular"
)

// TestLayoutHint checks K4 of specs/026-layout-keys: the hint of the layout
// key is drawn at the top right of the header as the version is drawn — its
// face, its colour, its baseline — ending at the margin of the header from
// the right edge, and nothing else of the frame changes; it is not drawn
// where it would come closer than the gap of the header to the version, nor
// without the header, and the band of the header, and so the grid, stays.
func TestLayoutHint(t *testing.T) {
	goFont := filepath.Join(t.TempDir(), "goregular.ttf")
	if err := os.WriteFile(goFont, goregular.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	fonts := []string{goFont}
	for _, layout := range []string{"carousel", "grid"} {
		sc := scene{name: layout, layout: layout, theme: "dark", width: 2520, height: 1400,
			windows: portableWindows(24), selected: 1, hover: -1, hostname: "ws1", version: "v0.1.0"}
		cfg := sceneConfig(sc, fonts)
		measure := func(s string, size float64) float64 {
			dc := gg.NewContext(1, 1)
			dc.SetFontFace(cfg.face(size))
			w, _ := dc.MeasureString(s)
			return w
		}
		small := float64(cfg.FontSize)
		versionX := headerMargin + measure(cfg.Hostname, small*headerScale) + headerGap
		versionEnd := versionX + measure(cfg.Version, small)

		// With the version's own text, the hint is the version's picture
		// moved by a whole number of pixels, to end at the margin
		plain := drawWith(sc, cfg)
		hinted := cfg
		hinted.LayoutHint = cfg.Version
		withHint := drawWith(sc, hinted)
		noVersion := cfg
		noVersion.Version = ""
		versionInk := diffBounds(plain, drawWith(sc, noVersion))
		hintInk := diffBounds(withHint, plain)
		if versionInk.Empty() || hintInk.Empty() {
			t.Fatalf("%s: the version drawn in %v, the hint in %v", layout, versionInk, hintInk)
		}
		dx := int(float64(cfg.Width) - headerMargin - measure(cfg.Version, small) - versionX)
		if want := versionInk.Add(image.Pt(dx, 0)); hintInk != want {
			t.Errorf("%s: the hint drawn in %v, want %v: the version's ink moved to end at the margin", layout, hintInk, want)
		}
		for y := versionInk.Min.Y; y < versionInk.Max.Y; y++ {
			for x := versionInk.Min.X; x < versionInk.Max.X; x++ {
				if a, b := withHint.RGBAAt(x, y), withHint.RGBAAt(x+dx, y); a != b {
					t.Fatalf("%s: the hint at (%d, %d) is %v, the version at (%d, %d) %v", layout, x+dx, y, b, x, y, a)
				}
			}
		}
		if band := int(headerBand(cfg)); hintInk.Max.Y > band || hintInk.Max.X > cfg.Width-int(headerMargin) {
			t.Errorf("%s: the hint drawn in %v, beyond the band %d px high or the margin", layout, hintInk, band)
		}

		// The band, and the grid laid out below it, are those without a hint
		if headerBand(hinted) != headerBand(cfg) || layoutGrid(24, headerBand(hinted), hinted) != layoutGrid(24, headerBand(cfg), cfg) {
			t.Errorf("%s: the hint changes the band of the header or the grid", layout)
		}

		// Narrow: drawn where it starts the gap of the header after the
		// version, not a pixel closer
		hinted.LayoutHint = "Q — carousel"
		fits := int(versionEnd + headerGap + measure(hinted.LayoutHint, small) + headerMargin)
		t.Logf("%s: %q fits beside %q and %q from a width of %d px", layout, hinted.LayoutHint, cfg.Hostname, cfg.Version, fits)
		for _, c := range []struct {
			width int
			drawn bool
		}{{fits, true}, {fits - 1, false}} {
			narrow, without := hinted, cfg
			narrow.Width, without.Width = c.width, c.width
			if drawn := !diffBounds(drawWith(sc, narrow), drawWith(sc, without)).Empty(); drawn != c.drawn {
				t.Errorf("%s: at a width of %d px the hint drawn %v, want %v", layout, c.width, drawn, c.drawn)
			}
		}
		// The narrow scene of Σ is narrower
		for _, n := range scenes() {
			if n.name != layout+"-header-narrow" {
				continue
			}
			without := sceneConfig(n, fonts)
			without.LayoutHint = ""
			if r := diffBounds(drawScene(n, fonts), drawWith(n, without)); n.hint == "" || !r.Empty() {
				t.Errorf("%s: the hint %q drawn in %v", n.name, n.hint, r)
			}
		}

		// Without the header no hint
		bare, bareHint := cfg, hinted
		bare.Hostname, bare.Version, bareHint.Hostname, bareHint.Version = "", "", "", ""
		if r := diffBounds(drawWith(sc, bareHint), drawWith(sc, bare)); !r.Empty() || headerBand(bareHint) != 0 {
			t.Errorf("%s: without the header the hint drawn in %v, the band %v", layout, r, headerBand(bareHint))
		}
	}
}

// drawWith draws the scene with the configuration cfg
func drawWith(sc scene, cfg Config) *image.RGBA {
	if sc.layout == "grid" {
		return DrawGridLayout(sc.windows, sc.selected, sc.hover, cfg)
	}
	return Draw3DCarouselWithData(sc.windows, sc.selected, sc.hover, 0, cfg)
}

// diffBounds is the smallest rectangle holding every pixel in which two
// frames of one size differ
func diffBounds(a, b *image.RGBA) image.Rectangle {
	var r image.Rectangle
	for y := a.Rect.Min.Y; y < a.Rect.Max.Y; y++ {
		for x := a.Rect.Min.X; x < a.Rect.Max.X; x++ {
			if a.RGBAAt(x, y) != b.RGBAAt(x, y) {
				r = r.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return r
}
