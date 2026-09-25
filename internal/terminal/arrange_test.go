package terminal

import (
	"testing"

	"github.com/thakurankit7/shellter/internal/input"
	"github.com/thakurankit7/shellter/internal/render"
)

func press(row, col, clicks int) render.Mouse {
	return render.Mouse{Button: input.MouseLeft, Action: input.MousePress, Row: row, Col: col, Clicks: clicks}
}

func drag(row, col int) render.Mouse {
	return render.Mouse{Button: input.MouseLeft, Action: input.MouseMove, Row: row, Col: col}
}

func release(row, col int) render.Mouse {
	return render.Mouse{Button: input.MouseLeft, Action: input.MouseRelease, Row: row, Col: col}
}

// Dragging the line between two panes moves it to the pointer, and every pane
// is told its new size.
func TestDraggingADividerResizesThePanes(t *testing.T) {
	g := newTestGUI(24, 81)
	tb := g.currentTab()
	left := g.current()
	right := g.addPane(tb, 24, 40, splitBeside)

	g.mouse(press(5, 40, 1)) // the divider is column 40
	g.mouse(drag(5, 60))
	g.mouse(release(5, 60))

	got := rects(g, tb)
	if got[0].Cols != 60 || got[1].Col != 61 || got[1].Cols != 20 {
		t.Fatalf("after the drag the panes are %+v, want 60 columns then 20", got)
	}
	if _, cols := left.scr.Size(); cols != 60 {
		t.Fatalf("the left pane's screen is %d columns, want 60", cols)
	}
	if _, cols := right.scr.Size(); cols != 20 {
		t.Fatalf("the right pane's screen is %d columns, want 20", cols)
	}

	// Once the button is up, the pointer moving is just the pointer moving.
	g.mouse(drag(5, 10))
	if got := rects(g, tb); got[0].Cols != 60 {
		t.Fatalf("a move after the release still dragged: %+v", got)
	}
}

// A drag cannot squeeze a pane to nothing, and a double click on the divider
// puts it back in the middle.
func TestDividerDragIsClampedAndDoubleClickResets(t *testing.T) {
	g := newTestGUI(24, 81)
	tb := g.currentTab()
	g.addPane(tb, 24, 40, splitBeside)

	g.mouse(press(5, 40, 1))
	g.mouse(drag(5, 0))
	g.mouse(release(5, 0))
	if got := rects(g, tb); got[0].Cols != minPaneCols {
		t.Fatalf("dragged to the edge, the left pane is %d columns, want the minimum %d",
			got[0].Cols, minPaneCols)
	}

	divider := rects(g, tb)[0].Cols
	g.mouse(press(5, divider, 2))
	g.mouse(release(5, divider))
	if got := rects(g, tb); got[0].Cols != 40 || got[1].Cols != 40 {
		t.Fatalf("a double click left the panes at %+v, want them halved", got)
	}
}

// Rows work the same way for a split one above the other.
func TestDraggingAHorizontalDivider(t *testing.T) {
	g := newTestGUI(25, 80)
	tb := g.currentTab()
	g.addPane(tb, 12, 80, splitAbove)

	g.mouse(press(12, 30, 1))
	g.mouse(drag(18, 30))
	g.mouse(release(18, 30))
	if got := rects(g, tb); got[0].Rows != 18 || got[1].Rows != 6 {
		t.Fatalf("after the drag the panes are %+v, want 18 rows then 6", got)
	}
}

// A press inside a pane is the pane's, divider or no divider elsewhere.
func TestPressInAPaneIsNotADividerDrag(t *testing.T) {
	g := newTestGUI(24, 81)
	g.addPane(g.currentTab(), 24, 40, splitBeside)
	if g.dragDivider(press(5, 10, 1)) {
		t.Fatal("a press in the left pane was taken as a divider drag")
	}
	if g.resizing != nil {
		t.Fatal("a drag was started")
	}
}

// Swapping moves the focused pane to its neighbour's place, and the focus with
// it; the shape of the tab does not change.
func TestSwapPaneExchangesPlaces(t *testing.T) {
	g := newTestGUI(24, 81)
	tb := g.currentTab()
	left := g.current()
	right := g.addPane(tb, 24, 40, splitBeside) // focus moves to the new pane
	before := rects(g, tb)

	g.swapPane(render.DirLeft)

	if got := tb.panes(); got[0] != right || got[1] != left {
		t.Fatal("the panes did not change places")
	}
	if tb.focus != right {
		t.Fatal("the focus did not travel with the pane")
	}
	if got := rects(g, tb); got[0] != before[0] || got[1] != before[1] {
		t.Fatalf("swapping changed the layout: %+v, was %+v", got, before)
	}

	// Nothing further left: nothing happens.
	g.swapPane(render.DirLeft)
	if got := tb.panes(); got[0] != right {
		t.Fatal("a swap with no neighbour moved something")
	}
}

// A pane moved to a new tab keeps its shell and screen; the tab it left keeps
// the rest.
func TestPaneToTab(t *testing.T) {
	g := newTestGUI(24, 81)
	first := g.currentTab()
	stays := g.current()
	moves := g.addPane(first, 24, 40, splitBeside)
	g.addTab(24, 81) // a tab after, to show the new one goes in between

	g.paneToTab()

	if len(g.tabs) != 3 || g.active != 1 {
		t.Fatalf("%d tabs, active %d; want 3 with the new one in front at 1", len(g.tabs), g.active)
	}
	if g.tabs[1].sole() != moves {
		t.Fatal("the new tab does not hold the moved pane")
	}
	if first.sole() != stays {
		t.Fatal("the old tab does not hold what was left")
	}
	if _, cols := moves.scr.Size(); cols != 81 {
		t.Fatalf("the moved pane is %d columns, want the whole window's 81", cols)
	}

	// A pane alone in its tab is already a tab.
	g.paneToTab()
	if len(g.tabs) != 3 {
		t.Fatal("moving a lone pane made another tab")
	}

	// And its reader, when the shell exits, finds it in its new home.
	g.forgetPane(moves)
	if len(g.tabs) != 2 || g.tabOf(moves) != nil {
		t.Fatal("the moved pane's tab outlived its shell")
	}
}

// Dragging a tab along the bar reorders the list, and you stay looking at the
// tab you were looking at.
func TestMoveTab(t *testing.T) {
	g := newTestGUI(5, 10)
	a := g.tabs[0]
	b := g.addTab(5, 10)
	c := g.addTab(5, 10)

	g.moveTab(0, 2)
	if g.tabs[0] != b || g.tabs[1] != c || g.tabs[2] != a {
		t.Fatal("moving the first tab to the end did not give b, c, a")
	}
	if g.active != 2 || g.currentTab() != a {
		t.Fatalf("active = %d, want the moved tab, now at 2", g.active)
	}

	// Moving a tab you are not on leaves you where you are.
	g.moveTab(1, 0)
	if g.tabs[0] != c || g.currentTab() != a {
		t.Fatal("moving another tab changed which one is in front")
	}

	g.moveTab(0, 7)
	g.moveTab(-1, 0)
	if g.tabs[0] != c {
		t.Fatal("an out-of-range move did something")
	}
}

// A name the user gave wins over the shell's title; an empty one gives the
// naming back; Cancel changes nothing.
func TestRenameTab(t *testing.T) {
	g := newTestGUI(5, 20)
	g.feed("\x1b]0;~/work/project\x07")

	var offered string
	answer, ok := "build server", true
	g.promptText = func(_, _, initial string) (string, bool) {
		offered = initial
		return answer, ok
	}

	g.renameTab(0)
	if offered != "project" {
		t.Fatalf("the prompt started from %q, want the current label", offered)
	}
	if v := g.tabViews()[0]; v.Title != "build server" || !v.Directory {
		t.Fatalf("tab shows %q (directory %v), want the given name and still the prompt mark", v.Title, v.Directory)
	}

	// A program taking over the pane takes the mark away, named tab or not.
	g.feed("\x1b]0;main.go (~/work) - VIM\x07")
	if v := g.tabViews()[0]; v.Title != "build server" || v.Directory {
		t.Fatalf("with vim running the tab shows %q (directory %v), want the name and no mark", v.Title, v.Directory)
	}

	// The shell retitling itself does not override the user.
	g.feed("\x1b]0;~/elsewhere\x07")
	if v := g.tabViews()[0]; v.Title != "build server" {
		t.Fatalf("the shell renamed a named tab to %q", v.Title)
	}

	answer, ok = "ignored", false
	g.renameTab(0)
	if v := g.tabViews()[0]; v.Title != "build server" {
		t.Fatalf("Cancel renamed the tab to %q", v.Title)
	}

	answer, ok = "   ", true
	g.renameTab(0)
	if v := g.tabViews()[0]; v.Title != "elsewhere" || !v.Directory {
		t.Fatalf("clearing the name left %q; want the shell's title back", v.Title)
	}
}

// A tab holds at most four panes. A fifth split is refused before any shell is
// started for it.
func TestSplitsStopAtFourPanes(t *testing.T) {
	g := newTestGUI(24, 81)
	tb := g.currentTab()
	g.addPane(tb, 24, 40, splitBeside)
	g.addPane(tb, 12, 40, splitAbove)
	if tb.full() {
		t.Fatal("three panes counted as full")
	}
	g.addPane(tb, 12, 20, splitBeside)
	if !tb.full() {
		t.Fatal("four panes did not count as full")
	}

	g.splitPane(render.SplitBeside)
	if n := len(tb.panes()); n != maxPanes {
		t.Fatalf("a fifth split went through: %d panes", n)
	}

	// Closing one makes room again.
	g.forgetPane(tb.focus)
	if tb.full() {
		t.Fatal("still full after a pane closed")
	}
}
