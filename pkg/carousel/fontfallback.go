package carousel

import (
	"image"
	"os"
	"sync"

	"github.com/golang/freetype/truetype"
	"github.com/rs/zerolog/log"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

var (
	fontCacheMu sync.RWMutex
	fontCache   = make(map[fontCacheKey]*MultiFallbackFace)

	// Parsed fonts by path; a truetype.Font is not changed after parsing and
	// is shared by all faces made of it
	parsedMu sync.Mutex
	parsed   = make(map[string]*truetype.Font)
)

type fontCacheKey struct {
	paths string // joined font paths
	size  float64
}

// MultiFallbackFace implements font.Face with support for multiple fallback fonts.
// It tries each font in order until it finds one that has the requested glyph.
type MultiFallbackFace struct {
	faces   []font.Face
	fonts   []*truetype.Font // For checking glyph presence via Index()
	primary font.Face        // First face for metrics
}

// NewMultiFallbackFace creates a new multi-fallback font face from font file paths.
// The first font is used as primary (for metrics), subsequent fonts are fallbacks.
// Returns nil if no fonts could be loaded.
// Results are cached by font paths and size for reuse.
func NewMultiFallbackFace(fontPaths []string, size float64) *MultiFallbackFace {
	if len(fontPaths) == 0 {
		return nil
	}

	// Create cache key from paths and size
	key := fontCacheKey{
		paths: joinPaths(fontPaths),
		size:  size,
	}

	// Check cache first (read lock)
	fontCacheMu.RLock()
	if cached, ok := fontCache[key]; ok {
		fontCacheMu.RUnlock()
		return cached
	}
	fontCacheMu.RUnlock()

	// Not in cache, create new (write lock)
	fontCacheMu.Lock()
	defer fontCacheMu.Unlock()

	// Double-check after acquiring write lock
	if cached, ok := fontCache[key]; ok {
		return cached
	}

	result := newMultiFallbackFace(fontPaths, size)
	if result != nil {
		// Cache the result
		fontCache[key] = result
	}
	return result
}

// FontSet is a cache of faces for one drawing at a time. The faces of
// truetype are not safe for concurrent use — Glyph returns its mask in a
// buffer that the next call reuses — so drawings that run in parallel each
// take a set of their own (specs/007-animation); a drawing without one uses
// the shared cache of NewMultiFallbackFace.
type FontSet struct {
	faces map[fontCacheKey]*MultiFallbackFace
}

// NewFontSet is an empty set
func NewFontSet() *FontSet {
	return &FontSet{faces: make(map[fontCacheKey]*MultiFallbackFace)}
}

// face is the face of the set for the paths and size
func (s *FontSet) face(fontPaths []string, size float64) *MultiFallbackFace {
	key := fontCacheKey{paths: joinPaths(fontPaths), size: size}
	if f, ok := s.faces[key]; ok {
		return f
	}
	f := newMultiFallbackFace(fontPaths, size)
	if f != nil {
		s.faces[key] = f
	}
	return f
}

// newMultiFallbackFace makes the faces of a fallback chain, uncached
func newMultiFallbackFace(fontPaths []string, size float64) *MultiFallbackFace {
	if len(fontPaths) == 0 {
		return nil
	}

	var faces []font.Face
	var fonts []*truetype.Font
	for _, path := range fontPaths {
		face, ttFont, err := loadFontFaceFromPath(path, size)
		if err != nil {
			log.Warn().
				Err(err).
				Str("path", path).
				Float64("size", size).
				Msg("Failed to load font, skipping")
			continue
		}
		faces = append(faces, face)
		fonts = append(fonts, ttFont)
	}

	if len(faces) == 0 {
		log.Error().
			Strs("paths", fontPaths).
			Float64("size", size).
			Msg("No fonts could be loaded from provided paths")
		return nil
	}

	// Trace: the drawings of the animation make chains of their own, a few
	// hundred a session
	log.Trace().
		Int("loaded", len(faces)).
		Int("total", len(fontPaths)).
		Float64("size", size).
		Msg("Font fallback chain created")

	return &MultiFallbackFace{
		faces:   faces,
		fonts:   fonts,
		primary: faces[0],
	}
}

// joinPaths creates a cache key from font paths
func joinPaths(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	result := paths[0]
	for i := 1; i < len(paths); i++ {
		result += "|" + paths[i]
	}
	return result
}

// loadFontFaceFromPath loads a font face from a file path
func loadFontFaceFromPath(path string, size float64) (font.Face, *truetype.Font, error) {
	f, err := parseFont(path)
	if err != nil {
		return nil, nil, err
	}

	face := truetype.NewFace(f, &truetype.Options{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	return face, f, nil
}

// parseFont parses the font file at path once
func parseFont(path string) (*truetype.Font, error) {
	parsedMu.Lock()
	defer parsedMu.Unlock()
	if f, ok := parsed[path]; ok {
		return f, nil
	}
	fontBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := truetype.Parse(fontBytes)
	if err != nil {
		return nil, err
	}
	parsed[path] = f
	log.Debug().Str("path", path).Msg("Font loaded successfully")
	return f, nil
}

// Close closes all font faces
func (m *MultiFallbackFace) Close() error {
	// Note: cached faces should not be closed as they are reused
	// This method is kept for interface compatibility but does nothing
	return nil
}

// Glyph returns the glyph for the given rune, trying each font in order
func (m *MultiFallbackFace) Glyph(dot fixed.Point26_6, r rune) (
	dr image.Rectangle,
	mask image.Image,
	maskp image.Point,
	advance fixed.Int26_6,
	ok bool,
) {
	for i, face := range m.faces {
		// Check if glyph exists in this font using truetype.Font.Index()
		// Index returns 0 for missing glyphs
		if m.fonts[i].Index(r) == 0 {
			continue
		}

		// Glyph exists, get it
		dr, mask, maskp, advance, ok = face.Glyph(dot, r)
		if ok {
			return dr, mask, maskp, advance, ok
		}
	}

	// If no font has the glyph, return the last attempt
	// (which will be a tofu/missing glyph indicator)
	log.Debug().
		Str("rune", string(r)).
		Int32("codepoint", int32(r)).
		Msg("Glyph not found in any font, using fallback placeholder")
	return dr, mask, maskp, advance, false
}

// GlyphBounds returns the bounds for the given rune
func (m *MultiFallbackFace) GlyphBounds(r rune) (
	bounds fixed.Rectangle26_6,
	advance fixed.Int26_6,
	ok bool,
) {
	for i, face := range m.faces {
		// Check if glyph exists using Index()
		if m.fonts[i].Index(r) == 0 {
			continue
		}
		bounds, advance, ok = face.GlyphBounds(r)
		if ok {
			return bounds, advance, ok
		}
	}

	// If no font has the glyph, return the last attempt
	return bounds, advance, false
}

// GlyphAdvance returns the advance width for the given rune
func (m *MultiFallbackFace) GlyphAdvance(r rune) (advance fixed.Int26_6, ok bool) {
	for i, face := range m.faces {
		// Check if glyph exists using Index()
		if m.fonts[i].Index(r) == 0 {
			continue
		}

		advance, ok = face.GlyphAdvance(r)
		if ok {
			return advance, ok
		}
	}
	return 0, false
}

// Kern returns the kerning for a pair of runes.
// Kerning only applies if both runes are from the same font.
func (m *MultiFallbackFace) Kern(r0, r1 rune) fixed.Int26_6 {
	// Find which font has both glyphs
	for i, face := range m.faces {
		// Check if both glyphs exist using Index()
		if m.fonts[i].Index(r0) != 0 && m.fonts[i].Index(r1) != 0 {
			return face.Kern(r0, r1)
		}
	}

	// No kerning if runes are from different fonts
	return 0
}

// Metrics returns the metrics of the primary font
func (m *MultiFallbackFace) Metrics() font.Metrics {
	return m.primary.Metrics()
}
