package screen

import "testing"

// Phase 9: the sequences full-screen programs actually use.

// --- scroll regions -------------------------------------------------------

func TestScrollRegionScrollsOnlyItsRows(t *testing.T) {
	s := New(5, 4)
	fillRows(s, 5) // 1..5, one per row

	// Rows 2-4 (one-based) become the region, then index four times from the
	// bottom of it. Only those rows move.
	feed(s, "\x1b[2;4r")
	feed(s, "\x1b[4;1H") // to the bottom of the region
	feed(s, "\n\n")

	if got := s.Line(0); got != "1" {
		t.Fatalf("row 0 = %q, want the header to stay put", got)
	}
	if got := s.Line(4); got != "5" {
		t.Fatalf("row 4 = %q, want the footer to stay put", got)
	}
	if got, want := s.Line(1), "4"; got != want {
		t.Fatalf("row 1 = %q, want %q: the region scrolled", got, want)
	}
	if got := s.Line(3); got != "" {
		t.Fatalf("row 3 = %q, want a blank line opened at the bottom", got)
	}
}

func TestScrollRegionSendsNothingToHistory(t *testing.T) {
	s := New(4, 4)
	feed(s, "\x1b[1;3r") // a region, even one starting at the top
	feed(s, "\x1b[3;1Habc\n\n\n")

	if got := s.ScrollbackLen(); got != 0 {
		t.Fatalf("scrollback holds %d lines; a program's region is not output", got)
	}
}

func TestScrollRegionHomesTheCursor(t *testing.T) {
	s := New(6, 4)
	feed(s, "\x1b[4;5H")
	feed(s, "\x1b[2;5r")
	if got := s.Cursor(); got != (Cursor{Row: 0, Col: 0}) {
		t.Fatalf("cursor = %+v, want home after DECSTBM", got)
	}
}

func TestInvalidScrollRegionIsIgnored(t *testing.T) {
	s := New(4, 4)
	feed(s, "\x1b[3;2r") // bottom above top
	feed(s, "\x1b[4;1Habc\n")
	// The region never changed, so this scrolled the whole screen.
	if got := s.ScrollbackLen(); got != 1 {
		t.Fatalf("scrollback = %d lines, want the full-screen behaviour to survive", got)
	}
}

func TestReverseIndexScrollsAtTheTopMargin(t *testing.T) {
	s := New(5, 4)
	fillRows(s, 5)
	feed(s, "\x1b[2;4r")
	feed(s, "\x1b[2;1H") // the top of the region
	feed(s, "\x1bM")     // RI

	if got := s.Line(0); got != "1" {
		t.Fatalf("row 0 = %q, want the header untouched", got)
	}
	if got := s.Line(1); got != "" {
		t.Fatalf("row 1 = %q, want a blank line opened at the top of the region", got)
	}
	if got := s.Line(2); got != "2" {
		t.Fatalf("row 2 = %q, want the region's first line pushed down", got)
	}
}

func TestOriginModeAddressesTheRegion(t *testing.T) {
	s := New(6, 6)
	feed(s, "\x1b[3;5r")
	feed(s, "\x1b[?6h")   // DECOM
	feed(s, "\x1b[1;1HX") // row 1 of the region, not of the screen

	if got := s.Line(2); got != "X" {
		t.Fatalf("row 2 = %q, want the write to land on the region's first row", got)
	}
	// The cursor cannot leave the region in origin mode.
	feed(s, "\x1b[99;1HY")
	if got := s.Line(4); got != "Y" {
		t.Fatalf("row 4 = %q, want addressing clamped to the region", got)
	}
	if got := s.Line(5); got != "" {
		t.Fatalf("row 5 = %q, want nothing outside the region", got)
	}
}

func TestCursorUpStopsAtTheTopMargin(t *testing.T) {
	s := New(6, 4)
	feed(s, "\x1b[3;5r")
	feed(s, "\x1b[4;1H")
	feed(s, "\x1b[10A") // far further up than the region is tall
	if got := s.Cursor().Row; got != 2 {
		t.Fatalf("row = %d, want 2: CUU stops at the top margin", got)
	}
}

// --- the alternate screen -------------------------------------------------

func TestAlternateScreenKeepsThePrimaryIntact(t *testing.T) {
	s := New(3, 20)
	feed(s, "shell output")

	feed(s, "\x1b[?1049h")
	if !s.AltScreen() {
		t.Fatal("the alternate screen should be active")
	}
	if got := s.String(); got != "" {
		t.Fatalf("alt screen = %q, want it blank", got)
	}
	feed(s, "vim is here")

	feed(s, "\x1b[?1049l")
	if s.AltScreen() {
		t.Fatal("the alternate screen should be gone")
	}
	if got := s.Line(0); got != "shell output" {
		t.Fatalf("row 0 = %q, want the shell's output back", got)
	}
}

func TestAlternateScreenRestoresTheCursor(t *testing.T) {
	s := New(4, 10)
	feed(s, "\x1b[3;5H")
	feed(s, "\x1b[?1049h")
	feed(s, "\x1b[1;1Hpainting")
	feed(s, "\x1b[?1049l")

	if got := s.Cursor(); got != (Cursor{Row: 2, Col: 4}) {
		t.Fatalf("cursor = %+v, want where the shell left it", got)
	}
}

func TestAlternateScreenIsNotArchived(t *testing.T) {
	s := New(3, 4)
	feed(s, "\x1b[?1049h")
	fillRows(s, 10) // ten lines through a three-row screen

	if got := s.ScrollbackLen(); got != 0 {
		t.Fatalf("scrollback = %d lines, want none: an alt screen is a picture", got)
	}
	if s.ScrollToTop() {
		t.Fatal("scrolling should do nothing on the alternate screen")
	}
}

func TestSwitchingToAlternateTwiceKeepsOnePrimary(t *testing.T) {
	s := New(2, 8)
	feed(s, "primary")
	feed(s, "\x1b[?1049h")
	feed(s, "\x1b[?1049h") // a second set must not overwrite the saved grid
	feed(s, "\x1b[?1049l")

	if got := s.Line(0); got != "primary" {
		t.Fatalf("row 0 = %q, want the primary screen back", got)
	}
}

func TestOlderAlternateScreenSpellings(t *testing.T) {
	for _, mode := range []string{"47", "1047"} {
		s := New(2, 8)
		feed(s, "before")
		feed(s, "\x1b[?"+mode+"h")
		feed(s, "after")
		feed(s, "\x1b[?"+mode+"l")
		if got := s.Line(0); got != "before" {
			t.Fatalf("?%s: row 0 = %q, want the primary screen back", mode, got)
		}
	}
}

func TestResizeWhileOnAlternateScreen(t *testing.T) {
	s := New(4, 10)
	feed(s, "primary text")
	feed(s, "\x1b[?1049h")
	s.Resize(6, 20)
	feed(s, "\x1b[?1049l")

	if rows, cols := s.Size(); rows != 6 || cols != 20 {
		t.Fatalf("size = %dx%d, want 6x20", rows, cols)
	}
	// The line was written into a 10-column screen, so it wrapped there; what
	// matters is that the restored buffer is the new shape and kept its text.
	if got := s.Line(0); got != "primary te" {
		t.Fatalf("row 0 = %q, want the primary text at the new size", got)
	}
	if got := len(s.Snapshot().Cells); got != 6*20 {
		t.Fatalf("snapshot has %d cells, want %d", got, 6*20)
	}
}

// --- tab stops ------------------------------------------------------------

func TestTabMovesToTheNextStop(t *testing.T) {
	s := New(2, 30)
	feed(s, "A\tB\tC")
	if got := s.Line(0); got != "A       B       C" {
		t.Fatalf("row 0 = %q, want the columns lined up every eight", got)
	}
}

func TestTabStopsCanBeSetAndCleared(t *testing.T) {
	s := New(2, 20)
	feed(s, "\x1b[3g")   // clear every stop
	feed(s, "\x1b[1;5H") // column 5
	feed(s, "\x1bH")     // HTS: a stop here
	feed(s, "\x1b[1;1H")
	feed(s, "\tX")
	if got := s.Cell(0, 4).Rune; got != 'X' {
		t.Fatalf("cell(0,4) = %q, want the tab to land on the new stop", got)
	}
}

func TestTabStopsAtTheEndOfTheLine(t *testing.T) {
	s := New(2, 10)
	feed(s, "\x1b[3g") // no stops at all
	feed(s, "\tX")
	if got := s.Cell(0, 9).Rune; got != 'X' {
		t.Fatalf("cell(0,9) = %q, want the tab to stop at the last column", got)
	}
}

func TestForwardAndBackwardTabs(t *testing.T) {
	s := New(2, 40)
	feed(s, "\x1b[3I") // CHT: three stops forward
	if got := s.Cursor().Col; got != 24 {
		t.Fatalf("col = %d, want 24", got)
	}
	feed(s, "\x1b[2Z") // CBT: two back
	if got := s.Cursor().Col; got != 8 {
		t.Fatalf("col = %d, want 8", got)
	}
}

// --- inserting and deleting -----------------------------------------------

func TestInsertAndDeleteCharacters(t *testing.T) {
	s := New(2, 10)
	feed(s, "abcdef")
	feed(s, "\x1b[1;4H") // on 'd'
	feed(s, "\x1b[2@")   // open two cells
	if got := s.Line(0); got != "abc  def" {
		t.Fatalf("row 0 = %q, want two blanks opened", got)
	}
	feed(s, "\x1b[2P") // and take them back
	if got := s.Line(0); got != "abcdef" {
		t.Fatalf("row 0 = %q, want the line closed up again", got)
	}
}

func TestInsertPushesCharactersOffTheEnd(t *testing.T) {
	s := New(1, 6)
	feed(s, "abcdef")
	feed(s, "\x1b[1;1H\x1b[2@")
	if got := s.Line(0); got != "  abcd" {
		t.Fatalf("row 0 = %q, want the line pushed right and truncated", got)
	}
}

func TestInsertAndDeleteLines(t *testing.T) {
	s := New(4, 4)
	fillRows(s, 4)

	feed(s, "\x1b[2;1H\x1b[L") // open a line at row 2
	if got := []string{s.Line(0), s.Line(1), s.Line(2)}; got[0] != "1" || got[1] != "" || got[2] != "2" {
		t.Fatalf("rows = %q, want a blank line opened", got)
	}
	feed(s, "\x1b[M") // and remove it
	if got := s.Line(1); got != "2" {
		t.Fatalf("row 1 = %q, want the lines pulled back up", got)
	}
}

func TestInsertLinesRespectsTheScrollRegion(t *testing.T) {
	s := New(5, 4)
	fillRows(s, 5)
	feed(s, "\x1b[2;4r")       // region rows 2-4
	feed(s, "\x1b[2;1H\x1b[L") // insert at the top of it

	if got := s.Line(4); got != "5" {
		t.Fatalf("row 4 = %q, want the footer untouched by IL", got)
	}
	if got := s.Line(3); got != "3" {
		t.Fatalf("row 3 = %q, want row 3's text pushed down inside the region", got)
	}
}

func TestInsertModePushesTheLineAlong(t *testing.T) {
	s := New(1, 8)
	feed(s, "abcd")
	feed(s, "\x1b[1;1H")
	feed(s, "\x1b[4h") // IRM
	feed(s, "XY")
	if got := s.Line(0); got != "XYabcd" {
		t.Fatalf("row 0 = %q, want the text pushed right", got)
	}
	feed(s, "\x1b[4l") // back to overwriting
	feed(s, "Z")
	if got := s.Line(0); got != "XYZbcd" {
		t.Fatalf("row 0 = %q, want overwriting again", got)
	}
}

func TestScrollUpAndDownSequences(t *testing.T) {
	s := New(3, 4)
	fillRows(s, 3)
	feed(s, "\x1b[S") // SU
	if got := s.Line(0); got != "2" {
		t.Fatalf("row 0 = %q, want the screen scrolled up", got)
	}
	feed(s, "\x1b[T") // SD
	if got := s.Line(1); got != "2" {
		t.Fatalf("row 1 = %q, want the screen scrolled back down", got)
	}
}

func TestRepeatLastCharacter(t *testing.T) {
	s := New(1, 10)
	feed(s, "-\x1b[4b")
	if got := s.Line(0); got != "-----" {
		t.Fatalf("row 0 = %q, want the character repeated four more times", got)
	}
}

// --- character sets -------------------------------------------------------

func TestLineDrawingCharacterSet(t *testing.T) {
	s := New(2, 10)
	feed(s, "\x1b(0") // G0 becomes the graphics set
	feed(s, "lqqk")   // a box's top edge
	feed(s, "\x1b(B") // back to ASCII
	feed(s, "x")

	if got := s.Line(0); got != "┌──┐x" {
		t.Fatalf("row 0 = %q, want line-drawing glyphs then a plain x", got)
	}
}

func TestShiftInAndOutSwitchCharacterSets(t *testing.T) {
	s := New(2, 10)
	feed(s, "\x1b)0") // G1 becomes the graphics set
	feed(s, "a\x0eq\x0fb")

	if got := s.Line(0); got != "a─b" {
		t.Fatalf("row 0 = %q, want only the shifted character translated", got)
	}
}

// --- replies --------------------------------------------------------------

func TestCursorPositionReport(t *testing.T) {
	s := New(10, 20)
	feed(s, "\x1b[4;7H")
	feed(s, "\x1b[6n")

	if got := string(s.TakeReplies()); got != "\x1b[4;7R" {
		t.Fatalf("reply = %q, want ESC[4;7R", got)
	}
	if s.TakeReplies() != nil {
		t.Fatal("replies should be drained once")
	}
}

func TestCursorPositionReportIsRelativeInOriginMode(t *testing.T) {
	s := New(10, 20)
	feed(s, "\x1b[3;8r\x1b[?6h")
	feed(s, "\x1b[2;1H\x1b[6n")

	if got := string(s.TakeReplies()); got != "\x1b[2;1R" {
		t.Fatalf("reply = %q, want the row relative to the region", got)
	}
}

func TestDeviceAttributesAndStatus(t *testing.T) {
	s := New(4, 4)
	feed(s, "\x1b[c")
	if got := string(s.TakeReplies()); got != "\x1b[?1;2c" {
		t.Fatalf("primary DA = %q", got)
	}
	feed(s, "\x1b[>c")
	if got := string(s.TakeReplies()); got != "\x1b[>0;95;0c" {
		t.Fatalf("secondary DA = %q", got)
	}
	feed(s, "\x1b[5n")
	if got := string(s.TakeReplies()); got != "\x1b[0n" {
		t.Fatalf("status = %q", got)
	}
}

func TestWindowSizeReport(t *testing.T) {
	s := New(24, 80)
	feed(s, "\x1b[18t")
	if got := string(s.TakeReplies()); got != "\x1b[8;24;80t" {
		t.Fatalf("size report = %q", got)
	}
}

func TestUnknownQueriesAreNotAnswered(t *testing.T) {
	s := New(4, 4)
	feed(s, "\x1b[?2n") // a private status request we do not implement
	feed(s, "\x1b[99t") // a window operation we do not implement
	if got := s.TakeReplies(); got != nil {
		t.Fatalf("replied %q; silence is better than a wrong answer", got)
	}
}

func TestRepliesAreBounded(t *testing.T) {
	s := New(4, 4)
	for i := 0; i < 2000; i++ {
		feed(s, "\x1b[6n")
	}
	if got := len(s.TakeReplies()); got > maxReplies+16 {
		t.Fatalf("queued %d bytes, want the queue capped near %d", got, maxReplies)
	}
}

// --- modes ----------------------------------------------------------------

func TestMouseModesTrackTheHighestAsked(t *testing.T) {
	s := New(4, 4)
	feed(s, "\x1b[?1000h")
	if got := s.Modes().Mouse; got != MouseClick {
		t.Fatalf("mouse = %v, want click tracking", got)
	}
	feed(s, "\x1b[?1002h")
	if got := s.Modes().Mouse; got != MouseDrag {
		t.Fatalf("mouse = %v, want drag tracking", got)
	}
	// Clearing a mode that is not the current one changes nothing: programs
	// routinely reset all of them on the way out.
	feed(s, "\x1b[?1000l")
	if got := s.Modes().Mouse; got != MouseDrag {
		t.Fatalf("mouse = %v, want drag tracking to survive", got)
	}
	feed(s, "\x1b[?1002l")
	if got := s.Modes().Mouse; got != MouseOff {
		t.Fatalf("mouse = %v, want tracking off", got)
	}
}

func TestSGRMouseAndFocusModes(t *testing.T) {
	s := New(4, 4)
	feed(s, "\x1b[?1006h\x1b[?1004h")
	m := s.Modes()
	if !m.MouseSGR || !m.ReportFocus {
		t.Fatalf("modes = %+v, want SGR mouse and focus reporting on", m)
	}
}

func TestCursorStyle(t *testing.T) {
	s := New(4, 4)
	feed(s, "\x1b[5 q")
	if got := s.Snapshot().CursorStyle; got != CursorBar {
		t.Fatalf("style = %v, want a bar", got)
	}
	feed(s, "\x1b[2 q")
	if got := s.Snapshot().CursorStyle; got != CursorBlock {
		t.Fatalf("style = %v, want a block", got)
	}
}

func TestWindowTitleFromOSC(t *testing.T) {
	s := New(4, 4)
	feed(s, "\x1b]0;my title\x07")
	if got := s.Title(); got != "my title" {
		t.Fatalf("title = %q", got)
	}
	feed(s, "\x1b]2;another\x1b\\")
	if got := s.Snapshot().Title; got != "another" {
		t.Fatalf("title = %q", got)
	}
}

func TestNewlineMode(t *testing.T) {
	s := New(3, 8)
	feed(s, "abc\ndef")
	if got := s.Line(1); got != "   def" {
		t.Fatalf("row 1 = %q, want LF to keep the column by default", got)
	}
	feed(s, "\x1b[20h") // LNM
	feed(s, "\nghi")
	if got := s.Line(2); got != "ghi" {
		t.Fatalf("row 2 = %q, want LF to return the carriage in LNM", got)
	}
}

func TestAlignmentTestFillsTheScreen(t *testing.T) {
	s := New(2, 3)
	feed(s, "\x1b#8")
	if got := s.String(); got != "EEE\nEEE" {
		t.Fatalf("screen = %q, want it filled with E", got)
	}
	if got := s.Cursor(); got != (Cursor{}) {
		t.Fatalf("cursor = %+v, want home", got)
	}
}

func TestResetRestoresTheScrollRegionAndTabs(t *testing.T) {
	s := New(6, 20)
	feed(s, "\x1b[2;4r\x1b[3g\x1b[?1049h")
	feed(s, "\x1bc") // RIS

	if s.AltScreen() {
		t.Fatal("RIS should leave the alternate screen")
	}
	feed(s, "A\tB")
	if got := s.Line(0); got != "A       B" {
		t.Fatalf("row 0 = %q, want the default tab stops back", got)
	}
	feed(s, "\x1b[6;1H\n")
	if got := s.ScrollbackLen(); got != 1 {
		t.Fatalf("scrollback = %d, want the full screen scrolling again", got)
	}
}

// vim pushes the window title on the way in and pops it on the way out, so
// that quitting it leaves the title the shell had set.
func TestTitleStack(t *testing.T) {
	s := New(4, 4)
	feed(s, "\x1b]0;zsh\x07")
	feed(s, "\x1b[22;2t")     // push
	feed(s, "\x1b]0;vim\x07") // the program's own title
	if got := s.Title(); got != "vim" {
		t.Fatalf("title = %q while the program is running", got)
	}
	feed(s, "\x1b[23;2t") // pop
	if got := s.Title(); got != "zsh" {
		t.Fatalf("title = %q, want the shell's title back", got)
	}
}

func TestTitleStackIsBounded(t *testing.T) {
	s := New(4, 4)
	for i := 0; i < 100; i++ {
		feed(s, "\x1b[22t")
	}
	if got := len(s.titleStack); got > maxTitleStack {
		t.Fatalf("stack holds %d titles, want at most %d", got, maxTitleStack)
	}
	// Popping an empty stack is not an error; it leaves the title alone.
	feed(s, "\x1b]0;here\x07")
	for i := 0; i < 200; i++ {
		feed(s, "\x1b[23t")
	}
	if got := s.Title(); got != "" {
		t.Fatalf("title = %q, want what the oversized stack held", got)
	}
}
