package carousel

import (
	"image"
	"os"
	"sort"
	"testing"
	"time"
)

// BenchmarkLiveGeometry is the CPU of the live items of the frames at rest
// of E1 (specs/020-live-thumbnails): CarouselLive at each selection of 24
// windows, as each step of S2 asks it, and GridLive of 36, with the fonts of
// E1 where the host has them; each case fresh — the faces made anew, as at
// the start of qws — and again
//
//	go test -run '^$' -bench LiveGeometry -benchtime 1x ./pkg/carousel
func BenchmarkLiveGeometry(b *testing.B) {
	fonts := []string{fontSystem, fontFallback}
	for _, f := range fonts {
		if _, err := os.Stat(f); err != nil {
			b.Skipf("no %s", f)
		}
	}
	carousel := sceneConfig(scene{layout: "carousel", theme: "dark", width: 2520, height: 1400}, fonts)
	grid := sceneConfig(scene{layout: "grid", theme: "dark", width: 2520, height: 1400}, fonts)
	windows := portableWindows(24)
	tiles := liveWindows(36, image.Pt(512, 271), image.Pt(512, 160))
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	for range b.N {
		fontCacheMu.Lock()
		fontCache = map[fontCacheKey]*MultiFallbackFace{}
		fontCacheMu.Unlock()
		for _, name := range []string{"cold", "warm"} {
			var steps []float64
			for sel := range windows {
				start := time.Now()
				CarouselLive(windows, sel, -1, carousel)
				steps = append(steps, ms(time.Since(start)))
			}
			sort.Float64s(steps)
			start := time.Now()
			GridLive(tiles, grid)
			g := ms(time.Since(start))
			b.ReportMetric(steps[len(steps)/2], name+"-carousel-p50-ms")
			b.ReportMetric(steps[len(steps)-1], name+"-carousel-max-ms")
			b.ReportMetric(g, name+"-grid-ms")
		}
	}
}
