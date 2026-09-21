package input

import "testing"

// Phase 9: the keys and the mouse that full-screen programs expect.

func TestFunctionKeys(t *testing.T) {
	cases := []struct {
		code Code
		want string
	}{
		// F1-F4 are SS3 sequences, left over from the VT100 keypad.
		{CodeF1, "\x1bOP"},
		{CodeF4, "\x1bOS"},
		// F5 upwards are tilde sequences, with the historical gaps.
		{CodeF5, "\x1b[15~"},
		{CodeF6, "\x1b[17~"},
		{CodeF10, "\x1b[21~"},
		{CodeF12, "\x1b[24~"},
	}
	for _, c := range cases {
		if got := string(Encode(Key{Code: c.code}, Mode{})); got != c.want {
			t.Fatalf("F%d = %q, want %q", c.code-CodeF1+1, got, c.want)
		}
	}
}

func TestInsertAndForwardDelete(t *testing.T) {
	if got := string(Encode(Key{Code: CodeInsert}, Mode{})); got != "\x1b[2~" {
		t.Fatalf("Insert = %q", got)
	}
	if got := string(Encode(Key{Code: CodeDelete}, Mode{})); got != "\x1b[3~" {
		t.Fatalf("Delete = %q", got)
	}
}

func TestModifiedKeys(t *testing.T) {
	cases := []struct {
		name string
		key  Key
		want string
	}{
		{"shift+up", Key{Code: CodeUp, Mods: ModShift}, "\x1b[1;2A"},
		{"alt+left", Key{Code: CodeLeft, Mods: ModOption}, "\x1b[1;3D"},
		{"ctrl+right", Key{Code: CodeRight, Mods: ModControl}, "\x1b[1;5C"},
		{"ctrl+shift+down", Key{Code: CodeDown, Mods: ModControl | ModShift}, "\x1b[1;6B"},
		{"shift+home", Key{Code: CodeHome, Mods: ModShift}, "\x1b[1;2H"},
		{"ctrl+pagedown", Key{Code: CodePageDown, Mods: ModControl}, "\x1b[6;5~"},
		{"shift+f5", Key{Code: CodeF5, Mods: ModShift}, "\x1b[15;2~"},
		{"shift+f1", Key{Code: CodeF1, Mods: ModShift}, "\x1b[1;2P"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(Encode(c.key, Mode{})); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

// A modifier has to be carried in a parameter, and SS3 sequences have no
// parameters - so holding a modifier overrides application-cursor mode.
func TestModifiersOverrideApplicationCursorMode(t *testing.T) {
	app := Mode{ApplicationCursor: true}
	if got := string(Encode(Key{Code: CodeUp}, app)); got != "\x1bOA" {
		t.Fatalf("plain Up in application mode = %q, want SS3", got)
	}
	if got := string(Encode(Key{Code: CodeUp, Mods: ModShift}, app)); got != "\x1b[1;2A" {
		t.Fatalf("Shift+Up in application mode = %q, want a CSI with the modifier", got)
	}
}

// --- mouse ----------------------------------------------------------------

func TestMouseIsSilentUntilAsked(t *testing.T) {
	e := MouseEvent{Button: MouseLeft, Action: MousePress, Row: 2, Col: 3}
	if got := EncodeMouse(e, MouseMode{}); got != nil {
		t.Fatalf("reported %q with tracking off", got)
	}
}

func TestMouseClickEncodings(t *testing.T) {
	e := MouseEvent{Button: MouseLeft, Action: MousePress, Row: 2, Col: 3}

	// The original encoding: three bytes, everything offset by 32, and
	// coordinates one-based - so col 3 becomes 4+32 = 36 = '$'.
	if got := string(EncodeMouse(e, MouseMode{Tracking: TrackClick})); got != "\x1b[M $#" {
		t.Fatalf("X10 press = %q", got)
	}
	// The SGR encoding writes the numbers out.
	if got := string(EncodeMouse(e, MouseMode{Tracking: TrackClick, SGR: true})); got != "\x1b[<0;4;3M" {
		t.Fatalf("SGR press = %q", got)
	}
}

func TestMouseReleaseLosesTheButtonInTheOldEncoding(t *testing.T) {
	e := MouseEvent{Button: MouseRight, Action: MouseRelease, Row: 0, Col: 0}

	// The old encoding has one code for "something was released".
	if got := string(EncodeMouse(e, MouseMode{Tracking: TrackClick})); got != "\x1b[M#!!" {
		t.Fatalf("X10 release = %q, want button 3", got)
	}
	// SGR keeps the button, and says release with a lowercase final byte.
	if got := string(EncodeMouse(e, MouseMode{Tracking: TrackClick, SGR: true})); got != "\x1b[<2;1;1m" {
		t.Fatalf("SGR release = %q, want button 2 and 'm'", got)
	}
}

func TestMouseTrackingLevels(t *testing.T) {
	drag := MouseEvent{Button: MouseLeft, Action: MouseMove, Row: 1, Col: 1}
	move := MouseEvent{Button: MouseNone, Action: MouseMove, Row: 1, Col: 1}

	if got := EncodeMouse(drag, MouseMode{Tracking: TrackClick}); got != nil {
		t.Fatalf("?1000 reported a drag: %q", got)
	}
	if got := EncodeMouse(drag, MouseMode{Tracking: TrackDrag, SGR: true}); string(got) != "\x1b[<32;2;2M" {
		t.Fatalf("?1002 drag = %q, want the motion bit set", got)
	}
	if got := EncodeMouse(move, MouseMode{Tracking: TrackDrag}); got != nil {
		t.Fatalf("?1002 reported buttonless motion: %q", got)
	}
	if got := EncodeMouse(move, MouseMode{Tracking: TrackMotion, SGR: true}); string(got) != "\x1b[<35;2;2M" {
		t.Fatalf("?1003 motion = %q, want button 3 plus the motion bit", got)
	}
}

func TestMouseWheelIsReportedAsButtons(t *testing.T) {
	up := MouseEvent{Button: MouseWheelUp, Action: MousePress, Row: 4, Col: 5}
	down := MouseEvent{Button: MouseWheelDown, Action: MousePress, Row: 4, Col: 5}

	if got := string(EncodeMouse(up, MouseMode{Tracking: TrackClick, SGR: true})); got != "\x1b[<64;6;5M" {
		t.Fatalf("wheel up = %q", got)
	}
	if got := string(EncodeMouse(down, MouseMode{Tracking: TrackClick, SGR: true})); got != "\x1b[<65;6;5M" {
		t.Fatalf("wheel down = %q", got)
	}
}

func TestMouseModifiers(t *testing.T) {
	e := MouseEvent{Button: MouseLeft, Action: MousePress, Row: 0, Col: 0,
		Mods: ModShift | ModControl}
	// 0 + shift 4 + control 16 = 20.
	if got := string(EncodeMouse(e, MouseMode{Tracking: TrackClick, SGR: true})); got != "\x1b[<20;1;1M" {
		t.Fatalf("got %q, want the modifier bits set", got)
	}
}

// The old encoding cannot express a coordinate past 223, and clamping is the
// least-wrong answer: wrapping would report a click somewhere else entirely.
func TestOldMouseEncodingClampsWideWindows(t *testing.T) {
	e := MouseEvent{Button: MouseLeft, Action: MousePress, Row: 0, Col: 300}
	got := EncodeMouse(e, MouseMode{Tracking: TrackClick})
	if got[3] != 32 || got[4] != 255 {
		t.Fatalf("got % x, want the column clamped", got)
	}
}
