package ui

import (
	"bytes"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/almaz-uno/qws/pkg/keygrab"
	"github.com/almaz-uno/qws/pkg/snapshot"
	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/image/font/gofont/goregular"
)

// The keys of specs/026-layout-keys, at the level of the selector: key
// presses handled as handleEventsSync hands them over, on an X connection of
// the test that sends no request — the keyboard and modifier mappings the
// selector reads once an activation are set in advance.

// testKeysyms are the keys of the test keyboard, from keycode 8
var testKeysyms = []uint32{
	0xFF09, // Tab
	0xFF1B, // Escape
	0x0063, // c
	0x0067, // g
	0x0071, // q
	0xFF51, // Left
	0xFF53, // Right
	0xFF52, // Up
	0xFF54, // Down
	0xFFBF, // F2
}

// testKeycode is the keycode of a key of the test keyboard
func testKeycode(t *testing.T, keysym uint32) xproto.Keycode {
	for i, k := range testKeysyms {
		if k == keysym {
			return xproto.Keycode(8 + i)
		}
	}
	t.Fatalf("no key 0x%X on the test keyboard", keysym)
	return 0
}

// frameRecorder is a renderer that draws nothing and records what each
// frame shows
type frameRecorder struct {
	sync.Mutex
	frames []recordedFrame
}

type recordedFrame struct {
	layout, hint string
	selected     int
}

func (r *frameRecorder) record(layout string, selected int, cfg carousel.Config) *image.RGBA {
	r.Lock()
	defer r.Unlock()
	r.frames = append(r.frames, recordedFrame{layout, cfg.LayoutHint, selected})
	return image.NewRGBA(image.Rect(0, 0, cfg.Width, cfg.Height))
}

// last is the last frame drawn and the number drawn
func (r *frameRecorder) last() (recordedFrame, int) {
	r.Lock()
	defer r.Unlock()
	if len(r.frames) == 0 {
		return recordedFrame{}, 0
	}
	return r.frames[len(r.frames)-1], len(r.frames)
}

func (r *frameRecorder) Draw3DCarousel([]image.Image, int, float64, carousel.Config) *image.RGBA {
	panic("not drawn by the selector")
}

func (r *frameRecorder) Draw3DCarouselWithData(_ []carousel.WindowData, selected, _ int, _ float64, cfg carousel.Config) *image.RGBA {
	return r.record("carousel", selected, cfg)
}

func (r *frameRecorder) DrawGridLayout(_ []carousel.WindowData, selected, _ int, cfg carousel.Config) *image.RGBA {
	return r.record("grid", selected, cfg)
}

func (r *frameRecorder) DrawPlaceholder(w, h int, _ string) image.Image {
	return image.NewRGBA(image.Rect(0, 0, w, h))
}

// keySelector is a selector of n windows in the layout of appearance, with
// the layout key named key, on the test keyboard, its header shown when the
// appearance has it
func keySelector(t *testing.T, appearance config.Appearance, key string, n int) (*Selector, *frameRecorder) {
	goFont := filepath.Join(t.TempDir(), "goregular.ttf")
	if err := os.WriteFile(goFont, goregular.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	keysyms := make([]xproto.Keysym, len(testKeysyms))
	for i, k := range testKeysyms {
		keysyms[i] = xproto.Keysym(k)
	}
	setup := xproto.SetupInfo{MinKeycode: 8, MaxKeycode: xproto.Keycode(8 + len(testKeysyms) - 1)}
	rec := &frameRecorder{}
	s := &Selector{
		conn:       &xgb.Conn{SetupBytes: setup.Bytes()},
		appearance: appearance,
		config: carousel.Config{
			Width: 1260, Height: 700, ThumbWidth: 256, ThumbHeight: 256, Spacing: 300,
			PerspectiveFactor: 0.6, ShadowOffset: 10, FontPaths: []string{goFont}, FontSize: 14,
			GridSpacing: 20,
		},
		keyConfig: keyConfig{
			modifierMask:          uint16(keygrab.ModAlt),
			backwardMask:          uint16(keygrab.ModShift),
			workspaceModifierMask: uint16(keygrab.ModControl),
			mainKeysym:            0xFF09,
			cancelKeysym:          0xFF1B,
		},
		keymap:            &xproto.GetKeyboardMappingReply{KeysymsPerKeycode: 1, Keysyms: keysyms},
		modmap:            &xproto.GetModifierMappingReply{KeycodesPerModifier: 1, Keycodes: make([]xproto.Keycode, 8)},
		renderer:          rec,
		presenter:         livePresenter{&fakePresenter{}},
		hoverIndex:        -1,
		initialLayoutMode: appearance.Layout,
		period:            7 * time.Millisecond,
	}
	s.keyConfig.layoutToggleKeysym, s.keyConfig.layoutToggleName = parseLayoutToggle(key)
	s.setLayout(appearance.Layout)
	s.SetHeader("ws1", "v0.1.0")
	for i := 0; i < n; i++ {
		s.windows = append(s.windows, x11.WindowInfo{
			ID:      xproto.Window(10 + i),
			Name:    fmt.Sprintf("Window %d", i),
			Preview: image.NewRGBA(image.Rect(0, 0, 512, 288)),
		})
	}
	s.selectedIndex = 1
	return s, rec
}

// press hands the selector a press of the key keysym with the modifiers of
// state
func press(t *testing.T, s *Selector, keysym uint32, state uint16) {
	t.Helper()
	if s.handleKeyPressSimple(xproto.KeyPressEvent{Detail: testKeycode(t, keysym), State: state}, s.prepareThumbnails()) {
		t.Fatalf("key 0x%X cancelled the switcher", keysym)
	}
}

// TestLayoutKeys checks K1, K7 and K8 of specs/026-layout-keys: the layout
// key toggles the carousel and the grid, with Shift too, not with the
// workspace modifier; c and g as before; each switch drops the layers and
// draws the other layout with the hint of what the key does in it; the
// activation ends in appearance.layout again (D2). No toggle and no hint
// without the key, no hint without the header. Up and Down move the
// selection of the grid by a row within its column, around it, and do
// nothing in the carousel. The live thumbnails of a layout switched to are
// those of that layout (specs/020-live-thumbnails).
func TestLayoutKeys(t *testing.T) {
	const q, c, g, up, down = 0x0071, 0x0063, 0x0067, 0xFF52, 0xFF54
	shift, ctrl := uint16(keygrab.ModShift), uint16(keygrab.ModControl)
	appearance := config.Default().Appearance
	s, rec := keySelector(t, appearance, "q", 12)

	// shows checks the layout, its hint, and the frame drawn for them
	shows := func(how, layout, hint string, frames int) {
		t.Helper()
		f, n := rec.last()
		if s.config.LayoutMode != layout || s.config.LayoutHint != hint {
			t.Errorf("%s: layout %q, hint %q; want %q, %q", how, s.config.LayoutMode, s.config.LayoutHint, layout, hint)
		}
		if n != frames {
			t.Errorf("%s: %d frames drawn, want %d", how, n, frames)
		} else if n > 0 && (f.layout != layout || f.hint != hint) {
			t.Errorf("%s: the frame drawn %+v, want the layout %q with the hint %q", how, f, layout, hint)
		}
	}
	shows("at the start", "carousel", "Q — grid", 0)

	gen := s.layers.gen
	press(t, s, q, 0)
	shows("q in the carousel", "grid", "Q — carousel", 1)
	if s.layers.gen == gen {
		t.Error("the layers of the carousel kept in the grid")
	}
	press(t, s, q, shift)
	shows("Shift+q in the grid", "carousel", "Q — grid", 2)
	press(t, s, q, ctrl)
	shows("Ctrl+q", "carousel", "Q — grid", 2)
	press(t, s, c, 0)
	shows("c in the carousel", "carousel", "Q — grid", 2)
	press(t, s, g, 0)
	shows("g", "grid", "Q — carousel", 3)
	press(t, s, g, 0)
	shows("g in the grid", "grid", "Q — carousel", 3)
	press(t, s, c, 0)
	shows("c", "carousel", "Q — grid", 4)

	// D2: the next activation opens in appearance.layout
	press(t, s, q, 0)
	s.restoreInitialLayoutMode()
	if _, n := rec.last(); s.config.LayoutMode != "carousel" || s.config.LayoutHint != "Q — grid" || n != 5 {
		t.Errorf("the end of the activation: layout %q, hint %q, %d frames; want the carousel, its hint, 5",
			s.config.LayoutMode, s.config.LayoutHint, n)
	}
	grid := appearance
	grid.Layout = "grid"
	gs, grec := keySelector(t, grid, "q", 12)
	press(t, gs, q, 0)
	if gs.config.LayoutMode != "carousel" {
		t.Errorf("q in the grid of appearance.layout: %q", gs.config.LayoutMode)
	}
	gs.restoreInitialLayoutMode()
	if f, _ := grec.last(); gs.config.LayoutMode != "grid" || gs.config.LayoutHint != "Q — carousel" || f.layout != "carousel" {
		t.Errorf("the end of an activation of the grid: layout %q, hint %q", gs.config.LayoutMode, gs.config.LayoutHint)
	}

	// Up and Down: nothing in the carousel; in the grid of 12, five columns
	// on the frame of the test — 0..4, 5..9, 10..11 — a row within the column
	press(t, s, down, 0)
	press(t, s, up, 0)
	if _, n := rec.last(); n != 5 || s.selectedIndex != 1 {
		t.Errorf("Up and Down in the carousel: %d frames, selection %d; want none, 1", n-5, s.selectedIndex)
	}
	press(t, s, q, 0)
	if cols := carousel.GridColumns(12, s.config); cols != 5 {
		t.Fatalf("%d columns, want 5", cols)
	}
	for _, step := range []struct {
		key  uint32
		want int
	}{{down, 6}, {down, 11}, {down, 1}, {up, 11}, {up, 6}, {up, 1}, {up, 11}} {
		before := s.selectedIndex
		press(t, s, step.key, 0)
		if f, _ := rec.last(); s.selectedIndex != step.want || f.selected != step.want {
			t.Errorf("0x%X from %d: selection %d, the frame's %d; want %d", step.key, before, s.selectedIndex, f.selected, step.want)
		}
	}

	// No key, no toggle and no hint; no header, no hint
	none, nrec := keySelector(t, appearance, "", 12)
	press(t, none, q, 0)
	if _, n := nrec.last(); none.config.LayoutMode != "carousel" || none.config.LayoutHint != "" || n != 0 {
		t.Errorf("without the key: layout %q, hint %q, %d frames", none.config.LayoutMode, none.config.LayoutHint, n)
	}
	headless := appearance
	headless.Header.Enabled = false
	hs, _ := keySelector(t, headless, "q", 12)
	for _, layout := range []string{"grid", "carousel"} {
		press(t, hs, q, 0)
		if hs.config.LayoutMode != layout || hs.config.LayoutHint != "" || hs.config.Hostname != "" {
			t.Errorf("without the header: layout %q, hint %q", hs.config.LayoutMode, hs.config.LayoutHint)
		}
	}

	// The live thumbnails after a toggle: the snapshotter is told every tile
	// of the grid, the frame draws the pictures where the grid has the
	// thumbnails; back in the carousel, the cards in view and theirs
	ls, _ := keySelector(t, appearance, "q", 12)
	src := &fakeSource{pics: map[xproto.Window]snapshot.Picture{}}
	p := &fakePresenter{}
	ls.live = liveThumbnails{snap: src, presenter: p, atom: 77, drawn: map[xproto.Window]uint64{}}
	for i := range ls.windows {
		src.pics[ls.windows[i].ID] = snapshot.Picture{Texture: uint32(100 + i), Width: 512, Height: 288, Gen: 1, Changed: time.Now()}
	}
	live := func(layout string, items []carousel.LiveItem) {
		t.Helper()
		var want []carousel.LiveItem
		for i, it := range items {
			if !it.Rect.Empty() {
				it.Texture = uint32(100 + i)
				want = append(want, it)
			}
		}
		if len(want) == 0 || !reflect.DeepEqual(p.items, want) {
			t.Errorf("%s: live items %d, want the %d of the layout", layout, len(p.items), len(want))
		}
	}
	press(t, ls, q, 0)
	if len(src.shown) != len(ls.windows) {
		t.Errorf("the grid: %d tiles in view for the snapshotter, want %d", len(src.shown), len(ls.windows))
	}
	live("the grid", carousel.GridLive(ls.prepareWindowData(), ls.config))
	press(t, ls, q, 0)
	if len(src.shown) == 0 || len(src.shown) == len(ls.windows) {
		t.Errorf("the carousel: %d cards in view for the snapshotter, of %d windows", len(src.shown), len(ls.windows))
	}
	live("the carousel", carousel.CarouselLive(ls.prepareWindowData(), ls.selectedIndex, ls.hoverIndex, ls.config))
}

// TestLayoutToggleKey checks K2 of specs/026-layout-keys: the name of
// keybindings.layout_toggle as keygrab reads it, the hint of each layout
// with a single letter upper-cased and any other name as written; an empty
// name is no key and no warning, an unknown one or one of the switcher's
// keys no key and a warning
func TestLayoutToggleKey(t *testing.T) {
	var buf bytes.Buffer
	saved := log.Logger
	log.Logger = zerolog.New(&buf)
	defer func() { log.Logger = saved }()

	for _, c := range []struct {
		name           string
		keysym         uint32
		carousel, grid string
		warns          bool
	}{
		{"q", 0x0071, "Q — grid", "Q — carousel", false},
		{" Q ", 0x0071, "Q — grid", "Q — carousel", false},
		{"F2", 0xFFBF, "F2 — grid", "F2 — carousel", false},
		{"space", 0x0020, "space — grid", "space — carousel", false},
		{"", 0, "", "", false},
		{"nokey", 0, "", "", true},
	} {
		buf.Reset()
		keysym, name := parseLayoutToggle(c.name)
		hints := [2]string{layoutHint(name, "carousel", true), layoutHint(name, "grid", true)}
		if keysym != c.keysym || hints != [2]string{c.carousel, c.grid} {
			t.Errorf("%q: keysym 0x%X, hints %q; want 0x%X, %q, %q", c.name, keysym, hints, c.keysym, c.carousel, c.grid)
		}
		if h := layoutHint(name, "carousel", false); h != "" {
			t.Errorf("%q: the hint %q without the header", c.name, h)
		}
		lines := strings.Count(buf.String(), "\n")
		if warned := lines == 1 && strings.Contains(buf.String(), `"level":"warn"`) &&
			strings.Contains(buf.String(), `"layout_toggle":"`+c.name+`"`); warned != c.warns || !c.warns && lines != 0 {
			t.Errorf("%q: logged %q, a warning naming the key wanted %v", c.name, buf.String(), c.warns)
		}
	}

	// A key of the switcher — here the cancel key q — keeps its meaning: no
	// layout key and no hint, and a warning
	buf.Reset()
	keysym, name := parseLayoutToggle("q", 0xFF09, 0x0071)
	if keysym != 0 || name != "" || layoutHint(name, "carousel", true) != "" {
		t.Errorf("q taken: keysym 0x%X, name %q; want no key", keysym, name)
	}
	if strings.Count(buf.String(), "\n") != 1 || !strings.Contains(buf.String(), `"level":"warn"`) ||
		!strings.Contains(buf.String(), `"layout_toggle":"q"`) {
		t.Errorf("q taken: logged %q, a warning naming the key wanted", buf.String())
	}
}

// TestColumnStep checks K7 of specs/026-layout-keys: for 1 to 40 tiles, in
// the columns the grid lays them out in — appearance.grid.columns 0, or 1 to
// 6 — Down from the top of a column passes its tiles in order, row after
// row, and from the last comes back to the top; Up passes them in reverse.
// So every tile is reached, Up after Down and Down after Up return where
// they started, and a column of one tile does not move; the tile reached is
// in the column of the grid as it is drawn, a row below or above.
func TestColumnStep(t *testing.T) {
	cfg := carousel.Config{Width: 2520, Height: 1400, ThumbWidth: 512, ThumbHeight: 512, GridSpacing: 20}
	for n := 1; n <= 40; n++ {
		for fixed := 0; fixed <= 6; fixed++ {
			cfg.GridColumns = fixed
			cols := carousel.GridColumns(n, cfg)
			name := fmt.Sprintf("%d tiles, appearance.grid.columns %d: %d columns", n, fixed, cols)
			reached := make([]bool, n)
			for c := 0; c < min(cols, n); c++ {
				var column []int
				for i := c; i < n; i += cols {
					column = append(column, i)
				}
				down, up := c, c
				for k := range column {
					if down != column[k] {
						t.Fatalf("%s: Down %d times from %d at %d, want %d", name, k, c, down, column[k])
					}
					if want := column[(len(column)-k)%len(column)]; up != want {
						t.Fatalf("%s: Up %d times from %d at %d, want %d", name, k, c, up, want)
					}
					reached[down] = true
					down, up = columnStep(down, n, cols, 1), columnStep(up, n, cols, -1)
				}
				if down != c || up != c {
					t.Fatalf("%s: around the column of %d at %d down, %d up", name, c, down, up)
				}
			}
			for i := 0; i < n; i++ {
				if !reached[i] {
					t.Fatalf("%s: tile %d not reached", name, i)
				}
				d, u := columnStep(i, n, cols, 1), columnStep(i, n, cols, -1)
				if columnStep(d, n, cols, -1) != i || columnStep(u, n, cols, 1) != i {
					t.Fatalf("%s: from %d Down then Up at %d, Up then Down at %d", name, i, columnStep(d, n, cols, -1), columnStep(u, n, cols, 1))
				}
				x, y, _, h := carousel.GridTile(n, i, cfg)
				dx, dy, _, _ := carousel.GridTile(n, d, cfg)
				if dx != x || d > i && math.Abs(dy-(y+h+cfg.GridSpacing)) > 1e-9 {
					t.Fatalf("%s: Down from %d at (%v, %v) to %d at (%v, %v)", name, i, x, y, d, dx, dy)
				}
			}
		}
	}
}

// fakeAnimator is a presenter that composes layers and draws nothing
type fakeAnimator struct{ livePresenter }

func (fakeAnimator) SetLayer(carousel.LayerID, *image.RGBA) error { return nil }
func (fakeAnimator) HasLayer(carousel.LayerID) bool               { return true }
func (fakeAnimator) DropLayers()                                  {}
func (fakeAnimator) PresentScene(carousel.LayerID, []carousel.SceneItem, carousel.Fade) error {
	return nil
}
func (fakeAnimator) PresentFaded(*image.RGBA, carousel.Fade) error  { return nil }
func (fakeAnimator) StageFrame(*image.RGBA, int) (int, bool, error) { return 0, true, nil }
func (fakeAnimator) PresentStaged(carousel.Fade) error              { return nil }

// TestRowSlide checks K7 of specs/026-layout-keys: on a presenter that
// composes layers, with the layers of the grid held, Up and Down to the tile
// above or below slide the selection frame there, as Right to the next tile;
// around a column of three tiles or more the selection is there at once, as
// from the last tile to the first
func TestRowSlide(t *testing.T) {
	const left, right, up, down = 0xFF51, 0xFF53, 0xFF52, 0xFF54
	grid := config.Default().Appearance
	grid.Layout = "grid"
	s, _ := keySelector(t, grid, "q", 12) // 0..4, 5..9, 10..11
	a := fakeAnimator{livePresenter{&fakePresenter{}}}
	s.presenter, s.animator = a, a
	s.anim = animationOptions{step: 150 * time.Millisecond}
	s.step.pos.d, s.step.gx.d, s.step.gy.d = s.anim.step, s.anim.step, s.anim.step
	s.dropLayers()
	s.layers.base = true
	for k, key := range gridLayers {
		s.layers.cards[key] = cardLayer{id: carousel.LayerID(2 + k)}
	}
	for _, c := range []struct {
		from int
		key  uint32
		to   int
		ease bool
	}{
		{1, down, 6, true},
		{6, up, 1, true},
		{6, down, 11, true},
		{11, down, 1, false}, // around a column of three
		{1, up, 11, false},
		{4, down, 9, true},
		{9, down, 4, true}, // around a column of two: the tile above
		{3, right, 4, true},
		{4, right, 5, true},
		{11, right, 0, false}, // around the list
		{0, left, 11, false},
	} {
		s.cancelStep()
		s.selectedIndex = c.from
		press(t, s, c.key, 0)
		x, y, _, _ := carousel.GridTile(len(s.windows), c.to, s.config)
		switch {
		case s.selectedIndex != c.to:
			t.Errorf("0x%X from %d: at %d, want %d", c.key, c.from, s.selectedIndex, c.to)
		case s.step.active != c.ease:
			t.Errorf("0x%X from %d to %d: the frame slides %v, want %v", c.key, c.from, c.to, s.step.active, c.ease)
		case c.ease && (s.step.gx.to != x || s.step.gy.to != y):
			t.Errorf("0x%X from %d to %d: the frame slides to (%v, %v), the tile is at (%v, %v)", c.key, c.from, c.to, s.step.gx.to, s.step.gy.to, x, y)
		}
	}
	s.cancelStep()
}
