package terminal

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thakurankit7/shellter/internal/render"
	"github.com/thakurankit7/shellter/internal/theme"
)

// Saving the settings file, or adding or editing a theme file, changes the
// fingerprint the watcher compares; doing nothing does not.
func TestSettingsStampSeesSaves(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	themes := filepath.Join(dir, "themes")
	os.MkdirAll(themes, 0o755)
	os.WriteFile(cfg, []byte("theme = nord\n"), 0o644)

	g := newTestGUI(5, 10)
	g.ConfigPath, g.ThemesDir = cfg, themes

	before := g.settingsStamp()
	if g.settingsStamp() != before {
		t.Fatal("the stamp changed with nothing saved")
	}

	os.WriteFile(cfg, []byte("theme = dracula\n"), 0o644)
	os.Chtimes(cfg, time.Now().Add(time.Second), time.Now().Add(time.Second))
	afterSave := g.settingsStamp()
	if afterSave == before {
		t.Fatal("saving the settings file did not change the stamp")
	}

	os.WriteFile(filepath.Join(themes, "mine"), []byte("background = #000000\n"), 0o644)
	if g.settingsStamp() == afterSave {
		t.Fatal("adding a theme file did not change the stamp")
	}
}

// Applying new settings changes the palette the panes are drawn with and the
// history every pane keeps - including panes that already exist.
func TestApplyChangesRunningPanes(t *testing.T) {
	g := newTestGUI(5, 10)
	g.addTab(5, 10)
	nord, _ := theme.Get("nord")

	s := Settings{
		Palette:         PaletteFor(nord),
		FontFamily:      g.FontFamily,
		FontSize:        g.FontSize,
		TabPosition:     g.TabPosition,
		ScrollbackLines: 42,
		Chrome:          render.Chrome{Opacity: 1, Padding: 12},
	}
	g.apply(s)

	if g.Palette.Background != nord.Background {
		t.Fatal("the palette was not replaced")
	}
	for i, p := range g.allPanes() {
		if got := p.scr.ScrollbackCap(); got != 42 {
			t.Fatalf("pane %d keeps %d lines of history, want 42", i, got)
		}
	}
}

// With a theme for each appearance, the palette follows macOS; with one
// theme, a switch changes nothing.
func TestAdaptiveTheme(t *testing.T) {
	light, dark := PaletteFor(mustTheme(t, "solarized-light")), PaletteFor(mustTheme(t, "nord"))
	g := newTestGUI(5, 20)
	g.apply(Settings{Palette: light, DarkPalette: dark, Adaptive: true, LineHeight: g.LineHeight})

	g.appearance(true)
	if g.Palette != dark {
		t.Fatal("dark mode did not bring the dark theme")
	}
	g.appearance(false)
	if g.Palette != light {
		t.Fatal("light mode did not bring the light theme back")
	}
	g.appearance(true)
	g.apply(Settings{Palette: light, LineHeight: g.LineHeight}) // one theme now
	if g.Palette != light {
		t.Fatal("a single theme was not used in dark mode")
	}
	g.appearance(false)
	g.appearance(true)
	if g.Palette != light {
		t.Fatal("a single theme changed with the system")
	}
}

func mustTheme(t *testing.T, name string) theme.Theme {
	t.Helper()
	th, ok := theme.Get(name)
	if !ok {
		t.Fatalf("no %s theme", name)
	}
	return th
}
