package main

import (
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/image/font"
)

// The dark palette of the terminals and the editor
const (
	tnBg      = "#1a1b26"
	tnBgDark  = "#16161e"
	tnBgHi    = "#292e42"
	tnFg      = "#c0caf5"
	tnDim     = "#565f89"
	tnGutter  = "#3b4261"
	tnBlue    = "#7aa2f7"
	tnCyan    = "#7dcfff"
	tnGreen   = "#9ece6a"
	tnMagenta = "#bb9af7"
	tnOrange  = "#ff9e64"
	tnRed     = "#f7768e"
	tnYellow  = "#e0af68"
	tnTeal    = "#2ac3de"
	tnOp      = "#89ddff"
)

// The text of the terminals and the editor
const (
	monoSize = 22.0
	lineH    = 31.0
)

// drawEditor draws nvim showing the end of the source, the cursor on its
// last statement
func drawEditor(p *painter, w, h float64, source, path string) {
	mono := p.k.code(monoSize)
	col := p.width("M", mono)
	ui := p.k.ui(18)

	// The tab line
	p.rect(0, 0, w, 40, tnBgDark)
	x := 0.0
	name := path[strings.LastIndex(path, "/")+1:]
	for i, tab := range []string{name, "animation.go", "hover.go"} {
		tw := p.width(tab, ui) + 44
		fg := tnDim
		if i == 0 {
			p.rect(x, 0, tw, 40, tnBg)
			p.rect(x, 0, tw, 3, tnBlue)
			fg = tnFg
		}
		p.text(tab, x+22, 27, ui, fg)
		x += tw
	}

	lines := strings.Split(strings.TrimRight(source, "\n"), "\n")
	statusY := h - 2*38
	rows := int((statusY - 48) / lineH)
	first := max(1, len(lines)-rows+2)
	cursor := len(lines) - 1
	gutter := 5*col + 20
	for i := range rows {
		n := first + i
		y := 48 + float64(i)*lineH
		base := y + 23
		if n > len(lines) {
			p.text("~", gutter, base, mono, tnGutter)
			continue
		}
		num := tnGutter
		if n == cursor {
			p.rect(0, y, w, lineH, tnBgHi)
			num = tnOrange
		}
		p.textR(fmt.Sprint(n), gutter-20, base, mono, num)
		p.spans(highlightGo(expandTabs(lines[n-1], 4)), gutter, base, col, mono)
		if n == cursor {
			// The block cursor on the first v of the expression
			at := strings.Index(expandTabs(lines[n-1], 4), "v")
			if at >= 0 {
				cx := gutter + float64(at)*col
				p.rect(cx, y+2, col, lineH-4, tnFg)
				p.text("v", cx, base, mono, tnBg)
			}
		}
	}

	// The status line and the command line
	p.rect(0, statusY, w, 38, tnBgDark)
	bold := p.k.strong(17)
	end := blockText(p, " NORMAL ", 0, statusY, 38, bold, tnBlue, tnBg)
	end = blockText(p, "  develop ", end, statusY, 38, ui, tnGutter, tnBlue)
	p.text(path, end+16, statusY+26, ui, tnFg)
	right := w
	for _, s := range []string{fmt.Sprintf(" %d:%d ", cursor, 9), " Bot ", " utf-8 ", " go "} {
		tw := p.width(s, ui)
		bg, fg := tnGutter, tnFg
		if s == " Bot " {
			bg, fg = tnBlue, tnBg
		}
		right -= tw + 16
		blockText(p, s, right, statusY, 38, ui, bg, fg)
	}
	p.text(fmt.Sprintf("\"%s\" %dL, %dB written", path, len(lines), len(source)), 12, h-12, mono, tnFg)
}

// blockText draws s on a block of colour bg from x and returns its end
func blockText(p *painter, s string, x, y, h float64, f font.Face, bg, fg string) float64 {
	tw := p.width(s, f) + 16
	p.rect(x, y, tw, h, bg)
	p.text(s, x+8, y+h-12, f, fg)
	return x + tw
}

// expandTabs expands the tabs of a line to stops of n columns
func expandTabs(line string, n int) string {
	var b strings.Builder
	column := 0
	for _, r := range line {
		if r == '\t' {
			k := n - column%n
			b.WriteString(strings.Repeat(" ", k))
			column += k
			continue
		}
		b.WriteRune(r)
		column++
	}
	return b.String()
}

var (
	goKeywords = map[string]bool{
		"break": true, "case": true, "chan": true, "const": true, "continue": true, "default": true,
		"defer": true, "else": true, "for": true, "func": true, "go": true, "if": true, "import": true,
		"interface": true, "map": true, "package": true, "range": true, "return": true, "select": true,
		"struct": true, "switch": true, "type": true, "var": true,
	}
	goTypes = map[string]bool{
		"bool": true, "byte": true, "error": true, "float32": true, "float64": true, "int": true,
		"int64": true, "rune": true, "string": true, "uint32": true, "any": true,
	}
)

// highlightGo colours a line of Go: comments, strings, keywords, types,
// calls, numbers and operators
func highlightGo(line string) []span {
	r := []rune(line)
	var out []span
	add := func(s, c string) { out = append(out, span{s, c}) }
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case c == '/' && i+1 < len(r) && r[i+1] == '/':
			add(string(r[i:]), tnDim)
			return out
		case c == '"' || c == '`' || c == '\'':
			j := i + 1
			for j < len(r) && r[j] != c {
				if r[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(r))
			add(string(r[i:j]), tnGreen)
			i = j
			continue
		case unicode.IsLetter(c) || c == '_':
			j := i
			for j < len(r) && (unicode.IsLetter(r[j]) || unicode.IsDigit(r[j]) || r[j] == '_') {
				j++
			}
			word := string(r[i:j])
			color := tnFg
			switch {
			case goKeywords[word]:
				color = tnMagenta
			case goTypes[word] || unicode.IsUpper(r[i]) && i > 0 && r[i-1] == '.':
				color = tnTeal
			case j < len(r) && r[j] == '(':
				color = tnBlue
			}
			add(word, color)
			i = j
			continue
		case unicode.IsDigit(c):
			j := i
			for j < len(r) && (unicode.IsDigit(r[j]) || r[j] == '.') {
				j++
			}
			add(string(r[i:j]), tnOrange)
			i = j
			continue
		case strings.ContainsRune("+-*/=<>!&|:", c):
			add(string(c), tnOp)
		case strings.ContainsRune("(){}[],.;", c):
			add(string(c), "#a9b1d6")
		default:
			add(string(c), tnFg)
		}
		i++
	}
	return out
}

// prompt is the prompt of the shell with a command after it
func prompt(cmd string) []span {
	return []span{{"user@devbox", tnGreen}, {":", tnFg}, {"~/src/qws", tnBlue}, {"$ ", tnFg}, {cmd, tnFg}}
}

// drawTerminal draws a terminal with the lines, a block cursor after the last
// when cursor, and the status bar of tmux at the bottom
func drawTerminal(p *painter, w, h float64, lines [][]span, cursor bool, window string) {
	mono := p.k.code(monoSize)
	col := p.width("M", mono)
	y := 18.0
	for i, l := range lines {
		base := y + 23
		p.spans(l, 18, base, col, mono)
		if cursor && i == len(lines)-1 {
			n := 0
			for _, s := range l {
				n += len([]rune(s.text))
			}
			p.rect(18+float64(n)*col, y+2, col, lineH-4, tnFg)
		}
		y += lineH
	}
	// tmux
	ui := p.k.code(19)
	p.rect(0, h-36, w, 36, tnGreen)
	p.text("[qws] 0:"+window+"* 1:nvim-", 12, h-11, ui, tnBg)
	p.textR("devbox  10:42 05-Oct-26", w-12, h-11, ui, tnBg)
}

// drawGoTest draws a terminal after make build and go test ./...
func drawGoTest(p *painter, w, h float64) {
	const mod = "github.com/almaz-uno/qws/"
	var lines [][]span
	lines = append(lines,
		prompt("make build"),
		[]span{{"→ Building qws v1.4.1...", tnFg}},
		[]span{{`go build -trimpath -ldflags "-s -w -X main.version=v1.4.1" -o qws ./cmd/qws`, tnDim}},
		[]span{{"✓", tnGreen}, {" Build completed: qws", tnFg}},
		prompt("go vet ./..."),
		prompt("go test ./..."),
	)
	tests := []struct{ pkg, time string }{
		{"cmd/qws", "0.048s"},
		{"cmd/relnotes", ""},
		{"console/capture", ""},
		{"console/framecmp", ""},
		{"console/glxoverlay", ""},
		{"console/readme-demo", ""},
		{"internal/config", "0.021s"},
		{"internal/relnotes", "0.007s"},
		{"pkg/carousel", "3.912s"},
		{"pkg/composite", ""},
		{"pkg/focus", ""},
		{"pkg/glx", "0.264s"},
		{"pkg/keygrab", ""},
		{"pkg/mru", "0.004s"},
		{"pkg/snapshot", "6.731s"},
		{"pkg/ui", "1.385s"},
		{"pkg/x11", "0.418s"},
	}
	for _, t := range tests {
		if t.time == "" {
			lines = append(lines, []span{{"?   \t", tnYellow}, {mod + t.pkg, tnDim}, {"\t[no test files]", tnDim}})
			continue
		}
		lines = append(lines, []span{{"ok", tnGreen}, {"  \t", tnFg}, {mod + t.pkg, tnFg}, {"\t" + t.time, tnDim}})
	}
	lines = append(lines, prompt(""))
	for _, l := range lines {
		for i := range l {
			l[i].text = expandTabsFrom(l, i)
		}
	}
	drawTerminal(p, w, h, lines, true, "zsh")
}

// expandTabsFrom is the text of the piece i of the line with its tabs
// expanded to stops of 8 columns, counted from the start of the line
func expandTabsFrom(l []span, i int) string {
	column := 0
	for _, s := range l[:i] {
		column += len([]rune(s.text))
	}
	var b strings.Builder
	for _, r := range l[i].text {
		if r == '\t' {
			k := 8 - column%8
			b.WriteString(strings.Repeat(" ", k))
			column += k
			continue
		}
		b.WriteRune(r)
		column++
	}
	return b.String()
}

// gitLog is the start of git log --graph --oneline --decorate of the
// repository at the time of spec 029, the gitmoji of the subjects left out
var gitLog = []string{
	"* 10307d4 (HEAD -> issue/qws#85-readme, origin/issue/qws#85-readme) docs: spec 029 — the README",
	"*   c3c598e (tag: v1.4.1, origin/develop, develop) Merge pull request #82 from almaz-uno/issue/qws#80-release-1.4.1",
	"|\\  ",
	"| * 692db20 docs: release notes of 1.4.1",
	"* |   7ffcbbd Merge pull request #81 from almaz-uno/issue/qws#80-grid-mouse",
	"|\\ \\  ",
	"| |/  ",
	"|/|   ",
	"| * 3887289 docs: spec 027 — results",
	"| * ba30dab fix: the mouse finds the tile drawn under it in the grid",
	"| * a94857f docs: spec 027 — the mouse in the grid",
	"|/  ",
	"*   53dc14c (tag: v1.4.0) Merge pull request #77 from almaz-uno/issue/qws#58-release-1.4.0",
	"|\\  ",
	"| * 1e760d0 docs: release notes of 1.4.0 — the layout keys",
	"| * 1150a24 docs: release notes of 1.4.0",
	"* |   1a91cef Merge pull request #79 from almaz-uno/issue/qws#78-layout-keys",
	"|\\ \\  ",
	"| |/  ",
	"|/|   ",
	"| * 7c60885 docs: spec 026 — results",
	"| * 5834543 feat: a key to toggle the layouts, its hint, Up and Down in the grid",
	"| * 11d5f66 docs: spec 026 — layout keys",
	"|/  ",
	"*   fd00d27 Merge pull request #60 from almaz-uno/issue/qws#58-live-thumbnails",
	"|\\  ",
	"| *   52fb918 Merge pull request #69 from almaz-uno/issue/qws#66-frame-pass-cost",
	"| |\\  ",
	"| | * 5bf8612 docs: spec 023 — results",
	"| * | 71ab94d docs: spec 020 — results",
	"* | |   7c76a35 (tag: v1.3.4) Merge pull request #76 from almaz-uno/issue/qws#70-release-1.3.4",
	"|\\ \\ \\  ",
	"| * | | 1a19a95 docs: release notes of 1.3.4",
	"* | | |   88a642a Merge pull request #75 from almaz-uno/issue/qws#74-xids-ahead",
	"|\\ \\ \\ \\  ",
	"| * | | | e442e55 docs: spec 024 — XIDs handed out ahead",
	"| * | | | 10826e3 fix: XIDs given ahead left out of the ranges of XC-MISC",
	"|/ / / /  ",
	"* | | |   4415462 Merge pull request #73 from almaz-uno/issue/qws#70-xid-reuse",
	"|\\ \\ \\ \\  ",
	"| * | | | c647afe fix: XIDs of every connection reused through XC-MISC",
}

// graphColors are the colours of the columns of the graph
var graphColors = []string{tnRed, tnGreen, tnYellow, tnBlue, tnMagenta, tnCyan}

// colourLog colours a line of the log: the graph by column, the hash, the
// references and the subject
func colourLog(line string) []span {
	var out []span
	i := 0
	for i < len(line) && strings.ContainsRune("*|/\\ _", rune(line[i])) {
		c := line[i]
		switch c {
		case ' ':
			out = append(out, span{" ", tnFg})
		case '*':
			out = append(out, span{"*", tnFg})
		default:
			out = append(out, span{string(c), graphColors[(i/2)%len(graphColors)]})
		}
		i++
	}
	rest := line[i:]
	if rest == "" {
		return out
	}
	hash, rest, _ := strings.Cut(rest, " ")
	out = append(out, span{hash + " ", tnYellow})
	if strings.HasPrefix(rest, "(") {
		refs, subject, _ := strings.Cut(rest[1:], ") ")
		out = append(out, span{"(", tnYellow})
		for j, ref := range strings.Split(refs, ", ") {
			if j > 0 {
				out = append(out, span{", ", tnYellow})
			}
			switch {
			case strings.HasPrefix(ref, "HEAD -> "):
				out = append(out, span{"HEAD -> ", tnCyan}, span{ref[len("HEAD -> "):], tnGreen})
			case strings.HasPrefix(ref, "tag: "):
				out = append(out, span{ref, tnYellow})
			case strings.HasPrefix(ref, "origin/"):
				out = append(out, span{ref, tnRed})
			default:
				out = append(out, span{ref, tnGreen})
			}
		}
		out = append(out, span{") ", tnYellow})
		rest = subject
	}
	return append(out, span{rest, tnFg})
}

// drawGitLog draws a terminal paging the log of the repository
func drawGitLog(p *painter, w, h float64) {
	lines := [][]span{prompt("git log --graph --oneline --decorate")}
	rows := int((h - 36 - 18) / lineH)
	for _, l := range gitLog {
		if len(lines) >= rows-1 {
			break
		}
		lines = append(lines, colourLog(l))
	}
	lines = append(lines, []span{{":", tnFg}})
	drawTerminal(p, w, h, lines, true, "git")
}
