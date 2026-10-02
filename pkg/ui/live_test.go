package ui

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/almaz-uno/qws/pkg/snapshot"
	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/jezek/xgb/xproto"
	"golang.org/x/image/font/gofont/goregular"
)

// fakeSource is a snapshotter of the live thumbnails: the pictures it gives,
// what it is told
type fakeSource struct {
	pics            map[xproto.Window]snapshot.Picture
	shown           []xproto.Window
	begun, inFrame  bool
	fence, previous uintptr
	overlay         xproto.Window
	interval        time.Duration
}

func (f *fakeSource) SetLive(overlay xproto.Window, interval time.Duration) {
	f.overlay, f.interval = overlay, interval
}

func (f *fakeSource) BeginFrame(shown []xproto.Window) map[xproto.Window]snapshot.Picture {
	f.begun, f.inFrame, f.shown = true, true, shown
	return f.pics
}

func (f *fakeSource) EndFrame(fence uintptr) uintptr {
	f.inFrame = false
	if fence == 0 {
		return 0
	}
	old := f.fence
	f.fence = fence
	f.previous = old
	return old
}

// fakePresenter draws live items as the GLX presenter takes them: the items
// set, a fence for a frame that drew some
type fakePresenter struct {
	items    []carousel.LiveItem
	next     uintptr // the fence of the next frame that draws
	fence    uintptr
	released []uintptr
}

func (p *fakePresenter) Live() bool { return true }

func (p *fakePresenter) SetLiveItems(items []carousel.LiveItem) { p.items = items }

func (p *fakePresenter) TakeLiveFence() uintptr {
	f := p.fence
	p.fence = 0
	return f
}

func (p *fakePresenter) ReleaseFence(f uintptr) {
	if f != 0 {
		p.released = append(p.released, f)
	}
}

// present is a frame presented with the items set
func (p *fakePresenter) present() {
	if len(p.items) > 0 {
		p.next++
		p.fence = p.next
	}
	p.items = nil
}

// livePresenter is a presenter that draws live items
type livePresenter struct{ *fakePresenter }

func (livePresenter) VisualID() xproto.Visualid   { return 0 }
func (livePresenter) Bind(*carousel.Window) error { return nil }
func (livePresenter) Present(*image.RGBA) error   { return nil }
func (livePresenter) Refresh() (bool, error)      { return true, nil }
func (livePresenter) Close()                      {}

// TestLiveFrames checks the live thumbnails of the selector
// (specs/020-live-thumbnails): a frame at rest takes the pictures, draws
// those of the size of their cards' thumbnails, gives the snapshotter the
// presenter's fence and releases the one it replaces, and the windows of the
// cards in view, the same slice while the selection stays; a frame's record
// has the lag of the latest pass it draws for the first time, and only then;
// the ClientMessage of the snapshotter asks for a frame at rest, which waits
// a refresh period after the frame before
func TestLiveFrames(t *testing.T) {
	goFont := filepath.Join(t.TempDir(), "goregular.ttf")
	if err := os.WriteFile(goFont, goregular.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	appearance := config.Default().Appearance
	s := &Selector{
		appearance: appearance,
		config: carousel.Config{
			Width: 1260, Height: 700, ThumbWidth: 256, ThumbHeight: 256, Spacing: 300,
			PerspectiveFactor: 0.6, ShadowOffset: 10, FontPaths: []string{goFont}, FontSize: 14,
			LayoutMode: "carousel",
		},
		period: 7 * time.Millisecond,
	}
	for i := 0; i < 7; i++ {
		s.windows = append(s.windows, x11.WindowInfo{
			ID:      xproto.Window(10 + i),
			Name:    "Terminal",
			Preview: image.NewRGBA(image.Rect(0, 0, 512, 271)),
		})
	}
	s.selectedIndex, s.hoverIndex = 2, -1
	src := &fakeSource{}
	p := &fakePresenter{}
	s.live = liveThumbnails{snap: src, presenter: p, atom: 77, drawn: map[xproto.Window]uint64{}}

	// The cards come into view, without pictures: those at offsets -2 to 3
	// reach into the frame of 1260, the one at 4 does not
	s.liveBegin()
	s.liveEnd(time.Now())
	shown := map[xproto.Window]bool{}
	for _, id := range src.shown {
		shown[id] = true
	}
	if len(shown) != 6 || shown[16] {
		t.Errorf("the windows in view %v, want 10 to 15", src.shown)
	}
	first := src.shown

	changed := time.Now().Add(-20 * time.Millisecond)
	src.pics = map[xproto.Window]snapshot.Picture{
		11: {Texture: 101, Width: 512, Height: 271, Gen: 3, Changed: changed},
		12: {Texture: 102, Width: 512, Height: 271, Gen: 5, Changed: changed.Add(5 * time.Millisecond)},
		13: {Texture: 103, Width: 400, Height: 271, Gen: 4, Changed: changed}, // of another size
	}
	frame := func() time.Time {
		s.liveBegin()
		s.liveRest()
		textures := map[uint32]bool{}
		for _, it := range p.items {
			textures[it.Texture] = true
		}
		if !textures[101] || !textures[102] || textures[103] || len(p.items) != 2 {
			t.Errorf("drawn %v, want 101 and 102, not the picture of another size", textures)
		}
		p.present()
		end := time.Now()
		s.liveEnd(end)
		if src.inFrame {
			t.Error("the frame not ended for the snapshotter")
		}
		return end
	}

	end := frame()
	if len(src.shown) == 0 || &src.shown[0] != &first[0] {
		t.Error("the windows in view given anew while the selection stays")
	}
	if src.fence != 1 {
		t.Errorf("the snapshotter has fence %d, want the presenter's 1", src.fence)
	}
	if lag := s.takeLag(); lag != end.Sub(changed.Add(5*time.Millisecond)) {
		t.Errorf("lag %v, want %v: the latest pass drawn for the first time", lag, end.Sub(changed.Add(5*time.Millisecond)))
	}
	frame()
	if len(p.released) != 1 || p.released[0] != 1 {
		t.Errorf("released %v, want the first fence once replaced", p.released)
	}
	if lag := s.takeLag(); lag != 0 {
		t.Errorf("lag %v for pictures drawn before", lag)
	}

	// A new pass of one window
	src.pics[11] = snapshot.Picture{Texture: 101, Width: 512, Height: 271, Gen: 6, Changed: time.Now()}
	frame()
	if lag := s.takeLag(); lag <= 0 || lag > time.Second {
		t.Errorf("lag %v of a new pass", lag)
	}

	// The grid: every tile in view
	s.config.LayoutMode = "grid"
	s.liveBegin()
	s.liveEnd(time.Now())
	if len(src.shown) != len(s.windows) {
		t.Errorf("%d tiles in view, want all %d", len(src.shown), len(s.windows))
	}
	s.config.LayoutMode = "carousel"

	// The wake of the snapshotter
	if s.liveEvent(xproto.ClientMessageEvent{Type: 76}) {
		t.Error("another ClientMessage taken for the snapshotter's")
	}
	if !s.liveEvent(xproto.ClientMessageEvent{Type: 77}) || !s.liveDue() {
		t.Error("the snapshotter's ClientMessage asks for no frame")
	}
	s.step.active = true
	if s.liveDue() {
		t.Error("a frame at rest asked for while a step moves")
	}
	s.step.active = false
	s.live.lastFrame = time.Now()
	s.liveIdle()
	if !s.liveDue() {
		t.Error("a frame at rest presented sooner than a refresh period after the one before")
	}
}

// TestLiveOff checks D4 and D7 of specs/020-live-thumbnails: the live
// thumbnails are off with the key off, under cpu and without the snapshotter
func TestLiveOff(t *testing.T) {
	for name, c := range map[string]struct {
		live     bool
		renderer string
		snap     bool
	}{
		"key off":     {false, "glx", true},
		"cpu":         {true, "cpu", true},
		"no snapshot": {true, "glx", false},
	} {
		appearance := config.Default().Appearance
		appearance.Thumbnail.Live, appearance.Renderer = c.live, c.renderer
		s := &Selector{appearance: appearance}
		var snap *snapshot.Snapshotter
		if c.snap {
			snap = &snapshot.Snapshotter{}
		}
		s.initLive(snap, livePresenter{&fakePresenter{}})
		if s.liveOn() {
			t.Errorf("%s: live thumbnails on", name)
		}
	}
}
