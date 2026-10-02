package screen

// Colors here are *semantic*, not pixels. "Colour 1" means "whatever this
// terminal calls red", and turning that into an RGB value is the renderer's
// job, not the screen's. That split is what will make themes possible without
// touching this package.

// ColorKind says how a Color should be interpreted.
type ColorKind uint8

const (
	// ColorDefault is the terminal's own foreground or background.
	ColorDefault ColorKind = iota
	// ColorIndexed is a palette entry: 0-7 the basic colours, 8-15 their
	// bright variants, 16-255 the xterm cube and greyscale ramp.
	ColorIndexed
	// ColorRGB is a direct 24-bit colour, from "ESC [ 38;2;r;g;b m".
	ColorRGB
)

// Color is a foreground or background colour.
type Color struct {
	Kind    ColorKind
	Index   uint8
	R, G, B uint8
}

// Indexed returns palette colour i.
func Indexed(i uint8) Color { return Color{Kind: ColorIndexed, Index: i} }

// RGB returns a direct 24-bit colour.
func RGB(r, g, b uint8) Color { return Color{Kind: ColorRGB, R: r, G: g, B: b} }

// Flags are the non-colour parts of SGR.
type Flags uint16

const (
	FlagBold Flags = 1 << iota
	FlagFaint
	FlagItalic
	FlagUnderline
	FlagBlink
	FlagInverse
	FlagHidden
	FlagStrike
	// The underline's shape, alongside FlagUnderline and at most one of them;
	// none of them means a single straight line. SGR 4:2 to 4:5.
	FlagUnderlineDouble
	FlagUnderlineCurly
	FlagUnderlineDotted
	FlagUnderlineDashed
)

// underlineFlags is every flag that says something about the underline.
const underlineFlags = FlagUnderline | FlagUnderlineDouble | FlagUnderlineCurly |
	FlagUnderlineDotted | FlagUnderlineDashed

// Has reports whether every flag in f is set.
func (fl Flags) Has(f Flags) bool { return fl&f == f }

// Attr is the "pen": the colours and styles a printed character is stamped
// with. Every cell carries a copy, because a cell keeps its appearance long
// after the pen has changed.
type Attr struct {
	FG, BG Color
	// UL is the underline's own colour (SGR 58), or the default - the text's.
	// Editors draw red squiggles for errors and yellow ones for warnings.
	UL    Color
	Flags Flags
}
