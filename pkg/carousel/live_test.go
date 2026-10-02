package carousel

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/almaz-uno/qws/pkg/glx"
	"github.com/fogleman/gg"
	"github.com/go-gl/gl/v4.6-core/gl"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/image/font/gofont/goregular"
)

// liveScene is a frame at rest TestLiveThumbnails draws live pictures over
type liveScene struct {
	name     string
	windows  []WindowData
	selected int
	cfg      Config
}

// liveScenes are the carousel of E1 with every card at offsets −3 to 3 —
// those of ±4 and ±5 lie outside its frame — and labels long enough to reach
// over the thumbnails of the cards before them; the carousel at the sizes of
// the defaults, every card at offsets 0 to ±5 in the frame; the grid of 36 of
// E1, an icon over every thumbnail; and that grid in a font whose titles
// reach into the thumbnails
func liveScenes(fonts []string) []liveScene {
	e1 := sceneConfig(scene{layout: "carousel", theme: "dark", width: 2520, height: 1400}, fonts)
	carousel := liveWindows(13, image.Pt(512, 271))
	carousel[7].Title = strings.Repeat("W", 40)
	carousel[8].Workspace = strings.Repeat("W", 30)
	carousel[4].Title = strings.Repeat("Ш", 40)

	small := e1
	small.ThumbWidth, small.ThumbHeight, small.Spacing, small.FontSize = 256, 256, 300, 14

	grid := sceneConfig(scene{layout: "grid", theme: "dark", width: 2520, height: 1400}, fonts)
	large := grid
	large.FontSize = 30
	return []liveScene{
		{"carousel-e1", carousel, 6, e1},
		{"carousel-defaults", carousel, 6, small},
		{"grid-e1-36", liveWindows(36, image.Pt(512, 271), image.Pt(512, 160)), 1, grid},
		{"grid-e1-36-font-30", liveWindows(36, image.Pt(512, 271), image.Pt(512, 160)), 1, large},
	}
}

// liveWindows are n windows of E1, their snapshots of the sizes in turn,
// each with an icon, a title and a workspace. In a tile of the grid of 36 of
// E1 a snapshot of 512×271 lies clear of the icon, one of 512×160 under it.
func liveWindows(n int, sizes ...image.Point) []WindowData {
	windows := make([]WindowData, n)
	for i := range windows {
		size := sizes[i%len(sizes)]
		windows[i] = WindowData{
			Thumbnail: livePicture(size.X, size.Y, int64(i)),
			Icon:      iconRGBA(48, 48),
			Title:     fmt.Sprintf("Terminal %d — ~/wsp/pet/qws", i),
			Workspace: fmt.Sprintf("%d", i%5+1),
		}
	}
	return windows
}

// livePicture is a thumbnail about as hard to scale as a window: lines of
// strokes on a light page, a dark panel, a bar with a gradient
// (~/.cache/qws-probe-live/kernel of the research)
func livePicture(w, h int, seed int64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(seed))
	page := color.RGBA{uint8(220 + r.Intn(30)), uint8(220 + r.Intn(30)), uint8(210 + r.Intn(30)), 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := page
			if x < w/6 {
				c = color.RGBA{40, 44, 52, 255}
			}
			if y < 14 {
				c = color.RGBA{uint8(x * 255 / w), uint8(255 - x*255/w), uint8(seed * 37), 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	for line := 0; line < h/10; line++ {
		for i := r.Intn(w / 6); i < w/6+r.Intn(5*w/6); i += 5 {
			x, y := w/6+i, 18+line*10
			for k := 0; k < 1+r.Intn(5); k++ {
				if x+k%2 < w && y+k < h {
					img.SetRGBA(x+k%2, y+k, color.RGBA{uint8(r.Intn(90)), uint8(r.Intn(90)), uint8(r.Intn(160)), 255})
				}
			}
		}
	}
	return img
}

// shareThread runs functions on a goroutine locked to its thread, where an
// offscreen context like the snapshotter's is current
type shareThread struct {
	do    chan func()
	share *glx.Share
}

func (s *shareThread) run(fn func()) {
	done := make(chan struct{})
	s.do <- func() { fn(); close(done) }
	<-done
}

// newShareThread starts the thread of the context the presenter is to share
// with; it skips the test without offscreen GLX
func newShareThread(t testing.TB) *shareThread {
	s := &shareThread{do: make(chan func())}
	ready := make(chan error)
	go func() {
		runtime.LockOSThread()
		off, err := glx.NewOffscreen()
		if err != nil {
			ready <- err
			return
		}
		defer off.Destroy()
		if err := gl.Init(); err != nil {
			ready <- err
			return
		}
		s.share = off.Share()
		ready <- nil
		for fn := range s.do {
			fn()
		}
	}()
	if err := <-ready; err != nil {
		t.Skipf("no offscreen GLX: %v", err)
	}
	t.Cleanup(func() { close(s.do) })
	return s
}

// upload makes a texture of the picture in the shared context and waits for
// it to complete there, as the snapshotter publishes its live pictures
func (s *shareThread) upload(img *image.RGBA) uint32 {
	var tex uint32
	s.run(func() {
		gl.GenTextures(1, &tex)
		gl.BindTexture(gl.TEXTURE_2D, tex)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
		gl.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
		gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, int32(img.Rect.Dx()), int32(img.Rect.Dy()), 0,
			gl.RGBA, gl.UNSIGNED_BYTE, unsafe.Pointer(&img.Pix[0]))
		gl.Finish()
	})
	return tex
}

// newLiveTestPresenter is the GLX presenter sharing the objects of the
// thread's context; it skips the test without X, a compositing manager or a
// context that shares
func newLiveTestPresenter(t *testing.T, share *glx.Share) (*glxPresenter, *xgb.Conn) {
	conn, err := xgb.NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	t.Cleanup(conn.Close)
	if !compositing(conn) {
		t.Skip("no compositing manager: the pixels of an off-screen window are undefined")
	}
	presenter, err := newLivePresenter(share)
	if err != nil {
		t.Skipf("no GLX: %v", err)
	}
	t.Cleanup(presenter.Close)
	p := presenter.(*glxPresenter)
	if !p.Live() {
		t.Skip("the presenter's context does not share the snapshotter's")
	}
	return p, conn
}

// textureImage reads a texture back in the current context
func textureImage(tex uint32, size image.Point) *image.RGBA {
	img := image.NewRGBA(image.Rectangle{Max: size})
	gl.BindTexture(gl.TEXTURE_2D, tex)
	gl.PixelStorei(gl.PACK_ALIGNMENT, 1)
	gl.GetTexImage(gl.TEXTURE_2D, 0, gl.RGBA, gl.UNSIGNED_BYTE, unsafe.Pointer(&img.Pix[0]))
	return img
}

// liveMask marks the pixels the items draw: their rectangles less their holes
func liveMask(size image.Point, items []LiveItem) []bool {
	mask := make([]bool, size.X*size.Y)
	for _, it := range items {
		if it.Texture == 0 {
			continue
		}
		for y := it.Rect.Min.Y; y < it.Rect.Max.Y; y++ {
		pixels:
			for x := it.Rect.Min.X; x < it.Rect.Max.X; x++ {
				for _, h := range it.Holes {
					if (image.Point{x, y}).In(h) {
						continue pixels
					}
				}
				mask[y*size.X+x] = true
			}
		}
	}
	return mask
}

// TestLiveThumbnails checks criterion K3 of specs/020-live-thumbnails: live
// pictures — textures written in a context like the snapshotter's, which the
// presenter's shares — drawn over a carousel at rest at offsets 0 to ±5 and
// over grids of 36 with an icon over every thumbnail: outside the live
// rectangles the frame is the cpu frame byte for byte; inside, within 1 per
// channel of the cpu frame drawn with the pictures read back as thumbnails,
// and equal to it in at least 99.5 % of the pixels. The same in a scene whose
// base is the frame and whose items carry the live pictures. Through the zoom
// of a fade, a live rectangle lies where the zoom takes it, within a pixel.
// Skipped without an X display, a compositing manager, GLX or a context that
// shares, as TestGLXPresenterMatchesCPU.
func TestLiveThumbnails(t *testing.T) {
	share := newShareThread(t)
	p, conn := newLiveTestPresenter(t, share.share)

	goFont := filepath.Join(t.TempDir(), "goregular.ttf")
	if err := os.WriteFile(goFont, goregular.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	const width, height = 2520, 1400
	w, err := NewWindowAt(conn, root, -width-100, 0, width, height, p.VisualID())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Show(); err != nil {
		t.Fatal(err)
	}
	if err := p.Bind(w); err != nil {
		t.Fatal(err)
	}

	for _, sc := range liveScenes([]string{goFont}) {
		// The live pictures, other than the snapshots the frame is drawn from
		pictures := make([]uint32, len(sc.windows))
		live := make([]WindowData, len(sc.windows))
		copy(live, sc.windows)
		for i := range sc.windows {
			b := sc.windows[i].Thumbnail.Bounds()
			pictures[i] = share.upload(livePicture(b.Dx(), b.Dy(), int64(100+i)))
			live[i].Thumbnail = textureImage(pictures[i], b.Size())
		}

		var frame, want *image.RGBA
		var items []LiveItem
		if sc.cfg.LayoutMode == "grid" {
			frame = DrawGridLayout(sc.windows, sc.selected, -1, sc.cfg)
			want = DrawGridLayout(live, sc.selected, -1, sc.cfg)
			items = GridLive(sc.windows, sc.cfg)
		} else {
			frame = Draw3DCarouselWithData(sc.windows, sc.selected, -1, 0, sc.cfg)
			want = Draw3DCarouselWithData(live, sc.selected, -1, 0, sc.cfg)
			items = CarouselLive(sc.windows, sc.selected, -1, sc.cfg)
			for k := range sc.windows {
				if o := k - sc.selected; o >= -5 && o <= 5 && items[k].Rect.Empty() && sc.name == "carousel-defaults" {
					t.Errorf("%s: no live rectangle at offset %d", sc.name, o)
				}
			}
		}
		holes := 0
		for i := range items {
			if !items[i].Rect.Empty() {
				items[i].Texture = pictures[i]
				holes += len(items[i].Holes)
			}
		}
		t.Logf("%s: %d holes", sc.name, holes)
		mask := liveMask(frame.Rect.Size(), items)

		check := func(how string, got *image.RGBA) {
			t.Helper()
			outside, inside, equal, worst := 0, 0, 0, 0
			for i := 0; i < len(mask); i++ {
				g, f, e := got.Pix[4*i:4*i+4], frame.Pix[4*i:4*i+4], want.Pix[4*i:4*i+4]
				if !mask[i] {
					if !bytes.Equal(g, f) {
						outside++
					}
					continue
				}
				inside++
				d := 0
				for c := 0; c < 4; c++ {
					d = max(d, int(math.Abs(float64(int(g[c])-int(e[c])))))
				}
				if d == 0 {
					equal++
				}
				worst = max(worst, d)
			}
			share := 100 * float64(equal) / float64(max(inside, 1))
			t.Logf("%s, %s: %d live pixels, %.3f %% equal, worst %d", sc.name, how, inside, share, worst)
			if inside == 0 {
				t.Errorf("%s, %s: no live pixel", sc.name, how)
			}
			if outside > 0 {
				t.Errorf("%s, %s: %d pixels outside the live rectangles differ from the cpu frame", sc.name, how, outside)
			}
			if worst > 1 || share < 99.5 {
				t.Errorf("%s, %s: inside, %.3f %% equal to the cpu frame of the pictures, worst %d", sc.name, how, share, worst)
			}
		}

		if err := p.draw(frame); err != nil {
			t.Fatal(err)
		}
		p.SetLiveItems(items)
		p.drawLiveItems(Opaque)
		check("at rest", p.readBack())

		if err := p.SetLayer(1, frame); err != nil {
			t.Fatal(err)
		}
		var scene []SceneItem
		for i := range items {
			scene = append(scene, SceneItem{Live: &items[i]})
		}
		if err := p.drawScene(1, scene, Opaque); err != nil {
			t.Fatal(err)
		}
		check("in a scene", p.readBack())
		p.DropLayers()

		share.run(func() { gl.DeleteTextures(int32(len(pictures)), &pictures[0]) })
	}

	// Through the zoom of a fade, a picture of one colour over the selected
	// card of E1, its frame drawn without it
	sc := liveScenes([]string{goFont})[0]
	b := sc.windows[sc.selected].Thumbnail.Bounds()
	magenta := image.NewRGBA(b)
	for i := 0; i < len(magenta.Pix); i += 4 {
		copy(magenta.Pix[i:], []byte{255, 0, 255, 255})
	}
	tex := share.upload(magenta)
	defer share.run(func() { gl.DeleteTextures(1, &tex) })
	item := CarouselLive(sc.windows, sc.selected, -1, sc.cfg)[sc.selected]
	item.Texture = tex
	if err := p.draw(Draw3DCarouselWithData(sc.windows, sc.selected, -1, 0, sc.cfg)); err != nil {
		t.Fatal(err)
	}
	zoom := Fade{Alpha: 1, Scale: 0.92}
	p.drawTexture(p.texture, zoom)
	p.SetLiveItems([]LiveItem{item})
	p.drawLiveItems(zoom)
	got := p.readBack()
	found := image.Rectangle{}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if c := got.RGBAAt(x, y); c.R == 255 && c.G == 0 && c.B == 255 {
				found = found.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	cx, cy := float64(width)/2, float64(height)/2
	r := item.Rect
	wantR := [4]float64{
		cx + (float64(r.Min.X)-cx)*zoom.Scale, cy + (float64(r.Min.Y)-cy)*zoom.Scale,
		cx + (float64(r.Max.X)-cx)*zoom.Scale, cy + (float64(r.Max.Y)-cy)*zoom.Scale,
	}
	gotR := [4]float64{float64(found.Min.X), float64(found.Min.Y), float64(found.Max.X), float64(found.Max.Y)}
	for i := range wantR {
		if math.Abs(gotR[i]-wantR[i]) > 1 {
			t.Errorf("zoomed: the live rectangle %v of %v lies at %v, want %.1f within a pixel", found, r, gotR, wantR)
			break
		}
	}
}

// TestLiveCovers checks the holes of the live rectangles against the drawing
// they stand for: every pixel a card of Σ's carousels draws at rest lies in
// its cover, with the selection frame, and with the hover frame when hovered;
// every pixel the icon and the labels of a tile draw within its live
// rectangle lies in a hole of it
func TestLiveCovers(t *testing.T) {
	goFont := filepath.Join(t.TempDir(), "goregular.ttf")
	if err := os.WriteFile(goFont, goregular.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	fonts := []string{goFont}
	inCover := func(covers []image.Rectangle, x, y int) bool {
		for _, c := range covers {
			if (image.Point{x, y}).In(c) {
				return true
			}
		}
		return false
	}

	var carousels []liveScene
	for _, sc := range scenes() {
		if sc.layout == "carousel" && !sc.host {
			carousels = append(carousels, liveScene{sc.name, sc.windows, sc.selected, sceneConfig(sc, fonts)})
		}
	}
	for _, sc := range liveScenes(fonts) {
		if sc.cfg.LayoutMode == "carousel" {
			carousels = append(carousels, sc)
		}
	}
	for _, sc := range carousels {
		canvas := image.Rect(0, 0, sc.cfg.Width, sc.cfg.Height)
		for j := range sc.windows {
			offset := j - sc.selected
			if offset < -5 || offset > 5 {
				continue
			}
			img := drawCard(sc.windows, j, offset, canvas, sc.cfg)
			covers := cardCover(sc.windows, j, sc.selected, -1, sc.cfg)
			if hover := CarouselHover(sc.windows, j, offset, sc.cfg); hover != nil && offset != 0 {
				// The hover frame is drawn over the card
				hovered := cardCover(sc.windows, j, sc.selected, j, sc.cfg)
				for y := hover.Rect.Min.Y; y < hover.Rect.Max.Y; y++ {
					for x := hover.Rect.Min.X; x < hover.Rect.Max.X; x++ {
						if hover.RGBAAt(x, y).A != 0 && !inCover(hovered, x, y) {
							t.Fatalf("%s, card %d hovered: pixel %d,%d outside its cover %v", sc.name, j, x, y, hovered)
						}
					}
				}
			}
			for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
				for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
					if img.RGBAAt(x, y).A != 0 && !inCover(covers, x, y) {
						t.Fatalf("%s, card %d at offset %d: pixel %d,%d outside its cover %v", sc.name, j, offset, x, y, covers)
					}
				}
			}
		}
	}

	for _, sc := range liveScenes(fonts) {
		if sc.cfg.LayoutMode != "grid" {
			continue
		}
		items := GridLive(sc.windows, sc.cfg)
		g := layoutGrid(len(sc.windows), headerBand(sc.cfg), sc.cfg)
		inside := 0
		for i := range sc.windows {
			win := sc.windows[i]
			dc := gg.NewContext(sc.cfg.Width, sc.cfg.Height)
			x, y := g.tile(i)
			drawGridTileTail(dc, &win, x, y, g.tileW, g.tileH, false, false, sc.cfg)
			if s, ok := gridIconScale(win.Icon.Bounds()); ok {
				dc.Push()
				dc.Translate(x, y)
				dc.Translate(gridIconPadding, gridIconPadding)
				dc.Scale(s, s)
				dc.DrawImage(win.Icon, 0, 0)
				dc.Pop()
			}
			img := dc.Image().(*image.RGBA)
			r := items[i].Rect
			for py := r.Min.Y; py < r.Max.Y; py++ {
				for px := r.Min.X; px < r.Max.X; px++ {
					if img.RGBAAt(px, py).A == 0 {
						continue
					}
					inside++
					if !inCover(items[i].Holes, px, py) {
						t.Fatalf("%s, tile %d: pixel %d,%d of its labels or icon in the live rectangle %v, outside its holes %v",
							sc.name, i, px, py, r, items[i].Holes)
					}
				}
			}
		}
		t.Logf("%s: %d pixels of labels and icons in the live rectangles, all in holes", sc.name, inside)
	}
}

// TestLiveFallback checks criterion K6 of specs/020-live-thumbnails: with the
// presenter's context made unable to share the snapshotter's, the presenter
// is created without live pictures, the log says so once at info level, and
// its frames are the cpu frames byte for byte, as in K2
func TestLiveFallback(t *testing.T) {
	saved := newGLXContext
	defer func() { newGLXContext = saved }()
	newGLXContext = func(*glx.Share) (*glx.Context, error) {
		return glx.NewContext(nil)
	}
	var out bytes.Buffer
	savedLogger := log.Logger
	defer func() { log.Logger = savedLogger }()
	log.Logger = zerolog.New(&out)

	conn, err := xgb.NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	if !compositing(conn) {
		t.Skip("no compositing manager: the pixels of an off-screen window are undefined")
	}
	_, presenter, err := NewBackend("glx", &glx.Share{})
	if err != nil {
		t.Fatal(err)
	}
	defer presenter.Close()
	p, ok := presenter.(*glxPresenter)
	if !ok {
		t.Skipf("no GLX: the presenter is %T", presenter)
	}
	if p.Live() {
		t.Error("live pictures on without a shared context")
	}
	if n := strings.Count(out.String(), "Live thumbnails off"); n != 1 || !strings.Contains(out.String(), `"level":"info"`) {
		t.Errorf("%d records of live thumbnails off, want 1 at info level: %s", n, out.String())
	}

	goFont := filepath.Join(t.TempDir(), "goregular.ttf")
	if err := os.WriteFile(goFont, goregular.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	for _, sc := range scenes()[:2] {
		w, err := NewWindowAt(conn, root, -sc.width-100, 0, sc.width, sc.height, p.VisualID())
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Show(); err != nil {
			t.Fatal(err)
		}
		if err := p.Bind(w); err != nil {
			t.Fatal(err)
		}
		frame := drawScene(sc, []string{goFont})
		if err := p.draw(frame); err != nil {
			t.Fatal(err)
		}
		if n := differentPixels(p.readBack(), frame); n != 0 {
			t.Errorf("%s: %d pixels differ from the cpu frame", sc.name, n)
		}
		w.Close()
	}
	if p.TakeLiveFence() != 0 {
		t.Error("a live fence without live pictures")
	}
}

// BenchmarkLiveFrame is the cost on the GPU of the live pictures of
// specs/020-live-thumbnails: a frame at rest of E1 — the carousel, seven
// cards in the frame, and the grid of 36 — presented to a window off the
// screen, without live pictures and with one over every thumbnail, the frames
// paced at 144 Hz, timed on the GPU by a timer query from the start of the
// frame to the end of its swap. It reports the p50 and p95 of each, and of
// their difference a live picture. b.N is the number of frames of each kind:
//
//	go test -run '^$' -bench LiveFrame -benchtime 600x ./pkg/carousel
func BenchmarkLiveFrame(b *testing.B) {
	share := newShareThread(b)
	conn, err := xgb.NewConn()
	if err != nil {
		b.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	if !compositing(conn) {
		b.Skip("no compositing manager")
	}
	level := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	defer zerolog.SetGlobalLevel(level)
	presenter, err := newLivePresenter(share.share)
	if err != nil {
		b.Skipf("no GLX: %v", err)
	}
	defer presenter.Close()
	p := presenter.(*glxPresenter)
	if !p.Live() {
		b.Skip("the presenter's context does not share")
	}
	goFont := filepath.Join(b.TempDir(), "goregular.ttf")
	if err := os.WriteFile(goFont, goregular.TTF, 0o644); err != nil {
		b.Fatal(err)
	}
	const width, height = 2520, 1400
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	w, err := NewWindowAt(conn, root, -width-100, 0, width, height, p.VisualID())
	if err != nil {
		b.Fatal(err)
	}
	defer w.Close()
	if err := w.Show(); err != nil {
		b.Fatal(err)
	}
	if err := p.Bind(w); err != nil {
		b.Fatal(err)
	}
	var queries [2]uint32
	gl.GenQueries(2, &queries[0])
	defer gl.DeleteQueries(2, &queries[0])

	scenes := liveScenes([]string{goFont})
	for _, sc := range []liveScene{scenes[0], scenes[2]} {
		var frame *image.RGBA
		var items []LiveItem
		if sc.cfg.LayoutMode == "grid" {
			frame, items = DrawGridLayout(sc.windows, sc.selected, -1, sc.cfg), GridLive(sc.windows, sc.cfg)
		} else {
			frame, items = Draw3DCarouselWithData(sc.windows, sc.selected, -1, 0, sc.cfg), CarouselLive(sc.windows, sc.selected, -1, sc.cfg)
		}
		var live []LiveItem
		var textures []uint32
		for i, it := range items {
			if it.Rect.Empty() {
				continue
			}
			b := sc.windows[i].Thumbnail.Bounds()
			it.Texture = share.upload(livePicture(b.Dx(), b.Dy(), int64(100+i)))
			textures = append(textures, it.Texture)
			live = append(live, it)
		}
		if err := p.Present(frame); err != nil {
			b.Fatal(err)
		}
		times := map[bool][]float64{}
		due := time.Now()
		for f := 0; f < 2*b.N; f++ {
			with := f%2 == 1
			for time.Now().Before(due) {
			}
			due = due.Add(time.Second / 144)
			gl.QueryCounter(queries[0], gl.TIMESTAMP)
			if with {
				p.SetLiveItems(live)
			}
			if _, err := p.Refresh(); err != nil {
				b.Fatal(err)
			}
			gl.QueryCounter(queries[1], gl.TIMESTAMP)
			var t0, t1 uint64
			gl.GetQueryObjectui64v(queries[0], gl.QUERY_RESULT, &t0)
			gl.GetQueryObjectui64v(queries[1], gl.QUERY_RESULT, &t1)
			times[with] = append(times[with], float64(t1-t0)/1e6)
			p.ReleaseFence(p.TakeLiveFence())
		}
		pct := func(v []float64, q int) float64 {
			v = append([]float64(nil), v...)
			sort.Float64s(v)
			return v[(len(v)*q+99)/100-1]
		}
		name := strings.SplitN(sc.name, "-", 2)[0]
		for _, q := range []int{50, 95} {
			without, with := pct(times[false], q), pct(times[true], q)
			b.ReportMetric(without, fmt.Sprintf("%s-without-p%d-ms", name, q))
			b.ReportMetric(with, fmt.Sprintf("%s-with-p%d-ms", name, q))
			b.ReportMetric(1000*(with-without)/float64(len(live)), fmt.Sprintf("%s-p%d-us-a-picture", name, q))
		}
		b.Logf("%s: %d live pictures", sc.name, len(live))
		share.run(func() { gl.DeleteTextures(int32(len(textures)), &textures[0]) })
	}
}
