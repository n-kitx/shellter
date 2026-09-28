package terminal

import (
	"strings"
	"testing"

	"github.com/thakurankit7/shellter/internal/input"
	"github.com/thakurankit7/shellter/internal/parser"
	"github.com/thakurankit7/shellter/internal/render"
	"github.com/thakurankit7/shellter/internal/screen"
)

// feedPane writes bytes to a pane's screen, the way its PTY pump would.
func feedPane(p *pane, data string) {
	parser.New().Parse([]byte(data), p.scr.Apply)
}

func at(line int64, col int) screen.Pos { return screen.Pos{Line: line, Col: col} }

const (
	mousePress   = input.MousePress
	mouseMove    = input.MouseMove
	mouseRelease = input.MouseRelease
)

func keyRune(r rune) input.Key { return input.Key{Chars: []rune{r}, Base: r} }

// --- the model -------------------------------------------------------------

func TestAnEmptySelectionSelectsNothing(t *testing.T) {
	var s selection
	if !s.empty() {
		t.Fatal("a zero selection should be empty")
	}
	s.begin(at(3, 4), 1, false)
	if !s.empty() {
		t.Fatal("a click with no drag selects nothing")
	}
	s.extend(at(3, 9))
	if s.empty() {
		t.Fatal("a drag should select something")
	}
}

func TestBoundsPutTheSelectionInReadingOrder(t *testing.T) {
	var s selection
	s.begin(at(5, 2), 1, false)
	s.extend(at(2, 7)) // dragged upwards

	from, to := s.bounds()
	if from != at(2, 7) || to != at(5, 2) {
		t.Fatalf("bounds = %v..%v, want the earlier position first", from, to)
	}
}

func TestSpansCoverOnlyTheSelectedPartOfEachRow(t *testing.T) {
	var s selection
	s.begin(at(10, 3), 1, false)
	s.extend(at(12, 5))

	// A view showing lines 9..13.
	got := s.spansInto(nil, 9, 5, 20)
	want := []span{
		{},      // line 9: before the selection
		{3, 20}, // line 10: from the click to the end of the row
		{0, 20}, // line 11: all of it
		{0, 5},  // line 12: up to where the drag stopped
		{},      // line 13: after
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d span = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSpansAreEmptyWhenNothingIsSelected(t *testing.T) {
	var s selection
	for i, sp := range s.spansInto(nil, 0, 4, 10) {
		if !sp.empty() {
			t.Fatalf("row %d is marked with nothing selected: %+v", i, sp)
		}
	}
}

// Scrolling must not move the selection: it is anchored to the text, not to
// the window. Same selection, a view two lines further down, two rows higher.
func TestScrollingMovesTheHighlightNotTheSelection(t *testing.T) {
	var s selection
	s.begin(at(10, 0), 1, false)
	s.extend(at(10, 4))

	before := s.spansInto(nil, 8, 6, 20)
	after := s.spansInto(nil, 10, 6, 20)

	if before[2] != (span{0, 4}) {
		t.Fatalf("with line 8 on top the selection should be on row 2, got %+v", before)
	}
	if after[0] != (span{0, 4}) {
		t.Fatalf("with line 10 on top it should be on row 0, got %+v", after)
	}
}

// --- words -----------------------------------------------------------------

func TestWordBoundaries(t *testing.T) {
	line := []rune("cd ~/PERSONAL/Flux && ls -la")
	cases := []struct {
		col  int
		want string
	}{
		{0, "cd"},
		{1, "cd"},
		{2, " "},               // the gap is a word of its own
		{5, "~/PERSONAL/Flux"}, // a path comes out whole
		{14, "~/PERSONAL/Flux"},
		{19, "&&"},
		{22, "ls"},
		{25, "-la"},
	}
	for _, c := range cases {
		from, to := wordAt(line, c.col)
		if got := string(line[from:to]); got != c.want {
			t.Errorf("wordAt(%d) = %q, want %q", c.col, got, c.want)
		}
	}
}

func TestWordAtHandlesAnEmptyLine(t *testing.T) {
	if from, to := wordAt(nil, 0); from != 0 || to != 0 {
		t.Fatalf("wordAt on nothing = %d..%d, want 0..0", from, to)
	}
}

// --- paste -----------------------------------------------------------------

func TestPasteTurnsNewlinesIntoReturns(t *testing.T) {
	got := string(pasteBytes("one\ntwo\r\nthree\rfour", false))
	if got != "one\rtwo\rthree\rfour" {
		t.Fatalf("paste = %q", got)
	}
}

// The reason this rule exists: an ESC in text copied off a web page would
// otherwise be read as an escape sequence by whatever it was pasted into.
func TestPasteDropsControlCharacters(t *testing.T) {
	got := string(pasteBytes("ls\x1b[31m -la\x07\x00", false))
	if got != "ls[31m -la" {
		t.Fatalf("paste = %q, want the escape and the bell gone", got)
	}
	if strings.ContainsRune(got, 0x1b) {
		t.Fatal("an ESC survived the paste")
	}
}

func TestPasteKeepsTabsAndText(t *testing.T) {
	if got := string(pasteBytes("a\tb ünïcode", false)); got != "a\tb ünïcode" {
		t.Fatalf("paste = %q", got)
	}
}

func TestBracketedPasteWrapsTheText(t *testing.T) {
	got := string(pasteBytes("hello", true))
	if got != "\x1b[200~hello\x1b[201~" {
		t.Fatalf("paste = %q, want the brackets round it", got)
	}
	if plain := string(pasteBytes("hello", false)); plain != "hello" {
		t.Fatalf("unbracketed paste = %q, want no markers", plain)
	}
}

// --- through the GUI -------------------------------------------------------

// press, drag, release - then read what would go on the clipboard.
func TestDraggingSelectsWhatWasDraggedOver(t *testing.T) {
	g := newTestGUI(5, 20)
	p := g.current()
	feedPane(p, "hello world\r\nsecond line\r\n")

	g.mouse(render.Mouse{Action: mousePress, Row: 0, Col: 6, Clicks: 1})
	g.mouse(render.Mouse{Action: mouseMove, Row: 0, Col: 11})
	g.mouse(render.Mouse{Action: mouseRelease, Row: 0, Col: 11})

	if got := p.sel.text(p.scr); got != "world" {
		t.Fatalf("selection = %q, want %q", got, "world")
	}
}

func TestDraggingAcrossLinesKeepsTheNewline(t *testing.T) {
	g := newTestGUI(5, 20)
	p := g.current()
	feedPane(p, "alpha\r\nbravo\r\n")

	g.mouse(render.Mouse{Action: mousePress, Row: 0, Col: 0, Clicks: 1})
	g.mouse(render.Mouse{Action: mouseMove, Row: 1, Col: 5})
	g.mouse(render.Mouse{Action: mouseRelease, Row: 1, Col: 5})

	if got := p.sel.text(p.scr); got != "alpha\nbravo" {
		t.Fatalf("selection = %q", got)
	}
}

func TestDoubleClickSelectsAWord(t *testing.T) {
	g := newTestGUI(5, 30)
	p := g.current()
	feedPane(p, "cd ~/PERSONAL/Flux\r\n")

	g.mouse(render.Mouse{Action: mousePress, Row: 0, Col: 8, Clicks: 2})
	g.mouse(render.Mouse{Action: mouseRelease, Row: 0, Col: 8})

	if got := p.sel.text(p.scr); got != "~/PERSONAL/Flux" {
		t.Fatalf("double click selected %q, want the whole path", got)
	}
}

func TestTripleClickSelectsTheLine(t *testing.T) {
	g := newTestGUI(5, 30)
	p := g.current()
	feedPane(p, "one two three\r\nnext\r\n")

	g.mouse(render.Mouse{Action: mousePress, Row: 0, Col: 5, Clicks: 3})
	g.mouse(render.Mouse{Action: mouseRelease, Row: 0, Col: 5})

	if got := p.sel.text(p.scr); got != "one two three\n" {
		t.Fatalf("triple click selected %q", got)
	}
}

func TestTypingClearsTheSelection(t *testing.T) {
	g := newTestGUI(5, 20)
	p := g.current()
	feedPane(p, "hello\r\n")

	g.mouse(render.Mouse{Action: mousePress, Row: 0, Col: 0, Clicks: 1})
	g.mouse(render.Mouse{Action: mouseMove, Row: 0, Col: 5})
	g.mouse(render.Mouse{Action: mouseRelease, Row: 0, Col: 5})
	if p.sel.empty() {
		t.Fatal("nothing was selected to begin with")
	}

	g.key(keyRune('x'))
	if !p.sel.empty() {
		t.Fatal("the selection survived typing")
	}
}

// The highlight has to reach the renderer, or none of this is visible.
func TestSelectedCellsAreDrawnWithTheSelectionColour(t *testing.T) {
	g := newTestGUI(5, 20)
	p := g.current()
	feedPane(p, "hello world\r\n")

	g.mouse(render.Mouse{Action: mousePress, Row: 0, Col: 0, Clicks: 1})
	g.mouse(render.Mouse{Action: mouseMove, Row: 0, Col: 5})
	g.mouse(render.Mouse{Action: mouseRelease, Row: 0, Col: 5})

	f := g.frame()
	pal := DefaultPalette()
	for col := 0; col < 5; col++ {
		if got := f.CellAt(0, col).BG; got != pal.Selection {
			t.Fatalf("cell %d is %06x, want the selection colour %06x", col, got, pal.Selection)
		}
	}
	if got := f.CellAt(0, 6).BG; got != pal.Background {
		t.Fatalf("cell 6 is outside the selection but is %06x", got)
	}
}

func TestSelectAllTakesTheWholePane(t *testing.T) {
	g := newTestGUI(4, 20)
	p := g.current()
	feedPane(p, "one\r\ntwo\r\nthree\r\n")

	g.selectAll()
	got := p.sel.text(p.scr)
	for _, want := range []string{"one", "two", "three"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Select All missed %q; got %q", want, got)
		}
	}
}
