package render

/*
#cgo CFLAGS: -x objective-c -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Cocoa -framework Carbon
#include <stdlib.h>
#include "window_darwin.h"
*/
import "C"

import (
	"errors"
	"os/exec"
	"strings"
	"sync"
	"unsafe"

	"github.com/thakurankit7/shellter/internal/input"
)

// The C callbacks below carry no user-data pointer, so the running Config
// lives in a package variable. One process draws one window; Run enforces it.
var (
	mu      sync.Mutex
	running *Config
)

// Run opens the window and blocks until it closes or Stop is called.
//
// It MUST be called from the main goroutine with runtime.LockOSThread held:
// AppKit refuses to run its event loop anywhere but the process's first thread.
func Run(cfg Config) error {
	if cfg.Draw == nil {
		return errors.New("render: Config.Draw is required")
	}
	if cfg.Rows < 1 {
		cfg.Rows = 24
	}
	if cfg.Cols < 1 {
		cfg.Cols = 80
	}
	if cfg.FontSize <= 0 {
		cfg.FontSize = 14
	}
	if cfg.Chrome.Opacity <= 0 || cfg.Chrome.Opacity > 1 {
		cfg.Chrome.Opacity = 1
	}
	if cfg.Chrome.Padding < 0 {
		cfg.Chrome.Padding = 0
	}

	mu.Lock()
	if running != nil {
		mu.Unlock()
		return errors.New("render: a window is already open")
	}
	running = &cfg
	mu.Unlock()

	defer func() {
		mu.Lock()
		running = nil
		mu.Unlock()
	}()

	// The bar's contents have to be in place before the window lays out, since
	// whether it is visible decides how many rows the terminal gets.
	SetTabs(cfg.Tabs, cfg.ActiveTab, cfg.TabPosition)

	title := C.CString(cfg.Title)
	defer C.free(unsafe.Pointer(title))

	var blur C.int
	if cfg.Chrome.Blur {
		blur = 1
	}

	family := C.CString(cfg.FontFamily)
	defer C.free(unsafe.Pointer(family))
	SetOptionAsMeta(cfg.OptionAsMeta)

	// Blocks in [NSApp run] until shellterStop is called.
	C.shellterRun(title, C.int(cfg.Rows), C.int(cfg.Cols), family, C.double(cfg.FontSize),
		C.double(cfg.LineHeight), C.double(cfg.Chrome.Padding), C.double(cfg.Chrome.Opacity), blur,
		C.uint(cfg.Chrome.Background))
	return nil
}

// SetFont changes the terminal font while the window is open: the family, the
// size Cmd+0 goes back to, and the line height. The window keeps its size and
// the grid gains or loses cells. Main thread only - see Post.
func SetFont(family string, size, lineHeight float64) {
	f := C.CString(family)
	defer C.free(unsafe.Pointer(f))
	C.shellterSetFont(f, C.double(size), C.double(lineHeight))
}

// SetChrome changes how the window is dressed while it is open: padding,
// translucency, blur and the colour the chrome is drawn in. Main thread only.
func SetChrome(c Chrome) {
	var blur C.int
	if c.Blur {
		blur = 1
	}
	C.shellterSetChrome(C.double(c.Padding), C.double(c.Opacity), blur, C.uint(c.Background))
}

// SetOptionAsMeta says whether Option+key is handed over unmodified. Safe from
// any goroutine.
func SetOptionAsMeta(on bool) {
	var v C.int
	if on {
		v = 1
	}
	C.shellterSetOptionAsMeta(v)
}

// posted is work waiting to be run on the main thread.
var (
	postMu sync.Mutex
	posted []func()
)

// Post runs f on the main thread, soon. It is how a goroutine - one watching
// the settings file, say - changes anything the window owns: AppKit may only
// be touched from the thread it runs on.
func Post(f func()) {
	postMu.Lock()
	posted = append(posted, f)
	postMu.Unlock()
	C.shellterPost()
}

// shellterGoRunPosted runs what Post queued. Called on the main thread.
//
//export shellterGoRunPosted
func shellterGoRunPosted() {
	postMu.Lock()
	work := posted
	posted = nil
	postMu.Unlock()
	for _, f := range work {
		f()
	}
}

// Redraw asks the window to repaint. Safe from any goroutine.
func Redraw() { C.shellterRedraw() }

// Stop makes Run return. Safe from any goroutine.
func Stop() { C.shellterStop() }

// SetTitle changes the window title. Safe from any goroutine.
func SetTitle(title string) {
	s := C.CString(title)
	defer C.free(unsafe.Pointer(s))
	C.shellterSetTitle(s)
}

// SetTabs replaces the tab bar's contents and says where the bar goes. Safe
// from any goroutine.
//
// Pushed rather than pulled: tabs change when a person opens or closes one, not
// sixty times a second like the grid.
func SetTabs(tabs []Tab, active int, pos TabPosition) {
	titles := make([]string, len(tabs))
	busy := make([]byte, len(tabs))
	dirs := make([]byte, len(tabs))
	bells := make([]byte, len(tabs))
	for i, t := range tabs {
		// Newline is the separator, so a title may not contain one. Shell
		// titles do not, but a program can set anything it likes.
		titles[i] = strings.ReplaceAll(t.Title, "\n", " ")
		busy[i] = flag(t.Activity)
		dirs[i] = flag(t.Directory)
		bells[i] = flag(t.Bell)
	}
	joined := strings.Join(titles, "\n")

	cTitles := C.CString(joined)
	defer C.free(unsafe.Pointer(cTitles))
	cBusy := C.CString(string(busy))
	defer C.free(unsafe.Pointer(cBusy))
	cDirs := C.CString(string(dirs))
	defer C.free(unsafe.Pointer(cDirs))
	cBells := C.CString(string(bells))
	defer C.free(unsafe.Pointer(cBells))

	C.shellterSetTabs(cTitles, C.int(len(joined)), cBusy, cDirs, cBells,
		C.int(len(tabs)), C.int(active), C.int(pos))
}

// flag is one per-tab boolean as the character the bar reads it back as.
func flag(on bool) byte {
	if on {
		return '1'
	}
	return '0'
}

// SetClipboard puts text on the system pasteboard. Safe from any goroutine.
func SetClipboard(text string) {
	if text == "" {
		return // clearing the clipboard is not something a copy should do
	}
	C.shellterSetClipboard(bytesOf(text), C.int(len(text)))
}

// Clipboard reads the system pasteboard, or returns "" when it holds no text.
func Clipboard() string {
	p := C.shellterClipboard()
	if p == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p)
}

// Beep is the system alert sound: the answer to a command that cannot be
// carried out, when there is nothing else to show, and the audible bell. Safe
// from any goroutine.
func Beep() { C.shellterBeep() }

// Attention bounces the Dock icon once, if Shellter is not the app in front.
// Safe from any goroutine.
func Attention() { C.shellterAttention() }

// ContextMenu pops up the right-click menu at the pointer: Copy, Paste, Select
// All, the splits and Clear, each doing what its menu bar twin does. canCopy
// greys Copy out when nothing is selected. It returns once the menu closes.
// Main thread only.
func ContextMenu(canCopy bool) {
	var c C.int
	if canCopy {
		c = 1
	}
	C.shellterContextMenu(c)
}

// SetCanSplit greys the Split Right and Split Down menu items out, or back in.
// Safe from any goroutine.
func SetCanSplit(can bool) {
	var on C.int
	if can {
		on = 1
	}
	C.shellterSetCanSplit(on)
}

// SetZoom says whether Zoom Pane can be used - the tab has more than one
// pane - and whether a pane is zoomed now, for the menu's check mark. Safe from
// any goroutine.
func SetZoom(can, zoomed bool) {
	var c, z C.int
	if can {
		c = 1
	}
	if zoomed {
		z = 1
	}
	C.shellterSetZoom(c, z)
}

// Confirm asks a yes/no question in a modal alert: message is the question,
// detail says why it is being asked, and action labels the button that goes
// ahead (the other is Cancel). It returns true for the action.
//
// Main thread only, which is where every callback from the window arrives.
func Confirm(message, detail, action string) bool {
	m, d, a := C.CString(message), C.CString(detail), C.CString(action)
	defer C.free(unsafe.Pointer(m))
	defer C.free(unsafe.Pointer(d))
	defer C.free(unsafe.Pointer(a))
	return C.shellterConfirm(m, d, a) != 0
}

// Prompt asks for one line of text in a modal alert, starting from initial.
// It returns what was typed and true, or false if the user cancelled.
//
// Main thread only, like Confirm.
func Prompt(message, detail, initial string) (string, bool) {
	m, d, i := C.CString(message), C.CString(detail), C.CString(initial)
	defer C.free(unsafe.Pointer(m))
	defer C.free(unsafe.Pointer(d))
	defer C.free(unsafe.Pointer(i))
	out := C.shellterPrompt(m, d, i)
	if out == nil {
		return "", false
	}
	defer C.free(unsafe.Pointer(out))
	return C.GoString(out), true
}

// SetLinkHover tells the window the pointer is over a link, and whether
// Command is held - which is when a click opens it, and so when the pointer
// becomes a hand. Main thread only.
func SetLinkHover(link, command bool) {
	var l, c C.int
	if link {
		l = 1
	}
	if command {
		c = 1
	}
	C.shellterSetLinkHover(l, c)
}

// SetFindStatus shows how a search went - "3 of 12", "no matches" - next to
// the search field. Safe from any goroutine.
func SetFindStatus(status string) {
	c := C.CString(status)
	defer C.free(unsafe.Pointer(c))
	C.shellterSetFindStatus(c)
}

// Open hands a URL or a file to the Mac, which opens it in whatever is set up
// to open it: a link in the browser, main.go in the editor.
func Open(target string) error {
	return exec.Command("open", target).Run()
}

// Reveal shows a file selected in a Finder window, without opening it.
func Reveal(path string) error {
	return exec.Command("open", "-R", path).Run()
}

// OpenInEditor opens a text file in the user's default editor for text - the
// app `open -t` picks, TextEdit unless they chose another.
func OpenInEditor(path string) error {
	return exec.Command("open", "-t", path).Run()
}

// SetMouseTracking turns plain motion reporting on and off. It is off unless a
// program asks for it, because it costs a callback per mouse move.
func SetMouseTracking(enabled bool) {
	var on C.int
	if enabled {
		on = 1
	}
	C.shellterSetMouseTracking(on)
}

// bytesOf hands C a pointer to a Go string's bytes.
//
// This is allowed because the string holds no Go pointers and the C side copies
// it before returning: the alternative, C.CString, is a malloc and a free per
// span, which measured 10us for a frame's worth of them.
func bytesOf(s string) *C.char {
	if len(s) == 0 {
		return nil
	}
	return (*C.char)(unsafe.Pointer(unsafe.StringData(s)))
}

func config() *Config {
	mu.Lock()
	defer mu.Unlock()
	return running
}

// ---------------------------------------------------------------------------
// Called from Objective-C
// ---------------------------------------------------------------------------

// shellterGoRender is called from drawRect:. Rather than marshalling the whole
// grid across the boundary, Go walks the spans and calls back into AppKit for
// each one - each side doing what it is good at.
//
//export shellterGoRender
func shellterGoRender() {
	cfg := config()
	if cfg == nil {
		return
	}
	layout := cfg.Draw()

	C.shellterClear(C.uint(layout.DefaultBG))

	split := len(layout.Panes) > 1
	for _, p := range layout.Panes {
		drawPane(p, layout.DefaultBG, split && !p.Focused)
		drawOverlays(p)
	}
	for _, d := range layout.Dividers {
		C.shellterDrawDivider(C.int(d.Row), C.int(d.Col), C.int(d.Rows), C.int(d.Cols))
	}
}

// drawPane paints one pane's grid, offset into the window's coordinates. The
// pane's own frame counts from its top-left corner and knows nothing about
// where it sits.
//
// faded is set for a pane that is not the focused one of several: its colours
// are drawn part way towards the background, so the focused pane is the one
// that reads at full strength.
func drawPane(p Pane, defaultBG uint32, faded bool) {
	f := p.Frame
	row, col := p.Rect.Row, p.Rect.Col

	C.shellterBeginPane(C.int(row), C.int(col), C.int(p.Rect.Rows), C.int(p.Rect.Cols))
	defer C.shellterEndPane()

	for _, sp := range spans(f) {
		fg, bg := sp.FG, sp.BG
		if faded {
			fg, bg = fade(fg, defaultBG), fade(bg, defaultBG)
		}
		if bg != defaultBG {
			C.shellterFillCells(C.int(row+sp.Row), C.int(col+sp.Col), C.int(sp.Width), C.uint(bg))
		}
		if sp.blank() {
			continue // the fill said everything there was to say
		}
		C.shellterDrawText(C.int(row+sp.Row), C.int(col+sp.Col),
			bytesOf(sp.Text), C.int(len(sp.Text)), C.uint(fg), C.uint(sp.Flags))
	}

	// Only the focused pane shows a cursor: two cursors on screen would both
	// look like the place typing goes.
	if !f.CursorVisible || !p.Focused {
		return
	}
	cell := f.CellAt(f.CursorRow, f.CursorCol)
	glyph := cell.Text()
	if glyph == "" {
		glyph = " " // an empty cell, or the far half of a wide character
	}
	width := f.CursorWidth
	if width < 1 {
		width = 1
	}
	// The cell's own colours go across; how to use them depends on the shape,
	// which the ObjC side decides.
	C.shellterDrawCursor(C.int(row+f.CursorRow), C.int(col+f.CursorCol), C.int(width),
		bytesOf(glyph), C.int(len(glyph)),
		C.uint(cell.FG), C.uint(cell.BG), C.int(f.CursorStyle))
}

// drawOverlays paints what goes over a pane's text: the scroll indicator and
// the visual bell.
func drawOverlays(p Pane) {
	r := p.Rect
	if sc := p.Scroll; sc.Total > sc.Visible && sc.Visible > 0 {
		var shown C.int
		if sc.Shown {
			shown = 1
		}
		C.shellterDrawScrollbar(C.int(r.Row), C.int(r.Col), C.int(r.Rows), C.int(r.Cols),
			C.int(sc.Above), C.int(sc.Visible), C.int(sc.Total), C.uint(sc.Color), shown)
	}
	if p.Flash {
		C.shellterFlash(C.int(r.Row), C.int(r.Col), C.int(r.Rows), C.int(r.Cols), C.uint(p.FlashColor))
	}
}

// shellterGoKey delivers a keystroke: the text it produces, the unmodified
// key, and the modifier bitmask built in keyDown:.
//
//export shellterGoKey
func shellterGoKey(chars *C.char, charsLen C.int, base *C.char, baseLen C.int, mods C.uint) {
	cfg := config()
	if cfg == nil || cfg.OnKey == nil {
		return
	}
	cfg.OnKey(classify(
		C.GoStringN(chars, charsLen),
		C.GoStringN(base, baseLen),
		input.Modifiers(mods),
	))
}

// shellterGoResize reports the window's new size in character cells.
//
//export shellterGoResize
func shellterGoResize(rows, cols C.int) {
	cfg := config()
	if cfg == nil || cfg.OnResize == nil {
		return
	}
	cfg.OnResize(int(rows), int(cols))
}

// shellterGoScroll reports a scroll-wheel gesture, in whole lines, along with
// where the pointer was.
//
//export shellterGoScroll
func shellterGoScroll(lines, row, col C.int) {
	cfg := config()
	if cfg == nil || cfg.OnScroll == nil {
		return
	}
	cfg.OnScroll(int(lines), int(row), int(col))
}

// shellterGoMouse reports a mouse event already converted to grid coordinates.
//
//export shellterGoMouse
func shellterGoMouse(button, action, row, col C.int, mods C.uint, clicks C.int) {
	cfg := config()
	if cfg == nil || cfg.OnMouse == nil {
		return
	}
	cfg.OnMouse(Mouse{
		Button: input.MouseButton(button),
		Action: input.MouseAction(action),
		Row:    int(row),
		Col:    int(col),
		Mods:   input.Modifiers(mods),
		Clicks: int(clicks),
	})
}

// shellterGoTabSelect reports a click on a tab.
//
//export shellterGoTabSelect
func shellterGoTabSelect(index C.int) {
	if cfg := config(); cfg != nil && cfg.OnTabSelect != nil {
		cfg.OnTabSelect(int(index))
	}
}

// shellterGoTabClose reports a click on a tab's close button.
//
//export shellterGoTabClose
func shellterGoTabClose(index C.int) {
	if cfg := config(); cfg != nil && cfg.OnTabClose != nil {
		cfg.OnTabClose(int(index))
	}
}

// shellterGoNewTab reports the "+" button or the New Tab menu item.
//
//export shellterGoNewTab
func shellterGoNewTab() {
	if cfg := config(); cfg != nil && cfg.OnNewTab != nil {
		cfg.OnNewTab()
	}
}

// shellterGoTabPosition reports the user moving the bar from the View menu.
//
//export shellterGoTabPosition
func shellterGoTabPosition(pos C.int) {
	if cfg := config(); cfg != nil && cfg.OnTabPosition != nil {
		cfg.OnTabPosition(TabPosition(pos))
	}
}

// shellterGoFocus reports the window gaining or losing keyboard focus.
//
//export shellterGoFocus
func shellterGoFocus(focused C.int) {
	cfg := config()
	if cfg == nil || cfg.OnFocus == nil {
		return
	}
	cfg.OnFocus(focused != 0)
}

// shellterGoClosed reports that the user closed the window.
//
//export shellterGoClosed
func shellterGoClosed() {
	cfg := config()
	if cfg == nil || cfg.OnClose == nil {
		return
	}
	cfg.OnClose()
}

// shellterGoSplit reports Cmd+D, Cmd+Shift+D or the Split menu items.
//
//export shellterGoSplit
func shellterGoSplit(dir C.int) {
	if cfg := config(); cfg != nil && cfg.OnSplit != nil {
		cfg.OnSplit(SplitDirection(dir))
	}
}

// shellterGoFocusPane reports a request to move the focus to another pane.
//
//export shellterGoFocusPane
func shellterGoFocusPane(dir C.int) {
	if cfg := config(); cfg != nil && cfg.OnFocusPane != nil {
		cfg.OnFocusPane(Direction(dir))
	}
}

// shellterGoClosePane reports Cmd+W, which closes the focused pane.
//
//export shellterGoClosePane
func shellterGoClosePane() {
	if cfg := config(); cfg != nil && cfg.OnClosePane != nil {
		cfg.OnClosePane()
	}
}

// shellterGoCopy reports Cmd+C or the Copy menu item.
//
//export shellterGoCopy
func shellterGoCopy() {
	if cfg := config(); cfg != nil && cfg.OnCopy != nil {
		cfg.OnCopy()
	}
}

// shellterGoPaste reports Cmd+V or the Paste menu item.
//
//export shellterGoPaste
func shellterGoPaste() {
	if cfg := config(); cfg != nil && cfg.OnPaste != nil {
		cfg.OnPaste()
	}
}

// shellterGoTabRename reports a double click on a tab, or Rename Tab.
//
//export shellterGoTabRename
func shellterGoTabRename(index C.int) {
	if cfg := config(); cfg != nil && cfg.OnTabRename != nil {
		cfg.OnTabRename(int(index))
	}
}

// shellterGoTabMove reports a tab dragged to another place in the bar.
//
//export shellterGoTabMove
func shellterGoTabMove(from, to C.int) {
	if cfg := config(); cfg != nil && cfg.OnTabMove != nil {
		cfg.OnTabMove(int(from), int(to))
	}
}

// shellterGoSwapPane reports one of the Swap Pane menu items.
//
//export shellterGoSwapPane
func shellterGoSwapPane(dir C.int) {
	if cfg := config(); cfg != nil && cfg.OnSwapPane != nil {
		cfg.OnSwapPane(Direction(dir))
	}
}

// SystemDark reports whether macOS is in dark mode. It reads the preference
// rather than asking the app, so it answers before the window exists.
func SystemDark() bool { return C.shellterSystemDark() != 0 }

// shellterGoAppearance reports macOS switching between light and dark.
//
//export shellterGoAppearance
func shellterGoAppearance(dark C.int) {
	if cfg := config(); cfg != nil && cfg.OnAppearance != nil {
		cfg.OnAppearance(dark != 0)
	}
}

// shellterGoZoomPane reports Zoom Pane.
//
//export shellterGoZoomPane
func shellterGoZoomPane() {
	if cfg := config(); cfg != nil && cfg.OnZoomPane != nil {
		cfg.OnZoomPane()
	}
}

// shellterGoPaneToTab reports Move Pane to New Tab.
//
//export shellterGoPaneToTab
func shellterGoPaneToTab() {
	if cfg := config(); cfg != nil && cfg.OnPaneToTab != nil {
		cfg.OnPaneToTab()
	}
}

// shellterGoHover reports the pointer over a cell of the grid.
//
//export shellterGoHover
func shellterGoHover(row, col C.int, mods C.uint) {
	if cfg := config(); cfg != nil && cfg.OnHover != nil {
		cfg.OnHover(int(row), int(col), input.Modifiers(mods))
	}
}

// shellterGoScrollTo reports a scroll thumb dragged: the pane whose top-left
// cell is row, col should have above lines of history above its view.
//
//export shellterGoScrollTo
func shellterGoScrollTo(row, col, above C.int) {
	if cfg := config(); cfg != nil && cfg.OnScrollTo != nil {
		cfg.OnScrollTo(int(row), int(col), int(above))
	}
}

// shellterGoDrop reports files or text dropped on the grid. Files arrive as
// their paths, one per line.
//
//export shellterGoDrop
func shellterGoDrop(row, col C.int, text *C.char, n C.int, files C.int) {
	cfg := config()
	if cfg == nil || cfg.OnDrop == nil {
		return
	}
	s := C.GoStringN(text, n)
	if files != 0 {
		cfg.OnDrop(int(row), int(col), strings.Split(s, "\n"), "")
		return
	}
	cfg.OnDrop(int(row), int(col), nil, s)
}

// shellterGoFind reports the search field: a new query, or Find Next or
// Previous.
//
//export shellterGoFind
func shellterGoFind(query *C.char, n C.int, dir C.int) {
	if cfg := config(); cfg != nil && cfg.OnFind != nil {
		cfg.OnFind(C.GoStringN(query, n), int(dir))
	}
}

// shellterGoClear reports Cmd+K or the Clear menu item.
//
//export shellterGoClear
func shellterGoClear() {
	if cfg := config(); cfg != nil && cfg.OnClear != nil {
		cfg.OnClear()
	}
}

// shellterGoSettings reports Settings... in the application menu.
//
//export shellterGoSettings
func shellterGoSettings() {
	if cfg := config(); cfg != nil && cfg.OnSettings != nil {
		cfg.OnSettings()
	}
}

// shellterGoCanQuit is asked before the window closes or the app quits.
//
//export shellterGoCanQuit
func shellterGoCanQuit() C.int {
	if cfg := config(); cfg != nil && cfg.CanQuit != nil && !cfg.CanQuit() {
		return 0
	}
	return 1
}

// shellterGoSelectAll reports Cmd+A or the Select All menu item.
//
//export shellterGoSelectAll
func shellterGoSelectAll() {
	if cfg := config(); cfg != nil && cfg.OnSelectAll != nil {
		cfg.OnSelectAll()
	}
}
