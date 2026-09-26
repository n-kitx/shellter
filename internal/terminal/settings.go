package terminal

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/thakurankit7/shellter/internal/config"
	"github.com/thakurankit7/shellter/internal/render"
)

// Settings is everything the settings file decides about a window, in the
// form the GUI uses it. main builds one from the file and the flags; the GUI
// takes it once at startup and again whenever the file changes.
type Settings struct {
	Palette         Palette
	FontFamily      string
	FontSize        float64
	Chrome          render.Chrome
	TabPosition     render.TabPosition
	ScrollbackLines int
	OptionAsMeta    bool
}

// Use takes settings before the window opens.
func (g *GUI) Use(s Settings) {
	g.Palette = s.Palette
	g.FontFamily = s.FontFamily
	g.FontSize = s.FontSize
	g.Chrome = s.Chrome
	g.TabPosition = s.TabPosition
	g.ScrollbackLines = s.ScrollbackLines
	g.OptionAsMeta = s.OptionAsMeta
}

// apply takes settings while the window is open, changing only what changed.
// Main thread only: it touches the window.
func (g *GUI) apply(s Settings) {
	if s.Chrome.Opacity <= 0 || s.Chrome.Opacity > 1 {
		s.Chrome.Opacity = 1
	}
	g.Palette = s.Palette
	g.Chrome = s.Chrome
	render.SetChrome(g.chrome())

	// A new font or size resets any zoom: the file is the newer word.
	if s.FontFamily != g.FontFamily || s.FontSize != g.FontSize {
		g.FontFamily, g.FontSize = s.FontFamily, s.FontSize
		render.SetFont(s.FontFamily, s.FontSize)
	}
	if s.OptionAsMeta != g.OptionAsMeta {
		g.OptionAsMeta = s.OptionAsMeta
		render.SetOptionAsMeta(s.OptionAsMeta)
	}
	if s.ScrollbackLines != g.ScrollbackLines {
		g.ScrollbackLines = s.ScrollbackLines
		for _, p := range g.allPanes() {
			p.mu.Lock()
			p.scr.SetScrollback(s.ScrollbackLines)
			p.mu.Unlock()
		}
	}
	if s.TabPosition != g.TabPosition {
		g.moveTabBar(s.TabPosition)
	}
	g.syncTabs()
	render.Redraw()
}

// watchInterval is how often the settings file is checked. A second is quick
// enough to feel immediate after saving, and a stat a second costs nothing.
const watchInterval = time.Second

// watchSettings re-reads the settings whenever the file, or a theme file
// beside it, changes, and applies them. It runs until stop is closed.
//
// It polls rather than asking the kernel to be told: editors save by writing
// a new file and renaming it over the old one, which a watch on the old file
// never hears about, and polling one small directory is simpler than
// following that dance.
func (g *GUI) watchSettings(stop <-chan struct{}) {
	if g.Reload == nil {
		return
	}
	last := g.settingsStamp()
	tick := time.NewTicker(watchInterval)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		stamp := g.settingsStamp()
		if stamp == last {
			continue
		}
		last = stamp
		s, err := g.Reload()
		render.Post(func() {
			if err != nil {
				// Keep what is running, and say why the change did not take.
				g.ask("Shellter could not use its settings file.", err.Error(), "OK")
				return
			}
			g.apply(s)
		})
	}
}

// settingsStamp is a fingerprint of the settings file and the theme files:
// their names, sizes and modification times. Any save changes it.
func (g *GUI) settingsStamp() string {
	path := g.ConfigPath
	if path == "" {
		path = config.Path()
	}
	stamp := ""
	add := func(p string) {
		if info, err := os.Stat(p); err == nil {
			stamp += fmt.Sprintf("%s:%d:%d;", p, info.Size(), info.ModTime().UnixNano())
		}
	}
	add(path)
	if g.ThemesDir != "" {
		entries, _ := os.ReadDir(g.ThemesDir)
		for _, e := range entries {
			add(filepath.Join(g.ThemesDir, e.Name()))
		}
	}
	return stamp
}
