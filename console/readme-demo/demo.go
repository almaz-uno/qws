package main

import (
	"image"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/almaz-uno/qws/pkg/carousel"
	"golang.org/x/image/draw"
)

// The desktop: the size of the monitor the pictures are drawn for; they are
// scaled down 2× to be shown
const (
	screenW, screenH = 2560, 1440
	shrink           = 2
)

// framePeriod is the interval between two frames of a step in the WebP: a
// frame of 20 ms plays at 50 frames a second in browsers, which slow down
// shorter ones
const framePeriod = 20 * time.Millisecond

// frame is a frame of the animation and how long it is shown
type frame struct {
	img *image.RGBA
	ms  int
}

// demo draws the overlay of qws for the synthetic windows over the desktop
type demo struct {
	data   []carousel.WindowData
	desk   *image.RGBA
	cfg    carousel.Config // the carousel's; layout returns that of a layout
	padX   int
	padY   int
	key    string        // the layout key, as configured
	step   time.Duration // of a step of the selection
	header bool
}

// newDemo is the demo of the windows over the desktop, with the overlay
// configured as NewSelector of pkg/ui configures it from config.Default(),
// but for the size of the thumbnails, the spacing and the font size of the
// options; the dark theme
func newDemo(o options, wins []window, desk *image.RGBA) *demo {
	def := config.Default()
	a := def.Appearance
	padX := config.ParsePadding(a.WindowPadding.Horizontal, screenW)
	padY := config.ParsePadding(a.WindowPadding.Vertical, screenH)
	colors := a.Colors.Dark
	cfg := carousel.Config{
		Width:                   screenW - 2*padX,
		Height:                  screenH - 2*padY,
		ThumbWidth:              o.thumb,
		ThumbHeight:             o.thumb,
		Spacing:                 o.spacing,
		PerspectiveFactor:       a.Perspective,
		ShadowOffset:            a.Shadow.Offset,
		ShadowBlur:              a.Shadow.Blur,
		FontPaths:               o.fontPaths,
		FontSize:                o.fontSize,
		WindowBackgroundEnabled: a.WindowBackground.Enabled,
		WindowBackgroundOpacity: a.WindowBackground.Opacity,
		WindowBackgroundRadius:  a.WindowBackground.BorderRadius,
		LayoutMode:              "carousel",
		GridColumns:             a.Grid.Columns,
		GridSpacing:             a.Grid.Spacing,
		BackgroundColor:         colors.Background,
		SelectionFrame:          colors.SelectionFrame,
		TextColor:               colors.Text,
		ShadowColor:             colors.Shadow,
		InactiveFrame:           colors.InactiveFrame,
		UrgentTitleBackground:   colors.UrgentTitleBackground,
	}
	if a.Header.Enabled {
		cfg.Hostname, cfg.Version = o.hostname, o.version
	}
	data := make([]carousel.WindowData, len(wins))
	for i, w := range wins {
		data[i] = carousel.WindowData{Thumbnail: w.thumb, Icon: w.icon, Title: w.title, Workspace: w.workspace, Urgent: w.urgent}
	}
	return &demo{
		data:   data,
		desk:   desk,
		cfg:    cfg,
		padX:   padX,
		padY:   padY,
		key:    def.Keybindings.LayoutToggle,
		step:   a.Animation.Duration,
		header: a.Header.Enabled,
	}
}

// layout is the configuration of the overlay showing the layout mode, with
// the hint of the layout key in its header
func (d *demo) layout(mode string) carousel.Config {
	cfg := d.cfg
	cfg.LayoutMode = mode
	cfg.LayoutHint = layoutHint(d.key, mode, d.header)
	return cfg
}

// layoutHint is the hint of the layout key, as layoutHint of pkg/ui makes it
// (specs/026-layout-keys)
func layoutHint(key, mode string, header bool) string {
	if key == "" || !header {
		return ""
	}
	if r := []rune(key); len(r) == 1 && unicode.IsLetter(r[0]) {
		key = strings.ToUpper(key)
	}
	if mode == "grid" {
		return key + " — carousel"
	}
	return key + " — grid"
}

// rest is the frame at rest of the layout with the selection, as the cpu
// renderer draws it; no window is hovered
func (d *demo) rest(mode string, selected int) *image.RGBA {
	cfg := d.layout(mode)
	if mode == "grid" {
		return carousel.DrawGridLayout(d.data, selected, -1, cfg)
	}
	return carousel.Draw3DCarouselWithData(d.data, selected, -1, 0, cfg)
}

// still is the frame at rest over the desktop, scaled down
func (d *demo) still(mode string, selected int) *image.RGBA {
	return d.shown(d.rest(mode, selected))
}

// shown is the overlay frame as it is seen: over the desktop, scaled down
func (d *demo) shown(overlay *image.RGBA) *image.RGBA {
	full := overDesktop(d.desk, overlay, d.padX, d.padY)
	small := image.NewRGBA(image.Rect(0, 0, screenW/shrink, screenH/shrink))
	draw.CatmullRom.Scale(small, small.Rect, full, full.Rect, draw.Src, nil)
	return small
}

// animation is the loop of the animated picture
func (d *demo) animation() []frame {
	s := newSelector(d, "carousel", 1)
	var frames []frame
	hold := func(ms int) {
		frames = append(frames, frame{d.shown(d.rest(s.mode, s.selected)), ms})
	}
	step := func(target int) {
		for _, img := range s.stepTo(target) {
			frames = append(frames, frame{d.shown(img), int(framePeriod / time.Millisecond)})
		}
	}
	hold(1100)
	step(2)
	hold(600)
	step(3)
	hold(800)
	s.switchLayout("grid")
	hold(900)
	step(3 + carousel.GridColumns(len(d.data), d.layout("grid")))
	hold(600)
	step(s.selected + 1)
	hold(800)
	s.switchLayout("carousel")
	hold(1000)
	return frames
}

// overDesktop is the overlay, premultiplied, blended over the opaque desktop
// at x, y as a compositor blends an ARGB window: source over
func overDesktop(desk, overlay *image.RGBA, x, y int) *image.RGBA {
	out := image.NewRGBA(desk.Rect)
	copy(out.Pix, desk.Pix)
	b := overlay.Rect
	for row := 0; row < b.Dy(); row++ {
		src := overlay.Pix[overlay.PixOffset(b.Min.X, b.Min.Y+row):][:4*b.Dx()]
		dst := out.Pix[out.PixOffset(x, y+row):][:4*b.Dx()]
		for i := 0; i < len(src); i += 4 {
			a := src[i+3]
			if a == 0 {
				continue
			}
			k := 1 - float64(a)/255
			for c := 0; c < 3; c++ {
				dst[i+c] = toByte(float64(src[i+c])/255 + float64(dst[i+c])/255*k)
			}
			dst[i+3] = 255
		}
	}
	return out
}

// toByte is the byte of a normalised value, as a GPU stores it in an RGBA8
// buffer: clamped to [0, 1], rounded to nearest
func toByte(v float64) uint8 {
	return uint8(math.Round(math.Max(0, math.Min(1, v)) * 255))
}
