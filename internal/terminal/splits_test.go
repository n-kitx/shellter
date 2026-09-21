package terminal

import (
	"testing"

	"github.com/ankitthakur/shellter/internal/input"
	"github.com/ankitthakur/shellter/internal/render"
)

// Phase 12: a tab holds a tree of panes. These drive the tree directly - there
// is no window and no shell, which is the point: the arrangement is separable
// from both.

// rects reports where a tab's panes are, in layout order.
func rects(g *GUI, t *tab) []rect {
	placed, _ := t.layout(g.grid(), nil, nil)
	out := make([]rect, len(placed))
	for i, p := range placed {
		out[i] = p.rect
	}
	return out
}

func TestATabStartsAsOnePaneFillingTheGrid(t *testing.T) {
	g := newTestGUI(24, 80)
	got := rects(g, g.currentTab())
	if len(got) != 1 {
		t.Fatalf("got %d panes, want 1", len(got))
	}
	if want := (rect{0, 0, 24, 80}); got[0] != want {
		t.Errorf("got %+v, want the whole grid %+v", got[0], want)
	}
}

func TestSplittingBesideDividesTheColumns(t *testing.T) {
	g := newTestGUI(24, 81)
	tb := g.currentTab()
	g.addPane(tb, 24, 40, splitBeside)

	got, lines := tb.layout(g.grid(), nil, nil)
	if len(got) != 2 {
		t.Fatalf("got %d panes, want 2", len(got))
	}
	left, right := got[0].rect, got[1].rect
	if left.Rows != 24 || right.Rows != 24 {
		t.Errorf("a side-by-side split changed the row count: %+v %+v", left, right)
	}
	if left.Cols != 40 || right.Cols != 40 {
		t.Errorf("got %d and %d columns, want them halved with one for the divider",
			left.Cols, right.Cols)
	}
	if left.Col != 0 || right.Col != 41 {
		t.Errorf("panes sit at columns %d and %d, want 0 and 41", left.Col, right.Col)
	}
	if len(lines) != 1 || lines[0].rect.Col != 40 || !lines[0].vertical {
		t.Errorf("got dividers %+v, want one vertical one in column 40", lines)
	}
}

func TestSplittingAboveDividesTheRows(t *testing.T) {
	g := newTestGUI(25, 80)
	tb := g.currentTab()
	g.addPane(tb, 12, 80, splitAbove)

	got, lines := tb.layout(g.grid(), nil, nil)
	top, bottom := got[0].rect, got[1].rect
	if top.Cols != 80 || bottom.Cols != 80 {
		t.Errorf("a stacked split changed the column count: %+v %+v", top, bottom)
	}
	if top.Rows != 12 || bottom.Rows != 12 || bottom.Row != 13 {
		t.Errorf("got %+v and %+v, want 12 rows each with row 12 for the divider", top, bottom)
	}
	if len(lines) != 1 || lines[0].rect.Row != 12 || lines[0].vertical {
		t.Errorf("got dividers %+v, want one horizontal one in row 12", lines)
	}
}

// Splitting should leave you typing in the new pane: that is where you are
// looking, and it is empty.
func TestSplittingMovesTheFocusToTheNewPane(t *testing.T) {
	g := newTestGUI(24, 80)
	tb := g.currentTab()
	fresh := g.addPane(tb, 24, 40, splitBeside)
	if tb.focus != fresh {
		t.Error("the focus stayed in the old pane")
	}
}

// Three panes, nested: the second split applies to the focused half, not to
// the whole tab.
func TestASecondSplitDividesOnlyTheFocusedPane(t *testing.T) {
	g := newTestGUI(25, 81)
	tb := g.currentTab()
	g.addPane(tb, 25, 40, splitBeside) // focus moves to the right half
	g.addPane(tb, 12, 40, splitAbove)  // which is then split in two

	got := rects(g, tb)
	if len(got) != 3 {
		t.Fatalf("got %d panes, want 3", len(got))
	}
	if got[0].Rows != 25 {
		t.Errorf("the left pane is %d rows, want the full 25 - the split was elsewhere", got[0].Rows)
	}
	if got[1].Rows != 12 || got[2].Rows != 12 {
		t.Errorf("the right half was not divided evenly: %+v %+v", got[1], got[2])
	}
	if got[1].Col != 41 || got[2].Col != 41 {
		t.Errorf("the two new panes are not both in the right half: %+v %+v", got[1], got[2])
	}
}

// Every cell of the grid belongs to exactly one pane or one divider. This is
// the property that matters: a gap would be drawn as stale pixels, and an
// overlap would be two panes writing to the same cell.
func TestPanesAndDividersCoverTheGridExactly(t *testing.T) {
	g := newTestGUI(25, 81)
	tb := g.currentTab()
	g.addPane(tb, 25, 40, splitBeside)
	g.addPane(tb, 12, 40, splitAbove)
	g.addPane(tb, 12, 20, splitBeside)

	owner := make([][]int, g.rows)
	for r := range owner {
		owner[r] = make([]int, g.cols)
	}
	panes, lines := tb.layout(g.grid(), nil, nil)
	mark := func(r rect) {
		for row := r.Row; row < r.Row+r.Rows; row++ {
			for col := r.Col; col < r.Col+r.Cols; col++ {
				if row < 0 || row >= g.rows || col < 0 || col >= g.cols {
					t.Fatalf("rect %+v leaves the grid", r)
				}
				owner[row][col]++
			}
		}
	}
	for _, p := range panes {
		mark(p.rect)
	}
	for _, d := range lines {
		mark(d.rect)
	}
	for row := range owner {
		for col, n := range owner[row] {
			if n != 1 {
				t.Fatalf("cell %d,%d is owned by %d things, want exactly 1", row, col, n)
			}
		}
	}
}

// A window too small to divide fairly still has to produce usable panes: a
// pane with no columns would make every program in it divide by zero.
func TestPanesNeverShrinkToNothing(t *testing.T) {
	g := newTestGUI(3, 10)
	tb := g.currentTab()
	g.addPane(tb, 3, 5, splitBeside)
	g.addPane(tb, 1, 5, splitAbove)

	for _, r := range rects(g, tb) {
		if r.Rows < 1 || r.Cols < 1 {
			t.Errorf("pane %+v has no room at all", r)
		}
	}
}

func TestClosingAPaneGivesItsSpaceToTheOther(t *testing.T) {
	g := newTestGUI(24, 81)
	tb := g.currentTab()
	first := tb.focus
	second := g.addPane(tb, 24, 40, splitBeside)

	if alive := tb.remove(second); !alive {
		t.Fatal("the tab reported itself empty with a pane left in it")
	}
	got := rects(g, tb)
	if len(got) != 1 || got[0] != (rect{0, 0, 24, 81}) {
		t.Errorf("got %+v, want the survivor back to the whole grid", got)
	}
	if tb.focus != first {
		t.Error("the focus did not move to the surviving pane")
	}
}

func TestClosingTheLastPaneEmptiesTheTab(t *testing.T) {
	g := newTestGUI(24, 80)
	tb := g.currentTab()
	if alive := tb.remove(tb.focus); alive {
		t.Error("a tab with no panes left reported itself alive")
	}
}

func TestFocusMovesBetweenNeighbours(t *testing.T) {
	g := newTestGUI(25, 81)
	tb := g.currentTab()
	left := tb.focus
	g.addPane(tb, 25, 40, splitBeside) // right half, now focused
	topRight := tb.focus
	bottomRight := g.addPane(tb, 12, 40, splitAbove)

	cases := []struct {
		from *pane
		d    direction
		want *pane
		name string
	}{
		{bottomRight, dirUp, topRight, "up from the bottom right"},
		{topRight, dirDown, bottomRight, "down from the top right"},
		{topRight, dirLeft, left, "left out of the right half"},
		{left, dirRight, topRight, "right into the top of the right half"},
		{left, dirLeft, nil, "left from the leftmost pane"},
		{left, dirUp, nil, "up from a full-height pane"},
	}
	for _, c := range cases {
		if got := tb.neighbour(g.grid(), c.from, c.d); got != c.want {
			t.Errorf("%s: got %p, want %p", c.name, got, c.want)
		}
	}
}

func TestClickingAPaneFocusesIt(t *testing.T) {
	g := newTestGUI(24, 81)
	tb := g.currentTab()
	left := tb.focus
	g.addPane(tb, 24, 40, splitBeside)

	g.mouse(render.Mouse{Row: 5, Col: 3, Action: input.MousePress})
	if tb.focus != left {
		t.Error("clicking the left pane did not focus it")
	}
}

// A program in a split pane is told where the click was *inside that pane*. It
// has no idea it is sharing a window, and it must not be told.
func TestAClickIsReportedInThePanesOwnCoordinates(t *testing.T) {
	g := newTestGUI(24, 81)
	tb := g.currentTab()
	right := tb.focus // the new pane after the split below
	right = g.addPane(tb, 24, 40, splitBeside)

	p, local := g.paneAt(5, 45)
	if p != right {
		t.Fatal("column 45 was not found in the right-hand pane")
	}
	if local.Row != 5 || local.Col != 4 {
		t.Errorf("got %d,%d, want 5,4 - the pane starts at column 41", local.Row, local.Col)
	}
}

// Every pane's PTY has to be told the size of its own rectangle, not the size
// of the window: that is what makes vim in a split fill the split.
func TestEveryPaneIsResizedToItsRectangle(t *testing.T) {
	g := newTestGUI(24, 81)
	tb := g.currentTab()
	g.addPane(tb, 24, 40, splitBeside)

	g.resizeAll()
	for _, p := range tb.panes() {
		rows, cols := p.scr.Size()
		if rows != 24 || cols != 40 {
			t.Errorf("a pane is %dx%d, want 24x40", rows, cols)
		}
	}
}

func TestResizingTheWindowReachesEveryPane(t *testing.T) {
	g := newTestGUI(24, 81)
	tb := g.currentTab()
	g.addPane(tb, 24, 40, splitBeside)

	g.resize(30, 101)
	for _, p := range tb.panes() {
		rows, cols := p.scr.Size()
		if rows != 30 || cols != 50 {
			t.Errorf("a pane is %dx%d, want 30x50", rows, cols)
		}
	}
}

// Typing goes to the focused pane and nowhere else.
func TestKeystrokesGoToTheFocusedPane(t *testing.T) {
	g := newTestGUI(24, 81)
	tb := g.currentTab()
	g.addPane(tb, 24, 40, splitBeside)

	if got := g.current(); got != tb.focus {
		t.Fatalf("current() is not the focused pane")
	}
}

func TestDrawReportsOnePaneEachWithOneCursor(t *testing.T) {
	g := newTestGUI(24, 81)
	tb := g.currentTab()
	g.addPane(tb, 24, 40, splitBeside)
	g.resizeAll()

	out := g.draw()
	if len(out.Panes) != 2 {
		t.Fatalf("got %d panes to draw, want 2", len(out.Panes))
	}
	if len(out.Dividers) != 1 {
		t.Fatalf("got %d dividers, want 1", len(out.Dividers))
	}
	focused := 0
	for _, p := range out.Panes {
		if p.Focused {
			focused++
		}
		// Once the shells have been told their new size the two agree. In the
		// frame or two before that they may not, which is why the renderer
		// clips a pane to its rectangle rather than trusting it.
		if p.Frame.Rows != p.Rect.Rows || p.Frame.Cols != p.Rect.Cols {
			t.Errorf("a pane's grid is %dx%d but its rectangle is %dx%d",
				p.Frame.Rows, p.Frame.Cols, p.Rect.Rows, p.Rect.Cols)
		}
	}
	if focused != 1 {
		t.Errorf("%d panes claim the focus, want exactly 1", focused)
	}
}

// With no splits the window looks exactly as it did before there were any:
// one pane, whole grid, cursor drawn. This is the case that must not regress.
func TestOnePaneIsStillJustAGrid(t *testing.T) {
	g := newTestGUI(10, 20)
	g.feed("hello")

	out := g.draw()
	if len(out.Panes) != 1 || len(out.Dividers) != 0 {
		t.Fatalf("got %d panes and %d dividers, want 1 and 0", len(out.Panes), len(out.Dividers))
	}
	p := out.Panes[0]
	if p.Rect != (render.Rect{Row: 0, Col: 0, Rows: 10, Cols: 20}) {
		t.Errorf("got %+v, want the whole grid", p.Rect)
	}
	if !p.Focused {
		t.Error("the only pane does not have the focus")
	}
	if got := frameLine(p.Frame, 0); got != "hello               " {
		t.Errorf("got %q, want the text drawn", got)
	}
}
