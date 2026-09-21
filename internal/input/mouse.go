package input

import "fmt"

// Mouse reporting.
//
// A terminal does not hand a program mouse events - it types them. When a
// program asks for mouse reporting it is asking the terminal to write escape
// sequences into its input stream whenever the mouse moves or a button is
// pressed. That is why a click in vim looks, from vim's side, exactly like
// someone typing a very strange key.
//
// Two encodings matter. The original packs everything into three bytes:
//
//	ESC [ M  <button+32>  <col+32>  <row+32>
//
// which is compact and completely stuck: a byte cannot hold a column past 223,
// so a wide window loses its right-hand side. The SGR encoding (?1006) writes
// the numbers as decimal text instead, and says on release which button was
// let go - the old one cannot tell:
//
//	ESC [ < button ; col ; row M    press
//	ESC [ < button ; col ; row m    release

// MouseButton is which button an event is about.
type MouseButton int

const (
	MouseLeft MouseButton = iota
	MouseMiddle
	MouseRight
	// MouseWheelUp and MouseWheelDown are reported as buttons 64 and 65: the
	// wheel arrived long after the protocol did, so it was bolted on as two
	// more buttons that are only ever "pressed".
	MouseWheelUp
	MouseWheelDown
	// MouseNone is for motion with no button held.
	MouseNone
)

// MouseAction is what happened.
type MouseAction int

const (
	MousePress MouseAction = iota
	MouseRelease
	MouseMove
)

// MouseEvent is one mouse event in terminal coordinates: zero-based, with row 0
// the top line, the same convention as the screen grid.
type MouseEvent struct {
	Button   MouseButton
	Action   MouseAction
	Row, Col int
	Mods     Modifiers
}

// MouseMode is what the program asked for. Tracking mirrors screen.Modes.Mouse,
// and SGR whether ?1006 is set.
type MouseMode struct {
	// Tracking levels, matching the DEC private modes:
	//	0  off             1  ?1000 press/release
	//	2  ?1002 drag      3  ?1003 any motion
	Tracking int
	SGR      bool
}

// Tracking levels, named so that callers do not pass bare integers.
const (
	TrackOff = iota
	TrackClick
	TrackDrag
	TrackMotion
)

// EncodeMouse returns the bytes reporting e, or nil when the program has not
// asked to hear about this event.
func EncodeMouse(e MouseEvent, m MouseMode) []byte {
	if m.Tracking == TrackOff {
		return nil
	}
	if !wants(e, m.Tracking) {
		return nil
	}

	// Coordinates go on the wire one-based, like everything else in the
	// protocol.
	col, row := e.Col+1, e.Row+1

	if m.SGR {
		final := 'M'
		if e.Action == MouseRelease {
			final = 'm'
		}
		return []byte(fmt.Sprintf("\x1b[<%d;%d;%d%c", buttonCode(e, true), col, row, final))
	}

	b := buttonCode(e, false)

	// The old encoding adds 32 to every field, so that none of them can be a
	// control character. Anything past column 223 cannot be expressed, and is
	// clamped rather than wrapped into nonsense.
	return []byte{esc, '[', 'M', byte(clampByte(b + 32)), byte(clampByte(col + 32)), byte(clampByte(row + 32))}
}

// wants reports whether this tracking level asks for this event.
func wants(e MouseEvent, tracking int) bool {
	switch e.Action {
	case MouseMove:
		if e.Button == MouseNone {
			return tracking >= TrackMotion // only ?1003 wants plain motion
		}
		return tracking >= TrackDrag // dragging needs ?1002
	default:
		return true // presses and releases are reported at every level
	}
}

// buttonCode builds the button byte: the button number, plus bits for the
// modifiers held and for whether this was a drag.
func buttonCode(e MouseEvent, sgr bool) int {
	var b int
	switch e.Button {
	case MouseLeft:
		b = 0
	case MouseMiddle:
		b = 1
	case MouseRight:
		b = 2
	case MouseWheelUp:
		b = 64
	case MouseWheelDown:
		b = 65
	case MouseNone:
		b = 3
	}

	// The old encoding has no way to say *which* button was released, so every
	// release is button 3. SGR fixed that, and keeps the real number - which is
	// why a program that wants to drag properly asks for ?1006.
	if e.Action == MouseRelease && !sgr {
		b = 3
	}
	if e.Action == MouseMove {
		b += 32 // the "motion" bit
	}

	if e.Mods.Has(ModShift) {
		b += 4
	}
	if e.Mods.Has(ModOption) {
		b += 8
	}
	if e.Mods.Has(ModControl) {
		b += 16
	}
	return b
}

func clampByte(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}
