package ui

import (
	"fmt"
	"image"
	"testing"
	"time"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/jezek/xgb/xproto"
)

// The grid that scrolls of specs/036-grid-scroll at the level of the
// selector: the selector of keys_test.go on the presenter of locate_test.go,
// on the overlay of E1 with the cards of the author, the least thumbnail of
// 320 px.

// scrollSelector is a selector of n windows in the grid of E1 on a
// sceneAnimator, animated or still, at the start of an activation: its view
// following the selection, its first frame presented, its layers held
func scrollSelector(t *testing.T, n int, animated bool) (*Selector, *frameRecorder, *sceneAnimator) {
	t.Helper()
	appearance := config.Default().Appearance
	appearance.Layout = "grid"
	s, rec := keySelector(t, appearance, "q", n)
	s.config.Width, s.config.Height = 2520, 1400
	s.config.ThumbWidth, s.config.ThumbHeight = 512, 512
	s.config.GridMinThumbWidth = 320
	a := &sceneAnimator{livePresenter: livePresenter{&fakePresenter{}}}
	s.presenter, s.animator = a, a
	s.animated, _ = parseAnimation(appearance.Animation)
	s.setStill(!animated)
	s.dropLayers()
	s.config.GridScroll = 0
	s.followSelection()
	s.render(s.prepareThumbnails())
	s.prefetch()
	loopIdle(s, func() bool { return false })
	if !s.gridReady() {
		t.Fatal("the layers of the grid not held")
	}
	if carousel.GridViewOf(n, s.config).MaxScroll == 0 {
		t.Fatal("the grid does not scroll")
	}
	return s, rec, a
}

// sceneScrolls are the scrolls of the scenes presented, from the place of
// their tiles layer, and whether every item of each is cut to the view
func sceneScrolls(s *Selector, a *sceneAnimator) (scrolls []int, cut bool) {
	tiles := s.layers.cards[cardKey{index: gridTiles}]
	clip := carousel.GridViewOf(len(s.windows), s.config).Clip(s.config)
	cut = true
	for _, p := range a.shown {
		if !p.scene {
			continue
		}
		for _, it := range p.items {
			if it.Live == nil && it.Clip != clip {
				cut = false
			}
			if it.A == tiles.id {
				scrolls = append(scrolls, tiles.bounds.Min.Y-int(it.RectA.Y))
			}
		}
	}
	return scrolls, cut
}

// TestGridScrollSteps checks K3 of specs/036-grid-scroll (D4, D7): Down
// through a column of 47 windows and around it, then Up around it: each
// step scrolls the view the least that shows the row of the selection in
// whole — animated, the scenes of the step moving the tiles from the scroll
// before to the scroll after, monotone, cut to the view; still, at its
// target in the first frame; around the column at once, to the top and to
// the bottom. The frame at rest is drawn at the scroll.
func TestGridScrollSteps(t *testing.T) {
	const up, down = 0xFF52, 0xFF54
	for _, animated := range []bool{true, false} {
		name := map[bool]string{true: "animated", false: "still"}[animated]
		s, rec, a := scrollSelector(t, 47, animated)
		n := len(s.windows)
		cols := carousel.GridColumns(n, s.config)
		view := carousel.GridViewOf(n, s.config)
		if cols != 6 {
			t.Fatalf("%s: %d columns, want 6", name, cols)
		}
		moved := 0
		for k, key := range []uint32{down, down, down, down, down, down, down, down, up, up} {
			from, prev := s.selectedIndex, s.config.GridScroll
			dir := 1
			if key == up {
				dir = -1
			}
			target := columnStep(from, n, cols, dir)
			want := carousel.GridFollow(n, target, s.config)
			wrap := target-from != cols && from-target != cols
			a.shown = nil
			pressKey(t, s, key)
			step := fmt.Sprintf("%s, key %d from %d to %d", name, k, from, target)
			if s.selectedIndex != target || s.config.GridScroll != want {
				t.Fatalf("%s: at %d, scroll %d; want %d, %d", step, s.selectedIndex, s.config.GridScroll, target, want)
			}
			if wrap {
				if s.step.active || want != map[int]int{1: 0, -1: view.MaxScroll}[dir] {
					t.Errorf("%s: around the column: a step %v, scroll %d", step, s.step.active, want)
				}
			} else if !s.step.active || s.step.sy.from != float64(prev) || s.step.sy.to != float64(want) {
				t.Errorf("%s: the step %v moves the view from %v to %v; want %d to %d", step, s.step.active, s.step.sy.from, s.step.sy.to, prev, want)
			}
			if want != prev {
				moved++
			}
			loopIdle(s, func() bool { return false })
			scrolls, cut := sceneScrolls(s, a)
			if !cut {
				t.Errorf("%s: an item of a scene not cut to the view", step)
			}
			last := prev
			for i, v := range scrolls {
				if (v-last)*(want-prev) < 0 || v < min(prev, want) || v > max(prev, want) {
					t.Errorf("%s: scene %d at scroll %d after %d, from %d to %d", step, i, v, last, prev, want)
				}
				last = v
			}
			if !animated && len(scrolls) > 0 && scrolls[0] != want {
				t.Errorf("%s: still, the first scene at scroll %d, want %d", step, scrolls[0], want)
			}
			if animated && !wrap && want != prev && len(scrolls) < 3 {
				t.Errorf("%s: %d scenes of the scroll", step, len(scrolls))
			}
			if f, _ := rec.last(); f.selected != target || f.scroll != want {
				t.Errorf("%s: the frame at rest %+v, want the selection %d at scroll %d", step, f, target, want)
			}
			// The row of the selection in whole
			_, y, _, h := carousel.GridTile(n, target, s.config)
			if y < float64(view.Top) || y+h > float64(s.config.Height) {
				t.Errorf("%s: the selection at %v–%v, the view %d–%d", step, y, y+h, view.Top, s.config.Height)
			}
		}
		if moved < 4 {
			t.Errorf("%s: the view moved %d times", name, moved)
		}
	}
}

// TestGridWheel checks K3 of specs/036-grid-scroll (D5): the wheel scrolls
// the grid of 100 windows by a row, down to its end and up to its top and no
// further, the selection where it is; the hover then the tile under the
// pointer
func TestGridWheel(t *testing.T) {
	s, _, _ := scrollSelector(t, 100, true)
	n := len(s.windows)
	view := carousel.GridViewOf(n, s.config)
	row := int(view.Row + 0.5)
	sel := s.selectedIndex
	x, _, w, _ := carousel.GridTile(n, 0, s.config)
	px, py := int16(x+w/2), int16(view.Top+300)
	hovered := 0
	wheel := func(button xproto.Button) {
		s.markFrameCause(causeEvent)
		s.wheel(xproto.ButtonPressEvent{Detail: button, EventX: px, EventY: py}, s.prepareThumbnails())
		loopIdle(s, func() bool { return false })
	}
	for _, c := range []struct {
		button xproto.Button
		dir    int
		end    int
	}{{5, 1, view.MaxScroll}, {4, -1, 0}} {
		for k := 0; s.config.GridScroll != c.end; k++ {
			prev := s.config.GridScroll
			wheel(c.button)
			want := min(max(prev+c.dir*row, 0), view.MaxScroll)
			if s.config.GridScroll != want || s.selectedIndex != sel || k > 20 {
				t.Fatalf("button %d from %d: scroll %d, selection %d; want %d, %d", c.button, prev, s.config.GridScroll, s.selectedIndex, want, sel)
			}
			if hover := s.getWindowIndexAtPosition(int(px), int(py)); s.hoverIndex != hover {
				t.Errorf("button %d at scroll %d: hover %d, the tile under the pointer %d", c.button, s.config.GridScroll, s.hoverIndex, hover)
			} else if hover >= 0 {
				hovered++
			}
		}
		wheel(c.button)
		if s.config.GridScroll != c.end {
			t.Errorf("button %d past the end: scroll %d", c.button, s.config.GridScroll)
		}
	}
	// The pointer in the gap between two rows at some scrolls
	if hovered < 10 {
		t.Errorf("a tile under the pointer after %d turns of the wheel", hovered)
	}
}

// TestGridScrollMouse checks K3 of specs/036-grid-scroll (D5): on a
// scrolled grid the hover and the click find the tile drawn under the
// pointer — at its centre and at the edges of the view of a tile the view
// cuts —, none over the header, where the rows pass under it
func TestGridScrollMouse(t *testing.T) {
	s, _, _ := scrollSelector(t, 47, false)
	n := len(s.windows)
	view := carousel.GridViewOf(n, s.config)
	s.config.GridScroll = view.MaxScroll / 2
	cut := 0
	for i := 0; i < n; i++ {
		x, y, w, h := carousel.GridTile(n, i, s.config)
		top, bottom := max(y, float64(view.Top)), min(y+h, float64(s.config.Height))
		if top >= bottom {
			continue
		}
		if y < float64(view.Top) || y+h > float64(s.config.Height) {
			cut++
		}
		for _, p := range [][2]float64{{x + w/2, (top + bottom) / 2}, {x + 1, top + 1}, {x + w - 1, bottom - 1}} {
			if got := s.getWindowIndexAtPosition(int(p[0]), int(p[1])); got != i {
				t.Errorf("tile %d at (%.0f, %.0f): found %d", i, p[0], p[1], got)
			}
		}
		if y < float64(view.Top) {
			if got := s.getWindowIndexAtPosition(int(x+w/2), view.Top-1); got != -1 {
				t.Errorf("over the header above tile %d: found %d", i, got)
			}
		}
	}
	if cut == 0 {
		t.Error("no tile cut by the view")
	}
}

// TestGridScrollLive checks K3 of specs/036-grid-scroll (D8): the windows
// the snapshotter passes are those of the tiles in the view, whole or cut,
// and the live items lie in the view
func TestGridScrollLive(t *testing.T) {
	s, _, _ := scrollSelector(t, 47, false)
	n := len(s.windows)
	view := carousel.GridViewOf(n, s.config)
	data := s.prepareWindowData()
	for _, scroll := range []int{0, view.MaxScroll / 2, view.MaxScroll} {
		s.config.GridScroll = scroll
		shown := map[xproto.Window]bool{}
		for _, w := range s.liveShown(time.Now()) {
			shown[w] = true
		}
		in := 0
		for i, w := range s.windows {
			_, y, _, h := carousel.GridTile(n, i, s.config)
			want := y+h > float64(view.Top) && y < float64(s.config.Height)
			if shown[w.ID] != want {
				t.Errorf("scroll %d: tile %d at %.0f–%.0f passed %v, in the view %v", scroll, i, y, y+h, shown[w.ID], want)
			}
			if want {
				in++
			}
		}
		if in == 0 || in == n {
			t.Errorf("scroll %d: %d of %d tiles in the view", scroll, in, n)
		}
		viewRect := image.Rect(0, view.Top, s.config.Width, s.config.Height)
		for i, it := range s.liveGeometry(data, scroll) {
			if !it.Rect.Empty() && !it.Rect.In(viewRect) {
				t.Errorf("scroll %d: the live item of tile %d at %v, out of the view %v", scroll, i, it.Rect, viewRect)
			}
			for _, h := range it.Holes {
				if !h.In(it.Rect) {
					t.Errorf("scroll %d: a hole of tile %d at %v, out of its item %v", scroll, i, h, it.Rect)
				}
			}
		}
	}
}
