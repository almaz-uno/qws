package carousel

import (
	"testing"

	"golang.org/x/image/math/fixed"
)

func TestMultiFallbackFace_Creation(t *testing.T) {
	// Test with valid font paths
	fontPaths := []string{
		"/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf",
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
	}

	face := NewMultiFallbackFace(fontPaths, 14.0)
	if face == nil {
		t.Skip("Fonts not available on this system, skipping test")
	}
	defer face.Close()

	if len(face.faces) == 0 {
		t.Error("Expected at least one font face to be loaded")
	}

	if face.primary == nil {
		t.Error("Expected primary font to be set")
	}
}

func TestMultiFallbackFace_EmptyPaths(t *testing.T) {
	face := NewMultiFallbackFace([]string{}, 14.0)
	if face != nil {
		t.Error("Expected nil face for empty font paths")
	}
}

func TestMultiFallbackFace_InvalidPaths(t *testing.T) {
	fontPaths := []string{
		"/nonexistent/path/to/font.ttf",
		"/another/invalid/path.ttf",
	}

	face := NewMultiFallbackFace(fontPaths, 14.0)
	if face != nil {
		t.Error("Expected nil face for invalid font paths")
		face.Close()
	}
}

func TestMultiFallbackFace_Metrics(t *testing.T) {
	fontPaths := []string{
		"/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf",
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
	}

	face := NewMultiFallbackFace(fontPaths, 14.0)
	if face == nil {
		t.Skip("Fonts not available on this system, skipping test")
	}
	defer face.Close()

	metrics := face.Metrics()
	if metrics.Height == 0 {
		t.Error("Expected non-zero font height")
	}
}

func TestMultiFallbackFace_GlyphBounds(t *testing.T) {
	fontPaths := []string{
		"/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf",
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
	}

	face := NewMultiFallbackFace(fontPaths, 14.0)
	if face == nil {
		t.Skip("Fonts not available on this system, skipping test")
	}
	defer face.Close()

	// Test basic ASCII character
	bounds, advance, ok := face.GlyphBounds('A')
	if !ok {
		t.Error("Expected glyph bounds for ASCII character 'A'")
	}
	if advance == 0 {
		t.Error("Expected non-zero advance for character 'A'")
	}
	_ = bounds // Use bounds to avoid unused variable warning

	// Test Unicode character (should be in one of the fallback fonts)
	_, _, ok = face.GlyphBounds('中')
	// We don't assert ok here because the fonts might not have CJK characters
	// Just ensure it doesn't panic
}

// TestMultiFallbackFace_SpecificGlyph tests fallback for specific glyph ⇃ (U+21C3)
func TestMultiFallbackFace_SpecificGlyph(t *testing.T) {
	fontPaths := []string{
		"/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf",
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
	}

	face := NewMultiFallbackFace(fontPaths, 14.0)
	if face == nil {
		t.Skip("Fonts not available on this system, skipping test")
	}
	defer face.Close()

	// Test the specific character ⇃ (U+21C3)
	testRune := '⇃'
	t.Logf("Testing rune: %c (U+%04X)", testRune, testRune)

	// Check with GlyphBounds
	bounds, advance, ok := face.GlyphBounds(testRune)
	if !ok {
		t.Errorf("GlyphBounds returned ok=false for '⇃' (U+21C3)")
	}

	t.Logf("GlyphBounds: ok=%v, advance=%v", ok, advance)
	t.Logf("Bounds: Min(%v,%v) Max(%v,%v)",
		bounds.Min.X, bounds.Min.Y, bounds.Max.X, bounds.Max.Y)

	// Check that bounds are non-empty (actual glyph exists)
	if bounds.Min.X == bounds.Max.X && bounds.Min.Y == bounds.Max.Y {
		t.Error("Glyph bounds are empty (no glyph found)")
	}

	if advance == 0 {
		t.Error("Expected non-zero advance for '⇃'")
	}

	// Test with Glyph method
	dr, mask, _, glyphAdvance, glyphOk := face.Glyph(fixed.Point26_6{}, testRune)
	if !glyphOk {
		t.Error("Glyph() returned ok=false for '⇃' (U+21C3)")
	}

	t.Logf("Glyph: ok=%v, advance=%v", glyphOk, glyphAdvance)
	t.Logf("Mask bounds: %v", mask.Bounds())
	t.Logf("Draw rect: %v", dr)

	if mask.Bounds().Empty() {
		t.Error("Glyph mask is empty")
	}
}

