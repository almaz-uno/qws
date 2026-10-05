package ui

import (
	"fmt"
	"testing"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/almaz-uno/qws/pkg/carousel"
)

// TestGridMouse checks K1 of specs/027-grid-mouse: in the grid the mouse
// finds the tile drawn under the pointer, with the header on and off, for
// any number of windows and of columns — every tile at its centre and a
// pixel inside its corners — and none in the gaps between the tiles nor
// above the first row
func TestGridMouse(t *testing.T) {
	for _, header := range []bool{true, false} {
		for _, n := range []int{1, 2, 5, 12, 24, 37} {
			for _, columns := range []int{0, 3} {
				name := fmt.Sprintf("header %v, %d windows, columns %d", header, n, columns)
				appearance := config.Default().Appearance
				appearance.Layout = "grid"
				appearance.Header.Enabled = header
				s, _ := keySelector(t, appearance, "", n)
				s.config.GridColumns = columns
				cols := carousel.GridColumns(n, s.config)
				rows := (n + cols - 1) / cols
				spacing := s.config.GridSpacing

				at := func(x, y float64) int { return s.getWindowIndexAtPosition(int(x), int(y)) }
				for i := 0; i < n; i++ {
					x, y, w, h := carousel.GridTile(n, i, s.config)
					for _, p := range [][2]float64{{x + w/2, y + h/2}, {x + 1, y + 1}, {x + w - 1, y + 1}, {x + 1, y + h - 1}, {x + w - 1, y + h - 1}} {
						if got := at(p[0], p[1]); got != i {
							t.Errorf("%s: tile %d at (%.0f, %.0f) of its rectangle %.0f,%.0f %.0f×%.0f: found %d", name, i, p[0], p[1], x, y, w, h, got)
						}
					}
					if i%cols != cols-1 && i+1 < n {
						if got := at(x+w+spacing/2, y+h/2); got != -1 {
							t.Errorf("%s: the gap right of tile %d found %d", name, i, got)
						}
					}
					if i/cols != rows-1 {
						if got := at(x+w/2, y+h+spacing/2); got != -1 {
							t.Errorf("%s: the gap below tile %d found %d", name, i, got)
						}
					}
					if i < cols {
						if got := at(x+w/2, y-spacing/2); got != -1 {
							t.Errorf("%s: above tile %d found %d", name, i, got)
						}
					}
				}
			}
		}
	}
}
