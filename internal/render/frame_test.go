package render

import "testing"

func cells(f *Frame, spec string, fg, bg uint32, flags Flags) {
	for _, r := range spec {
		f.Cells = append(f.Cells, Cell{Rune: r, FG: fg, BG: bg, Flags: flags})
	}
}

func TestSpansBatchIdenticalCells(t *testing.T) {
	f := Frame{Rows: 1, Cols: 6}
	cells(&f, "aaa", 0xffffff, 0, 0)
	cells(&f, "bbb", 0xff0000, 0, 0)

	got := spans(f)
	if len(got) != 2 {
		t.Fatalf("got %d spans, want 2: %+v", len(got), got)
	}
	if got[0].Text != "aaa" || got[0].Col != 0 || got[0].FG != 0xffffff {
		t.Fatalf("span 0 = %+v", got[0])
	}
	if got[1].Text != "bbb" || got[1].Col != 3 || got[1].FG != 0xff0000 {
		t.Fatalf("span 1 = %+v", got[1])
	}
}

func TestSpansSplitOnEveryStyleChange(t *testing.T) {
	f := Frame{Rows: 1, Cols: 4}
	cells(&f, "a", 0xffffff, 0, 0)
	cells(&f, "b", 0xffffff, 0x000080, 0) // background changes
	cells(&f, "c", 0xffffff, 0x000080, FlagBold)
	cells(&f, "d", 0xffffff, 0x000080, FlagBold)

	got := spans(f)
	if len(got) != 3 {
		t.Fatalf("got %d spans, want 3: %+v", len(got), got)
	}
	if got[2].Text != "cd" || !got[2].Flags.Has(FlagBold) {
		t.Fatalf("span 2 = %+v, want bold \"cd\"", got[2])
	}
}

func TestSpansDoNotCrossRows(t *testing.T) {
	f := Frame{Rows: 2, Cols: 2}
	cells(&f, "ab", 1, 2, 0)
	cells(&f, "cd", 1, 2, 0)

	got := spans(f)
	if len(got) != 2 {
		t.Fatalf("got %d spans, want one per row: %+v", len(got), got)
	}
	if got[0].Row != 0 || got[1].Row != 1 {
		t.Fatalf("rows = %d, %d", got[0].Row, got[1].Row)
	}
}

func TestSpansDrawOrphanContinuationsAsBlanks(t *testing.T) {
	// A freshly allocated grid holds zero runes, which mean "continuation".
	// With no wide character in front of them they are simply blanks.
	f := Frame{Rows: 1, Cols: 3, Cells: make([]Cell, 3)}
	got := spans(f)
	if len(got) != 1 || got[0].Text != "   " || got[0].Width != 3 {
		t.Fatalf("spans = %+v, want one blank span three cells wide", got)
	}
}

func TestSpansGiveEachWideCharacterItsOwn(t *testing.T) {
	// "a漢b": the wide character is split out so it can be positioned exactly.
	f := Frame{Rows: 1, Cols: 4}
	f.Cells = []Cell{
		{Rune: 'a'},
		{Rune: '漢'},
		{Rune: 0}, // continuation
		{Rune: 'b'},
	}
	got := spans(f)
	if len(got) != 3 {
		t.Fatalf("got %d spans, want 3: %+v", len(got), got)
	}
	if got[0].Text != "a" || got[0].Width != 1 {
		t.Fatalf("span 0 = %+v", got[0])
	}
	if got[1].Text != "漢" || got[1].Width != 2 || got[1].Col != 1 {
		t.Fatalf("span 1 = %+v, want 漢 covering two cells at column 1", got[1])
	}
	if got[2].Text != "b" || got[2].Col != 3 {
		t.Fatalf("span 2 = %+v, want b at column 3", got[2])
	}
}

func TestSpanWidthIsColumnsNotRunes(t *testing.T) {
	f := Frame{Rows: 1, Cols: 2, Cells: []Cell{{Rune: '字'}, {Rune: 0}}}
	got := spans(f)
	if len(got) != 1 || got[0].Width != 2 || len([]rune(got[0].Text)) != 1 {
		t.Fatalf("span = %+v, want one rune covering two columns", got[0])
	}
}

func TestSpansCarryCombiningMarks(t *testing.T) {
	f := Frame{Rows: 1, Cols: 2, Cells: []Cell{
		{Rune: 'e', Comb: "\u0301"},
		{Rune: 'x'},
	}}
	got := spans(f)
	if len(got) != 1 || got[0].Text != "e\u0301x" {
		t.Fatalf("span = %+v, want the accent kept with its letter", got[0])
	}
	if got[0].Width != 2 {
		t.Fatalf("width = %d, want 2 cells", got[0].Width)
	}
}

func TestBlankSpansNeedNoText(t *testing.T) {
	if !(Span{Text: "   "}).blank() {
		t.Fatal("spaces should count as blank")
	}
	if (Span{Text: " x "}).blank() {
		t.Fatal("text is not blank")
	}
	// Underline and strikethrough are visible on a run of spaces.
	if (Span{Text: "   ", Flags: FlagUnderline}).blank() {
		t.Fatal("an underlined run must still be drawn")
	}
}
