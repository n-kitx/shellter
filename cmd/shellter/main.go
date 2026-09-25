// Command shellter runs a zsh session on a pseudoterminal.
//
// Without arguments it runs inside the terminal it was launched from, writing
// the shell's bytes straight back out. With -gui it opens a macOS window and
// draws the terminal itself - which is also what it does when it was started
// as Shellter.app, where there is no terminal to run inside.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/thakurankit7/shellter/internal/config"
	"github.com/thakurankit7/shellter/internal/render"
	"github.com/thakurankit7/shellter/internal/terminal"
	"github.com/thakurankit7/shellter/internal/theme"
)

func init() {
	// AppKit's event loop must run on the process's first thread. Locking here,
	// before any other goroutine starts, is the only way to guarantee that the
	// main goroutine stays on it.
	runtime.LockOSThread()
}

func main() {
	code, err := run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "shellter: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

// run returns the shell's exit status, or an error if Shellter itself could
// not start.
func run() (int, error) {
	gui := flag.Bool("gui", bundled(), "open a macOS window instead of running inside this terminal")
	path := flag.String("config", "", "settings file (default "+config.Path()+")")

	// Every other flag is named after a setting in the file, and is validated
	// by the same code. The defaults shown in -h are the built-in ones; what a
	// flag actually overrides is whatever the file left in place.
	d := config.Default()
	flag.String("theme", d.Theme.Name, "colour scheme: "+strings.Join(theme.Names(), ", "))
	flag.String("tab-position", d.TabPosition, "where the tab bar sits: top or left")
	flag.Float64("background-opacity", d.BackgroundOpacity, "window opacity, 0 to 1")
	flag.Bool("background-blur", d.BackgroundBlur, "frost whatever shows through a translucent window")
	flag.Int("window-padding", d.WindowPadding, "gap in points between the window edge and the text")
	flag.Int("scrollback", d.Scrollback, "lines of history to keep (0 disables)")
	flag.Float64("font-size", d.FontSize, "terminal font size in points")
	flag.Parse()

	cfg, err := settings(*path)
	if err != nil {
		return -1, err
	}

	// Checked here rather than in package config, which must not depend on the
	// renderer. Everything else config accepted is already in range.
	position, ok := render.ParseTabPosition(cfg.TabPosition)
	if !ok {
		return -1, fmt.Errorf("tab-position must be top or left, not %q", cfg.TabPosition)
	}

	runner := terminal.New().Run
	if *gui {
		w := terminal.NewGUI()
		w.Palette = terminal.PaletteFor(cfg.Theme)
		w.ScrollbackLines = cfg.Scrollback
		w.TabPosition = position
		w.FontSize = cfg.FontSize
		w.ConfigPath = *path
		w.Chrome = render.Chrome{
			Opacity: cfg.BackgroundOpacity,
			Blur:    cfg.BackgroundBlur,
			Padding: cfg.WindowPadding,
		}
		runner = w.Run
	}

	return runner()
}

// bundled reports whether we were started as Shellter.app rather than as a
// binary on someone's PATH. It decides the default for -gui, because an app
// launched from the Dock has no terminal to run inside: it has no stdin to
// read and no stdout anyone would see, so it would sit there doing nothing
// while the Dock bounced.
//
// The test is the bundle's own shape - "Shellter.app/Contents/MacOS/shellter"
// - which is fixed by macOS and is the one thing that is true of every bundle
// and of no ordinary build.
func bundled() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.Contains(exe, ".app/Contents/MacOS/")
}

// settings layers the configuration file over the defaults, then the flags the
// user actually typed over that.
func settings(path string) (config.Config, error) {
	cfg, err := config.Load()
	if path != "" {
		cfg, err = config.LoadFrom(path)
	}
	if err != nil {
		return cfg, err
	}

	var bad error
	// Visit, not VisitAll: it reports only the flags that were given, so an
	// untouched flag never overwrites what the file said.
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "gui", "config":
			return // not settings
		}
		if err := cfg.Set(f.Name, f.Value.String()); err != nil && bad == nil {
			bad = fmt.Errorf("-%s: %w", f.Name, err)
		}
	})
	return cfg, bad
}
