package terminal

import (
	"testing"

	"github.com/ankitthakur/shellter/internal/input"
	"github.com/ankitthakur/shellter/internal/parser"
	"github.com/ankitthakur/shellter/internal/render"
	"github.com/ankitthakur/shellter/internal/screen"
)

// The window is not testable without a display, but everything between the
// screen buffer and the renderer is: these tests drive the GUI's callbacks
// directly, with no shell and no window.

// newTestGUI builds a GUI with one tab and no shell behind it: everything
// between the screen buffer and the renderer can then be driven directly.
func newTestGUI(rows, cols int) *GUI {
	g := &GUI{
		Palette:     DefaultPalette(),
		Title:       "Shellter",
		rows:        rows,
		cols:        cols,
		lastRows:    rows,
		lastCols:    cols,
		TabPosition: render.TabsTop,
	}
	g.tabs = []*tab{newTab(&pane{scr: screen.New(rows, cols)})}
	return g
}

// screen is the focused pane's buffer, for the tests written before there were
// tabs and panes to have one each.
func (g *GUI) screen() *screen.Screen { return g.current().scr }

// addTab adds another tab with one pane and no shell behind it.
func (g *GUI) addTab(rows, cols int) *tab {
	t := newTab(&pane{scr: screen.New(rows, cols)})
	g.mu.Lock()
	g.tabs = append(g.tabs, t)
	g.mu.Unlock()
	return t
}

// addPane splits a tab's focused pane, again with no shell behind the new one.
func (g *GUI) addPane(t *tab, rows, cols int, dir splitDir) *pane {
	p := &pane{scr: screen.New(rows, cols)}
	g.mu.Lock()
	t.split(p, dir)
	g.mu.Unlock()
	return p
}

// frame is the focused pane's grid, for the tests written before a window
// could hold more than one.
func (g *GUI) frame() render.Frame {
	for _, p := range g.draw().Panes {
		if p.Focused {
			return p.Frame
		}
	}
	return render.Frame{Rows: g.rows, Cols: g.cols}
}

// frameLine reads a row back out of a rendered frame as text.
func frameLine(f render.Frame, row int) string {
	var out []rune
	for col := 0; col < f.Cols; col++ {
		out = append(out, f.CellAt(row, col).Rune)
	}
	return string(out)
}

func (g *GUI) feed(data string) {
	parser.New().Parse([]byte(data), g.screen().Apply)
}

func TestFrameMirrorsTheScreen(t *testing.T) {
	g := newTestGUI(3, 6)
	g.feed("hi\r\nthere")

	f := g.frame()
	if f.Rows != 3 || f.Cols != 6 {
		t.Fatalf("frame size = %dx%d, want 3x6", f.Rows, f.Cols)
	}
	// The renderer draws a rectangle, so the grid must be complete.
	if len(f.Cells) != 18 {
		t.Fatalf("got %d cells, want 18", len(f.Cells))
	}
	if got := frameLine(f, 0); got != "hi    " {
		t.Fatalf("row 0 = %q", got)
	}
	if got := frameLine(f, 1); got != "there " {
		t.Fatalf("row 1 = %q", got)
	}
	if f.CursorRow != 1 || f.CursorCol != 5 {
		t.Fatalf("cursor = %d,%d, want 1,5", f.CursorRow, f.CursorCol)
	}
}

func TestResizeUpdatesTheScreen(t *testing.T) {
	g := newTestGUI(4, 10)
	g.feed("hello")

	// No session and no window: resize must still update the buffer.
	g.resize(2, 4)

	f := g.frame()
	if f.Rows != 2 || f.Cols != 4 {
		t.Fatalf("frame size = %dx%d, want 2x4", f.Rows, f.Cols)
	}
	if got := frameLine(f, 0); got != "hell" {
		t.Fatalf("row 0 = %q, want %q", got, "hell")
	}
	if f.CursorCol != 3 {
		t.Fatalf("cursor col = %d, want it clamped to 3", f.CursorCol)
	}
}

func TestKeyWithoutSessionIsHarmless(t *testing.T) {
	newTestGUI(2, 2).key(input.Key{Chars: []rune{'a'}, Base: 'a'}) // must not panic
}

// The encoding of an arrow key depends on a mode the *shell* set through its
// output. This is the only place the two directions meet, so it is worth a test.
func TestArrowEncodingFollowsTheScreenMode(t *testing.T) {
	g := newTestGUI(3, 10)

	if got := string(g.encode(input.Key{Code: input.CodeUp})); got != "\x1b[A" {
		t.Fatalf("normal mode: got %q, want ESC [ A", got)
	}

	// zsh's line editor enables application cursor keys.
	g.feed("\x1b[?1h")
	if got := string(g.encode(input.Key{Code: input.CodeUp})); got != "\x1bOA" {
		t.Fatalf("application mode: got %q, want ESC O A", got)
	}

	g.feed("\x1b[?1l")
	if got := string(g.encode(input.Key{Code: input.CodeUp})); got != "\x1b[A" {
		t.Fatalf("back to normal mode: got %q, want ESC [ A", got)
	}
}

func TestFrameIsSafeWhileTheScreenChanges(t *testing.T) {
	// The UI thread calls draw() while the PTY goroutine calls Apply. Run
	// both under -race to prove the lock covers it.
	//
	// The lock in question is the *pane's*: the GUI's own lock guards only the
	// list of tabs and the shape of their trees, so a pane printing at full
	// speed cannot hold up the frame being drawn for another.
	g := newTestGUI(10, 20)
	tab := g.current()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			tab.mu.Lock()
			tab.scr.Apply(parser.Event{Kind: parser.EventPrint, Rune: 'x'})
			tab.mu.Unlock()
		}
		close(done)
	}()
	for i := 0; i < 500; i++ {
		_ = g.draw()
	}
	<-done
}

// Scrolling the view is the GUI's own business: those keystrokes must not reach
// the shell, and ordinary keys must snap the view back to the live screen.

func TestShiftPageKeysScrollTheView(t *testing.T) {
	g := newTestGUI(3, 8)
	for i := 0; i < 6; i++ {
		g.feed("line\r\n")
	}
	if g.screen().ScrollbackLen() == 0 {
		t.Fatal("expected history to exist")
	}

	if !g.scrollKey(input.Key{Code: input.CodePageUp, Mods: input.ModShift}) {
		t.Fatal("Shift+PageUp should be consumed by the view")
	}
	if got := g.screen().ScrollOffset(); got != 2 { // a page is rows-1
		t.Fatalf("offset = %d, want 2", got)
	}

	g.scrollKey(input.Key{Code: input.CodePageDown, Mods: input.ModShift})
	if got := g.screen().ScrollOffset(); got != 0 {
		t.Fatalf("offset = %d, want 0", got)
	}

	g.scrollKey(input.Key{Code: input.CodeHome, Mods: input.ModShift})
	if got := g.screen().ScrollOffset(); got != g.screen().ScrollbackLen() {
		t.Fatalf("Shift+Home should jump to the oldest line, offset = %d", got)
	}
	g.scrollKey(input.Key{Code: input.CodeEnd, Mods: input.ModShift})
	if got := g.screen().ScrollOffset(); got != 0 {
		t.Fatalf("Shift+End should return to the live view, offset = %d", got)
	}
}

func TestUnshiftedPageKeysGoToTheShell(t *testing.T) {
	g := newTestGUI(3, 8)
	for i := 0; i < 6; i++ {
		g.feed("line\r\n")
	}

	// Without Shift these are terminal keys, so the view must not consume them.
	if g.scrollKey(input.Key{Code: input.CodePageUp}) {
		t.Fatal("plain PageUp should be sent to the shell, not eaten")
	}
	if got := string(g.encode(input.Key{Code: input.CodePageUp})); got != "\x1b[5~" {
		t.Fatalf("plain PageUp encodes as %q, want ESC[5~", got)
	}
}

func TestWheelScrollsTheView(t *testing.T) {
	g := newTestGUI(3, 8)
	for i := 0; i < 6; i++ {
		g.feed("line\r\n")
	}

	g.scroll(2, 0, 0)
	if got := g.screen().ScrollOffset(); got != 2 {
		t.Fatalf("offset = %d, want 2 after scrolling back", got)
	}
	g.scroll(-5, 0, 0)
	if got := g.screen().ScrollOffset(); got != 0 {
		t.Fatalf("offset = %d, want 0 after scrolling forward", got)
	}
}

func TestScrolledViewShowsHistoryInTheFrame(t *testing.T) {
	g := newTestGUI(2, 8)
	g.feed("first\r\nsecond\r\nthird")

	g.scroll(2, 0, 0)
	if got := frameLine(g.frame(), 0); got != "first   " {
		t.Fatalf("frame row 0 = %q, want the oldest line", got)
	}
	// The cursor lives in the live grid, which is now off the view.
	if g.frame().CursorVisible {
		t.Fatal("the cursor should be hidden while looking at history")
	}
}

// A tab is named after the shell inside it, and a shell says its name at every
// prompt. The bar used to be told once, when the tab opened, which is why a
// tab read "shell 1" for as long as it lived.
func TestATabIsRenamedWhenItsShellSaysWhereItIs(t *testing.T) {
	g := newTestGUI(24, 80)
	tb := g.tabs[0]
	p := tb.focus

	// A shell that has not said anything yet is named after the program, not
	// counted: zsh spends a second or two on its startup files before it sends
	// a title, and "shell 2" was only ever a placeholder for that gap.
	g.Config.Path = "/bin/zsh"
	if got := g.tabViews()[0].Title; got != "zsh" {
		t.Fatalf("a shell that has not said anything should be %q, got %q", "zsh", got)
	}

	// What zsh sends before it prints a prompt.
	parser.New().Parse([]byte("\x1b]2;me@host:/Users/me/PERSONAL/Flux\x07"), p.scr.Apply)
	g.panePrinted(tb, p)

	if got := g.tabViews()[0].Title; got != "Flux" {
		t.Fatalf("the tab should be named after the directory, got %q", got)
	}
	if tb.shown != "me@host:/Users/me/PERSONAL/Flux" {
		t.Fatalf("the bar was not told: shown is %q", tb.shown)
	}
}
