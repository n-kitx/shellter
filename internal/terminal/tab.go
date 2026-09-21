package terminal

// A tab is one or more panes sharing a rectangle of the window.
//
// The panes form a binary tree. A leaf is a pane; a branch is a split, which
// says whether its two children sit side by side or one above the other and
// how the space between them is divided. A tab with no splits is a tree of one
// leaf, which is why splitting changed nothing about how a pane works.
//
//	one pane            after Cmd+D           after Cmd+Shift+D on the right
//	+-----------+       +-----+-----+         +-----+-----+
//	|           |       |     |     |         |     |  B  |
//	|     A     |       |  A  |  B  |         |  A  +-----+
//	|           |       |     |     |         |     |  C  |
//	+-----------+       +-----+-----+         +-----+-----+
//
//	   leaf A            beside              beside
//	                    /      \            /      \
//	                 leaf A  leaf B      leaf A   above
//	                                             /     \
//	                                          leaf B  leaf C
type tab struct {
	root *node
	// focus is the pane keystrokes go to. It is always a leaf of root.
	focus *pane

	// activity says something has been printed in this tab since it was last
	// looked at. It is the GUI's business - only the GUI knows which tab is in
	// front - so the GUI's lock guards it.
	activity bool

	// shown is the focused pane's title as the bar last heard it. A shell
	// retitles itself at every prompt, so the bar is only told when the answer
	// actually changed. Guarded by the GUI's lock, like activity.
	shown string
}

// A node is either a leaf holding one pane, or a split holding two children.
type node struct {
	pane *pane // set on a leaf, nil on a split

	dir splitDir
	// ratio is how much of the space the first child gets, from 0 to 1.
	ratio float64
	a, b  *node
}

type splitDir int

const (
	// splitBeside puts the two children left and right, divided by a column.
	splitBeside splitDir = iota
	// splitAbove puts them one over the other, divided by a row.
	splitAbove
)

// dividerCells is how much room a split takes out of the middle. One cell:
// enough to draw a line in, and it keeps the arithmetic in whole cells, which
// is the only unit a terminal grid has.
const dividerCells = 1

// minPaneCols and minPaneRows are the smallest a pane may be squeezed to.
// A shell in a two-column pane is useless, but a pane that rounds down to zero
// columns is worse: programs divide by it.
const (
	minPaneCols = 8
	minPaneRows = 2
)

func newTab(p *pane) *tab {
	return &tab{root: &node{pane: p}, focus: p}
}

// rect is a rectangle of character cells inside the window's grid.
type rect struct {
	Row, Col, Rows, Cols int
}

func (r rect) contains(row, col int) bool {
	return row >= r.Row && row < r.Row+r.Rows && col >= r.Col && col < r.Col+r.Cols
}

// placed is a pane and where it currently sits.
type placed struct {
	pane *pane
	rect rect
}

// layout works out where every pane goes, and where the lines between them are.
//
// It is computed on demand rather than stored, because a stored rectangle is a
// second copy of the truth: it would have to be updated on every split, close
// and resize, and the day one of those forgot, a pane would be drawn somewhere
// it no longer is. The tree is small, so recomputing costs nothing.
//
// The two slices are appended to and returned, so a caller that runs every
// frame can hand back the ones it got last time and allocate nothing. Pass nil
// from anywhere that does not care.
func (t *tab) layout(r rect, panes []placed, lines []divider) ([]placed, []divider) {
	panes, lines = panes[:0], lines[:0]
	t.root.place(r, &panes, &lines)
	return panes, lines
}

// divider is a line between two panes, one cell thick.
type divider struct {
	rect     rect
	vertical bool
}

func (n *node) place(r rect, panes *[]placed, lines *[]divider) {
	if n.pane != nil {
		*panes = append(*panes, placed{pane: n.pane, rect: r})
		return
	}

	if n.dir == splitBeside {
		first := share(r.Cols, n.ratio, minPaneCols)
		rest := r.Cols - first - dividerCells
		n.a.place(rect{r.Row, r.Col, r.Rows, first}, panes, lines)
		*lines = append(*lines, divider{
			rect:     rect{r.Row, r.Col + first, r.Rows, dividerCells},
			vertical: true,
		})
		n.b.place(rect{r.Row, r.Col + first + dividerCells, r.Rows, rest}, panes, lines)
		return
	}

	first := share(r.Rows, n.ratio, minPaneRows)
	rest := r.Rows - first - dividerCells
	n.a.place(rect{r.Row, r.Col, first, r.Cols}, panes, lines)
	*lines = append(*lines, divider{
		rect: rect{r.Row + first, r.Col, dividerCells, r.Cols},
	})
	n.b.place(rect{r.Row + first + dividerCells, r.Col, rest, r.Cols}, panes, lines)
}

// share divides total between two panes and a divider, giving the first the
// asked-for fraction and keeping both sides at least min cells wide.
//
// When there is not enough room for that - a very small window, or many splits
// - the two sides are simply halved. Something has to give, and an even split
// is the least surprising thing to give.
func share(total int, ratio float64, min int) int {
	usable := total - dividerCells
	first := int(float64(usable)*ratio + 0.5)
	if usable < 2*min {
		return usable / 2
	}
	if first < min {
		first = min
	}
	if first > usable-min {
		first = usable - min
	}
	return first
}

// panes lists the tab's panes in layout order: left to right, top to bottom.
func (t *tab) panes() []*pane {
	var out []*pane
	t.root.walk(func(n *node) {
		if n.pane != nil {
			out = append(out, n.pane)
		}
	})
	return out
}

func (n *node) walk(fn func(*node)) {
	if n == nil {
		return
	}
	fn(n)
	n.a.walk(fn)
	n.b.walk(fn)
}

// split replaces the focused pane with a branch holding it and next, and moves
// the focus to the new pane - which is where a person's attention already is.
func (t *tab) split(next *pane, dir splitDir) {
	leaf := t.leafOf(t.focus)
	if leaf == nil {
		return
	}
	// The leaf becomes the branch, in place, so whatever pointed at it in the
	// tree still does. Its pane moves down into a new leaf of its own.
	*leaf = node{
		dir:   dir,
		ratio: 0.5,
		a:     &node{pane: leaf.pane},
		b:     &node{pane: next},
	}
	t.focus = next
}

// remove takes a pane out of the tree, collapsing the split it was half of.
// It reports whether the tab still has any panes left.
func (t *tab) remove(p *pane) bool {
	if t.root == nil {
		return false
	}
	if t.root.pane == p {
		t.root = nil
		t.focus = nil
		return false
	}
	parent, sibling := t.parentOf(p)
	if parent == nil {
		return t.root != nil
	}
	// The surviving sibling takes the parent's place: a split with one pane in
	// it is not a split any more.
	*parent = *sibling
	if t.focus == p {
		t.focus = firstPane(parent)
	}
	return true
}

// leafOf finds the node holding a pane.
func (t *tab) leafOf(p *pane) *node {
	var found *node
	t.root.walk(func(n *node) {
		if n.pane == p {
			found = n
		}
	})
	return found
}

// parentOf finds the split a pane is half of, and the other half.
func (t *tab) parentOf(p *pane) (parent, sibling *node) {
	t.root.walk(func(n *node) {
		switch {
		case n.pane != nil:
		case n.a.pane == p:
			parent, sibling = n, n.b
		case n.b.pane == p:
			parent, sibling = n, n.a
		}
	})
	return parent, sibling
}

func firstPane(n *node) *pane {
	if n == nil {
		return nil
	}
	if n.pane != nil {
		return n.pane
	}
	if p := firstPane(n.a); p != nil {
		return p
	}
	return firstPane(n.b)
}

// sole is the tab's only pane, or nil if it has been split. Tests use it; the
// GUI works in terms of the focused pane instead.
func (t *tab) sole() *pane {
	if t.root != nil && t.root.pane != nil {
		return t.root.pane
	}
	return nil
}

// paneAt finds the pane covering a cell, for a click.
func (t *tab) paneAt(r rect, row, col int) *pane {
	panes, _ := t.layout(r, nil, nil)
	for _, p := range panes {
		if p.rect.contains(row, col) {
			return p.pane
		}
	}
	return nil
}

// direction is which way Cmd+Option+arrow moves the focus.
type direction int

const (
	dirLeft direction = iota
	dirRight
	dirUp
	dirDown
)

// neighbour is the pane to move the focus to, or nil if there is none that way.
//
// Only panes entirely beyond the moving edge count, and only those that
// actually line up with the one being left - the overlap test is what stops
// Cmd+Option+Right jumping to a pane in a different row of the window. Of
// those, the nearest wins, and a tie goes to whichever is closest to where the
// eye already is.
func (t *tab) neighbour(r rect, from *pane, d direction) *pane {
	panes, _ := t.layout(r, nil, nil)
	var here rect
	found := false
	for _, p := range panes {
		if p.pane == from {
			here, found = p.rect, true
		}
	}
	if !found {
		return nil
	}

	var best *pane
	bestDist, bestOffset := 0, 0
	for _, p := range panes {
		if p.pane == from {
			continue
		}
		dist, offset, ok := gap(here, p.rect, d)
		if !ok {
			continue
		}
		if best == nil || dist < bestDist || (dist == bestDist && offset < bestOffset) {
			best, bestDist, bestOffset = p.pane, dist, offset
		}
	}
	return best
}

// gap measures how far another rectangle is in direction d, and how far its
// middle is from this one's across the direction of travel. ok is false when
// the other rectangle is not that way at all, or does not line up with this
// one.
func gap(here, other rect, d direction) (dist, offset int, ok bool) {
	switch d {
	case dirLeft:
		dist = here.Col - (other.Col + other.Cols)
		ok = dist >= 0 && overlaps(here.Row, here.Rows, other.Row, other.Rows)
	case dirRight:
		dist = other.Col - (here.Col + here.Cols)
		ok = dist >= 0 && overlaps(here.Row, here.Rows, other.Row, other.Rows)
	case dirUp:
		dist = here.Row - (other.Row + other.Rows)
		ok = dist >= 0 && overlaps(here.Col, here.Cols, other.Col, other.Cols)
	case dirDown:
		dist = other.Row - (here.Row + here.Rows)
		ok = dist >= 0 && overlaps(here.Col, here.Cols, other.Col, other.Cols)
	}
	if !ok {
		return 0, 0, false
	}

	if d == dirLeft || d == dirRight {
		offset = abs(middle(here.Row, here.Rows) - middle(other.Row, other.Rows))
	} else {
		offset = abs(middle(here.Col, here.Cols) - middle(other.Col, other.Cols))
	}
	return dist, offset, true
}

// overlaps reports whether two spans share any cell.
func overlaps(startA, lenA, startB, lenB int) bool {
	return startA < startB+lenB && startB < startA+lenA
}

func middle(start, length int) int { return start + length/2 }
