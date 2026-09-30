// Command glxoverlay checks, outside the switcher, that an OpenGL context can be
// created on and presented to an override-redirect ARGB X window whose XID was
// minted by jezek/xgb — the mechanism of the GLX presenter of qws
// (specs/001-rendering-speed), without the carousel.
//
// Run it in an X11 session with a compositor (picom):
//
//	go run ./console/glxoverlay
//
// Expected: a borderless 480x320 window near the top-left that cycles through
// a few translucent colours for about 5 seconds, then exits. Colours blending
// with the desktop behind mean ARGB and the GLX swap work; if glXMakeCurrent
// fails or the window is opaque or black, the logged error says why.
package main

import (
	"runtime"
	"time"

	"github.com/almaz-uno/qws/pkg/glx"
	"github.com/go-gl/gl/v4.6-core/gl"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	winW = 480
	winH = 320
)

func main() {
	zerolog.SetGlobalLevel(zerolog.DebugLevel)

	// The GLX context and every GL call must stay on a single OS thread.
	runtime.LockOSThread()

	conn, err := xgb.NewConn()
	if err != nil {
		log.Fatal().Err(err).Msg("xgb.NewConn failed")
	}
	defer conn.Close()

	setup := xproto.Setup(conn)
	screen := setup.DefaultScreen(conn)
	root := screen.Root

	// 1. Create the GL context (opens its own Xlib display, picks an ARGB FBConfig).
	ctx, err := glx.NewContext()
	if err != nil {
		log.Fatal().Err(err).Msg("glx.NewContext failed")
	}
	defer ctx.Destroy()
	log.Info().Uint32("visualid", ctx.VisualID()).Msg("GLX context created")

	// 2. Create an override-redirect, depth-32 window with the FBConfig's visual.
	win, err := createWindow(conn, root, ctx.VisualID())
	if err != nil {
		log.Fatal().Err(err).Msg("createWindow failed")
	}

	// 3. Map and raise, then sync so the server has the window before make-current.
	if err := xproto.MapWindowChecked(conn, win).Check(); err != nil {
		log.Fatal().Err(err).Msg("MapWindow failed")
	}
	xproto.ConfigureWindow(conn, win, xproto.ConfigWindowStackMode,
		[]uint32{xproto.StackModeAbove})
	conn.Sync()

	// 4. Bind the context to the xgb-created window and bring up GL.
	if err := ctx.MakeCurrent(uint32(win)); err != nil {
		log.Fatal().Err(err).Msg("glXMakeCurrent failed — visual/FBConfig mismatch?")
	}
	if err := gl.Init(); err != nil {
		log.Fatal().Err(err).Msg("gl.Init failed")
	}
	log.Info().
		Str("version", gl.GoStr(gl.GetString(gl.VERSION))).
		Str("renderer", gl.GoStr(gl.GetString(gl.RENDERER))).
		Msg("GL up on the overlay window")

	// 5. Present a few translucent frames via glXSwapBuffers (no readback).
	colors := [][4]float32{
		{0.8, 0.1, 0.1, 0.5},
		{0.1, 0.8, 0.1, 0.5},
		{0.1, 0.1, 0.8, 0.5},
		{0.0, 0.0, 0.0, 0.0}, // fully transparent — should show desktop through
	}
	gl.Viewport(0, 0, winW, winH)
	for _, c := range colors {
		gl.ClearColor(c[0], c[1], c[2], c[3])
		gl.Clear(gl.COLOR_BUFFER_BIT)
		ctx.SwapBuffers(uint32(win))
		log.Info().Floats32("rgba", c[:]).Msg("frame presented")
		time.Sleep(1200 * time.Millisecond)
	}

	xproto.DestroyWindow(conn, win)
	conn.Sync()
	log.Info().Msg("done")
}

// createWindow builds a borderless, override-redirect, depth-32 window using the
// supplied visual id (mirrors the relevant bits of pkg/carousel/window.go).
func createWindow(conn *xgb.Conn, root xproto.Window, visualID uint32) (xproto.Window, error) {
	win, err := xproto.NewWindowId(conn)
	if err != nil {
		return 0, err
	}

	colormap, err := xproto.NewColormapId(conn)
	if err != nil {
		return 0, err
	}
	if err := xproto.CreateColormapChecked(conn, xproto.ColormapAllocNone,
		colormap, root, xproto.Visualid(visualID)).Check(); err != nil {
		return 0, err
	}

	mask := uint32(xproto.CwBackPixel | xproto.CwBorderPixel |
		xproto.CwOverrideRedirect | xproto.CwEventMask | xproto.CwColormap)
	values := []uint32{
		0, // transparent background
		0, // border pixel (required: window visual differs from root visual)
		1, // override-redirect
		xproto.EventMaskExposure | xproto.EventMaskKeyPress,
		uint32(colormap),
	}

	err = xproto.CreateWindowChecked(
		conn,
		32, // depth — the ARGB visual is depth 32 by construction
		win, root,
		20, 20, // x, y
		winW, winH,
		0, // border width
		xproto.WindowClassInputOutput,
		xproto.Visualid(visualID),
		mask, values,
	).Check()
	if err != nil {
		return 0, err
	}
	return win, nil
}
