package carousel

import (
	"bytes"
	"image"
	"math"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/fogleman/gg"
)

// Heads of grid tiles drawn apart from the canvas (specs/019-grid-speed).
//
// A tile of the grid is drawn as its head — the shadow of a selected or a
// hovered tile, the background, the thumbnail with its border, the icon, the
// bar under an urgent title — and then its tail: the title, the workspace and
// the frame. Each operation of the head writes a pixel as a function of the
// pixel under it: gg's fills and strokes compose a colour over it by the
// coverage of a path, x/image/draw resamples an image onto it. The coverage,
// the resampling and the clipping depend on the size of the canvas — gg's
// rasterizer subdivides curves by it — but not on its contents. A head drawn
// with a context of its own on a scratch canvas of the same size, onto a
// rectangle filled with one colour, therefore gets in that rectangle the
// pixels it gets on the canvas wherever the canvas is that colour in that
// rectangle — provided the rectangle holds every pixel the head writes
// (gridHeadBounds).
//
// The heads of a frame are drawn so in parallel, into one scratch canvas, each
// in its own rectangle; a tile whose rectangle meets that of another is drawn
// in place. The thumbnails, which do not depend on the canvas, are resampled
// before it is made (prepareOpaque). The tiles are then drawn in order, and a
// tile uses its head only if the canvas is still that colour everywhere in the
// rectangle: the tail of a tile drawn before it may have reached into it.

// gridTiles is the tiles of a grid being drawn
type gridTiles struct {
	windows         []WindowData
	selected, hover int
	cfg             Config
	grid            gridLayout
	canvas          image.Rectangle // the bounds of the canvas they are drawn on
	heads           []*tileHead     // nil for a head drawn in place
	scratch         *image.RGBA     // the canvas the heads are drawn into
	base            chan struct{}   // closed once every head knows the pixel it is drawn on
}

// tileHead is the head of a tile drawn into the scratch canvas
type tileHead struct {
	sync.WaitGroup                 // done when the head is drawn
	bounds         image.Rectangle // every pixel the head writes, within the canvas
	under          []byte          // a row of the pixel the head is drawn on
	scratch        *image.RGBA
}

// startGridTiles lays out the tiles of the windows on a canvas of the size of
// cfg below top and starts drawing their heads, in the order of the tiles, on
// half of the CPUs: the resampling gains nothing from the second thread of a
// core, and the other half stays with the drawing of the canvas and the loop
// of the animation (specs/019-grid-speed, research)
func startGridTiles(windowData []WindowData, top float64, selected, hoverIndex int, cfg Config) *gridTiles {
	t := &gridTiles{
		windows:  windowData,
		selected: selected,
		hover:    hoverIndex,
		cfg:      cfg,
		grid:     layoutGrid(len(windowData), top, cfg),
		canvas:   image.Rect(0, 0, cfg.Width, cfg.Height),
		heads:    make([]*tileHead, len(windowData)),
		base:     make(chan struct{}),
	}
	g := t.grid
	for i := range windowData {
		x, y := g.tile(i)
		r, ok := gridHeadBounds(t.canvas, &windowData[i], x, y, g.tileW, g.tileH, i == selected, i == hoverIndex, cfg)
		if ok && !r.Empty() {
			t.heads[i] = &tileHead{bounds: r}
		}
	}
	for i, a := range t.heads {
		for j := i + 1; a != nil && j < len(t.heads); j++ {
			if b := t.heads[j]; b != nil && a.bounds.Overlaps(b.bounds) {
				t.heads[i], t.heads[j] = nil, nil
			}
		}
	}

	var jobs []func()
	for i, head := range t.heads {
		if head == nil {
			continue
		}
		if t.scratch == nil {
			t.scratch = headCanvas(t.canvas)
		}
		head.scratch = t.scratch
		head.Add(1)
		jobs = append(jobs, func() {
			defer head.Done()
			win := &windowData[i]
			x, y := g.tile(i)
			var thumb *preparedImage
			if win.Thumbnail != nil {
				thumbX, thumbY, _, _, scale := gridThumbnail(win.Thumbnail.Bounds(), g.tileW, g.tileH)
				thumb = prepareOpaque(t.canvas, gridImageMatrix(x, y, thumbX, thumbY, scale), win.Thumbnail)
			}
			<-t.base
			r := head.bounds
			for y := r.Min.Y; y < r.Max.Y; y++ {
				copy(head.scratch.Pix[head.scratch.PixOffset(r.Min.X, y):], head.under)
			}
			drawGridTileHead(gg.NewContextForRGBA(head.scratch), win, x, y, g.tileW, g.tileH, i == selected, i == hoverIndex, cfg, thumb)
		})
	}
	runJobs(jobs, runtime.GOMAXPROCS(0)/2)
	return t
}

// draw draws each window in its grid cell on dc, one after another, and
// returns the number of heads drawn in place; a tile waits for its head
func (t *gridTiles) draw(dc *gg.Context) (inPlace int) {
	canvas := dc.Image().(*image.RGBA)
	usable := canvas.Rect == t.canvas
	for _, head := range t.heads {
		if head != nil && usable {
			r := head.bounds
			head.under = bytes.Repeat(canvas.Pix[canvas.PixOffset(r.Min.X, r.Min.Y):][:4], r.Dx())
		}
	}
	close(t.base)

	g := t.grid
	for i := range t.windows {
		x, y := g.tile(i)
		head := t.heads[i]
		if head != nil {
			head.Wait()
		}
		if !usable {
			head = nil
		}
		if !drawGridTile(dc, &t.windows[i], x, y, g.tileW, g.tileH, i == t.selected, i == t.hover, t.cfg, head) {
			inPlace++
		}
	}
	if t.scratch != nil {
		Recycle(t.scratch)
	}
	return inPlace
}

// drawTo copies the head into the canvas if the canvas is everywhere in its
// rectangle the pixel the head was drawn on; otherwise it returns false, and
// the head must be drawn in place
func (h *tileHead) drawTo(dst *image.RGBA) bool {
	r := h.bounds
	for y := r.Min.Y; y < r.Max.Y; y++ {
		if !bytes.Equal(dst.Pix[dst.PixOffset(r.Min.X, y):][:len(h.under)], h.under) {
			return false
		}
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		copy(dst.Pix[dst.PixOffset(r.Min.X, y):][:len(h.under)], h.scratch.Pix[h.scratch.PixOffset(r.Min.X, y):])
	}
	return true
}

// headCanvas is a scratch canvas with bounds r from the free list of
// canvases; its pixels are left as they were
func headCanvas(r image.Rectangle) *image.RGBA {
	img, _ := takeImage(r)
	return img
}

// runJobs runs the jobs in their order on n goroutines, at least one, and
// returns without waiting for them
func runJobs(jobs []func(), n int) {
	var next atomic.Int64
	for range min(max(n, 1), len(jobs)) {
		go func() {
			for j := next.Add(1) - 1; j < int64(len(jobs)); j = next.Add(1) - 1 {
				jobs[j]()
			}
		}()
	}
}

// gridHeadBounds is a rectangle of the canvas that holds every pixel
// drawGridTileHead writes for the tile of win at (x, y), of size w×h; false
// when its geometry is not finite.
//
// A fill writes only the pixels its path passes through or encloses, within
// the rectangle of the path rounded out to whole pixels; a stroke, those
// within half its width of the path; a resampled image, those of the
// rectangle placement gives. The arcs of rounded corners, quadratic curves,
// leave their squares by less than a hundredth of the radius. The margin of
// two pixels is to spare.
func gridHeadBounds(canvas image.Rectangle, win *WindowData, x, y, w, h float64, isSelected, isHovered bool, cfg Config) (image.Rectangle, bool) {
	const margin = 2
	var r image.Rectangle
	ok := true
	add := func(b image.Rectangle, finite bool) {
		r, ok = r.Union(b), ok && finite
	}
	// rect is the pixels within d of the rectangle (rx, ry, rw, rh) of the
	// tile
	rect := func(rx, ry, rw, rh, d float64) (image.Rectangle, bool) {
		x0, x1 := math.Min(x+rx, x+rx+rw)-d, math.Max(x+rx, x+rx+rw)+d
		y0, y1 := math.Min(y+ry, y+ry+rh)-d, math.Max(y+ry, y+ry+rh)+d
		for _, v := range [...]float64{x0, x1, y0, y1} {
			if math.IsNaN(v) || math.Abs(v) > 1<<24 {
				return image.Rectangle{}, false
			}
		}
		return image.Rect(int(math.Floor(x0)), int(math.Floor(y0)), int(math.Ceil(x1)), int(math.Ceil(y1))), true
	}
	// rounded is rect for a rounded rectangle of radius rr, whose arcs leave
	// the rectangle when the radius is larger than a side
	rounded := func(rx, ry, rw, rh, rr, d float64) (image.Rectangle, bool) {
		x0, x1 := math.Min(rx, rx+rw-rr), math.Max(rx+rw, rx+rr)
		y0, y1 := math.Min(ry, ry+rh-rr), math.Max(ry+rh, ry+rr)
		return rect(x0, y0, x1-x0, y1-y0, d)
	}
	resampled := func(src image.Image, ix, iy, s float64) (image.Rectangle, bool) {
		b := src.Bounds()
		if _, finite := rect(ix, iy, s*float64(b.Dx()), s*float64(b.Dy()), 0); !finite {
			return image.Rectangle{}, false
		}
		_, p := placement(canvas, gridImageMatrix(x, y, ix, iy, s), src)
		return p, true
	}

	if isSelected || isHovered {
		o := gridShadowOffset(isSelected, cfg)
		add(rounded(o, o, w, h, 8, margin))
	}
	add(rounded(0, 0, w, h, 8, margin))
	if win.Thumbnail != nil {
		thumbX, thumbY, scaledW, scaledH, scale := gridThumbnail(win.Thumbnail.Bounds(), w, h)
		add(resampled(win.Thumbnail, thumbX, thumbY, scale))
		add(rect(thumbX, thumbY, scaledW, scaledH, 0.5+margin))
	}
	if win.Icon != nil {
		if iconScale, has := gridIconScale(win.Icon.Bounds()); has {
			add(resampled(win.Icon, gridIconPadding, gridIconPadding, iconScale))
		}
	}
	if win.Title != "" && win.Urgent {
		add(rounded(5, h-gridTitleY-5, w-10, 30, 4, margin))
	}
	return r.Intersect(canvas), ok
}

// gridImageMatrix is the matrix drawGridTileHead draws an image under: in the
// tile whose top-left corner is (x, y), at (ix, iy) from it, at scale s
func gridImageMatrix(x, y, ix, iy, s float64) gg.Matrix {
	return gg.Identity().Translate(x, y).Translate(ix, iy).Scale(s, s)
}
