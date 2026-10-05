package ui

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/almaz-uno/qws/pkg/keygrab"
	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/image/font/gofont/goregular"
)

// BenchmarkE1Switch measures, off the screen, switches from the carousel to
// the grid of E1 by the layout key (specs/028-grid-locate): an overlay of
// 2520×1400, 33 windows with thumbnails of 512×288, the author's appearance,
// the frames and the layers drawn as the switcher draws them, presented by
// the GLX presenter on a window mapped off the screen. Each switch starts
// from the carousel at rest, every layer it asked for drawn and uploaded; the
// loop of handleEventsSync then runs as it runs while no event comes. It
// reports, in milliseconds, the p50 and the p95 over the switches of the time
// from the key to the first frame of the grid presented (frame_*: L off the
// screen) and to the grid's layers held (ready_*: gridReady).
func BenchmarkE1Switch(b *testing.B) {
	// Debug, as in the author's trials, but not written: the presenter then
	// waits for the GPU at the end of a frame, as it does there
	level, logger := zerolog.GlobalLevel(), log.Logger
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	log.Logger = zerolog.New(io.Discard).Level(zerolog.DebugLevel)
	defer func() { zerolog.SetGlobalLevel(level); log.Logger = logger }()

	conn, err := xgb.NewConn()
	if err != nil {
		b.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	_, presenter, err := carousel.NewBackend("glx", nil)
	if err != nil {
		b.Fatal(err)
	}
	defer presenter.Close()
	animator, ok := presenter.(carousel.Animator)
	if !ok {
		b.Skip("no GLX presenter")
	}

	s := e1Selector(b, 33)
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	w, err := carousel.NewWindowAt(conn, root, -s.config.Width-100, 0, s.config.Width, s.config.Height, presenter.VisualID())
	if err != nil {
		b.Fatal(err)
	}
	defer w.Close()
	if err := w.Show(); err != nil {
		b.Fatal(err)
	}
	if err := presenter.Bind(w); err != nil {
		b.Fatal(err)
	}
	timed := &timedPresenter{Presenter: presenter, Animator: animator}
	s.presenter, s.animator = timed, timed

	// The activation, in the carousel
	s.dropLayers()
	s.render(s.prepareThumbnails())
	s.prefetch()

	var frame, ready []float64
	for range b.N {
		loopIdle(s, func() bool { return false })

		s.markFrameCause(causeKey)
		key := s.timing.start
		timed.since(key)
		if s.handleKeyPressSimple(xproto.KeyPressEvent{Detail: benchKeycode(b, 0x0071)}, s.prepareThumbnails()) || !s.grid() {
			b.Fatal("q did not switch to the grid")
		}
		var readyAt time.Time
		loopIdle(s, func() bool {
			if readyAt.IsZero() && s.gridReady() {
				readyAt = time.Now()
			}
			return !readyAt.IsZero() && !timed.first.IsZero()
		})
		if readyAt.IsZero() || timed.first.IsZero() {
			b.Fatal("the grid not presented, or its layers not held")
		}
		frame = append(frame, ms(timed.first.Sub(key)))
		ready = append(ready, ms(readyAt.Sub(key)))

		if s.handleKeyPressSimple(xproto.KeyPressEvent{Detail: benchKeycode(b, 0x0071)}, s.prepareThumbnails()) || s.grid() {
			b.Fatal("q did not switch back to the carousel")
		}
	}
	// The layers of the last switch back drawn, not left to the next run
	loopIdle(s, func() bool { return false })
	b.ReportMetric(percentile(frame, 50), "frame_p50_ms")
	b.ReportMetric(percentile(frame, 95), "frame_p95_ms")
	b.ReportMetric(percentile(ready, 50), "ready_p50_ms")
	b.ReportMetric(percentile(ready, 95), "ready_p95_ms")
}

// loopIdle runs the loop of handleEventsSync as it runs while no event comes
// — the frames of what moves, else the work of the background — until done
// reports true, or until the loop would wait for an event
func loopIdle(s *Selector, done func() bool) {
	for !done() {
		switch {
		case s.moving():
			s.frame()
		case s.backgroundDue() || s.liveDue():
			if s.liveDue() {
				s.liveIdle()
			} else {
				s.uploadIdle()
			}
		default:
			return
		}
	}
}

// timedPresenter is a presenter that records the end of the first frame it
// presents after a time
type timedPresenter struct {
	carousel.Presenter
	carousel.Animator
	after, first time.Time
}

// since starts recording the first frame presented after t
func (p *timedPresenter) since(t time.Time) {
	p.after, p.first = t, time.Time{}
}

func (p *timedPresenter) presented() {
	if p.first.IsZero() && !p.after.IsZero() {
		p.first = time.Now()
	}
}

func (p *timedPresenter) Present(img *image.RGBA) error {
	defer p.presented()
	return p.Presenter.Present(img)
}

func (p *timedPresenter) PresentScene(base carousel.LayerID, items []carousel.SceneItem, f carousel.Fade) error {
	defer p.presented()
	return p.Animator.PresentScene(base, items, f)
}

func (p *timedPresenter) PresentFaded(img *image.RGBA, f carousel.Fade) error {
	defer p.presented()
	return p.Animator.PresentFaded(img, f)
}

func (p *timedPresenter) PresentStaged(f carousel.Fade) error {
	defer p.presented()
	return p.Animator.PresentStaged(f)
}

// e1Selector is a selector of n windows on the overlay of E1 — 2520×1400,
// the monitor of 2560×1440 at 144 Hz less the padding — with the author's
// appearance (research of specs/028-grid-locate): thumbnails of 512×512 on
// the cards, the windows' of 512×288, the spacing 600, the shadow 10 and 15,
// the fonts Noto Sans and DejaVu Sans at 20 where the host has them, the Go
// font otherwise; the default colours of the dark theme, the animations and
// the layout key q, the header of ws1. The test keyboard of keys_test.go.
func e1Selector(tb testing.TB, n int) *Selector {
	fonts := []string{"/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf", "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"}
	for _, f := range fonts {
		if _, err := os.Stat(f); err != nil {
			fonts = []string{filepath.Join(tb.TempDir(), "goregular.ttf")}
			if err := os.WriteFile(fonts[0], goregular.TTF, 0o644); err != nil {
				tb.Fatal(err)
			}
			break
		}
	}
	appearance := config.Default().Appearance
	dark := appearance.Colors.Dark
	keysyms := make([]xproto.Keysym, len(testKeysyms))
	for i, k := range testKeysyms {
		keysyms[i] = xproto.Keysym(k)
	}
	setup := xproto.SetupInfo{MinKeycode: 8, MaxKeycode: xproto.Keycode(8 + len(testKeysyms) - 1)}
	s := &Selector{
		conn:       &xgb.Conn{SetupBytes: setup.Bytes()},
		appearance: appearance,
		config: carousel.Config{
			Width: 2520, Height: 1400, ThumbWidth: 512, ThumbHeight: 512, Spacing: 600,
			PerspectiveFactor: 0.6, ShadowOffset: 10, ShadowBlur: 15, FontPaths: fonts, FontSize: 20,
			WindowBackgroundEnabled: true, WindowBackgroundOpacity: 0.85, WindowBackgroundRadius: 20,
			GridSpacing:     20,
			BackgroundColor: dark.Background, SelectionFrame: dark.SelectionFrame, TextColor: dark.Text,
			ShadowColor: dark.Shadow, InactiveFrame: dark.InactiveFrame, UrgentTitleBackground: dark.UrgentTitleBackground,
		},
		keyConfig: keyConfig{
			modifierMask:          uint16(keygrab.ModAlt),
			backwardMask:          uint16(keygrab.ModShift),
			workspaceModifierMask: uint16(keygrab.ModControl),
			mainKeysym:            0xFF09,
			cancelKeysym:          0xFF1B,
		},
		keymap:            &xproto.GetKeyboardMappingReply{KeysymsPerKeycode: 1, Keysyms: keysyms},
		modmap:            &xproto.GetModifierMappingReply{KeycodesPerModifier: 1, Keycodes: make([]xproto.Keycode, 8)},
		renderer:          carousel.NewCPURenderer(),
		hoverIndex:        -1,
		initialLayoutMode: appearance.Layout,
		period:            time.Second / 144,
	}
	s.keyConfig.layoutToggleKeysym, s.keyConfig.layoutToggleName = parseLayoutToggle("q")
	s.setLayout(appearance.Layout)
	s.SetHeader("ws1", "v1.4.1")
	anim, _ := parseAnimation(appearance.Animation)
	s.anim = anim
	s.step.pos.d, s.step.gx.d, s.step.gy.d = anim.step, anim.step, anim.step
	s.fade.level.d = anim.duration
	titles := []string{"Terminal — ~/wsp/pet/qws", "Документация — Firefox", "qws — Visual Studio Code", "Почта", "Telegram"}
	for i := range n {
		var icon image.Image
		if i%3 != 2 {
			icon = benchPattern(i+100, 48, 48)
		}
		s.windows = append(s.windows, x11.WindowInfo{
			ID:        xproto.Window(10 + i),
			Name:      fmt.Sprintf("%s %d", titles[i%len(titles)], i),
			Icon:      icon,
			Preview:   benchPattern(i, 512, 288),
			Workspace: fmt.Sprintf("%d", i%5+1),
		})
	}
	s.allWindows = s.windows
	s.selectedIndex = 1
	return s
}

// benchPattern is an opaque picture of the size, distinct for every seed
func benchPattern(seed, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 255 / w), G: uint8(y * 255 / h), B: uint8(seed*37) + uint8((x/16+y/16)%2*96), A: 255})
		}
	}
	return img
}

// benchKeycode is the keycode of a key of the test keyboard
func benchKeycode(tb testing.TB, keysym uint32) xproto.Keycode {
	for i, k := range testKeysyms {
		if k == keysym {
			return xproto.Keycode(8 + i)
		}
	}
	tb.Fatalf("no key 0x%X on the test keyboard", keysym)
	return 0
}

// ms is d in milliseconds
func ms(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

// percentile is the p-th percentile of v, the nearest rank
func percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	sorted := slices.Clone(v)
	slices.Sort(sorted)
	i := int(math.Ceil(float64(len(sorted))*p/100)) - 1
	return sorted[min(max(i, 0), len(sorted)-1)]
}
