package x11

import (
	"bytes"
	"image"
	"image/draw"
	"testing"
)

// iconProperty builds a _NET_WM_ICON of icons of the sizes, each pixel a
// function of its icon and place
func iconProperty(sizes ...[2]int) []uint32 {
	var data []uint32
	for n, s := range sizes {
		data = append(data, uint32(s[0]), uint32(s[1]))
		for i := 0; i < s[0]*s[1]; i++ {
			data = append(data, uint32(0xff000000|(n*0x10101+i*0x30507)&0xffffff))
		}
	}
	return data
}

// rangeOf reads ranges of a property held in memory, as GetProperty would
func rangeOf(data []uint32) iconRange {
	return func(offset, length uint32) ([]uint32, uint32, error) {
		if int(offset) > len(data) {
			return nil, 0, nil
		}
		end := min(len(data), int(offset+length))
		return data[offset:end], uint32(len(data) - end), nil
	}
}

// sameIcon reports whether two icons are both missing, the same image — an
// icon found on disk by class, kept once — or equal pixel for pixel
func sameIcon(a, b image.Image) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a == b {
		return true
	}
	return a.Bounds() == b.Bounds() && bytes.Equal(rgba(a).Pix, rgba(b).Pix)
}

// rgba is the image as RGBA
func rgba(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok {
		return r
	}
	r := image.NewRGBA(img.Bounds())
	draw.Draw(r, r.Rect, img, img.Bounds().Min, draw.Src)
	return r
}

// TestIconByHeaders checks criterion K2 of specs/003-window-list on properties
// made here: reading the sizes first and then the one icon chosen gives the
// icon that decoding the whole property gives
func TestIconByHeaders(t *testing.T) {
	cases := map[string][]uint32{
		"one":              iconProperty([2]int{48, 48}),
		"several":          iconProperty([2]int{16, 16}, [2]int{32, 32}, [2]int{48, 48}, [2]int{64, 64}, [2]int{256, 256}),
		"larger first":     iconProperty([2]int{128, 128}, [2]int{64, 64}, [2]int{16, 16}),
		"smaller only":     iconProperty([2]int{16, 16}, [2]int{24, 24}),
		"not square":       iconProperty([2]int{48, 32}, [2]int{20, 60}),
		"a tie":            iconProperty([2]int{64, 32}, [2]int{32, 64}),
		"empty":            nil,
		"a header alone":   {48, 48},
		"invalid size":     append(iconProperty([2]int{32, 32}), 0, 16),
		"too large":        append(iconProperty([2]int{16, 16}), 600, 600),
		"short":            iconProperty([2]int{32, 32})[:500],
		"one value":        {16},
		"invalid then one": append([]uint32{0, 0}, iconProperty([2]int{48, 48})...),
	}
	for name, data := range cases {
		want := findBestIcon(data)
		got, err := readIconByHeaders(rangeOf(data))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !sameIcon(got, want) {
			t.Errorf("%s: by headers %v, whole %v", name, bounds(got), bounds(want))
		}
	}
}

func bounds(img image.Image) any {
	if img == nil {
		return nil
	}
	return img.Bounds()
}

// TestIconByHeadersOfWindows checks K2 on the windows of the display there
// is: each icon read by its headers equals the icon read whole
func TestIconByHeadersOfWindows(t *testing.T) {
	c, err := Connect()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer c.Close()
	windows, err := c.GetClientList()
	if err != nil {
		t.Skipf("no client list: %v", err)
	}
	atom, err := c.InternAtom("_NET_WM_ICON", true)
	if err != nil {
		t.Skip("no _NET_WM_ICON")
	}
	for _, w := range windows {
		want, _ := c.GetWindowIcon(w)
		got, err := readIconByHeaders(propertyRange(c.Conn, w, atom))
		if err != nil {
			continue // gone meanwhile
		}
		if !sameIcon(got, want) {
			t.Errorf("window 0x%x: by headers %v, whole %v", w, bounds(got), bounds(want))
		}
	}
}
