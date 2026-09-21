package screen

import "testing"

// Phase 8: lines that scroll off the top are kept, and the view can move back
// into them without disturbing the live grid.

// fillRows writes n numbered lines, scrolling the screen as it goes.
func fillRows(s *Screen, n int) {
	for i := 1; i <= n; i++ {
		feed(s, itoa(i))
		if i < n {
			feed(s, "\r\n")
		}
	}
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

func TestLinesScrollingOffAreKept(t *testing.T) {
	s := New(3, 8)
	fillRows(s, 5) // rows 1..5 on a three-row screen

	// The live grid holds the last three.
	assertLine(t, s, 0, "3")
	assertLine(t, s, 1, "4")
	assertLine(t, s, 2, "5")

	// The first two went to history.
	if got := s.ScrollbackLen(); got != 2 {
		t.Fatalf("ScrollbackLen = %d, want 2", got)
	}
}

func TestScrollbackCanBeDisabled(t *testing.T) {
	s := NewWithScrollback(2, 8, 0)
	fillRows(s, 5)

	if got := s.ScrollbackLen(); got != 0 {
		t.Fatalf("ScrollbackLen = %d, want 0 when disabled", got)
	}
	if s.ScrollBy(5) {
		t.Fatal("scrolling should do nothing with no history")
	}
}

func TestScrollbackCapacityIsBounded(t *testing.T) {
	s := NewWithScrollback(2, 8, 3)
	fillRows(s, 20)

	if got := s.ScrollbackLen(); got != 3 {
		t.Fatalf("ScrollbackLen = %d, want the cap 3", got)
	}
	// It kept the newest history, not the oldest.
	s.ScrollToTop()
	if got := s.Snapshot().Lines()[0]; got != "16      " {
		t.Fatalf("oldest kept line = %q, want line 16", got)
	}
}

func TestScrollingShowsHistoryWithoutTouchingTheGrid(t *testing.T) {
	s := New(3, 8)
	fillRows(s, 5)
	before := s.String()
	cursor := s.Cursor()

	s.ScrollBy(2)

	snap := s.Snapshot()
	if snap.Lines()[0] != "1       " || snap.Lines()[1] != "2       " {
		t.Fatalf("view = %q, want history on top", snap.Lines()[:2])
	}
	if snap.Lines()[2] != "3       " {
		t.Fatalf("view row 2 = %q, want the top of the live grid", snap.Lines()[2])
	}

	// The terminal state itself is untouched: only the view moved.
	if got := s.String(); got != before {
		t.Fatalf("the grid changed:\n%s\nwant:\n%s", got, before)
	}
	if s.Cursor() != cursor {
		t.Fatalf("cursor moved to %+v, want %+v", s.Cursor(), cursor)
	}
}

func TestScrollingIsClampedAtBothEnds(t *testing.T) {
	s := New(3, 8)
	fillRows(s, 5) // two lines of history

	if !s.ScrollBy(99) {
		t.Fatal("scrolling back should have moved")
	}
	if got := s.ScrollOffset(); got != 2 {
		t.Fatalf("offset = %d, want it clamped to 2", got)
	}
	if s.ScrollBy(5) {
		t.Fatal("already at the oldest line; nothing should move")
	}

	s.ScrollBy(-99)
	if got := s.ScrollOffset(); got != 0 {
		t.Fatalf("offset = %d, want 0", got)
	}
	if s.ScrollBy(-1) {
		t.Fatal("already at the live view; nothing should move")
	}
}

func TestCursorFollowsTheViewAndDisappears(t *testing.T) {
	s := New(3, 8)
	fillRows(s, 5) // cursor on the bottom row

	if got := s.Snapshot(); got.Cursor.Row != 2 || !got.Modes.CursorVisible {
		t.Fatalf("live view cursor = %+v visible=%v", got.Cursor, got.Modes.CursorVisible)
	}

	// One line back: the grid slides down and the cursor, on row 2 of 3, is
	// pushed off the bottom of the view.
	s.ScrollBy(1)
	if got := s.Snapshot(); got.Modes.CursorVisible {
		t.Fatalf("cursor should be hidden once pushed off the view, got %+v", got.Cursor)
	}

	s.ScrollToBottom()
	if got := s.Snapshot(); !got.Modes.CursorVisible || got.Cursor.Row != 2 {
		t.Fatalf("returning to the live view should restore the cursor, got %+v", got.Cursor)
	}
}

func TestViewStaysAnchoredWhileOutputArrives(t *testing.T) {
	s := New(3, 8)
	fillRows(s, 5)
	s.ScrollBy(2) // looking at lines 1 and 2

	top := s.Snapshot().Lines()[0]
	if top != "1       " {
		t.Fatalf("top = %q, want line 1", top)
	}

	// More output arrives while we are reading history.
	feed(s, "\r\n6\r\n7")

	if got := s.Snapshot().Lines()[0]; got != top {
		t.Fatalf("view drifted to %q, want to stay on %q", got, top)
	}
	if got := s.ScrollbackLen(); got != 4 {
		t.Fatalf("ScrollbackLen = %d, want 4 after two more lines scrolled off", got)
	}
}

func TestScrollToTopAndBottom(t *testing.T) {
	s := New(2, 8)
	fillRows(s, 6)

	s.ScrollToTop()
	if got := s.ScrollOffset(); got != s.ScrollbackLen() {
		t.Fatalf("offset = %d, want the whole history %d", got, s.ScrollbackLen())
	}
	if got := s.Snapshot().Lines()[0]; got != "1       " {
		t.Fatalf("top line = %q, want line 1", got)
	}

	s.ScrollToBottom()
	if got := s.Snapshot().Lines()[1]; got != "6       " {
		t.Fatalf("bottom line = %q, want line 6", got)
	}
}

func TestSetScrollbackReconfigures(t *testing.T) {
	s := New(2, 8)
	fillRows(s, 10)

	s.SetScrollback(2)
	if got := s.ScrollbackLen(); got != 2 {
		t.Fatalf("ScrollbackLen = %d, want 2 after shrinking", got)
	}
	if got := s.ScrollbackCap(); got != 2 {
		t.Fatalf("ScrollbackCap = %d, want 2", got)
	}

	s.SetScrollback(0)
	if got := s.ScrollbackLen(); got != 0 || s.ScrollOffset() != 0 {
		t.Fatalf("disabling left len=%d offset=%d", got, s.ScrollOffset())
	}
}

func TestEraseDisplay3ClearsHistory(t *testing.T) {
	s := New(2, 8)
	fillRows(s, 6)
	if s.ScrollbackLen() == 0 {
		t.Fatal("expected history to exist")
	}

	feed(s, "\x1b[3J")
	if got := s.ScrollbackLen(); got != 0 {
		t.Fatalf("ScrollbackLen = %d, want 0 after ED 3", got)
	}

	// Plain ED 2 leaves history alone.
	s2 := New(2, 8)
	fillRows(s2, 6)
	feed(s2, "\x1b[2J")
	if s2.ScrollbackLen() == 0 {
		t.Fatal("ED 2 should not clear history")
	}
}

func TestResetClearsHistory(t *testing.T) {
	s := New(2, 8)
	fillRows(s, 6)
	feed(s, "\x1bc") // RIS
	if got := s.ScrollbackLen(); got != 0 {
		t.Fatalf("ScrollbackLen = %d, want 0 after RIS", got)
	}
}

func TestHistoryKeepsAttributesAndWideCharacters(t *testing.T) {
	s := New(2, 6)
	feed(s, "\x1b[31m漢字\x1b[0m\r\nb\r\nc") // the coloured line scrolls off

	s.ScrollToTop()
	snap := s.Snapshot()
	if got := snap.CellAt(0, 0); got.Rune != '漢' || got.Attr.FG != Indexed(1) {
		t.Fatalf("history cell = %+v, want a red 漢", got)
	}
	if !snap.CellAt(0, 1).IsContinuation() {
		t.Fatal("history lost the wide character's continuation cell")
	}
}

func TestSnapshotReportsScrollPosition(t *testing.T) {
	s := New(2, 8)
	fillRows(s, 5)

	if snap := s.Snapshot(); snap.ScrollOffset != 0 || snap.ScrollbackLen != 3 {
		t.Fatalf("offset=%d len=%d, want 0 and 3", snap.ScrollOffset, snap.ScrollbackLen)
	}
	s.ScrollBy(2)
	if snap := s.Snapshot(); snap.ScrollOffset != 2 {
		t.Fatalf("offset = %d, want 2", snap.ScrollOffset)
	}
}
