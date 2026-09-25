package screen

import (
	"strings"
	"testing"
)

// The bug that asked for reflow: a pane is split (narrowed), then the split is
// closed (widened back). Every line must come back whole, not cut at the width
// it was squeezed to.
func TestNarrowThenWidenRestoresLines(t *testing.T) {
	s := NewWithScrollback(6, 40, 100)
	feed(s, "/Users/me/.zshrc:124: operation not permitted\r\n$ ")

	s.Resize(6, 20)
	assertLine(t, s, 0, "/Users/me/.zshrc:124")
	assertLine(t, s, 1, ": operation not perm")
	assertLine(t, s, 2, "itted")

	s.Resize(6, 40)
	assertLine(t, s, 0, "/Users/me/.zshrc:124: operation not perm")
	assertLine(t, s, 1, "itted")
	assertLine(t, s, 2, "$")
	assertCursor(t, s, 2, 2)

	// And typing carries on where the prompt is.
	feed(s, "ls")
	assertLine(t, s, 2, "$ ls")
}

// Widening joins what wrapping split, including lines already in history.
func TestReflowJoinsHistory(t *testing.T) {
	s := NewWithScrollback(2, 5, 100)
	feed(s, "abcdefgh\r\n12345678\r\n$ ")
	// At 5 columns: abcde fgh 12345 678 "$ " - three rows went into history.
	if s.ScrollbackLen() != 3 {
		t.Fatalf("setup: history has %d lines", s.ScrollbackLen())
	}

	s.Resize(2, 10)
	var history []string
	for n := s.FirstLine(); n < s.FirstLine()+int64(s.ScrollbackLen()); n++ {
		history = append(history, strings.TrimRight(string(s.LineRunes(n, nil)), " "))
	}
	if got := strings.Join(history, "|"); got != "abcdefgh" {
		t.Fatalf("history = %q, want the first line joined back into one", got)
	}
	assertLine(t, s, 0, "12345678")
	assertLine(t, s, 1, "$")
}

// Widening does not pull history back over a screen the user cleared.
func TestReflowKeepsAClearedScreenClear(t *testing.T) {
	s := NewWithScrollback(4, 10, 100)
	feed(s, "one\r\ntwo\r\nthree\r\nfour\r\nfive\r\n")
	feed(s, "\x1b[H\x1b[2J$ ")

	s.Resize(4, 20)
	assertLine(t, s, 0, "$")
	for r := 1; r < 4; r++ {
		assertLine(t, s, r, "")
	}
}

// A cursor at the right edge, waiting to wrap, is one past the last cell:
// after a reflow the next character must not overwrite it.
func TestReflowRespectsAPendingWrap(t *testing.T) {
	s := New(3, 5)
	feed(s, "abcde") // fills the row; the cursor waits on the e
	s.Resize(3, 10)
	feed(s, "f")
	assertLine(t, s, 0, "abcdef")
}

// Height alone changing still keeps the cursor's row: shrinking the window
// pushes the top into history rather than cutting off the prompt at the bottom.
func TestShorterWindowKeepsThePrompt(t *testing.T) {
	s := NewWithScrollback(5, 10, 100)
	feed(s, "1\r\n2\r\n3\r\n4\r\n$ ")
	s.Resize(2, 10)
	assertLine(t, s, 0, "4")
	assertLine(t, s, 1, "$")
	assertCursor(t, s, 1, 2)
}
