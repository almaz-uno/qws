package snapshot

import (
	"image"
	"math/rand"
	"runtime"
	"testing"

	"github.com/almaz-uno/qws/pkg/glx"
	"github.com/go-gl/gl/v4.6-core/gl"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// TestGPUThumbnail checks criterion K4 of specs/008-window-snapshots: a pixmap
// bound as a texture and averaged on the GPU gives the area average of its
// pixels computed on the CPU, within 1 per channel; a pixmap no larger than
// a thumbnail comes out as it is. Then the pixmap is drawn anew and bound
// again, as a window that changed is, and the thumbnail is that of the new
// contents. Pixmaps of depth 24 and 32, as windows have. Each binding waits
// for the X server's drawing first, as captureGPU does: without it the
// thumbnail drawn anew was stale in most runs on NVIDIA with the desktop in
// use (specs/025-snapshot-wait-x). Needs an X display with
// GLX_EXT_texture_from_pixmap, not a compositor: the pixmaps are made here.
func TestGPUThumbnail(t *testing.T) {
	conn, err := xgb.NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer conn.Close()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	o, err := glx.NewOffscreen()
	if err != nil {
		t.Skipf("no offscreen GLX: %v", err)
	}
	defer o.Destroy()
	g, err := newGPU()
	if err != nil {
		t.Fatal(err)
	}
	defer g.close()

	screen := xproto.Setup(conn).DefaultScreen(conn)
	for _, depth := range []int{24, 32} {
		for _, size := range []image.Point{{1500, 900}, {2560, 1381}, {300, 200}, {511, 700}} {
			first := testImage(size.X, size.Y, int64(depth*size.X))
			pixmap := newPixmap(t, conn, screen.Root, depth, size)
			fillPixmap(t, conn, pixmap, depth, first)

			tex := newWindowTexture()
			o.WaitX()
			tp, err := o.BindPixmap(uint32(pixmap), depth)
			if err != nil {
				t.Fatalf("depth %d: %v", depth, err)
			}
			checkThumbnail(t, g.thumbnail(size.X, size.Y, tp.YInverted), first, "first", depth, size)

			second := testImage(size.X, size.Y, int64(depth*size.X+1))
			fillPixmap(t, conn, pixmap, depth, second)
			o.WaitX()
			tp.Rebind()
			checkThumbnail(t, g.thumbnail(size.X, size.Y, tp.YInverted), second, "drawn anew", depth, size)

			tp.Release()
			gl.DeleteTextures(1, &tex)
			xproto.FreePixmap(conn, pixmap)
		}
	}
}

// checkThumbnail compares a thumbnail with the area average of the source
func checkThumbnail(t *testing.T, got, src *image.RGBA, what string, depth int, size image.Point) {
	t.Helper()
	want := areaAverage(src, got.Rect.Dx(), got.Rect.Dy())
	worst, n := 0, 0
	for i := range got.Pix {
		d := int(got.Pix[i]) - int(want.Pix[i])
		if d < 0 {
			d = -d
		}
		if d > 0 {
			n++
		}
		worst = max(worst, d)
	}
	small := size.X <= maxSide && size.Y <= maxSide
	if worst > 1 || small && worst > 0 {
		t.Errorf("depth %d, %v → %v, %s: %d channels differ, by up to %d", depth, size, got.Rect.Size(), what, n, worst)
	}
}

// testImage is a gradient with noise and sharp edges, opaque
func testImage(w, h int, seed int64) *image.RGBA {
	r := rand.New(rand.NewSource(seed))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := img.PixOffset(x, y)
			img.Pix[o] = uint8(x * 255 / w)
			img.Pix[o+1] = uint8(y * 255 / h)
			img.Pix[o+2] = uint8(r.Intn(256))
			if (x/37+y/23)%2 == 0 {
				img.Pix[o+1] = 255 - img.Pix[o+1]
			}
			img.Pix[o+3] = 255
		}
	}
	return img
}

// newPixmap makes a pixmap of the depth and size
func newPixmap(t *testing.T, conn *xgb.Conn, root xproto.Window, depth int, size image.Point) xproto.Pixmap {
	pixmap, err := xproto.NewPixmapId(conn)
	if err != nil {
		t.Fatal(err)
	}
	if err := xproto.CreatePixmapChecked(conn, byte(depth), pixmap, xproto.Drawable(root), uint16(size.X), uint16(size.Y)).Check(); err != nil {
		t.Skipf("no pixmap of depth %d: %v", depth, err)
	}
	return pixmap
}

// fillPixmap draws the image into the pixmap, BGRA as the X server keeps 24-
// and 32-bit pixels on this machine
func fillPixmap(t *testing.T, conn *xgb.Conn, pixmap xproto.Pixmap, depth int, img *image.RGBA) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	gc, _ := xproto.NewGcontextId(conn)
	xproto.CreateGC(conn, gc, xproto.Drawable(pixmap), 0, nil)
	defer xproto.FreeGC(conn, gc)

	// In strips within the largest request without BIG-REQUESTS, 256 KB
	rows := max(1, 200000/(4*w))
	for y0 := 0; y0 < h; y0 += rows {
		y1 := min(y0+rows, h)
		data := make([]byte, 4*w*(y1-y0))
		for y := y0; y < y1; y++ {
			for x := 0; x < w; x++ {
				s, d := img.PixOffset(x, y), 4*((y-y0)*w+x)
				data[d], data[d+1], data[d+2], data[d+3] = img.Pix[s+2], img.Pix[s+1], img.Pix[s], 255
			}
		}
		if err := xproto.PutImageChecked(conn, xproto.ImageFormatZPixmap, xproto.Drawable(pixmap), gc,
			uint16(w), uint16(y1-y0), 0, int16(y0), 0, byte(depth), data).Check(); err != nil {
			t.Fatal(err)
		}
	}
}

// areaAverage scales the image to tw×th by the exact area average
func areaAverage(img *image.RGBA, tw, th int) *image.RGBA {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	out := image.NewRGBA(image.Rect(0, 0, tw, th))
	fx, fy := float64(w)/float64(tw), float64(h)/float64(th)
	for y := 0; y < th; y++ {
		y0, y1 := float64(y)*fy, float64(y+1)*fy
		for x := 0; x < tw; x++ {
			x0, x1 := float64(x)*fx, float64(x+1)*fx
			var sum [3]float64
			var area float64
			for sy := int(y0); float64(sy) < y1 && sy < h; sy++ {
				wy := min(float64(sy+1), y1) - max(float64(sy), y0)
				for sx := int(x0); float64(sx) < x1 && sx < w; sx++ {
					wx := min(float64(sx+1), x1) - max(float64(sx), x0)
					o := img.PixOffset(sx, sy)
					for c := 0; c < 3; c++ {
						sum[c] += wx * wy * float64(img.Pix[o+c])
					}
					area += wx * wy
				}
			}
			o := out.PixOffset(x, y)
			for c := 0; c < 3; c++ {
				out.Pix[o+c] = uint8(sum[c]/area + 0.5)
			}
			out.Pix[o+3] = 255
		}
	}
	return out
}
