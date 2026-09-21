package screen

import "testing"

func lineOf(text string) []Cell {
	out := make([]Cell, 0, len(text))
	for _, r := range text {
		out = append(out, Cell{Rune: r})
	}
	return out
}

func textOf(cells []Cell) string {
	var out []rune
	for _, c := range cells {
		out = append(out, c.Rune)
	}
	return string(out)
}

func TestScrollbackKeepsLinesInOrder(t *testing.T) {
	sb := newScrollback(4)
	for _, s := range []string{"one", "two", "three"} {
		sb.push(lineOf(s), false)
	}

	if sb.Len() != 3 {
		t.Fatalf("Len = %d, want 3", sb.Len())
	}
	for i, want := range []string{"one  ", "two  ", "three"} {
		if got := textOf(sb.line(i, 5)); got != want {
			t.Fatalf("line(%d) = %q, want %q", i, got, want)
		}
	}

	// A narrower view truncates rather than overflowing.
	if got := textOf(sb.line(2, 3)); got != "thr" {
		t.Fatalf("narrow line = %q, want %q", got, "thr")
	}
}

func TestScrollbackDropsTheOldestWhenFull(t *testing.T) {
	sb := newScrollback(3)
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		sb.push(lineOf(s), false)
	}

	if sb.Len() != 3 {
		t.Fatalf("Len = %d, want the capacity 3", sb.Len())
	}
	for i, want := range []string{"c", "d", "e"} {
		if got := textOf(sb.line(i, 1)); got != want {
			t.Fatalf("line(%d) = %q, want %q", i, got, want)
		}
	}
}

func TestScrollbackPadsAndTrims(t *testing.T) {
	sb := newScrollback(2)
	sb.push(lineOf("hi    "), false) // trailing blanks are not stored

	got := sb.line(0, 6)
	if len(got) != 6 {
		t.Fatalf("got %d cells, want the line padded to 6", len(got))
	}
	if textOf(got) != "hi    " {
		t.Fatalf("line = %q", textOf(got))
	}
	// A styled blank is *not* trailing padding and must survive.
	styled := lineOf("x ")
	styled[1].Attr.BG = Indexed(1)
	sb.push(styled, false)
	if got := sb.line(1, 2); got[1].Attr.BG != Indexed(1) {
		t.Fatal("a coloured blank was trimmed away")
	}
}

func TestScrollbackZeroCapacityStoresNothing(t *testing.T) {
	sb := newScrollback(0)
	sb.push(lineOf("gone"), false)
	if sb.Len() != 0 {
		t.Fatalf("Len = %d, want 0 with scrollback disabled", sb.Len())
	}
}

func TestScrollbackResizeKeepsTheNewestLines(t *testing.T) {
	sb := newScrollback(5)
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		sb.push(lineOf(s), false)
	}

	sb.resize(2)
	if sb.Len() != 2 {
		t.Fatalf("Len = %d, want 2", sb.Len())
	}
	for i, want := range []string{"d", "e"} {
		if got := textOf(sb.line(i, 1)); got != want {
			t.Fatalf("line(%d) = %q, want %q", i, got, want)
		}
	}

	// Growing keeps what is there and makes room for more.
	sb.resize(4)
	sb.push(lineOf("f"), false)
	if sb.Len() != 3 || textOf(sb.line(0, 1)) != "d" || textOf(sb.line(2, 1)) != "f" {
		t.Fatalf("after growing: len=%d first=%q last=%q",
			sb.Len(), textOf(sb.line(0, 1)), textOf(sb.line(2, 1)))
	}
}

func TestScrollbackClear(t *testing.T) {
	sb := newScrollback(3)
	sb.push(lineOf("x"), false)
	sb.clear()
	if sb.Len() != 0 {
		t.Fatalf("Len = %d, want 0 after clear", sb.Len())
	}
}
