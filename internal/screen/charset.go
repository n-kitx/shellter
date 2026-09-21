package screen

// Character sets.
//
// Before UTF-8, a terminal drew boxes by *switching alphabets*: "ESC ( 0"
// redefines the ASCII range so that `q` is a horizontal line and `x` a vertical
// one. ncurses still does this, so less, top and dialog-style programs draw
// their frames with it. Without the translation the screen fills with
// `qqqqqqqq` where the line should be - a classic broken-terminal symptom.
//
//	ESC ( 0    G0 := DEC special graphics      ESC ( B    G0 := ASCII
//	ESC ) 0    G1 := DEC special graphics      ESC ) B    G1 := ASCII
//	SO (0x0e)  use G1                          SI (0x0f)  use G0
type charset byte

const (
	charsetASCII    charset = 'B'
	charsetGraphics charset = '0'
)

// charsets is the pair of designated sets plus which one is in use.
type charsets struct {
	g0, g1  charset
	shifted bool // SO: G1 is in use
}

func defaultCharsets() charsets { return charsets{g0: charsetASCII, g1: charsetASCII} }

// active is the set characters are currently translated through.
func (c charsets) active() charset {
	if c.shifted {
		return c.g1
	}
	return c.g0
}

// designate handles "ESC ( x" and "ESC ) x". Sets we do not implement are
// mapped to ASCII, which prints the original characters rather than nothing.
func (c *charsets) designate(slot byte, set byte) {
	cs := charset(set)
	if cs != charsetGraphics {
		cs = charsetASCII
	}
	switch slot {
	case '(':
		c.g0 = cs
	case ')':
		c.g1 = cs
	}
}

// graphics maps the ASCII range onto the DEC line-drawing glyphs. Only
// 0x5f-0x7e differ; everything else passes through.
var graphics = map[rune]rune{
	'_': ' ', '`': '◆', 'a': '▒', 'b': '␉', 'c': '␌', 'd': '␍', 'e': '␊',
	'f': '°', 'g': '±', 'h': '␤', 'i': '␋', 'j': '┘', 'k': '┐', 'l': '┌',
	'm': '└', 'n': '┼', 'o': '⎺', 'p': '⎻', 'q': '─', 'r': '⎼', 's': '⎽',
	't': '├', 'u': '┤', 'v': '┴', 'w': '┬', 'x': '│', 'y': '≤', 'z': '≥',
	'{': 'π', '|': '≠', '}': '£', '~': '·',
}

// translate maps one character through the active set.
func (c charset) translate(r rune) rune {
	if c != charsetGraphics {
		return r
	}
	if g, ok := graphics[r]; ok {
		return g
	}
	return r
}
