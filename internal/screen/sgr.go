package screen

// SGR - Select Graphic Rendition, "ESC [ ... m" - is the sequence that sets
// colours and styles. Its parameters are read left to right, each one changing
// part of the pen, which is why "ESC [ 1;31m" means bold *and* red rather than
// one replacing the other.
//
// sub marks the parameters that followed a ':' (parser.Event.Sub). Those
// belong to the parameter before them: "4:3" is underline style 3, wavy, and
// "38:2::r:g:b" a colour - the newer, unambiguous spellings of what ';' says
// with "4" and "38;2;r;g;b".
func (a *Attr) applySGR(params []int, sub uint32) {
	// No parameters at all means reset: "ESC [ m" is "ESC [ 0 m".
	if len(params) == 0 {
		*a = Attr{}
		return
	}

	for i := 0; i < len(params); i++ {
		p := params[i]
		// The sub-parameters that go with this one, if any.
		end := i + 1
		for end < len(params) && end < 32 && sub&(1<<end) != 0 {
			end++
		}
		if subs := params[i+1 : end]; len(subs) > 0 {
			a.applyColonSGR(p, subs)
			i = end - 1
			continue
		}
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
			a.setUnderline(1)
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
		// 21 is a double underline (ECMA-48, and xterm, kitty and Ghostty);
		// the Linux console's "bold off" reading of it lost that argument.
		case p == 21:
			a.setUnderline(2)
		case p == 22:
			a.Flags &^= FlagBold | FlagFaint
		case p == 23:
			a.Flags &^= FlagItalic
		case p == 24:
			a.setUnderline(0)
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
		case p == 58: // the underline's colour, spelled like 38's
			if c, used, ok := extendedColor(params[i:]); ok {
				a.UL = c
				i += used - 1
			} else {
				return
			}
		case p == 59:
			a.UL = Color{}
		}
	}
}

// applyColonSGR applies one parameter that came with sub-parameters.
func (a *Attr) applyColonSGR(p int, subs []int) {
	switch p {
	case 4: // 4:0 none, 4:1 single, 4:2 double, 4:3 wavy, 4:4 dotted, 4:5 dashed
		a.setUnderline(subs[0])
	case 38, 48, 58:
		c, ok := colonColor(subs)
		if !ok {
			return
		}
		switch p {
		case 38:
			a.FG = c
		case 48:
			a.BG = c
		default:
			a.UL = c
		}
	}
}

// colonColor reads a colour's sub-parameters: "5:n" for palette entry n, and
// "2:r:g:b" or "2:id:r:g:b" for a direct colour, where id is the colour space
// ECMA-48 put there and everyone leaves empty.
func colonColor(subs []int) (Color, bool) {
	switch {
	case subs[0] == 5 && len(subs) >= 2:
		return Indexed(clampByte(subs[1])), true
	case subs[0] == 2 && len(subs) >= 5:
		return RGB(clampByte(subs[2]), clampByte(subs[3]), clampByte(subs[4])), true
	case subs[0] == 2 && len(subs) == 4:
		return RGB(clampByte(subs[1]), clampByte(subs[2]), clampByte(subs[3])), true
	}
	return Color{}, false
}

// setUnderline sets the underline's style: 0 none, 1 single, 2 double, 3 wavy,
// 4 dotted, 5 dashed. A style it does not know is a single line, so the text
// is still marked as the program meant.
func (a *Attr) setUnderline(style int) {
	a.Flags &^= underlineFlags
	switch style {
	case 0:
	case 2:
		a.Flags |= FlagUnderline | FlagUnderlineDouble
	case 3:
		a.Flags |= FlagUnderline | FlagUnderlineCurly
	case 4:
		a.Flags |= FlagUnderline | FlagUnderlineDotted
	case 5:
		a.Flags |= FlagUnderline | FlagUnderlineDashed
	default:
		a.Flags |= FlagUnderline
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
