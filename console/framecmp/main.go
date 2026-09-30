// Command framecmp compares a frame dumped by qws --debug-dump-frames, raw RGBA
// bytes, with the contents of the overlay window read from the X server, and
// prints how many pixels differ — the absolute error of criterion K3 of
// specs/001-rendering-speed. It exits with status 1 when any pixel differs.
//
//	framecmp <frame.rgba> <width> <height> <window>
//
// The window is read with GetImage, as xwd -id does on its plain path; xwd
// itself sometimes switches to reading the screen through the root visual and
// returns a 24-bit image instead.
package main

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"os"
	"strconv"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: framecmp <frame.rgba> <width> <height> <window>")
		os.Exit(2)
	}
	width, err1 := strconv.Atoi(os.Args[2])
	height, err2 := strconv.Atoi(os.Args[3])
	window, err3 := strconv.ParseUint(os.Args[4], 0, 32)
	if err1 != nil || err2 != nil || err3 != nil {
		fail("bad arguments %v", os.Args[2:])
	}
	frame, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail("%v", err)
	}
	if len(frame) != width*height*4 {
		fail("frame has %d bytes, want %d", len(frame), width*height*4)
	}

	conn, err := xgb.NewConn()
	if err != nil {
		fail("%v", err)
	}
	defer conn.Close()

	img, err := xproto.GetImage(conn, xproto.ImageFormatZPixmap, xproto.Drawable(window),
		0, 0, uint16(width), uint16(height), 0xffffffff).Reply()
	if err != nil {
		fail("GetImage: %v", err)
	}
	if img.Depth != 32 {
		fail("window has depth %d, want 32", img.Depth)
	}
	visual := findVisual(conn, img.Visual)
	if visual == nil {
		fail("visual 0x%x not found", img.Visual)
	}
	if len(img.Data) < width*height*4 {
		fail("window image has %d bytes, want %d", len(img.Data), width*height*4)
	}

	var order binary.ByteOrder = binary.LittleEndian
	if xproto.Setup(conn).ImageByteOrder != xproto.ImageOrderLSBFirst {
		order = binary.BigEndian
	}
	alphaMask := ^(visual.RedMask | visual.GreenMask | visual.BlueMask)
	channel := func(v, mask uint32) byte {
		return byte((v & mask) >> bits.TrailingZeros32(mask))
	}

	diff := 0
	for i := 0; i < width*height; i++ {
		v := order.Uint32(img.Data[i*4:])
		f := frame[i*4:]
		if channel(v, visual.RedMask) != f[0] || channel(v, visual.GreenMask) != f[1] ||
			channel(v, visual.BlueMask) != f[2] || channel(v, alphaMask) != f[3] {
			diff++
		}
	}
	fmt.Printf("AE %d of %d pixels\n", diff, width*height)
	if diff != 0 {
		os.Exit(1)
	}
}

// findVisual looks the visual up in the screens of the setup
func findVisual(conn *xgb.Conn, id xproto.Visualid) *xproto.VisualInfo {
	for _, screen := range xproto.Setup(conn).Roots {
		for _, depth := range screen.AllowedDepths {
			for i := range depth.Visuals {
				if depth.Visuals[i].VisualId == id {
					return &depth.Visuals[i]
				}
			}
		}
	}
	return nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "framecmp: "+format+"\n", args...)
	os.Exit(2)
}
