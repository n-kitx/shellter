package terminal

import (
	"unicode/utf8"

	"github.com/thakurankit7/shellter/internal/input"
	"github.com/thakurankit7/shellter/internal/render"
	"github.com/thakurankit7/shellter/internal/screen"
)

// Selecting with the mouse, and the clipboard.
//
// All of this runs on the UI thread - mouse events, menu items and drawing all
// arrive there - so a pane's selection needs no lock of its own. Reading the
// text behind it does, because that touches the screen buffer the PTY pump is
// writing to.

// selectable reports whether a mouse event should mark text rather than be
// reported to the program.
//
// Holding Shift takes the mouse back from a program that asked for it, which is
// the only way to copy anything out of vim or less. Without that rule a
// full-screen program would own the pointer completely.
func selectable(tracking int, m render.Mouse) bool {
	return tracking == input.TrackOff || m.Mods.Has(input.ModShift)
}

// selectWithMouse turns a press, a drag and a release into a selection.
func (g *GUI) selectWithMouse(p *pane, at render.Mouse, m render.Mouse, rows, cols int) {
	if m.Button != input.MouseLeft {
		return
	}

	switch m.Action {
	case input.MousePress:
		g.dragging = p
		// Option+drag selects a rectangle, as in Terminal.app and iTerm.
		p.sel.begin(p.posAt(at.Row, at.Col, rows, cols), m.Clicks, m.Mods.Has(input.ModOption))
		p.snapSelection(cols)
	case input.MouseMove:
		if g.dragging != p {
			return
		}
		p.edgeScroll(at.Row, rows)
		p.sel.extend(p.posAt(at.Row, at.Col, rows, cols))
		p.snapSelection(cols)
	case input.MouseRelease:
		if g.dragging != p {
			return
		}
		g.dragging = nil
		if g.CopyOnSelect && !p.sel.empty() {
			p.mu.Lock()
			text := p.sel.text(p.scr)
			p.mu.Unlock()
			render.SetClipboard(text)
		}
	default:
		return
	}
	render.Redraw()
}

// edgeScroll scrolls the view when a selection is dragged past the top or the
// bottom of the pane, so that it can go on to take in what is out of sight.
// The further past the edge, the faster: a line per report just outside it,
// up to maxEdgeScroll further out. The window repeats a drag held still out
// there twenty times a second, so the scrolling carries on without the mouse
// moving.
func (p *pane) edgeScroll(row, rows int) {
	var lines int
	switch {
	case row < 0:
		lines = clamp(-row, 1, maxEdgeScroll)
	case row >= rows:
		lines = -clamp(row-rows+1, 1, maxEdgeScroll)
	default:
		return
	}
	p.mu.Lock()
	p.scr.ScrollBy(lines)
	p.mu.Unlock()
}

// maxEdgeScroll is the most lines one drag report scrolls.
const maxEdgeScroll = 3

// posAt turns a place in the window into a place in the session's output.
//
// The column is allowed to reach cols, one past the last cell: that is how a
// selection says "to the end of this line" rather than "up to its last
// character".
func (p *pane) posAt(row, col, rows, cols int) screen.Pos {
	p.mu.Lock()
	top := p.scr.TopLine()
	p.mu.Unlock()
	return screen.Pos{
		Line: top + int64(clamp(row, 0, rows-1)),
		Col:  clamp(col, 0, cols),
	}
}

// snapSelection grows a word or line selection out to its edges. It is a no-op
// for an ordinary drag, which is most of them.
func (p *pane) snapSelection(cols int) {
	if p.sel.mode == selectChar {
		return
	}
	if cap(p.words) < cols {
		p.words = make([]rune, cols)
	}
	p.mu.Lock()
	p.sel.snap(p.scr, p.words[:cols])
	p.mu.Unlock()
}

// clearSelection drops the highlight and asks for a repaint, if there was one.
// Typing replaces what you were looking at, so leaving a selection marked over
// text that has moved on would only ever be wrong.
func (g *GUI) clearSelection(p *pane) {
	if p == nil || !p.sel.active {
		return
	}
	p.sel.clear()
	render.Redraw()
}

// ---------------------------------------------------------------------------
// The menu
// ---------------------------------------------------------------------------

// copySelection puts the focused pane's selection on the clipboard. With
// nothing selected it does nothing, rather than emptying the clipboard.
func (g *GUI) copySelection() {
	p := g.current()
	if p == nil {
		return
	}
	p.mu.Lock()
	text := p.sel.text(p.scr)
	p.mu.Unlock()

	render.SetClipboard(text)
}

// selectAll marks everything this pane still holds: all of its history and all
// of the live grid.
func (g *GUI) selectAll() {
	p := g.current()
	if p == nil {
		return
	}
	p.mu.Lock()
	first, last := p.scr.FirstLine(), p.scr.TotalLines()
	_, cols := p.scr.Size()
	p.mu.Unlock()

	p.sel = selection{
		origin: screen.Pos{Line: first},
		anchor: screen.Pos{Line: first},
		head:   screen.Pos{Line: last - 1, Col: cols},
		active: true,
	}
	render.Redraw()
}

// paste sends the clipboard to the focused pane's shell.
func (g *GUI) paste() {
	p := g.current()
	if p == nil {
		return
	}
	text := render.Clipboard()
	if text == "" {
		return
	}

	p.mu.Lock()
	bracketed := p.scr.Modes().BracketedPaste
	p.mu.Unlock()

	g.writeTo(p, pasteBytes(text, bracketed))
}

// Bracketed paste (?2004) wraps the text in markers so that the program can
// tell a paste from typing. It is how an editor knows not to auto-indent what
// arrives, and how a shell knows not to run a multi-line paste line by line.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// pasteBytes turns clipboard text into what the shell should receive.
//
// Two rules, and the second one matters more than it looks:
//
//   - Newlines become carriage returns. Return is what a terminal sends when
//     you press it, and a paste should arrive as though you had.
//   - Control characters are dropped, tab and return aside. Pasted text is
//     text. If it were passed through as-is, an ESC in something copied off a
//     web page would be read as an escape sequence and could move the cursor,
//     change colours, or - inside a shell that has not asked for bracketed
//     paste - end the line and start running it.
func pasteBytes(text string, bracketed bool) []byte {
	out := make([]byte, 0, len(text)+len(pasteStart)+len(pasteEnd))
	if bracketed {
		out = append(out, pasteStart...)
	}

	lastWasReturn := false
	for _, r := range text {
		switch {
		case r == '\r' || r == '\n':
			if r == '\n' && lastWasReturn {
				continue // "\r\n" is one Return, not two
			}
			out = append(out, '\r')
			lastWasReturn = true
			continue
		case r == '\t':
			out = append(out, '\t')
		case r < 0x20 || r == 0x7f:
			// dropped: see above
		default:
			out = utf8Append(out, r)
		}
		lastWasReturn = false
	}

	if bracketed {
		out = append(out, pasteEnd...)
	}
	return out
}

// utf8Append writes one rune, keeping the allocation behaviour of append.
func utf8Append(dst []byte, r rune) []byte {
	var buf [4]byte
	return append(dst, buf[:utf8.EncodeRune(buf[:], r)]...)
}

// contextClick reports whether a mouse event asks for the context menu: a
// right click, or a Control-click, which is the same thing on a Mac with one
// button.
func contextClick(m render.Mouse) bool {
	if m.Action != input.MousePress {
		return false
	}
	return m.Button == input.MouseRight ||
		(m.Button == input.MouseLeft && m.Mods.Has(input.ModControl))
}

// contextMenu pops up the menu over pane p. The click has already focused p,
// so every item acts on it.
func (g *GUI) contextMenu(p *pane) {
	g.hovered = linkHover{}
	render.SetLinkHover(false, false)
	render.ContextMenu(!p.sel.empty())
}
