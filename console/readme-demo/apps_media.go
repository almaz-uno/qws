package main

import (
	"fmt"
	"math"
	"math/rand/v2"

	"github.com/fogleman/gg"
)

// drawMusic draws a music player playing a synthetic album
func drawMusic(p *painter, w, h float64) {
	k := p.k
	g := gg.NewLinearGradient(0, 0, 0, h)
	g.AddColorStop(0, parseHex("#4a1d5f"))
	g.AddColorStop(0.7, parseHex("#121212"))
	p.SetFillStyle(g)
	p.DrawRectangle(0, 0, w, h)
	p.Fill()
	p.rect(0, 0, w, 52, "#00000050")
	p.textC("Music", w/2, 34, k.strong(19), "#ffffff")

	// The cover
	ax, ay, as := 70.0, 110.0, 520.0
	p.Push()
	p.DrawRoundedRectangle(ax, ay, as, as, 14)
	p.Clip()
	cover(p.Context, ax, ay, as)
	p.Pop()

	x := 660.0
	p.text("NOW PLAYING", x, 172, k.strong(17), "#b9a8c9")
	p.text("Vertical Blank", x, 252, k.strong(60), "#ffffff")
	p.text("The Quiet Frames", x, 304, k.ui(28), "#e6dcef")
	p.text("Low Orbit · 2026 · 9 songs", x, 344, k.ui(20), "#9d93a8")

	// The progress
	bx, bw := x, w-x-70
	p.rrect(bx, 424, bw, 6, 3, "#ffffff30")
	p.rrect(bx, 424, bw*0.43, 6, 3, "#ff4f81")
	p.circle(bx+bw*0.43, 427, 10, "#ffffff")
	p.text("1:42", bx, 466, k.ui(18), "#b3b3b3")
	p.textR("3:58", bx+bw, 466, k.ui(18), "#b3b3b3")

	// The controls
	cx := bx + bw/2
	p.circle(cx, 548, 42, "#ffffff")
	p.rect(cx-14, 530, 9, 36, "#121212")
	p.rect(cx+5, 530, 9, 36, "#121212")
	triangle := func(x0, dir float64) {
		p.SetHexColor("#ffffff")
		p.MoveTo(x0, 548)
		p.LineTo(x0+22*dir, 532)
		p.LineTo(x0+22*dir, 564)
		p.ClosePath()
		p.Fill()
	}
	triangle(cx-120, 1)
	p.rect(cx-126, 532, 5, 32, "#ffffff")
	triangle(cx+120, -1)
	p.rect(cx+121, 532, 5, 32, "#ffffff")
	p.SetHexColor("#b3b3b3")
	p.SetLineWidth(3)
	p.DrawLine(cx-250, 536, cx-218, 560)
	p.DrawLine(cx-250, 560, cx-218, 536)
	p.Stroke()
	p.frame(cx+214, 536, 36, 24, 8, 3, "#ff4f81")

	// Up next
	y := 656.0
	p.text("Up next", x, y, k.strong(24), "#ffffff")
	y += 20
	for i, t := range []struct{ title, length string }{
		{"Ease Out", "3:21"}, {"Sixteen Milliseconds", "4:05"}, {"Cross-fade", "2:58"},
		{"Refresh Rate", "3:44"}, {"Premultiplied", "4:12"},
	} {
		if i == 0 {
			p.rrect(x-14, y+4, bw+28, 50, 8, "#ffffff12")
		}
		p.text(fmt.Sprint(i+2), x, y+38, k.ui(19), "#9d93a8")
		p.text(t.title, x+44, y+38, k.ui(20), "#ffffff")
		p.textR(t.length, bx+bw, y+38, k.ui(19), "#9d93a8")
		y += 54
	}

	// The spectrum under the cover
	for i := range 32 {
		v := 0.5 + 0.5*math.Sin(float64(i)*0.7)*math.Cos(float64(i)*0.23+1)
		bh := 30 + 220*math.Abs(v)
		c := []string{"#ff4f81", "#ff7a5c", "#c86bfa", "#7b6cff"}[i%4]
		p.rrect(ax+float64(i)*16.3, h-60-bh, 11, bh, 3, c+"c0")
	}
}

// cover draws the cover of the album into the square: a sun over a grid
func cover(dc *gg.Context, x, y, s float64) {
	horizon := y + 0.64*s
	sky := gg.NewLinearGradient(0, y, 0, horizon)
	sky.AddColorStop(0, parseHex("#1a0533"))
	sky.AddColorStop(0.6, parseHex("#6a1b9a"))
	sky.AddColorStop(1, parseHex("#ff4f81"))
	dc.SetFillStyle(sky)
	dc.DrawRectangle(x, y, s, horizon-y)
	dc.Fill()

	sun := gg.NewLinearGradient(0, y+0.2*s, 0, horizon)
	sun.AddColorStop(0, parseHex("#ffe259"))
	sun.AddColorStop(1, parseHex("#ff6a88"))
	dc.SetFillStyle(sun)
	dc.DrawCircle(x+0.5*s, y+0.5*s, 0.27*s)
	dc.Fill()
	// Bands cut out of the sun
	dc.SetFillStyle(sky)
	for i := range 5 {
		by := y + 0.5*s + float64(i)*0.035*s
		dc.DrawRectangle(x, by, s, 0.008*s+float64(i)*0.004*s)
		dc.Fill()
	}

	dc.SetHexColor("#12002b")
	dc.DrawRectangle(x, horizon, s, y+s-horizon)
	dc.Fill()
	// Mountains on the horizon
	dc.SetHexColor("#2d0b4e")
	dc.MoveTo(x, horizon)
	for i := 0; i <= 40; i++ {
		t := float64(i) / 40
		dc.LineTo(x+t*s, horizon-s*(0.04+0.05*math.Abs(math.Sin(t*9))*math.Sin(t*3.1+0.4)))
	}
	dc.LineTo(x+s, horizon)
	dc.ClosePath()
	dc.Fill()
	// The grid of the floor
	dc.SetHexColor("#ff4fd8b0")
	dc.SetLineWidth(2)
	vx := x + 0.5*s
	for i := -10; i <= 10; i++ {
		dc.DrawLine(vx, horizon, vx+float64(i)*0.16*s, y+s)
		dc.Stroke()
	}
	for i := 1; i <= 8; i++ {
		t := float64(i) / 8
		ly := horizon + (y+s-horizon)*t*t
		dc.DrawLine(x, ly, x+s, ly)
		dc.Stroke()
	}
}

// drawFiles draws a file manager on the repository
func drawFiles(p *painter, w, h float64) {
	k := p.k
	ui := k.ui(19)
	p.rect(0, 0, w, 62, "#ebebeb")
	p.line(0, 62, w, 62, 1, "#d6d6d6")
	for i := range 2 {
		bx := 16 + float64(i)*52
		p.rrect(bx, 12, 44, 38, 8, "#dcdcdc")
		p.SetHexColor("#2e3436")
		p.SetLineWidth(3)
		d := 1.0 - 2*float64(i)
		p.MoveTo(bx+26*d+float64(i)*44, 22)
		p.LineTo(bx+18*d+float64(i)*44, 31)
		p.LineTo(bx+26*d+float64(i)*44, 40)
		p.Stroke()
	}
	x := 330.0
	for i, seg := range []string{"Home", "src", "qws"} {
		face := ui
		if i == 2 {
			face = k.strong(19)
		}
		sw := p.width(seg, face) + 32
		p.rrect(x, 12, sw, 38, 8, []string{"#dcdcdc", "#dcdcdc", "#d0d0d0"}[i])
		p.text(seg, x+16, 38, face, "#2e3436")
		x += sw + 8
	}
	p.circle(w-120, 31, 9, "#2e3436")
	p.circle(w-120, 31, 6, "#ebebeb")
	for i := range 3 {
		p.rect(w-60, 22+float64(i)*8, 24, 3, "#2e3436")
	}

	// The sidebar
	p.rect(0, 63, 300, h-63, "#f6f5f4")
	y := 88.0
	for _, item := range []string{"Recent", "Starred", "Home", "Documents", "Downloads", "Music", "Pictures", "Videos", "Trash", "", "src", "Other Locations"} {
		if item == "" {
			p.line(16, y+10, 284, y+10, 1, "#deddda")
			y += 24
			continue
		}
		if item == "src" {
			p.rrect(8, y, 284, 44, 8, "#dedddb")
		}
		p.rrect(28, y+13, 20, 18, 4, "#77767b")
		p.text(item, 64, y+29, ui, "#2e3436")
		y += 48
	}

	// The items
	items := []struct{ name, kind string }{
		{".github", "dir"}, {"cmd", "dir"}, {"console", "dir"}, {"doc", "dir"}, {"internal", "dir"}, {"pkg", "dir"},
		{"script", "dir"}, {"scripts", "dir"}, {"specs", "dir"}, {".gitignore", "#77767b"}, {"AGENTS.md", "#3584e4"},
		{"CLAUDE.md", "#3584e4"}, {"config.yaml.example", "#c01c28"}, {"go.mod", "#2190a4"}, {"go.sum", "#2190a4"},
		{"LICENSE", "#77767b"}, {"Makefile", "#e66100"}, {"qws", "#26a269"}, {"README.md", "#3584e4"},
		{"RELEASE-NOTES.adoc", "#9141ac"},
	}
	const cellW, cellH = 260.0, 270.0
	cols := int((w - 340) / cellW)
	for i, it := range items {
		cx := 340 + float64(i%cols)*cellW + cellW/2
		cy := 100 + float64(i/cols)*cellH
		if it.name == "pkg" {
			p.rrect(cx-110, cy-10, 220, 250, 12, "#d6e4f7")
		}
		if it.kind == "dir" {
			folder(p, cx-80, cy+10, 160, 136)
		} else {
			page(p, cx-58, cy, 116, 150, it.kind)
		}
		face := ui
		lines := p.wrap(it.name, cellW-40, face)
		for j, l := range lines {
			p.textC(l, cx, cy+190+float64(j)*26, face, "#2e3436")
		}
	}
	p.rrect(w-330, h-60, 300, 40, 8, "#ebebeb")
	p.textC("20 items, 11 files", w-180, h-33, k.ui(17), "#2e3436")
}

// page draws the icon of a file: a page with its corner folded and a band of
// the colour of its kind
func page(p *painter, x, y, w, h float64, c string) {
	p.rrect(x+3, y+4, w, h, 6, "#0000001c")
	p.SetHexColor("#ffffff")
	p.MoveTo(x, y+6)
	p.LineTo(x+w-34, y)
	p.LineTo(x+w, y+34)
	p.LineTo(x+w, y+h)
	p.LineTo(x, y+h)
	p.ClosePath()
	p.Fill()
	p.SetHexColor("#c0bfbc")
	p.SetLineWidth(1.5)
	p.DrawRectangle(x, y, w, h)
	p.Stroke()
	p.rect(x+w-34, y, 34, 34, "#e6e5e3")
	for i := range 5 {
		p.rect(x+16, y+44+float64(i)*14, w-32-float64(i%2)*24, 5, "#deddda")
	}
	p.rect(x, y+h-30, w, 30, c)
}

// drawMonitor draws a system monitor's resources: the CPUs, the memory and
// the network over the last minute
func drawMonitor(p *painter, w, h float64) {
	k := p.k
	ui := k.ui(18)
	p.rect(0, 0, w, 60, "#242424")
	tx := w/2 - 330
	for i, t := range []string{"Processes", "Resources", "File Systems"} {
		tw := p.width(t, ui) + 60
		if i == 1 {
			p.rrect(tx, 10, tw, 40, 8, "#3a3a3a")
		}
		p.text(t, tx+30, 37, ui, "#ffffff")
		tx += tw + 20
	}
	rng := rand.New(rand.NewPCG(29, 7))
	series := func(n int, base, swing float64) []float64 {
		v := make([]float64, n)
		x := base
		for i := range v {
			x += (rng.Float64() - 0.5) * swing
			x += (base - x) * 0.08
			v[i] = math.Max(0.01, math.Min(0.99, x))
		}
		return v
	}
	graph := func(x, y, gw, gh float64, lines [][]float64, colors []string, fill bool) {
		p.rect(x, y, gw, gh, "#262626")
		for i := 1; i < 4; i++ {
			p.line(x, y+gh*float64(i)/4, x+gw, y+gh*float64(i)/4, 1, "#3a3a3a")
		}
		for i := 1; i < 6; i++ {
			p.line(x+gw*float64(i)/6, y, x+gw*float64(i)/6, y+gh, 1, "#323232")
		}
		p.frame(x, y, gw, gh, 0, 1.5, "#3a3a3a")
		for j, s := range lines {
			p.SetHexColor(colors[j])
			p.SetLineWidth(2.5)
			p.SetLineJoinRound()
			for i, v := range s {
				px := x + gw*float64(i)/float64(len(s)-1)
				py := y + gh*(1-v)
				if i == 0 {
					p.MoveTo(px, py)
				} else {
					p.LineTo(px, py)
				}
			}
			if fill {
				p.StrokePreserve()
				p.LineTo(x+gw, y+gh)
				p.LineTo(x, y+gh)
				p.ClosePath()
				p.SetHexColor(colors[j] + "40")
				p.Fill()
			} else {
				p.Stroke()
			}
		}
		p.text("60 seconds", x, y+gh+26, k.ui(15), "#9a9a9a")
		p.textR("0", x+gw, y+gh+26, k.ui(15), "#9a9a9a")
	}

	gx, gw := 60.0, w-120
	p.text("CPU", gx, 112, k.strong(24), "#ffffff")
	var cpus [][]float64
	var colors []string
	for i := range 16 {
		cpus = append(cpus, series(120, 0.12+0.04*float64(i%5), 0.09))
		colors = append(colors, hsv(float64(i)/16, 0.65, 0.95))
	}
	graph(gx, 130, gw, 360, cpus, colors, false)
	for i := range 16 {
		lx := gx + float64(i%4)*(gw/4)
		ly := 560 + float64(i/4)*36
		p.rrect(lx, ly-16, 18, 18, 4, colors[i])
		p.text(fmt.Sprintf("CPU%d   %.1f%%", i+1, 100*cpus[i][len(cpus[i])-1]), lx+30, ly, ui, "#dddddd")
	}

	p.text("Memory and Swap", gx, 760, k.strong(24), "#ffffff")
	mem := series(120, 0.29, 0.01)
	swap := make([]float64, 120)
	graph(gx, 778, gw/2-30, 230, [][]float64{mem, swap}, []string{"#c061cb", "#33d17a"}, true)
	p.text("Memory  9.1 GiB (29.2%) of 31.2 GiB", gx, 1066, ui, "#dddddd")
	p.text("Swap  0 bytes (0.0%) of 2.0 GiB", gx+560, 1066, ui, "#dddddd")

	nx := gx + gw/2 + 30
	p.text("Network", nx, 760, k.strong(24), "#ffffff")
	recv := series(120, 0.25, 0.3)
	sent := series(120, 0.08, 0.12)
	graph(nx, 778, gw/2-30, 230, [][]float64{recv, sent}, []string{"#3584e4", "#e01b24"}, true)
	p.text("Receiving  2.4 MiB/s   Total 1.8 GiB", nx, 1066, ui, "#dddddd")
	p.text("Sending  310 KiB/s   Total 212 MiB", nx+560, 1066, ui, "#dddddd")

	p.text("Disk", gx, 1150, k.strong(24), "#ffffff")
	p.rrect(gx, 1172, gw, 28, 14, "#262626")
	p.rrect(gx, 1172, gw*0.47, 28, 14, "#f6d32d")
	p.text("/  438 GiB of 931 GiB used", gx, 1236, ui, "#dddddd")
}

// hsv is the colour of hue, saturation and value as #rrggbb
func hsv(hue, s, v float64) string {
	i := math.Floor(hue * 6)
	f := hue*6 - i
	pp, q, t := v*(1-s), v*(1-f*s), v*(1-(1-f)*s)
	var r, g, b float64
	switch int(i) % 6 {
	case 0:
		r, g, b = v, t, pp
	case 1:
		r, g, b = q, v, pp
	case 2:
		r, g, b = pp, v, t
	case 3:
		r, g, b = pp, q, v
	case 4:
		r, g, b = t, pp, v
	default:
		r, g, b = v, pp, q
	}
	return fmt.Sprintf("#%02x%02x%02x", toByte(r), toByte(g), toByte(b))
}

// drawCalendar draws the week of the calendar
func drawCalendar(p *painter, w, h float64) {
	k := p.k
	ui := k.ui(17)
	p.rect(0, 0, w, 66, "#ffffff")
	p.line(0, 66, w, 66, 1, "#dadce0")
	p.frame(20, 14, 90, 38, 8, 1.5, "#dadce0")
	p.textC("Today", 65, 40, ui, "#3c4043")
	p.text("‹   ›", 132, 41, k.ui(24), "#5f6368")
	p.text("October 2026", 206, 43, k.strong(28), "#3c4043")
	sx := w - 330
	for i, v := range []string{"Day", "Week", "Month"} {
		bg := "#ffffff"
		if i == 1 {
			bg = "#e8f0fe"
		}
		p.rrect(sx+float64(i)*104, 14, 100, 38, 8, bg)
		p.textC(v, sx+float64(i)*104+50, 40, ui, "#3c4043")
	}

	const hourW = 80.0
	top, first, last := 150.0, 8, 20
	colW := (w - hourW) / 7
	rowH := (h - top) / float64(last-first)
	for d, name := range []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"} {
		cx := hourW + colW*(float64(d)+0.5)
		fg := "#70757a"
		if d == 0 {
			fg = "#1a73e8"
			p.circle(cx, 116, 24, "#1a73e8")
			p.textC("5", cx, 126, k.ui(26), "#ffffff")
		} else {
			p.textC(fmt.Sprint(5+d), cx, 126, k.ui(26), "#3c4043")
		}
		p.textC(name, cx, 84, k.strong(14), fg)
		p.line(hourW+colW*float64(d), top-20, hourW+colW*float64(d), h, 1, "#dadce0")
	}
	for i := first; i < last; i++ {
		y := top + float64(i-first)*rowH
		p.line(hourW-8, y, w, y, 1, "#e8eaed")
		label := fmt.Sprintf("%d AM", i)
		if i >= 12 {
			label = fmt.Sprintf("%d PM", (i-1)%12+1)
		}
		p.textR(label, hourW-14, y+6, k.ui(14), "#70757a")
	}

	type event struct {
		day        int
		start, end float64
		title      string
		color      int
	}
	colors := []struct{ bg, fg string }{
		{"#d2e3fc", "#174ea6"}, {"#e9d7fe", "#5b21b6"}, {"#ceead6", "#0d652d"},
		{"#fde4c8", "#9a4a00"}, {"#fad2cf", "#a50e0e"}, {"#c8f0ec", "#00695c"},
	}
	var events []event
	for d := range 5 {
		events = append(events, event{d, 10, 10.25, "Standup", 0})
	}
	events = append(events,
		event{0, 11, 12, "Spec 029 review", 1}, event{0, 13, 14, "Lunch", 2}, event{0, 14.5, 17, "Focus: README", 3},
		event{1, 12, 13, "Lunch", 2}, event{1, 15, 15.5, "1:1 Bob", 5}, event{1, 18, 19, "Gym", 4},
		event{2, 11, 12.5, "Pairing: grid keys", 1}, event{2, 13, 14, "Lunch", 2}, event{2, 16, 17, "Release sync", 0},
		event{3, 12, 13, "Lunch", 2}, event{3, 14, 16, "Measurements", 3},
		event{4, 12, 13.5, "Team lunch", 2}, event{4, 15, 16, "Demo", 1},
		event{5, 9, 13, "Hike", 5}, event{6, 17, 19, "Dinner", 4},
	)
	for _, e := range events {
		x := hourW + colW*float64(e.day) + 4
		y := top + (e.start-float64(first))*rowH + 2
		eh := (e.end-e.start)*rowH - 4
		c := colors[e.color]
		p.rrect(x, y, colW-10, eh, 6, c.bg)
		p.rrect(x, y, 5, eh, 2, c.fg)
		p.text(clip(p, e.title, colW-30, k.strong(15)), x+12, y+min(22, eh-6), k.strong(15), c.fg)
		if eh > 50 {
			p.text(fmt.Sprintf("%s – %s", clock(e.start), clock(e.end)), x+12, y+44, k.ui(14), c.fg)
		}
	}
	// Now
	ny := top + (10.7-float64(first))*rowH
	p.line(hourW, ny, hourW+colW, ny, 2.5, "#ea4335")
	p.circle(hourW, ny, 7, "#ea4335")
}

// clock is the time of day of the hour h
func clock(h float64) string {
	return fmt.Sprintf("%d:%02d", int(h), int(math.Round((h-math.Floor(h))*60)))
}

// palette is the colours of a landscape at a time of day
type palette struct {
	skyTop, skyMid, horizon, sun string
	ridges                       [4]string
	water                        string
}

var palettes = []palette{
	{"#0b1d3a", "#5b3a7a", "#f6a04d", "#fff1c1", [4]string{"#7a5a8e", "#56437a", "#35305e", "#1b1a33"}, "#2a2f5a"},
	{"#1e5fa8", "#6fb3e6", "#d7eefa", "#ffffff", [4]string{"#8fb3cf", "#5f88ad", "#3c6185", "#1f3a52"}, "#2f6a99"},
	{"#2b1055", "#a53a7a", "#ffb36b", "#ffe0a3", [4]string{"#9a4f7f", "#713d6c", "#4a2a55", "#24163a"}, "#3c2050"},
	{"#03060f", "#0e1d3b", "#2c4a7a", "#dfe8ff", [4]string{"#283a5c", "#1d2b47", "#131e33", "#090f1c"}, "#0d1a33"},
	{"#3a6b35", "#9cc58f", "#f2e8b5", "#fffbe0", [4]string{"#7d9a6a", "#5a7a4c", "#3b5734", "#20331d"}, "#3d6655"},
}

// landscape draws a lake among mountains at dusk into the rectangle, in the
// palette
func landscape(p *painter, x, y, w, h float64, pal palette) {
	horizon := y + 0.62*h
	sky := gg.NewLinearGradient(0, y, 0, horizon)
	sky.AddColorStop(0, parseHex(pal.skyTop))
	sky.AddColorStop(0.65, parseHex(pal.skyMid))
	sky.AddColorStop(1, parseHex(pal.horizon))
	p.SetFillStyle(sky)
	p.DrawRectangle(x, y, w, horizon-y)
	p.Fill()

	sunX, sunY := x+0.64*w, horizon-0.12*h
	glow := gg.NewRadialGradient(sunX, sunY, 0, sunX, sunY, 0.35*w)
	glow.AddColorStop(0, parseHex(pal.sun+"a0"))
	glow.AddColorStop(1, parseHex(pal.sun+"00"))
	p.SetFillStyle(glow)
	p.DrawRectangle(x, y, w, horizon-y)
	p.Fill()
	p.circle(sunX, sunY, 0.045*w, pal.sun)

	ridge := func(layer int, t float64) float64 {
		f := float64(layer)
		return 0.05 + 0.03*f + 0.06*math.Sin(t*(4+f*2.3)+f*1.7)*math.Sin(t*(1.3+f)+f) +
			0.03*math.Sin(t*(13+f*5)+f*0.9) + 0.012*math.Sin(t*(37+f*11))
	}
	// The mountains, far to near, and their reflections in the lake
	water := gg.NewLinearGradient(0, horizon, 0, y+h)
	water.AddColorStop(0, parseHex(pal.horizon))
	water.AddColorStop(0.25, parseHex(pal.water))
	water.AddColorStop(1, parseHex(pal.skyTop))
	p.SetFillStyle(water)
	p.DrawRectangle(x, horizon, w, y+h-horizon)
	p.Fill()
	for layer := range 4 {
		heights := func(t float64) float64 { return h * (ridge(layer, t) + 0.22 - 0.05*float64(layer)) }
		p.SetHexColor(pal.ridges[layer])
		p.MoveTo(x, horizon)
		for i := 0; i <= 120; i++ {
			t := float64(i) / 120
			p.LineTo(x+t*w, horizon-heights(t))
		}
		p.LineTo(x+w, horizon)
		p.ClosePath()
		p.Fill()
		p.SetHexColor(pal.ridges[layer] + "70")
		p.MoveTo(x, horizon)
		for i := 0; i <= 120; i++ {
			t := float64(i) / 120
			p.LineTo(x+t*w, horizon+0.6*heights(t))
		}
		p.LineTo(x+w, horizon)
		p.ClosePath()
		p.Fill()
	}
	// The light of the sun on the water
	for i := range 9 {
		ly := horizon + 0.02*h + float64(i)*0.03*h
		lw := (0.16 - 0.012*float64(i)) * w
		p.rrect(sunX-lw/2, ly, lw, 0.006*h, 0.003*h, pal.sun+"60")
	}
	// Pines on the near shore
	for i := range 14 {
		t := float64(i) / 13
		tx := x + (0.02+0.25*t)*w
		th := h * (0.12 + 0.05*math.Abs(math.Sin(float64(i)*2.1)))
		p.SetHexColor("#0a0d14")
		p.MoveTo(tx, y+h*0.98-th)
		p.LineTo(tx-0.022*w, y+h*0.98)
		p.LineTo(tx+0.022*w, y+h*0.98)
		p.ClosePath()
		p.Fill()
	}
	p.rect(x, y+0.975*h, w, 0.025*h, "#0a0d14")
}

// drawImageViewer draws an image viewer on a photograph of the lake, the
// others of the folder in the strip below
func drawImageViewer(p *painter, w, h float64) {
	k := p.k
	ui := k.ui(18)
	p.rect(0, 0, w, 56, "#2a2a2a")
	p.text("‹   ›", 24, 37, k.ui(26), "#dddddd")
	p.textC("lake.jpg", w/2, 36, k.strong(19), "#ffffff")
	p.textR("3 / 24      −   62%   +", w-30, 36, ui, "#dddddd")

	stripH := 170.0
	ih := h - 56 - stripH - 20
	iw := ih * 1.5
	ix := (w - iw) / 2
	p.Push()
	p.DrawRectangle(ix, 66, iw, ih)
	p.Clip()
	landscape(p, ix, 66, iw, ih, palettes[0])
	p.Pop()

	tw, th := 180.0, 120.0
	n := 9
	sx := (w - float64(n)*tw - float64(n-1)*16) / 2
	for i := range n {
		x := sx + float64(i)*(tw+16)
		y := h - stripH + 10
		p.Push()
		p.DrawRoundedRectangle(x, y, tw, th, 6)
		p.Clip()
		landscape(p, x, y, tw, th, palettes[(i+1)%len(palettes)])
		p.Pop()
		if i == 4 {
			p.frame(x-4, y-4, tw+8, th+8, 8, 4, "#3584e4")
		}
	}
}
