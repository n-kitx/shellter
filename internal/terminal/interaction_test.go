package terminal

import (
	"strings"
	"testing"
	"time"

	"github.com/thakurankit7/shellter/internal/input"
	"github.com/thakurankit7/shellter/internal/render"
)

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/me/notes.txt":       "/Users/me/notes.txt",
		"/Users/me/My Files/a b":    `/Users/me/My\ Files/a\ b`,
		"/tmp/it's (1).png":         `/tmp/it\'s\ \(1\).png`,
		"/tmp/$HOME&*?[x]":          `/tmp/\$HOME\&\*\?\[x\]`,
		"/tmp/=cmd":                 `/tmp/\=cmd`,
		"/Users/me/Résumé été.pdf":  `/Users/me/Résumé\ été.pdf`,
		"/tmp/a-b_c+d,e:f@g%h":      "/tmp/a-b_c+d,e:f@g%h",
		"/tmp/back\\slash\"quote\"": `/tmp/back\\slash\"quote\"`,
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// Dropped files are typed as quoted paths, a space after each.
func TestDropTypesQuotedPaths(t *testing.T) {
	g := newTestGUI(5, 40)
	out := g.sink()
	g.drop(0, 0, []string{"/a b/c.txt", "/x"}, "")
	if got := out.String(); got != `/a\ b/c.txt /x ` {
		t.Fatalf("typed %q", got)
	}

	// A program that asked for bracketed paste is told it was pasted; text is
	// pasted as it is.
	out.Reset()
	feedPane(g.current(), "\x1b[?2004h")
	g.drop(0, 0, nil, "echo hi")
	if got := out.String(); got != "\x1b[200~echo hi\x1b[201~" {
		t.Fatalf("typed %q", got)
	}
}

// A drop lands in the pane under it, which takes the focus.
func TestDropFocusesThePaneUnderIt(t *testing.T) {
	g := newTestGUI(10, 41)
	left := g.current()
	right := g.addPane(g.currentTab(), 10, 20, splitBeside)
	g.focusOn(left)
	g.sink()
	g.drop(2, 30, []string{"/x"}, "")
	if g.current() != right {
		t.Fatal("the pane dropped on did not take the focus")
	}
}

// history fills a pane with numbered lines, enough to scroll.
func history(p *pane, n int) {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString("line ")
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString("\r\n")
	}
	feedPane(p, b.String())
}

// Dragging a selection above the pane scrolls back into history, and the
// selection follows.
func TestDraggingPastTheTopScrollsBack(t *testing.T) {
	g := newTestGUI(5, 20)
	p := g.current()
	history(p, 30)

	g.mouse(render.Mouse{Action: mousePress, Row: 2, Col: 0, Clicks: 1})
	for i := 0; i < 3; i++ { // the window repeats a drag held still
		g.mouse(render.Mouse{Action: mouseMove, Row: -1, Col: 3})
	}
	if off := p.scr.ScrollOffset(); off != 3 {
		t.Fatalf("scrolled back %d lines, want 3", off)
	}
	from, _ := p.sel.bounds()
	if from.Line != p.scr.TopLine() {
		t.Fatalf("selection starts at line %d, want the top of the view %d", from.Line, p.scr.TopLine())
	}

	// Further out is faster, and below the bottom goes the other way.
	g.mouse(render.Mouse{Action: mouseMove, Row: -10, Col: 3})
	if off := p.scr.ScrollOffset(); off != 3+maxEdgeScroll {
		t.Fatalf("scrolled back %d lines, want %d", off, 3+maxEdgeScroll)
	}
	g.mouse(render.Mouse{Action: mouseMove, Row: 5, Col: 3})
	if off := p.scr.ScrollOffset(); off != 2+maxEdgeScroll {
		t.Fatalf("after dragging below, offset %d", off)
	}
	g.mouse(render.Mouse{Action: mouseRelease, Row: 5, Col: 3})
}

// A selection keeps following the pointer when it crosses into another pane.
func TestDragStaysWithItsPane(t *testing.T) {
	g := newTestGUI(5, 41)
	left := g.current()
	g.addPane(g.currentTab(), 5, 20, splitBeside)
	feedPane(left, "alpha bravo charlie delta")
	g.focusOn(left)

	g.mouse(render.Mouse{Action: mousePress, Row: 0, Col: 0, Clicks: 1})
	g.mouse(render.Mouse{Action: mouseMove, Row: 0, Col: 5})
	g.mouse(render.Mouse{Action: mouseMove, Row: 1, Col: 30}) // over the right pane
	g.mouse(render.Mouse{Action: mouseRelease, Row: 1, Col: 30})

	if got := left.sel.text(left.scr); !strings.HasPrefix(got, "alpha bravo charlie") {
		t.Fatalf("selection = %q", got)
	}
	if g.current() != left {
		t.Fatal("the drag moved the focus")
	}
}

// A program is told about motion once per cell, however often the window
// reports it.
func TestMotionIsReportedOncePerCell(t *testing.T) {
	g := newTestGUI(5, 20)
	out := g.sink()
	feedPane(g.current(), "\x1b[?1002h\x1b[?1006h")

	g.mouse(render.Mouse{Action: mousePress, Row: 1, Col: 1, Clicks: 1})
	g.mouse(render.Mouse{Action: mouseMove, Row: 1, Col: 2})
	g.mouse(render.Mouse{Action: mouseMove, Row: 1, Col: 2})
	g.mouse(render.Mouse{Action: mouseMove, Row: -3, Col: 2}) // off the top: row 0
	g.mouse(render.Mouse{Action: mouseMove, Row: -4, Col: 2}) // still row 0
	if got, want := out.String(), "\x1b[<0;2;2M\x1b[<32;3;2M\x1b[<32;3;1M"; got != want {
		t.Fatalf("reported %q, want %q", got, want)
	}
}

func TestContextClick(t *testing.T) {
	for _, c := range []struct {
		m    render.Mouse
		want bool
	}{
		{render.Mouse{Button: input.MouseRight, Action: mousePress}, true},
		{render.Mouse{Button: input.MouseLeft, Action: mousePress, Mods: input.ModControl}, true},
		{render.Mouse{Button: input.MouseLeft, Action: mousePress}, false},
		{render.Mouse{Button: input.MouseRight, Action: mouseRelease}, false},
	} {
		if got := contextClick(c.m); got != c.want {
			t.Errorf("contextClick(%+v) = %v", c.m, got)
		}
	}
}

// The scroll indicator shows while the view is in history, and goes a moment
// after it returns to the live screen.
func TestScrollIndicator(t *testing.T) {
	g := newTestGUI(5, 20)
	p := g.current()
	if sc := g.draw().Panes[0].Scroll; sc != (render.Scroll{}) {
		t.Fatalf("no history, yet an indicator: %+v", sc)
	}
	history(p, 30)
	g.draw()
	p.scrolledAt = time.Time{} // long ago
	if sc := g.draw().Panes[0].Scroll; sc != (render.Scroll{}) {
		t.Fatalf("at the live screen, an indicator: %+v", sc)
	}

	p.scr.ScrollBy(4)
	sc := g.draw().Panes[0].Scroll
	hist := p.scr.TotalLines() - p.scr.FirstLine() - 5
	if sc.Visible != 5 || sc.Total != int(hist)+5 || sc.Above != int(hist)-4 {
		t.Fatalf("scrolled back 4: %+v (history %d)", sc, hist)
	}
	p.scrolledAt = time.Time{}
	if g.draw().Panes[0].Scroll == (render.Scroll{}) {
		t.Fatal("the indicator went while the view is still in history")
	}

	p.scr.ScrollToBottom()
	if g.draw().Panes[0].Scroll == (render.Scroll{}) {
		t.Fatal("the indicator went the moment the view came back")
	}
	p.scrolledAt = time.Time{}
	if sc := g.draw().Panes[0].Scroll; sc != (render.Scroll{}) {
		t.Fatalf("the indicator lingered: %+v", sc)
	}

	// A full-screen program has no history to place.
	feedPane(p, "\x1b[?1049h")
	p.scrolledAt = time.Now()
	if sc := g.draw().Panes[0].Scroll; sc != (render.Scroll{}) {
		t.Fatalf("an indicator over the alternate screen: %+v", sc)
	}
}

// A bell in a background tab marks it until it is looked at; the flash shows
// for a moment in the pane that rang.
func TestBellMarksABackgroundTab(t *testing.T) {
	g := newTestGUI(5, 20)
	g.Bell = BellFlash
	back := g.addTab(5, 20)
	front := g.tabs[0]

	g.ring(front, front.focus)
	if g.tabViews()[0].Bell {
		t.Fatal("the tab in front was marked")
	}
	if !g.draw().Panes[0].Flash {
		t.Fatal("the pane did not flash")
	}
	front.focus.flashUntil.Store(0)
	if g.draw().Panes[0].Flash {
		t.Fatal("the flash did not end")
	}

	g.ring(back, back.focus)
	if !g.tabViews()[1].Bell {
		t.Fatal("the background tab was not marked")
	}
	g.selectTab(1)
	if g.tabViews()[1].Bell {
		t.Fatal("looking at the tab did not clear its mark")
	}

	g.Bell = BellOff
	g.selectTab(0)
	g.ring(back, back.focus)
	if g.tabViews()[1].Bell {
		t.Fatal("bell = off still marked the tab")
	}
}

func TestParseBell(t *testing.T) {
	for in, want := range map[string]Bell{"sound": BellSound, "flash": BellFlash,
		"both": BellBoth, "off": BellOff, "": BellSound} {
		if got := ParseBell(in); got != want {
			t.Errorf("ParseBell(%q) = %v", in, got)
		}
	}
}
