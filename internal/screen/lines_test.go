package screen

import (
	"strings"
	"testing"
)

func feedLines(s *Screen, text string) {
	feed(s, text)
}

func TestLineNumbersNeverGoBackwards(t *testing.T) {
	s := NewWithScrollback(3, 10, 2) // room for two lines of history

	if s.TopLine() != 0 || s.TotalLines() != 3 {
		t.Fatalf("a fresh screen starts at 0..%d, got %d..%d", 3, s.TopLine(), s.TotalLines())
	}

	// Six lines through a three-row screen with two of history: the first
	// ones are evicted, and their numbers are not handed out again.
	for i := 1; i <= 6; i++ {
		feedLines(s, "line"+string(rune('0'+i))+"\r\n")
	}

	// Four lines were archived and two of those evicted, so numbering starts
	// at 2 even though nothing can read line 0 or 1 any more.
	if got := s.FirstLine(); got != 2 {
		t.Fatalf("the oldest readable line is %d, want 2", got)
	}
	if got := s.TotalLines(); got != 7 {
		t.Fatalf("TotalLines = %d, want 7", got)
	}
	if got := s.TopLine(); got != 4 {
		t.Fatalf("TopLine = %d, want 4", got)
	}
}

func TestTopLineFollowsScrolling(t *testing.T) {
	s := NewWithScrollback(3, 10, 10)
	for i := 1; i <= 6; i++ {
		feedLines(s, "line"+string(rune('0'+i))+"\r\n")
	}
	live := s.TopLine()

	s.ScrollBy(2)
	if got := s.TopLine(); got != live-2 {
		t.Fatalf("after scrolling back 2, TopLine = %d, want %d", got, live-2)
	}
	s.ScrollToBottom()
	if got := s.TopLine(); got != live {
		t.Fatalf("back at the bottom, TopLine = %d, want %d", got, live)
	}
}

func TestTextReadsAcrossHistoryAndGrid(t *testing.T) {
	s := NewWithScrollback(3, 10, 10)
	for _, line := range []string{"alpha", "bravo", "charlie", "delta"} {
		feedLines(s, line+"\r\n")
	}
	// "alpha" is line 0; "delta" is on the live grid.
	got := s.Text(Pos{Line: 0}, Pos{Line: 3, Col: 10})
	if got != "alpha\nbravo\ncharlie\ndelta" {
		t.Fatalf("Text = %q", got)
	}
}

func TestTextDropsTrailingBlanksButKeepsInnerOnes(t *testing.T) {
	s := NewWithScrollback(2, 20, 10)
	feedLines(s, "a  b      \r\n")

	if got := s.Text(Pos{Line: 0}, Pos{Line: 0, Col: 20}); got != "a  b" {
		t.Fatalf("Text = %q, want the padding gone and the gap kept", got)
	}
}

func TestTextJoinsAWrappedLine(t *testing.T) {
	s := NewWithScrollback(4, 6, 10)
	// Twelve characters through a six-column screen: two rows, one line.
	feedLines(s, "abcdefghijkl")

	got := s.Text(Pos{Line: 0}, Pos{Line: 1, Col: 6})
	if got != "abcdefghijkl" {
		t.Fatalf("Text = %q, want the wrap joined up", got)
	}
}

func TestTextSeparatesLinesThatReallyEnded(t *testing.T) {
	s := NewWithScrollback(4, 6, 10)
	feedLines(s, "abc\r\ndef\r\n")

	if got := s.Text(Pos{Line: 0}, Pos{Line: 1, Col: 6}); got != "abc\ndef" {
		t.Fatalf("Text = %q, want a newline between them", got)
	}
}

// A shell redrawing a line shorter than it was must not leave it looking
// wrapped: "\r", the new text, then erase to the end of the line.
func TestRedrawingALineShorterEndsTheWrap(t *testing.T) {
	s := NewWithScrollback(4, 6, 10)
	feedLines(s, "abcdefghijkl") // wraps; the cursor ends up on the second row
	feedLines(s, "\x1b[1;1Hshort\x1b[K")

	if s.rowWrapped(0) {
		t.Fatal("row 0 still claims to wrap after being erased to the end")
	}
}

func TestTextClampsToWhatIsStillHeld(t *testing.T) {
	s := NewWithScrollback(2, 10, 1)
	for _, line := range []string{"one", "two", "three", "four"} {
		feedLines(s, line+"\r\n")
	}
	// Ask for everything from the beginning of the session; only what
	// scrollback still holds, plus the grid, can come back.
	got := s.Text(Pos{Line: 0}, Pos{Line: s.TotalLines()})
	if strings.Contains(got, "one") {
		t.Fatalf("Text returned an evicted line: %q", got)
	}
	if !strings.Contains(got, "four") {
		t.Fatalf("Text lost the live grid: %q", got)
	}
}

func TestTextIsEmptyWhenTheRangeIsBackwardsOrEmpty(t *testing.T) {
	s := NewWithScrollback(3, 10, 10)
	feedLines(s, "hello\r\n")

	if got := s.Text(Pos{Line: 1}, Pos{Line: 0}); got != "" {
		t.Fatalf("a backwards range returned %q", got)
	}
	if got := s.Text(Pos{Line: 0, Col: 2}, Pos{Line: 0, Col: 2}); got != "" {
		t.Fatalf("an empty range returned %q", got)
	}
}

func TestTextTakesPartOfALine(t *testing.T) {
	s := NewWithScrollback(3, 20, 10)
	feedLines(s, "hello world\r\n")

	if got := s.Text(Pos{Line: 0, Col: 6}, Pos{Line: 0, Col: 11}); got != "world" {
		t.Fatalf("Text = %q, want %q", got, "world")
	}
}

// Stopping at the start of the next line means the newline at the end of this
// one was selected, so it comes back. That is what makes selecting three whole
// lines and pasting them produce three lines.
func TestSelectingToTheStartOfTheNextLineKeepsTheNewline(t *testing.T) {
	s := NewWithScrollback(3, 10, 10)
	feedLines(s, "abc\r\ndef\r\n")

	if got := s.Text(Pos{Line: 0}, Pos{Line: 1, Col: 0}); got != "abc\n" {
		t.Fatalf("Text = %q, want %q", got, "abc\n")
	}
}

// A block is the same columns of every line, a line of text per row.
func TestBlockText(t *testing.T) {
	s := New(4, 12)
	for _, l := range []string{"id  name   \r\n", "1   alpha\r\n", "22  b\r\n", "333 charlie!"} {
		feed(s, l)
	}
	top := s.TopLine()
	if got, want := s.BlockText(top, top+3, 4, 9), "name\nalpha\nb\ncharl"; got != want {
		t.Fatalf("block = %q, want %q", got, want)
	}
	if got := s.BlockText(top, top+3, 0, 3); got != "id\n1\n22\n333" {
		t.Fatalf("first column = %q", got)
	}
	if got := s.BlockText(top+2, top+1, 0, 3); got != "" {
		t.Fatalf("an upside-down block gave %q", got)
	}
}
