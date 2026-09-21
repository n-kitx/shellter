package screen

// SGR - Select Graphic Rendition, "ESC [ ... m" - is the sequence that sets
// colours and styles. Its parameters are read left to right, each one changing
// part of the pen, which is why "ESC [ 1;31m" means bold *and* red rather than
// one replacing the other.
func (a *Attr) applySGR(params []int) {
	// No parameters at all means reset: "ESC [ m" is "ESC [ 0 m".
	if len(params) == 0 {
		*a = Attr{}
		return
	}

	for i := 0; i < len(params); i++ {
		p := params[i]
		switch {
		case p == 0:
			*a = Attr{}
		case p == 1:
			a.Flags |= FlagBold
		case p == 2:
			a.Flags |= FlagFaint
		case p == 3:
			a.Flags |= FlagItalic
		case p == 4:
			a.Flags |= FlagUnderline
		case p == 5, p == 6:
			a.Flags |= FlagBlink
		case p == 7:
			a.Flags |= FlagInverse
		case p == 8:
			a.Flags |= FlagHidden
		case p == 9:
			a.Flags |= FlagStrike

		// The "turn it off again" parameters. 22 clears both bold and faint,
		// because they are two ends of one VT100 intensity setting.
		case p == 21, p == 22:
			a.Flags &^= FlagBold | FlagFaint
		case p == 23:
			a.Flags &^= FlagItalic
		case p == 24:
			a.Flags &^= FlagUnderline
		case p == 25:
			a.Flags &^= FlagBlink
		case p == 27:
			a.Flags &^= FlagInverse
		case p == 28:
			a.Flags &^= FlagHidden
		case p == 29:
			a.Flags &^= FlagStrike

		// 30-37 and 40-47 are the eight original colours; 90-97 and 100-107
		// are their bright forms, which live at palette entries 8-15.
		case p >= 30 && p <= 37:
			a.FG = Indexed(uint8(p - 30))
		case p >= 40 && p <= 47:
			a.BG = Indexed(uint8(p - 40))
		case p >= 90 && p <= 97:
			a.FG = Indexed(uint8(p-90) + 8)
		case p >= 100 && p <= 107:
			a.BG = Indexed(uint8(p-100) + 8)
		case p == 39:
			a.FG = Color{}
		case p == 49:
			a.BG = Color{}

		// 38 and 48 are escape hatches that consume the parameters after them:
		//	38;5;n        palette entry n
		//	38;2;r;g;b    a direct colour
		case p == 38:
			if c, used, ok := extendedColor(params[i:]); ok {
				a.FG = c
				i += used - 1
			} else {
				return // malformed: the rest of the sequence is unreadable
			}
		case p == 48:
			if c, used, ok := extendedColor(params[i:]); ok {
				a.BG = c
				i += used - 1
			} else {
				return
			}
		}
	}
}

// extendedColor reads a 38/48 colour starting at params[0], returning the
// colour and how many parameters it consumed.
func extendedColor(params []int) (Color, int, bool) {
	if len(params) < 2 {
		return Color{}, 0, false
	}
	switch params[1] {
	case 5: // 38;5;n
		if len(params) < 3 {
			return Color{}, 0, false
		}
		return Indexed(clampByte(params[2])), 3, true
	case 2: // 38;2;r;g;b
		if len(params) < 5 {
			return Color{}, 0, false
		}
		return RGB(clampByte(params[2]), clampByte(params[3]), clampByte(params[4])), 5, true
	}
	return Color{}, 0, false
}

func clampByte(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}
