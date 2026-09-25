package screen

import "testing"

// Phase 6: the screen no longer just recognises sequences, it obeys them.

func TestSGRBasicColors(t *testing.T) {
	s := New(2, 10)
	feed(s, "\x1b[31mR\x1b[42mG\x1b[0mP")

	if got := s.Cell(0, 0).Attr.FG; got != Indexed(1) {
		t.Fatalf("fg = %+v, want indexed 1 (red)", got)
	}
	// The pen is sticky: 'G' is still red, and now on green.
	if got := s.Cell(0, 1).Attr; got.FG != Indexed(1) || got.BG != Indexed(2) {
		t.Fatalf("attr = %+v, want red on green", got)
	}
	// SGR 0 resets everything.
	if got := s.Cell(0, 2).Attr; got != (Attr{}) {
		t.Fatalf("attr = %+v, want the default pen", got)
	}
}

func TestSGRBrightColors(t *testing.T) {
	s := New(2, 10)
	feed(s, "\x1b[91mA\x1b[104mB")

	if got := s.Cell(0, 0).Attr.FG; got != Indexed(9) {
		t.Fatalf("fg = %+v, want indexed 9 (bright red)", got)
	}
	if got := s.Cell(0, 1).Attr.BG; got != Indexed(12) {
		t.Fatalf("bg = %+v, want indexed 12 (bright blue)", got)
	}
}

func TestSGRStyles(t *testing.T) {
	s := New(2, 20)
	feed(s, "\x1b[1;4mA\x1b[22mB\x1b[24mC\x1b[3;7mD")

	if a := s.Cell(0, 0).Attr; !a.Flags.Has(FlagBold) || !a.Flags.Has(FlagUnderline) {
		t.Fatalf("flags = %b, want bold+underline", a.Flags)
	}
	// 22 clears bold (and faint) but leaves underline alone.
	if a := s.Cell(0, 1).Attr; a.Flags.Has(FlagBold) || !a.Flags.Has(FlagUnderline) {
		t.Fatalf("flags = %b, want underline only", a.Flags)
	}
	if a := s.Cell(0, 2).Attr; a.Flags != 0 {
		t.Fatalf("flags = %b, want none", a.Flags)
	}
	if a := s.Cell(0, 3).Attr; !a.Flags.Has(FlagItalic) || !a.Flags.Has(FlagInverse) {
		t.Fatalf("flags = %b, want italic+inverse", a.Flags)
	}
}

func TestSGRDefaultsAndEmptySequence(t *testing.T) {
	s := New(2, 10)
	feed(s, "\x1b[31;42mA\x1b[39mB\x1b[49mC")

	if got := s.Cell(0, 1).Attr.FG; got != (Color{}) {
		t.Fatalf("39 should restore the default fg, got %+v", got)
	}
	if got := s.Cell(0, 1).Attr.BG; got != Indexed(2) {
		t.Fatalf("39 should leave the bg alone, got %+v", got)
	}
	if got := s.Cell(0, 2).Attr.BG; got != (Color{}) {
		t.Fatalf("49 should restore the default bg, got %+v", got)
	}

	// "ESC [ m" with no parameters is a full reset.
	feed(s, "\x1b[1;31m\x1b[mZ")
	if got := s.Cell(0, 3).Attr; got != (Attr{}) {
		t.Fatalf("attr = %+v, want the default pen", got)
	}
}

func TestSGR256AndTrueColor(t *testing.T) {
	s := New(2, 10)
	feed(s, "\x1b[38;5;196mA\x1b[48;5;21mB\x1b[38;2;10;20;30mC")

	if got := s.Cell(0, 0).Attr.FG; got != Indexed(196) {
		t.Fatalf("fg = %+v, want indexed 196", got)
	}
	if got := s.Cell(0, 1).Attr.BG; got != Indexed(21) {
		t.Fatalf("bg = %+v, want indexed 21", got)
	}
	if got := s.Cell(0, 2).Attr.FG; got != RGB(10, 20, 30) {
		t.Fatalf("fg = %+v, want rgb(10,20,30)", got)
	}

	// A combined sequence: the 38;2;... parameters must not be read as styles.
	s.Reset()
	feed(s, "\x1b[1;38;2;1;2;3;4mX")
	a := s.Cell(0, 0).Attr
	if a.FG != RGB(1, 2, 3) {
		t.Fatalf("fg = %+v, want rgb(1,2,3)", a.FG)
	}
	if !a.Flags.Has(FlagBold) || !a.Flags.Has(FlagUnderline) {
		t.Fatalf("flags = %b, want bold+underline around the colour", a.Flags)
	}
}

func TestSGRTruncatedExtendedColorIsIgnored(t *testing.T) {
	s := New(2, 10)
	feed(s, "\x1b[38;5mA") // missing the index
	if got := s.Cell(0, 0).Attr.FG; got != (Color{}) {
		t.Fatalf("fg = %+v, want the default after a malformed sequence", got)
	}
}

func TestErasingUsesTheCurrentBackground(t *testing.T) {
	// Setting a background and clearing is how a program paints a region.
	s := New(2, 4)
	feed(s, "\x1b[41m\x1b[2J")

	for col := 0; col < 4; col++ {
		if got := s.Cell(0, col).Attr.BG; got != Indexed(1) {
			t.Fatalf("cell(0,%d) bg = %+v, want indexed 1", col, got)
		}
		if got := s.Cell(0, col).Rune; got != ' ' {
			t.Fatalf("cell(0,%d) = %q, want a blank", col, got)
		}
	}
	// Only the background carries over - not bold, not the foreground.
	if got := s.Cell(0, 0).Attr.Flags; got != 0 {
		t.Fatalf("flags = %b, want none on an erased cell", got)
	}
}

func TestEraseCharacters(t *testing.T) {
	s := New(2, 10)
	feed(s, "abcdef\x1b[1;2H\x1b[3X") // ECH 3 from column 2

	assertLine(t, s, 0, "a   ef")
	assertCursor(t, s, 0, 1) // ECH does not move the cursor
}

func TestAbsoluteCursorMovement(t *testing.T) {
	s := New(6, 20)

	feed(s, "\x1b[3;5H")
	assertCursor(t, s, 2, 4)

	feed(s, "\x1b[10G") // CHA - column only
	assertCursor(t, s, 2, 9)

	feed(s, "\x1b[5d") // VPA - row only
	assertCursor(t, s, 4, 9)

	feed(s, "\x1b[2E")       // CNL - two lines down, column 0
	assertCursor(t, s, 5, 0) // clamped to the last row

	feed(s, "\x1b[3F") // CPL - three lines up, column 0
	assertCursor(t, s, 2, 0)
}

func TestSaveAndRestoreCursor(t *testing.T) {
	for _, pair := range []struct{ save, restore string }{
		{"\x1b7", "\x1b8"},   // DECSC / DECRC
		{"\x1b[s", "\x1b[u"}, // the ANSI.SYS spelling
	} {
		s := New(5, 20)
		feed(s, "\x1b[3;5H\x1b[31m"+pair.save)
		feed(s, "\x1b[1;1H\x1b[32mX")
		feed(s, pair.restore+"Y")

		assertCursor(t, s, 2, 5)
		// The pen came back too, so Y is red, not green.
		if got := s.Cell(2, 4).Attr.FG; got != Indexed(1) {
			t.Fatalf("%q: fg = %+v, want red restored", pair.restore, got)
		}
	}
}

func TestReverseIndexScrollsAtTheTop(t *testing.T) {
	s := New(3, 6)
	feed(s, "one\r\ntwo\r\nthree")

	feed(s, "\x1b[1;1H\x1bM") // home, then RI
	assertLine(t, s, 0, "")
	assertLine(t, s, 1, "one")
	assertLine(t, s, 2, "two")
	assertCursor(t, s, 0, 0)

	// Away from the top it is just "up one line".
	feed(s, "\x1b[3;1H\x1bM")
	assertCursor(t, s, 1, 0)
}

func TestIndexAndNextLine(t *testing.T) {
	s := New(3, 6)
	feed(s, "ab\x1bD") // IND keeps the column
	assertCursor(t, s, 1, 2)

	feed(s, "\x1bE") // NEL returns to column 0
	assertCursor(t, s, 2, 0)
}

func TestCursorVisibilityMode(t *testing.T) {
	s := New(2, 5)
	if !s.Modes().CursorVisible {
		t.Fatal("the cursor should start visible")
	}
	feed(s, "\x1b[?25l")
	if s.Modes().CursorVisible {
		t.Fatal("ESC[?25l should hide the cursor")
	}
	feed(s, "\x1b[?25h")
	if !s.Modes().CursorVisible {
		t.Fatal("ESC[?25h should show it again")
	}
}

func TestAutoWrapMode(t *testing.T) {
	s := New(3, 4)
	if !s.Modes().AutoWrap {
		t.Fatal("autowrap should default to on")
	}

	feed(s, "\x1b[?7l") // DECAWM off
	feed(s, "abcdef")

	// Without autowrap the last column is overwritten again and again.
	assertLine(t, s, 0, "abcf")
	assertLine(t, s, 1, "")
	assertCursor(t, s, 0, 3)
}

func TestBracketedPasteMode(t *testing.T) {
	s := New(2, 5)
	feed(s, "\x1b[?2004h")
	if !s.Modes().BracketedPaste {
		t.Fatal("ESC[?2004h should set bracketed paste")
	}
	feed(s, "\x1b[?2004l")
	if s.Modes().BracketedPaste {
		t.Fatal("ESC[?2004l should clear it")
	}
}

func TestFullReset(t *testing.T) {
	s := New(3, 6)
	feed(s, "\x1b[31mhello\x1b[?25l\x1b[2;3H")

	feed(s, "\x1bc") // RIS

	if got := s.String(); got != "" {
		t.Fatalf("screen = %q, want empty after RIS", got)
	}
	assertCursor(t, s, 0, 0)
	if s.Pen() != (Attr{}) {
		t.Fatalf("pen = %+v, want default after RIS", s.Pen())
	}
	if !s.Modes().CursorVisible {
		t.Fatal("RIS should restore cursor visibility")
	}
}

func TestSnapshotCarriesAttributesAndModes(t *testing.T) {
	s := New(2, 4)
	feed(s, "\x1b[1;31mAB\x1b[?25l")

	snap := s.Snapshot()
	if len(snap.Cells) != 8 {
		t.Fatalf("got %d cells, want 8", len(snap.Cells))
	}
	if c := snap.CellAt(0, 0); c.Rune != 'A' || c.Attr.FG != Indexed(1) || !c.Attr.Flags.Has(FlagBold) {
		t.Fatalf("cell = %+v, want bold red A", c)
	}
	if snap.Modes.CursorVisible {
		t.Fatal("the snapshot should report the cursor hidden")
	}

	// Still an independent copy.
	feed(s, "\x1b[0m\x1b[1;1HZ")
	if snap.CellAt(0, 0).Rune != 'A' {
		t.Fatal("the snapshot was mutated")
	}
}

// OSC 7 is how a shell says where it is. The path is a URL's, so it arrives
// percent-encoded; the host is kept for the caller to judge.
func TestOSC7RecordsTheDirectory(t *testing.T) {
	s := New(3, 20)
	if host, path := s.Directory(); host != "" || path != "" {
		t.Fatalf("a fresh screen knows a directory: %q %q", host, path)
	}

	feed(s, "\x1b]7;file://my-mac.local/Users/me/My%20Project\x07")
	if host, path := s.Directory(); host != "my-mac.local" || path != "/Users/me/My Project" {
		t.Fatalf("Directory() = %q, %q", host, path)
	}

	// ST-terminated works as well as BEL, and an empty host is allowed.
	feed(s, "\x1b]7;file:///tmp\x1b\\")
	if host, path := s.Directory(); host != "" || path != "/tmp" {
		t.Fatalf("Directory() = %q, %q", host, path)
	}

	// Anything that is not a file URL with an absolute path is ignored, and
	// the last good report stands.
	for _, bad := range []string{"http://x/y", "file://host", "not a url %zz"} {
		feed(s, "\x1b]7;"+bad+"\x07")
		if _, path := s.Directory(); path != "/tmp" {
			t.Fatalf("%q replaced the directory with %q", bad, path)
		}
	}

	// It survives a reset: clearing the screen does not move the shell.
	feed(s, "\x1bc")
	if _, path := s.Directory(); path != "/tmp" {
		t.Fatalf("RIS lost the directory: %q", path)
	}
}
