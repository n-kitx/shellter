package terminal

import (
	"strings"

	"github.com/n-kitx/shellter/internal/input"
	"github.com/n-kitx/shellter/internal/render"
)

// Rearranging what is already open: dragging a divider, swapping two panes,
// moving a pane out into a tab of its own, reordering tabs and naming them.
// None of it starts or stops a shell - every one of these moves a pane or a
// tab that exists, which is why none of it needed anything new below the tree.

// resizing is a divider being dragged: which one, and the cell the pointer was
// last in, so a drag that moves within a cell does not resize every shell in
// the tab again.
type resizing struct {
	divider  divider
	row, col int
}

// dragDivider handles a mouse event that belongs to a divider rather than to a
// pane, and reports whether it did. A press on a divider starts a drag, and
// until the button comes up every event is the drag's, wherever the pointer
// has wandered. A double click puts the divider back in the middle.
func (g *GUI) dragDivider(m render.Mouse) bool {
	if g.resizing != nil {
		switch m.Action {
		case input.MouseRelease:
			g.resizing = nil
		case input.MouseMove:
			if m.Row != g.resizing.row || m.Col != g.resizing.col {
				g.resizing.row, g.resizing.col = m.Row, m.Col
				g.mu.Lock()
				g.resizing.divider.moveTo(m.Row, m.Col)
				g.mu.Unlock()
				g.relayout()
			}
		}
		return true
	}

	if m.Action != input.MousePress || m.Button != input.MouseLeft {
		return false
	}
	tb := g.currentTab()
	if tb == nil {
		return false
	}
	g.mu.Lock()
	d, ok := tb.dividerAt(g.grid(), m.Row, m.Col)
	if ok && m.Clicks == 2 {
		d.split.ratio = 0.5
	}
	g.mu.Unlock()
	if !ok {
		return false
	}
	g.resizing = &resizing{divider: d, row: m.Row, col: m.Col}
	if m.Clicks == 2 {
		g.relayout()
	}
	return true
}

// relayout tells every pane its new size after the arrangement changed, and
// redraws.
func (g *GUI) relayout() {
	g.resizeAll()
	g.syncTabs()
	render.Redraw()
}

// swapPane exchanges the focused pane with its neighbour in a direction. The
// focus goes with the pane, so pressing the same keys again carries it on.
func (g *GUI) swapPane(d render.Direction) {
	tb := g.currentTab()
	if tb == nil {
		return
	}
	g.mu.Lock()
	unzoomed := tb.unzoom()
	next := tb.neighbour(g.grid(), tb.focus, direction(d))
	if next != nil {
		tb.swap(tb.focus, next)
	}
	g.mu.Unlock()

	if next != nil || unzoomed {
		g.relayout()
	}
}

// paneToTab moves the focused pane out of its split and into a new tab of its
// own, just after the one it left. The shell carries on: nothing is restarted,
// the pane is only moved from one tree to another.
func (g *GUI) paneToTab() {
	g.mu.Lock()
	if g.active < 0 || g.active >= len(g.tabs) {
		g.mu.Unlock()
		return
	}
	from := g.tabs[g.active]
	p := from.focus
	if p == nil || from.sole() != nil {
		g.mu.Unlock()
		return // a pane alone in its tab is already a tab
	}
	from.remove(p)
	at := g.active + 1
	g.tabs = append(g.tabs[:at], append([]*tab{newTab(p)}, g.tabs[at:]...)...)
	g.active = at
	g.mu.Unlock()

	g.relayout()
}

// moveTab puts the tab at index from at index to, sliding the ones between
// along. The tab you are looking at stays the one you are looking at, wherever
// it ends up.
func (g *GUI) moveTab(from, to int) {
	g.mu.Lock()
	n := len(g.tabs)
	if from < 0 || from >= n || to < 0 || to >= n || from == to {
		g.mu.Unlock()
		return
	}
	active := g.tabs[g.active]
	moved := g.tabs[from]
	g.tabs = append(g.tabs[:from], g.tabs[from+1:]...)
	g.tabs = append(g.tabs[:to], append([]*tab{moved}, g.tabs[to:]...)...)
	for i, tb := range g.tabs {
		if tb == active {
			g.active = i
		}
	}
	g.mu.Unlock()

	g.syncTabs()
}

// renameTab asks for a name for a tab. An empty answer hands the naming back
// to the shell.
func (g *GUI) renameTab(index int) {
	g.mu.Lock()
	if index < 0 || index >= len(g.tabs) {
		g.mu.Unlock()
		return
	}
	tb := g.tabs[index]
	current := tb.name
	if current == "" && tb.focus != nil {
		current, _ = label(tb.focus.currentTitle(), g.shellName())
	}
	g.mu.Unlock()

	// Asked with no lock held: the prompt is modal, and every pane keeps
	// printing under it.
	name, ok := g.askText("Rename Tab",
		"Leave it empty to name the tab after whatever is running in it.", current)
	if !ok {
		return
	}
	g.mu.Lock()
	tb.name = strings.TrimSpace(name)
	g.mu.Unlock()
	g.syncTabs()
}

// askText asks for a line of text. Tests replace it through promptText, since
// the real one needs a window.
func (g *GUI) askText(message, detail, initial string) (string, bool) {
	if g.promptText != nil {
		return g.promptText(message, detail, initial)
	}
	return render.Prompt(message, detail, initial)
}
