// Package text answers one question: how many columns does a rune occupy?
//
// It is the only place in Shellter that knows about Unicode beyond decoding.
// The parser already turns bytes into runes; this package turns runes into
// *cells*, which is a different thing entirely:
//
//	'a'      1 column
//	'é'      1 column
//	'漢'      2 columns - a wide character occupies two cells
//	U+0301   0 columns - a combining accent lives on the cell before it
//
// Keeping it here means the screen buffer can stay simple arithmetic and the
// renderer never has to think about Unicode at all.
package text

import "unicode"

// Width returns how many terminal columns r occupies: 0, 1 or 2.
//
// Printable ASCII is checked first and answered with one comparison. That is
// not a guess: profiling the renderer showed Width was 38% of the time spent
// splitting a frame into spans, because every plain 'a' was going through the
// combining-mark tables and a binary search of the wide ranges before coming
// back with 1.
func Width(r rune) int {
	if r >= 0x20 && r < 0x7f {
		return 1
	}
	if r == 0 {
		return 0
	}
	// C0 and C1 controls never reach the grid; the screen handles them as
	// events. Reported as zero so nothing accidentally advances on them.
	if r < 0x20 || (r >= 0x7f && r < 0xa0) {
		return 0
	}
	if IsCombining(r) {
		return 0
	}
	if isWide(r) {
		return 2
	}
	return 1
}

// StringWidth is the total width of a string, i.e. how many columns printing it
// would take.
func StringWidth(s string) int {
	w := 0
	for _, r := range s {
		w += Width(r)
	}
	return w
}

// IsCombining reports whether r attaches to the character before it rather than
// occupying a cell of its own: accents, diacritics, joiners, variation
// selectors.
func IsCombining(r rune) bool {
	// Mn: nonspacing marks (é as e + U+0301). Me: enclosing marks.
	// Cf: format characters, which includes the zero-width joiner that glues
	// emoji sequences together.
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
		return true
	}
	// Hangul Jamo medial and final letters compose onto the leading jamo.
	if r >= 0x1160 && r <= 0x11ff {
		return true
	}
	return false
}

// wideRanges approximates the Unicode East Asian Width "W" and "F" classes:
// the characters that terminals draw two cells wide. It is a curated subset
// covering the blocks that actually turn up - CJK, Hangul, kana, fullwidth
// forms and emoji - rather than a generated copy of EastAsianWidth.txt.
var wideRanges = [][2]rune{
	{0x1100, 0x115f}, // Hangul Jamo leading consonants
	{0x231a, 0x231b}, // watch, hourglass
	{0x2329, 0x232a}, // angle brackets
	{0x23e9, 0x23ec},
	{0x23f0, 0x23f0},
	{0x23f3, 0x23f3},
	{0x25fd, 0x25fe},
	{0x2614, 0x2615},
	{0x2648, 0x2653}, // zodiac
	{0x267f, 0x267f},
	{0x2693, 0x2693},
	{0x26a1, 0x26a1},
	{0x26aa, 0x26ab},
	{0x26bd, 0x26be},
	{0x26c4, 0x26c5},
	{0x26ce, 0x26ce},
	{0x26d4, 0x26d4},
	{0x26ea, 0x26ea},
	{0x26f2, 0x26f3},
	{0x26f5, 0x26f5},
	{0x26fa, 0x26fa},
	{0x26fd, 0x26fd},
	{0x2705, 0x2705},
	{0x270a, 0x270b},
	{0x2728, 0x2728},
	{0x274c, 0x274c},
	{0x274e, 0x274e},
	{0x2753, 0x2755},
	{0x2757, 0x2757},
	{0x2795, 0x2797},
	{0x27b0, 0x27b0},
	{0x27bf, 0x27bf},
	{0x2b1b, 0x2b1c},
	{0x2b50, 0x2b50},
	{0x2b55, 0x2b55},
	{0x2e80, 0x303e}, // CJK radicals, Kangxi, CJK symbols and punctuation
	{0x3041, 0x33ff}, // hiragana, katakana, bopomofo, Hangul compat, CJK compat
	{0x3400, 0x4dbf}, // CJK extension A
	{0x4e00, 0x9fff}, // CJK unified ideographs
	{0xa000, 0xa4cf}, // Yi
	{0xa960, 0xa97f}, // Hangul Jamo extended A
	{0xac00, 0xd7a3}, // Hangul syllables
	{0xf900, 0xfaff}, // CJK compatibility ideographs
	{0xfe10, 0xfe19}, // vertical forms
	{0xfe30, 0xfe6f}, // CJK compatibility forms, small form variants
	{0xff00, 0xff60}, // fullwidth ASCII forms
	{0xffe0, 0xffe6}, // fullwidth signs
	{0x16fe0, 0x16fe4},
	{0x17000, 0x18aff}, // Tangut
	{0x1b000, 0x1b16f}, // kana supplement
	{0x1f004, 0x1f004}, // mahjong red dragon
	{0x1f0cf, 0x1f0cf}, // playing card black joker
	{0x1f18e, 0x1f18e},
	{0x1f191, 0x1f19a},
	{0x1f200, 0x1f2ff},
	{0x1f300, 0x1f64f}, // emoji: symbols, pictographs, emoticons
	{0x1f680, 0x1f6ff}, // transport and map symbols
	{0x1f900, 0x1f9ff}, // supplemental symbols and pictographs
	{0x1fa70, 0x1faff}, // symbols and pictographs extended A
	{0x20000, 0x2fffd}, // CJK extension planes
	{0x30000, 0x3fffd},
}

// isWide binary-searches the range table.
func isWide(r rune) bool {
	lo, hi := 0, len(wideRanges)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		switch {
		case r < wideRanges[mid][0]:
			hi = mid - 1
		case r > wideRanges[mid][1]:
			lo = mid + 1
		default:
			return true
		}
	}
	return false
}
