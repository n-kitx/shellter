package config

import (
	"strings"
	"testing"

	"github.com/thakurankit7/shellter/internal/theme"
)

func parse(t *testing.T, text string) Config {
	t.Helper()
	cfg, err := Parse(strings.NewReader(text))
	if err != nil {
		t.Fatalf("Parse(%q): %v", text, err)
	}
	return cfg
}

func TestAnEmptyFileLeavesTheDefaults(t *testing.T) {
	if got, want := parse(t, ""), Default(); got != want {
		t.Errorf("got %+v, want the defaults %+v", got, want)
	}
}

func TestEverySettingCanBeSet(t *testing.T) {
	cfg := parse(t, `
theme = nord
tab-position = left
background-opacity = 0.85
background-blur = off
window-padding = 20
scrollback = 5000
font-size = 16.5
`)
	nord, ok := theme.Get("nord")
	if !ok {
		t.Fatal("no nord theme to test with")
	}
	want := Config{
		Theme:             nord,
		TabPosition:       "left",
		BackgroundOpacity: 0.85,
		BackgroundBlur:    false,
		WindowPadding:     20,
		Scrollback:        5000,
		FontSize:          16.5,
	}
	if cfg != want {
		t.Errorf("got %+v, want %+v", cfg, want)
	}
}

func TestUnsetSettingsKeepTheirDefault(t *testing.T) {
	cfg := parse(t, "font-size = 20")
	if cfg.FontSize != 20 {
		t.Errorf("font-size: got %v, want 20", cfg.FontSize)
	}
	if cfg.TabPosition != Default().TabPosition {
		t.Errorf("tab-position: got %q, want the default %q", cfg.TabPosition, Default().TabPosition)
	}
}

func TestCommentsAndBlankLinesAreIgnored(t *testing.T) {
	cfg := parse(t, `
# where the tabs go
tab-position = left   # trailing comments too

   # indented
`)
	if cfg.TabPosition != "left" {
		t.Errorf("got %q, want left", cfg.TabPosition)
	}
}

func TestSpacesAroundTheEqualsAreOptional(t *testing.T) {
	if cfg := parse(t, "scrollback=42"); cfg.Scrollback != 42 {
		t.Errorf("got %d, want 42", cfg.Scrollback)
	}
}

func TestBadValuesAreRejected(t *testing.T) {
	for _, line := range []string{
		"tab-position = bottom",
		"background-opacity = 2",
		"background-opacity = -0.5",
		"background-opacity = quite",
		"background-blur = sometimes",
		"window-padding = -1",
		"window-padding = huge",
		"scrollback = -1",
		"font-size = 0",
		"font-size = 200",
		"colour-scheme = dracula", // a real Ghostty setting, not one of ours
		"tab-position left",
	} {
		if _, err := Parse(strings.NewReader(line)); err == nil {
			t.Errorf("%q was accepted", line)
		}
	}
}

// A file with one typo in it should still say what else is wrong, and the
// settings that did parse should still be applied.
func TestEveryBadLineIsReported(t *testing.T) {
	cfg, err := Parse(strings.NewReader("font-size = 18\nnonsense = 1\nwindow-padding = -3\n"))
	if err == nil {
		t.Fatal("two bad lines were accepted")
	}
	if n := strings.Count(err.Error(), "line "); n != 2 {
		t.Errorf("got %d problems, want 2:\n%v", n, err)
	}
	if cfg.FontSize != 18 {
		t.Errorf("the good line was dropped: font-size is %v", cfg.FontSize)
	}
}

func TestErrorsNameTheLine(t *testing.T) {
	_, err := Parse(strings.NewReader("# comment\n\nscrollback = -5\n"))
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("got %v, want it to mention line 3", err)
	}
}

func TestPathIsUnderTheConfigDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/somewhere")
	if got, want := Path(), "/tmp/somewhere/shellter/config"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAMissingFileIsNotAnError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with no file: %v", err)
	}
	if cfg != Default() {
		t.Errorf("got %+v, want the defaults", cfg)
	}
}

func TestAnUnknownThemeNamesTheRealOnes(t *testing.T) {
	_, err := Parse(strings.NewReader("theme = monokai"))
	if err == nil {
		t.Fatal("a theme that does not exist was accepted")
	}
	// The list is the whole point of the message: a typo should not send
	// anyone to the source to find out what they were allowed to say.
	for _, name := range theme.Names() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error does not mention %q: %v", name, err)
		}
	}
}

func TestThemeNamesAreForgiving(t *testing.T) {
	cfg := parse(t, "theme = Tokyo Night")
	if cfg.Theme.Name != "tokyo-night" {
		t.Errorf("got %q, want tokyo-night", cfg.Theme.Name)
	}
}
