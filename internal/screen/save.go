package screen

import (
	"strconv"
	"strings"
)

// Transcript is the last lines of the session - history, then the live grid
// down to its last line with anything on it - as the bytes that would draw
// them again: text, with SGR wherever the colours or styles change. Feeding
// it to a new screen puts the text back, which is how a session is restored
// after Shellter quits.
//
// A line that wrapped runs straight on into the next one, so the restored
// text reflows to whatever width it is given rather than keeping this one's.
// A full-screen program's alternate screen is not part of it: that is the
// program's, and the program is gone. Only the history behind it is kept.
func (s *Screen) Transcript(maxLines int) string {
	last := s.TotalLines()
	if s.altActive {
		last = s.gridLine()
	} else {
		for last > s.gridLine() && s.lineBlank(int(last-s.gridLine()-1)) {
			last--
		}
	}
	first := s.firstLine()
	if last-first > int64(maxLines) {
		first = last - int64(maxLines)
	}

	buf := make([]Cell, s.cols)
	var b strings.Builder
	var pen Attr
	for n := first; n < last; n++ {
		wrapped, ok := s.lineAt(n, buf)
		if !ok {
			continue
		}
		end := s.cols
		if !wrapped {
			for end > 0 && isPlainBlank(buf[end-1]) {
				end--
			}
		}
		for _, c := range buf[:end] {
			if c.IsContinuation() {
				continue
			}
			if c.Attr != pen {
				b.WriteString(sgrFor(c.Attr))
				pen = c.Attr
			}
			b.WriteRune(c.Rune)
			b.WriteString(c.Comb)
		}
		if !wrapped && n < last-1 {
			if pen != (Attr{}) {
				// Reset before the line ends, so a coloured background does
				// not bleed across the rest of the row when it is replayed.
				b.WriteString("\x1b[0m")
				pen = Attr{}
			}
			b.WriteString("\r\n")
		}
	}
	if pen != (Attr{}) {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// lineBlank reports whether a row of the live grid has nothing on it.
func (s *Screen) lineBlank(r int) bool {
	for _, c := range s.row(r) {
		if !isPlainBlank(c) {
			return false
		}
	}
	return true
}

// sgrFor is the SGR sequence that sets the pen to a, from nothing: it always
// starts with a reset, so it does not depend on what came before.
func sgrFor(a Attr) string {
	params := []string{"0"}
	for _, f := range []struct {
		flag Flags
		code string
	}{
		{FlagBold, "1"}, {FlagFaint, "2"}, {FlagItalic, "3"}, {FlagUnderline, "4"},
		{FlagBlink, "5"}, {FlagInverse, "7"}, {FlagHidden, "8"}, {FlagStrike, "9"},
	} {
		if a.Flags.Has(f.flag) {
			params = append(params, f.code)
		}
	}
	params = appendColor(params, a.FG, 30, 38)
	params = appendColor(params, a.BG, 40, 48)
	return "\x1b[" + strings.Join(params, ";") + "m"
}

// appendColor adds the parameters for one colour: base+i for the eight named
// ones, base+60+i for their bright halves, and the extended form, ext, for the
// rest of the 256 and for RGB.
func appendColor(params []string, c Color, base, ext int) []string {
	switch c.Kind {
	case ColorIndexed:
		switch {
		case c.Index < 8:
			return append(params, strconv.Itoa(base+int(c.Index)))
		case c.Index < 16:
			return append(params, strconv.Itoa(base+60+int(c.Index)-8))
		}
		return append(params, strconv.Itoa(ext), "5", strconv.Itoa(int(c.Index)))
	case ColorRGB:
		return append(params, strconv.Itoa(ext), "2",
			strconv.Itoa(int(c.R)), strconv.Itoa(int(c.G)), strconv.Itoa(int(c.B)))
	}
	return params
}
