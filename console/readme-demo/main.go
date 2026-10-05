// Command readme-demo draws the pictures of README.md (specs/029-readme): the
// animated picture doc/images/qws.webp and the stills doc/images/carousel.png
// and doc/images/grid.png.
//
//	go run ./console/readme-demo [flags]   (make readme-images)
//
// Nothing comes from the screen. The program draws a dozen synthetic windows
// — a terminal, an editor showing the source of qws, a browser and the like —
// and their icons; averages each window into its thumbnail as the snapshots
// of pkg/snapshot do; and draws the overlay of qws over a synthetic desktop
// of 2560×1440, the overlay 2520×1400 at its default padding of 20 pixels:
//
//   - a frame at rest is the frame of the cpu renderer:
//     carousel.Draw3DCarouselWithData or carousel.DrawGridLayout;
//   - a frame in motion is a scene of the layers of pkg/carousel composed on
//     the CPU as the glx presenter composes it on the GPU (specs/007-animation):
//     the items of pkg/ui's carouselItems and gridItems, placed as its
//     placeLayer places them, sampled bilinearly and blended source-over with
//     premultiplied alpha (scene.go).
//
// The overlay is configured from config.Default() in the dark theme, but for
// the thumbnails of 512, the spacing of 600 and the font size of 20 — the
// author's configuration on such a monitor, where those of config.Default()
// leave the cards small. The windows do not change while it is shown, so no
// live thumbnail is drawn over their cards, as in qws.
//
// The overlay is blended over the desktop as a compositor blends an ARGB
// window, and the picture is scaled down 2× with Catmull-Rom. The animation
// is a loop: the carousel at rest, two steps right, the layout key — the grid
// at once —, a step down and a step right in the grid, the layout key back to
// the carousel. Steps take 150 ms along ease-out cubic, as in qws; their
// frames are 20 ms apart, and a frame at rest is one long frame of the WebP,
// which img2webp (the webp package) encodes losslessly: its frames are the
// pixels drawn.
//
// The stills come out byte for byte the same on every run: the data is fixed,
// nothing depends on the time or on chance.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// options are the flags of the program
type options struct {
	out         string
	fontPaths   []string
	monoFont    string
	boldFont    string
	source      string
	hostname    string
	version     string
	thumb       int
	spacing     float64
	fontSize    int
	img2webp    string
	maxWebPSize int64
}

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	defaults := config.Default().Appearance
	var o options
	var fonts string
	flag.StringVar(&o.out, "out", "doc/images", "directory the pictures are written to")
	flag.StringVar(&fonts, "font-paths", strings.Join(defaults.Font.Paths, ","),
		"fonts of the overlay and of the windows' text, primary first, comma-separated (appearance.font.paths)")
	flag.StringVar(&o.monoFont, "mono-font", "/usr/share/fonts/truetype/noto/NotoSansMono-Regular.ttf",
		"monospace font of the terminals and the editor")
	flag.StringVar(&o.boldFont, "bold-font", "/usr/share/fonts/truetype/noto/NotoSans-Bold.ttf",
		"bold font of the windows' headings")
	flag.StringVar(&o.source, "source", "pkg/ui/motion.go", "the source file the editor shows")
	flag.StringVar(&o.hostname, "hostname", "devbox", "hostname of the header")
	flag.StringVar(&o.version, "version", "v1.4.1", "version of the header")
	flag.IntVar(&o.thumb, "thumb", 512, "appearance.thumbnail.width and height")
	flag.Float64Var(&o.spacing, "spacing", 600, "appearance.spacing")
	flag.IntVar(&o.fontSize, "font-size", 20, "appearance.font.size")
	flag.StringVar(&o.img2webp, "img2webp", "img2webp", "the img2webp program of libwebp")
	flag.Int64Var(&o.maxWebPSize, "max-webp-size", 3_000_000, "the largest WebP accepted, bytes")
	flag.Parse()
	o.fontPaths = strings.Split(fonts, ",")

	if err := run(o); err != nil {
		log.Fatal().Err(err).Msg("readme-demo failed")
	}
}

// run draws the pictures
func run(o options) error {
	for _, path := range append(append([]string{}, o.fontPaths...), o.monoFont, o.boldFont) {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("font missing: %w", err)
		}
	}
	webp, err := exec.LookPath(o.img2webp)
	if err != nil {
		return fmt.Errorf("img2webp is needed for the animated picture (Debian: apt install webp): %w", err)
	}
	source, err := os.ReadFile(o.source)
	if err != nil {
		return fmt.Errorf("the source the editor shows: %w", err)
	}
	if err := os.MkdirAll(o.out, 0o755); err != nil {
		return err
	}

	k := &kit{sans: o.fontPaths, mono: append([]string{o.monoFont}, o.fontPaths...), bold: append([]string{o.boldFont}, o.fontPaths...)}
	log.Info().Msg("Drawing the windows")
	wins := drawWindows(k, string(source), filepath.ToSlash(o.source))
	desk := drawDesktop(k, wins)
	d := newDemo(o, wins, desk)

	log.Info().Msg("Drawing the stills")
	if err := writePNG(filepath.Join(o.out, "carousel.png"), d.still("carousel", 3), png.BestCompression); err != nil {
		return err
	}
	if err := writePNG(filepath.Join(o.out, "grid.png"), d.still("grid", 8), png.BestCompression); err != nil {
		return err
	}

	log.Info().Msg("Drawing the animation")
	tmp, err := os.MkdirTemp("", "readme-demo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	frames := d.animation()
	args := []string{"-loop", "0", "-lossless", "-m", "4"}
	total := 0
	for i, f := range frames {
		path := filepath.Join(tmp, fmt.Sprintf("frame-%03d.png", i))
		if err := writePNG(path, f.img, png.BestSpeed); err != nil {
			return err
		}
		args = append(args, "-d", fmt.Sprint(f.ms), path)
		total += f.ms
	}
	target := filepath.Join(o.out, "qws.webp")
	args = append(args, "-o", target)
	cmd := exec.Command(webp, args...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("img2webp: %w", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if info.Size() > o.maxWebPSize {
		return fmt.Errorf("%s has %d bytes, more than %d", target, info.Size(), o.maxWebPSize)
	}
	log.Info().Int("frames", len(frames)).Int("ms", total).Int64("bytes", info.Size()).Str("path", target).Msg("Animation written")
	return nil
}

// writePNG writes img to path as PNG
func writePNG(path string, img image.Image, level png.CompressionLevel) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := png.Encoder{CompressionLevel: level}
	if err := enc.Encode(f, img); err != nil {
		f.Close()
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	if level == png.BestCompression {
		log.Info().Str("path", path).Msg("Still written")
	}
	return nil
}
