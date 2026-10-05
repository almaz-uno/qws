package main

import (
	"golang.org/x/image/font"
)

// drawBrowser draws a browser on the documentation of pkg/carousel served by
// a local documentation server
func drawBrowser(p *painter, w, h float64) {
	k := p.k
	ui, small := k.ui(19), k.ui(17)

	// Tabs
	p.rect(0, 0, w, 46, "#dfe1e5")
	tabs := []string{"carousel - Go Docs", "spec 007 — animation", "Extended Window Manager Hints", "Go 1.26 Release Notes"}
	x := 12.0
	for i, t := range tabs {
		tw := 380.0
		if i == 0 {
			p.rrect(x, 6, tw, 50, 10, "#ffffff")
		} else if i > 1 {
			p.line(x, 14, x, 36, 1, "#a8abb0")
		}
		p.circle(x+26, 26, 9, []string{"#00add8", "#4a9eff", "#7d8590", "#00add8"}[i])
		p.text(t, x+46, 32, small, "#3c4043")
		p.text("×", x+tw-30, 32, ui, "#5f6368")
		x += tw + 4
	}
	p.text("+", x+14, 34, k.ui(26), "#5f6368")

	// The tool bar
	p.rect(0, 46, w, 54, "#ffffff")
	arrow := func(cx float64, dir float64, c string) {
		p.SetHexColor(c)
		p.SetLineWidth(3)
		p.SetLineCapRound()
		p.DrawLine(cx-10*dir, 73, cx+10*dir, 73)
		p.Stroke()
		p.MoveTo(cx-2*dir, 65)
		p.LineTo(cx-10*dir, 73)
		p.LineTo(cx-2*dir, 81)
		p.Stroke()
	}
	arrow(36, 1, "#5f6368")
	arrow(84, -1, "#babcbe")
	p.SetHexColor("#5f6368")
	p.SetLineWidth(3)
	p.DrawArc(132, 73, 10, 0.6, 5.6)
	p.Stroke()
	p.rrect(170, 54, w-340, 38, 19, "#f1f3f4")
	p.circle(196, 73, 7, "#5f6368")
	end := p.text("localhost:6060", 220, 80, ui, "#202124")
	p.text("/pkg/github.com/almaz-uno/qws/pkg/carousel/", end, 80, ui, "#5f6368")
	for i := range 3 {
		p.circle(w-120+float64(i)*28, 73, 9, []string{"#e8eaed", "#e8eaed", "#4a9eff"}[i])
	}
	p.line(0, 100, w, 100, 1, "#dadce0")

	// The page
	p.rect(0, 101, w, 64, "#253858")
	p.text("Go Documentation Server", 60, 143, k.strong(24), "#ffffff")
	p.textR("Packages    Search", w-60, 143, ui, "#c9d4e8")

	left, col := 120.0, 1560.0
	y := 250.0
	p.text("Package carousel", left, y, k.strong(46), "#202224")
	y += 50
	mono := k.code(20)
	p.rrect(left, y, 700, 46, 6, "#f2f3f5")
	p.text(`import "github.com/almaz-uno/qws/pkg/carousel"`, left+18, y+31, mono, "#202224")
	y += 110
	p.text("Overview", left, y, k.strong(32), "#202224")
	y += 50
	text := k.ui(21)
	y = p.para("Layers of the animation of specs/007-animation: pieces of a frame that the CPU draws once, "+
		"with the drawing code of both renderers, and the GLX presenter moves. Each layer is an image whose "+
		"bounds are its place in the frame at rest.", left, y, col, 34, text, "#3e4042")
	y += 14
	y = p.para("Frames are always drawn by the CPU renderer; the presenter only decides how they reach the "+
		"window: cpu presents them with PutImage, glx through GLX on the overlay window, and falls back to "+
		"cpu when GLX cannot be initialised.", left, y, col, 34, text, "#3e4042")
	y += 40
	p.text("Index", left, y, k.strong(32), "#202224")
	y += 50
	for _, f := range []string{
		"func CardCenter(windowData []WindowData, index int, offset float64, cfg Config) (x, y, scale float64, ok bool)",
		"func CardLayer(windowData []WindowData, index, offset int, cfg Config) *image.RGBA",
		"func CarouselBase(cfg Config) *image.RGBA",
		"func Draw3DCarouselWithData(windowData []WindowData, selected int, hoverIndex int, animOffset float64, cfg Config) *image.RGBA",
		"func DrawGridLayout(windowData []WindowData, selected int, hoverIndex int, cfg Config) *image.RGBA",
		"func GridColumns(n int, cfg Config) int",
		"func GridSelection(w, h float64, cfg Config) *image.RGBA",
		"func GridShadow(w, h, o float64, cfg Config) *image.RGBA",
		"func GridTile(n, i int, cfg Config) (x, y, w, h float64)",
		"func GridTiles(windowData []WindowData, cfg Config) *image.RGBA",
		"type Animator",
		"type Config",
		"type Fade",
		"type Presenter",
		"type SceneItem",
	} {
		p.text(f, left+24, y, mono, "#2f62b0")
		y += 36
	}
	y += 40
	p.text("func CardLayer", left, y, k.strong(28), "#202224")
	y += 26
	p.rrect(left, y, col, 60, 6, "#f7f8fa")
	p.frame(left, y, col, 60, 6, 1, "#e1e4e8")
	p.text("func CardLayer(windowData []WindowData, index, offset int, cfg Config) *image.RGBA", left+20, y+38, mono, "#202224")
	y += 100
	p.para("CardLayer is the card of window index as Draw3DCarouselWithData draws it at the integer offset "+
		"from the selection — with the selection frame at offset 0, without the hover — on a transparent "+
		"image; nil when the card is not drawn at that offset.", left, y, col, 34, text, "#3e4042")

	// Contents
	sx := 1840.0
	p.line(sx-40, 220, sx-40, h-40, 1, "#e8eaed")
	p.text("Contents", sx, 250, k.strong(22), "#202224")
	cy := 300.0
	for _, s := range []string{"Overview", "Index", "Functions", "Types", "Source files"} {
		p.text(s, sx, cy, ui, "#2f62b0")
		cy += 40
	}
	cy += 30
	p.text("Source files", sx, cy, k.strong(20), "#202224")
	cy += 44
	for _, s := range []string{"factory.go", "fontfallback.go", "gridheads.go", "layers.go", "live.go", "prepared.go",
		"present.go", "present_glx.go", "renderer-cpu.go", "renderer.go", "window.go"} {
		p.text(s, sx, cy, mono, "#2f62b0")
		cy += 36
	}
}

// drawDocument draws a document viewer on the first page of spec 007
func drawDocument(p *painter, w, h float64) {
	k := p.k
	ui := k.ui(18)
	p.rect(0, 0, w, 56, "#2d2d2d")
	for i := range 3 {
		p.rect(24, 18+float64(i)*8, 24, 3, "#d8d8d8")
	}
	p.text("spec-007-animation.pdf", 72, 36, ui, "#ececec")
	p.rrect(w-330, 12, 90, 32, 6, "#3d3d3d")
	p.text("1 / 9", w-312, 35, ui, "#ececec")
	p.text("−   100%   +", w-220, 35, ui, "#ececec")
	p.circle(w-40, 26, 9, "#9a9a9a")
	p.circle(w-40, 26, 6, "#2d2d2d")

	// The page, cut by the bottom of the window
	px, py, pw := 56.0, 90.0, w-112
	ph := pw * 1.414
	p.rrect(px-2, py+2, pw+4, ph+6, 2, "#00000060")
	p.rect(px, py, pw, ph, "#ffffff")
	x, right := px+90, px+pw-90
	y := py + 130
	p.text("007 — animation", x, y, k.strong(44), "#111111")
	y += 44
	p.text("2026-10-01 · implemented · issue 17", x, y, ui, "#777777")
	y += 50
	p.rrect(x, y, right-x, 214, 4, "#f4f6f8")
	p.text("Contents", x+24, y+40, k.strong(20), "#333333")
	for i, s := range []string{"1  What and why", "2  Decisions", "3  Behavior", "4  Acceptance criteria", "5  Results"} {
		p.text(s, x+40, y+78+float64(i)*28, k.ui(17), "#1565c0")
	}
	y += 280
	body := k.ui(19)
	heading := func(s string) {
		p.text(s, x, y, k.strong(28), "#1a237e")
		y += 46
	}
	para := func(s string) {
		y = p.para(s, x, y, right-x, 30, body, "#333333") + 14
	}
	heading("1  What and why")
	para("The switcher changes its picture at once: a step replaces one frame of the carousel with the next, " +
		"the overlay appears and disappears in one frame, the selection frame of the grid jumps to the next tile. " +
		"The author wants these changes animated and version 1.0.0 released after a good animation.")
	para("The CPU draws a frame of E1 in 33–40 ms (research), about 25 frames per second with presenting — too " +
		"slow for a smooth animation, which at the 144 Hz of E1 needs a new frame every 6.94 ms. Author's decision: " +
		"on glx the GPU moves layers the CPU draws once; frames at rest stay the frames of cpu bit for bit, frames " +
		"in motion do not; cpu keeps changing at once.")
	heading("2  Decisions")
	para("Author, 2026-10-01: the approach above; the scope — the carousel step with retargeting, the appearance " +
		"and disappearance of the overlay, the selection frame of the grid; 150 ms with ease-out, a new frame " +
		"every vertical blank.")
	for _, s := range []string{
		"the background snapshots of the active window pause while the switcher is shown;",
		"the keyboard and modifier mappings are read once an activation, and again on MappingNotify;",
		"the loop does not wait for the frame at rest: a step ends on time with its scene at the target.",
	} {
		p.circle(x+8, y-7, 4, "#333333")
		y = p.para(s, x+28, y, right-x-28, 30, body, "#333333") + 8
	}
	y += 20
	heading("3  Behavior")
	para("The carousel step. The position of the selection, p, is continuous: a step sets a target, and p moves " +
		"from where it is to the target in 150 ms along ease-out cubic, 1 − (1 − u)³.")
}

// message is a message of the chat
type message struct {
	name, color, time string
	lines             []string
	code              string
	reactions         []string
}

// drawChat draws a chat on the channel of qws
func drawChat(p *painter, w, h float64) {
	k := p.k
	ui, small := k.ui(19), k.ui(16)

	// The rail of the workspaces
	p.rect(0, 0, 88, h, "#101116")
	for i, ws := range []struct{ letter, color string }{{"Q", "#7c5cff"}, {"G", "#2bb673"}, {"X", "#e5484d"}, {"+", "#2a2c35"}} {
		y := 20 + float64(i)*84
		p.rrect(12, y, 64, 64, 18, ws.color)
		p.textC(ws.letter, 44, y+44, k.strong(28), "#ffffff")
	}
	p.rrect(0, 26, 5, 52, 2, "#ffffff")

	// The channels
	p.rect(88, 0, 380, h, "#17181d")
	p.text("qws", 116, 50, k.strong(26), "#ffffff")
	p.rrect(108, 76, 340, 40, 8, "#22242c")
	p.text("Search", 128, 103, small, "#7c7f8c")
	y := 166.0
	p.text("CHANNELS", 116, y, k.strong(15), "#7c7f8c")
	y += 22
	for _, c := range []string{"general", "qws-dev", "releases", "measurements", "random", "design"} {
		fg := "#a6a8b3"
		if c == "qws-dev" {
			p.rrect(104, y, 348, 42, 8, "#2c2f3d")
			fg = "#ffffff"
		}
		p.text("#  "+c, 122, y+29, ui, fg)
		y += 46
	}
	y += 36
	p.text("DIRECT MESSAGES", 116, y, k.strong(15), "#7c7f8c")
	y += 22
	for i, d := range []struct{ name, color string }{{"Alice", "#f76b15"}, {"Bob", "#3e63dd"}, {"Carol", "#30a46c"}, {"Dan", "#8e4ec6"}, {"Erin", "#d6409f"}} {
		p.circle(136, y+21, 14, d.color)
		p.textC(d.name[:1], 136, y+28, k.strong(15), "#ffffff")
		status := "#3fb950"
		if i >= 3 {
			status = "#6e7681"
		}
		p.circle(147, y+31, 6, "#17181d")
		p.circle(147, y+31, 4, status)
		p.text(d.name, 162, y+29, ui, "#a6a8b3")
		y += 46
	}

	// The channel
	left := 468.0
	p.rect(left, 0, w-left, 76, "#1d1e24")
	end := p.text("# qws-dev", left+32, 48, k.strong(26), "#ffffff")
	p.text("the window switcher — specs, reviews, measurements", end+24, 47, small, "#8b8d98")
	for i, c := range []string{"#30a46c", "#3e63dd", "#f76b15"} {
		p.circle(w-160+float64(i)*26, 38, 16, "#1d1e24")
		p.circle(w-160+float64(i)*26, 38, 14, c)
	}
	p.text("12", w-70, 46, ui, "#a6a8b3")
	p.line(left, 76, w, 76, 1, "#2a2c35")

	y = 130
	p.line(left+40, y, w-40, y, 1, "#2a2c35")
	p.rrect((left+w)/2-50, y-16, 100, 32, 16, "#1d1e24")
	p.frame((left+w)/2-50, y-16, 100, 32, 16, 1, "#2a2c35")
	p.textC("Today", (left+w)/2, y+7, k.strong(16), "#a6a8b3")
	y += 40
	messages := []message{
		{"Bob", "#3e63dd", "09:41", []string{"Pushed the grid step: the selection frame slides over the tiles now,", "150 ms along ease-out, as the carousel."}, "", []string{"+1  4"}},
		{"Alice", "#f76b15", "09:44", []string{"Nice. Are the frames at rest still byte for byte those of cpu?"}, "", nil},
		{"Bob", "#3e63dd", "09:45", []string{"They are:"}, "$ framecmp frame.rgba 2520 1400 0x3c00007\n0 pixels differ", nil},
		{"Carol", "#30a46c", "10:02", []string{"q toggles the carousel and the grid for me, and the header shows what it does."}, "", []string{"✓  2"}},
		{"Dan", "#8e4ec6", "10:15", []string{"Live thumbnails on NVIDIA: the terminal keeps changing in its card while the switcher is open."}, "", nil},
		{"Erin", "#d6409f", "10:31", []string{"Can someone review spec 029? It is the README, with the pictures drawn by qws itself."}, "", []string{"+1  3", "eyes  1"}},
		{"Alice", "#f76b15", "10:38", []string{"On it."}, "", nil},
	}
	mono := k.code(19)
	for _, m := range messages {
		ax := left + 56
		p.circle(ax, y+26, 26, m.color)
		p.textC(m.name[:1], ax, y+35, k.strong(22), "#ffffff")
		tx := ax + 46
		end := p.text(m.name, tx, y+18, k.strong(20), "#ffffff")
		p.text(m.time, end+14, y+18, small, "#7c7f8c")
		ly := y + 52
		for _, l := range m.lines {
			p.text(l, tx, ly, ui, "#d6d7dc")
			ly += 32
		}
		if m.code != "" {
			p.rrect(tx, ly-18, 820, 84, 8, "#14151a")
			p.frame(tx, ly-18, 820, 84, 8, 1, "#2a2c35")
			for i, cl := range splitLines(m.code) {
				p.text(cl, tx+18, ly+14+float64(i)*30, mono, []string{"#9ece6a", "#d6d7dc"}[min(i, 1)])
			}
			ly += 84
		}
		if len(m.reactions) > 0 {
			rx := tx
			for _, r := range m.reactions {
				rw := p.width(r, small) + 28
				p.rrect(rx, ly-18, rw, 32, 16, "#25304a")
				p.frame(rx, ly-18, rw, 32, 16, 1, "#3e63dd")
				p.text(r, rx+14, ly+4, small, "#c8d3f5")
				rx += rw + 10
			}
			ly += 34
		}
		y = ly + 18
	}

	// The input
	p.rrect(left+32, h-104, w-left-64, 72, 12, "#262833")
	p.text("Message #qws-dev", left+60, h-60, ui, "#7c7f8c")
	p.circle(w-80, h-68, 18, "#3e63dd")
	p.SetHexColor("#ffffff")
	p.MoveTo(w-88, h-78)
	p.LineTo(w-70, h-68)
	p.LineTo(w-88, h-58)
	p.ClosePath()
	p.Fill()
}

// splitLines is s split at its newlines
func splitLines(s string) []string {
	var out []string
	start := 0
	for i := range len(s) {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// drawMail draws the inbox of a mail client
func drawMail(p *painter, w, h float64) {
	k := p.k
	ui, small := k.ui(18), k.ui(16)
	p.rect(0, 0, w, 64, "#f6f8fc")
	for i := range 3 {
		p.rect(24, 22+float64(i)*8, 24, 3, "#5f6368")
	}
	p.text("Mail", 70, 42, k.strong(24), "#202124")
	p.rrect(300, 12, w-420, 40, 20, "#e9eef6")
	p.text("Search mail", 336, 39, ui, "#5f6368")
	p.circle(w-48, 32, 18, "#7c5cff")
	p.textC("U", w-48, 39, k.strong(18), "#ffffff")

	// The folders
	p.rect(0, 64, 250, h-64, "#f6f8fc")
	p.rrect(16, 80, 160, 52, 16, "#c2e7ff")
	p.text("Compose", 56, 113, k.strong(18), "#001d35")
	y := 160.0
	for i, f := range []struct{ name, count string }{{"Inbox", "3"}, {"Starred", ""}, {"Snoozed", ""}, {"Sent", ""}, {"Drafts", "1"}, {"Archive", ""}, {"Spam", ""}, {"Trash", ""}} {
		face := ui
		if i == 0 {
			p.rrect(8, y, 234, 40, 20, "#d3e3fd")
			face = k.strong(18)
		}
		p.text(f.name, 40, y+27, face, "#202124")
		if f.count != "" {
			p.textR(f.count, 226, y+27, face, "#202124")
		}
		y += 44
	}
	y += 30
	p.text("Labels", 28, y, k.strong(17), "#202124")
	y += 20
	for _, l := range []struct{ name, color string }{{"qws", "#4a9eff"}, {"releases", "#30a46c"}, {"family", "#f76b15"}} {
		p.circle(40, y+20, 7, l.color)
		p.text(l.name, 60, y+27, ui, "#202124")
		y += 44
	}

	// The list
	left := 250.0
	p.rect(left, 64, w-left, h-64, "#ffffff")
	mails := []struct {
		from, subject, snippet, date string
		unread                       bool
	}{
		{"Carol", "Spec 029: the README — review", "I read the draft; every figure links to the spec that measured it. Two small things", "10:31", true},
		{"CI", "test: passed — issue/qws#85-readme", "go build, gofmt, go vet, check-specs and go test passed", "10:20", true},
		{"Bob", "The grid step", "Tried it on the laptop: the frame slides from tile to tile, smooth at 60 Hz", "09:58", true},
		{"Releases", "qws v1.4.1", "Release notes: the mouse finds the tile drawn under it in the grid", "Oct 3", false},
		{"Dan", "Lunch on Friday?", "The new place on the corner, 12:30?", "Oct 2", false},
		{"Erin", "Thumbnails on NVIDIA", "The snapshot waits for the X server now; no stale pictures since", "Oct 1", false},
		{"Alice", "Re: frame pass cost", "With the RENDER chain kept, a pass from the frame is one round trip", "Sep 30", false},
		{"Frank", "Photos from the hike", "Uploaded 24 photos; lake.jpg is my favourite", "Sep 27", false},
		{"Carol", "Re: spec 026 — layout keys", "q for the layout reads well in the header", "Sep 25", false},
		{"Weekly digest", "This week in window managers", "Compositors, input methods and a few new releases", "Sep 24", false},
		{"Bob", "XIDs", "The XC-MISC ranges explain the reuse; patch coming", "Sep 22", false},
		{"Dan", "Board games", "Saturday at mine, bring snacks", "Sep 20", false},
	}
	y = 64
	for _, m := range mails {
		bg := "#ffffff"
		if !m.unread {
			bg = "#f8fafd"
		}
		p.rect(left, y, w-left, 104, bg)
		p.line(left, y+104, w, y+104, 1, "#eceff1")
		face, fg := ui, "#5f6368"
		if m.unread {
			face, fg = k.strong(18), "#202124"
			p.circle(left+22, y+34, 6, "#1a73e8")
		}
		p.text(m.from, left+44, y+40, face, fg)
		p.textR(m.date, w-24, y+40, face, fg)
		p.text(m.subject, left+44, y+68, face, "#202124")
		p.text(clip(p, m.snippet, w-left-80, small), left+44, y+94, small, "#80868b")
		y += 105
	}
}

// clip is s cut with an ellipsis to fit the width in the face
func clip(p *painter, s string, width float64, f font.Face) string {
	if p.width(s, f) <= width {
		return s
	}
	r := []rune(s)
	for n := len(r) - 1; n > 0; n-- {
		if t := string(r[:n]) + "…"; p.width(t, f) <= width {
			return t
		}
	}
	return "…"
}
