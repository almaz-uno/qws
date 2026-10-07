package main

import (
	"image"
	"math"
	"testing"
	"time"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/almaz-uno/qws/pkg/carousel"
)

// TestLocateFrames checks the frames of the convergence of the picture
// against the formula of pkg/ui (specs/032-readme-locate, K1): after the
// switch to the grid a scene every framePeriod from the key while the
// selection frame converges, at the defaults of config.Default(), d = 400 ms
// and z = 1.6; in the scene at t the shadow of the selected tile and the
// selection frame faded by the level v = 1 − (1 − t/d)³ — easeOut of pkg/ui,
// the level of its motion from 0 to 1 — and zoomed by z + (1 − z)·v about
// the tile's centre — effects.look and zoomRect of pkg/ui —, both monotone;
// the tiles as at rest; nothing else in the scene. The windows are those of
// the picture, without the header, which would need the fonts; the layers
// are blank images of bounds of their own, the scene placing them by their
// bounds alone.
func TestLocateFrames(t *testing.T) {
	a := config.Default().Appearance.Animation
	d, z := a.LocateDuration, a.LocateZoom
	if d != 400*time.Millisecond || z != 1.6 {
		t.Fatalf("the defaults %v, %v; want 400ms, 1.6", d, z)
	}
	dm := newDemo(options{thumb: 512, spacing: 600, fontSize: 20}, make([]window, 12), nil)
	if dm.locate != d || dm.locateZoom != z {
		t.Fatalf("the demo converges in %v from %v; want %v from %v", dm.locate, dm.locateZoom, d, z)
	}
	s := &selector{d: dm, mode: "grid", selected: 3, cfg: dm.layout("grid")}
	tiles := image.NewRGBA(image.Rect(20, 40, 2500, 1380))
	s.layers = map[cardKey]*image.RGBA{
		{index: gridShadow}:    image.NewRGBA(image.Rect(6, 6, 470, 520)),
		{index: gridTiles}:     tiles,
		{index: gridSelection}: image.NewRGBA(image.Rect(-4, -4, 466, 514)),
	}
	x, y, w, h := carousel.GridTile(len(dm.data), s.selected, s.cfg)
	cx, cy := x+w/2, y+h/2
	rest := s.gridItems(x, y, carousel.Opaque)
	if len(rest) != 3 || rest[1].a != tiles {
		t.Fatalf("the scene at rest %+v; want the shadow, the tiles and the frame", rest)
	}
	for i, it := range rest {
		b := it.a.Rect
		dx, dy := x, y
		if it.a == tiles {
			dx, dy = 0, 0
		}
		want := carousel.Rect{X: dx + float64(b.Min.X), Y: dy + float64(b.Min.Y), W: float64(b.Dx()), H: float64(b.Dy())}
		if it.ra != want || it.alpha != 1 || it.b != nil || it.weightB != 0 {
			t.Errorf("at rest, item %d %+v; want at %+v, opaque", i, it, want)
		}
	}

	scenes := s.locateScenes()
	if want := int(d / framePeriod); len(scenes) != want {
		t.Fatalf("%d scenes of the convergence; want %d, at 0, %v, … before %v", len(scenes), want, framePeriod, d)
	}
	near := func(a, b float64) bool { return math.Abs(a-b) <= 1e-9 }
	prevAlpha, prevScale := -1.0, math.Inf(1)
	for k, items := range scenes {
		at := time.Duration(k) * framePeriod
		v := 1 - math.Pow(1-float64(at)/float64(d), 3)
		scale := z + (1-z)*v
		if len(items) != len(rest) {
			t.Fatalf("at %v: %d items, at rest %d", at, len(items), len(rest))
		}
		for i, it := range items {
			want := rest[i]
			if it.a != tiles {
				r := want.ra
				want.alpha = v
				want.ra = carousel.Rect{X: cx + (r.X-cx)*scale, Y: cy + (r.Y-cy)*scale, W: r.W * scale, H: r.H * scale}
			}
			if it.a != want.a || it.b != nil || it.weightB != 0 || !near(it.alpha, want.alpha) ||
				!near(it.ra.X, want.ra.X) || !near(it.ra.Y, want.ra.Y) || !near(it.ra.W, want.ra.W) || !near(it.ra.H, want.ra.H) {
				t.Errorf("at %v, item %d: alpha %v at %+v; want %v at %+v", at, i, it.alpha, it.ra, want.alpha, want.ra)
			}
		}
		if v <= prevAlpha || scale >= prevScale {
			t.Errorf("at %v: alpha %v after %v, scale %v after %v; want the one growing, the other shrinking", at, v, prevAlpha, scale, prevScale)
		}
		prevAlpha, prevScale = v, scale
	}
	if f := locateLook(0, d, z); f.Alpha != 0 || f.Scale != z {
		t.Errorf("at the key %+v; want transparent at %v", f, z)
	}
	if f := locateLook(d, d, z); f != carousel.Opaque {
		t.Errorf("at %v %+v; want opaque at 1, as at rest", d, f)
	}
}
