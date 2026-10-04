package ui

import (
	"image"
	"testing"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/almaz-uno/qws/pkg/carousel"
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
		appearance.Animation.Enabled = enabled
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
		// base at once, then its frame at rest drawn in the background
		switches := func(layout string) {
			t.Helper()
			a.shown = nil
			press(t, s, q, 0)
			if len(a.shown) != 1 || !a.shown[0].scene || a.shown[0].base != bases[layout] {
				t.Fatalf("%s: q to the %s presented %+v; want a scene over its base %d at once", name, layout, a.shown, bases[layout])
			}
			loopIdle(s, func() bool { return false })
			f, _ := rec.last()
			if len(a.shown) != 2 || a.shown[1].scene || f.layout != layout || f.selected != s.selectedIndex {
				t.Errorf("%s: after the scene of the %s %d frames, the last drawn %+v; want its frame at rest", name, layout, len(a.shown), f)
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
