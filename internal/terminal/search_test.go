package terminal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/thakurankit7/shellter/internal/input"
	"github.com/thakurankit7/shellter/internal/render"
	"github.com/thakurankit7/shellter/internal/screen"
)

// A fresh search selects the newest match - the bottom of the pane is where
// you are - and Find Next walks up through older ones, wrapping round.
func TestFindWalksMatchesNewestFirst(t *testing.T) {
	g := newTestGUI(3, 20)
	p := g.current()
	p.scr = screen.NewWithScrollback(3, 20, 100)
	g.feed("error one\r\nfine\r\nERROR two\r\nfine\r\nan error three\r\n$ ")

	g.find("error", findFresh)
	if got := matchText(g); got != "error" || p.sel.anchor.Col != 3 {
		t.Fatalf("fresh search selected %q at col %d, want the newest, in 'an error three'", got, p.sel.anchor.Col)
	}
	newest := p.sel.anchor.Line

	g.find("error", findOlder)
	if got := matchText(g); got != "ERROR" {
		t.Fatalf("Find Next selected %q, want the case-insensitive match before it", got)
	}
	g.find("error", findOlder)
	g.find("error", findOlder) // past the oldest: wraps to the newest
	if p.sel.anchor.Line != newest {
		t.Fatal("Find Next did not wrap round to the newest match")
	}
	g.find("error", findNewer) // and back the other way wraps to the oldest
	if got := matchText(g); got != "error" || p.sel.anchor.Col != 0 {
		t.Fatalf("Find Previous from the newest selected %q at col %d, want the oldest", got, p.sel.anchor.Col)
	}

	// The match that is selected is scrolled into view.
	if top := p.scr.TopLine(); p.sel.anchor.Line < top || p.sel.anchor.Line >= top+3 {
		t.Fatalf("match on line %d is not on screen (top %d)", p.sel.anchor.Line, top)
	}

	g.find("nowhere", findFresh)
	if p.sel.active {
		t.Fatal("a search with no matches left a selection")
	}
}

// A wide character is one character to a search, and the match covers both of
// its cells.
func TestFindWideCharacters(t *testing.T) {
	g := newTestGUI(2, 20)
	g.feed("ab漢字cd")
	g.find("漢字", findFresh)
	p := g.current()
	if p.sel.anchor.Col != 2 || p.sel.head.Col != 6 {
		t.Fatalf("match spans cols %d-%d, want 2-6", p.sel.anchor.Col, p.sel.head.Col)
	}
}

func matchText(g *GUI) string {
	p := g.current()
	return p.sel.text(p.scr)
}

func TestTokenSpan(t *testing.T) {
	line := []rune(`see (https://example.com/a?b=1). or "main.go:12:3"`)
	cols := make([]int, len(line))
	for i := range cols {
		cols[i] = i
	}
	cases := map[int]string{
		10: "https://example.com/a?b=1",
		40: "main.go:12:3",
		3:  "", // the space after "see"
	}
	for col, want := range cases {
		if got, _, _ := tokenSpan(line, cols, col); got != want {
			t.Errorf("tokenSpan(col %d) = %q, want %q", col, got, want)
		}
	}
	// The span is the trimmed token's cells: "(https://...)." covers only
	// the URL.
	if _, from, to := tokenSpan(line, cols, 10); from != 5 || to != 30 {
		t.Errorf("URL spans cols %d-%d, want 5-30", from, to)
	}
}

func TestLinkTarget(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), nil, 0o644)

	cases := []struct {
		token, want string
		ok          bool
	}{
		{"https://example.com", "https://example.com", true},
		{"www.example.com", "https://www.example.com", true},
		{"main.go", filepath.Join(dir, "main.go"), true},
		{"main.go:42:7", filepath.Join(dir, "main.go"), true},
		{filepath.Join(dir, "main.go"), filepath.Join(dir, "main.go"), true},
		{"missing.go", "", false},
		// A file:// URL is a path, and gets a path's checks.
		{"file://" + filepath.Join(dir, "main.go"), filepath.Join(dir, "main.go"), true},
		{"file://localhost" + filepath.Join(dir, "main.go"), filepath.Join(dir, "main.go"), true},
		{"file://" + filepath.Join(dir, "missing.go"), "", false},
		{"file://elsewhere" + filepath.Join(dir, "main.go"), "", false},
		{"hello", "", false},
	}
	for _, c := range cases {
		got, ok := linkTarget(c.token, dir)
		if ok != c.ok || got != c.want {
			t.Errorf("linkTarget(%q) = %q, %v; want %q, %v", c.token, got, ok, c.want, c.ok)
		}
	}
}

// Anything that would run rather than open is shown in the Finder instead.
func TestRunnableFilesAreNotOpened(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "deploy")
	os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755)
	text := filepath.Join(dir, "notes.txt")
	os.WriteFile(text, nil, 0o644)

	if !runnable(script) {
		t.Error("an executable file was not treated as runnable")
	}
	if !runnable(filepath.Join(dir, "Evil.command")) {
		t.Error("a .command file was not treated as runnable")
	}
	if runnable(text) {
		t.Error("a text file was treated as runnable")
	}
	// The same file as a file:// URL must not slip past as "just a URL".
	if target, ok := linkTarget("file://"+script, dir); !ok || !runnable(target) {
		t.Errorf("file:// to an executable resolved to %q, %v, and would be opened", target, ok)
	}
}

// Hovering over a link underlines exactly its cells, in the theme's blue; with
// Command held, the brighter blue; moving off it takes the underline away.
func TestHoverUnderlinesALink(t *testing.T) {
	g := newTestGUI(3, 40)
	g.feed("see https://example.com now")

	g.hover(0, 8, 0)
	if g.hovered.pane == nil || g.hovered.from != 4 || g.hovered.to != 23 {
		t.Fatalf("hovered = %+v, want cols 4-23", g.hovered)
	}
	f := g.frame()
	for c := 0; c < 27; c++ {
		underlined := f.CellAt(0, c).Flags&render.FlagUnderline != 0
		if underlined != (c >= 4 && c < 23) {
			t.Fatalf("col %d underlined = %v", c, underlined)
		}
	}
	if f.CellAt(0, 4).FG != g.Palette.ANSI[4] {
		t.Fatal("the link is not in the theme's blue")
	}

	g.hover(0, 8, input.ModCommand)
	if !g.hovered.command || g.frame().CellAt(0, 4).FG != g.Palette.ANSI[12] {
		t.Fatal("holding Command did not mark the link as clickable")
	}

	g.hover(0, 1, 0) // "see": a word, not a link
	if g.hovered.pane != nil || g.frame().CellAt(0, 4).Flags&render.FlagUnderline != 0 {
		t.Fatal("moving off the link left it underlined")
	}

	// Empty space past the end of a line is not the last word on it: after a
	// prompt's "~", which is a real directory, it must not become a link.
	g.feed("\r\n~")
	g.hover(1, 20, input.ModCommand)
	if g.hovered.pane != nil {
		t.Fatal("empty space after \"~\" was taken for a link")
	}
	if g.openAt(g.current(), 1, 20) {
		t.Fatal("Cmd+click in empty space after \"~\" would have opened it")
	}

	g.hover(0, 8, 0)
	g.key(input.Key{Chars: []rune{'x'}, Base: 'x'})
	if g.hovered.pane != nil {
		t.Fatal("typing did not clear the hover")
	}
}

// With Aa on, case counts.
func TestFindMatchCase(t *testing.T) {
	g := newTestGUI(5, 20)
	g.feed("Error\r\nerror\r\nERROR\r\n$ ")
	for _, c := range []struct {
		matchCase bool
		status    string
	}{{false, "3 of 3"}, {true, "1 of 1"}} {
		g.findCase = c.matchCase
		g.find("error", findFresh)
		if got := matchText(g); got != "error" && c.matchCase {
			t.Errorf("match case found %q", got)
		}
		p := g.current()
		p.mu.Lock()
		n := len(findIn(p.scr, "error", c.matchCase, p.scr.FirstLine(), p.scr.TotalLines()))
		p.mu.Unlock()
		if want := map[bool]int{false: 3, true: 1}[c.matchCase]; n != want {
			t.Errorf("matchCase %v: %d matches, want %d", c.matchCase, n, want)
		}
	}
}

// While the bar is open every match on screen is tinted, the current one is
// the selection, and closing the bar takes the tint away.
func TestFindTintsEveryMatch(t *testing.T) {
	g := newTestGUI(4, 20)
	g.feed("cat dog cat\r\nbird\r\ncat\r\n$ ")
	g.find("cat", findFresh) // the newest, on row 2, is the selection

	f := g.frame()
	tint := mix(g.Palette.ANSI[3], g.Palette.Background, 0.55)
	for _, c := range []struct{ row, col int }{{0, 0}, {0, 2}, {0, 8}, {0, 10}} {
		if bg := f.CellAt(c.row, c.col).BG; bg != tint {
			t.Errorf("row %d col %d: background %06x, want the match tint", c.row, c.col, bg)
		}
	}
	if bg := f.CellAt(0, 4).BG; bg == tint {
		t.Error("'dog' was tinted")
	}
	if bg := f.CellAt(2, 0).BG; bg != g.Palette.Selection {
		t.Errorf("the current match is %06x, want the selection colour", bg)
	}

	g.find("", findDone)
	if bg := g.frame().CellAt(0, 0).BG; bg == tint {
		t.Error("the tint stayed after the bar closed")
	}
}
