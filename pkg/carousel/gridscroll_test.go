package carousel

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
)

// TestGridScrollLayout checks K2 of specs/036-grid-scroll: over 1 to 200
// windows, on the overlay of E1 and on one of a screen of 1920×1080 less the
// padding, with the header, the columns of GridColumns are ⌈√(1.5 n)⌉ but
// at most as many as fit at the least size — 6 and 5 —, and every thumbnail
// box is at least the least, 320 × 320·H/W; a grid laid out on one screen
// is the grid of a least of 0 when its boxes hold the least, and scrolls
// otherwise. With columns set, that many. With a least of 0, the columns
// and the grid as before. GridFollow brings the row of every tile in whole.
func TestGridScrollLayout(t *testing.T) {
	goFont := filepath.Join(t.TempDir(), "goregular.ttf")
	if err := os.WriteFile(goFont, goregular.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, screen := range []struct {
		w, h, fit int
	}{{2520, 1400, 6}, {1880, 1040, 5}} {
		cfg := sceneConfig(scene{layout: "grid", theme: "dark", width: screen.w, height: screen.h, hostname: "ws1", version: "v0.1.0"}, []string{goFont})
		cfg.GridScroll = 0
		least := float64(cfg.GridMinThumbWidth)
		aspect := float64(screen.h) / float64(screen.w)
		top := headerBand(cfg)
		none := cfg
		none.GridMinThumbWidth = 0
		for n := 1; n <= 200; n++ {
			name := fmt.Sprintf("%d×%d, %d windows", screen.w, screen.h, n)
			auto := max(2, int(math.Ceil(math.Sqrt(1.5*float64(n)))))
			if got, want := GridColumns(n, cfg), min(auto, screen.fit); got != want {
				t.Fatalf("%s: %d columns, want %d", name, got, want)
			}
			if got, want := GridColumns(n, none), min(auto, 6); got != want {
				t.Fatalf("%s, no least: %d columns, want %d", name, got, want)
			}
			g := layoutGrid(n, top, cfg)
			boxW, boxH := g.tileW-2*gridThumbPadding, g.tileH-gridTitleSpace
			if boxW < least || boxH < least*aspect-1e-9 {
				t.Fatalf("%s: thumbnail box %.1f×%.1f under the least", name, boxW, boxH)
			}
			old := layoutGrid(n, top, none)
			holds := old.tileW-2*gridThumbPadding >= least && old.tileH-gridTitleSpace >= least*aspect
			if old.cols == g.cols && holds != (g.maxScroll == 0) {
				t.Fatalf("%s: the boxes on one screen hold the least %v, the grid scrolls %v", name, holds, g.maxScroll > 0)
			}
			if g.maxScroll == 0 && g != old {
				t.Fatalf("%s: on one screen %+v, without the least %+v", name, g, old)
			}
			if g.maxScroll > 0 && math.Abs(boxH-boxW*aspect) > 1e-9 {
				t.Fatalf("%s: a box of %.1f×%.1f not in the proportions of the overlay", name, boxW, boxH)
			}
			checkFollow(t, name, n, cfg)
		}
		for _, cols := range []int{3, 8} {
			set := cfg
			set.GridColumns = cols
			for _, n := range []int{5, 47, 100} {
				if got := GridColumns(n, set); got != cols {
					t.Errorf("columns %d, %d windows: %d", cols, n, got)
				}
				g := layoutGrid(n, top, set)
				if w := (float64(screen.w) - g.spacing*float64(cols+1)) / float64(cols); math.Abs(g.tileW-math.Min(w, float64(cfg.ThumbWidth)+40)) > 1e-9 {
					t.Errorf("columns %d, %d windows: tiles %.1f wide, the columns make them %.1f", cols, n, g.tileW, w)
				}
				checkFollow(t, fmt.Sprintf("columns %d, %d windows", cols, n), n, set)
			}
		}
	}
}

// checkFollow checks GridFollow from the top to every tile and back: the
// row of the tile and the spacing around it in the view, the scroll within
// its ends and moved only when the row was not in whole
func checkFollow(t *testing.T, name string, n int, cfg Config) {
	t.Helper()
	view := GridViewOf(n, cfg)
	whole := func(i, scroll int) bool {
		c := cfg
		c.GridScroll = scroll
		_, y, _, h := GridTile(n, i, c)
		return y-gridSpacing(cfg) >= float64(view.Top)-1e-9 && y+h+gridSpacing(cfg) <= float64(cfg.Height)+1e-9
	}
	c := cfg
	c.GridScroll = 0
	for _, i := range append(seq(n), reverse(seq(n))...) {
		scroll := GridFollow(n, i, c)
		if scroll < 0 || scroll > view.MaxScroll {
			t.Fatalf("%s: tile %d at scroll %d, outside 0..%d", name, i, scroll, view.MaxScroll)
		}
		if !whole(i, scroll) && view.Row+gridSpacing(cfg) <= float64(cfg.Height-view.Top) {
			t.Fatalf("%s: tile %d not in whole at scroll %d", name, i, scroll)
		}
		if whole(i, c.GridScroll) && scroll != c.GridScroll {
			t.Fatalf("%s: tile %d in whole at %d, scrolled to %d", name, i, c.GridScroll, scroll)
		}
		c.GridScroll = scroll
	}
	if view.MaxScroll > 0 && (GridFollow(n, n-1, cfg) != view.MaxScroll || GridFollow(n, 0, c) != 0) {
		t.Fatalf("%s: the last tile at %d, the first at %d; want %d and 0", name, GridFollow(n, n-1, cfg), GridFollow(n, 0, c), view.MaxScroll)
	}
}

func seq(n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = i
	}
	return s
}

func reverse(s []int) []int {
	r := make([]int, len(s))
	for i, v := range s {
		r[len(s)-1-i] = v
	}
	return r
}
