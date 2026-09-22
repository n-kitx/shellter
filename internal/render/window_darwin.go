package render

/*
#cgo CFLAGS: -x objective-c -Wno-deprecated-declarations
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
#include "window_darwin.h"
*/
import "C"

import (
	"errors"
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

	// Blocks in [NSApp run] until shellterStop is called.
	C.shellterRun(title, C.int(cfg.Rows), C.int(cfg.Cols), C.double(cfg.FontSize),
		C.double(cfg.Chrome.Padding), C.double(cfg.Chrome.Opacity), blur,
		C.uint(cfg.Chrome.Background))
	return nil
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
	for i, t := range tabs {
		// Newline is the separator, so a title may not contain one. Shell
		// titles do not, but a program can set anything it likes.
		titles[i] = strings.ReplaceAll(t.Title, "\n", " ")
		busy[i] = flag(t.Activity)
		dirs[i] = flag(t.Directory)
	}
	joined := strings.Join(titles, "\n")

	cTitles := C.CString(joined)
	defer C.free(unsafe.Pointer(cTitles))
	cBusy := C.CString(string(busy))
	defer C.free(unsafe.Pointer(cBusy))
	cDirs := C.CString(string(dirs))
	defer C.free(unsafe.Pointer(cDirs))

	C.shellterSetTabs(cTitles, C.int(len(joined)), cBusy, cDirs,
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

	for _, p := range layout.Panes {
		drawPane(p, layout.DefaultBG)
	}
	for _, d := range layout.Dividers {
		C.shellterDrawDivider(C.int(d.Row), C.int(d.Col), C.int(d.Rows), C.int(d.Cols))
	}
	// Dimming last, over everything the pane drew. One translucent rectangle is
	// cheaper than recolouring every cell, and it dims the background as well,
	// which recolouring the text alone would not.
	if len(layout.Panes) > 1 {
		for _, p := range layout.Panes {
			if !p.Focused {
				C.shellterDimCells(C.int(p.Rect.Row), C.int(p.Rect.Col),
					C.int(p.Rect.Rows), C.int(p.Rect.Cols))
			}
		}
	}
}

// drawPane paints one pane's grid, offset into the window's coordinates. The
// pane's own frame counts from its top-left corner and knows nothing about
// where it sits.
func drawPane(p Pane, defaultBG uint32) {
	f := p.Frame
	row, col := p.Rect.Row, p.Rect.Col

	C.shellterBeginPane(C.int(row), C.int(col), C.int(p.Rect.Rows), C.int(p.Rect.Cols))
	defer C.shellterEndPane()

	for _, sp := range spans(f) {
		if sp.BG != defaultBG {
			C.shellterFillCells(C.int(row+sp.Row), C.int(col+sp.Col), C.int(sp.Width), C.uint(sp.BG))
		}
		if sp.blank() {
			continue // the fill said everything there was to say
		}
		C.shellterDrawText(C.int(row+sp.Row), C.int(col+sp.Col),
			bytesOf(sp.Text), C.int(len(sp.Text)), C.uint(sp.FG), C.uint(sp.Flags))
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

// shellterGoSelectAll reports Cmd+A or the Select All menu item.
//
//export shellterGoSelectAll
func shellterGoSelectAll() {
	if cfg := config(); cfg != nil && cfg.OnSelectAll != nil {
		cfg.OnSelectAll()
	}
}
