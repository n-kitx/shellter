package screen

import "github.com/thakurankit7/shellter/internal/parser"

// Terminal modes: the switches a program flips through its output to change how
// the terminal behaves.
//
//	"ESC [ ? n h"   set a private (DEC) mode        "ESC [ ? n l"   reset it
//	"ESC [ n h"     set an ANSI mode                "ESC [ n l"     reset it
//
// Modes are the reason terminal emulation cannot be a pure function of the
// bytes: the same arrow key sends different bytes, and the same mouse click is
// reported differently, depending on switches set minutes earlier.

// Modes are the terminal modes a program can switch on and off. They are
// terminal state like the grid is: set by the program's output, read by the
// rest of Shellter.
type Modes struct {
	// ApplicationCursor is DECCKM (?1): the arrow keys send SS3 rather than
	// CSI sequences. Read when encoding keyboard input.
	ApplicationCursor bool
	// Origin is DECOM (?6): row addressing is relative to the scroll region,
	// and the cursor cannot leave it.
	Origin bool
	// AutoWrap is DECAWM (?7), on by default: whether printing past the last
	// column continues on the next line or overwrites in place.
	AutoWrap bool
	// CursorVisible is DECTCEM (?25), on by default. Read by the renderer.
	CursorVisible bool
	// CursorBlink is ?12. Tracked for the renderer; we draw a steady cursor.
	CursorBlink bool
	// AltScreen reports that the alternate buffer is on screen (?1049). It is
	// set by switching buffers, not written to directly.
	AltScreen bool
	// Insert is IRM (4): printed characters push the rest of the line right
	// instead of overwriting it.
	Insert bool
	// NewLine is LNM (20): a line feed also returns the carriage.
	NewLine bool
	// BracketedPaste is ?2004: pasted text should be wrapped in markers, so a
	// program can tell a paste from typing.
	BracketedPaste bool
	// ReportFocus is ?1004: send "ESC [ I" and "ESC [ O" as the window gains
	// and loses focus. vim uses it to reload changed files.
	ReportFocus bool
	// Mouse is which mouse events the program wants reported, and MouseSGR
	// whether it wants them in the extended encoding.
	Mouse    MouseTracking
	MouseSGR bool
	// SyncOutput is ?2026, synchronised output: the program is part way
	// through redrawing and asks not to be shown until it has finished, so a
	// half-painted screen never flickers past.
	SyncOutput bool
}

// MouseTracking is how much of the mouse a program has asked to see. Each level
// includes the one before it.
type MouseTracking int

const (
	// MouseOff: the program gets nothing; the mouse belongs to the UI.
	MouseOff MouseTracking = iota
	// MouseClick is ?1000: button presses and releases.
	MouseClick
	// MouseDrag is ?1002: presses, releases, and motion while a button is held.
	// This is what vim and less ask for.
	MouseDrag
	// MouseMotion is ?1003: every mouse move, button or not.
	MouseMotion
)

// CursorStyle is the shape DECSCUSR asks for, "ESC [ n SP q". Programs use it
// to show a mode: vim switches to a bar in insert mode.
type CursorStyle int

const (
	CursorBlock CursorStyle = iota
	CursorUnderline
	CursorBar
)

// setMode handles "ESC [ n h/l" and its private "ESC [ ? n h/l" form.
func (s *Screen) setMode(e parser.Event, set bool) {
	for _, p := range e.Params {
		if e.Private == '?' {
			s.setPrivateMode(p, set)
		} else {
			s.setANSIMode(p, set)
		}
	}
}

// setPrivateMode handles the DEC private modes, the ones written with '?'.
// Modes not listed are ignored rather than guessed at.
func (s *Screen) setPrivateMode(p int, set bool) {
	switch p {
	case 1: // DECCKM - application cursor keys
		s.mode.ApplicationCursor = set
	case 6: // DECOM - origin mode: addressing is relative to the scroll region
		s.mode.Origin = set
		s.cursorHome()
	case 7: // DECAWM - autowrap
		s.mode.AutoWrap = set
	case 12: // cursor blink
		s.mode.CursorBlink = set
	case 25: // DECTCEM - cursor visibility
		s.mode.CursorVisible = set

	// --- mouse reporting: which events, and in which encoding ---
	case 1000:
		s.setMouse(MouseClick, set)
	case 1002:
		s.setMouse(MouseDrag, set)
	case 1003:
		s.setMouse(MouseMotion, set)
	case 1004:
		s.mode.ReportFocus = set
	case 1006: // SGR encoding: coordinates as decimal text, so they can exceed 223
		s.mode.MouseSGR = set
	case 1005, 1015: // other extended encodings; we only speak the SGR one
		// ignored on purpose

	// --- the alternate screen, in its three historical spellings ---
	case 47, 1047:
		s.switchAlt(set)
	case 1048: // save/restore the cursor only
		if set {
			s.saveCursor()
		} else {
			s.restoreCursor()
		}
	case 1049: // the modern one: both, in the right order
		if set {
			s.saveCursor()
			s.switchAlt(true)
		} else {
			s.switchAlt(false)
			s.restoreCursor()
		}

	case 2004: // bracketed paste
		s.mode.BracketedPaste = set
	case 2026: // synchronised output
		s.mode.SyncOutput = set
	}
}

// modeState answers DECRQM, "ESC [ ? n $ p": is mode n set? It is how a
// program finds out, before relying on a mode, that the terminal has it at
// all - nvim and tmux ask about 2026 this way. 1 is set, 2 is reset, 0 is
// "not a mode I know".
func (s *Screen) modeState(p int, private bool) int {
	on := func(b bool) int {
		if b {
			return 1
		}
		return 2
	}
	if !private {
		switch p {
		case 4:
			return on(s.mode.Insert)
		case 20:
			return on(s.mode.NewLine)
		}
		return 0
	}
	switch p {
	case 1:
		return on(s.mode.ApplicationCursor)
	case 6:
		return on(s.mode.Origin)
	case 7:
		return on(s.mode.AutoWrap)
	case 12:
		return on(s.mode.CursorBlink)
	case 25:
		return on(s.mode.CursorVisible)
	case 1000:
		return on(s.mode.Mouse == MouseClick)
	case 1002:
		return on(s.mode.Mouse == MouseDrag)
	case 1003:
		return on(s.mode.Mouse == MouseMotion)
	case 1004:
		return on(s.mode.ReportFocus)
	case 1006:
		return on(s.mode.MouseSGR)
	case 47, 1047, 1049:
		return on(s.altActive)
	case 2004:
		return on(s.mode.BracketedPaste)
	case 2026:
		return on(s.mode.SyncOutput)
	}
	return 0
}

// setANSIMode handles the unprefixed modes. There are only two worth having.
func (s *Screen) setANSIMode(p int, set bool) {
	switch p {
	case 4: // IRM - insert mode
		s.mode.Insert = set
	case 20: // LNM - a line feed implies a carriage return
		s.mode.NewLine = set
	}
}

// setMouse raises or lowers the tracking level. Resetting a mode only turns
// tracking off if that mode is the one currently in force, so
// "?1002h ... ?1000l" leaves drag reporting alone.
func (s *Screen) setMouse(level MouseTracking, set bool) {
	if set {
		s.mode.Mouse = level
		return
	}
	if s.mode.Mouse == level {
		s.mode.Mouse = MouseOff
	}
}

// switchAlt moves between the two buffers and keeps the mode flag in step.
func (s *Screen) switchAlt(alt bool) {
	if alt {
		s.enterAlt()
	} else {
		s.leaveAlt()
	}
	s.mode.AltScreen = s.altActive
}

// setCursorStyle implements DECSCUSR, "ESC [ n SP q". 0 and 1 are a blinking
// block, 2 a steady one, 3/4 underline, 5/6 bar.
func (s *Screen) setCursorStyle(n int) {
	switch n {
	case 0, 1, 2:
		s.cursorStyle = CursorBlock
	case 3, 4:
		s.cursorStyle = CursorUnderline
	case 5, 6:
		s.cursorStyle = CursorBar
	}
}
