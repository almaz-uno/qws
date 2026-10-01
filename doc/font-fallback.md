# Font Fallback Implementation

## Overview

`qws` supports multiple fallback fonts at the level of glyphs (characters).

## Architecture

### MultiFallbackFace

The new type `MultiFallbackFace` in [pkg/carousel/fontfallback.go](pkg/carousel/fontfallback.go) implements the `font.Face` interface of `golang.org/x/image/font` and provides:

1. **Glyph-level fallback** — a font is chosen automatically for every character
2. **Multiple fallbacks** — a chain of several fonts is supported
3. **Pure Go** — works without CGo, using only `golang.org/x/image` and `golang/freetype`

### How it works

```
Text: "Hello 世界 🎨"
  ↓
'H' → look in the primary font → found → use the primary
'世' → look in the primary font → not found → look in fallback #1 → found
'🎨' → primary → fallback #1 → fallback #2 → use the last one
```

### Font metrics

The metrics (line height, baseline) are taken from the **primary font**, so that the text is displayed consistently.

Kerning is applied only if both characters come from the same font.

## Configuration

```yaml
appearance:
  font:
    paths:
      - "/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf"
      - "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
      - "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf"
    size: 14
```

### Command line

```bash
qws --appearance-font-paths=/path/to/font1.ttf,/path/to/font2.ttf
```

## Usage

The type is used automatically in [pkg/carousel/renderer.go](pkg/carousel/renderer.go) when text is rendered:

```go
fallbackFace := NewMultiFallbackFace(cfg.FontPaths, fontSize)
if fallbackFace == nil {
    // Skip rendering if no fonts available
    return
}
defer fallbackFace.Close()
dc.SetFontFace(fallbackFace)
dc.DrawString(text, x, y)
```

## Testing

The tests are in [pkg/carousel/fontfallback_test.go](pkg/carousel/fontfallback_test.go):

```bash
go test ./pkg/carousel/...
```

## Limitations

1. **Not fontconfig** — full paths to font files are required; system names such as "Noto Sans" are not supported (they could be added through `fc-match`)
2. **Performance** — several fonts are checked for every glyph, but this is fast for short texts
3. **Complex scripts** — no support for ligatures or complex shaping (Arabic, Devanagari, etc.)

## Future improvements

- [ ] Caching of glyph lookup results
- [ ] Support for system font names through `fc-match`
- [ ] Automatic choice of fonts for CJK/Emoji
- [ ] Metrics based on the font in use (not only the primary)
