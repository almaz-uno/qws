package main

import (
	"image"
	"image/color"
	"math"

	"github.com/fogleman/gg"
)

// iconSize is the side of the icons, as an application gives one in
// _NET_WM_ICON
const iconSize = 128

// parseHex is the colour of #rrggbb or #rrggbbaa
func parseHex(s string) color.Color {
	var c [4]uint8
	c[3] = 255
	for i := 0; i < (len(s)-1)/2 && i < 4; i++ {
		c[i] = hexByte(s[1+2*i])<<4 | hexByte(s[2+2*i])
	}
	return color.NRGBA{c[0], c[1], c[2], c[3]}
}

func hexByte(b byte) uint8 {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10
	}
	return 0
}

// drawIcon draws the icon of a kind of application on a transparent image
func drawIcon(k *kit, kind string) *image.RGBA {
	p := &painter{gg.NewContext(iconSize, iconSize), k}
	const s = iconSize
	tile := func(top, bottom string) {
		g := gg.NewLinearGradient(0, 8, 0, s-8)
		g.AddColorStop(0, parseHex(top))
		g.AddColorStop(1, parseHex(bottom))
		p.SetFillStyle(g)
		p.DrawRoundedRectangle(8, 8, s-16, s-16, 26)
		p.Fill()
	}
	switch kind {
	case "terminal":
		tile("#3b4261", "#1a1b26")
		p.SetHexColor("#9ece6a")
		p.SetLineWidth(9)
		p.SetLineCapRound()
		p.MoveTo(34, 44)
		p.LineTo(58, 64)
		p.LineTo(34, 84)
		p.Stroke()
		p.line(66, 88, 94, 88, 9, "#c0caf5")
	case "editor":
		tile("#4fb47a", "#1f6f4a")
		p.SetLineCapRound()
		p.SetLineWidth(9)
		p.SetHexColor("#ffffff")
		p.MoveTo(48, 38)
		p.LineTo(28, 64)
		p.LineTo(48, 90)
		p.Stroke()
		p.MoveTo(80, 38)
		p.LineTo(100, 64)
		p.LineTo(80, 90)
		p.Stroke()
		p.line(72, 32, 56, 96, 8, "#d8ffe8")
	case "browser":
		g := gg.NewLinearGradient(0, 10, 0, s-10)
		g.AddColorStop(0, parseHex("#5ac8fa"))
		g.AddColorStop(1, parseHex("#1565c0"))
		p.SetFillStyle(g)
		p.DrawCircle(s/2, s/2, 54)
		p.Fill()
		p.SetHexColor("#ffffffd0")
		p.SetLineWidth(5)
		p.DrawCircle(s/2, s/2, 44)
		p.Stroke()
		p.DrawEllipse(s/2, s/2, 20, 44)
		p.Stroke()
		p.DrawLine(s/2-44, s/2, s/2+44, s/2)
		p.Stroke()
		p.DrawLine(s/2-38, s/2-22, s/2+38, s/2-22)
		p.Stroke()
		p.DrawLine(s/2-38, s/2+22, s/2+38, s/2+22)
		p.Stroke()
	case "pdf":
		tile("#ff6b5a", "#c62828")
		p.rrect(36, 24, 56, 76, 6, "#ffffff")
		for i := range 5 {
			p.rect(44, 38+float64(i)*11, 40-float64(i%2)*12, 5, "#e57373")
		}
		p.text("PDF", 40, 116, k.strong(22), "#ffffff")
	case "chat":
		tile("#9b7cff", "#5b3fd1")
		p.SetHexColor("#ffffff")
		p.DrawRoundedRectangle(26, 32, 76, 52, 18)
		p.Fill()
		p.MoveTo(40, 80)
		p.LineTo(34, 100)
		p.LineTo(58, 82)
		p.ClosePath()
		p.Fill()
		for i := range 3 {
			p.circle(46+float64(i)*18, 58, 6, "#6f52e6")
		}
	case "music":
		tile("#ff8a65", "#d81b60")
		p.SetHexColor("#ffffff")
		p.DrawEllipse(46, 88, 15, 11)
		p.Fill()
		p.DrawEllipse(86, 80, 15, 11)
		p.Fill()
		p.SetLineWidth(7)
		p.DrawLine(58, 88, 58, 36)
		p.Stroke()
		p.DrawLine(98, 80, 98, 28)
		p.Stroke()
		p.SetLineWidth(12)
		p.DrawLine(58, 40, 98, 32)
		p.Stroke()
	case "files":
		folder(p, 12, 20, 104, 88)
	case "monitor":
		tile("#2b2f3a", "#121419")
		p.SetLineWidth(6)
		p.SetLineCapRound()
		p.SetLineJoinRound()
		p.SetHexColor("#66e08a")
		pts := []float64{30, 78, 46, 70, 56, 84, 70, 46, 82, 60, 98, 40}
		p.MoveTo(pts[0], pts[1])
		for i := 2; i < len(pts); i += 2 {
			p.LineTo(pts[i], pts[i+1])
		}
		p.Stroke()
		p.line(28, 98, 100, 98, 4, "#66e08a80")
	case "calendar":
		p.rrect(14, 14, 100, 100, 22, "#ffffff")
		p.Push()
		p.DrawRoundedRectangle(14, 14, 100, 100, 22)
		p.Clip()
		p.rect(14, 14, 100, 30, "#ea4335")
		p.Pop()
		p.textC("OCT", 64, 38, k.strong(18), "#ffffff")
		p.textC("5", 64, 100, k.strong(52), "#202124")
	case "image":
		p.Push()
		p.DrawRoundedRectangle(8, 8, s-16, s-16, 26)
		p.Clip()
		landscape(p, 8, 8, s-16, s-16, palettes[0])
		p.Pop()
	case "mail":
		tile("#4f8ff7", "#1a5fd0")
		p.rrect(26, 38, 76, 52, 8, "#ffffff")
		p.SetHexColor("#1a5fd0")
		p.SetLineWidth(5)
		p.SetLineJoinRound()
		p.MoveTo(30, 42)
		p.LineTo(64, 68)
		p.LineTo(98, 42)
		p.Stroke()
	}
	return p.image()
}

// folder draws a folder in the rectangle
func folder(p *painter, x, y, w, h float64) {
	p.rrect(x, y, w*0.45, h*0.3, h*0.08, "#3584e4")
	p.rrect(x, y+h*0.1, w, h*0.9, h*0.08, "#438de6")
	g := gg.NewLinearGradient(0, y+h*0.22, 0, y+h)
	g.AddColorStop(0, parseHex("#7cb4f2"))
	g.AddColorStop(1, parseHex("#5a9ce9"))
	p.SetFillStyle(g)
	p.DrawRoundedRectangle(x, y+h*0.22, w, h*0.78, h*0.08)
	p.Fill()
	p.line(x+w*0.08, y+h*0.32, x+w*0.92, y+h*0.32, math.Max(1, h*0.02), "#ffffff40")
}
