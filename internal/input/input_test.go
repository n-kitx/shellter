package input

import (
	"bytes"
	"testing"
)

// key builds an ordinary character keystroke.
func key(r rune, mods Modifiers) Key {
	return Key{Chars: []rune{r}, Base: r, Mods: mods}
}

func TestPrintableCharacters(t *testing.T) {
	cases := []struct {
		name string
		k    Key
		want []byte
	}{
		{"lowercase", key('a', 0), []byte{'a'}},
		{"shifted", Key{Chars: []rune{'A'}, Base: 'A', Mods: ModShift}, []byte{'A'}},
		{"digit", key('7', 0), []byte{'7'}},
		{"space", key(' ', 0), []byte{' '}},
		{"symbol", key('$', 0), []byte{'$'}},
		// Non-ASCII goes out as UTF-8, the same bytes the shell would see from
		// any other terminal.
		{"non-ascii", key('é', 0), []byte{0xc3, 0xa9}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Encode(c.k, Mode{}); !bytes.Equal(got, c.want) {
				t.Fatalf("got % x, want % x", got, c.want)
			}
		})
	}
}

func TestSpecialKeys(t *testing.T) {
	cases := []struct {
		name string
		code Code
		want []byte
		why  string
	}{
		{"Return", CodeReturn, []byte{0x0d}, "CR, which the line discipline turns into NL"},
		{"Tab", CodeTab, []byte{0x09}, "HT"},
		{"Escape", CodeEscape, []byte{0x1b}, "ESC"},
		{"Backspace", CodeBackspace, []byte{0x7f}, "DEL, not BS - macOS erase is ^?"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Encode(Key{Code: c.code}, Mode{})
			if !bytes.Equal(got, c.want) {
				t.Fatalf("got % x, want % x (%s)", got, c.want, c.why)
			}
		})
	}
}

func TestArrowKeysNormalMode(t *testing.T) {
	cases := []struct {
		code Code
		want string
	}{
		{CodeUp, "\x1b[A"},
		{CodeDown, "\x1b[B"},
		{CodeRight, "\x1b[C"},
		{CodeLeft, "\x1b[D"},
	}
	for _, c := range cases {
		got := Encode(Key{Code: c.code}, Mode{})
		if string(got) != c.want {
			t.Fatalf("code %d: got %q, want %q", c.code, got, c.want)
		}
	}
}

func TestArrowKeysApplicationMode(t *testing.T) {
	// After the program sends ESC[?1h, the same keys send SS3 sequences. This
	// is what makes Up recall history inside zsh's line editor.
	mode := Mode{ApplicationCursor: true}
	cases := []struct {
		code Code
		want string
	}{
		{CodeUp, "\x1bOA"},
		{CodeDown, "\x1bOB"},
		{CodeRight, "\x1bOC"},
		{CodeLeft, "\x1bOD"},
	}
	for _, c := range cases {
		got := Encode(Key{Code: c.code}, mode)
		if string(got) != c.want {
			t.Fatalf("code %d: got %q, want %q", c.code, got, c.want)
		}
	}
}

func TestControlKeys(t *testing.T) {
	cases := []struct {
		name string
		base rune
		want byte
		why  string
	}{
		{"Ctrl+C", 'c', 0x03, "ETX - the line discipline raises SIGINT"},
		{"Ctrl+D", 'd', 0x04, "EOT - end of input"},
		{"Ctrl+L", 'l', 0x0c, "FF - the shell redraws its prompt"},
		{"Ctrl+A", 'a', 0x01, "start of line in the shell"},
		{"Ctrl+Z", 'z', 0x1a, "SUB - suspends the foreground job"},
		{"Ctrl+Space", ' ', 0x00, "NUL"},
		{"Ctrl+[", '[', 0x1b, "the same byte as Escape"},
		{"Ctrl+backslash", '\\', 0x1c, "FS - raises SIGQUIT"},
		{"Ctrl+?", '?', 0x7f, "DEL - the exception to the arithmetic"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Encode(key(c.base, ModControl), Mode{})
			if len(got) != 1 || got[0] != c.want {
				t.Fatalf("got % x, want %02x (%s)", got, c.want, c.why)
			}
		})
	}
}

func TestControlIgnoresCaseAndAppKitFolding(t *testing.T) {
	// Ctrl+Shift+C is still 0x03.
	if got := Encode(key('C', ModControl|ModShift), Mode{}); len(got) != 1 || got[0] != 0x03 {
		t.Fatalf("Ctrl+Shift+C = % x, want 03", got)
	}
	// AppKit already folds Ctrl into Chars; we compute from Base, so it does
	// not matter what it put there.
	k := Key{Chars: []rune{0x03}, Base: 'c', Mods: ModControl}
	if got := Encode(k, Mode{}); len(got) != 1 || got[0] != 0x03 {
		t.Fatalf("got % x, want 03", got)
	}
}

func TestControlOnAnUnmappedKeySendsNothing(t *testing.T) {
	// Ctrl+F1, Ctrl+é: no control character exists, so send none rather than
	// something wrong.
	if got := Encode(key('é', ModControl), Mode{}); got != nil {
		t.Fatalf("got % x, want nothing", got)
	}
}

func TestCommandIsNeverSentToTheShell(t *testing.T) {
	// Cmd+Q, Cmd+C and friends belong to the application, not the terminal.
	for _, k := range []Key{
		key('q', ModCommand),
		key('c', ModCommand),
		{Code: CodeLeft, Mods: ModCommand},
	} {
		if got := Encode(k, Mode{}); got != nil {
			t.Fatalf("%+v produced % x, want nothing", k, got)
		}
	}
}

func TestUnsupportedSpecialKeysSendNothing(t *testing.T) {
	// F1, Home, Page Up: better silent than wrong.
	if got := Encode(Key{Code: CodeOther}, Mode{}); got != nil {
		t.Fatalf("got % x, want nothing", got)
	}
	if got := Encode(Key{}, Mode{}); got != nil {
		t.Fatalf("empty key produced % x, want nothing", got)
	}
}

func TestKeypadEnterSendsReturn(t *testing.T) {
	// The numeric keypad's Enter arrives as 0x03, the same byte as Ctrl+C.
	// Without Control held it can only be Enter.
	k := Key{Chars: []rune{0x03}, Base: 0x03}
	if got := Encode(k, Mode{}); len(got) != 1 || got[0] != 0x0d {
		t.Fatalf("got % x, want 0d", got)
	}
}

func TestModifiersHas(t *testing.T) {
	m := ModControl | ModShift
	if !m.Has(ModControl) || !m.Has(ModShift) || m.Has(ModCommand) {
		t.Fatalf("Has is wrong for %b", m)
	}
}

func TestPagingAndHomeEndKeys(t *testing.T) {
	cases := []struct {
		name string
		code Code
		mode Mode
		want string
	}{
		{"page up", CodePageUp, Mode{}, "\x1b[5~"},
		{"page down", CodePageDown, Mode{}, "\x1b[6~"},
		{"home", CodeHome, Mode{}, "\x1b[H"},
		{"end", CodeEnd, Mode{}, "\x1b[F"},
		// Home and End follow the cursor-key mode, exactly like the arrows.
		{"home in application mode", CodeHome, Mode{ApplicationCursor: true}, "\x1bOH"},
		{"end in application mode", CodeEnd, Mode{ApplicationCursor: true}, "\x1bOF"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(Encode(Key{Code: c.code}, c.mode)); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

// With Option as Meta, Option+key is ESC then the key - letters, Delete,
// Return - while arrows keep their modifier parameter. Without it, Option
// types what macOS composed.
func TestOptionAsMeta(t *testing.T) {
	meta := Mode{OptionAsMeta: true}
	cases := []struct {
		key  Key
		want string
	}{
		{Key{Chars: []rune{'b'}, Base: 'b', Mods: ModOption}, "\x1bb"},
		{Key{Chars: []rune{'B'}, Base: 'b', Mods: ModOption | ModShift}, "\x1bB"},
		{Key{Code: CodeBackspace, Base: 0x7f, Mods: ModOption}, "\x1b\x7f"},
		{Key{Code: CodeReturn, Base: 0x0d, Mods: ModOption}, "\x1b\r"},
		{Key{Chars: []rune{'c'}, Base: 'c', Mods: ModOption | ModControl}, "\x1b\x03"},
		{Key{Code: CodeLeft, Mods: ModOption}, "\x1b[1;3D"},
	}
	for _, c := range cases {
		if got := string(Encode(c.key, meta)); got != c.want {
			t.Errorf("Encode(%+v) = %q, want %q", c.key, got, c.want)
		}
	}
	if got := string(Encode(Key{Chars: []rune{'∫'}, Base: 'b', Mods: ModOption}, Mode{})); got != "∫" {
		t.Errorf("without Option as Meta, Option+B = %q, want the composed ∫", got)
	}
}

// In application keypad mode the keypad names its keys; otherwise it types
// what is printed on them, and the rest of the keyboard is never affected.
func TestApplicationKeypad(t *testing.T) {
	app := Mode{ApplicationKeypad: true}
	pad := func(r rune) Key { return Key{Chars: []rune{r}, Base: r, Keypad: true} }
	for r, want := range map[rune]string{
		'0': "\x1bOp", '5': "\x1bOu", '9': "\x1bOy", '.': "\x1bOn", '+': "\x1bOk",
		'-': "\x1bOm", '*': "\x1bOj", '/': "\x1bOo", '=': "\x1bOX", 0x03: "\x1bOM",
	} {
		if got := string(Encode(pad(r), app)); got != want {
			t.Errorf("keypad %q = %q, want %q", r, got, want)
		}
	}
	if got := string(Encode(pad('5'), Mode{})); got != "5" {
		t.Errorf("numeric mode: keypad 5 = %q", got)
	}
	if got := string(Encode(pad(0x03), Mode{})); got != "\r" {
		t.Errorf("numeric mode: keypad Enter = %q", got)
	}
	if got := string(Encode(Key{Chars: []rune{'5'}, Base: '5'}, app)); got != "5" {
		t.Errorf("the 5 above the letters = %q", got)
	}
	ctrl := pad('5')
	ctrl.Mods = ModControl
	if got := string(Encode(ctrl, app)); got == "\x1bOu" {
		t.Errorf("Ctrl+keypad 5 sent the application sequence")
	}
}
