package theme

import (
	"sort"
	"strings"
	"testing"
)

// A theme nobody can read is worse than no theme: these check the properties
// that would make one unusable, not the individual colours, which are copied
// from their projects and are not ours to have an opinion about.

func TestEveryThemeIsUsable(t *testing.T) {
	for _, th := range all {
		if th.Name == "" {
			t.Fatal("a theme with no name can never be chosen")
		}
		if th.Name != normalize(th.Name) {
			t.Errorf("%q cannot be looked up: Get normalizes to %q", th.Name, normalize(th.Name))
		}
		if th.Foreground == th.Background {
			t.Errorf("%s: text the colour of the background is invisible", th.Name)
		}
		// Several published palettes reuse the same value for a colour and its
		// bright twin - Nord and Tokyo Night both do, for five and six of the
		// eight - so identical pairs are not an error. Black is the exception:
		// bright black is what programs use for "dimmed but still readable",
		// and if it equals black then dimmed means gone.
		if th.ANSI[8] == th.ANSI[0] {
			t.Errorf("%s: bright black is the same as black, so dimmed text disappears", th.Name)
		}
		for i, c := range th.ANSI {
			if c > 0xffffff {
				t.Errorf("%s: colour %d is %#x, which is not RGB", th.Name, i, c)
			}
		}
	}
}

func TestNamesAreUniqueAndOrdered(t *testing.T) {
	names := Names()
	if len(names) != len(all) {
		t.Fatalf("Names returned %d of %d themes", len(names), len(all))
	}
	if names[0] != Default().Name {
		t.Errorf("the default should be listed first, got %q then %q", names[0], Default().Name)
	}
	rest := append([]string(nil), names[1:]...)
	if !sort.StringsAreSorted(rest) {
		t.Errorf("the themes after the default should be alphabetical, got %v", rest)
	}
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			t.Errorf("two themes are both called %q", n)
		}
		seen[n] = true
	}
}

func TestGetForgivesTheSpelling(t *testing.T) {
	for _, spelling := range []string{"tokyo-night", "Tokyo-Night", "tokyo_night", "Tokyo Night", "  TOKYO-NIGHT "} {
		th, ok := Get(spelling)
		if !ok {
			t.Fatalf("Get(%q) found nothing", spelling)
		}
		if th.Name != "tokyo-night" {
			t.Fatalf("Get(%q) returned %q", spelling, th.Name)
		}
	}
}

func TestGetRejectsWhatIsNotThere(t *testing.T) {
	if _, ok := Get("monokai"); ok {
		t.Fatal("Get invented a theme")
	}
	if _, ok := Get(""); ok {
		t.Fatal("the empty name is not a theme")
	}
}

func TestDefaultIsNotBlack(t *testing.T) {
	// Not a matter of taste: the window chrome is drawn as shades of this
	// colour, and every shade of black is black.
	if Default().Background == 0x000000 {
		t.Fatal("the default background must have something to be a shade of")
	}
}

func TestEveryThemeHasAVisibleSelection(t *testing.T) {
	for _, th := range all {
		if th.Selection == th.Background {
			t.Errorf("%s: selected text would look exactly like unselected text", th.Name)
		}
		textOnSelection := th.Foreground
		if th.HasSelectionText {
			textOnSelection = th.SelectionText // Srcery's highlight is its text colour, on purpose
		}
		if th.Selection == textOnSelection {
			t.Errorf("%s: selected text would be invisible against its own highlight", th.Name)
		}
	}
}

// A theme file sets the colours it names and inherits the rest.
func TestParseThemeFile(t *testing.T) {
	src := `# catppuccin, near enough
background = #1e1e2e
foreground = cdd6f4   # without the hash
color1 = 0xf38ba8
`
	th, err := Parse("Mocha", strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if th.Name != "mocha" || th.Background != 0x1e1e2e || th.Foreground != 0xcdd6f4 || th.ANSI[1] != 0xf38ba8 {
		t.Fatalf("parsed %+v", th)
	}
	if th.ANSI[2] != Default().ANSI[2] || th.Selection != Default().Selection {
		t.Fatal("colours the file left out did not come from the default")
	}

	_, err = Parse("bad", strings.NewReader("background = blue\ncolor16 = #000000\nshade = #000000\n"))
	if err == nil || strings.Count(err.Error(), "line") != 3 {
		t.Fatalf("want all three bad lines reported, got %v", err)
	}
}

// The themes taken from Ghostty match it, selection text included.
func TestGhosttyThemes(t *testing.T) {
	for _, c := range []struct {
		name               string
		fg, bg, sel, selFG uint32
		red, brightWhite   uint32
	}{
		{"moonfly", 0xbdbdbd, 0x080808, 0xb2ceee, 0x080808, 0xff5454, 0xe4e4e4},
		{"srcery", 0xfce8c3, 0x1c1b19, 0xfce8c3, 0x1c1b19, 0xef2f27, 0xfce8c3},
	} {
		th, ok := Get(c.name)
		if !ok {
			t.Fatalf("no %s theme", c.name)
		}
		if th.Foreground != c.fg || th.Background != c.bg || th.Selection != c.sel ||
			!th.HasSelectionText || th.SelectionText != c.selFG ||
			th.ANSI[1] != c.red || th.ANSI[15] != c.brightWhite {
			t.Errorf("%s: %+v", c.name, th)
		}
	}
	if th, _ := Get("Moonfly"); th.Name != "moonfly" {
		t.Error("Moonfly with a capital was not found")
	}
}

func TestThemeFileSelectionText(t *testing.T) {
	th, err := Parse("mine", strings.NewReader("selection = #ffffff\nselection-text = #000000\n"))
	if err != nil || !th.HasSelectionText || th.SelectionText != 0 {
		t.Fatalf("selection-text: %+v, %v", th, err)
	}
}
