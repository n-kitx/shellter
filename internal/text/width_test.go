package text

import "testing"

func TestWidth(t *testing.T) {
	cases := []struct {
		name string
		r    rune
		want int
	}{
		{"ascii", 'a', 1},
		{"digit", '7', 1},
		{"space", ' ', 1},
		{"latin accented", 'é', 1},
		{"cyrillic", 'д', 1},
		{"greek", 'λ', 1},
		{"arrow", '→', 1},
		{"box drawing", '─', 1},
		{"replacement char", '�', 1},

		{"cjk han", '漢', 2},
		{"chinese", '中', 2},
		{"hiragana", 'あ', 2},
		{"katakana", 'ア', 2},
		{"hangul syllable", '한', 2},
		{"fullwidth A", 'Ａ', 2},
		{"cjk punctuation", '、', 2},
		{"emoji", '😀', 2},
		{"emoji symbol", '🚀', 2},

		{"combining acute", 0x0301, 0},
		{"combining cedilla", 0x0327, 0},
		{"zero width joiner", 0x200d, 0},
		{"variation selector 16", 0xfe0f, 0},
		{"hangul medial jamo", 0x1160, 0},

		{"nul", 0, 0},
		{"control", '\n', 0},
		{"delete", 0x7f, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Width(c.r); got != c.want {
				t.Fatalf("Width(%U) = %d, want %d", c.r, got, c.want)
			}
		})
	}
}

func TestStringWidth(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"", 0},
		{"hello", 5},
		{"漢字", 4},
		{"a漢b", 4},
		{"é", 1},  // precomposed
		{"é", 1}, // e + combining acute: still one column
		{"👍", 2},
		{"こんにちは", 10},
	}
	for _, c := range cases {
		if got := StringWidth(c.s); got != c.want {
			t.Fatalf("StringWidth(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

func TestWideRangesAreSortedAndDisjoint(t *testing.T) {
	// The lookup binary-searches, so the table must stay ordered.
	for i := 1; i < len(wideRanges); i++ {
		if wideRanges[i][0] <= wideRanges[i-1][1] {
			t.Fatalf("range %d (%U-%U) overlaps or follows %U-%U",
				i, wideRanges[i][0], wideRanges[i][1],
				wideRanges[i-1][0], wideRanges[i-1][1])
		}
	}
	for i, r := range wideRanges {
		if r[0] > r[1] {
			t.Fatalf("range %d is inverted: %U-%U", i, r[0], r[1])
		}
	}
}
