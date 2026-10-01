package carousel

import (
	"image"
	"image/draw"
	"math"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
)

// TestLayers checks the layers of specs/007-animation against the frames they
// come from: a card layer holds what the card draws, and it sits where the
// card is; the grid tiles are where the grid draws them
func TestLayers(t *testing.T) {
	goFont := filepath.Join(t.TempDir(), "goregular.ttf")
	if err := os.WriteFile(goFont, goregular.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	var sc scene
	for _, s := range scenes() {
		if s.name == "carousel-e1-header" {
			sc = s
		}
	}
	cfg := sceneConfig(sc, []string{goFont})

	// One card over the base, drawn with "over", gives the frame where only
	// that card is — up to the rounding of the composition
	for _, offset := range []int{0, 1, -1, 2} { // |o| > 2 is off the frame of E1
		index := sc.selected + offset
		layer := CardLayer(sc.windows, index, offset, cfg)
		if layer == nil {
			t.Fatalf("offset %d: no layer", offset)
		}
		alone := make([]WindowData, len(sc.windows))
		alone[index] = sc.windows[index]
		want := Draw3DCarouselWithData(alone, sc.selected, -1, 0, cfg)
		got := CarouselBase(cfg)
		draw.Draw(got, layer.Rect, layer, layer.Rect.Min, draw.Over)
		if n, worst := compare(got, want); worst > 2 {
			t.Errorf("offset %d: %d pixels differ, by up to %d", offset, n, worst)
		}
		if !want.Rect.Intersect(layer.Rect).Eq(layer.Rect) {
			t.Errorf("offset %d: layer %v outside the frame", offset, layer.Rect)
		}
	}
	if CardLayer(sc.windows, sc.selected+6, 6, cfg) != nil {
		t.Error("a layer at offset 6, where cards are not drawn")
	}

	x, y, w, h := GridTile(len(sc.windows), 7, cfg)
	g := layoutGrid(len(sc.windows), headerBand(cfg), cfg)
	gx, gy := g.tile(7)
	if x != gx || y != gy || w != g.tileW || h != g.tileH {
		t.Errorf("GridTile %v %v %v %v, layout %v %v %v %v", x, y, w, h, gx, gy, g.tileW, g.tileH)
	}
	if sel := GridSelection(w, h, cfg); opaqueBounds(sel).Empty() {
		t.Error("an empty selection frame")
	}

	// The grid of layers: the base, the shadows of the selected and the
	// hovered tile, the tiles, the hover and the selection frame — the grid
	// with the tiles selected and hovered but for the frames of the tiles
	// under the frames over them, and the edges of the layers placed at the
	// nearest pixel here
	for _, name := range []string{"grid-e1-header", "grid-24-middle-hover"} {
		for _, s := range scenes() {
			if s.name == name {
				sc = s
			}
		}
		cfg := sceneConfig(sc, []string{goFont})
		x, y, w, h := GridTile(len(sc.windows), sc.selected, cfg)
		o := cfg.ShadowOffset
		type placed struct {
			img  *image.RGBA
			x, y float64
		}
		layers := []placed{{GridShadow(w, h, o, cfg), x, y}}
		var hx, hy float64
		hovered := sc.hover >= 0 && sc.hover != sc.selected
		if hovered {
			hx, hy, _, _ = GridTile(len(sc.windows), sc.hover, cfg)
			layers = append(layers, placed{GridShadow(w, h, o/2, cfg), hx, hy})
		}
		layers = append(layers, placed{GridTiles(sc.windows, cfg), 0, 0})
		if hovered {
			layers = append(layers, placed{GridHover(w, h), hx, hy})
		}
		layers = append(layers, placed{GridSelection(w, h, cfg), x, y})

		got := CarouselBase(cfg)
		for _, l := range layers {
			r := l.img.Rect.Add(image.Pt(int(math.Round(l.x)), int(math.Round(l.y))))
			draw.Draw(got, r, l.img, l.img.Rect.Min, draw.Over)
		}
		want := DrawGridLayout(sc.windows, sc.selected, sc.hover, cfg)
		// Near the edge of a rectangle: within d of its border
		near := func(fx, fy, x, y, w, h, d float64) bool {
			inner := fx > x+d && fx < x+w-d && fy > y+d && fy < y+h-d
			outer := fx > x-d && fx < x+w+d && fy > y-d && fy < y+h+d
			return outer && !inner
		}
		// The frame, and the shadow, whose rounded corners leave its
		// rectangle by up to 2.3 pixels
		edge := func(fx, fy, x, y, o float64) bool {
			return near(fx, fy, x, y, w, h, 5) || near(fx, fy, x+o, y+o, w, h, 3)
		}
		ring, far := 0, 0
		for py := 0; py < cfg.Height; py++ {
			for px := 0; px < cfg.Width; px++ {
				i := got.PixOffset(px, py)
				d := 0
				for c := 0; c < 4; c++ {
					d = max(d, abs(int(got.Pix[i+c])-int(want.Pix[i+c])))
				}
				if d <= 2 {
					continue
				}
				fx, fy := float64(px)+0.5, float64(py)+0.5
				if edge(fx, fy, x, y, o) || hovered && edge(fx, fy, hx, hy, o/2) {
					ring++
				} else {
					far++
				}
			}
		}
		if far > 0 {
			t.Errorf("%s: %d pixels off the frames of the selected and hovered tiles differ", name, far)
		}
		t.Logf("%s: %d pixels on the frames of the selected and hovered tiles or the edges of their shadows differ", name, ring)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// compare counts the pixels in which two frames differ and the largest
// difference of a channel
func compare(a, b *image.RGBA) (n int, worst int) {
	for i := 0; i < len(a.Pix); i += 4 {
		d := 0
		for c := 0; c < 4; c++ {
			v := int(a.Pix[i+c]) - int(b.Pix[i+c])
			if v < 0 {
				v = -v
			}
			d = max(d, v)
		}
		if d > 0 {
			n++
			worst = max(worst, d)
		}
	}
	return n, worst
}
