package carousel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
)

// Scene set Σ of specs/001-rendering-speed: fixed input data drawn by the cpu
// drawing code. The SHA-256 of every frame is recorded in testdata/scenes.json,
// and a frame that differs by one byte fails the test (criterion K1).
//
// Portable scenes use the Go font from golang.org/x/image and nothing from the
// host. Host scenes also need DejaVu Sans: placeholders hard-code it, and the
// fallback titles need glyphs the Go font lacks. They are skipped when the
// host's DejaVu Sans differs from the one the digests were recorded with.
//
//	go test ./pkg/carousel -run TestScenes -update
//
// rewrites the digests; that is only right when the picture is meant to change.
var updateScenes = flag.Bool("update", false, "rewrite testdata/scenes.json")

const scenesFile = "testdata/scenes.json"

// sceneDigests is the content of scenesFile
type sceneDigests struct {
	Fonts  map[string]string `json:"fonts"`  // host font file → SHA-256
	Scenes map[string]string `json:"scenes"` // scene name → SHA-256 of the frame
}

type scene struct {
	name     string
	host     bool // needs the host's DejaVu Sans
	layout   string
	theme    string
	width    int
	height   int
	windows  []WindowData
	selected int
	hover    int
}

func TestScenes(t *testing.T) {
	goFont := filepath.Join(t.TempDir(), "goregular.ttf")
	if err := os.WriteFile(goFont, goregular.TTF, 0o644); err != nil {
		t.Fatal(err)
	}
	hostFont := fileDigest(fontFallback)

	var golden sceneDigests
	if !*updateScenes {
		b, err := os.ReadFile(scenesFile)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &golden); err != nil {
			t.Fatal(err)
		}
	}
	got := sceneDigests{
		Fonts:  map[string]string{fontFallback: hostFont},
		Scenes: map[string]string{},
	}

	for _, sc := range scenes() {
		t.Run(sc.name, func(t *testing.T) {
			fonts := []string{goFont}
			if sc.host {
				if hostFont == "" {
					t.Skipf("no %s on this host", fontFallback)
				}
				if !*updateScenes && hostFont != golden.Fonts[fontFallback] {
					t.Skipf("%s differs from the one the digests were recorded with", fontFallback)
				}
				fonts = append(fonts, fontFallback)
			}

			digest := frameDigest(drawScene(sc, fonts))
			got.Scenes[sc.name] = digest
			if *updateScenes {
				return
			}
			want, ok := golden.Scenes[sc.name]
			if !ok {
				t.Fatalf("no recorded digest; run with -update")
			}
			if digest != want {
				t.Errorf("frame digest %s, recorded %s", digest, want)
			}
		})
	}

	if *updateScenes {
		b, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(scenesFile, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// drawScene draws a scene the way Selector.render does
func drawScene(sc scene, fonts []string) *image.RGBA {
	cfg := sceneConfig(sc, fonts)
	if sc.layout == "grid" {
		return DrawGridLayout(sc.windows, sc.selected, sc.hover, cfg)
	}
	return Draw3DCarouselWithData(sc.windows, sc.selected, sc.hover, 0, cfg)
}

// sceneConfig is the configuration Selector builds from the defaults of
// internal/config with the author's overrides on E1, spelled out so that a
// change of defaults does not change the digests
func sceneConfig(sc scene, fonts []string) Config {
	cfg := Config{
		Width:                   sc.width,
		Height:                  sc.height,
		ThumbWidth:              512,
		ThumbHeight:             512,
		Spacing:                 600,
		PerspectiveFactor:       0.6,
		ShadowOffset:            10,
		ShadowBlur:              15,
		FontPaths:               fonts,
		FontSize:                20,
		WindowBackgroundEnabled: true,
		WindowBackgroundOpacity: 0.85,
		WindowBackgroundRadius:  20,
		LayoutMode:              sc.layout,
		GridColumns:             0,
		GridSpacing:             20,
	}
	if sc.theme == "light" {
		cfg.BackgroundColor = "#f5f5f5"
		cfg.SelectionFrame = "#0078d4"
		cfg.TextColor = "#1a1a1a"
		cfg.ShadowColor = "rgba(0, 0, 0, 0.3)"
		cfg.InactiveFrame = "#cccccc"
		cfg.UrgentTitleBackground = "#e53935"
	} else {
		cfg.BackgroundColor = "#1a1a2e"
		cfg.SelectionFrame = "#4a9eff"
		cfg.TextColor = "#ffffff"
		cfg.ShadowColor = "rgba(0, 0, 0, 0.8)"
		cfg.InactiveFrame = "#404050"
		cfg.UrgentTitleBackground = "#d32f2f"
	}
	return cfg
}

func scenes() []scene {
	many := portableWindows(24)
	special := hostWindows()

	var list []scene
	add := func(name string, host bool, layout, theme string, w, h int, windows []WindowData, selected, hover int) {
		list = append(list, scene{name, host, layout, theme, w, h, windows, selected, hover})
	}
	for _, layout := range []string{"carousel", "grid"} {
		// The frame of E1: 2520×1400, 24 windows, Alt+Tab selects the second
		add(layout+"-e1-24-second", false, layout, "dark", 2520, 1400, many, 1, -1)
		add(layout+"-24-first", false, layout, "dark", 1260, 700, many, 0, -1)
		add(layout+"-24-middle-hover", false, layout, "dark", 1260, 700, many, 12, 14)
		add(layout+"-24-last", false, layout, "dark", 1260, 700, many, 23, -1)
		add(layout+"-24-middle-light", false, layout, "light", 1260, 700, many, 12, -1)
		add(layout+"-2", false, layout, "dark", 1260, 700, many[:2], 1, -1)
		add(layout+"-1", false, layout, "dark", 1260, 700, many[:1], 0, -1)
		add(layout+"-host", true, layout, "dark", 1260, 700, special, 2, 4)
		add(layout+"-host-light", true, layout, "light", 1260, 700, special, 3, -1)
	}
	return list
}

// portableWindows gives n windows drawn with the Go font alone: thumbnails of
// the sizes a capture gives, square, non-square, NRGBA and missing icons,
// Latin and Cyrillic titles, a title long enough to truncate, an urgent window
func portableWindows(n int) []WindowData {
	sizes := [][2]int{{512, 288}, {512, 384}, {288, 512}, {512, 512}, {512, 320}}
	titles := []string{
		"Terminal — ~/wsp/pet/qws",
		"Документация — Firefox",
		"A very long window title that is certainly longer than the truncation limit",
		"qws — Visual Studio Code",
		"",
		"Почта",
	}
	windows := make([]WindowData, n)
	for i := range windows {
		size := sizes[i%len(sizes)]
		w := WindowData{
			Thumbnail: thumbnail(i, size[0], size[1]),
			Title:     titles[i%len(titles)],
			Workspace: fmt.Sprintf("%d", i%5+1),
			Urgent:    i == 3,
		}
		switch i % 4 {
		case 0:
			w.Icon = iconRGBA(48, 48)
		case 1:
			w.Icon = iconNRGBA(64, 64)
		case 2:
			w.Icon = iconRGBA(64, 32)
		}
		windows[i] = w
	}
	return windows
}

// hostWindows gives windows that need the host's DejaVu Sans: placeholders,
// titles with glyphs only DejaVu has, a CJK title no font here has
func hostWindows() []WindowData {
	return []WindowData{
		{Thumbnail: thumbnail(0, 512, 288), Icon: iconRGBA(48, 48), Title: "★ Избранное ✓", Workspace: "1"},
		{Thumbnail: DrawPlaceholder(256, 256, "Placeholder window"), Icon: iconNRGBA(64, 64), Title: "Placeholder window", Workspace: "2"},
		{Thumbnail: thumbnail(1, 512, 384), Title: "⌘ Настройки", Workspace: "web", Urgent: true},
		{Thumbnail: DrawPlaceholder(256, 256, "Окно без эскиза с длинным заголовком"), Title: "Окно без эскиза с длинным заголовком", Workspace: "3"},
		{Thumbnail: thumbnail(2, 288, 512), Icon: iconRGBA(64, 32), Title: "日本語のタイトル", Workspace: "code"},
		{Thumbnail: thumbnail(3, 512, 512), Icon: iconRGBA(48, 48), Title: "Terminal", Workspace: "4"},
	}
}

// thumbnail is an opaque pattern of the given size, distinct for every seed,
// with gradients and edges for the resampling to work on
func thumbnail(seed, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			check := uint8(((x/16 + y/16) % 2) * 96)
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(x * 255 / w),
				G: uint8(y * 255 / h),
				B: uint8(seed*37) + check,
				A: 255,
			})
		}
	}
	return img
}

// iconRGBA is an icon as _NET_WM_ICON gives it: straight, not premultiplied
// values in an image.RGBA, with an opaque disc and a translucent ring
func iconRGBA(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	cx, cy, r := float64(w)/2, float64(h)/2, float64(min(w, h))/2
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			d := dx*dx + dy*dy
			switch {
			case d < (r*0.6)*(r*0.6):
				img.SetRGBA(x, y, color.RGBA{230, 120, 40, 255})
			case d < r*r:
				img.SetRGBA(x, y, color.RGBA{250, 250, 250, 128})
			}
		}
	}
	return img
}

// iconNRGBA is an icon as a decoded PNG gives it, with alpha falling off
// towards the edges
func iconNRGBA(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := uint8(255 - (x*x+y*y)*255/(w*w+h*h))
			img.SetNRGBA(x, y, color.NRGBA{40, 160, 220, a})
		}
	}
	return img
}

// frameDigest is the SHA-256 of a frame's geometry and pixels
func frameDigest(img *image.RGBA) string {
	h := sha256.New()
	fmt.Fprintf(h, "%v %d\n", img.Bounds(), img.Stride)
	h.Write(img.Pix)
	return hex.EncodeToString(h.Sum(nil))
}

// fileDigest is the SHA-256 of a file, or "" when it cannot be read
func fileDigest(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
