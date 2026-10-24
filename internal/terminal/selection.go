package terminal

import (
	"unicode"

	"github.com/n-kitx/shellter/internal/screen"
)

// Selecting text with the mouse.
//
// A selection belongs to the application, not to the terminal: the program
// running in the pane has no idea one exists, which is why none of this is in
// package screen. What screen does provide is the two things selection cannot
// work without - a way to name a line that does not change as output arrives
// (screen.Pos), and a way to read text back out of a range of them.
//
// Positions are absolute, never view rows. A selection made now has to still
// mean the same words after a screenful of output has pushed them up the
// window, and it has to survive the line under it scrolling into history.

type selectMode int

const (
	// selectChar is a plain drag: the selection ends wherever the pointer is.
	selectChar selectMode = iota
	// selectWord follows a double click, selectLine a triple one. Dragging
	// after either keeps snapping to whole words or whole lines.
	selectWord
	selectLine
)

// A selection is a range of the session's output marked with the mouse.
type selection struct {
	// origin is where the gesture started, before any snapping to words or
	// lines. Extending measures from it rather than from anchor, so dragging
	// back past the start pivots correctly instead of dragging the anchor
	// along with the pointer.
	origin screen.Pos
	// anchor and head are the selection as it stands, in no particular order:
	// head follows the pointer and may be either side of anchor.
	anchor, head screen.Pos
	mode         selectMode
	// block makes the selection a rectangle - the same columns of every line
	// between anchor and head - rather than a run of text. Option+drag.
	block bool
	// active distinguishes "nothing selected" from "an empty selection at the
	// place that was clicked", which is what a click with no drag produces.
	active bool
}

// empty reports whether there is nothing to copy.
func (s *selection) empty() bool { return !s.active || s.anchor == s.head }

// clear forgets the selection.
func (s *selection) clear() { *s = selection{} }

// bounds returns the selection in reading order: first position first.
func (s *selection) bounds() (from, to screen.Pos) {
	if s.head.Before(s.anchor) {
		return s.head, s.anchor
	}
	return s.anchor, s.head
}

// A span is the selected columns of one view row, from included and to
// excluded. An empty span means nothing on that row is selected.
type span struct{ from, to int }

func (s span) empty() bool { return s.from >= s.to }

// spansInto describes the selection as one span per view row, so that turning
// a snapshot into a frame costs two integer comparisons per cell rather than a
// question asked of the selection.
//
// top is the absolute line at view row 0. dst is reused between frames; the
// returned slice borrows it.
func (s *selection) spansInto(dst []span, top int64, rows, cols int) []span {
	if cap(dst) < rows {
		dst = make([]span, rows)
	}
	dst = dst[:rows]
	for i := range dst {
		dst[i] = span{}
	}
	if s.empty() {
		return dst
	}

	from, to := s.bounds()
	left, right := s.columns()
	for r := 0; r < rows; r++ {
		line := top + int64(r)
		if line < from.Line || line > to.Line {
			continue
		}
		if s.block {
			dst[r] = span{from: clampInt(left, 0, cols), to: clampInt(right, 0, cols)}
			continue
		}
		sp := span{from: 0, to: cols}
		if line == from.Line {
			sp.from = clampInt(from.Col, 0, cols)
		}
		if line == to.Line {
			sp.to = clampInt(to.Col, 0, cols)
		}
		dst[r] = sp
	}
	return dst
}

// text is what this selection would put on the clipboard.
func (s *selection) text(scr *screen.Screen) string {
	if s.empty() {
		return ""
	}
	from, to := s.bounds()
	if s.block {
		left, right := s.columns()
		return scr.BlockText(from.Line, to.Line, left, right)
	}
	return scr.Text(from, to)
}

// columns is a block selection's left and right edges, whichever way it was
// dragged: from the leftmost column to one past the rightmost.
func (s *selection) columns() (left, right int) {
	left, right = s.anchor.Col, s.head.Col
	if right < left {
		left, right = right, left
	}
	return left, right
}

// ---------------------------------------------------------------------------
// Double and triple clicks
// ---------------------------------------------------------------------------

// begin starts a selection at a position. clicks is 1 for a plain drag, 2 to
// select whole words and 3 for whole lines; block makes a plain drag select a
// rectangle.
func (s *selection) begin(at screen.Pos, clicks int, block bool) {
	mode := selectChar
	switch {
	case clicks >= 3:
		mode = selectLine
	case clicks == 2:
		mode = selectWord
	}
	*s = selection{origin: at, anchor: at, head: at, mode: mode, active: true,
		block: block && mode == selectChar}
}

// extend moves the far end of the selection to a position. The near end goes
// back to the origin first, so that a word selection dragged backwards and then
// forwards again ends up where it started rather than somewhere it grew to.
func (s *selection) extend(to screen.Pos) {
	if !s.active {
		return
	}
	s.anchor, s.head = s.origin, to
}

// snap grows the selection out to whole words or whole lines, depending on how
// many times the mouse was clicked. A double click on "shellter" selects all of
// it, and dragging from there carries on selecting whole words.
func (s *selection) snap(scr *screen.Screen, scratch []rune) {
	from, to := s.bounds()
	switch s.mode {
	case selectWord:
		start, _ := wordAt(scr.LineRunes(from.Line, scratch), from.Col)
		_, end := wordAt(scr.LineRunes(to.Line, scratch), to.Col)
		from.Col, to.Col = start, end
	case selectLine:
		from.Col = 0
		to = screen.Pos{Line: to.Line + 1, Col: 0}
	default:
		return
	}
	// Reading order, because snapping has just decided which end is which.
	s.anchor, s.head = from, to
}

// A charClass is what double-clicking treats as one thing. Three of them, not
// two: without a class for punctuation, double-clicking "&&" would select the
// spaces on either side of it as well, because they would be the same
// not-a-word as the ampersands.
type charClass int

const (
	classSpace charClass = iota
	classWord
	classOther
)

func classOf(r rune) charClass {
	switch {
	case r == ' ' || r == '\t':
		return classSpace
	case wordish(r):
		return classWord
	default:
		return classOther
	}
}

// wordAt finds the edges of the run under col: the word, the gap, or the
// punctuation. Every column belongs to something, so a double click anywhere
// selects something rather than nothing.
func wordAt(line []rune, col int) (from, to int) {
	if len(line) == 0 {
		return 0, 0
	}
	col = clampInt(col, 0, len(line)-1)
	want := classOf(line[col])

	from = col
	for from > 0 && classOf(line[from-1]) == want {
		from--
	}
	to = col
	for to+1 < len(line) && classOf(line[to+1]) == want {
		to++
	}
	return from, to + 1
}

// wordish says whether a character belongs to a word.
//
// The punctuation here is not arbitrary: '/', '.', '-', '_', '~' and ':' are
// what a double click has to include for a path or a URL to come out whole,
// which is most of what anyone double-clicks in a terminal.
func wordish(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	switch r {
	case '_', '-', '.', '/', '~', ':', '@', '+', '=':
		return true
	}
	return false
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
