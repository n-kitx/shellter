package terminal

import (
	"github.com/thakurankit7/shellter/internal/render"
	"github.com/thakurankit7/shellter/internal/screen"
	"github.com/thakurankit7/shellter/internal/theme"
)

// A Palette turns the screen's semantic colours ("colour 1", "the default
// foreground") into the RGB values the renderer draws with.
//
// It sits here, in the application layer, on purpose. The screen buffer should
// not care what red looks like, and the renderer should not have opinions about
// it either. A theme is a different Palette and nothing else changes - which is
// exactly what happened when themes arrived.
type Palette struct {
	Foreground uint32
	Background uint32
	// Selection is the background behind text the user has marked.
	Selection uint32
	// ANSI holds the sixteen named colours: 0-7 normal, 8-15 bright.
	ANSI [16]uint32
}

// DefaultPalette is the built-in theme.
func DefaultPalette() Palette { return PaletteFor(theme.Default()) }

// PaletteFor turns a theme into the palette the renderer resolves against.
//
// The conversion is a copy, which is the point: past here nothing knows a theme
// was ever involved, so choosing one is a decision made once at startup rather
// than a question asked of every cell.
func PaletteFor(t theme.Theme) Palette {
	return Palette{
		Foreground: t.Foreground,
		Background: t.Background,
		Selection:  t.Selection,
		ANSI:       t.ANSI,
	}
}

// resolve turns one Color into RGB. def is what "the default" means in this
// position - the palette's foreground or background.
func (p Palette) resolve(c screen.Color, def uint32) uint32 {
	switch c.Kind {
	case screen.ColorRGB:
		return uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
	case screen.ColorIndexed:
		return p.indexed(c.Index)
	default:
		return def
	}
}

// indexed implements the 256-colour layout every terminal agrees on:
//
//	0-15     the named colours
//	16-231   a 6x6x6 colour cube
//	232-255  a 24-step grey ramp
func (p Palette) indexed(i uint8) uint32 {
	switch {
	case i < 16:
		return p.ANSI[i]
	case i < 232:
		levels := [6]uint32{0, 95, 135, 175, 215, 255}
		n := uint32(i) - 16
		r := levels[(n/36)%6]
		g := levels[(n/6)%6]
		b := levels[n%6]
		return r<<16 | g<<8 | b
	default:
		grey := 8 + 10*(uint32(i)-232)
		return grey<<16 | grey<<8 | grey
	}
}

// Frame converts a screen snapshot into something the renderer can draw,
// resolving every appearance question on the way:
//
//	inverse -> the two colours swapped
//	faint   -> a dimmer foreground
//	hidden  -> foreground equal to background
//
// After this, the renderer only has to know about RGB, bold, italic, underline
// and strikethrough.
func (p Palette) Frame(snap screen.Snapshot) render.Frame {
	return p.FrameInto(snap, nil, nil)
}

// FrameInto is Frame with somewhere to put the cells, so that a window drawing
// 60 frames a second can reuse one buffer instead of allocating a grid per
// frame. The returned Frame borrows buf; it is valid until the next call.
//
// sel is the selected columns of each view row, or nil when nothing is
// selected. It is applied here, with the rest of the appearance questions,
// rather than in the renderer: past this point a cell is just two colours and
// some flags, and the renderer has never needed to know why.
func (p Palette) FrameInto(snap screen.Snapshot, buf []render.Cell, sel []span) render.Frame {
	cells := buf
	if len(cells) != len(snap.Cells) {
		cells = make([]render.Cell, len(snap.Cells))
	}
	for i, c := range snap.Cells {
		fg := p.resolve(c.Attr.FG, p.Foreground)
		bg := p.resolve(c.Attr.BG, p.Background)

		if c.Attr.Flags.Has(screen.FlagFaint) {
			fg = p.dim(fg)
		}
		if c.Attr.Flags.Has(screen.FlagInverse) {
			fg, bg = bg, fg
		}
		if c.Attr.Flags.Has(screen.FlagHidden) {
			fg = bg
		}

		// Selection is applied last, so it wins over inverse video: a marked
		// region has to be unmistakable even where the program was already
		// painting with the colours swapped.
		if selected(sel, i/snap.Cols, i%snap.Cols) {
			bg = p.Selection
			if fg == bg {
				fg = p.Background // never leave the text invisible
			}
		}

		cells[i] = render.Cell{
			Rune:  c.Rune,
			Comb:  c.Comb,
			FG:    fg,
			BG:    bg,
			Flags: renderFlags(c.Attr.Flags),
		}
	}

	// A cursor sitting on a wide character must cover both of its columns.
	cursorWidth := 1
	if snap.CellAt(snap.Cursor.Row, snap.Cursor.Col).Width() == 2 {
		cursorWidth = 2
	}

	return render.Frame{
		Rows:          snap.Rows,
		Cols:          snap.Cols,
		Cells:         cells,
		CursorRow:     snap.Cursor.Row,
		CursorCol:     snap.Cursor.Col,
		CursorVisible: snap.Modes.CursorVisible,
		CursorWidth:   cursorWidth,
		CursorStyle:   render.CursorStyle(snap.CursorStyle),
		DefaultBG:     p.Background,
	}
}

// selected reports whether a cell falls inside the selection. Two integer
// comparisons per cell: the spans were worked out once per row, before this.
func selected(sel []span, row, col int) bool {
	if row < 0 || row >= len(sel) {
		return false
	}
	sp := sel[row]
	return col >= sp.from && col < sp.to
}

// renderFlags keeps only the styles the renderer draws. Blink is dropped
// deliberately: nothing here animates.
func renderFlags(f screen.Flags) render.Flags {
	var out render.Flags
	if f.Has(screen.FlagBold) {
		out |= render.FlagBold
	}
	if f.Has(screen.FlagItalic) {
		out |= render.FlagItalic
	}
	if f.Has(screen.FlagUnderline) {
		out |= render.FlagUnderline
	}
	if f.Has(screen.FlagStrike) {
		out |= render.FlagStrike
	}
	return out
}

// dim moves a colour towards the background, which is what "faint" looks like.
//
// Towards the background, not towards black: on a light theme black is the
// high-contrast end of the range, so a faint that darkened would come out
// looking bolder than the text around it.
func (p Palette) dim(rgb uint32) uint32 { return mix(rgb, p.Background, 0.4) }

// mix blends two RGB colours, t of the way from a to b.
func mix(a, b uint32, t float64) uint32 {
	blend := func(shift uint) uint32 {
		av := float64((a >> shift) & 0xff)
		bv := float64((b >> shift) & 0xff)
		return uint32(av + (bv-av)*t)
	}
	return blend(16)<<16 | blend(8)<<8 | blend(0)
}
