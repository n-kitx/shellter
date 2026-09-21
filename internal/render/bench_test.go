package render

import "testing"

// Phase 10 baseline for the draw path. spans() runs once per frame and decides
// how many calls cross into AppKit.

// frameFor builds a frame that looks like a shell screen: mostly default
// colours with a few coloured stretches per line.
func frameFor(rows, cols int) Frame {
	f := Frame{Rows: rows, Cols: cols, Cells: make([]Cell, rows*cols), CursorVisible: true, CursorWidth: 1}
	text := []rune("the quick brown fox jumps over the lazy dog 0123456789 ")
	for i := range f.Cells {
		fg := uint32(0xd0d0d0)
		if (i/8)%5 == 0 {
			fg = 0x00af00
		}
		f.Cells[i] = Cell{Rune: text[i%len(text)], FG: fg}
	}
	return f
}

func BenchmarkSpans(b *testing.B) {
	f := frameFor(24, 80)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = spans(f)
	}
}

// A blank screen is the cheap case, and the one a fresh window draws.
func BenchmarkSpansBlank(b *testing.B) {
	f := Frame{Rows: 24, Cols: 80, Cells: make([]Cell, 24*80)}
	for i := range f.Cells {
		f.Cells[i] = Cell{Rune: ' '}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = spans(f)
	}
}

func BenchmarkSpansCount(b *testing.B) {
	f := frameFor(24, 80)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.SetBytes(int64(len(spans(f))))
	}
}
