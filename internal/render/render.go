// Package render draws a terminal frame in a macOS window.
//
// It is the only package in Shellter that touches AppKit, and it deliberately
// knows nothing about PTYs, parsers or screen buffers: it is handed a Frame of
// plain text plus a cursor position, and it hands back keystrokes and window
// sizes. Everything Objective-C lives in window_darwin.m; this file is the
// contract between that and the rest of Go.
package render

import "github.com/n-kitx/shellter/internal/input"

// Config is the wiring between the window and the application.
//
// The layout is pulled (the window asks when it needs to draw) while keys and
// resizes are pushed (the window tells us when they happen). Pulling means we
// never draw a half-updated screen and never queue up redraws we are about to
// throw away.
type Config struct {
	// Title of the window.
	Title string
	// FontSize in points for the monospaced font.
	FontSize float64
	// FontFamily is the terminal font; "" or a font that is not installed
	// means Menlo. OptionAsMeta hands Option+key over unmodified, for Go to
	// send as Meta, instead of letting macOS compose a character from it.
	FontFamily   string
	OptionAsMeta bool
	// LineHeight scales the rows: 1 is the font's own line spacing, 1.2 a
	// fifth more, with the text centred in the extra room.
	LineHeight float64
	// Rows and Cols are the initial size, in character cells.
	Rows, Cols int
	// Chrome is how the window is dressed: padding, translucency, blur.
	Chrome Chrome

	// Draw is called on the UI thread whenever the window redraws. It returns
	// every pane that should be on screen.
	Draw func() Layout
	// OnKey receives a keystroke, described without any AppKit types. Deciding
	// which bytes it sends is package input's job, not this package's.
	OnKey func(input.Key)
	// OnResize reports the new size in cells after the window is resized.
	OnResize func(rows, cols int)
	// OnScroll reports a scroll-wheel gesture, in lines: positive scrolls back
	// into history. Row and col are where the pointer was, which matters when
	// the wheel has to be reported to the program as a button.
	OnScroll func(lines, row, col int)
	// OnScrollTo reports a scroll thumb being dragged: the pane whose top-left
	// cell is row, col should show its history with above lines above it.
	OnScrollTo func(row, col, above int)
	// OnMouse reports a mouse event in character cells. Whether the program
	// running in the terminal wants to hear about it is not this package's
	// question; it reports, the application decides.
	OnMouse func(Mouse)
	// OnFocus reports the window gaining or losing keyboard focus.
	OnFocus func(focused bool)
	// OnClose is called when the user closes the window.
	OnClose func()

	// --- the tab bar ---

	// Tabs is the initial set of tabs, and TabPosition where the bar starts
	// out. Both change later through SetTabs.
	Tabs        []Tab
	ActiveTab   int
	TabPosition TabPosition

	// OnTabSelect, OnTabClose and OnNewTab report what was clicked in the bar
	// or chosen from the menu. OnTabPosition reports the user moving the bar.
	OnTabSelect   func(index int)
	OnTabClose    func(index int)
	OnNewTab      func()
	OnTabPosition func(TabPosition)
	// OnTabRename asks for a tab to be named (a double click on it, or the
	// menu). OnTabMove reports a tab dragged from one place in the bar to
	// another.
	OnTabRename func(index int)
	OnTabMove   func(from, to int)

	// --- splits ---

	// OnSplit divides the focused pane. OnFocusPane moves the focus between
	// panes, and OnClosePane ends the focused one.
	OnSplit     func(SplitDirection)
	OnFocusPane func(Direction)
	OnClosePane func()
	// OnSwapPane exchanges the focused pane with its neighbour that way, and
	// OnPaneToTab moves the focused pane out into a tab of its own.
	OnSwapPane  func(Direction)
	OnPaneToTab func()
	// OnZoomPane enlarges the focused pane to fill its tab, or puts it back.
	OnZoomPane func()
	// OnAppearance reports macOS switching between light and dark mode.
	OnAppearance func(dark bool)

	// --- the clipboard ---

	// OnCopy, OnPaste and OnSelectAll report the Edit menu. What is copied and
	// where a paste goes are questions this package cannot answer: it does not
	// know which pane has the selection.
	OnCopy      func()
	OnPaste     func()
	OnSelectAll func()
	// OnClear reports Cmd+K: clear the focused pane's screen and history.
	OnClear func()
	// OnFind searches the focused pane: dir is 0 when the query changed, 1
	// for Find Next (towards older output), -1 for Find Previous, and 2 when
	// the bar closed. matchCase is the bar's Aa toggle. The result is reported
	// back with SetFindStatus.
	OnFind func(query string, dir int, matchCase bool)
	// OnHover follows the pointer over the grid, whether or not a program
	// asked for mouse reports - it is for the terminal's own use, such as
	// underlining a link. row is -1 when the pointer leaves. Called again,
	// with the same cell, when a modifier key goes down or up.
	OnHover func(row, col int, mods input.Modifiers)
	// OnDrop reports something dropped on the grid at a cell: files, as their
	// paths, or text.
	OnDrop func(row, col int, paths []string, text string)

	// --- the application ---

	// OnSettings reports Settings... (Cmd+,).
	OnSettings func()
	// CanQuit is asked before the window closes or the app quits, and may say
	// no - after asking the user, typically, when something is still running.
	// Nil means yes.
	CanQuit func() bool
}

// Mouse is one mouse event, in the same coordinates as the grid: zero-based,
// row 0 at the top. Translating pixels into cells is the window's job, so this
// is where that unit change ends.
type Mouse struct {
	Button   input.MouseButton
	Action   input.MouseAction
	Row, Col int
	Mods     input.Modifiers
	// Clicks is 1 for a click, 2 for a double, 3 for a triple, and 0 on
	// anything that is not a press. AppKit owns the timing and the slop radius
	// that decide when two clicks are one gesture, so it is reported rather
	// than worked out here.
	Clicks int
}

// modKeypad is the bit keyDown: adds to the modifiers of a key on the numeric
// keypad. It travels with them across cgo and is taken off again in classify:
// it is a fact about the key, not a modifier held.
const modKeypad input.Modifiers = 1 << 4

// AppKit reports arrows and function keys as Unicode private-use values.
const (
	keyUp    = 0xF700
	keyDown  = 0xF701
	keyLeft  = 0xF702
	keyRight = 0xF703
	// F1 to F12 are consecutive, as they are in package input, so the two
	// ranges map onto each other by subtraction.
	keyF1        = 0xF704
	keyF12       = 0xF70F
	keyInsert    = 0xF727
	keyDelete    = 0xF728
	keyHome      = 0xF729
	keyEnd       = 0xF72B
	keyPageUp    = 0xF72C
	keyPageDown  = 0xF72D
	funcKeyFirst = 0xF700
	funcKeyLast  = 0xF8FF
)

// classify turns what NSEvent reported into a platform-neutral input.Key.
//
// chars is -characters (modifiers applied: 'A' for Shift+a) and base is
// -charactersIgnoringModifiers (the physical key), which is what identifies a
// special key and what Ctrl arithmetic must use.
func classify(chars, base string, mods input.Modifiers) input.Key {
	k := input.Key{Chars: []rune(chars), Mods: mods &^ modKeypad, Keypad: mods&modKeypad != 0}
	if br := []rune(base); len(br) > 0 {
		k.Base = br[0]
	}

	switch k.Base {
	case keyUp:
		k.Code = input.CodeUp
	case keyDown:
		k.Code = input.CodeDown
	case keyLeft:
		k.Code = input.CodeLeft
	case keyRight:
		k.Code = input.CodeRight
	case keyHome:
		k.Code = input.CodeHome
	case keyEnd:
		k.Code = input.CodeEnd
	case keyPageUp:
		k.Code = input.CodePageUp
	case keyPageDown:
		k.Code = input.CodePageDown
	case keyInsert:
		k.Code = input.CodeInsert
	case keyDelete:
		k.Code = input.CodeDelete
	case 0x0d:
		k.Code = input.CodeReturn
	case 0x09:
		k.Code = input.CodeTab
	case 0x1b:
		k.Code = input.CodeEscape
	case 0x7f:
		k.Code = input.CodeBackspace
	default:
		switch {
		case k.Base >= keyF1 && k.Base <= keyF12:
			k.Code = input.CodeF1 + input.Code(k.Base-keyF1)
		case k.Base >= funcKeyFirst && k.Base <= funcKeyLast:
			// Every other function key: recognised here, dropped by the encoder.
			k.Code = input.CodeOther
		}
	}
	return k
}
