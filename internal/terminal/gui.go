package terminal

import (
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/thakurankit7/shellter/internal/input"
	"github.com/thakurankit7/shellter/internal/pty"
	"github.com/thakurankit7/shellter/internal/render"
	"github.com/thakurankit7/shellter/internal/screen"
	"github.com/thakurankit7/shellter/internal/shell"
)

// readBufferSize is how much is asked for per read from the PTY.
//
// Measured, not guessed: draining a large file through a real PTY ran at
// 155 MB/s with a 4 KB buffer and 193 MB/s with 16 KB, and 64 KB was no better
// than 16. End to end, with parsing and the screen in the loop, the same change
// was worth about 20%.
const readBufferSize = 16 * 1024

// GUI runs shell sessions in a macOS window.
//
// There can be many of them: tabs hold panes, panes hold sessions. The
// pipeline below is per pane and unchanged since Phase 10; the GUI's job is to
// own the tabs, say which pane has the focus, and route the window's events to
// the right one.
//
//	window keys ──► the focused pane ──► PTY ──► parser ──► screen.Screen
//	                                                             │ Snapshot
//	                                                             ▼
//	                            one render.Frame per pane ──► render.Layout ──► NSView
type GUI struct {
	Config   shell.Config
	Title    string
	FontSize float64
	Palette  Palette
	// ScrollbackLines is how much history each tab keeps. Zero disables it.
	ScrollbackLines int
	// TabPosition is where the tab bar starts out. The user can move it from
	// the View menu afterwards.
	TabPosition render.TabPosition
	// Chrome is how the window is dressed: padding, translucency, blur.
	Chrome render.Chrome

	// mu guards the tab list, which tab is active, and the shape of each tab's
	// pane tree. A pane's *contents* are guarded by the pane itself, so a pane
	// printing at full speed never holds up the frame being drawn for another.
	//
	// Lock order, where both are needed: this one first, then the pane's.
	mu     sync.Mutex
	tabs   []*tab
	active int
	// counter names new tabs before their shell has set a title.
	counter int

	// The grid size the window currently has, in cells. Every tab is kept at
	// this size, so switching between them is instant.
	rows, cols int

	// UI thread only, from here down. The cell scratch lives on each pane,
	// since every pane draws its own grid; these are the per-frame lists that
	// describe the arrangement, reused so that a redraw allocates nothing.
	placedBuf     []placed
	dividerBuf    []divider
	paneOut       []render.Pane
	dividerOut    []render.Rect
	redrawPending atomic.Bool
	lastRows      int
	lastCols      int
	lastTitle     string
	// dragging is the pane a selection is being dragged in, or nil. UI thread
	// only, like the selection itself.
	dragging     *pane
	lastTracking screen.MouseTracking

	// out, when set, replaces the active tab's PTY as the destination for
	// encoded input. Tests set it; nothing else does.
	out io.Writer
}

// NewGUI returns a GUI running the default shell in an 80x24 window.
func NewGUI() *GUI {
	cfg := shell.DefaultConfig()
	cfg.Size = pty.Size{Rows: 24, Cols: 80}
	return &GUI{
		Config:          cfg,
		Title:           "Shellter",
		FontSize:        14,
		Palette:         DefaultPalette(),
		ScrollbackLines: screen.DefaultScrollback,
		TabPosition:     render.TabsTop,
		Chrome:          render.DefaultChrome(),
	}
}

// Run opens the window and returns once every tab has closed.
//
// It must be called from the main goroutine, with runtime.LockOSThread held:
// AppKit's event loop only runs on the process's first thread.
func (g *GUI) Run() (int, error) {
	g.rows, g.cols = int(g.Config.Size.Rows), int(g.Config.Size.Cols)
	g.lastRows, g.lastCols = g.rows, g.cols

	first, err := g.openTab()
	if err != nil {
		return -1, err
	}
	firstPane := first.focus

	runErr := render.Run(render.Config{
		Title:         g.Title,
		FontSize:      g.FontSize,
		Rows:          g.rows,
		Cols:          g.cols,
		Chrome:        g.chrome(),
		Tabs:          g.tabViews(),
		ActiveTab:     0,
		TabPosition:   g.TabPosition,
		Draw:          g.draw,
		OnKey:         g.key,
		OnResize:      g.resize,
		OnScroll:      g.scroll,
		OnMouse:       g.mouse,
		OnFocus:       g.focus,
		OnClose:       g.closeWindow,
		OnTabSelect:   g.selectTab,
		OnTabClose:    g.closeTab,
		OnNewTab:      g.newTab,
		OnTabPosition: g.moveTabBar,
		OnSplit:       g.splitPane,
		OnFocusPane:   g.focusPane,
		OnClosePane:   g.closePane,
		OnCopy:        g.copySelection,
		OnPaste:       g.paste,
		OnSelectAll:   g.selectAll,
	})

	// The window is gone; make sure no shell outlives it.
	g.closeWindow()
	code, waitErr := firstPane.sess.Wait()

	switch {
	case runErr != nil:
		return -1, runErr
	case firstPane.readErr != nil:
		return code, firstPane.readErr
	default:
		return code, waitErr
	}
}

// ---------------------------------------------------------------------------
// Tabs
// ---------------------------------------------------------------------------

// openTab starts a session and adds it as the active tab.
func (g *GUI) openTab() (*tab, error) {
	p, err := g.openPane(g.rows, g.cols)
	if err != nil {
		return nil, err
	}
	tb := newTab(p)

	g.mu.Lock()
	g.tabs = append(g.tabs, tb)
	g.active = len(g.tabs) - 1
	g.mu.Unlock()

	g.startPane(tb, p)
	return tb, nil
}

// openPane starts one shell on a grid of the given size.
func (g *GUI) openPane(rows, cols int) (*pane, error) {
	return newPane(g.Config, rows, cols, g.ScrollbackLines)
}

// startPane gives a pane the goroutine that reads its PTY. One per pane,
// exactly as there was one for the single session in Phase 5.
func (g *GUI) startPane(tb *tab, p *pane) {
	go func() {
		p.pump(p.sess, func() { g.panePrinted(tb, p) })
		g.forgetPane(tb, p)
	}()
}

// newTab is the "+" button and Cmd+T.
func (g *GUI) newTab() {
	if _, err := g.openTab(); err != nil {
		// Nothing to show an error in yet, and one tab failing to open must not
		// take the window with it.
		return
	}
	g.syncTabs()
	g.resizeAll()
	render.Redraw()
}

// splitPane divides the focused pane in two and starts a shell in the new half.
func (g *GUI) splitPane(dir render.SplitDirection) {
	tb := g.currentTab()
	if tb == nil {
		return
	}
	// Started at the window's size and resized a moment later, once the tree
	// knows how much of it this pane actually gets. Starting it at the final
	// size would mean laying the tree out twice.
	p, err := g.openPane(g.rows, g.cols)
	if err != nil {
		return
	}

	g.mu.Lock()
	tb.split(p, splitDirOf(dir))
	g.mu.Unlock()

	g.startPane(tb, p)
	g.resizeAll()
	g.syncTabs()
	render.Redraw()
}

func splitDirOf(d render.SplitDirection) splitDir {
	if d == render.SplitBeside {
		return splitBeside
	}
	return splitAbove
}

// focusPane moves the focus to the neighbouring pane, if there is one that way.
func (g *GUI) focusPane(d render.Direction) {
	tb := g.currentTab()
	if tb == nil {
		return
	}
	g.mu.Lock()
	next := tb.neighbour(g.grid(), tb.focus, direction(d))
	if next != nil {
		tb.focus = next
	}
	g.mu.Unlock()

	if next != nil {
		g.syncTabs()
		render.Redraw()
	}
}

// closePane ends the focused pane's shell. Its pump then stops and forgetPane
// takes it out of the tree - the same path a shell exiting on its own takes.
func (g *GUI) closePane() {
	if p := g.current(); p != nil {
		p.close()
	}
}

// closeTab closes every shell in a tab. Each pane's pump ends, and the last
// one to go takes the tab with it.
func (g *GUI) closeTab(index int) {
	g.mu.Lock()
	var panes []*pane
	if index >= 0 && index < len(g.tabs) {
		panes = g.tabs[index].panes()
	}
	g.mu.Unlock()

	for _, p := range panes {
		p.close()
	}
}

// forgetPane removes a pane whose shell has gone. When it was the tab's last
// pane the tab goes too, and when that was the last tab the window stops.
func (g *GUI) forgetPane(tb *tab, p *pane) {
	g.mu.Lock()
	alive := tb.remove(p)
	g.mu.Unlock()

	if !alive {
		g.forgetTab(tb)
		return
	}
	// The survivors share out the space the closed pane was using.
	g.resizeAll()
	g.syncTabs()
	render.Redraw()
}

// forgetTab removes a tab that has no panes left, and stops the window when
// the last one goes with it.
func (g *GUI) forgetTab(t *tab) {
	g.mu.Lock()
	for i, other := range g.tabs {
		if other != t {
			continue
		}
		g.tabs = append(g.tabs[:i], g.tabs[i+1:]...)
		// Stay on the tab to the left, which is what closing a tab should feel
		// like: the neighbour you came from, not a jump to the end.
		if g.active > i || g.active >= len(g.tabs) {
			g.active--
		}
		if g.active < 0 {
			g.active = 0
		}
		break
	}
	empty := len(g.tabs) == 0
	g.mu.Unlock()

	if empty {
		render.Stop()
		return
	}
	g.syncTabs()
	render.Redraw()
}

// selectTab brings a tab to the front.
func (g *GUI) selectTab(index int) {
	g.mu.Lock()
	if index < 0 || index >= len(g.tabs) || index == g.active {
		g.mu.Unlock()
		return
	}
	g.active = index
	g.tabs[index].activity = false // you have now looked at it
	g.mu.Unlock()

	g.syncTabs()
	render.Redraw()
}

// moveTabBar puts the bar somewhere else. The window works out what that means
// for the grid and reports the new size back.
func (g *GUI) moveTabBar(pos render.TabPosition) {
	g.TabPosition = pos
	g.syncTabs()
	render.Redraw()
}

// panePrinted is called when a pane's shell printed something.
func (g *GUI) panePrinted(tb *tab, p *pane) {
	g.mu.Lock()
	front := g.active < len(g.tabs) && g.tabs[g.active] == tb
	changed := false
	if !front && !tb.activity {
		tb.activity = true // light the dot: something happened over there
		changed = true
	}
	// A tab is named after the pane you are looking at, so a shell that
	// renames itself renames its tab. Compared against what the bar was last
	// told rather than pushed on every print: zsh retitles at every prompt,
	// and pushing crosses into AppKit and relays the bar.
	if p == tb.focus {
		if title := p.currentTitle(); title != tb.shown {
			tb.shown = title
			changed = true
		}
	}
	g.mu.Unlock()

	if front {
		g.requestRedraw()
		g.updateWindowTitle(p)
	}
	if changed {
		g.syncTabs()
	}
}

// currentTab is the tab in front, or nil once they have all gone.
func (g *GUI) currentTab() *tab {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active < 0 || g.active >= len(g.tabs) {
		return nil
	}
	return g.tabs[g.active]
}

// current is the focused pane: the one keystrokes go to and the one whose
// modes decide how they are encoded.
func (g *GUI) current() *pane {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active < 0 || g.active >= len(g.tabs) {
		return nil
	}
	return g.tabs[g.active].focus
}

// chrome is how the window is dressed. The background is not a setting: it is
// the terminal's own, so that the title bar cannot drift out of agreement with
// the grid below it.
func (g *GUI) chrome() render.Chrome {
	c := g.Chrome
	c.Background = g.Palette.Background
	return c
}

// grid is the rectangle of cells a tab's panes share out between them: all of
// the window that is not chrome.
func (g *GUI) grid() rect {
	return rect{Rows: g.rows, Cols: g.cols}
}

// tabViews describes the tabs for the bar. Called with the lock held or from
// syncTabs, which takes it.
func (g *GUI) tabViews() []render.Tab {
	out := make([]render.Tab, len(g.tabs))
	for i, t := range g.tabs {
		// A tab is named after the pane you are looking at inside it.
		title := ""
		if t.focus != nil {
			title = t.focus.currentTitle()
		}
		// Remember what the bar is about to be told, so that panePrinted can
		// tell a retitle from the hundred prints that did not change anything.
		t.shown = title
		out[i] = render.Tab{
			Title:    label(title, g.shellName()),
			Activity: t.activity,
		}
	}
	return out
}

// shellName is what a tab is called before its shell has told us anything:
// "zsh", from the program we are about to run. It is right from the first
// frame, which "shell 2" never was.
func (g *GUI) shellName() string {
	switch name := filepath.Base(g.Config.Path); name {
	case "", ".", "/":
		return "shell" // no shell configured: the tests, and nothing else
	default:
		return name
	}
}

// syncTabs pushes the list to the window.
func (g *GUI) syncTabs() {
	g.mu.Lock()
	tabs, active := g.tabViews(), g.active
	g.mu.Unlock()
	render.SetTabs(tabs, active, g.TabPosition)
}

// ---------------------------------------------------------------------------
// Drawing
// ---------------------------------------------------------------------------

// requestRedraw asks for one repaint, however many chunks arrive before it
// happens.
//
// AppKit collapses repeated dirty marks into a single frame, but each request
// is still a hop onto the main queue, and a program producing output as fast as
// the PTY allows makes thousands of them a second. The flag is cleared by the
// frame that results, so at most one request is ever outstanding.
func (g *GUI) requestRedraw() {
	if g.redrawPending.Swap(true) {
		return
	}
	render.Redraw()
}

// draw is called on the UI thread every time the window redraws. Each pane's
// snapshot is taken under that pane's own lock; turning it into colours is
// not, because the copy is ours alone by then.
func (g *GUI) draw() render.Layout {
	g.redrawPending.Store(false)

	out := render.Layout{Rows: g.rows, Cols: g.cols, DefaultBG: g.Palette.Background}

	tb := g.currentTab()
	if tb == nil {
		return out
	}

	g.mu.Lock()
	panes, lines := tb.layout(g.grid(), g.placedBuf, g.dividerBuf)
	g.placedBuf, g.dividerBuf = panes, lines
	focus := tb.focus
	g.mu.Unlock()

	out.Panes, out.Dividers = g.paneOut[:0], g.dividerOut[:0]

	for _, pl := range panes {
		pl.pane.mu.Lock()
		snap := pl.pane.scr.SnapshotInto(pl.pane.snapCells)
		pl.pane.mu.Unlock()
		pl.pane.snapCells = snap.Cells

		// Only worked out when there is something to show: an empty selection
		// costs a redraw nothing, which matters because most of them are.
		var marked []span
		if !pl.pane.sel.empty() {
			pl.pane.selSpans = pl.pane.sel.spansInto(
				pl.pane.selSpans, snap.TopLine, snap.Rows, snap.Cols)
			marked = pl.pane.selSpans
		}

		f := g.Palette.FrameInto(snap, pl.pane.frameCells, marked)
		pl.pane.frameCells = f.Cells

		out.Panes = append(out.Panes, render.Pane{
			Rect:    render.Rect{Row: pl.rect.Row, Col: pl.rect.Col, Rows: pl.rect.Rows, Cols: pl.rect.Cols},
			Frame:   f,
			Focused: pl.pane == focus,
		})
	}
	for _, d := range lines {
		out.Dividers = append(out.Dividers, render.Rect{
			Row: d.rect.Row, Col: d.rect.Col, Rows: d.rect.Rows, Cols: d.rect.Cols,
		})
	}
	g.paneOut, g.dividerOut = out.Panes, out.Dividers
	return out
}

// updateWindowTitle keeps the window named after the focused pane.
func (g *GUI) updateWindowTitle(p *pane) {
	title := p.currentTitle()
	if title == g.lastTitle {
		return
	}
	g.lastTitle = title
	render.SetTitle(g.titleFor(title))
}

// titleFor is what the window should say. An empty title means the program
// never set one, or cleared it, so our own name comes back.
func (g *GUI) titleFor(asked string) string {
	if asked == "" {
		return g.Title
	}
	return asked
}

// ---------------------------------------------------------------------------
// Input
// ---------------------------------------------------------------------------

// key encodes a keystroke and writes it to the active tab's PTY.
//
// Two things are intercepted first, because they are requests to *this program*
// rather than input for the shell: the scrollback shortcuts, and Cmd+digit for
// switching tabs.
func (g *GUI) key(k input.Key) {
	if g.tabKey(k) || g.scrollKey(k) {
		return
	}
	b := g.encode(k)
	if len(b) == 0 {
		return
	}

	// Typing returns to the live screen, as every terminal does, and drops the
	// selection: what was marked is about to be scrolled away by the answer.
	if t := g.current(); t != nil {
		t.mu.Lock()
		moved := t.scr.ScrollToBottom()
		t.mu.Unlock()
		g.clearSelection(t)
		if moved {
			render.Redraw()
		}
	}
	g.write(b)
}

// tabKey handles Cmd+1 to Cmd+9, reporting whether it consumed the keystroke.
// The rest of the tab commands are menu items, where they can be found.
func (g *GUI) tabKey(k input.Key) bool {
	if !k.Mods.Has(input.ModCommand) || len(k.Chars) != 1 {
		return false
	}
	d := k.Chars[0]
	if d < '1' || d > '9' {
		return false
	}
	g.selectTab(int(d - '1'))
	return true
}

// scrollKey handles the view-scrolling shortcuts, reporting whether it consumed
// the keystroke.
func (g *GUI) scrollKey(k input.Key) bool {
	if !k.Mods.Has(input.ModShift) {
		return false
	}
	t := g.current()
	if t == nil {
		return false
	}

	t.mu.Lock()
	// On the alternate screen there is no history to scroll, and the program
	// may well want Shift+Page Up itself.
	if t.scr.AltScreen() {
		t.mu.Unlock()
		return false
	}
	rows, _ := t.scr.Size()
	page := rows - 1
	if page < 1 {
		page = 1
	}
	var moved bool
	switch k.Code {
	case input.CodePageUp:
		moved = t.scr.ScrollBy(page)
	case input.CodePageDown:
		moved = t.scr.ScrollBy(-page)
	case input.CodeHome:
		moved = t.scr.ScrollToTop()
	case input.CodeEnd:
		moved = t.scr.ScrollToBottom()
	default:
		t.mu.Unlock()
		return false
	}
	t.mu.Unlock()

	if moved {
		render.Redraw()
	}
	return true
}

// scroll handles a wheel turn, which means one of three different things
// depending on what is running:
//
//	a program asked for the mouse  ->  report the wheel as buttons 64/65
//	the alternate screen is up     ->  send arrow keys, so less and man scroll
//	otherwise                      ->  scroll our own view through history
func (g *GUI) scroll(lines, row, col int) {
	// The wheel turns in whichever pane the pointer is over, focused or not -
	// that is what a pointer is for.
	t, local := g.paneAt(row, col)
	if t == nil {
		return
	}
	row, col = local.Row, local.Col

	t.mu.Lock()
	mouse := mouseMode(t.scr.Modes())
	alt := t.scr.AltScreen()
	appCursor := t.scr.ApplicationCursorKeys()
	t.mu.Unlock()

	if mouse.Tracking != input.TrackOff {
		button := input.MouseWheelUp
		if lines < 0 {
			button = input.MouseWheelDown
		}
		for i := 0; i < abs(lines); i++ {
			g.writeTo(t, input.EncodeMouse(input.MouseEvent{
				Button: button,
				Action: input.MousePress, // the wheel is only ever pressed
				Row:    row,
				Col:    col,
			}, mouse))
		}
		return
	}

	if alt {
		// No history to scroll, so send what the program would get from the
		// arrow keys. This is what makes the wheel work in less and man.
		code := input.CodeUp
		if lines < 0 {
			code = input.CodeDown
		}
		for i := 0; i < abs(lines); i++ {
			g.writeTo(t, input.Encode(input.Key{Code: code}, input.Mode{ApplicationCursor: appCursor}))
		}
		return
	}

	t.mu.Lock()
	moved := t.scr.ScrollBy(lines)
	t.mu.Unlock()
	if moved {
		render.Redraw()
	}
}

// mouse reports a click, release or drag to the program in the pane it landed
// in. When that program did not ask for the mouse the event is dropped: the
// mouse belongs to the window.
//
// Either way a press moves the focus, the way clicking a window does. That
// happens before the report, so a program that does want the click sees it in
// a pane that is already focused.
func (g *GUI) mouse(m render.Mouse) {
	p, local := g.paneAt(m.Row, m.Col)
	if p == nil {
		return
	}
	if m.Action == input.MousePress {
		g.focusOn(p)
	}

	p.mu.Lock()
	mode := mouseMode(p.scr.Modes())
	rows, cols := p.scr.Size()
	p.mu.Unlock()

	// The mouse either marks text or goes to the program. Shift is the way
	// back from a program that asked for it - see selectable.
	if selectable(mode.Tracking, m) {
		g.selectWithMouse(p, local, m, rows, cols)
		return
	}
	g.writeTo(p, input.EncodeMouse(input.MouseEvent{
		Button: m.Button,
		Action: m.Action,
		Row:    clamp(local.Row, 0, rows-1),
		Col:    clamp(local.Col, 0, cols-1),
		Mods:   m.Mods,
	}, mode))
}

// paneAt finds the pane covering a window cell and translates the cell into
// that pane's own coordinates. With no splits this is the focused pane and the
// coordinates come back unchanged.
func (g *GUI) paneAt(row, col int) (*pane, render.Mouse) {
	tb := g.currentTab()
	if tb == nil {
		return nil, render.Mouse{}
	}

	g.mu.Lock()
	panes, _ := tb.layout(g.grid(), nil, nil)
	g.mu.Unlock()

	for _, pl := range panes {
		if pl.rect.contains(row, col) {
			return pl.pane, render.Mouse{Row: row - pl.rect.Row, Col: col - pl.rect.Col}
		}
	}
	// A click in a divider, or outside the grid: give it to the focused pane
	// rather than dropping it, since a drag that leaves a pane should still end
	// in the one it started in.
	return g.current(), render.Mouse{Row: row, Col: col}
}

// focusOn moves the focus to a pane, if it is not there already.
func (g *GUI) focusOn(p *pane) {
	g.mu.Lock()
	tb := (*tab)(nil)
	if g.active >= 0 && g.active < len(g.tabs) {
		tb = g.tabs[g.active]
	}
	changed := tb != nil && tb.focus != p
	if changed {
		tb.focus = p
	}
	g.mu.Unlock()

	if changed {
		g.syncTabs()
		render.Redraw()
	}
}

// focus tells the program the window gained or lost focus, if it asked (?1004).
func (g *GUI) focus(focused bool) {
	t := g.current()
	if t == nil {
		return
	}
	t.mu.Lock()
	want := t.scr.Modes().ReportFocus
	t.mu.Unlock()
	if !want {
		return
	}
	if focused {
		g.write([]byte("\x1b[I"))
	} else {
		g.write([]byte("\x1b[O"))
	}
}

// encode is key without the write, so it can be tested without a shell.
//
// The mode comes from the screen: what an arrow key sends depends on whether
// the program running inside asked for application cursor keys.
func (g *GUI) encode(k input.Key) []byte {
	mode := input.Mode{}
	if t := g.current(); t != nil {
		t.mu.Lock()
		mode.ApplicationCursor = t.scr.ApplicationCursorKeys()
		t.mu.Unlock()
	}
	return input.Encode(k, mode)
}

// ---------------------------------------------------------------------------
// Window
// ---------------------------------------------------------------------------

// resize keeps every pane in agreement with the window, not just the visible
// ones, so that switching to a background tab never shows a grid of the wrong
// shape.
func (g *GUI) resize(rows, cols int) {
	// The window reports every pixel of a drag, but the cell count only changes
	// every font-width of it. Without this check each pixel sent every shell
	// another SIGWINCH, and a program like vim redraws its whole screen for
	// every one of them.
	if !g.sizeChanged(rows, cols) {
		return
	}
	g.rows, g.cols = rows, cols
	g.resizeAll()
	g.updateTracking()
	render.Redraw()
}

// resizeAll gives every pane in every tab the rectangle the layout says it has.
//
// It runs after a window resize and after any change to a tree - a split, a
// pane closing - because all three mean the same thing to a pane: the space it
// was using is no longer the space it has.
func (g *GUI) resizeAll() {
	type sizing struct {
		pane       *pane
		rows, cols int
	}

	g.mu.Lock()
	var work []sizing
	for _, tb := range g.tabs {
		panes, _ := tb.layout(g.grid(), nil, nil)
		for _, pl := range panes {
			work = append(work, sizing{pl.pane, pl.rect.Rows, pl.rect.Cols})
		}
	}
	g.mu.Unlock()

	// Outside the lock: resizing a pane writes to its PTY, and a pane's lock
	// must never be taken while holding the GUI's for longer than a copy.
	for _, w := range work {
		w.pane.resize(w.rows, w.cols)
	}
}

// sizeChanged reports whether rows x cols differs from the size last applied,
// and remembers it. Called only from the UI thread.
func (g *GUI) sizeChanged(rows, cols int) bool {
	if rows == g.lastRows && cols == g.lastCols {
		return false
	}
	g.lastRows, g.lastCols = rows, cols
	return true
}

// updateTracking installs the motion tracking area only while the visible tab's
// program wants every mouse move (?1003). Clicks and drags arrive without it.
func (g *GUI) updateTracking() {
	t := g.current()
	if t == nil {
		return
	}
	t.mu.Lock()
	tracking := t.scr.Modes().Mouse
	t.mu.Unlock()

	if tracking == g.lastTracking {
		return
	}
	g.lastTracking = tracking
	render.SetMouseTracking(tracking == screen.MouseMotion)
}

// closeWindow is called when the user closes the window: every shell hangs up.
func (g *GUI) closeWindow() {
	g.mu.Lock()
	var panes []*pane
	for _, tb := range g.tabs {
		panes = append(panes, tb.panes()...)
	}
	g.mu.Unlock()
	for _, p := range panes {
		p.close()
	}
}

// mouseMode translates the screen's modes into what the encoder needs. The two
// packages keep their own vocabulary; this is the seam between them.
func mouseMode(m screen.Modes) input.MouseMode {
	mode := input.MouseMode{SGR: m.MouseSGR}
	switch m.Mouse {
	case screen.MouseClick:
		mode.Tracking = input.TrackClick
	case screen.MouseDrag:
		mode.Tracking = input.TrackDrag
	case screen.MouseMotion:
		mode.Tracking = input.TrackMotion
	}
	return mode
}

// write sends bytes to the focused pane's shell.
func (g *GUI) write(b []byte) { g.writeTo(g.current(), b) }

// writeTo sends bytes to one pane's shell. The mouse and the wheel use it,
// because the pane they are reporting about is not always the focused one.
func (g *GUI) writeTo(p *pane, b []byte) {
	if len(b) == 0 {
		return
	}
	if g.out != nil {
		_, _ = g.out.Write(b)
		return
	}
	if p != nil {
		p.write(b)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
