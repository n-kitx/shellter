package screen

import "strings"

// Naming a line, and reading text back out of one.
//
// Everything else in this package works in *view* coordinates: row 0 is the top
// of the window, and which text that is changes every time the screen scrolls.
// That is right for drawing and wrong for remembering. A selection made ten
// seconds ago has to still mean the same words after a screenful of output, and
// it has to survive the oldest line being evicted from scrollback.
//
// So lines are also numbered absolutely, from the start of the session:
//
//	line 0       the first line ever written
//	...          ...later evicted from scrollback, but the number is not reused
//	TopLine()    the line currently drawn at view row 0
//	TotalLines() one past the last line of the live grid
//
// The numbers only ever go up. Converting between the two is arithmetic the
// caller can do itself - view row v is TopLine() + v - which is why this file
// exposes the anchors rather than a pair of conversion functions.

// A Pos names a place in the session's output: an absolute line and a column.
type Pos struct {
	Line int64
	Col  int
}

// Before reports whether p comes earlier in the output than q.
func (p Pos) Before(q Pos) bool {
	return p.Line < q.Line || (p.Line == q.Line && p.Col < q.Col)
}

// firstLine is the absolute number of the oldest line still held: everything
// before it has been evicted from scrollback and cannot be read back.
func (s *Screen) firstLine() int64 { return s.sb.Dropped() }

// gridLine is the absolute number of view row 0 of the live grid, which is also
// one past the newest line in scrollback.
func (s *Screen) gridLine() int64 { return s.sb.Dropped() + int64(s.sb.Len()) }

// TopLine is the absolute number of the line shown at view row 0. Scrolling
// back moves it down; it is gridLine when looking at the live screen.
func (s *Screen) TopLine() int64 { return s.gridLine() - int64(s.offset) }

// TotalLines is one past the last line of the live grid: the end of everything
// that can be selected.
func (s *Screen) TotalLines() int64 { return s.gridLine() + int64(s.rows) }

// FirstLine is the oldest line still readable. A selection older than this has
// scrolled out of history and Text will return only the part that survives.
func (s *Screen) FirstLine() int64 { return s.firstLine() }

// lineAt reads absolute line n into dst, which must be cols long, and reports
// whether the line continues on the next one. ok is false when n names a line
// that has been evicted or does not exist yet.
func (s *Screen) lineAt(n int64, dst []Cell) (wrapped, ok bool) {
	switch grid := s.gridLine(); {
	case n < s.firstLine() || n >= s.TotalLines():
		return false, false
	case n < grid: // in history
		i := int(n - s.firstLine())
		s.sb.lineInto(dst, i)
		return s.sb.wrappedAt(i), true
	default: // on the live grid
		r := int(n - grid)
		copy(dst, s.row(r))
		return s.rowWrapped(r), true
	}
}

// Text returns the text between two positions, from included and to excluded.
//
// Three rules, and they are the whole of why this is not a loop over cells:
//
//   - Trailing blanks are dropped. A terminal line is padded to the full width;
//     the spaces after the last character were never typed and should not be
//     pasted.
//   - A line that wrapped joins the next one with nothing between them. The
//     person who typed a long command typed one line, whatever the width of the
//     window made of it.
//   - A line that ended joins the next one with "\n".
//
// Positions outside what is still held are clamped, so a selection that has
// half scrolled out of history returns the half that is left.
func (s *Screen) Text(from, to Pos) string {
	if !from.Before(to) {
		return ""
	}
	first, last := s.FirstLine(), s.TotalLines()
	if from.Line < first {
		from = Pos{Line: first, Col: 0}
	}
	if to.Line >= last {
		to = Pos{Line: last - 1, Col: s.cols}
	}
	if !from.Before(to) {
		return ""
	}

	buf := make([]Cell, s.cols)
	var b strings.Builder

	for n := from.Line; n <= to.Line; n++ {
		wrapped, ok := s.lineAt(n, buf)
		if !ok {
			continue
		}

		start, end := 0, s.cols
		if n == from.Line {
			start = clamp(from.Col, 0, s.cols)
		}
		if n == to.Line {
			end = clamp(to.Col, 0, s.cols)
		}

		// Only a line that runs to its right edge keeps its trailing spaces,
		// and only because the next line is about to be joined onto it.
		if !wrapped || end < s.cols {
			for end > start && isBlankish(buf[end-1]) {
				end--
			}
		}
		writeCells(&b, buf[clamp(start, 0, end):end])

		if n == to.Line {
			break
		}
		if !wrapped {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// writeCells appends the text of a run of cells, skipping the second half of
// every wide character - it has no text of its own.
func writeCells(b *strings.Builder, cells []Cell) {
	for _, c := range cells {
		if c.IsContinuation() {
			continue
		}
		b.WriteRune(c.Rune)
		b.WriteString(c.Comb)
	}
}

// isBlankish reports whether a cell is a space, whatever colour it was painted.
// Unlike isPlainBlank this ignores attributes: a space with a background is
// still a space once it is text in a paste buffer.
func isBlankish(c Cell) bool { return c.Rune == ' ' && c.Comb == "" }

// LineRunes reads absolute line n as one rune per column, into dst.
//
// One rune per *column*, not per character: the second half of a wide character
// repeats the first half's rune, so a caller scanning columns - looking for the
// edges of a word, say - sees one character occupying two columns rather than a
// hole it has to know about. Columns past the end of the line read as spaces.
//
// The returned slice borrows dst when it is long enough, so a caller can scan a
// line without allocating.
func (s *Screen) LineRunes(n int64, dst []rune) []rune {
	if cap(dst) < s.cols {
		dst = make([]rune, s.cols)
	}
	dst = dst[:s.cols]

	buf := make([]Cell, s.cols)
	if _, ok := s.lineAt(n, buf); !ok {
		for i := range dst {
			dst[i] = ' '
		}
		return dst
	}
	for i, c := range buf {
		if c.IsContinuation() && i > 0 {
			dst[i] = dst[i-1]
			continue
		}
		dst[i] = c.Rune
	}
	return dst
}

// LineChars reads absolute line n as the characters on it and the column each
// one starts in, trailing blanks dropped. It is the shape a search wants: one
// entry per character - a wide one is not doubled the way LineRunes doubles
// it - and a column to turn a match back into cells.
//
// The slices passed in are reused when they are long enough.
func (s *Screen) LineChars(n int64, runes []rune, cols []int) ([]rune, []int) {
	runes, cols = runes[:0], cols[:0]
	buf := make([]Cell, s.cols)
	if _, ok := s.lineAt(n, buf); !ok {
		return runes, cols
	}
	end := len(buf)
	for end > 0 && buf[end-1].Rune == ' ' && buf[end-1].Comb == "" {
		end--
	}
	for i := 0; i < end; i++ {
		if buf[i].IsContinuation() {
			continue
		}
		runes = append(runes, buf[i].Rune)
		cols = append(cols, i)
	}
	return runes, cols
}
