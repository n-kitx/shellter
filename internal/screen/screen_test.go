package screen

import (
	"testing"

	"github.com/thakurankit7/shellter/internal/parser"
)

// feed runs bytes through the real parser into the screen. Tests are written
// against byte strings rather than hand-built events because that is the
// actual pipeline: bytes -> parser -> events -> screen.
func feed(s *Screen, data string) {
	parser.New().Parse([]byte(data), s.Apply)
}

func assertCursor(t *testing.T, s *Screen, row, col int) {
	t.Helper()
	if got := s.Cursor(); got.Row != row || got.Col != col {
		t.Fatalf("cursor = %+v, want {Row:%d Col:%d}", got, row, col)
	}
}

func assertLine(t *testing.T, s *Screen, row int, want string) {
	t.Helper()
	if got := s.Line(row); got != want {
		t.Fatalf("line %d = %q, want %q", row, got, want)
	}
}

func TestPrintAdvancesCursor(t *testing.T) {
	s := New(5, 10)
	feed(s, "Hello")

	assertLine(t, s, 0, "Hello")
	assertCursor(t, s, 0, 5)
	if c := s.Cell(0, 0); c.Rune != 'H' {
		t.Fatalf("cell(0,0) = %q, want 'H'", c.Rune)
	}
	if c := s.Cell(0, 5); c != Blank {
		t.Fatalf("cell(0,5) = %q, want blank", c.Rune)
	}
}

func TestNewlineAndCarriageReturn(t *testing.T) {
	s := New(5, 10)

	// LF moves down but keeps the column - that is why a shell always sends
	// CR LF, not just LF.
	feed(s, "abc\ndef")
	assertLine(t, s, 0, "abc")
	assertLine(t, s, 1, "   def")
	assertCursor(t, s, 1, 6)

	s.Reset()
	feed(s, "abc\r\ndef")
	assertLine(t, s, 0, "abc")
	assertLine(t, s, 1, "def")
	assertCursor(t, s, 1, 3)
}

func TestCarriageReturnOverwrites(t *testing.T) {
	s := New(2, 10)
	feed(s, "12345\rab")

	assertLine(t, s, 0, "ab345")
	assertCursor(t, s, 0, 2)
}

func TestBackspaceMovesWithoutErasing(t *testing.T) {
	s := New(2, 10)
	feed(s, "abc\b")

	// BS only moves; the character is still there until something overwrites it.
	assertLine(t, s, 0, "abc")
	assertCursor(t, s, 0, 2)

	// The usual "erase a character" idiom a shell sends: BS, space, BS.
	feed(s, " \b")
	assertLine(t, s, 0, "ab")
	assertCursor(t, s, 0, 2)
}

func TestBackspaceStopsAtLeftMargin(t *testing.T) {
	s := New(2, 10)
	feed(s, "\b\b\b")
	assertCursor(t, s, 0, 0)
}

func TestCursorMovementCSI(t *testing.T) {
	s := New(10, 20)
	feed(s, "\x1b[5;5H") // row 5, column 5, one-based
	assertCursor(t, s, 4, 4)

	feed(s, "\x1b[2A") // up 2
	assertCursor(t, s, 2, 4)
	feed(s, "\x1b[3B") // down 3
	assertCursor(t, s, 5, 4)
	feed(s, "\x1b[4C") // right 4
	assertCursor(t, s, 5, 8)
	feed(s, "\x1b[2D") // left 2
	assertCursor(t, s, 5, 6)
}

func TestCursorMovementDefaultsToOne(t *testing.T) {
	s := New(10, 20)
	feed(s, "\x1b[5;5H\x1b[A\x1b[C")
	assertCursor(t, s, 3, 5)

	// An explicit 0 also means 1, per the protocol.
	feed(s, "\x1b[0B")
	assertCursor(t, s, 4, 5)
}

func TestCursorMovementClampsToScreen(t *testing.T) {
	s := New(4, 8)
	feed(s, "\x1b[99A\x1b[99D")
	assertCursor(t, s, 0, 0)
	feed(s, "\x1b[99B\x1b[99C")
	assertCursor(t, s, 3, 7)
	feed(s, "\x1b[99;99H")
	assertCursor(t, s, 3, 7)
}

func TestCursorPositionDefaultsToHome(t *testing.T) {
	s := New(4, 8)
	feed(s, "\x1b[3;3H")
	feed(s, "\x1b[H") // no parameters: home
	assertCursor(t, s, 0, 0)

	feed(s, "\x1b[2;4f") // 'f' is the same command as 'H'
	assertCursor(t, s, 1, 3)
}

func TestPrintingIsWrittenWhereTheCursorWasMoved(t *testing.T) {
	s := New(3, 10)
	feed(s, "\x1b[2;3HX")

	assertLine(t, s, 1, "  X")
	assertCursor(t, s, 1, 3)
}

func TestEraseInLine(t *testing.T) {
	// K0: cursor to end of line.
	s := New(2, 10)
	feed(s, "abcdef\x1b[1;3H\x1b[0K")
	assertLine(t, s, 0, "ab")
	assertCursor(t, s, 0, 2) // erasing never moves the cursor

	// K1: start of line to cursor, inclusive.
	s = New(2, 10)
	feed(s, "abcdef\x1b[1;3H\x1b[1K")
	assertLine(t, s, 0, "   def")

	// K2: the whole line.
	s = New(2, 10)
	feed(s, "abcdef\x1b[1;3H\x1b[2K")
	assertLine(t, s, 0, "")
}

func TestEraseInLineTouchesOnlyOneRow(t *testing.T) {
	s := New(3, 10)
	feed(s, "aaa\r\nbbb\r\nccc")
	feed(s, "\x1b[2;1H\x1b[2K")

	assertLine(t, s, 0, "aaa")
	assertLine(t, s, 1, "")
	assertLine(t, s, 2, "ccc")
}

func TestEraseInDisplay(t *testing.T) {
	setup := func() *Screen {
		s := New(3, 5)
		feed(s, "aaaaa\r\nbbbbb\r\nccccc")
		feed(s, "\x1b[2;3H") // middle row, middle column
		return s
	}

	// J0: cursor to end of screen.
	s := setup()
	feed(s, "\x1b[0J")
	assertLine(t, s, 0, "aaaaa")
	assertLine(t, s, 1, "bb")
	assertLine(t, s, 2, "")
	assertCursor(t, s, 1, 2)

	// J1: start of screen to cursor, inclusive.
	s = setup()
	feed(s, "\x1b[1J")
	assertLine(t, s, 0, "")
	assertLine(t, s, 1, "   bb")
	assertLine(t, s, 2, "ccccc")

	// J2: everything. The cursor stays put, as in xterm.
	s = setup()
	feed(s, "\x1b[2J")
	if got := s.String(); got != "" {
		t.Fatalf("screen = %q, want empty", got)
	}
	assertCursor(t, s, 1, 2)

	// The "clear the screen and go home" pair that `clear` actually sends.
	s = setup()
	feed(s, "\x1b[2J\x1b[H")
	assertCursor(t, s, 0, 0)
}

func TestPrintWrapsAtRightEdge(t *testing.T) {
	s := New(3, 4)
	feed(s, "abcdef")

	assertLine(t, s, 0, "abcd")
	assertLine(t, s, 1, "ef")
	assertCursor(t, s, 1, 2)
}

func TestLineFeedScrollsAtTheBottom(t *testing.T) {
	s := New(3, 8)
	feed(s, "one\r\ntwo\r\nthree")
	assertCursor(t, s, 2, 5)

	feed(s, "\r\nfour")

	// The top line is gone, everything moved up one row.
	assertLine(t, s, 0, "two")
	assertLine(t, s, 1, "three")
	assertLine(t, s, 2, "four")
	assertCursor(t, s, 2, 4)
}

func TestWrapIsDeferredUntilTheNextCharacter(t *testing.T) {
	s := New(3, 4)
	feed(s, "abcd") // exactly fills the row

	// The cursor stays on the last column instead of jumping to the next row:
	// a full line must not scroll the screen until more text actually arrives.
	assertCursor(t, s, 0, 3)
	assertLine(t, s, 0, "abcd")

	feed(s, "e")
	assertCursor(t, s, 1, 1)
	assertLine(t, s, 1, "e")

	// A CR cancels the pending wrap rather than wrapping.
	s.Reset()
	feed(s, "abcd\rZ")
	assertLine(t, s, 0, "Zbcd")
	assertLine(t, s, 1, "")
}

func TestWrapAtBottomRightScrolls(t *testing.T) {
	s := New(2, 3)
	feed(s, "abc\r\ndef") // fills both rows exactly
	assertLine(t, s, 0, "abc")
	assertLine(t, s, 1, "def")

	feed(s, "g")
	assertLine(t, s, 0, "def")
	assertLine(t, s, 1, "g")
	assertCursor(t, s, 1, 1)
}

func TestUnhandledEventsAreIgnored(t *testing.T) {
	s := New(2, 10)
	// SGR color, a window title, keypad mode: recognised by the parser,
	// meaningless to this phase's screen.
	feed(s, "\x1b[31mred\x1b]0;title\x07\x1b=!")

	assertLine(t, s, 0, "red!")
	assertCursor(t, s, 0, 4)
}

func TestStateSurvivesSplitWrites(t *testing.T) {
	// The parser is stateful across reads, so the screen sees the same events
	// whether or not a sequence is torn by a read boundary.
	whole := New(3, 10)
	feed(whole, "ab\x1b[2;5Hcd")

	split := New(3, 10)
	p := parser.New()
	for _, chunk := range []string{"ab\x1b", "[2", ";5H", "cd"} {
		p.Parse([]byte(chunk), split.Apply)
	}

	if whole.String() != split.String() || whole.Cursor() != split.Cursor() {
		t.Fatalf("split feed differs: %q %+v vs %q %+v",
			split.String(), split.Cursor(), whole.String(), whole.Cursor())
	}
	assertLine(t, split, 1, "    cd")
}

func TestResizeKeepsContentAndClampsCursor(t *testing.T) {
	s := New(4, 10)
	feed(s, "hello\r\nworld")
	assertCursor(t, s, 1, 5)

	s.Resize(2, 4)
	rows, cols := s.Size()
	if rows != 2 || cols != 4 {
		t.Fatalf("size = %dx%d, want 2x4", rows, cols)
	}
	assertLine(t, s, 0, "hell")
	assertLine(t, s, 1, "worl")
	assertCursor(t, s, 1, 3)
}

func TestStringDropsTrailingBlankLines(t *testing.T) {
	s := New(5, 10)
	feed(s, "a\r\nb")
	if got, want := s.String(), "a\nb"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestSnapshotCopiesGridAndCursor(t *testing.T) {
	s := New(3, 5)
	feed(s, "ab\r\ncd")

	snap := s.Snapshot()
	if snap.Rows != 3 || snap.Cols != 5 {
		t.Fatalf("snapshot size = %dx%d, want 3x5", snap.Rows, snap.Cols)
	}
	if len(snap.Lines()) != 3 {
		t.Fatalf("got %d lines, want 3", len(snap.Lines()))
	}
	// Lines are full width - a renderer wants a rectangle, not trimmed text.
	if snap.Lines()[0] != "ab   " || snap.Lines()[1] != "cd   " || snap.Lines()[2] != "     " {
		t.Fatalf("lines = %q", snap.Lines())
	}
	if snap.Cursor != (Cursor{Row: 1, Col: 2}) {
		t.Fatalf("cursor = %+v, want {1 2}", snap.Cursor)
	}

	// The snapshot must not change when the screen does afterwards.
	feed(s, "XYZ")
	if snap.Lines()[1] != "cd   " {
		t.Fatalf("snapshot was mutated: %q", snap.Lines()[1])
	}
}

func TestApplicationCursorKeysMode(t *testing.T) {
	s := New(2, 10)
	if s.ApplicationCursorKeys() {
		t.Fatal("DECCKM should start off")
	}

	feed(s, "\x1b[?1h") // DECSET 1: zsh's line editor sends this
	if !s.ApplicationCursorKeys() {
		t.Fatal("ESC[?1h should set application cursor keys")
	}

	feed(s, "\x1b[?1l") // DECRST 1
	if s.ApplicationCursorKeys() {
		t.Fatal("ESC[?1l should clear application cursor keys")
	}

	// Other private modes must not touch it, and a non-private h must not either.
	feed(s, "\x1b[?1h\x1b[?2004l\x1b[?25l\x1b[1l")
	if !s.ApplicationCursorKeys() {
		t.Fatal("unrelated modes changed DECCKM")
	}

	// It must not disturb the grid or the cursor.
	s.Reset()
	feed(s, "ab\x1b[?1hc")
	assertLine(t, s, 0, "abc")
	assertCursor(t, s, 0, 3)
}
