package terminal

import (
	"fmt"
	"unicode"

	"github.com/thakurankit7/shellter/internal/render"
	"github.com/thakurankit7/shellter/internal/screen"
)

// Finding text in a pane: everything it still holds, history and screen.
//
// A match is shown by making it the selection. That is one fewer thing to
// draw, and it means the obvious next step - Cmd+C - copies what was found.
//
// Matches are found again on every step rather than remembered. Output keeps
// arriving while the search bar is open, and a list built a moment ago names
// lines that may since have scrolled into history or out of it altogether;
// searching a thousand lines is quick enough to be done per keystroke.

// match is one place the query was found, as a selection would describe it:
// from the first cell to one past the last.
type match struct {
	from, to screen.Pos
}

// findDirection is which way Find Next and Find Previous move.
type findDirection int

const (
	findFresh findDirection = 0  // the query changed: start again at the newest
	findOlder findDirection = 1  // Return, Cmd+G: up, towards history
	findNewer findDirection = -1 // Shift+Return, Cmd+Shift+G: down
)

// find looks for query in the focused pane and selects one match: the newest
// on a fresh search, otherwise the next one in the direction asked, wrapping
// round at either end. It tells the search bar how it went.
func (g *GUI) find(query string, dir findDirection) {
	p := g.current()
	if p == nil {
		return
	}
	if query == "" {
		p.sel.clear()
		render.SetFindStatus("")
		render.Redraw()
		return
	}

	p.mu.Lock()
	matches := findAll(p.scr, query)
	p.mu.Unlock()

	if len(matches) == 0 {
		p.sel.clear()
		render.SetFindStatus("no matches")
		render.Redraw()
		return
	}

	i := len(matches) - 1 // newest first: the bottom of the pane is where you are
	if dir != findFresh && p.sel.active {
		i = step(matches, p.sel.anchor, dir)
	}
	m := matches[i]
	p.sel = selection{origin: m.from, anchor: m.from, head: m.to, active: true}
	g.reveal(p, m.from.Line)
	render.SetFindStatus(fmt.Sprintf("%d of %d", i+1, len(matches)))
	render.Redraw()
}

// step finds the match after the one starting at from, going older or newer,
// wrapping round. from need not be a match itself any more - output may have
// moved things - so this looks for the nearest one past it.
func step(matches []match, from screen.Pos, dir findDirection) int {
	if dir == findOlder {
		for i := len(matches) - 1; i >= 0; i-- {
			if matches[i].from.Before(from) {
				return i
			}
		}
		return len(matches) - 1
	}
	for i, m := range matches {
		if from.Before(m.from) {
			return i
		}
	}
	return 0
}

// findAll lists every match of query in a screen's history and grid, oldest
// first. Case is ignored. A match does not cross from one row to the next: a
// word broken by the right edge is not found, which is the price of searching
// rows rather than rebuilding logical lines on every keystroke.
func findAll(scr *screen.Screen, query string) []match {
	q := fold([]rune(query))
	var out []match
	var runes []rune
	var cols []int
	_, width := scr.Size()
	for n := scr.FirstLine(); n < scr.TotalLines(); n++ {
		runes, cols = scr.LineChars(n, runes, cols)
		line := fold(runes)
		for i := 0; i+len(q) <= len(line); i++ {
			if !equalRunes(line[i:i+len(q)], q) {
				continue
			}
			last := i + len(q) - 1
			end := width
			if last+1 < len(cols) {
				end = cols[last+1]
			} else if w := runeWidth(runes[last]); cols[last]+w <= width {
				end = cols[last] + w
			}
			out = append(out, match{
				from: screen.Pos{Line: n, Col: cols[i]},
				to:   screen.Pos{Line: n, Col: end},
			})
		}
	}
	return out
}

// reveal scrolls a pane so that line n is on screen, as near the middle as it
// can be, and leaves it alone if it is already showing.
func (g *GUI) reveal(p *pane, n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	rows, _ := p.scr.Size()
	top := p.scr.TopLine()
	if n >= top && n < top+int64(rows) {
		return
	}
	want := n - int64(rows/2)
	p.scr.ScrollBy(int(top - want))
}

func fold(runes []rune) []rune {
	out := make([]rune, len(runes))
	for i, r := range runes {
		out[i] = unicode.ToLower(r)
	}
	return out
}

func equalRunes(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// runeWidth is a character's width in cells, for the end of a match that runs
// to the end of its row.
func runeWidth(r rune) int {
	if (screen.Cell{Rune: r}).Width() == 2 {
		return 2
	}
	return 1
}
