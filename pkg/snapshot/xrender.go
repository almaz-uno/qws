package snapshot

import (
	"errors"
	"fmt"
	"image"
	"math"

	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/render"
	"github.com/jezek/xgb/shm"
	"github.com/jezek/xgb/xproto"
	"golang.org/x/sys/unix"
)

// xrender scales the pixmaps of windows in the X server through RENDER, for
// the windows whose pixmap the GPU will not bind
// (specs/018-snapshot-bind-conflicts): halved by bilinear filtering — a 2×2
// box at exactly ½ — along each side more than twice the thumbnail's, then one
// bilinear pass into a pixmap of the thumbnail's size, read back over a shared
// memory segment made once
type xrender struct {
	conn    *xgb.Conn
	root    xproto.Window
	formats map[xproto.Visualid]render.Pictformat // the picture format of each visual
	argb32  render.Pictformat                     // a8r8g8b8, of the pixmaps scaled into
	seg     shm.Seg
	shm     []byte // the segment, a thumbnail of maxSide×maxSide at most
}

const filterBilinear = "bilinear"

// newXRender sets up the RENDER path on a connection of x11.NewConn; an error
// without RENDER or MIT-SHM
func newXRender(conn *xgb.Conn, root xproto.Window) (*xrender, error) {
	// The pixels are read as the bytes of a little-endian a8r8g8b8: BGRA
	if xproto.Setup(conn).ImageByteOrder != xproto.ImageOrderLSBFirst {
		return nil, errors.New("RENDER: images of the X server are not LSBFirst")
	}
	if err := x11.CheckExtension(conn, "RENDER"); err != nil {
		return nil, fmt.Errorf("RENDER: %w", err)
	}
	if _, err := render.QueryVersion(conn, 0, 11).Reply(); err != nil {
		return nil, fmt.Errorf("RENDER: %w", err)
	}
	if err := x11.CheckExtension(conn, "MIT-SHM"); err != nil {
		return nil, fmt.Errorf("MIT-SHM: %w", err)
	}
	if _, err := shm.QueryVersion(conn).Reply(); err != nil {
		return nil, fmt.Errorf("MIT-SHM: %w", err)
	}
	formats, err := render.QueryPictFormats(conn).Reply()
	if err != nil {
		return nil, fmt.Errorf("RENDER: %w", err)
	}
	x := &xrender{conn: conn, root: root, formats: make(map[xproto.Visualid]render.Pictformat)}
	for _, s := range formats.Screens {
		for _, d := range s.Depths {
			for _, v := range d.Visuals {
				x.formats[v.Visual] = v.Format
			}
		}
	}
	argb32 := render.Directformat{RedShift: 16, RedMask: 0xff, GreenShift: 8, GreenMask: 0xff,
		BlueShift: 0, BlueMask: 0xff, AlphaShift: 24, AlphaMask: 0xff}
	for _, f := range formats.Formats {
		if f.Type == render.PictTypeDirect && f.Depth == 32 && f.Direct == argb32 {
			x.argb32 = f.Id
		}
	}
	if x.argb32 == 0 {
		return nil, errors.New("RENDER: no format a8r8g8b8")
	}

	id, err := unix.SysvShmGet(unix.IPC_PRIVATE, 4*maxSide*maxSide, unix.IPC_CREAT|0o600)
	if err != nil {
		return nil, fmt.Errorf("MIT-SHM: %w", err)
	}
	// Removed once qws and the X server detach, however qws ends
	defer unix.SysvShmCtl(id, unix.IPC_RMID, nil)
	data, err := unix.SysvShmAttach(id, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("MIT-SHM: %w", err)
	}
	seg, err := shm.NewSegId(conn)
	if err == nil {
		err = shm.AttachChecked(conn, seg, uint32(id), false).Check()
	}
	if err != nil {
		unix.SysvShmDetach(data)
		return nil, fmt.Errorf("MIT-SHM: %w", err)
	}
	x.seg, x.shm = seg, data
	return x, nil
}

// close detaches the segment
func (x *xrender) close() {
	shm.Detach(x.conn, x.seg)
	unix.SysvShmDetach(x.shm)
}

// thumbnail scales the rectangle r of a pixmap of the visual and the depth —
// a window, at r — into its thumbnail and reads it back
func (x *xrender) thumbnail(pixmap xproto.Pixmap, visual xproto.Visualid, depth int, r image.Rectangle) (*image.RGBA, error) {
	tw, th := thumbSize(r.Dx(), r.Dy())
	dst, err := xproto.NewPixmapId(x.conn)
	if err != nil {
		return nil, err
	}
	xproto.CreatePixmap(x.conn, 32, dst, xproto.Drawable(x.root), uint16(tw), uint16(th))
	defer xproto.FreePixmap(x.conn, dst)
	pic, err := render.NewPictureId(x.conn)
	if err != nil {
		return nil, err
	}
	render.CreatePicture(x.conn, pic, xproto.Drawable(dst), x.argb32, 0, nil)
	defer render.FreePicture(x.conn, pic)
	c, err := x.newChain(pixmap, visual, depth, r.Size(), r.Min != image.Point{}, pic)
	if err != nil {
		return nil, err
	}
	defer c.free()
	passes := c.run(pixmap, r.Min)

	if _, err := shm.GetImage(x.conn, xproto.Drawable(dst), 0, 0, uint16(tw), uint16(th), ^uint32(0),
		xproto.ImageFormatZPixmap, x.seg, 0).Reply(); err != nil {
		return nil, fmt.Errorf("RENDER: %w", err)
	}
	for _, p := range passes {
		if err := p.Check(); err != nil {
			return nil, fmt.Errorf("RENDER: %w", err)
		}
	}
	// BGRA to RGBA; the alpha of a window is ignored, as on the GPU and the CPU
	img := image.NewRGBA(image.Rect(0, 0, tw, th))
	for i := 0; i < 4*tw*th; i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = x.shm[i+2], x.shm[i+1], x.shm[i], 255
	}
	return img, nil
}

// chain is how RENDER scales a window into a picture a8r8g8b8 of its
// thumbnail's size — the thumbnail, or the scaled pixmap of a live pass of
// specs/020-live-thumbnails — made once and run for each picture: the copy
// of the window out of the pixmap it is drawn in, when it lies off the
// pixmap's corner, so that the passes pad at its edges, not with the frame
// around it (specs/022-uncaptured-windows); bilinear halvings along each side
// more than twice the thumbnail's; one bilinear pass into the picture. A
// snapshot makes and frees one; the passes of a window from its frame keep
// theirs for an activation (specs/023-frame-pass-cost, D1 b), so that a pass
// sends the copy and the composites alone.
type chain struct {
	x      *xrender
	size   image.Point     // the window's
	visual xproto.Visualid // of the pixmap it is taken from
	depth  int
	copies bool            // the window lies off the pixmap's corner
	pixmap xproto.Pixmap   // without a copy, the pixmap read
	dst    render.Picture  // scaled into, the caller's
	copy   xproto.Pixmap   // the copy of the window
	gc     xproto.Gcontext // its graphics context
	steps  []chainStep

	pictures []render.Picture // made by the chain, freed by it
	pixmaps  []xproto.Pixmap
}

// chainStep is a composite of the chain, from src into dst, of w×h
type chainStep struct {
	src, dst render.Picture
	w, h     int
}

// newChain makes the chain of a window of size, taken from a pixmap of the
// visual and the depth — pixmap, read without a copy, at the corner — into
// dst: the pixmaps and the pictures, each with its transform and filter
func (x *xrender) newChain(pixmap xproto.Pixmap, visual xproto.Visualid, depth int, size image.Point, copies bool, dst render.Picture) (*chain, error) {
	format, ok := x.formats[visual]
	if !ok {
		return nil, fmt.Errorf("RENDER: no picture format for visual 0x%x", visual)
	}
	c := &chain{x: x, size: size, visual: visual, depth: depth, copies: copies, dst: dst}
	fail := func(err error) (*chain, error) {
		c.free()
		return nil, err
	}
	// picture makes a picture of the drawable; the filter reads past its
	// edges the pixels at the edges
	picture := func(d xproto.Drawable, f render.Pictformat) (render.Picture, error) {
		p, err := render.NewPictureId(x.conn)
		if err != nil {
			return 0, err
		}
		render.CreatePicture(x.conn, p, d, f, render.CpRepeat, []uint32{render.RepeatPad})
		c.pictures = append(c.pictures, p)
		return p, nil
	}

	w, h := size.X, size.Y
	source := xproto.Drawable(pixmap)
	if copies {
		var err error
		if c.copy, err = xproto.NewPixmapId(x.conn); err != nil {
			return fail(err)
		}
		xproto.CreatePixmap(x.conn, byte(depth), c.copy, xproto.Drawable(x.root), uint16(w), uint16(h))
		if c.gc, err = xproto.NewGcontextId(x.conn); err != nil {
			c.gc = 0
			return fail(err)
		}
		// Without graphics exposures, the protocol's default, each copy
		// sends a NoExposure event, which woke the snapshotter's loop for
		// nothing (specs/023-frame-pass-cost, D3)
		xproto.CreateGC(x.conn, c.gc, xproto.Drawable(c.copy), xproto.GcGraphicsExposures, []uint32{0})
		source = xproto.Drawable(c.copy)
	} else {
		c.pixmap = pixmap
	}
	src, err := picture(source, format)
	if err != nil {
		return fail(err)
	}
	// The sides of the window in the pixels of the last pass: a halving keeps
	// every pixel where it was, at half the scale, and pads an odd side with
	// its last pixel, so the thumbnail is scaled from the window's own extent
	tw, th := thumbSize(w, h)
	fw, fh := float64(w), float64(h)
	for final := false; !final; {
		final = fw <= float64(2*tw) && fh <= float64(2*th)
		sx, sy, nw, nh := fw/float64(tw), fh/float64(th), tw, th
		next := dst
		if !final {
			sx, sy = 1, 1
			if fw > float64(2*tw) {
				sx = 2
			}
			if fh > float64(2*th) {
				sy = 2
			}
			fw, fh = fw/sx, fh/sy
			nw, nh = int(math.Ceil(fw)), int(math.Ceil(fh))
			half, err := xproto.NewPixmapId(x.conn)
			if err != nil {
				return fail(err)
			}
			xproto.CreatePixmap(x.conn, 32, half, xproto.Drawable(x.root), uint16(nw), uint16(nh))
			c.pixmaps = append(c.pixmaps, half)
			if next, err = picture(xproto.Drawable(half), x.argb32); err != nil {
				return fail(err)
			}
		}
		// A pixel of the destination samples the source at its centre scaled:
		// at ½ exactly, the corner of four pixels, averaged. Each picture is
		// the source of one step: its transform and filter set once.
		render.SetPictureTransform(x.conn, src, render.Transform{
			Matrix11: fixed(sx), Matrix22: fixed(sy), Matrix33: fixed(1)})
		render.SetPictureFilter(x.conn, src, uint16(len(filterBilinear)), filterBilinear, nil)
		c.steps = append(c.steps, chainStep{src, next, nw, nh})
		src = next
	}
	return c, nil
}

// fits reports whether the chain scales a window of size, taken from the
// pixmap, of the visual and the depth, copied out or not, into dst
func (c *chain) fits(pixmap xproto.Pixmap, visual xproto.Visualid, depth int, size image.Point, copies bool, dst render.Picture) bool {
	return c.size == size && c.visual == visual && c.depth == depth && c.copies == copies &&
		c.dst == dst && (copies || c.pixmap == pixmap)
}

// run sends the copy of the window at at in the pixmap, when the chain
// copies, and the composites, and returns their cookies: a failed request
// leaves a pass it feeds failing too, so the passes alone are checked, once
// the X server has answered a request after them, without a round trip of
// their own
func (c *chain) run(pixmap xproto.Pixmap, at image.Point) []render.CompositeCookie {
	x := c.x
	if c.copies {
		xproto.CopyArea(x.conn, xproto.Drawable(pixmap), xproto.Drawable(c.copy), c.gc,
			int16(at.X), int16(at.Y), 0, 0, uint16(c.size.X), uint16(c.size.Y))
	}
	passes := make([]render.CompositeCookie, len(c.steps))
	for i, st := range c.steps {
		passes[i] = render.CompositeChecked(x.conn, render.PictOpSrc, st.src, 0, st.dst,
			0, 0, 0, 0, 0, 0, uint16(st.w), uint16(st.h))
	}
	return passes
}

// free frees what the chain made: the X server frees it after the requests
// before
func (c *chain) free() {
	x := c.x
	for _, p := range c.pictures {
		render.FreePicture(x.conn, p)
	}
	for _, p := range c.pixmaps {
		xproto.FreePixmap(x.conn, p)
	}
	if c.gc != 0 {
		xproto.FreeGC(x.conn, c.gc)
	}
	if c.copy != 0 {
		xproto.FreePixmap(x.conn, c.copy)
	}
	c.pictures, c.pixmaps, c.gc, c.copy = nil, nil, 0, 0
}

// fixed is the 16.16 fixed point number of RENDER nearest to f
func fixed(f float64) render.Fixed {
	return render.Fixed(math.Round(f * 65536))
}
