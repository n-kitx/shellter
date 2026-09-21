package screen

// The scroll region, and the operations that shift lines and characters
// around inside it.
//
// A scroll region ("margins", DECSTBM) is how a full-screen program keeps part
// of the screen still while the rest moves. less keeps its status line at the
// bottom; vim keeps its own; top redraws a header that must not scroll away.
// Without margins, a line feed on the last row moves the *whole* screen, and
// those programs fall back to redrawing everything - or draw nonsense.
//
//	┌──────────────┐ row 0
//	│  header      │        outside the region: never scrolls
//	├──────────────┤ top
//	│              │
//	│  region      │        line feeds move only these rows
//	│              │
//	├──────────────┤ bottom
//	│  status      │        outside the region
//	└──────────────┘ rows-1

// setScrollRegion implements DECSTBM, "ESC [ top ; bottom r". The arguments are
// one-based and inclusive; an invalid region is ignored, as on a real terminal.
//
// The cursor always ends up home afterwards, which is the part programs rely on.
func (s *Screen) setScrollRegion(top, bottom int) {
	top, bottom = top-1, bottom-1
	if bottom >= s.rows {
		bottom = s.rows - 1
	}
	if top < 0 || top >= bottom || bottom >= s.rows {
		return
	}
	s.top, s.bottom = top, bottom
	s.cursorHome()
}

// resetScrollRegion opens the region back up to the whole screen.
func (s *Screen) resetScrollRegion() { s.top, s.bottom = 0, s.rows-1 }

// cursorHome moves to the top-left, which in origin mode is the top-left of the
// scroll region rather than of the screen.
func (s *Screen) cursorHome() {
	s.cur.Col = 0
	s.cur.Row = 0
	if s.mode.Origin {
		s.cur.Row = s.top
	}
	s.pendingWrap = false
}

// archives reports whether lines scrolling off the top should be kept.
//
// Only the primary buffer with no margins set: history is a record of the
// session's output, and a program that carved out a region or switched to the
// alternate screen is painting a UI, not producing output.
func (s *Screen) archives() bool {
	return !s.altActive && s.top == 0 && s.bottom == s.rows-1
}

// shiftUp moves rows [top,bottom] up by n and blanks the n rows freed at the
// bottom. With archive set, the lines pushed off the top go to scrollback.
func (s *Screen) shiftUp(top, bottom, n int, archive bool) {
	if n <= 0 || top > bottom {
		return
	}
	if height := bottom - top + 1; n > height {
		n = height
	}

	if archive {
		for i := 0; i < n; i++ {
			s.sb.push(s.row(top+i), s.rowWrapped(top+i))
		}
		// Stay anchored on the same text while new lines arrive underneath.
		if s.offset > 0 {
			s.offset = clamp(s.offset+n, 0, s.sb.Len())
		}
	}

	// Only the index moves; the text stays exactly where it is.
	s.rotateRows(top, bottom, n)
	for r := bottom - n + 1; r <= bottom; r++ {
		s.blankRow(r)
	}
}

// shiftDown moves rows [top,bottom] down by n and blanks the n rows freed at
// the top. Nothing is archived: these lines are being pushed *into* the screen.
func (s *Screen) shiftDown(top, bottom, n int) {
	if n <= 0 || top > bottom {
		return
	}
	if height := bottom - top + 1; n > height {
		n = height
	}
	// Rotating up by everything-but-n is rotating down by n.
	s.rotateRows(top, bottom, bottom-top+1-n)
	for r := top; r < top+n; r++ {
		s.blankRow(r)
	}
}

// insertLines implements IL, "ESC [ n L": open n blank lines at the cursor,
// pushing the rest of the region down. This is how an editor opens a line
// without repainting everything below it.
func (s *Screen) insertLines(n int) {
	if s.cur.Row < s.top || s.cur.Row > s.bottom {
		return
	}
	s.shiftDown(s.cur.Row, s.bottom, n)
	s.cur.Col = 0
}

// deleteLines implements DL, "ESC [ n M": remove n lines at the cursor, pulling
// the rest of the region up.
func (s *Screen) deleteLines(n int) {
	if s.cur.Row < s.top || s.cur.Row > s.bottom {
		return
	}
	s.shiftUp(s.cur.Row, s.bottom, n, false)
	s.cur.Col = 0
}

// insertChars implements ICH, "ESC [ n @": open n blank cells at the cursor,
// pushing the rest of the line right. Characters pushed past the last column
// are lost - a line never grows.
func (s *Screen) insertChars(n int) {
	if n <= 0 {
		return
	}
	if n > s.cols-s.cur.Col {
		n = s.cols - s.cur.Col
	}
	line := s.row(s.cur.Row)
	from := s.cur.Col
	copy(line[from+n:], line[from:])
	s.blankCells(s.cur.Row, from, from+n)
	s.repairWide()
}

// deleteChars implements DCH, "ESC [ n P": remove n cells at the cursor,
// pulling the rest of the line left and blanking the end.
func (s *Screen) deleteChars(n int) {
	if n <= 0 {
		return
	}
	if n > s.cols-s.cur.Col {
		n = s.cols - s.cur.Col
	}
	line := s.row(s.cur.Row)
	from := s.cur.Col
	copy(line[from:], line[from+n:])
	s.blankCells(s.cur.Row, s.cols-n, s.cols)
	s.repairWide()
}
