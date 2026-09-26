// Package input turns keyboard events into the bytes a terminal sends.
//
// It is the mirror image of package parser. The parser decodes escape
// sequences arriving from the shell; this package produces them, travelling
// the other way:
//
//	key press ──► input.Encode ──► PTY ──► zsh
//
// Like the parser it is pure: no AppKit, no PTY, no state beyond the Mode it
// is handed. The platform layer says *which key* was pressed; this package
// decides *which bytes* that means.
package input

import "fmt"

// Code identifies a key that does not simply type a character. The platform
// layer maps its own representation (AppKit uses Unicode private-use values)
// onto these, so nothing below this line is macOS-specific.
type Code int

const (
	// CodeNone means an ordinary key: whatever it types is in Key.Chars.
	CodeNone Code = iota
	CodeUp
	CodeDown
	CodeRight
	CodeLeft
	CodeReturn
	CodeTab
	CodeEscape
	CodeBackspace
	CodePageUp
	CodePageDown
	CodeHome
	CodeEnd
	CodeInsert
	// CodeDelete is the forward delete key, not the one labelled Delete on a
	// Mac keyboard - that one is CodeBackspace.
	CodeDelete
	// The function keys. They are consecutive, so CodeF1+n-1 is F(n); htop and
	// midnight commander are built around them, and vim uses F1 for help.
	CodeF1
	CodeF2
	CodeF3
	CodeF4
	CodeF5
	CodeF6
	CodeF7
	CodeF8
	CodeF9
	CodeF10
	CodeF11
	CodeF12
	// CodeOther is a special key we recognise but do not encode. Encode drops
	// it rather than sending the wrong bytes.
	CodeOther
)

// Modifiers is the set of modifier keys held down. The values match the
// bitmask the window layer builds from NSEvent's modifier flags.
type Modifiers uint8

const (
	ModShift Modifiers = 1 << iota
	ModControl
	ModOption
	ModCommand
)

// Has reports whether every modifier in m is held.
func (mods Modifiers) Has(m Modifiers) bool { return mods&m == m }

// Key is one keystroke, described without reference to any platform.
type Key struct {
	// Code identifies a special key, or CodeNone for an ordinary one.
	Code Code
	// Chars is the text the key produces with its modifiers applied: 'a',
	// 'A' for Shift+a, 'ø' for Option+o on a US layout.
	Chars []rune
	// Base is the key without modifiers applied, used for control-byte
	// arithmetic so that Ctrl+C is 0x03 whatever else is held down.
	Base rune
	// Mods are the modifier keys held.
	Mods Modifiers
}

// Mode is the part of the terminal's state that changes what a key sends.
//
// This is the surprising part of terminal input: the same arrow key sends
// different bytes depending on a mode the *application* switched on earlier,
// through its output. zsh's line editor turns it on while it is reading a line
// and off again before running a command.
type Mode struct {
	// ApplicationCursor is DECCKM, set by "ESC [ ? 1 h" and cleared by
	// "ESC [ ? 1 l".
	ApplicationCursor bool
	// OptionAsMeta is a user setting rather than a program's mode, but it
	// changes the bytes the same way: Option held means "prefix ESC", the
	// way Alt does on every other keyboard, instead of "type what macOS
	// composed". The window hands over the unmodified key in that case.
	OptionAsMeta bool
}

// Control bytes worth naming.
const (
	nul = 0x00 // Ctrl+Space
	tab = 0x09 // Tab
	cr  = 0x0d // Return
	esc = 0x1b // Escape
	del = 0x7f // the key macOS labels Delete
)

// Encode returns the bytes to write to the PTY, or nil if the key sends
// nothing.
func Encode(k Key, m Mode) []byte {
	// Command is the window manager's, never the terminal's: Cmd+Q quits,
	// Cmd+C copies. None of it reaches the shell.
	if k.Mods.Has(ModCommand) {
		return nil
	}

	// Meta is ESC and then the key as it would be without it: Option+B is
	// "ESC b", which zsh reads as back-a-word, and Option+Delete is "ESC DEL",
	// delete-a-word. Keys that already carry their modifiers in a parameter -
	// arrows, function keys - keep doing that instead.
	if m.OptionAsMeta && k.Mods.Has(ModOption) && metaPrefixed(k.Code) {
		k.Mods &^= ModOption
		if b := Encode(k, m); len(b) > 0 {
			return append([]byte{esc}, b...)
		}
		return nil
	}

	switch k.Code {
	// Arrow keys are escape sequences, not characters. In normal mode they are
	// CSI sequences; in application-cursor mode the CSI becomes SS3 (ESC O),
	// which is how a program can tell an arrow key from someone typing ESC [ A.
	//
	// Held modifiers change the shape again: Shift+Up is "ESC [ 1 ; 2 A". The
	// modifiers go in a parameter because there is nowhere else to put them,
	// and application-cursor mode is overridden, since SS3 takes no parameters.
	case CodeUp:
		return cursorKey('A', m, k.Mods)
	case CodeDown:
		return cursorKey('B', m, k.Mods)
	case CodeRight:
		return cursorKey('C', m, k.Mods)
	case CodeLeft:
		return cursorKey('D', m, k.Mods)

	// Return sends CR (0x0d), not LF. The PTY's line discipline translates it
	// to NL for the program (ICRNL), which is why pressing Return works even
	// though the shell is waiting for a newline.
	case CodeReturn:
		return []byte{cr}

	// Tab is a plain control character; the shell decides it means completion.
	case CodeTab:
		return []byte{tab}

	// Escape is the same byte that introduces escape sequences. A lone ESC is
	// ambiguous with the start of a sequence, which is why terminals and
	// editors use a timeout to tell them apart.
	case CodeEscape:
		return []byte{esc}

	// The key macOS labels Delete sends DEL (0x7f), not BS (0x08). Check with
	// `stty -a`: erase = ^?.
	case CodeBackspace:
		return []byte{del}

	// Page Up/Down, Insert and Delete are "tilde" sequences: a CSI, a number,
	// and a '~'. They are what a program like less reads to move around.
	case CodeInsert:
		return tildeKey(2, k.Mods)
	case CodeDelete:
		return tildeKey(3, k.Mods)
	case CodePageUp:
		return tildeKey(5, k.Mods)
	case CodePageDown:
		return tildeKey(6, k.Mods)

	// Home and End follow the cursor-key mode, like the arrows do.
	case CodeHome:
		return cursorKey('H', m, k.Mods)
	case CodeEnd:
		return cursorKey('F', m, k.Mods)

	case CodeOther:
		// Recognised, deliberately not encoded.
		return nil
	}

	// The function keys split into two families for historical reasons: F1-F4
	// came from the VT100 keypad and are SS3 sequences, F5 upwards are tilde
	// sequences with numbers that skip 16 and 22 - gaps left by keys that no
	// longer exist. Nothing derives them; they are simply the table.
	if k.Code >= CodeF1 && k.Code <= CodeF12 {
		return functionKey(k.Code, k.Mods)
	}

	// Ctrl+letter is not a lookup table, it is arithmetic: clearing bits 5 and
	// 6 of the ASCII code maps @A-Z[\]^_ onto 0x00-0x1f. That is where the
	// whole C0 control set comes from.
	//
	//	Ctrl+C -> 'C' & 0x1f = 0x03   SIGINT (the line discipline raises it)
	//	Ctrl+D -> 'D' & 0x1f = 0x04   EOF
	//	Ctrl+L -> 'L' & 0x1f = 0x0c   the shell redraws; we never see it
	if k.Mods.Has(ModControl) {
		if b, ok := controlByte(k.Base); ok {
			return []byte{b}
		}
		return nil
	}

	if len(k.Chars) == 0 {
		return nil
	}

	// The numeric keypad's Enter arrives as 0x03, which is also Ctrl+C's byte.
	// Without Control held it can only be Enter.
	if len(k.Chars) == 1 && k.Chars[0] == 0x03 {
		return []byte{cr}
	}

	// Anything else is text: send it as UTF-8, exactly as typed.
	return []byte(string(k.Chars))
}

// metaPrefixed reports whether Meta is sent as an ESC prefix for a key, rather
// than as a modifier parameter the key's own sequence already has room for.
func metaPrefixed(c Code) bool {
	switch c {
	case CodeNone, CodeReturn, CodeTab, CodeBackspace, CodeEscape:
		return true
	}
	return false
}

// cursorKey builds an arrow-key sequence for the current cursor-key mode, with
// any held modifiers encoded into it.
func cursorKey(final byte, m Mode, mods Modifiers) []byte {
	if p := modParam(mods); p != 0 {
		// The '1' is the row parameter, unused here but required as a
		// placeholder: the modifier has to be the second parameter.
		return []byte(fmt.Sprintf("\x1b[1;%d%c", p, final))
	}
	if m.ApplicationCursor {
		return []byte{esc, 'O', final} // SS3 A/B/C/D
	}
	return []byte{esc, '[', final} // CSI A/B/C/D
}

// tildeKey builds "ESC [ n ~", or "ESC [ n ; mods ~" when modifiers are held.
func tildeKey(n int, mods Modifiers) []byte {
	if p := modParam(mods); p != 0 {
		return []byte(fmt.Sprintf("\x1b[%d;%d~", n, p))
	}
	return []byte(fmt.Sprintf("\x1b[%d~", n))
}

// tildeNumbers are the numbers F5 to F12 use. The gaps are real: 16 and 22 were
// assigned to keys that no terminal has had for decades.
var tildeNumbers = [...]int{15, 17, 18, 19, 20, 21, 23, 24}

func functionKey(code Code, mods Modifiers) []byte {
	n := int(code-CodeF1) + 1
	if n <= 4 {
		final := byte('P' + n - 1) // F1..F4 -> P Q R S
		if p := modParam(mods); p != 0 {
			return []byte(fmt.Sprintf("\x1b[1;%d%c", p, final))
		}
		return []byte{esc, 'O', final}
	}
	return tildeKey(tildeNumbers[n-5], mods)
}

// modParam encodes held modifiers the way every CSI sequence does: a bitfield,
// plus one. No modifiers is 0 here, meaning "leave the parameter out".
//
//	shift 1   alt 2   control 4   meta/command 8
func modParam(mods Modifiers) int {
	bits := 0
	if mods.Has(ModShift) {
		bits |= 1
	}
	if mods.Has(ModOption) {
		bits |= 2
	}
	if mods.Has(ModControl) {
		bits |= 4
	}
	if bits == 0 {
		return 0
	}
	return bits + 1
}

// controlByte maps a key to its control character.
func controlByte(r rune) (byte, bool) {
	switch {
	case r == ' ':
		return nul, true // Ctrl+Space is NUL
	case r == '?':
		return del, true // Ctrl+? is DEL, the one that breaks the pattern
	case r >= 'a' && r <= 'z':
		return byte(r) & 0x1f, true
	case r >= '@' && r <= '_':
		// Covers @, A-Z and [ \ ] ^ _ in one stroke.
		return byte(r) & 0x1f, true
	}
	return 0, false
}
