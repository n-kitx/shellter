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

// reflowInactive reflows the primary screen while it waits behind the
// alternate one, so that leaving vim after a resize shows the shell's text
// rewrapped to the new width rather than cut at the old one.
func (s *Screen) reflowInactive(rows, cols int) {
	b := s.inactive
	if b == nil {
		return
	}
	oldCols := s.cols
	cells, wrapped, cur := s.reflowPrimary(gridRows{
		rows: len(b.cells) / oldCols,
		cols: oldCols,
		row: func(r int) []Cell {
			p := b.rowIndex[r] * oldCols
			return b.cells[p : p+oldCols]
		},
		wrapped: func(r int) bool { return b.wrapped[b.rowIndex[r]] },
	}, b.cur, b.pendingWrap, rows, cols)

	b.cells, b.wrapped, b.cur = cells, wrapped, cur
	b.rowIndex = identityRows(rows)
	b.saved.cur.Row = clamp(b.saved.cur.Row, 0, rows-1)
	b.saved.cur.Col = clamp(b.saved.cur.Col, 0, cols-1)
	b.top, b.bottom = 0, rows-1
	b.pendingWrap = false
}
