package main

import (
	"image"

	"github.com/fogleman/gg"
)

// window is a synthetic window: what the switcher knows of it, and how it is
// drawn
type window struct {
	title     string
	workspace string
	urgent    bool
	icon      *image.RGBA
	thumb     *image.RGBA
	full      *image.RGBA // the window itself, kept for the desktop
	rect      image.Rectangle
	shown     bool // on the current workspace, behind the overlay
}

// The layout of the desktop, an i3 session with gaps: the bar at the top,
// the windows tiled below it
const (
	barH = 36
	gap  = 20
)

var (
	fullRect  = image.Rect(gap, barH+gap, screenW-gap, screenH-gap)
	leftRect  = image.Rect(gap, barH+gap, screenW/2-gap/2, screenH-gap)
	rightRect = image.Rect(screenW/2+gap/2, barH+gap, screenW-gap, screenH-gap)
	floatRect = image.Rect(480, 220, 2080, 1220)
)

// spec is how a window is made
type spec struct {
	title, workspace string
	urgent           bool
	rect             image.Rectangle
	icon             string
	bg               string
	draw             func(p *painter, w, h float64)
}

// drawWindows draws the windows in the order of the switcher, the most
// recently used first: the focused editor and the terminal beside it on the
// current workspace, then the rest
func drawWindows(k *kit, source, sourcePath string) []window {
	specs := []spec{
		{"nvim — " + sourcePath, "1: code", false, leftRect, "editor", tnBg,
			func(p *painter, w, h float64) { drawEditor(p, w, h, source, sourcePath) }},
		{"Terminal — go test ./...", "1: code", false, rightRect, "terminal", tnBg, drawGoTest},
		{"carousel - Go Docs — Browser", "2: web", false, fullRect, "browser", "#ffffff", drawBrowser},
		{"spec 007 — Document Viewer", "3: docs", false, leftRect, "pdf", "#3b3b3b", drawDocument},
		{"#qws-dev — Chat", "4: chat", true, fullRect, "chat", "#1d1e24", drawChat},
		{"Vertical Blank — Music", "5: media", false, floatRect, "music", "#121212", drawMusic},
		{"qws — Files", "6: files", false, fullRect, "files", "#ffffff", drawFiles},
		{"System Monitor", "7: sys", false, fullRect, "monitor", "#1e1e1e", drawMonitor},
		{"October 2026 — Calendar", "8: plan", false, rightRect, "calendar", "#ffffff", drawCalendar},
		{"lake.jpg — Image Viewer", "5: media", false, fullRect, "image", "#1b1b1b", drawImageViewer},
		{"Inbox (3) — Mail", "8: plan", false, leftRect, "mail", "#ffffff", drawMail},
		{"Terminal — git log --graph", "3: docs", false, rightRect, "terminal", tnBg, drawGitLog},
	}
	wins := make([]window, len(specs))
	for i, s := range specs {
		p := newPainter(k, s.rect.Dx(), s.rect.Dy(), s.bg)
		s.draw(p, float64(s.rect.Dx()), float64(s.rect.Dy()))
		full := p.image()
		wins[i] = window{
			title:     s.title,
			workspace: s.workspace,
			urgent:    s.urgent,
			icon:      drawIcon(k, s.icon),
			thumb:     thumbnail(full),
			rect:      s.rect,
			shown:     s.workspace == specs[0].workspace,
		}
		if wins[i].shown {
			wins[i].full = full
		}
	}
	return wins
}

// drawDesktop draws the desktop behind the overlay: the wallpaper, the bar
// and the windows of the current workspace with their borders, the focused
// one first in the list
func drawDesktop(k *kit, wins []window) *image.RGBA {
	p := newPainter(k, screenW, screenH, "#0b0d1a")
	wallpaper(p.Context, 0, 0, screenW, screenH)

	// The bar
	p.rect(0, 0, screenW, barH, "#0d0f1aee")
	face := k.ui(18)
	x := 8.0
	focused := wins[0].workspace
	for _, ws := range []string{"1: code", "2: web", "3: docs", "4: chat", "5: media", "6: files", "7: sys", "8: plan"} {
		w := p.width(ws, face) + 24
		bg, fg := "#1c1f30", "#9aa0b8"
		switch {
		case ws == focused:
			bg, fg = "#4a9eff", "#0b0d1a"
		case urgentOn(wins, ws):
			bg, fg = "#d32f2f", "#ffffff"
		}
		p.rrect(x, 4, w, barH-8, 4, bg)
		p.text(ws, x+12, 24, face, fg)
		x += w + 6
	}
	p.textR("CPU 7%   MEM 9.1 GiB   VOL 40%   Mon 5 Oct 10:42", screenW-16, 24, face, "#c0caf5")

	for i, w := range wins {
		if !w.shown {
			continue
		}
		r := w.rect
		p.DrawImage(w.full, r.Min.X, r.Min.Y)
		border := "#33364a"
		if i == 0 {
			border = "#4a9eff"
		}
		p.SetHexColor(border)
		p.SetLineWidth(2)
		p.DrawRectangle(float64(r.Min.X)+1, float64(r.Min.Y)+1, float64(r.Dx())-2, float64(r.Dy())-2)
		p.Stroke()
	}
	return p.image()
}

// urgentOn reports whether a window of the workspace is urgent
func urgentOn(wins []window, ws string) bool {
	for _, w := range wins {
		if w.workspace == ws && w.urgent {
			return true
		}
	}
	return false
}

// wallpaper draws the wallpaper into the rectangle: a dusk gradient with two
// soft glows and faint rings
func wallpaper(dc *gg.Context, x, y, w, h float64) {
	g := gg.NewLinearGradient(x, y, x+w, y+h)
	g.AddColorStop(0, parseHex("#0e1a3a"))
	g.AddColorStop(0.45, parseHex("#2b1f5c"))
	g.AddColorStop(0.8, parseHex("#6a2c70"))
	g.AddColorStop(1, parseHex("#b8456b"))
	dc.SetFillStyle(g)
	dc.DrawRectangle(x, y, w, h)
	dc.Fill()

	glow := func(cx, cy, r float64, c string) {
		rg := gg.NewRadialGradient(cx, cy, 0, cx, cy, r)
		rg.AddColorStop(0, parseHex(c))
		rg.AddColorStop(1, parseHex(c[:7]+"00"))
		dc.SetFillStyle(rg)
		dc.DrawRectangle(x, y, w, h)
		dc.Fill()
	}
	glow(x+0.18*w, y+0.82*h, 0.45*w, "#1fb6c680")
	glow(x+0.82*w, y+0.18*h, 0.35*w, "#ff7a9a60")

	dc.SetLineWidth(3)
	for i, r := range []float64{0.30, 0.38, 0.47} {
		dc.SetHexColor([]string{"#ffffff18", "#ffffff12", "#ffffff0c"}[i])
		dc.DrawCircle(x+0.62*w, y+0.58*h, r*w)
		dc.Stroke()
	}
}
