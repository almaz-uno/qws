# Thumbnail Scaling Algorithms

QWS supports three scaling algorithms for thumbnail generation, configurable via `config.yaml`:

## Configuration

```yaml
appearance:
  thumbnail:
    width: 256
    height: 256
    scaling_algorithm: bilinear  # nearest, bilinear, or catmull-rom
```

## Algorithms

### 1. **nearest** (Nearest-Neighbor)

**Speed:** ⚡⚡⚡⚡⚡ Fastest  
**Quality:** ⭐ Lowest  
**Use case:** Maximum performance, retro aesthetic

- Simple integer math, minimal CPU usage
- Produces pixelated/blocky appearance
- Best for very frequent captures or low-end hardware
- No anti-aliasing

**Performance:** ~1-2ms per thumbnail (512×512 → 256×256)

### 2. **bilinear** (Bilinear Interpolation) — **Default**

**Speed:** ⚡⚡⚡⚡ Fast  
**Quality:** ⭐⭐⭐ Good  
**Use case:** Balanced quality/performance

- 2×2 pixel sampling with linear interpolation
- Smooth gradients, minimal artifacts
- Excellent balance for real-time window switching
- Suitable for most users

**Performance:** ~3-5ms per thumbnail (512×512 → 256×256)

### 3. **catmull-rom** (Catmull-Rom / Bicubic)

**Speed:** ⚡⚡ Slower  
**Quality:** ⭐⭐⭐⭐⭐ Highest  
**Use case:** Professional appearance, slow capture intervals

- 4×4 pixel sampling with cubic interpolation
- Sharpest details, smoothest edges
- Professional photo-quality results
- May cause slight lag on slower systems

**Performance:** ~15-50ms per thumbnail (512×512 → 256×256)

## Visual Comparison

```
Original → Scaled Down

Nearest-neighbor:          Bilinear:             Catmull-Rom:
████████                   ████████              ████████
████░░██                   ████▓▒██              ████▓▓▒██
████░░██                   ███▓▒▒▒██             ███▓▓▒▒▒██
████████                   ████████              ████████
(sharp edges)              (smooth)              (very smooth)
```

## Recommendations

| Scenario | Algorithm | Reason |
|----------|-----------|--------|
| **Default use** | `bilinear` | Best balance for most users |
| **Slow CPU / many windows** | `nearest` | Minimal performance impact |
| **High snapshot_interval (>5s)** | `catmull-rom` | Quality over speed |
| **Visual presentation** | `catmull-rom` | Professional appearance |
| **Battery-powered devices** | `nearest` or `bilinear` | Power efficiency |

## Implementation Details

The scaling is performed in [pkg/composite/capture.go](../pkg/composite/capture.go):

- `nearest`: Manual pixel-by-pixel copy
- `bilinear`: `golang.org/x/image/draw.BiLinear.Scale()`
- `catmull-rom`: `golang.org/x/image/draw.CatmullRom.Scale()`

All algorithms preserve aspect ratio and scale only when necessary (source larger than max dimensions).
