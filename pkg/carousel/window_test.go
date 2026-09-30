package carousel

import (
	"bytes"
	"image"
	"image/color"
	"math/rand"
	"testing"
)

// TestToBGRA checks that reading an *image.RGBA from Pix gives the bytes the
// conversion through At gives, for both depths
func TestToBGRA(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(3, 5, 3+37, 5+11))
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			a := uint8(rng.Intn(256))
			img.SetRGBA(x, y, color.RGBA{uint8(rng.Intn(int(a) + 1)), uint8(rng.Intn(int(a) + 1)), uint8(rng.Intn(int(a) + 1)), a})
		}
	}
	// Hides the concrete type, so that toBGRA takes the path through At
	generic := struct{ image.Image }{img}

	for _, alpha := range []bool{true, false} {
		for _, rows := range [][2]int{{0, 11}, {4, 9}} {
			n := 4 * img.Rect.Dx() * (rows[1] - rows[0])
			fast, slow := make([]byte, n), make([]byte, n)
			toBGRA(fast, img, rows[0], rows[1], alpha)
			toBGRA(slow, generic, rows[0], rows[1], alpha)
			if !bytes.Equal(fast, slow) {
				t.Errorf("alpha %v, rows %v: Pix and At give different bytes", alpha, rows)
			}
		}
	}
}
