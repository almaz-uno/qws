package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// The switch of specs/028-grid-locate at the level of the selector: the
// selector of keys_test.go on a presenter that composes layers, draws
// nothing and records what it presents; the layers drawn in the background
// as the switcher draws them.

// sceneAnimator is a presenter that composes layers, draws nothing and
// records the frames it presents
type sceneAnimator struct {
	livePresenter
	shown []presented
}

// presented is a frame presented: a scene of layers over a base, or a frame
// of the CPU — given, the last or the one staged
type presented struct {
	scene bool
	base  carousel.LayerID
	items []carousel.SceneItem
	fade  carousel.Fade
}

func (a *sceneAnimator) Present(*image.RGBA) error {
	a.shown = append(a.shown, presented{fade: carousel.Opaque})
	return nil
}
func (a *sceneAnimator) SetLayer(carousel.LayerID, *image.RGBA) error { return nil }
func (a *sceneAnimator) HasLayer(carousel.LayerID) bool               { return true }
func (a *sceneAnimator) DropLayers()                                  {}
func (a *sceneAnimator) PresentScene(base carousel.LayerID, items []carousel.SceneItem, f carousel.Fade) error {
	a.shown = append(a.shown, presented{scene: true, base: base, items: items, fade: f})
	return nil
}
func (a *sceneAnimator) PresentFaded(_ *image.RGBA, f carousel.Fade) error {
	a.shown = append(a.shown, presented{fade: f})
	return nil
}
func (a *sceneAnimator) StageFrame(*image.RGBA, int) (int, bool, error) { return 0, true, nil }
func (a *sceneAnimator) PresentStaged(f carousel.Fade) error {
	a.shown = append(a.shown, presented{fade: f})
	return nil
}

// animatedSelector is the selector of keySelector on a sceneAnimator, with
// the animations of the appearance, at the start of an activation in its
// layout: its first frame presented, the layers it asks for drawn and held
func animatedSelector(t *testing.T, appearance config.Appearance, n int) (*Selector, *frameRecorder, *sceneAnimator) {
	t.Helper()
	s, rec := keySelector(t, appearance, "q", n)
	a := &sceneAnimator{livePresenter: livePresenter{&fakePresenter{}}}
	s.presenter, s.animator = a, a
	anim, _ := parseAnimation(appearance.Animation)
	s.anim = anim
	s.step.pos.d, s.step.gx.d, s.step.gy.d = anim.step, anim.step, anim.step
	s.fade.level.d = anim.duration
	s.dropLayers()
	s.render(s.prepareThumbnails())
	s.prefetch()
	loopIdle(s, func() bool { return false })
	return s, rec, a
}

// TestSwitchFromLayers checks K4 of specs/028-grid-locate (D3, D7): while
// the carousel is shown the grid's layers are drawn in the background and
// held, with a base of the grid's own; a switch presents the grid from them
// at once — no frame of the CPU before it — and its frame at rest follows;
// the layers of both stay, and the switch back is the same; a step of the
// grid draws the carousel's layers at its new selection. With the animations
// off the same (D7). Without the layers — just dropped, as the workspace
// filter drops both layouts' — the frame of the CPU as before.
func TestSwitchFromLayers(t *testing.T) {
	const q, right = 0x0071, 0xFF53
	for _, enabled := range []bool{true, false} {
		appearance := config.Default().Appearance
		appearance.Animation.Enabled = strconv.FormatBool(enabled)
		s, rec, a := animatedSelector(t, appearance, 12)
		name := map[bool]string{true: "animations on", false: "animations off"}[enabled]

		if !s.gridReady() || !s.carouselReady() {
			t.Fatalf("%s: the carousel shown, its layers held %v, the grid's %v; want both", name, s.carouselReady(), s.gridReady())
		}
		gen := s.layers.gen
		bases := map[string]carousel.LayerID{
			"carousel": s.layers.cards[baseKey("carousel")].id,
			"grid":     s.layers.cards[baseKey("grid")].id,
		}
		if bases["carousel"] == bases["grid"] {
			t.Fatalf("%s: one base for both layouts", name)
		}

		// switches presents the layout switched to by q: a scene over its
		// base at once — in the grid with the animations on, the first of
		// the convergence —, then its frame at rest drawn in the background
		switches := func(layout string) {
			t.Helper()
			a.shown = nil
			press(t, s, q, 0)
			if len(a.shown) != 1 || !a.shown[0].scene || a.shown[0].base != bases[layout] {
				t.Fatalf("%s: q to the %s presented %+v; want a scene over its base %d at once", name, layout, a.shown, bases[layout])
			}
			loopIdle(s, func() bool { return false })
			f, _ := rec.last()
			n := len(a.shown)
			if a.shown[n-1].scene || f.layout != layout || f.selected != s.selectedIndex {
				t.Errorf("%s: after the scene of the %s %d frames, the last drawn %+v; want its frame at rest", name, layout, n, f)
			}
			for i, p := range a.shown[:n-1] {
				if !p.scene || p.base != bases[layout] {
					t.Errorf("%s: frame %d of the %s not a scene of it, %+v", name, i, layout, p)
				}
			}
			if converges := layout == "grid" && enabled; converges != (n > 2) {
				t.Errorf("%s: %d frames of the %s; want 2 but for the convergence", name, n, layout)
			}
			if s.layers.gen != gen || !s.gridReady() || !s.carouselReady() {
				t.Errorf("%s: the %s shown, the layers dropped or not held", name, layout)
			}
		}
		switches("grid")
		press(t, s, right, 0)
		loopIdle(s, func() bool { return false })
		if s.selectedIndex != 2 || !s.carouselReady() {
			t.Errorf("%s: a step of the grid to %d, the carousel's layers at it held %v", name, s.selectedIndex, s.carouselReady())
		}
		switches("carousel")

		s.dropLayers()
		if s.gridReady() || s.carouselReady() {
			t.Errorf("%s: layers held after they are dropped", name)
		}
		a.shown = nil
		press(t, s, q, 0)
		if f, _ := rec.last(); len(a.shown) != 1 || a.shown[0].scene || f.layout != "grid" {
			t.Errorf("%s: q without the layers presented %+v, the last frame drawn %+v; want the grid's frame", name, a.shown, f)
		}
		loopIdle(s, func() bool { return false })
	}
}

// pressKey hands the selector a press of the key keysym as the loop does:
// the cause of the frames it brings marked first
func pressKey(t *testing.T, s *Selector, keysym uint32) {
	t.Helper()
	s.markFrameCause(causeKey)
	press(t, s, keysym, 0)
}

// TestLocateScene checks K1 of specs/028-grid-locate (D1): after a switch to
// the grid the shadow of the selected tile and the selection frame are in
// the scene at locate_zoom about the tile's centre and transparent, then
// shrink onto the tile and become opaque by locate_duration, both monotone,
// along the ease-out of the hover's levels; nothing else of the scene scaled
// or faded; at the end the scene of the grid at rest, item for item. The
// first frame of the switch is the first of the convergence.
func TestLocateScene(t *testing.T) {
	const q = 0x0071
	s, _, a := animatedSelector(t, config.Default().Appearance, 12)
	pressKey(t, s, q)
	if !s.locate.active {
		t.Fatal("q from the carousel: no convergence")
	}
	start, d, zoom := s.locate.level.start, s.anim.locate, s.anim.locateZoom
	if d != 400*time.Millisecond || zoom != 1.6 {
		t.Fatalf("the defaults %v, %v; want 400ms, 1.6", d, zoom)
	}
	converging := map[carousel.LayerID]bool{
		s.layers.cards[cardKey{index: gridShadow}].id:    true,
		s.layers.cards[cardKey{index: gridSelection}].id: true,
	}
	x, y, w, h := carousel.GridTile(len(s.windows), s.selectedIndex, s.config)
	cx, cy := x+w/2, y+h/2

	// atRest is the scene of the grid at rest at now
	atRest := func(now time.Time) []carousel.SceneItem {
		saved := s.locate
		s.locate.active = false
		defer func() { s.locate = saved }()
		return s.gridItems(now)
	}
	// The level of a hover frame coming over the same time
	var hover motion
	hover.d = d
	hover.rest(0)
	hover.moveTo(start, 1)

	check := func(name string, items, rest []carousel.SceneItem, alpha, scale float64) {
		t.Helper()
		if len(items) != len(rest) {
			t.Fatalf("%s: %d items, at rest %d", name, len(items), len(rest))
		}
		n := 0
		for i, it := range items {
			r := rest[i]
			if !converging[it.A] {
				if it != r {
					t.Errorf("%s: item %d %+v, at rest %+v", name, i, it, r)
				}
				continue
			}
			n++
			want := r
			want.Alpha = alpha
			want.RectA = zoomRect(r.RectA, cx, cy, scale)
			const eps = 1e-9
			if math.Abs(it.Alpha-want.Alpha) > eps || math.Abs(it.RectA.X-want.RectA.X) > eps || math.Abs(it.RectA.Y-want.RectA.Y) > eps ||
				math.Abs(it.RectA.W-want.RectA.W) > eps || math.Abs(it.RectA.H-want.RectA.H) > eps {
				t.Errorf("%s: item %d %+v, want %+v", name, i, it, want)
			}
		}
		if n != 2 {
			t.Errorf("%s: %d items converge, want the shadow and the frame", name, n)
		}
	}

	first := a.shown[len(a.shown)-1]
	check("the first frame", first.items, atRest(start), 0, zoom)
	if items := s.gridItems(start); items[0].Alpha != 0 || items[0].RectA.W != zoomRect(atRest(start)[0].RectA, cx, cy, zoom).W {
		t.Errorf("at the start: %+v, want alpha 0 at the scale %v", items[0], zoom)
	}
	prevAlpha, prevScale := -1.0, math.Inf(1)
	for k := 0; k < 40; k++ {
		now := start.Add(d * time.Duration(k) / 40)
		v := hover.at(now)
		items, rest := s.gridItems(now), atRest(now)
		check(fmt.Sprintf("at %v", now.Sub(start)), items, rest, v, zoom+(1-zoom)*v)
		if v < prevAlpha || zoom+(1-zoom)*v > prevScale {
			t.Errorf("at %v: alpha %v after %v, scale %v after %v; want both monotone", now.Sub(start), v, prevAlpha, zoom+(1-zoom)*v, prevScale)
		}
		prevAlpha, prevScale = v, zoom+(1-zoom)*v
	}
	for _, after := range []time.Duration{d, d + time.Millisecond} {
		if items, rest := s.gridItems(start.Add(after)), atRest(start.Add(after)); !reflect.DeepEqual(items, rest) {
			t.Errorf("at %v: %+v, want the scene at rest %+v", after, items, rest)
		}
	}

	// The frames of the loop: scenes of the convergence, then the frame at
	// rest, and nothing moves
	loopIdle(s, func() bool { return false })
	if s.locate.active || s.moving() || a.shown[len(a.shown)-1].scene {
		t.Errorf("after the loop: converging %v, the last frame a scene %v", s.locate.active, a.shown[len(a.shown)-1].scene)
	}
	alpha := -1.0
	for _, p := range a.shown[:len(a.shown)-1] {
		for _, it := range p.items {
			if it.A == s.layers.cards[cardKey{index: gridSelection}].id {
				if it.Alpha < alpha {
					t.Errorf("the selection frame at alpha %v after %v", it.Alpha, alpha)
				}
				alpha = it.Alpha
			}
		}
	}
	if alpha < 0.9 {
		t.Errorf("the last scene of the convergence at alpha %v, want near 1 before the frame at rest", alpha)
	}
}

// TestLocateWhen checks K2 of specs/028-grid-locate (D2, D5, D4): the
// convergence after the layout key and after g from the carousel; not at the
// opening of an activation in the grid, nor after a switch to the carousel,
// nor under cpu, with the animations off or at locate_duration 0s. Without
// the grid's layers at the key, the grid as before, and the convergence when
// they come within locate_duration of the key, else none.
func TestLocateWhen(t *testing.T) {
	const q, c, g = 0x0071, 0x0063, 0x0067
	appearance := config.Default().Appearance
	s, _, _ := animatedSelector(t, appearance, 12)
	for _, key := range []uint32{q, g} {
		pressKey(t, s, key)
		if !s.locate.active || !s.grid() {
			t.Errorf("0x%X from the carousel: converging %v in the %s", key, s.locate.active, s.config.LayoutMode)
		}
		pressKey(t, s, c)
		loopIdle(s, func() bool { return false })
		if s.locate.active || s.locate.key != (time.Time{}) || s.grid() {
			t.Errorf("c: converging %v, waiting %v in the %s", s.locate.active, s.locate.key, s.config.LayoutMode)
		}
	}
	pressKey(t, s, q)
	loopIdle(s, func() bool { return false })
	pressKey(t, s, q)
	if s.locate.active || s.grid() {
		t.Errorf("q to the carousel: converging %v in the %s", s.locate.active, s.config.LayoutMode)
	}
	loopIdle(s, func() bool { return false })

	grid := appearance
	grid.Layout = "grid"
	if gs, _, _ := animatedSelector(t, grid, 12); gs.locate.active || !gs.locate.key.IsZero() {
		t.Error("an activation opened in the grid converges")
	}

	cpu, _ := keySelector(t, appearance, "q", 12)
	cpu.anim, _ = parseAnimation(appearance.Animation)
	pressKey(t, cpu, q)
	if cpu.locate.active || !cpu.locate.key.IsZero() || !cpu.grid() {
		t.Errorf("cpu: converging %v, waiting %v", cpu.locate.active, cpu.locate.key)
	}
	for name, off := range map[string]func(*config.Animation){
		"the animations off": func(a *config.Animation) { a.Enabled = "false" },
		"duration 0":         func(a *config.Animation) { a.Duration = 0 },
		"locate_duration 0s": func(a *config.Animation) { a.LocateDuration = 0 },
	} {
		ap := appearance
		off(&ap.Animation)
		s, _, a := animatedSelector(t, ap, 12)
		pressKey(t, s, q)
		if s.locate.active || !s.grid() || len(a.shown) == 0 || !a.shown[len(a.shown)-1].scene {
			t.Errorf("%s: converging %v; want the grid from its layers at rest", name, s.locate.active)
		}
		loopIdle(s, func() bool { return false })
	}

	// Without the layers: the frame of the CPU, then the convergence when
	// they come; or none, if they come later than locate_duration after the
	// key
	for _, late := range []bool{false, true} {
		s, rec, a := animatedSelector(t, appearance, 12)
		if !late {
			// Time enough for the layers, under -race too
			s.anim.locate = 2 * time.Second
		}
		s.dropLayers()
		pressKey(t, s, q)
		if f, _ := rec.last(); s.locate.active || s.locate.key != s.timing.start || f.layout != "grid" || a.shown[len(a.shown)-1].scene {
			t.Fatalf("q without the layers: converging %v, waiting since %v, the frame drawn %+v; want the grid's frame, waiting", s.locate.active, s.locate.key, f)
		}
		if late {
			s.locate.key = s.locate.key.Add(-s.anim.locate)
		}
		loopIdle(s, func() bool { return s.locate.active })
		if s.locate.active == late {
			t.Errorf("the layers held, late %v: converging %v", late, s.locate.active)
		}
		if late {
			continue
		}
		if !s.gridReady() || s.locate.cause != s.timing.start {
			t.Errorf("converging before the layers are held, or from %v, not the key", s.locate.cause)
		}
		loopIdle(s, func() bool { return false })
		if s.locate.active || a.shown[len(a.shown)-1].scene {
			t.Error("the convergence without its frame at rest at its end")
		}
	}
}

// TestLocateKeys checks K3 of specs/028-grid-locate (D6): a step during the
// convergence — an arrow, the main key — ends it at once, and the selection
// frame slides from the tile; a switch back to the carousel ends it; the
// hover of another tile does not; no frame at rest for the live thumbnails
// while it runs (specs/020-live-thumbnails), as for the other animations
func TestLocateKeys(t *testing.T) {
	const q, tab, right, left, down = 0x0071, 0xFF09, 0xFF53, 0xFF51, 0xFF54
	s, _, _ := animatedSelector(t, config.Default().Appearance, 12)
	for _, key := range []uint32{right, left, tab, down} {
		pressKey(t, s, q)
		if !s.locate.active {
			t.Fatal("q: no convergence")
		}
		from := s.selectedIndex
		x, y, _, _ := carousel.GridTile(len(s.windows), from, s.config)
		pressKey(t, s, key)
		if s.locate.active || !s.step.active || s.step.gx.from != x || s.step.gy.from != y || s.selectedIndex == from {
			t.Errorf("0x%X: converging %v, the step from (%v, %v) to %d; want it ended, a step from the tile %d at (%v, %v)",
				key, s.locate.active, s.step.gx.from, s.step.gy.from, s.selectedIndex, from, x, y)
		}
		loopIdle(s, func() bool { return false })
		pressKey(t, s, q)
		loopIdle(s, func() bool { return false })
	}

	pressKey(t, s, q)
	s.hoverIndex = (s.selectedIndex + 3) % len(s.windows)
	s.hoverChanged(s.prepareThumbnails())
	if !s.locate.active || !s.hover.active {
		t.Errorf("the hover of another tile: converging %v, the hover moving %v; want both", s.locate.active, s.hover.active)
	}
	s.live.wanted = true
	if s.liveDue() {
		t.Error("a frame at rest for the live thumbnails while the selection frame converges")
	}
	s.live.wanted = false // the live thumbnails are off here: no frame would take it
	pressKey(t, s, q)
	if s.locate.active || s.grid() {
		t.Errorf("q back: converging %v in the %s", s.locate.active, s.config.LayoutMode)
	}
	loopIdle(s, func() bool { return false })
	s.live.wanted = true
	if !s.liveDue() {
		t.Error("no frame at rest for the live thumbnails once nothing moves")
	}
	s.live.wanted = false
}

// TestLocateRecords checks the records of L and F of specs/028-grid-locate:
// "Switching layout" says the layers were held; the first frame of the grid
// is a record "Frame" of cause key, and the first "Animation frame" of kind
// locate, with the time from the key; every frame of the convergence one of
// that kind, with its interval, the last at rest
func TestLocateRecords(t *testing.T) {
	const q = 0x0071
	s, _, _ := animatedSelector(t, config.Default().Appearance, 12)
	var buf bytes.Buffer
	level, logger := zerolog.GlobalLevel(), log.Logger
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	log.Logger = zerolog.New(&buf).Level(zerolog.DebugLevel)
	defer func() { zerolog.SetGlobalLevel(level); log.Logger = logger }()

	pressKey(t, s, q)
	loopIdle(s, func() bool { return false })
	log.Logger = logger

	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		records = append(records, r)
	}
	if len(records) < 3 || records[0]["message"] != "Switching layout" || records[0]["layers"] != true {
		t.Fatalf("records %v; want Switching layout with the layers held first", records)
	}
	frame, first := records[1], records[2]
	if frame["message"] != "Frame" || frame["cause"] != causeKey || frame["layout"] != "grid" || frame["total_ms"] == nil {
		t.Errorf("the first frame of the grid %v; want a Frame of cause key with total_ms", frame)
	}
	if first["message"] != "Animation frame" || first["kind"] != "locate" || first["response_ms"] == nil || first["interval_ms"] != nil {
		t.Errorf("the first frame of the convergence %v; want of kind locate with response_ms", first)
	}
	var locate []map[string]any
	for _, r := range records {
		if r["message"] == "Animation frame" && r["kind"] == "locate" {
			locate = append(locate, r)
		}
	}
	for i, r := range locate[1:] {
		if r["interval_ms"] == nil || r["animation"] != first["animation"] {
			t.Errorf("frame %d of the convergence %v; want its interval, of the animation %v", i+1, r, first["animation"])
		}
	}
	if last := locate[len(locate)-1]; len(locate) < 3 || last["at_rest"] != true || last["progress"] != 1.0 {
		t.Errorf("%d frames of the convergence, the last %v; want it at rest", len(locate), last)
	}
}
