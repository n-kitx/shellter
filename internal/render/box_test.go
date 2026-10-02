package render

import "testing"

// Every box-drawing and block character, and the Powerline set, has a shape;
// nothing next to them does.
func TestBoxGlyphCoverage(t *testing.T) {
	for r := rune(0x2500); r <= 0x259F; r++ {
		if _, ok := boxGlyph(r); !ok {
			t.Errorf("U+%04X has no shape", r)
		}
	}
	for r := rune(0xE0B0); r <= 0xE0B7; r++ {
		if g, ok := boxGlyph(r); !ok || g.kind != glyphPowerline {
			t.Errorf("U+%04X is not a Powerline shape", r)
		}
	}
	for _, r := range []rune{'a', ' ', 0x24FF, 0x25A0, 0xE0AF, 0xE0B8, '─' + 0x1000} {
		if _, ok := boxGlyph(r); ok {
			t.Errorf("U+%04X has a shape it should not", r)
		}
	}
	// Spot checks against the Unicode names.
	for _, c := range []struct {
		r    rune
		want glyph
	}{
		{'┌', glyph{glyphLines, 0, 1, 1, 0}},
		{'╋', glyph{glyphLines, 2, 2, 2, 2}},
		{'╠', glyph{glyphLines, 3, 3, 3, 0}},
		{'╒', glyph{glyphLines, 0, 3, 1, 0}},
		{'╭', glyph{glyphArc, 0, 0, 0, 0}},
		{'▄', glyph{glyphBlock, 0, 4, 8, 8}},
		{'▌', glyph{glyphBlock, 0, 0, 4, 8}},
		{'▒', glyph{glyphShade, 50, 0, 0, 0}},
		{'▚', glyph{glyphQuadrants, 9, 0, 0, 0}},
	} {
		if g, _ := boxGlyph(c.r); g != c.want {
			t.Errorf("%c: %+v, want %+v", c.r, g, c.want)
		}
	}
}

// A box character is a span of its own, so it can be drawn as a shape, and
// the text either side of it stays in runs.
func TestBoxGlyphsSplitRuns(t *testing.T) {
	text := "ab│cd─"
	f := Frame{Rows: 1, Cols: 6, Cells: make([]Cell, 6)}
	for i, r := range []rune(text) {
		f.Cells[i] = Cell{Rune: r, FG: 1}
	}
	var got []string
	for _, sp := range spans(f) {
		kind := "text"
		if sp.Glyph.kind != glyphNone {
			kind = "glyph"
		}
		got = append(got, kind+":"+sp.Text)
	}
	want := []string{"text:ab", "glyph:│", "text:cd", "glyph:─"}
	if len(got) != len(want) {
		t.Fatalf("spans %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("spans %v, want %v", got, want)
		}
	}
}
