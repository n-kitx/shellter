package screen

// Scrollback holds the lines that have scrolled off the top of the screen.
//
// It is deliberately separate from the grid. The grid is *live state* - the
// thing escape sequences move a cursor around and paint into. Scrollback is
// *history*: finished lines that nothing can edit any more. Keeping them apart
// is what makes it safe to scroll up and look at the past while the shell keeps
// writing to the present.
//
// Storage is a ring buffer, so discarding the oldest line when full costs
// nothing and memory stays bounded however long the session runs.
type scrollback struct {
	lines [][]Cell
	// wrapped[i] says line i ran off its right edge and continues on the next
	// one, rather than ending there. Copying text needs it: a wrapped line and
	// its continuation are one line as far as the person who typed it is
	// concerned, and pasting a URL broken in half is not a small annoyance.
	wrapped []bool
	start   int // index of the oldest line
	count   int
	// dropped counts the lines evicted since the session began, so that a line
	// can be named by a number that never changes. Without it the oldest line
	// is always 0 and every line renumbers each time the ring evicts one, which
	// would quietly slide a selection up the screen as output arrived.
	dropped int64
}

func newScrollback(capacity int) *scrollback {
	if capacity < 0 {
		capacity = 0
	}
	return &scrollback{
		lines:   make([][]Cell, capacity),
		wrapped: make([]bool, capacity),
	}
}

// Dropped is how many lines have been evicted since the session began.
func (sb *scrollback) Dropped() int64 { return sb.dropped }

// wrappedAt reports whether history line i continues on the next one.
func (sb *scrollback) wrappedAt(i int) bool {
	if i < 0 || i >= sb.count {
		return false
	}
	return sb.wrapped[(sb.start+i)%len(sb.lines)]
}

// Cap is how many lines can be kept. Zero disables scrollback entirely.
func (sb *scrollback) Cap() int { return len(sb.lines) }

// Len is how many lines are currently held.
func (sb *scrollback) Len() int { return sb.count }

// push stores a copy of a line, dropping the oldest if that makes room.
//
// Trailing blanks are not stored: terminal lines are mostly empty on the right,
// and Line pads them back out on the way in.
func (sb *scrollback) push(line []Cell, wrapped bool) {
	if len(sb.lines) == 0 {
		sb.dropped++ // no room to keep it, but it still happened
		return
	}

	end := len(line)
	for end > 0 && isPlainBlank(line[end-1]) {
		end--
	}

	slot := (sb.start + sb.count) % len(sb.lines)
	full := sb.count == len(sb.lines)
	if full {
		slot = sb.start
	}

	// Once the ring is full every push evicts a line, and the memory that line
	// was using is free the moment it is evicted. Reusing it makes a long
	// session's steady state allocation-free, which is what it should be: the
	// whole point of a ring buffer is that it stops growing.
	stored := sb.lines[slot]
	if cap(stored) >= end {
		stored = stored[:end]
	} else {
		stored = make([]Cell, end)
	}
	copy(stored, line[:end])
	sb.lines[slot] = stored
	sb.wrapped[slot] = wrapped

	if full {
		sb.start = (sb.start + 1) % len(sb.lines)
		sb.dropped++
		return
	}
	sb.count++
}

// line returns history line i - 0 is the oldest - padded to cols cells.
func (sb *scrollback) line(i, cols int) []Cell {
	return sb.lineInto(make([]Cell, cols), i)
}

// lineInto writes history line i into dst, padding it out with blanks. The
// caller owns dst, which is how a snapshot fills a whole view without a slice
// per row.
func (sb *scrollback) lineInto(dst []Cell, i int) []Cell {
	n := 0
	if i >= 0 && i < sb.count {
		n = copy(dst, sb.lines[(sb.start+i)%len(sb.lines)])
	}
	for j := n; j < len(dst); j++ {
		dst[j] = Blank
	}
	return dst
}

// reset empties the ring but keeps the memory each slot holds, for a reflow
// that is about to refill it with the same lines at a new width. Unlike clear
// it does not count the lines as dropped: they are coming straight back.
func (sb *scrollback) reset() {
	for i := range sb.wrapped {
		sb.wrapped[i] = false
	}
	sb.start, sb.count = 0, 0
}

// clear forgets all history.
func (sb *scrollback) clear() {
	for i := range sb.lines {
		sb.lines[i] = nil
		sb.wrapped[i] = false
	}
	// dropped is deliberately not reset: line numbers name a place in the
	// session's output, and a clear discards the lines, not the history of
	// there having been any.
	sb.dropped += int64(sb.count)
	sb.start, sb.count = 0, 0
}

// resize changes the capacity, keeping the most recent lines.
func (sb *scrollback) resize(capacity int) {
	if capacity < 0 {
		capacity = 0
	}
	if capacity == len(sb.lines) {
		return
	}

	keep := sb.count
	if keep > capacity {
		keep = capacity
	}
	next := make([][]Cell, capacity)
	nextWrapped := make([]bool, capacity)
	for i := 0; i < keep; i++ {
		at := (sb.start + sb.count - keep + i) % len(sb.lines)
		next[i] = sb.lines[at]
		nextWrapped[i] = sb.wrapped[at]
	}
	sb.dropped += int64(sb.count - keep)
	sb.lines, sb.wrapped, sb.start, sb.count = next, nextWrapped, 0, keep
}

// isPlainBlank reports whether a cell is an unstyled space, i.e. one that can be
// dropped from the end of a stored line and rebuilt later.
func isPlainBlank(c Cell) bool {
	return c.Rune == ' ' && c.Comb == "" && c.Attr == (Attr{})
}
