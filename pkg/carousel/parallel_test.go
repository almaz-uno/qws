package carousel

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
)

// TestParallelDrawing draws frames and card layers in parallel, each with a
// FontSet of its own, as the animation of specs/007-animation does — and the
// frames and the tiles layer of the grid, whose heads are drawn in parallel
// themselves (specs/019-grid-speed) — and compares them with the same drawn
// one after another; with -race it also checks that they share nothing
// unguarded
func TestParallelDrawing(t *testing.T) {
	goFont := filepath.Join(t.TempDir(), "goregular.ttf")
	if err := os.WriteFile(goFont, goregular.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	var sc scene
	for _, s := range scenes() {
		if s.name == "carousel-24-middle-hover" {
			sc = s
		}
	}
	cfg := sceneConfig(sc, []string{goFont})

	type job struct {
		frame  bool
		offset int
		grid   bool // the grid frame, or its tiles layer
	}
	jobs := []job{{frame: true}, {frame: true}}
	for o := -3; o <= 3; o++ {
		jobs = append(jobs, job{offset: o})
	}
	jobs = append(jobs, job{grid: true, frame: true}, job{grid: true, frame: true}, job{grid: true}, job{grid: true})
	run := func(j job, c Config) []byte {
		if j.grid && j.frame {
			return DrawGridLayout(sc.windows, sc.selected, sc.hover, c).Pix
		}
		if j.grid {
			return GridTiles(sc.windows, c).Pix
		}
		if j.frame {
			return Draw3DCarouselWithData(sc.windows, sc.selected, sc.hover, 0, c).Pix
		}
		if l := CardLayer(sc.windows, sc.selected+j.offset, j.offset, c); l != nil {
			return l.Pix
		}
		return nil
	}

	want := make([][]byte, len(jobs))
	for i, j := range jobs {
		want[i] = run(j, cfg)
	}
	got := make([][]byte, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Go(func() {
			c := cfg
			c.Fonts = NewFontSet()
			got[i] = run(j, c)
		})
	}
	wg.Wait()
	for i := range jobs {
		if !bytes.Equal(got[i], want[i]) {
			t.Errorf("job %+v: drawn in parallel, it differs", jobs[i])
		}
	}
}
