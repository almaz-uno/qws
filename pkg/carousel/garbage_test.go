package carousel

import "testing"

// TestScenesOnGarbage checks K1 of specs/030-drawing-memory for the canvases
// reused: every canvas taken comes as one given back, of bytes no drawing
// wrote, and every scene of Σ still gives the digest TestScenes recorded —
// every drawing writes each byte of its canvas before it reads one
func TestScenesOnGarbage(t *testing.T) {
	if *updateScenes {
		t.Skip("the digests are recorded by TestScenes")
	}
	poisonCanvases.Store(true)
	defer poisonCanvases.Store(false)
	TestScenes(t)
}

// TestLayersOnGarbage is TestLayers with every canvas taken of bytes no
// drawing wrote: the layers, drawn on them, compose to the frames of cpu
// (specs/030-drawing-memory, K1)
func TestLayersOnGarbage(t *testing.T) {
	poisonCanvases.Store(true)
	defer poisonCanvases.Store(false)
	TestLayers(t)
}
