package carousel

import (
	"cmp"
	"image"
	"slices"
	"sync"
)

// The canvases of the frames and the layers, reused (specs/030-drawing-memory,
// D3). A frame of E1 is 2520×1400×4 bytes, 14.1 MB, a tiles layer up to that,
// a card a few megabytes, drawn on a band of the frame and cropped. Made anew
// for each drawing, they were most of what the drawing allocated, and the
// drawings that run in parallel held theirs at once: the peak of the heap. A
// canvas is now taken from a free list of pixel buffers by size and given
// back once nothing reads it — here, a canvas once cropped and the scratch
// canvas of the heads once copied; in pkg/ui, a layer once the presenter has
// uploaded it, a frame once the presenter no longer holds it (Recycle).
//
// A canvas taken keeps the bytes it had, and every drawing writes all of
// them before it reads one: a canvas of the window background is a copy of
// it or a clear to its colour (newCanvas), a transparent one is cleared
// (clearImage), a crop copies every row, a head of the grid fills its
// rectangle of the scratch canvas before it draws there. Its bounds and its
// stride are those of a canvas made anew, which is all gg's rasterizer
// depends on: its pixels are those of a canvas made anew.

// canvases is the free list of the pixel buffers of canvases
var canvases pixelList

// pixelList is a free list of pixel buffers by capacity. It keeps at most
// eight times the largest buffer it was given — eight frames — in all: the
// drawings in flight give theirs back, and those of sizes no longer asked
// for go to the collector.
type pixelList struct {
	mu      sync.Mutex
	free    [][]byte // ascending by capacity
	held    int      // their capacities in all
	largest int      // the largest capacity given
}

// take is a buffer of n bytes and whether it was free: the smallest free
// one of a capacity from n to half as much again, else a new one, zero. The
// bytes of a free one are as they were.
func (l *pixelList) take(n int) ([]byte, bool) {
	l.mu.Lock()
	i, _ := slices.BinarySearchFunc(l.free, n, byCapacity)
	if i < len(l.free) && cap(l.free[i]) <= n+n/2 {
		b := l.free[i]
		l.free = slices.Delete(l.free, i, i+1)
		l.held -= cap(b)
		l.mu.Unlock()
		return b[:n], true
	}
	l.mu.Unlock()
	return make([]byte, n), false
}

// give makes a buffer free; nothing reads or writes it after
func (l *pixelList) give(b []byte) {
	if cap(b) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.largest = max(l.largest, cap(b))
	if l.held+cap(b) > 8*l.largest {
		return
	}
	i, _ := slices.BinarySearchFunc(l.free, cap(b), byCapacity)
	l.free = slices.Insert(l.free, i, b[:0])
	l.held += cap(b)
}

// byCapacity orders a buffer by its capacity against n
func byCapacity(b []byte, n int) int {
	return cmp.Compare(cap(b), n)
}

// takeImage is an image with bounds r, its pixels from the free list: as
// they were, unless fresh, then zero
func takeImage(r image.Rectangle) (img *image.RGBA, fresh bool) {
	pix, free := canvases.take(4 * r.Dx() * r.Dy())
	return &image.RGBA{Pix: pix, Stride: 4 * r.Dx(), Rect: r}, !free
}

// clearImage is a transparent image with bounds r, as image.NewRGBA makes
// it, its pixels from the free list
func clearImage(r image.Rectangle) *image.RGBA {
	img, fresh := takeImage(r)
	if !fresh {
		// A row at a time, as copyRow copies
		for y := 0; y < len(img.Pix); y += img.Stride {
			clearRow(img.Pix[y : y+img.Stride])
		}
	}
	return img
}

// clearRow zeroes a row of pixels
//
//go:noinline
func clearRow(row []byte) {
	clear(row)
}

// Recycle gives the pixels of img, a frame or a layer drawn here, back for
// the canvases of the drawings after it: once nothing reads or writes img
// any more (specs/030-drawing-memory, D3)
func Recycle(img *image.RGBA) {
	if img != nil {
		canvases.give(img.Pix)
	}
}
