package screen

import "testing"

// Phase 7: a rune is not a column.

func TestNarrowNonASCIIAdvancesOne(t *testing.T) {
	s := New(2, 10)
	feed(s, "héllo")

	assertLine(t, s, 0, "héllo")
	assertCursor(t, s, 0, 5)
	if got := s.Cell(0, 1).Rune; got != 'é' {
		t.Fatalf("cell(0,1) = %q, want 'é'", got)
	}
}

func TestWideCharacterTakesTwoCells(t *testing.T) {
	s := New(2, 10)
	feed(s, "漢字")

	// Two characters, four columns.
	assertCursor(t, s, 0, 4)
	assertLine(t, s, 0, "漢字")

	if got := s.Cell(0, 0).Rune; got != '漢' {
		t.Fatalf("cell(0,0) = %q, want 漢", got)
	}
	if !s.Cell(0, 1).IsContinuation() {
		t.Fatalf("cell(0,1) = %+v, want a continuation", s.Cell(0, 1))
	}
	if got := s.Cell(0, 2).Rune; got != '字' {
		t.Fatalf("cell(0,2) = %q, want 字", got)
	}
	if !s.Cell(0, 3).IsContinuation() {
		t.Fatal("cell(0,3) should be a continuation")
	}
	if got := s.Cell(0, 0).Width(); got != 2 {
		t.Fatalf("width = %d, want 2", got)
	}
}

func TestMixedWidthText(t *testing.T) {
	s := New(2, 12)
	feed(s, "a漢b")

	assertCursor(t, s, 0, 4)
	if got := s.Cell(0, 3).Rune; got != 'b' {
		t.Fatalf("cell(0,3) = %q, want 'b' after the wide character", got)
	}
}

func TestEmojiIsWide(t *testing.T) {
	s := New(2, 10)
	feed(s, "🚀x")

	assertCursor(t, s, 0, 3)
	if got := s.Cell(0, 0).Rune; got != '🚀' {
		t.Fatalf("cell(0,0) = %q", got)
	}
	if got := s.Cell(0, 2).Rune; got != 'x' {
		t.Fatalf("cell(0,2) = %q, want 'x'", got)
	}
}

func TestCombiningMarkJoinsThePreviousCell(t *testing.T) {
	s := New(2, 10)
	feed(s, "éx") // e + combining acute, then x

	// The accent took no column of its own.
	assertCursor(t, s, 0, 2)
	if got := s.Cell(0, 0).Text(); got != "é" {
		t.Fatalf("cell(0,0) text = %q, want e + combining acute", got)
	}
	if got := s.Cell(0, 1).Rune; got != 'x' {
		t.Fatalf("cell(0,1) = %q, want 'x'", got)
	}
	assertLine(t, s, 0, "éx")
}

func TestCombiningMarkOnAWideCharacter(t *testing.T) {
	s := New(2, 10)
	feed(s, "漢́")

	// It must land on the character, not on its continuation half.
	if got := s.Cell(0, 0).Text(); got != "漢́" {
		t.Fatalf("cell(0,0) text = %q", got)
	}
	if s.Cell(0, 1).Comb != "" {
		t.Fatal("the continuation cell must stay empty")
	}
	assertCursor(t, s, 0, 2)
}

func TestCombiningMarkAtTheStartOfALineIsDropped(t *testing.T) {
	s := New(2, 10)
	feed(s, "́ab") // nothing to combine with

	assertLine(t, s, 0, "ab")
	assertCursor(t, s, 0, 2)
}

func TestCombiningMarksAreCapped(t *testing.T) {
	s := New(2, 10)
	feed(s, "a")
	for i := 0; i < 200; i++ {
		feed(s, "́")
	}
	if got := len(s.Cell(0, 0).Comb); got > maxCombining*4 {
		t.Fatalf("accumulated %d bytes of marks, want a cap", got)
	}
}

func TestWideCharacterWrapsWholeAtTheRightEdge(t *testing.T) {
	// Five columns: "abcd" fills four, leaving one - not enough for 漢.
	s := New(3, 5)
	feed(s, "abcd漢")

	assertLine(t, s, 0, "abcd")
	assertLine(t, s, 1, "漢")
	assertCursor(t, s, 1, 2)
	// The leftover column stays blank rather than holding half a character.
	if got := s.Cell(0, 4); got.Rune != ' ' {
		t.Fatalf("cell(0,4) = %+v, want a blank", got)
	}
}

func TestWideCharacterExactlyFillsTheLine(t *testing.T) {
	s := New(3, 4)
	feed(s, "ab漢")

	assertLine(t, s, 0, "ab漢")
	// Deferred wrap: the cursor waits on the last column.
	assertCursor(t, s, 0, 3)

	feed(s, "c")
	assertLine(t, s, 1, "c")
	assertCursor(t, s, 1, 1)
}

func TestWideCharacterDroppedWhenAutowrapIsOff(t *testing.T) {
	s := New(2, 5)
	feed(s, "\x1b[?7labcd漢") // one column left, nowhere to wrap to

	assertLine(t, s, 0, "abcd")
	assertCursor(t, s, 0, 4)
}

func TestOverwritingHalfAWideCharacterClearsBoth(t *testing.T) {
	// Writing over the left half.
	s := New(2, 6)
	feed(s, "漢字\x1b[1;1HX")
	assertLine(t, s, 0, "X 字")
	if got := s.Cell(0, 1).Rune; got != ' ' {
		t.Fatalf("cell(0,1) = %q, want the orphaned half blanked", got)
	}

	// Writing over the right half.
	s = New(2, 6)
	feed(s, "漢字\x1b[1;2HY")
	assertLine(t, s, 0, " Y字")
	if got := s.Cell(0, 0).Rune; got != ' ' {
		t.Fatalf("cell(0,0) = %q, want the orphaned half blanked", got)
	}
}

func TestErasingHalfAWideCharacterClearsBoth(t *testing.T) {
	s := New(2, 6)
	// Erase from column 2 to the end, which cuts 漢 in half.
	feed(s, "a漢b\x1b[1;3H\x1b[0K")

	assertLine(t, s, 0, "a")
	if got := s.Cell(0, 1).Rune; got != ' ' {
		t.Fatalf("cell(0,1) = %q, want the orphaned half blanked", got)
	}
}

func TestWideCharactersSurviveScrolling(t *testing.T) {
	s := New(2, 6)
	feed(s, "漢字\r\nab\r\ncd") // scrolls once

	assertLine(t, s, 0, "ab")
	assertLine(t, s, 1, "cd")

	s = New(3, 6)
	feed(s, "x\r\n漢字\r\ny\r\nz") // 漢字 moves up a row
	assertLine(t, s, 0, "漢字")
	if !s.Cell(0, 1).IsContinuation() {
		t.Fatal("the continuation cell did not move with its character")
	}
}

func TestResizeDoesNotLeaveHalfCharacters(t *testing.T) {
	s := New(2, 6)
	feed(s, "a漢b")

	s.Resize(2, 2) // the new right edge falls inside 漢
	assertLine(t, s, 0, "a")
	if got := s.Cell(0, 1).Rune; got != ' ' {
		t.Fatalf("cell(0,1) = %q, want a blank rather than half a character", got)
	}
}

func TestSnapshotKeepsWideAndCombiningInformation(t *testing.T) {
	s := New(1, 6)
	feed(s, "é漢")

	snap := s.Snapshot()
	if got := snap.CellAt(0, 0).Text(); got != "é" {
		t.Fatalf("cell(0,0) = %q", got)
	}
	if got := snap.CellAt(0, 1).Width(); got != 2 {
		t.Fatalf("cell(0,1) width = %d, want 2", got)
	}
	if !snap.CellAt(0, 2).IsContinuation() {
		t.Fatal("the snapshot lost the continuation cell")
	}
	if snap.Lines()[0] != "é漢   " {
		t.Fatalf("line = %q", snap.Lines()[0])
	}
}
