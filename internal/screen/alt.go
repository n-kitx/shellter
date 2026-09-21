package screen

// The alternate screen buffer.
//
// A terminal keeps two grids. The primary one is where the shell's output
// accumulates and where scrollback comes from. The alternate one is a scratch
// surface for a full-screen program: vim, less, top. When the program exits it
// switches back, and the shell's output is still there, untouched - which is
// why quitting vim does not leave the file on your screen.
//
//	"ESC [ ? 1049 h"   save the cursor, switch to alt, clear it
//	"ESC [ ? 1049 l"   switch back to primary, restore the cursor
//
// Nothing on the alternate screen is ever archived: it is a picture, not
// output. That single rule is also why scrolling back while vim is running does
// nothing sensible, so we disable it there.

// buffer is one grid plus the state that belongs with it. The inactive buffer
// sits here while the other one is on screen.
type buffer struct {
	cells       []Cell
	rowIndex    []int
	wrapped     []bool
	cur         Cursor
	pen         Attr
	saved       savedCursor
	pendingWrap bool
	top, bottom int
	charsets    charsets
}

// enterAlt switches to the alternate screen. It is a no-op if already there,
// because a program that sets the mode twice must not lose the primary grid.
func (s *Screen) enterAlt() {
	if s.altActive {
		return
	}
	s.inactive = s.current()
	s.cells = make([]Cell, s.rows*s.cols)
	s.rowIndex = identityRows(s.rows)
	s.wrapped = make([]bool, s.rows)
	s.altActive = true
	s.cur = Cursor{}
	s.saved = savedCursor{}
	s.pendingWrap = false
	s.resetScrollRegion()
	s.blankAll()

	// Looking at history makes no sense once the live screen is a program's
	// canvas, so the view returns to it.
	s.offset = 0
}

// leaveAlt switches back to the primary screen, discarding the alternate grid.
func (s *Screen) leaveAlt() {
	if !s.altActive {
		return
	}
	b := s.inactive
	s.inactive = nil
	s.altActive = false
	s.cells = b.cells
	s.rowIndex = b.rowIndex
	s.wrapped = b.wrapped
	s.cur = b.cur
	s.pen = b.pen
	s.saved = b.saved
	s.pendingWrap = b.pendingWrap
	s.top, s.bottom = b.top, b.bottom
	s.charsets = b.charsets
	s.offset = 0
}

// current captures everything that belongs to the buffer on screen.
func (s *Screen) current() *buffer {
	return &buffer{
		cells:       s.cells,
		rowIndex:    s.rowIndex,
		wrapped:     s.wrapped,
		cur:         s.cur,
		pen:         s.pen,
		saved:       s.saved,
		pendingWrap: s.pendingWrap,
		top:         s.top,
		bottom:      s.bottom,
		charsets:    s.charsets,
	}
}

// AltScreen reports whether the alternate buffer is on screen. The UI reads it
// to decide what the scroll wheel should do.
func (s *Screen) AltScreen() bool { return s.altActive }

// resizeInactive keeps the off-screen buffer the same size as the screen, so
// that leaving the alternate screen after a resize does not restore a grid of
// the wrong shape.
//
// The primary buffer's content is kept top-left, exactly as the visible grid's
// is: reflowing wrapped lines needs to know which line breaks were soft, which
// this terminal does not yet record.
func (s *Screen) resizeInactive(rows, cols int) {
	b := s.inactive
	if b == nil {
		return
	}
	next := make([]Cell, rows*cols)
	for i := range next {
		next[i] = Blank
	}
	oldRows := len(b.cells) / s.cols
	for row := 0; row < min(rows, oldRows); row++ {
		p := b.rowIndex[row] * s.cols
		copy(next[row*cols:(row+1)*cols], b.cells[p:p+min(cols, s.cols)])
	}
	b.cells = next
	b.rowIndex = identityRows(rows)
	b.wrapped = make([]bool, rows) // see Resize: they described the old width

	b.cur.Row = clamp(b.cur.Row, 0, rows-1)
	b.cur.Col = clamp(b.cur.Col, 0, cols-1)
	b.top, b.bottom = 0, rows-1
	b.pendingWrap = false
}
