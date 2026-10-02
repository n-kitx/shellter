package terminal

import (
	"testing"

	"github.com/thakurankit7/shellter/internal/parser"
	"github.com/thakurankit7/shellter/internal/render"
	"github.com/thakurankit7/shellter/internal/screen"
	"github.com/thakurankit7/shellter/internal/theme"
)

func renderScreen(t *testing.T, data string, rows, cols int) render.Frame {
	t.Helper()
	s := screen.New(rows, cols)
	parser.New().Parse([]byte(data), s.Apply)
	return DefaultPalette().Frame(s.Snapshot())
}

func TestDefaultColorsComeFromThePalette(t *testing.T) {
	p := DefaultPalette()
	f := renderScreen(t, "x", 1, 2)

	if got := f.CellAt(0, 0); got.FG != p.Foreground || got.BG != p.Background {
		t.Fatalf("cell = %+v, want the palette defaults", got)
	}
	if f.DefaultBG != p.Background {
		t.Fatalf("DefaultBG = %06x, want %06x", f.DefaultBG, p.Background)
	}
}

func TestNamedColorsResolve(t *testing.T) {
	p := DefaultPalette()
	f := renderScreen(t, "\x1b[31;42mx", 1, 2)

	if got := f.CellAt(0, 0).FG; got != p.ANSI[1] {
		t.Fatalf("fg = %06x, want %06x", got, p.ANSI[1])
	}
	if got := f.CellAt(0, 0).BG; got != p.ANSI[2] {
		t.Fatalf("bg = %06x, want %06x", got, p.ANSI[2])
	}
}

func TestIndexedColorCubeAndGreys(t *testing.T) {
	p := DefaultPalette()
	cases := []struct {
		index uint8
		want  uint32
	}{
		{1, p.ANSI[1]},  // named
		{9, p.ANSI[9]},  // bright named
		{16, 0x000000},  // first cube entry
		{231, 0xffffff}, // last cube entry
		{196, 0xff0000}, // the red everybody uses
		{21, 0x0000ff},  // pure blue in the cube
		{232, 0x080808}, // first grey
		{255, 0xeeeeee}, // last grey
	}
	for _, c := range cases {
		if got := p.indexed(c.index); got != c.want {
			t.Fatalf("indexed(%d) = %06x, want %06x", c.index, got, c.want)
		}
	}
}

func TestTrueColorPassesThrough(t *testing.T) {
	f := renderScreen(t, "\x1b[38;2;18;52;86mx", 1, 2)
	if got := f.CellAt(0, 0).FG; got != 0x123456 {
		t.Fatalf("fg = %06x, want 123456", got)
	}
}

func TestInverseSwapsColors(t *testing.T) {
	p := DefaultPalette()
	f := renderScreen(t, "\x1b[7mx", 1, 2)

	if got := f.CellAt(0, 0); got.FG != p.Background || got.BG != p.Foreground {
		t.Fatalf("cell = %+v, want the defaults swapped", got)
	}

	// Inverse applies after the colours resolve, so an explicit pair swaps too.
	f = renderScreen(t, "\x1b[31;47;7mx", 1, 2)
	if got := f.CellAt(0, 0); got.FG != p.ANSI[7] || got.BG != p.ANSI[1] {
		t.Fatalf("cell = %+v, want white on red", got)
	}
}

func TestFaintDimsAndHiddenMatchesTheBackground(t *testing.T) {
	// Faint moves the text towards the background, so what it does depends on
	// which way the background is: on a light theme the same rule has to make
	// text lighter, not darker.
	for _, name := range []string{"shellter", "github-light"} {
		th, ok := theme.Get(name)
		if !ok {
			t.Fatalf("no theme %q", name)
		}
		p := PaletteFor(th)
		grey := uint32(0x646464)
		faint := p.dim(grey)
		for _, shift := range []uint{16, 8, 0} {
			got := (faint >> shift) & 0xff
			from := (grey >> shift) & 0xff
			to := (p.Background >> shift) & 0xff
			if got < min(from, to) || got > max(from, to) {
				t.Fatalf("%s: faint channel %d is %d, outside %d..%d", name, shift, got, from, to)
			}
			if got == from && from != to {
				t.Fatalf("%s: faint channel %d did not move at all", name, shift)
			}
		}
	}

	f := renderScreen(t, "\x1b[8mx", 1, 2)
	if c := f.CellAt(0, 0); c.FG != c.BG {
		t.Fatalf("hidden cell = %+v, want fg == bg", c)
	}
}

func TestStylesMapToRendererFlags(t *testing.T) {
	f := renderScreen(t, "\x1b[1;3;4;9mx", 1, 2)
	got := f.CellAt(0, 0).Flags
	for _, want := range []render.Flags{render.FlagBold, render.FlagItalic, render.FlagUnderline, render.FlagStrike} {
		if !got.Has(want) {
			t.Fatalf("flags = %b, missing %b", got, want)
		}
	}

	// Blink reaches the frame, for the GUI to animate.
	f = renderScreen(t, "\x1b[5mx", 1, 2)
	if f.CellAt(0, 0).Flags != render.FlagBlink {
		t.Fatalf("flags = %b, want just blink", f.CellAt(0, 0).Flags)
	}
}

func TestCursorVisibilityReachesTheFrame(t *testing.T) {
	if f := renderScreen(t, "x", 1, 2); !f.CursorVisible {
		t.Fatal("the cursor should be visible by default")
	}
	if f := renderScreen(t, "\x1b[?25lx", 1, 2); f.CursorVisible {
		t.Fatal("ESC[?25l should hide the cursor in the frame")
	}
}

func TestErasedRegionKeepsItsBackground(t *testing.T) {
	p := DefaultPalette()
	// Paint the screen red, which is a background fill with no text at all.
	f := renderScreen(t, "\x1b[41m\x1b[2J", 2, 3)
	for col := 0; col < 3; col++ {
		if got := f.CellAt(0, col).BG; got != p.ANSI[1] {
			t.Fatalf("cell(0,%d) bg = %06x, want %06x", col, got, p.ANSI[1])
		}
	}
}

// An underline's style and colour reach the renderer: the colour resolved
// like any other, and runs that differ only in it drawn apart.
func TestUnderlineStyleAndColourReachTheRenderer(t *testing.T) {
	p := DefaultPalette()
	s := screen.New(1, 10)
	ps := parser.New()
	ps.Parse([]byte("\x1b[4:3;58:5:1mab\x1b[58:5:3mcd\x1b[4mef\x1b[0m"), s.Apply)
	f := p.Frame(s.Snapshot())

	wavy := f.CellAt(0, 0)
	if !wavy.Flags.Has(render.FlagUnderline|render.FlagUnderlineCurly|render.FlagUnderlineColor) || wavy.UL != p.ANSI[1] {
		t.Errorf("a red wavy underline came out as flags %b ul %06x", wavy.Flags, wavy.UL)
	}
	if c := f.CellAt(0, 2); c.UL != p.ANSI[3] {
		t.Errorf("the yellow one came out %06x", c.UL)
	}
	if c := f.CellAt(0, 4); c.Flags.Has(render.FlagUnderlineCurly) || !c.Flags.Has(render.FlagUnderlineColor) {
		t.Errorf("4 after a wavy one: flags %b; want straight, still coloured", c.Flags)
	}
}
