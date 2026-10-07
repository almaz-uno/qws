package main

import (
	"image"
	"math"
	"time"

	"github.com/almaz-uno/qws/pkg/carousel"
)

// The frames in motion. The selector here keeps what the Selector of pkg/ui
// keeps for the glx presenter (pkg/ui/animation.go): the base layer and the
// card or grid layers of the layout shown, all of them drawn by the time a
// step or a switch needs them, as they are after a pause at rest. pkg/ui
// keeps the layers of both layouts side by side (specs/028-grid-locate);
// here those of the layout shown are drawn again at a switch, the same
// pixels. A frame of a step is the scene of carouselItems or gridItems at
// the position of the selection then; a frame of the convergence after a
// switch to the grid, the scene of gridItems at the look of the selection
// frame then; each composed as present_glx_scene.go composes it on the GPU.

// cardKey names a layer as pkg/ui names it: the card of window index at an
// integer offset from the selection, or, with a negative index, a layer of
// the grid
type cardKey struct {
	index, offset int
}

// The layers of the grid. pkg/ui also holds the hover shadow and the hover
// frame; with no window hovered, its scenes never place them.
const (
	gridTiles     = -1 - iota // the tiles
	gridShadow                // the shadow of the selected tile
	gridSelection             // the selection frame
)

var gridLayers = []cardKey{{index: gridTiles}, {index: gridShadow}, {index: gridSelection}}

// item is a carousel.SceneItem with its layers: a at ra and b at rb,
// cross-faded with the weight weightB of b, then scaled by alpha
type item struct {
	a, b    *image.RGBA
	ra, rb  carousel.Rect
	weightB float64
	alpha   float64
}

// selector is the state of the switcher a step depends on
type selector struct {
	d        *demo
	mode     string
	selected int
	cfg      carousel.Config
	base     *image.RGBA             // nil until a step uploads it
	layers   map[cardKey]*image.RGBA // held; nil where the layer is not drawn (layer 0 of pkg/ui)
}

// newSelector is the switcher shown in the layout with the selection: the
// layers dropped, then prefetched, as Show does
func newSelector(d *demo, mode string, selected int) *selector {
	s := &selector{d: d, mode: mode, selected: selected}
	s.dropLayers()
	s.prefetch()
	return s
}

// dropLayers forgets the layers, for the configuration of the layout shown
func (s *selector) dropLayers() {
	s.cfg = s.d.layout(s.mode)
	s.base = nil
	s.layers = map[cardKey]*image.RGBA{}
}

// switchLayout shows the layout from its layers, as switchLayout of pkg/ui
// does on a presenter that composes, prefetches them, and returns the frames
// in motion that follow the key: in the grid, those of the selection frame
// converging onto its tile (locateScenes); the frame after the last is the
// frame at rest. None in the carousel.
func (s *selector) switchLayout(mode string) []*image.RGBA {
	s.mode = mode
	s.dropLayers()
	s.prefetch()
	if mode != "grid" {
		return nil
	}
	s.ensureBase()
	var frames []*image.RGBA
	for _, items := range s.locateScenes() {
		frames = append(frames, composeScene(s.base, items))
	}
	return frames
}

// locateScenes are the scenes of the grid after a switch to it while the
// selection frame and the shadow of the selected tile converge onto the
// tile (specs/028-grid-locate): at 0, framePeriod, 2·framePeriod, … before
// the locate duration, the first at the key; none at a duration of 0
func (s *selector) locateScenes() [][]item {
	x, y, _, _ := carousel.GridTile(len(s.d.data), s.selected, s.cfg)
	var scenes [][]item
	for t := time.Duration(0); t < s.d.locate; t += framePeriod {
		scenes = append(scenes, s.gridItems(x, y, locateLook(t, s.d.locate, s.d.locateZoom)))
	}
	return scenes
}

// ensureBase draws the base of the layout shown — the canvas and the header
// with the hint of the layout key — unless it is held
func (s *selector) ensureBase() {
	if s.base == nil {
		s.base = carousel.CarouselBase(s.cfg)
	}
}

// locateLook is how the selection frame and the shadow of the selected tile
// are drawn at t after a switch to the grid while they converge, as
// locateLook of pkg/ui draws them (specs/028-grid-locate): their level moves
// from 0 to 1 in the duration d along ease-out cubic, as a motion of pkg/ui;
// they are faded by it and zoomed from zoom at 0 to 1 at 1, as effects.look
// of pkg/ui with both effects
func locateLook(t, d time.Duration, zoom float64) carousel.Fade {
	v := easeOut(float64(t) / float64(d))
	return carousel.Fade{Alpha: v, Scale: zoom + (1-zoom)*v}
}

// request draws the layers not held
func (s *selector) request(keys []cardKey) {
	for _, k := range keys {
		if _, ok := s.layers[k]; !ok {
			s.layers[k] = s.drawLayer(k)
		}
	}
}

// drawLayer draws the layer named by k, as drawLayer of pkg/ui does
func (s *selector) drawLayer(k cardKey) *image.RGBA {
	if k.index >= 0 {
		return carousel.CardLayer(s.d.data, k.index, k.offset, s.cfg)
	}
	_, _, w, h := carousel.GridTile(len(s.d.data), 0, s.cfg)
	switch k.index {
	case gridTiles:
		return carousel.GridTiles(s.d.data, s.cfg)
	case gridShadow:
		return carousel.GridShadow(w, h, s.cfg.ShadowOffset, s.cfg)
	}
	return carousel.GridSelection(w, h, s.cfg)
}

// motionLayers are the card layers a motion from p to target passes, as
// pkg/ui requests them
func (s *selector) motionLayers(p, target float64) []cardKey {
	lo, hi := math.Min(p, target), math.Max(p, target)
	var keys []cardKey
	for k := range s.d.data {
		for n := int(math.Floor(float64(k) - hi)); n <= int(math.Ceil(float64(k)-lo)); n++ {
			if n >= -5 && n <= 5 {
				keys = append(keys, cardKey{index: k, offset: n})
			}
		}
	}
	return keys
}

// prefetch requests the layers of the next step either way, as pkg/ui does
// after every step and every new picture
func (s *selector) prefetch() {
	if s.mode == "grid" {
		s.request(gridLayers)
		return
	}
	sel := float64(s.selected)
	s.request(append(s.motionLayers(sel-1, sel), s.motionLayers(sel, sel+1)...))
}

// stepTo moves the selection to target, as stepTo of pkg/ui on a presenter
// that composes layers, and returns the frames of the step: the scenes at
// framePeriod, 2·framePeriod, … while the selection moves. The frame at the
// key itself is the frame at rest before it; the frame after the last is the
// frame at rest at the target.
func (s *selector) stepTo(target int) []*image.RGBA {
	n := len(s.d.data)
	from := float64(s.selected)
	gx0, gy0, _, _ := carousel.GridTile(n, s.selected, s.cfg)
	gx1, gy1, _, _ := carousel.GridTile(n, target, s.cfg)
	s.selected = target
	s.ensureBase()
	if s.mode != "grid" {
		s.request(s.motionLayers(from, float64(target)))
	}
	s.prefetch()

	var frames []*image.RGBA
	for t := framePeriod; t < s.d.step; t += framePeriod {
		e := easeOut(float64(t) / float64(s.d.step))
		var items []item
		if s.mode == "grid" {
			items = s.gridItems(gx0+(gx1-gx0)*e, gy0+(gy1-gy0)*e, carousel.Opaque)
		} else {
			items = s.carouselItems(from + (float64(target)-from)*e)
		}
		frames = append(frames, composeScene(s.base, items))
	}
	return frames
}

// easeOut is ease-out cubic on [0, 1], as easeOut of pkg/ui
func easeOut(u float64) float64 {
	v := 1 - u
	return 1 - v*v*v
}

// carouselItems is the scene of the carousel with the selection at p, as
// carouselItems of pkg/ui makes it without hover and live thumbnails: each
// card as its layers at the integer offsets around its fractional one,
// cross-faded
func (s *selector) carouselItems(p float64) []item {
	var items []item
	for k := range s.d.data {
		o := float64(k) - p
		x, y, scale, ok := carousel.CardCenter(s.d.data, k, o, s.cfg)
		if !ok {
			continue
		}
		n0 := math.Floor(o)
		it := item{weightB: o - n0, alpha: 1}
		it.a, it.ra = s.placeLayer(cardKey{index: k, offset: int(n0)}, x, y, scale)
		if it.weightB > 0 {
			it.b, it.rb = s.placeLayer(cardKey{index: k, offset: int(n0) + 1}, x, y, scale)
		}
		items = append(items, it)
	}
	return items
}

// placeLayer is the layer of the card of key nearest to its integer offset
// among those held, placed and scaled for a card whose centre is at x, y
// with the scale, as placeLayer of pkg/ui places it
func (s *selector) placeLayer(key cardKey, x, y, scale float64) (*image.RGBA, carousel.Rect) {
	for d := 0; d <= 2; d++ {
		for _, n := range []int{key.offset + d, key.offset - d} {
			layer := s.layers[cardKey{index: key.index, offset: n}]
			if layer == nil {
				continue
			}
			xn, yn, sn, ok := carousel.CardCenter(s.d.data, key.index, float64(n), s.cfg)
			if !ok || sn == 0 {
				continue
			}
			f := scale / sn
			b := layer.Rect
			return layer, carousel.Rect{
				X: x + (float64(b.Min.X)-xn)*f,
				Y: y + (float64(b.Min.Y)-yn)*f,
				W: float64(b.Dx()) * f,
				H: float64(b.Dy()) * f,
			}
		}
	}
	return nil, carousel.Rect{}
}

// gridItems is the scene of the grid with the selection frame's tile at x, y,
// as gridItems of pkg/ui makes it without hover and live thumbnails: the
// shadow of the selected tile, the tiles, the selection frame — the shadow
// and the frame drawn at the look f, faded by its alpha and zoomed by its
// scale about the centre of their tile (specs/028-grid-locate), the tiles as
// they are
func (s *selector) gridItems(x, y float64, f carousel.Fade) []item {
	_, _, w, h := carousel.GridTile(len(s.d.data), s.selected, s.cfg)
	cx, cy := x+w/2, y+h/2
	var items []item
	add := func(key cardKey, dx, dy float64, f carousel.Fade) {
		l := s.layers[key]
		if l == nil {
			return
		}
		b := l.Rect
		r := carousel.Rect{X: dx + float64(b.Min.X), Y: dy + float64(b.Min.Y), W: float64(b.Dx()), H: float64(b.Dy())}
		items = append(items, item{a: l, ra: zoomRect(r, cx, cy, f.Scale), alpha: f.Alpha})
	}
	add(cardKey{index: gridShadow}, x, y, f)
	add(cardKey{index: gridTiles}, 0, 0, carousel.Opaque)
	add(cardKey{index: gridSelection}, x, y, f)
	return items
}

// zoomRect is r scaled by f about the point cx, cy, as zoomRect of pkg/ui
func zoomRect(r carousel.Rect, cx, cy, f float64) carousel.Rect {
	if f == 1 {
		return r
	}
	return carousel.Rect{X: cx + (r.X-cx)*f, Y: cy + (r.Y-cy)*f, W: r.W * f, H: r.H * f}
}

// composeScene composes a scene as drawScene of the glx presenter does: the
// base copied texel for texel, then each item drawn as a quad covering both
// of its rectangles with the blending ONE, ONE_MINUS_SRC_ALPHA into an RGBA8
// buffer. A fragment at the pixel centre samples each layer through a linear
// filter, clamped to its edge, where the layer's rectangle maps it, and
// nothing outside the rectangle; the colour is ((1−w)·A + w·B)·alpha.
func composeScene(base *image.RGBA, items []item) *image.RGBA {
	out := image.NewRGBA(base.Rect)
	copy(out.Pix, base.Pix)
	width, height := out.Rect.Dx(), out.Rect.Dy()
	for _, it := range items {
		ra, rb := it.ra, it.rb
		if it.a == nil {
			ra = carousel.Rect{}
		}
		if it.b == nil {
			rb = carousel.Rect{}
		}
		q := union(ra, rb)
		if q.W <= 0 || q.H <= 0 {
			continue
		}
		// The pixels whose centres lie in the quad, within the window
		x0, x1 := max(0, int(math.Ceil(q.X-0.5))), min(width, int(math.Ceil(q.X+q.W-0.5)))
		y0, y1 := max(0, int(math.Ceil(q.Y-0.5))), min(height, int(math.Ceil(q.Y+q.H-0.5)))
		wa, wb := (1-it.weightB)*it.alpha, it.weightB*it.alpha
		for py := y0; py < y1; py++ {
			cy := float64(py) + 0.5
			row := out.Pix[out.PixOffset(0, py):]
			for px := x0; px < x1; px++ {
				cx := float64(px) + 0.5
				var c [4]float64
				if wa != 0 {
					sampleInto(&c, it.a, ra, cx, cy, wa)
				}
				if wb != 0 {
					sampleInto(&c, it.b, rb, cx, cy, wb)
				}
				if c == [4]float64{} {
					continue
				}
				dst := row[4*px : 4*px+4]
				k := 1 - c[3]
				for ch := range 4 {
					dst[ch] = toByte(c[ch] + float64(dst[ch])/255*k)
				}
			}
		}
	}
	return out
}

// sampleInto adds weight times the layer img, sampled at the point x, y of the
// window with its rectangle at r, to c: the texture lookup of sampleLayer of
// the scene's fragment shader, GL_LINEAR and GL_CLAMP_TO_EDGE
func sampleInto(c *[4]float64, img *image.RGBA, r carousel.Rect, x, y, weight float64) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	u, v := (x-r.X)/r.W, (y-r.Y)/r.H
	if u < 0 || v < 0 || u > 1 || v > 1 {
		return
	}
	w, h := img.Rect.Dx(), img.Rect.Dy()
	sx, sy := u*float64(w)-0.5, v*float64(h)-0.5
	fx, fy := math.Floor(sx), math.Floor(sy)
	ax, ay := sx-fx, sy-fy
	i0, j0 := int(fx), int(fy)
	i1, j1 := clamp(i0+1, w), clamp(j0+1, h)
	i0, j0 = clamp(i0, w), clamp(j0, h)
	p00 := img.Pix[j0*img.Stride+4*i0:]
	p10 := img.Pix[j0*img.Stride+4*i1:]
	p01 := img.Pix[j1*img.Stride+4*i0:]
	p11 := img.Pix[j1*img.Stride+4*i1:]
	for ch := range 4 {
		top := float64(p00[ch])*(1-ax) + float64(p10[ch])*ax
		bottom := float64(p01[ch])*(1-ax) + float64(p11[ch])*ax
		c[ch] += weight * (top*(1-ay) + bottom*ay) / 255
	}
}

// clamp is i clamped to [0, n)
func clamp(i, n int) int {
	return max(0, min(n-1, i))
}

// union is the smallest rectangle holding both; an empty one does not count,
// as union of the glx presenter
func union(a, b carousel.Rect) carousel.Rect {
	if a.W <= 0 || a.H <= 0 {
		return b
	}
	if b.W <= 0 || b.H <= 0 {
		return a
	}
	x0, y0 := math.Min(a.X, b.X), math.Min(a.Y, b.Y)
	x1, y1 := math.Max(a.X+a.W, b.X+b.W), math.Max(a.Y+a.H, b.Y+b.H)
	return carousel.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}
