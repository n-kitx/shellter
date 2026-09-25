package screen

// Reflow: what a resize does to the text that is already there.
//
// A terminal line is not a row. "ls -la" printing a long filename produces one
// line that happens to occupy two rows at 80 columns; at 120 it should occupy
// one, and at 40 it should occupy three. The screen records which row breaks
// were forced by the right edge (the wrapped flags), and that is enough to
// undo them: join each run of wrapped rows back into the line it was, then
// break it again at the new width.
//
//	80 columns                          40 columns
//	+--------------------------------+  +----------------+
//	| $ echo a long line that wraps ~|  | $ echo a long l~|   ~ = wrapped
//	| around the edge                |  | ine that wraps ~|
//	| $                              |  | around the edge |
//	+--------------------------------+  | $               |
//	                                    +----------------+
//
// History takes part as well as the screen, so a shrink that pushes rows off
// the top pushes them into scrollback, and scrolling back finds them at the
// new width like everything else. What is on screen stays on screen, and what
// was history stays history: widening does not pull old lines back down over a
// screen the user cleared.
//
// Only the primary screen reflows. A full-screen program on the alternate
// screen is told its new size and redraws; rewrapping its grid would only
// scramble what it is about to paint over.

// reflowPrimary rewraps the primary screen - its grid, cursor and history -
// from the old width to cols, and returns the new grid for rows rows.
//
// History is the bulk of the work - a thousand lines against a screen's forty
// - so it is done without a slice per line: the old history is gathered into
// one reused buffer, each line is broken into a single scratch row, and every
// row goes straight back into the ring, which reuses the memory its slots
// already had.
func (s *Screen) reflowPrimary(g gridRows, cur Cursor, pendingWrap bool, rows, cols int) (
	cells []Cell, wrapped []bool, next Cursor) {

	// The history, gathered as logical lines: one flat run of cells, and
	// where each line ends. A history row that wrapped was cut at the old
	// width, so it is padded back out to it - its trailing blanks were dropped
	// when it was stored, and they are part of the line.
	flat := s.reflowCells[:0]
	ends := s.reflowEnds[:0]
	lineStart := 0
	for i := 0; i < s.sb.Len(); i++ {
		rowStart := len(flat)
		flat = append(flat, s.sb.lines[(s.sb.start+i)%len(s.sb.lines)]...)
		if s.sb.wrappedAt(i) {
			flat = padTo(flat, rowStart+g.cols)
			continue
		}
		ends = append(ends, len(flat))
		lineStart = len(flat)
	}
	// A line the last history row was still in the middle of carries on onto
	// the screen: it is finished there, as part of the screen's first line.
	carried := flat[lineStart:]
	flat = flat[:lineStart]

	// The screen, as logical lines, as far down as there is anything: the last
	// row with text on it, or the cursor's row if that is further.
	end := cur.Row + 1
	for r := g.rows - 1; r >= end; r-- {
		if !rowIsBlank(g.row(r)) {
			end = r + 1
			break
		}
	}
	var screen [][]Cell
	open := append([]Cell(nil), carried...)
	cursorLine, cursorAt := 0, 0
	for r := 0; r < end; r++ {
		row := g.row(r)
		if r == cur.Row {
			cursorLine, cursorAt = len(screen), len(open)+cur.Col
			if pendingWrap {
				// The row is full and the next character goes on the next
				// row: the cursor is really one past the last cell.
				cursorAt++
			}
		}
		if g.wrapped(r) && r < end-1 {
			open = append(open, row...)
			continue
		}
		screen = append(screen, append(open, trimBlanks(row)...))
		open = nil
	}

	// Break the screen's lines at the new width. There are few of them, so
	// each row is kept as its own slice.
	var gridRows [][]Cell
	var gridWraps []bool
	curRow, curCol := 0, 0
	for i, l := range screen {
		at := -1
		if i == cursorLine {
			at = cursorAt
		}
		first := len(gridRows)
		cr, cc := rewrap(l, cols, at, s.reflowRow(cols), func(row []Cell, w bool) {
			gridRows = append(gridRows, append([]Cell(nil), row...))
			gridWraps = append(gridWraps, w)
		})
		if i == cursorLine {
			curRow, curCol = first+cr, cc
		}
	}

	// Rows of the screen that do not fit the new height go into history,
	// keeping the cursor in view, exactly as if they had scrolled off.
	start := max(0, len(gridRows)-rows)
	if curRow < start {
		start = curRow
	}

	// Refill the history: the old lines rewrapped, then the screen's
	// overflow. The ring keeps its line count as a running total, so numbering
	// stays monotonic for anything holding a line number.
	dropped := s.sb.dropped
	s.sb.reset()
	s.sb.dropped = dropped
	from := 0
	for _, to := range ends {
		rewrap(flat[from:to], cols, -1, s.reflowRow(cols), s.sb.push)
		from = to
	}
	for i := 0; i < start; i++ {
		s.sb.push(gridRows[i], gridWraps[i])
	}
	s.reflowCells, s.reflowEnds = flat[:0], ends[:0]

	cells = make([]Cell, rows*cols)
	for i := range cells {
		cells[i] = Blank
	}
	wrapped = make([]bool, rows)
	for r := 0; r < rows && start+r < len(gridRows); r++ {
		copy(cells[r*cols:(r+1)*cols], gridRows[start+r])
		wrapped[r] = gridWraps[start+r]
	}
	next = cur
	next.Row = clamp(curRow-start, 0, rows-1)
	next.Col = clamp(curCol, 0, cols-1)
	return cells, wrapped, next
}

// reflowRow is the scratch row rewrap breaks lines into, kept between resizes.
func (s *Screen) reflowRow(cols int) []Cell {
	if cap(s.reflowScratch) < cols {
		s.reflowScratch = make([]Cell, cols)
	}
	return s.reflowScratch[:cols]
}

// gridRows is a grid as reflow reads it, whichever buffer it belongs to.
type gridRows struct {
	rows, cols int
	row        func(r int) []Cell
	wrapped    func(r int) bool
}

// rewrap breaks one logical line into rows of cols cells, handing each to emit
// along with whether the line carries on past it. The row passed to emit is
// scratch, reused for the next row: emit must copy what it keeps.
//
// It also reports where cell index at lands, for the cursor; pass -1 when
// there is no cursor in the line.
//
// A wide character never straddles the edge: one that would start in the last
// column leaves it blank and moves to the next row, which is what printing it
// would have done.
func rewrap(cells []Cell, cols, at int, row []Cell, emit func([]Cell, bool)) (atRow, atCol int) {
	// The cursor may sit past the end of the text - after a prompt's trailing
	// space, say. Blanks out to it keep its place in the line.
	if at >= len(cells) {
		cells = padTo(append([]Cell(nil), cells...), at+1)
	}

	blankRow(row)
	n, col := 0, 0
	for i := 0; i < len(cells); i++ {
		c := cells[i]
		w := c.Width()
		if w == 0 {
			continue // a continuation cell travels with its wide character
		}
		if col+w > cols {
			if w > cols {
				c, w = Blank, 1 // a wide character in a one-column terminal
			} else {
				emit(row, true)
				blankRow(row)
				n, col = n+1, 0
			}
		}
		if i == at {
			atRow, atCol = n, col
		}
		row[col] = c
		if w == 2 {
			row[col+1] = Cell{Attr: c.Attr} // the continuation
		}
		col += w
	}
	emit(row, false)
	return atRow, atCol
}

func blankRow(row []Cell) {
	for i := range row {
		row[i] = Blank
	}
}

// padTo extends a row with blanks to n cells.
func padTo(row []Cell, n int) []Cell {
	for len(row) < n {
		row = append(row, Blank)
	}
	return row
}

// trimBlanks drops trailing plain blanks: they are the unwritten rest of the
// row, not part of the line, and keeping them would make every short line as
// long as the old width.
func trimBlanks(row []Cell) []Cell {
	end := len(row)
	for end > 0 && isPlainBlank(row[end-1]) {
		end--
	}
	return append([]Cell(nil), row[:end]...)
}

func rowIsBlank(row []Cell) bool {
	for _, c := range row {
		if !isPlainBlank(c) {
			return false
		}
	}
	return true
}
