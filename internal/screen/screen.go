// Package screen keeps the virtual terminal screen: a grid of cells and a
// cursor, updated by the events the parser produces.
//
// It is the answer to "what would the terminal look like right now?". It does
// no I/O and knows nothing about PTYs, zsh, fonts or drawing. A renderer will
// later read this state; nothing here reaches back out.
package screen

import (
	"net/url"
	"strings"

	"github.com/thakurankit7/shellter/internal/parser"
	"github.com/thakurankit7/shellter/internal/text"
)

// Cell is one character position on the screen: what is there, and how it
// looks. A cell keeps the pen it was printed with, which is why changing the
// colour later does not repaint what is already on screen.
//
// A wide character (CJK, most emoji) occupies two cells: the character itself,
// followed by a *continuation* cell holding rune 0. The continuation is a real
// cell - it takes the same background, and the cursor can land on it - but
// nothing is drawn there, because the glyph to its left already covers it.
// The field order is not accidental. Comb is a string, so it wants eight-byte
// alignment; with it between the two small fields the struct padded out to 40
// bytes, and putting it first brings that down to 32. Every scroll, fill and
// snapshot copies cells by the thousand, so a fifth off the size of one is a
// fifth off all of that.
type Cell struct {
	// Comb holds combining marks attached to Rune - an accent, a variation
	// selector, a zero-width joiner. A string rather than a []rune so that Cell
	// stays comparable, and because it is empty almost always.
	Comb string
	Rune rune
	Attr Attr
}

// IsContinuation reports whether this cell is the second half of a wide
// character rather than a character of its own.
func (c Cell) IsContinuation() bool { return c.Rune == 0 }

// Width is how many columns the cell occupies: 2 for a wide character, 0 for a
// continuation cell, 1 otherwise.
func (c Cell) Width() int {
	if c.IsContinuation() {
		return 0
	}
	return text.Width(c.Rune)
}

// Text is what should be drawn in this cell: the character plus anything
// combining onto it.
func (c Cell) Text() string {
	if c.IsContinuation() {
		return ""
	}
	return string(c.Rune) + c.Comb
}

// Blank is what an erased cell holds.
var Blank = Cell{Rune: ' '}

// Cursor is a position on the grid, zero-based. Row 0 is the top line,
// column 0 the leftmost column.
//
// Note that the terminal protocol itself is one-based: CSI 1;1 H means the
// top-left corner. The conversion happens in exactly one place, moveTo.
type Cursor struct {
	Row int
	Col int
}

// Screen is the in-memory terminal. Not safe for concurrent use; the goroutine
// reading the PTY owns it.
type Screen struct {
	rows, cols int
	cells      []Cell // the rows, in no particular order - see rowIndex
	// rowIndex maps a logical row to the physical row holding it.
	//
	// Scrolling a terminal means every line moves up by one. Doing that to the
	// text costs a copy of the whole grid per line of output, which profiling
	// put at 41% of the time spent absorbing output. Doing it to the index is a
	// rotation of one small array of ints, and the text never moves at all.
	//
	//	logical      rowIndex        cells
	//	  0  ───────────► 2 ───────► "third line"
	//	  1  ───────────► 0 ───────► "first line"
	//	  2  ───────────► 1 ───────► "second line"
	rowIndex []int
	// wrapped says a row ran off its right edge and continues on the next one.
	// Indexed by PHYSICAL row, like cells, so that rotating rowIndex carries
	// each flag along with the text it describes and nothing has to be moved.
	wrapped []bool
	cur     Cursor

	// pen is the attribute printed characters are stamped with, changed by SGR.
	pen Attr

	// mode holds the terminal modes set through "ESC [ ? n h/l".
	mode Modes

	// saved is the cursor and pen stashed by DECSC / "ESC [ s".
	saved savedCursor

	// top and bottom are the scroll region, inclusive and zero-based. They are
	// the whole screen unless a program set margins with DECSTBM.
	top, bottom int

	// tabs marks the columns a Tab character jumps to.
	tabs []bool

	// altActive says the alternate buffer is on screen, and inactive holds the
	// buffer that is not - the primary one, waiting for vim to exit.
	altActive bool
	inactive  *buffer

	// charsets is the ASCII/line-drawing translation state.
	charsets charsets

	// lastPrint is the character REP ("ESC [ n b") repeats.
	lastPrint rune

	// cursorStyle is the shape DECSCUSR asked for. Only the renderer reads it.
	cursorStyle CursorStyle

	// title is what OSC 0/2 last set the window title to, and titleStack the
	// titles a program has pushed aside while it borrows the window.
	title      string
	titleStack []string

	// dirHost and dirPath are where the shell last said it was, through OSC 7.
	// A reset leaves them alone: clearing the screen does not move the shell.
	dirHost, dirPath string

	// Scratch for reflow, kept between resizes so that dragging a divider -
	// a resize per cell of movement - does not allocate the history anew
	// each time. See reflow.go.
	reflowCells   []Cell
	reflowEnds    []int
	reflowScratch []Cell

	// replies are bytes owed to the program in answer to a query. The
	// application layer drains them into the PTY; see reply.go.
	replies []byte

	// sb holds lines that have scrolled off the top, and offset is how far the
	// view has been scrolled back into them: 0 means "showing the live grid".
	//
	// The grid is never touched by scrolling. Looking at history is a question
	// of what to *display*, not of what the terminal state is.
	sb     *scrollback
	offset int

	// pendingWrap is set after printing into the last column: the cursor stays
	// put and only wraps when another character actually arrives. Without this
	// flag, a line exactly as wide as the screen would scroll the moment it was
	// finished, one line too early.
	pendingWrap bool
}

type savedCursor struct {
	cur         Cursor
	pen         Attr
	pendingWrap bool
	valid       bool
}

// New returns a blank screen of the given size.
// DefaultScrollback is how many lines of history a new Screen keeps.
const DefaultScrollback = 1000

// New returns a blank screen of the given size with the default scrollback.
func New(rows, cols int) *Screen {
	return NewWithScrollback(rows, cols, DefaultScrollback)
}

// NewWithScrollback returns a blank screen keeping at most scrollback lines of
// history. Zero disables history entirely.
func NewWithScrollback(rows, cols, scrollbackLines int) *Screen {
	if rows < 1 {
		rows = 1
	}
	if cols < 1 {
		cols = 1
	}
	s := &Screen{
		rows:     rows,
		cols:     cols,
		cells:    make([]Cell, rows*cols),
		rowIndex: identityRows(rows),
		wrapped:  make([]bool, rows),
		mode:     Modes{AutoWrap: true, CursorVisible: true},
		bottom:   rows - 1,
		tabs:     defaultTabs(cols),
		charsets: defaultCharsets(),
		sb:       newScrollback(scrollbackLines),
	}
	s.blankAll()
	return s
}

// row returns the cells of one logical row. Everything that touches the grid
// goes through here, which is what keeps the indirection in one place.
func (s *Screen) row(r int) []Cell {
	p := s.rowIndex[r] * s.cols
	return s.cells[p : p+s.cols]
}

// identityRows is the starting arrangement: logical row n is physical row n.
func identityRows(rows int) []int {
	idx := make([]int, rows)
	for i := range idx {
		idx[i] = i
	}
	return idx
}

// rotateRows moves the rows of [top,bottom] up by n, wrapping the first n round
// to the end. Three reversals, no allocation, and the rows that scrolled off
// become the ones waiting to be blanked at the bottom.
func (s *Screen) rotateRows(top, bottom, n int) {
	seg := s.rowIndex[top : bottom+1]
	if n <= 0 || n >= len(seg) {
		return
	}
	reverseInts(seg[:n])
	reverseInts(seg[n:])
	reverseInts(seg)
}

func reverseInts(v []int) {
	for i, j := 0, len(v)-1; i < j; i, j = i+1, j-1 {
		v[i], v[j] = v[j], v[i]
	}
}

// Size reports the current dimensions.
func (s *Screen) Size() (rows, cols int) { return s.rows, s.cols }

// Cursor reports where the next printed character will go.
func (s *Screen) Cursor() Cursor { return s.cur }

// ApplicationCursorKeys reports whether DECCKM is set, i.e. whether the arrow
// keys should send ESC O A rather than ESC [ A. zsh's line editor turns this on
// while it is reading a line.
func (s *Screen) ApplicationCursorKeys() bool { return s.mode.ApplicationCursor }

// Modes returns the current terminal modes.
func (s *Screen) Modes() Modes { return s.mode }

// Pen returns the attribute the next printed character will carry.
func (s *Screen) Pen() Attr { return s.pen }

// Cell returns the cell at row, col, or a blank cell if that is off-screen.
func (s *Screen) Cell(row, col int) Cell {
	if row < 0 || row >= s.rows || col < 0 || col >= s.cols {
		return Blank
	}
	return s.row(row)[col]
}

// Line returns row as text with trailing blanks removed. Handy in tests and
// for a first text-mode renderer.
func (s *Screen) Line(row int) string {
	if row < 0 || row >= s.rows {
		return ""
	}
	var b strings.Builder
	b.Grow(s.cols)
	for _, c := range s.row(row) {
		// A continuation contributes nothing: the wide character before it
		// already stands for both columns.
		if c.IsContinuation() {
			continue
		}
		b.WriteRune(c.Rune)
		b.WriteString(c.Comb)
	}
	return strings.TrimRight(b.String(), " ")
}

// String renders the whole screen as text, dropping trailing blank lines.
func (s *Screen) String() string {
	lines := make([]string, s.rows)
	last := -1
	for row := 0; row < s.rows; row++ {
		lines[row] = s.Line(row)
		if lines[row] != "" {
			last = row
		}
	}
	return strings.Join(lines[:last+1], "\n")
}

// Reset blanks the screen and homes the cursor: RIS, "ESC c". Everything a
// program could have changed goes back to how the terminal started, which is
// what `reset` in the shell is for when a program has left the terminal
// confused.
func (s *Screen) Reset() {
	s.altActive = false
	s.inactive = nil
	s.cells = make([]Cell, s.rows*s.cols)
	s.rowIndex = identityRows(s.rows)
	s.wrapped = make([]bool, s.rows)
	s.pen = Attr{}
	s.blankAll()
	s.cur = Cursor{}
	s.pendingWrap = false
	s.mode = Modes{AutoWrap: true, CursorVisible: true}
	s.saved = savedCursor{}
	s.resetScrollRegion()
	s.tabs = defaultTabs(s.cols)
	s.charsets = defaultCharsets()
	s.cursorStyle = CursorBlock
	s.title = ""
	s.titleStack = nil
	s.sb.clear()
	s.offset = 0
}

// Resize changes the grid. On the primary screen the text is reflowed to the
// new width - see reflow.go - and on the alternate screen it is cropped, since
// the program there redraws.
func (s *Screen) Resize(rows, cols int) {
	if rows < 1 || cols < 1 || (rows == s.rows && cols == s.cols) {
		return
	}

	if s.altActive {
		// A full-screen program is told its new size and redraws, so its grid
		// is simply cropped or extended. The shell's screen behind it is
		// reflowed, so that it is right when the program exits.
		s.cells = cropGrid(s.rows, s.cols, s.row, rows, cols)
		s.wrapped = make([]bool, rows)
		s.cur.Row = clamp(s.cur.Row, 0, rows-1)
		s.cur.Col = clamp(s.cur.Col, 0, cols-1)
		s.reflowInactive(rows, cols)
	} else {
		cells, wrapped, cur := s.reflowPrimary(gridRows{
			rows: s.rows, cols: s.cols, row: s.row, wrapped: s.rowWrapped,
		}, s.cur, s.pendingWrap, rows, cols)
		s.cells, s.wrapped, s.cur = cells, wrapped, cur
	}

	s.rows, s.cols = rows, cols
	s.rowIndex = identityRows(rows)
	s.pendingWrap = false
	s.offset = 0
	s.saved.cur.Row = clamp(s.saved.cur.Row, 0, rows-1)
	s.saved.cur.Col = clamp(s.saved.cur.Col, 0, cols-1)

	// A scroll region describes rows that may no longer exist, so it opens back
	// up - which is also what xterm does. The program is told about the resize
	// through SIGWINCH and sets its margins again.
	s.resetScrollRegion()
	s.resizeTabs(cols)
	s.repairWide()
}

// cropGrid copies a grid into a new size, keeping the top-left corner: what
// fits stays where it was, and the rest is cut or left blank.
func cropGrid(oldRows, oldCols int, row func(int) []Cell, rows, cols int) []Cell {
	next := make([]Cell, rows*cols)
	for i := range next {
		next[i] = Blank
	}
	for r := 0; r < min(rows, oldRows); r++ {
		copy(next[r*cols:(r+1)*cols], row(r)[:min(cols, oldCols)])
	}
	return next
}

// repairWide blanks any wide character left without its continuation, or any
// continuation left without its character. Resizing can cut one in half at the
// new right edge.
func (s *Screen) repairWide() {
	for r := 0; r < s.rows; r++ {
		line := s.row(r)
		for col := range line {
			c := line[col]
			switch {
			case c.IsContinuation():
				if col == 0 || line[col-1].Width() != 2 {
					blankCell(line, col)
				}
			case c.Width() == 2:
				if col+1 >= s.cols || !line[col+1].IsContinuation() {
					blankCell(line, col)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Scrollback
// ---------------------------------------------------------------------------

// ScrollbackLen is how many lines of history are currently held.
func (s *Screen) ScrollbackLen() int { return s.sb.Len() }

// ScrollbackCap is how many lines of history can be held.
func (s *Screen) ScrollbackCap() int { return s.sb.Cap() }

// SetScrollback changes how much history is kept, keeping the newest lines.
func (s *Screen) SetScrollback(lines int) {
	s.sb.resize(lines)
	s.offset = clamp(s.offset, 0, s.sb.Len())
}

// ScrollOffset is how far the view has been scrolled back, in lines. Zero means
// the live grid is on screen.
func (s *Screen) ScrollOffset() int { return s.offset }

// ScrollBy moves the view: positive goes back into history, negative returns
// towards the present. It reports whether anything actually moved.
func (s *Screen) ScrollBy(lines int) bool {
	// History belongs to the primary screen. While a full-screen program owns
	// the grid, scrolling back would splice its picture onto old output.
	if s.altActive {
		return false
	}
	before := s.offset
	s.offset = clamp(s.offset+lines, 0, s.sb.Len())
	return s.offset != before
}

// ScrollToBottom returns to the live grid, which is what typing should do.
func (s *Screen) ScrollToBottom() bool {
	if s.offset == 0 {
		return false
	}
	s.offset = 0
	return true
}

// ScrollToTop jumps to the oldest line held.
func (s *Screen) ScrollToTop() bool { return s.ScrollBy(s.sb.Len()) }

// view returns the rows to display: history lines first when scrolled back,
// then as much of the live grid as still fits.
//
//	offset 0          offset 2
//	┌──────────┐      ┌──────────┐
//	│ grid  0  │      │ history  │  sb.Len()-2
//	│ grid  1  │      │ history  │  sb.Len()-1
//	│ grid  2  │      │ grid  0  │
//	│ grid  3  │      │ grid  1  │
//	└──────────┘      └──────────┘
func (s *Screen) view() []Cell { return s.viewInto(nil) }

// viewInto writes the view into buf when it is the right size, and allocates
// otherwise.
func (s *Screen) viewInto(buf []Cell) []Cell {
	out := buf
	if len(out) != len(s.cells) {
		out = make([]Cell, len(s.cells))
	}

	if s.offset == 0 {
		for r := 0; r < s.rows; r++ {
			copy(out[r*s.cols:(r+1)*s.cols], s.row(r))
		}
		return out
	}

	fromHistory := s.offset
	if fromHistory > s.rows {
		fromHistory = s.rows
	}
	first := s.sb.Len() - s.offset

	for row := 0; row < fromHistory; row++ {
		s.sb.lineInto(out[row*s.cols:(row+1)*s.cols], first+row)
	}
	for row := fromHistory; row < s.rows; row++ {
		copy(out[row*s.cols:(row+1)*s.cols], s.row(row-fromHistory))
	}
	return out
}

// ---------------------------------------------------------------------------
// Event handling
// ---------------------------------------------------------------------------

// Apply updates the screen for one parser event.
//
// Sequences that are recognised but not implemented are ignored on purpose:
// the parser sees more than the screen acts on, and a terminal that ignores a
// sequence it does not know is behaving correctly. Guessing is what leaves a
// screen full of garbage.
func (s *Screen) Apply(e parser.Event) {
	switch e.Kind {
	case parser.EventPrint:
		s.print(e.Rune)
	case parser.EventControl:
		s.control(byte(e.Rune))
	case parser.EventCSI:
		s.csi(e)
	case parser.EventEscape:
		s.escape(e)
	case parser.EventOSC:
		s.osc(e.Data)
	}
}

// osc handles the Operating System Commands worth acting on. "0;text" sets both
// the window title and the icon name, "2;text" just the title - which is what
// zsh, vim and git send on every prompt and every file opened.
func (s *Screen) osc(data string) {
	cmd, text, found := strings.Cut(data, ";")
	if !found {
		return
	}
	switch cmd {
	case "0", "2":
		s.title = text
	case "7":
		s.setDirectory(text)
	}
}

// setDirectory records an OSC 7 report: "file://host/path", with the path
// percent-encoded the way any URL's is. The host is kept, not checked - a
// shell on the far end of ssh reports its own machine's directories, and only
// the caller knows which machine is this one.
func (s *Screen) setDirectory(report string) {
	u, err := url.Parse(report)
	if err != nil || u.Scheme != "file" || !strings.HasPrefix(u.Path, "/") {
		return
	}
	s.dirHost, s.dirPath = u.Host, u.Path
}

// Directory is where the shell last said it was working, and on which host,
// or "", "" if it never said. Shells that report it do so at every prompt.
func (s *Screen) Directory() (host, path string) { return s.dirHost, s.dirPath }

// Title is the window title the program last asked for, or "" if it never did.
func (s *Screen) Title() string { return s.title }

// print writes a character at the cursor and advances by its width.
//
// Three cases, and the whole of Unicode handling in the screen is here:
//
//	width 0  a combining mark: it joins the character already printed
//	width 1  the ordinary case
//	width 2  a wide character: it needs two cells, so it may not fit
//
// The wrap to the next line happens on the character *after* the one that
// filled the last column - see pendingWrap.
func (s *Screen) print(r rune) {
	r = s.charsets.active().translate(r)
	w := text.Width(r)
	if w == 0 {
		s.combine(r)
		return
	}
	s.lastPrint = r

	if s.pendingWrap {
		// This is the moment the wrap actually happens: the row being left
		// continues on the next one.
		s.setWrapped(s.cur.Row, true)
		s.cur.Col = 0
		s.lineFeed()
		s.pendingWrap = false
	}

	// A wide character cannot straddle the right edge: it either moves to the
	// next line whole, or - with autowrap off - is dropped.
	if w == 2 && s.cur.Col+1 >= s.cols {
		if !s.mode.AutoWrap || s.cols < 2 {
			return
		}
		s.cur.Col = 0
		s.lineFeed()
	}

	// Insert mode: the character pushes the rest of the line along rather than
	// overwriting it.
	if s.mode.Insert {
		s.insertChars(w)
	}

	line := s.row(s.cur.Row)
	col := s.cur.Col
	splitWide(line, col)
	line[col] = Cell{Rune: r, Attr: s.pen}
	if w == 2 {
		splitWide(line, col+1)
		// The continuation carries the same pen, so a coloured background
		// covers both halves.
		line[col+1] = Cell{Rune: 0, Attr: s.pen}
	}

	// last is the column the character ends in. Landing on the final column
	// arms the deferred wrap instead of moving.
	if last := s.cur.Col + w - 1; last >= s.cols-1 {
		s.cur.Col = s.cols - 1
		if s.mode.AutoWrap {
			s.pendingWrap = true
		}
	} else {
		s.cur.Col += w
	}
}

// maxCombining caps how many marks one cell will accumulate, so that a stream
// of combining characters cannot grow a cell without bound.
const maxCombining = 8

// combine attaches a zero-width character to the cell that was printed last -
// which is the cell *before* the cursor, unless a wrap is pending, in which
// case the cursor is still sitting on it.
func (s *Screen) combine(r rune) {
	col := s.cur.Col
	if !s.pendingWrap {
		col--
	}
	if col < 0 {
		return // nothing has been printed on this line yet
	}

	line := s.row(s.cur.Row)
	// Marks belong on the character, not on its continuation half.
	if line[col].IsContinuation() && col > 0 {
		col--
	}
	if len(line[col].Comb) >= maxCombining*4 {
		return
	}
	line[col].Comb += string(r)
}

// splitWide clears the other half of a wide character overlapping col, so that
// overwriting one half never leaves a dangling glyph behind.
func splitWide(line []Cell, col int) {
	if line[col].IsContinuation() && col > 0 {
		blankCell(line, col-1)
		return
	}
	if line[col].Width() == 2 && col+1 < len(line) && line[col+1].IsContinuation() {
		blankCell(line, col+1)
	}
}

// blankCell empties one cell, keeping whatever colours it already had.
func blankCell(line []Cell, col int) {
	line[col] = Cell{Rune: ' ', Attr: line[col].Attr}
}

func (s *Screen) control(b byte) {
	switch b {
	case '\n', 0x0b, 0x0c: // LF, VT and FF all index down one line
		s.pendingWrap = false
		if s.mode.NewLine {
			s.cur.Col = 0
		}
		s.lineFeed()
	case '\r': // CR: back to the left margin
		s.pendingWrap = false
		s.cur.Col = 0
	case '\b': // BS: one column left, stopping at the margin
		s.pendingWrap = false
		if s.cur.Col > 0 {
			s.cur.Col--
		}
	case '\t': // HT: to the next tab stop, not "print some spaces"
		s.tabForward(1)
	case 0x0e: // SO: use the G1 character set
		s.charsets.shifted = true
	case 0x0f: // SI: use G0
		s.charsets.shifted = false
	}
	// BEL and everything else is ignored, and in particular does not cancel a
	// pending wrap: it did not move the cursor.
}

func (s *Screen) csi(e parser.Event) {
	// Anything that moves or erases cancels a pending wrap; SGR and mode
	// changes do not, because they do not touch the cursor.
	switch e.Command {
	case 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'f', 'd', 'J', 'K', 'X', 's', 'u',
		'@', 'P', 'L', 'M', 'I', 'Z', 'r':
		s.pendingWrap = false
	}

	switch e.Command {
	// --- cursor movement, relative ---
	case 'A': // CUU - cursor up
		s.moveRowBy(-e.Param(0, 1))
	case 'B': // CUD - cursor down
		s.moveRowBy(e.Param(0, 1))
	case 'C': // CUF - cursor forward
		s.cur.Col = clamp(s.cur.Col+e.Param(0, 1), 0, s.cols-1)
	case 'D': // CUB - cursor back
		s.cur.Col = clamp(s.cur.Col-e.Param(0, 1), 0, s.cols-1)
	case 'E': // CNL - n lines down, column 0
		s.moveRowBy(e.Param(0, 1))
		s.cur.Col = 0
	case 'F': // CPL - n lines up, column 0
		s.moveRowBy(-e.Param(0, 1))
		s.cur.Col = 0

	// --- cursor movement, absolute ---
	case 'G', '`': // CHA / HPA - to a column, row unchanged
		s.cur.Col = clamp(e.Param(0, 1)-1, 0, s.cols-1)
	case 'd': // VPA - to a row, column unchanged
		s.moveToRow(e.Param(0, 1))
	case 'H', 'f': // CUP / HVP - absolute position, one-based in the protocol
		s.moveTo(e.Param(0, 1), e.Param(1, 1))

	// --- erasing ---
	case 'J': // ED - erase in display
		s.eraseDisplay(e.Param(0, 0))
	case 'K': // EL - erase in line
		s.eraseLine(e.Param(0, 0))
	case 'X': // ECH - erase n characters from the cursor, cursor unmoved
		s.eraseChars(e.Param(0, 1))

	// --- appearance ---
	case 'm': // SGR - colours and styles
		s.pen.applySGR(e.Params)

	case 'b': // REP - repeat the last printed character
		s.repeatLast(e.Param(0, 1))

	// --- editing: shifting what is already on screen ---
	case '@': // ICH - insert blanks, pushing the line right
		s.insertChars(e.Param(0, 1))
	case 'P': // DCH - delete characters, pulling the line left
		s.deleteChars(e.Param(0, 1))
	case 'L': // IL - insert blank lines, pushing the region down
		s.insertLines(e.Param(0, 1))
	case 'M': // DL - delete lines, pulling the region up
		s.deleteLines(e.Param(0, 1))
	case 'S': // SU - scroll the region up
		if e.Private == 0 {
			s.shiftUp(s.top, s.bottom, e.Param(0, 1), s.archives())
		}
	case 'T': // SD - scroll the region down
		if e.Private == 0 {
			s.shiftDown(s.top, s.bottom, e.Param(0, 1))
		}

	// --- tab stops ---
	case 'I': // CHT - forward n tab stops
		s.tabForward(e.Param(0, 1))
	case 'Z': // CBT - back n tab stops
		s.tabBackward(e.Param(0, 1))
	case 'g': // TBC - clear tab stops
		s.clearTabs(e.Param(0, 0))

	// --- the scroll region ---
	case 'r': // DECSTBM - set top and bottom margins
		if e.Private == 0 {
			s.setScrollRegion(e.Param(0, 1), e.Param(1, s.rows))
		}

	// --- modes and saved state ---
	case 'h', 'l': // SM / RM, private forms DECSET / DECRST
		s.setMode(e, e.Command == 'h')
	case 's': // save cursor (ANSI.SYS form of DECSC)
		s.saveCursor()
	case 'u': // restore cursor
		s.restoreCursor()
	case 'q': // DECSCUSR when it carries the space intermediate
		if len(e.Intermediates) == 1 && e.Intermediates[0] == ' ' {
			s.setCursorStyle(e.Param(0, 0))
		}

	// --- queries: the program is asking, and expects an answer ---
	case 'c': // DA - device attributes
		s.deviceAttributes(csiQuery{private: e.Private})
	case 'n': // DSR - device status, including the cursor position
		s.deviceStatus(csiQuery{private: e.Private, param: e.Param(0, 0)})
	case 't': // window operations: size reports and the title stack
		if e.Private == 0 {
			s.windowOp(csiQuery{param: e.Param(0, 0)})
		}
	}
}

// repeatLast implements REP: print the last character again n times. Terminals
// that support it let a program compress a run of one character.
func (s *Screen) repeatLast(n int) {
	if s.lastPrint == 0 {
		return
	}
	if n > s.cols {
		n = s.cols
	}
	r := s.lastPrint
	for i := 0; i < n; i++ {
		s.print(r)
	}
}

// escape handles the short "ESC x" sequences that affect the screen. The
// intermediate byte, when there is one, says which family it belongs to:
// "ESC ( 0" designates a character set, "ESC # 8" is a test pattern.
func (s *Screen) escape(e parser.Event) {
	if len(e.Intermediates) == 1 {
		switch e.Intermediates[0] {
		case '(', ')':
			s.charsets.designate(e.Intermediates[0], e.Command)
		case '#':
			if e.Command == '8' { // DECALN - fill the screen with E
				s.alignmentTest()
			}
		}
		return
	}

	switch e.Command {
	case '7': // DECSC - save cursor and pen
		s.saveCursor()
	case '8': // DECRC - restore them
		s.restoreCursor()
	case 'D': // IND - index: down one line, scrolling at the bottom
		s.pendingWrap = false
		s.lineFeed()
	case 'E': // NEL - next line: carriage return plus index
		s.pendingWrap = false
		s.cur.Col = 0
		s.lineFeed()
	case 'H': // HTS - set a tab stop at the cursor
		if s.cur.Col < len(s.tabs) {
			s.tabs[s.cur.Col] = true
		}
	case 'M': // RI - reverse index: up one line, scrolling at the *top*
		s.pendingWrap = false
		s.reverseLineFeed()
	case 'c': // RIS - reset to initial state
		s.Reset()
	}
	// ESC = and ESC > (application/numeric keypad) are recognised by the parser
	// and ignored here: our keypad sends the same bytes either way.
}

// alignmentTest implements DECALN, which fills the screen with capital E. It
// exists so that a technician could check the screen alignment; today it is how
// terminal test suites check that a resize really changed the grid.
func (s *Screen) alignmentTest() {
	for i := range s.cells {
		s.cells[i] = Cell{Rune: 'E'}
	}
	_ = s.rowIndex // every row is identical, so the arrangement does not matter
	s.cur = Cursor{}
	s.pendingWrap = false
}

func (s *Screen) saveCursor() {
	s.saved = savedCursor{cur: s.cur, pen: s.pen, pendingWrap: s.pendingWrap, valid: true}
}

// restoreCursor puts back what was saved. With nothing saved, the terminal
// homes the cursor and resets the pen.
func (s *Screen) restoreCursor() {
	if !s.saved.valid {
		s.cur = Cursor{}
		s.pen = Attr{}
		s.pendingWrap = false
		return
	}
	s.cur.Row = clamp(s.saved.cur.Row, 0, s.rows-1)
	s.cur.Col = clamp(s.saved.cur.Col, 0, s.cols-1)
	s.pen = s.saved.pen
	s.pendingWrap = s.saved.pendingWrap
}

// moveTo takes the one-based row and column used by the protocol.
func (s *Screen) moveTo(row, col int) {
	s.moveToRow(row)
	s.cur.Col = clamp(col-1, 0, s.cols-1)
}

// moveToRow handles the one piece of addressing that origin mode changes: with
// DECOM set, row 1 is the top of the scroll region and the cursor cannot leave
// it. That is how a program addresses its region without knowing where on the
// screen the region sits.
func (s *Screen) moveToRow(row int) {
	if s.mode.Origin {
		s.cur.Row = clamp(s.top+row-1, s.top, s.bottom)
		return
	}
	s.cur.Row = clamp(row-1, 0, s.rows-1)
}

// moveRowBy moves the cursor vertically without scrolling. A cursor inside the
// scroll region stays inside it - CUU never carries a program's text out of its
// own region - while one outside moves within the screen.
func (s *Screen) moveRowBy(delta int) {
	lo, hi := 0, s.rows-1
	if s.cur.Row >= s.top && s.cur.Row <= s.bottom {
		lo, hi = s.top, s.bottom
	}
	s.cur.Row = clamp(s.cur.Row+delta, lo, hi)
}

// eraseDisplay implements CSI J. The cursor does not move, which is what
// xterm does and what programs expect.
func (s *Screen) eraseDisplay(mode int) {
	switch mode {
	case 0: // cursor to end of screen
		s.blankCells(s.cur.Row, s.cur.Col, s.cols)
		for r := s.cur.Row + 1; r < s.rows; r++ {
			s.blankRow(r)
		}
	case 1: // start of screen to cursor, inclusive
		for r := 0; r < s.cur.Row; r++ {
			s.blankRow(r)
		}
		s.blankCells(s.cur.Row, 0, s.cur.Col+1)
	case 2: // the whole screen
		s.blankAll()
	case 3: // the screen *and* the scrollback
		s.blankAll()
		s.sb.clear()
		s.offset = 0
	}
}

// Clear is Cmd+K: the history goes, and so does everything above the cursor's
// row, which moves to the top. The shell is not told - it is waiting at a
// prompt on that row, and keeping the row is what keeps the prompt, and
// anything half-typed after it, without asking the shell to draw it again.
//
// A full-screen program owns what is on screen, so for vim or less only the
// history behind it is dropped.
func (s *Screen) Clear() {
	s.sb.clear()
	s.offset = 0
	if s.altActive {
		return
	}
	s.shiftUp(0, s.rows-1, s.cur.Row, false)
	s.cur.Row = 0
	for r := 1; r < s.rows; r++ {
		s.blankRow(r)
	}
	s.pendingWrap = false
}

// eraseChars implements CSI X: blank n cells from the cursor without moving it
// and without shifting anything along.
func (s *Screen) eraseChars(n int) {
	s.blankCells(s.cur.Row, s.cur.Col, min(s.cur.Col+n, s.cols))
}

// eraseLine implements CSI K, on the cursor's row only.
func (s *Screen) eraseLine(mode int) {
	switch mode {
	case 0: // cursor to end of line
		s.blankCells(s.cur.Row, s.cur.Col, s.cols)
	case 1: // start of line to cursor, inclusive
		s.blankCells(s.cur.Row, 0, s.cur.Col+1)
	case 2: // whole line
		s.blankRow(s.cur.Row)
	}
}

// lineFeed moves down one row, scrolling the region when there is no row left.
//
// The bottom margin, not the bottom of the screen, is where scrolling happens:
// that is the whole point of a scroll region.
func (s *Screen) lineFeed() {
	if s.cur.Row == s.bottom {
		s.shiftUp(s.top, s.bottom, 1, s.archives())
		return
	}
	if s.cur.Row < s.rows-1 {
		s.cur.Row++
	}
}

// reverseLineFeed moves up one row, scrolling the other way at the top margin.
// This is how a program inserts a line above the first one.
func (s *Screen) reverseLineFeed() {
	if s.cur.Row == s.top {
		s.shiftDown(s.top, s.bottom, 1)
		return
	}
	if s.cur.Row > 0 {
		s.cur.Row--
	}
}

// fill blanks a range of cells. Erased cells take the pen's *background*, which
// is why "set a red background, then clear the screen" paints the screen red.
// They do not take bold or underline: there is nothing there to embolden.
func (s *Screen) blankCells(r, from, to int) {
	if from >= to {
		return
	}
	line := s.row(r)

	// Erasing half of a wide character orphans the other half, which sits
	// outside the range being cleared. Both checks must happen before the fill,
	// while the halves can still recognise each other.
	if from > 0 && line[from].IsContinuation() {
		blankCell(line, from-1)
	}
	if to < len(line) && line[to].IsContinuation() && line[to-1].Width() == 2 {
		blankCell(line, to)
	}

	blank := Cell{Rune: ' ', Attr: Attr{BG: s.pen.BG}}
	for i := from; i < to; i++ {
		line[i] = blank
	}

	// Erasing through the right edge ends the line there, so it no longer runs
	// on. This is what keeps the flag honest when a shell redraws a line
	// shorter than it was: "\r", the new text, then "ESC [ K".
	if to >= s.cols {
		s.setWrapped(r, false)
	}
}

// blankRow erases a whole row.
func (s *Screen) blankRow(r int) {
	blank := Cell{Rune: ' ', Attr: Attr{BG: s.pen.BG}}
	line := s.row(r)
	for i := range line {
		line[i] = blank
	}
	s.setWrapped(r, false)
}

// setWrapped records whether logical row r continues on the next one. The flag
// lives at the row's physical address so that scrolling, which only permutes
// rowIndex, moves it without anything being copied.
func (s *Screen) setWrapped(r int, wrapped bool) {
	if r >= 0 && r < len(s.rowIndex) {
		s.wrapped[s.rowIndex[r]] = wrapped
	}
}

// rowWrapped reports whether logical row r continues on the next one.
func (s *Screen) rowWrapped(r int) bool {
	if r < 0 || r >= len(s.rowIndex) {
		return false
	}
	return s.wrapped[s.rowIndex[r]]
}

// blankAll erases the screen.
func (s *Screen) blankAll() {
	for r := 0; r < s.rows; r++ {
		s.blankRow(r)
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Snapshot is a copy of the screen at one instant, for a renderer to draw.
//
// A copy, not the Screen itself, because the goroutine reading the PTY writes
// to the Screen while the UI thread draws. Copying under a lock is the smallest
// correct answer; the alternative is locking the whole Screen for the duration
// of a redraw.
type Snapshot struct {
	Rows, Cols int
	// Cells is the grid, row-major, len == Rows*Cols. This is what a renderer
	// draws.
	//
	// It may be scratch space reused between frames - see SnapshotInto - so a
	// caller that keeps a snapshot past the next one must copy it.
	Cells []Cell
	// Cursor is where the cursor sits *in the view*, and Modes carries the
	// state a renderer needs - including whether to draw the cursor at all,
	// which is false once history has pushed it off the bottom.
	Cursor Cursor
	Modes  Modes

	// ScrollOffset is how far back in history this view is, and ScrollbackLen
	// how much history exists - for a renderer that wants to show where the
	// user is. Zero offset means the live screen.
	ScrollOffset  int
	ScrollbackLen int

	// TopLine is the absolute number of the line at view row 0, so that a
	// caller holding absolute positions - a selection - can work out where on
	// this particular view they fall. See lines.go.
	TopLine int64

	// CursorStyle is the shape the program asked for, and Title what it last
	// set the window title to.
	CursorStyle CursorStyle
	Title       string
}

// CellAt returns the cell at row, col, or a blank one if that is off-grid.
func (s Snapshot) CellAt(row, col int) Cell {
	if row < 0 || row >= s.Rows || col < 0 || col >= s.Cols {
		return Blank
	}
	return s.Cells[row*s.Cols+col]
}

// Line returns row of the snapshot as plain text, padded to the full width.
// Nothing in the draw path needs it; tests and dumps do.
func (s Snapshot) Line(row int) string {
	if row < 0 || row >= s.Rows {
		return ""
	}
	var b strings.Builder
	b.Grow(s.Cols)
	for col := 0; col < s.Cols; col++ {
		c := s.Cells[row*s.Cols+col]
		if c.IsContinuation() {
			continue
		}
		b.WriteRune(c.Rune)
		b.WriteString(c.Comb)
	}
	return b.String()
}

// Lines is the whole snapshot as plain text.
func (s Snapshot) Lines() []string {
	out := make([]string, s.Rows)
	for row := range out {
		out[row] = s.Line(row)
	}
	return out
}

// Snapshot copies the current contents and cursor position.
func (s *Screen) Snapshot() Snapshot { return s.SnapshotInto(nil) }

// SnapshotInto is Snapshot with somewhere to put the grid.
//
// A renderer draws the same shape of grid every frame, so handing back its
// previous buffer saves copying the screen into fresh memory 60 times a second.
// The buffer is overwritten, which is why Snapshot.Cells is documented as
// borrowed: the frame being drawn owns it, and only until the next one.
func (s *Screen) SnapshotInto(buf []Cell) Snapshot {
	cells := s.viewInto(buf)

	// Scrolling back pushes the live grid down the view, taking the cursor with
	// it. Once it is off the bottom there is nothing to draw.
	modes := s.mode
	cursor := Cursor{Row: s.cur.Row + s.offset, Col: s.cur.Col}
	if cursor.Row >= s.rows {
		modes.CursorVisible = false
		cursor.Row = s.rows - 1
	}

	return Snapshot{
		Rows:          s.rows,
		Cols:          s.cols,
		Cells:         cells,
		Cursor:        cursor,
		Modes:         modes,
		ScrollOffset:  s.offset,
		ScrollbackLen: s.sb.Len(),
		TopLine:       s.TopLine(),
		CursorStyle:   s.cursorStyle,
		Title:         s.title,
	}
}
