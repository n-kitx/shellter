package render

import (
	"strings"

	"github.com/thakurankit7/shellter/internal/text"
)

// textWidth is package text's width function, named locally so the renderer's
// dependency on Unicode tables stays visible in one line.
var textWidth = text.Width

// Layout is what the window should display: the panes of the tab in front,
// each with its own grid, and the lines between them.
//
// The window is one grid of cells, and a pane owns a rectangle of it. Keeping
// a single coordinate system means the renderer never has to know that panes
// exist beyond offsetting what it draws - the arithmetic of who owns which
// rectangle belongs to whoever arranged them.
//
// Every slice in here, Cells included, is BORROWED: it is valid until the next
// call to Draw, which is free to write over it. Drawing reads it and is done
// with it, which is the whole reason a redraw allocates nothing.
type Layout struct {
	Rows, Cols int
	// DefaultBG is painted behind everything, panes included.
	DefaultBG uint32
	// Panes are drawn in order. One pane, with a rectangle covering the whole
	// grid, is the ordinary unsplit case.
	Panes []Pane
	// Dividers are the one-cell gaps the layout left between panes.
	Dividers []Rect
}

// Pane is one terminal inside the window.
type Pane struct {
	// Rect is where it sits, in cells. Its size matches Frame's.
	Rect Rect
	// Frame is what it is showing.
	Frame Frame
	// Focused says whether keystrokes go here. Only the focused pane draws a
	// cursor, and the others are dimmed, which is how a person can tell.
	Focused bool
	// Flash washes the pane in its foreground colour: the visual bell.
	Flash      bool
	FlashColor uint32
	// Scroll is where the view is in the pane's history, for the scroll
	// indicator; its zero value draws none.
	Scroll Scroll
}

// Scroll places a pane's view in its history, in lines: Above is how many
// lines of it are above the view, Visible how many rows the view shows, and
// Total how many lines there are altogether, history and screen.
type Scroll struct {
	Above, Visible, Total int
	// Color is what the indicator is drawn in, at part opacity.
	Color uint32
	// Shown says the indicator should be drawn. When it is not, the pane
	// still has history, and a pointer over its right edge brings the
	// indicator up to be dragged, as a Mac overlay scroller does.
	Shown bool
}

// Rect is a rectangle of character cells: row 0, column 0 is the top left of
// the window's grid.
type Rect struct {
	Row, Col, Rows, Cols int
}

// Frame is what one pane should display: a grid of cells whose colours are
// already resolved to RGB.
//
// Resolved, because this package draws pixels and nothing else. Deciding that
// "colour 1" is a particular red, that inverse video means swapping the two
// colours, or that faint means a dimmer foreground - all of that happens
// before a Frame is built, which is why the renderer needs no palette, no
// theme, and no knowledge of the screen buffer.
type Frame struct {
	Rows, Cols int
	Cells      []Cell // row-major, len == Rows*Cols

	CursorRow, CursorCol int
	CursorVisible        bool
	// CursorWidth is 2 when the cursor sits on a wide character, so the block
	// covers the whole glyph.
	CursorWidth int
	// CursorStyle is the shape to draw: a block, an underline or a bar. A
	// program sets it to show a mode - vim switches to a bar to say "insert".
	CursorStyle CursorStyle

	// DefaultBG is painted behind everything, so runs that use it need no fill.
	DefaultBG uint32
}

// CursorStyle is the shape of the cursor. The values match what the ObjC side
// expects, so the conversion across cgo is a cast.
type CursorStyle int

const (
	CursorBlock CursorStyle = iota
	CursorUnderline
	CursorBar
)

// Cell is one character position, ready to draw.
//
// Rune 0 marks a continuation cell: the right-hand half of a wide character.
// Nothing is drawn there, because the glyph in the cell before it covers both
// columns - but its background still belongs to the span.
// As in package screen, the string goes first so the struct packs into 32 bytes
// rather than 40: a frame is a few thousand of these, rebuilt per redraw.
type Cell struct {
	Comb   string // combining marks attached to Rune, usually empty
	Rune   rune
	FG, BG uint32 // 0xRRGGBB
	UL     uint32 // the underline's colour, when FlagUnderlineColor is set
	Flags  Flags
}

// IsContinuation reports whether this is the second half of a wide character.
func (c Cell) IsContinuation() bool { return c.Rune == 0 }

// Wide reports whether this cell's character occupies two columns.
func (c Cell) Wide() bool { return textWidth(c.Rune) == 2 }

// Text is what to draw in this cell.
func (c Cell) Text() string {
	if c.IsContinuation() {
		return ""
	}
	return string(c.Rune) + c.Comb
}

// Flags are the styles the renderer draws. Hidden is resolved away earlier, as
// "foreground equals background". Blink is kept so that the application can
// find blinking text and hide it on the off beat, the same way; the window
// never looks at it.
type Flags uint16

const (
	FlagBold Flags = 1 << iota
	FlagItalic
	FlagUnderline
	FlagStrike
	FlagBlink
	// The underline's shape, with FlagUnderline; none of them is a straight
	// single line.
	FlagUnderlineDouble
	FlagUnderlineCurly
	FlagUnderlineDotted
	FlagUnderlineDashed
	// FlagUnderlineColor says Cell.UL is the underline's colour; without it
	// the underline is drawn in the text's.
	FlagUnderlineColor
)

// Has reports whether every flag in f is set.
func (fl Flags) Has(f Flags) bool { return fl&f == f }

// CellAt returns the cell at row, col.
func (f Frame) CellAt(row, col int) Cell {
	if row < 0 || row >= f.Rows || col < 0 || col >= f.Cols {
		return Cell{}
	}
	return f.Cells[row*f.Cols+col]
}

// Span is a horizontal stretch of cells on one row that share an appearance, so
// they can be filled and drawn in one go.
//
// Batching matters: a shell line is usually one or two spans, so an 80x24 screen
// costs a few dozen draw calls instead of nearly two thousand.
type Span struct {
	Row, Col int
	Width    int // in cells, which is not len(Text) once wide characters appear
	Text     string
	FG, BG   uint32
	UL       uint32
	Flags    Flags
}

// spans splits a frame into the stretches to draw, row by row, left to right.
//
// A wide character always becomes a span of its own. Grouping it with its
// neighbours would mean trusting the font's advance for the substituted CJK or
// emoji glyph to be exactly two cells wide, and it usually is not: the rest of
// the line would drift. One span per wide character costs a few more draw calls
// and keeps every column where it belongs.
func spans(f Frame) []Span {
	if f.Rows == 0 || f.Cols == 0 || len(f.Cells) < f.Rows*f.Cols {
		return nil
	}
	out := make([]Span, 0, f.Rows*2)
	var b strings.Builder

	for row := 0; row < f.Rows; row++ {
		// The row as a slice: the inner loops run per cell, and going through
		// CellAt's bounds checks for each one showed up in the profile.
		line := f.Cells[row*f.Cols : (row+1)*f.Cols]

		col := 0
		for col < f.Cols {
			first := line[col]

			if first.Wide() {
				width := 1
				if col+1 < f.Cols && line[col+1].IsContinuation() {
					width = 2
				}
				out = append(out, Span{
					Row: row, Col: col, Width: width, Text: first.Text(),
					FG: first.FG, BG: first.BG, UL: first.UL, Flags: first.Flags,
				})
				col += width
				continue
			}

			// A run of ordinary cells sharing one appearance.
			b.Reset()
			b.Grow(f.Cols - col)
			end := col
			// Legitimate continuation cells are consumed above, with their
			// character. Anything still holding rune 0 here has no character to
			// belong to, so it draws as a blank.
			for end < f.Cols {
				c := line[end]
				if c.Wide() || !sameStyle(c, first) {
					break
				}
				switch {
				case c.IsContinuation():
					b.WriteByte(' ')
				default:
					// Written in two pieces rather than through Cell.Text():
					// building "rune + marks" as a string first meant a
					// concatenation per cell, which the profile put at a fifth
					// of this function.
					b.WriteRune(c.Rune)
					if c.Comb != "" {
						b.WriteString(c.Comb)
					}
				}
				end++
			}
			out = append(out, Span{
				Row: row, Col: col, Width: end - col, Text: b.String(),
				FG: first.FG, BG: first.BG, UL: first.UL, Flags: first.Flags,
			})
			col = end
		}
	}
	return out
}

func sameStyle(a, b Cell) bool {
	return a.FG == b.FG && a.BG == b.BG && a.Flags == b.Flags && a.UL == b.UL
}

// blank reports whether a run needs no text drawn: all spaces, so the
// background fill is the whole picture. Underline and strikethrough still
// print on blanks, so those runs are not blank.
func (r Span) blank() bool {
	if r.Flags&(FlagUnderline|FlagStrike) != 0 {
		return false
	}
	for _, c := range r.Text {
		if c != ' ' {
			return false
		}
	}
	return true
}

// fadeAmount is how far an unfocused pane's colours move towards the
// background: enough that the focused pane is plainly the one being typed in,
// not so much that the others look switched off.
const fadeAmount = 0.42

// fade moves a colour part of the way towards the background.
//
// This is how an unfocused pane is told apart, rather than a darkening wash
// laid over it: a wash covers only the cells, and stops short of the padding
// and the divider, so each inactive pane became a darker box with a light
// border round it. Fading the colours leaves every background exactly the
// window's own, and only the text recedes.
func fade(rgb, bg uint32) uint32 {
	mix := func(shift uint) uint32 {
		a := float64((rgb >> shift) & 0xff)
		b := float64((bg >> shift) & 0xff)
		return uint32(a+(b-a)*fadeAmount+0.5) << shift
	}
	return mix(16) | mix(8) | mix(0)
}
