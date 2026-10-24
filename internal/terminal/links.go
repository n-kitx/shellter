package terminal

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/n-kitx/shellter/internal/input"
	"github.com/n-kitx/shellter/internal/render"
)

// Cmd+click: opening what the pointer is on, if it is a URL or a file.
//
// The text under the pointer is read back from the screen and the token
// around the click cut out of it. A URL opens in the browser. A path - as
// ls, git status or a compiler error prints one - is resolved against the
// directory the pane's shell is in, and opens in whatever the Mac opens that
// kind of file with; "main.go:42:7" is main.go.

// urlPattern is what counts as a URL: a scheme a browser or mail client
// handles, or a bare www. address.
var urlPattern = regexp.MustCompile(`^(?i)(https?://|ftp://|mailto:|www\.)\S+$`)

// fileURL is a file:// URL. It is not opened as a URL: it names a file on
// this Mac, and goes through the same checks as a path does - above all
// runnable, since `open file:///Applications/Calculator.app` would launch it.
var fileURL = regexp.MustCompile(`^(?i)file://`)

// lineSuffix is the ":line" or ":line:col" a compiler appends to a path.
var lineSuffix = regexp.MustCompile(`(:\d+)+$`)

// openAt opens the link at a cell of a pane, and reports whether there was
// one - a Cmd+click on plain text falls through to whatever a click does.
func (g *GUI) openAt(p *pane, row, col int) bool {
	p.mu.Lock()
	line := p.scr.TopLine() + int64(row)
	runes, cols := p.scr.LineChars(line, nil, nil)
	p.mu.Unlock()

	token, _, _ := tokenSpan(runes, cols, col)
	if token == "" {
		return false
	}
	target, ok := linkTarget(token, p.directory())
	if !ok {
		return false
	}
	if runnable(target) {
		_ = render.Reveal(target)
	} else {
		_ = render.Open(target)
	}
	return true
}

// tokenSpan is the run of non-space characters covering column col, with the
// punctuation that surrounds a link in prose - quotes, brackets, a full stop
// at the end of a sentence - trimmed off, and the columns it covers: from the
// first cell to one past the last. "" when col is not on a word.
func tokenSpan(runes []rune, cols []int, col int) (token string, from, to int) {
	at := -1
	for i, c := range cols {
		if c <= col {
			at = i
		}
	}
	// The character found must actually cover col: past the end of a line,
	// the last character before the pointer is not under it.
	if at < 0 || isSpace(runes[at]) || col >= cols[at]+runeWidth(runes[at]) {
		return "", 0, 0
	}
	start, end := at, at+1
	for start > 0 && !isSpace(runes[start-1]) {
		start--
	}
	for end < len(runes) && !isSpace(runes[end]) {
		end++
	}
	const surround = "\"'`<>()[]{},;.!?"
	for start < end && strings.ContainsRune(surround, runes[start]) {
		start++
	}
	for end > start && strings.ContainsRune(surround, runes[end-1]) {
		end--
	}
	if start == end {
		return "", 0, 0
	}
	return string(runes[start:end]), cols[start], cols[end-1] + runeWidth(runes[end-1])
}

// linkHover is the link under the pointer: where it is, so it can be drawn
// underlined, and whether Command is held, which is when a click will open it.
type linkHover struct {
	pane     *pane
	line     int64
	from, to int
	token    string
	command  bool
}

// hover follows the pointer across the grid, underlining a link when it is
// over one. row < 0 means the pointer left the window.
//
// Only the token under the pointer is looked at, and the file system is asked
// about a path only when the pointer moves onto a different token - so moving
// the mouse across a screen of text costs a string scan per cell, not a stat.
func (g *GUI) hover(row, col int, mods input.Modifiers) {
	next := linkHover{}
	if row >= 0 {
		if p, local := g.paneAt(row, col); p != nil {
			p.mu.Lock()
			line := p.scr.TopLine() + int64(local.Row)
			runes, cols := p.scr.LineChars(line, g.hoverRunes, g.hoverCols)
			p.mu.Unlock()
			g.hoverRunes, g.hoverCols = runes, cols

			token, from, to := tokenSpan(runes, cols, local.Col)
			same := g.hovered.pane == p && g.hovered.line == line &&
				g.hovered.from == from && g.hovered.token == token
			switch {
			case token == "":
			case same:
				next = g.hovered
			default:
				if _, ok := linkTarget(token, p.directory()); ok {
					next = linkHover{pane: p, line: line, from: from, to: to, token: token}
				}
			}
			next.command = next.pane != nil && mods.Has(input.ModCommand)
		}
	}
	if next == g.hovered {
		return
	}
	g.hovered = next
	render.SetLinkHover(next.pane != nil, next.command)
	render.Redraw()
}

// markLink draws the hovered link in a frame: underlined, in the theme's
// blue, and its brighter blue while Command is held and a click would open it.
func (g *GUI) markLink(p *pane, f render.Frame, top int64) {
	h := g.hovered
	if h.pane != p {
		return
	}
	row := int(h.line - top)
	if row < 0 || row >= f.Rows {
		return
	}
	colour := g.Palette.ANSI[4]
	if h.command {
		colour = g.Palette.ANSI[12]
	}
	for c := h.from; c < h.to && c < f.Cols; c++ {
		cell := &f.Cells[row*f.Cols+c]
		cell.Flags |= render.FlagUnderline
		cell.Flags &^= render.FlagUnderlineColor // the link's blue, not the program's
		cell.FG = colour
	}
}

func isSpace(r rune) bool { return r == ' ' || r == '\t' || r == 0 }

// linkTarget decides whether a token is something to open, and what to hand
// to `open`: a URL as it stands, or a path made absolute. A path that does not
// exist is not a link - most words would otherwise be one.
func linkTarget(token, dir string) (string, bool) {
	if urlPattern.MatchString(token) {
		if strings.HasPrefix(strings.ToLower(token), "www.") {
			token = "https://" + token
		}
		return token, true
	}

	path := lineSuffix.ReplaceAllString(token, "")
	if fileURL.MatchString(path) {
		u, err := url.Parse(path)
		if err != nil || (u.Host != "" && u.Host != "localhost") || !filepath.IsAbs(u.Path) {
			return "", false
		}
		path = u.Path
	}
	switch {
	case path == "~" || strings.HasPrefix(path, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", false
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	case !filepath.IsAbs(path):
		if dir == "" {
			return "", false
		}
		path = filepath.Join(dir, path)
	}
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	return path, true
}

// runnable reports whether opening a path would run something: an executable
// file, or a bundle or script the Finder launches rather than displays. Those
// are shown in the Finder instead. A click is not consent to execute, and
// the path on screen may have been printed by anything, including a program
// on the far side of ssh.
func runnable(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".app", ".command", ".tool", ".terminal", ".pkg", ".mpkg", ".workflow",
		".scpt", ".applescript", ".jar", ".sh", ".zsh", ".bash":
		return true
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
}
