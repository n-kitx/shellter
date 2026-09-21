package theme

import (
	"sort"
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
		if th.Selection == th.Foreground {
			t.Errorf("%s: selected text would be invisible against its own highlight", th.Name)
		}
	}
}
