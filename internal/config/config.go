// Package config reads Shellter's settings file.
//
// One file, one syntax: "key = value", one per line, "#" starts a comment.
// That is deliberately less than a real configuration language offers. The
// package's job is to turn text into a Config struct and to say clearly what
// is wrong with a line it cannot use; it does not know what any setting means,
// which window it belongs to, or when it is applied.
//
// Precedence is decided by the caller, not here: defaults come from Default,
// this package layers the file on top, and main layers command-line flags on
// top of that.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thakurankit7/shellter/internal/screen"
	"github.com/thakurankit7/shellter/internal/theme"
)

// Config is every setting Shellter understands.
type Config struct {
	// Theme is the name of a built-in colour scheme. Kept as the theme itself
	// rather than a string because package theme depends on nothing, so
	// checking the name here costs no coupling and saves main a second lookup.
	Theme theme.Theme
	// TabPosition is "top" or "left". Kept as a string because package config
	// must not depend on the renderer; main turns it into a render.TabPosition
	// and reports a bad value the same way it reports a bad flag.
	TabPosition string
	// BackgroundOpacity is how solid the window is, from 0 (invisible) to 1
	// (opaque). Anything below 1 lets the desktop through.
	BackgroundOpacity float64
	// BackgroundBlur frosts whatever shows through. It does nothing at full
	// opacity.
	BackgroundBlur bool
	// WindowPadding is the gap in points between the window edge and the first
	// character cell.
	WindowPadding int
	// Scrollback is how many lines of history each pane keeps. Zero disables it.
	Scrollback int
	// FontSize is the terminal font's size in points.
	FontSize float64
	// FontFamily is the terminal font, by the name Font Book shows. A name
	// that is not installed falls back to Menlo when the window opens, rather
	// than failing here: fonts are the Mac's business, not the file's.
	FontFamily string
	// OptionAsMeta makes Option act as Meta (Alt) for the shell - Option+B is
	// "back a word" in zsh - instead of typing the character macOS gives it
	// (Option+B is "∫").
	OptionAsMeta bool
	// Bell is what a program ringing the bell does: "sound", "flash" (the
	// pane blinks), "both" or "off". A tab in the background is marked either
	// way, and the Dock icon bounces once if Shellter is not in front.
	Bell string
	// CopyOnSelect puts text on the clipboard as soon as it is selected, the
	// X11 way, rather than waiting for Cmd+C.
	CopyOnSelect bool

	// themesDir is where theme files are looked for: "themes" beside the
	// settings file.
	themesDir string
}

// Default is the configuration used when there is no file at all.
func Default() Config {
	return Config{
		Theme:             theme.Default(),
		TabPosition:       "top",
		BackgroundOpacity: 1,
		BackgroundBlur:    true,
		WindowPadding:     12,
		Scrollback:        screen.DefaultScrollback,
		FontSize:          14,
		FontFamily:        "Menlo",
		Bell:              "sound",
		themesDir:         themesBeside(Path()),
	}
}

// themesBeside is the themes directory for a settings file at path.
func themesBeside(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(path), "themes")
}

// ThemesDir is where this configuration looks for theme files.
func (c Config) ThemesDir() string { return c.themesDir }

// Path is where Shellter looks for the file: $XDG_CONFIG_HOME/shellter/config,
// or ~/.config/shellter/config when that is unset.
func Path() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "shellter", "config")
}

// Load reads Path on top of Default.
//
// A missing file is not an error - it is the normal case, and means "use the
// defaults". Anything else is reported, including the unreadable file that
// would otherwise look identical to a missing one.
func Load() (Config, error) {
	path := Path()
	if path == "" {
		return Default(), nil
	}
	cfg, err := LoadFrom(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	return cfg, err
}

// Create writes a settings file at path listing every setting at its default,
// commented out, unless a file is already there. It is what Settings... opens
// the first time: a blank file teaches nothing, and one that names every key
// and what it takes is most of the documentation.
func Create(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil // theirs, and not ours to touch
	}
	if err != nil {
		return err
	}
	d := Default()
	_, err = fmt.Fprintf(f, `# Shellter settings: "key = value", one per line. Remove the "#" to use one.
# Changes apply as soon as the file is saved.

# theme = %s    # %s, or a file in themes/
# font-family = %s
# font-size = %g
# option-as-meta = %t    # Option types Meta (Alt) for the shell, not accents
# copy-on-select = %t
# bell = %s    # sound, flash, both or off
# tab-position = %s    # top or left
# background-opacity = %g    # 0 to 1
# background-blur = %t
# window-padding = %d
# scrollback = %d
`, d.Theme.Name, strings.Join(theme.Names(), ", "), d.FontFamily, d.FontSize, d.OptionAsMeta, d.CopyOnSelect, d.Bell, d.TabPosition,
		d.BackgroundOpacity, d.BackgroundBlur, d.WindowPadding, d.Scrollback)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Parse layers a settings file over Default.
//
// It reads the whole file even after a bad line, so one typo reports one error
// rather than hiding the next twenty.
func Parse(r io.Reader) (Config, error) {
	return parseIn(r, themesBeside(Path()))
}

func parseIn(r io.Reader, themesDir string) (Config, error) {
	cfg := Default()
	cfg.themesDir = themesDir
	var problems []string

	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(stripComment(sc.Text()))
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			problems = append(problems, fmt.Sprintf("line %d: %q is not \"key = value\"", n, line))
			continue
		}
		if err := cfg.Set(strings.TrimSpace(key), strings.TrimSpace(value)); err != nil {
			problems = append(problems, fmt.Sprintf("line %d: %v", n, err))
		}
	}
	if err := sc.Err(); err != nil {
		return cfg, err
	}
	if len(problems) > 0 {
		return cfg, errors.New(strings.Join(problems, "\n"))
	}
	return cfg, nil
}

// Set applies one setting, given as the text on either side of the "=".
//
// It is exported because a command-line flag is the same thing as a line in
// the file - "background-opacity" is "background-opacity" either way - and one
// validator for both means a value can never be accepted from one and rejected
// by the other.
//
// Unknown keys are an error rather than a shrug: a misspelled setting that
// silently does nothing is the most annoying kind of configuration bug.
func (c *Config) Set(key, value string) error {
	switch key {
	case "theme":
		t, err := c.findTheme(value)
		if err != nil {
			return err
		}
		c.Theme = t
	case "font-family":
		if value == "" {
			return errors.New("font-family needs a font name, such as Menlo")
		}
		c.FontFamily = strings.Trim(value, `"`)
	case "option-as-meta":
		b, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("option-as-meta: %w", err)
		}
		c.OptionAsMeta = b
	case "copy-on-select":
		b, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("copy-on-select: %w", err)
		}
		c.CopyOnSelect = b
	case "bell":
		switch value {
		case "sound", "flash", "both", "off":
			c.Bell = value
		default:
			return fmt.Errorf("bell must be sound, flash, both or off, not %q", value)
		}
	case "tab-position":
		if value != "top" && value != "left" {
			return fmt.Errorf("tab-position must be top or left, not %q", value)
		}
		c.TabPosition = value
	case "background-opacity":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || f < 0 || f > 1 {
			return fmt.Errorf("background-opacity must be between 0 and 1, not %q", value)
		}
		c.BackgroundOpacity = f
	case "background-blur":
		b, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("background-blur: %w", err)
		}
		c.BackgroundBlur = b
	case "window-padding":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > 200 {
			return fmt.Errorf("window-padding must be between 0 and 200, not %q", value)
		}
		c.WindowPadding = n
	case "scrollback":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("scrollback must be zero or more, not %q", value)
		}
		c.Scrollback = n
	case "font-size":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || f < 4 || f > 72 {
			return fmt.Errorf("font-size must be between 4 and 72, not %q", value)
		}
		c.FontSize = f
	default:
		return fmt.Errorf("unknown setting %q", key)
	}
	return nil
}

// findTheme looks a theme up by name: a built-in one first, then a file of
// that name in the themes directory.
func (c Config) findTheme(name string) (theme.Theme, error) {
	if t, ok := theme.Get(name); ok {
		return t, nil
	}
	if c.themesDir != "" && !strings.ContainsAny(name, `/\`) {
		f, err := os.Open(filepath.Join(c.themesDir, name))
		if err == nil {
			defer f.Close()
			t, err := theme.Parse(name, f)
			if err != nil {
				return t, fmt.Errorf("theme %q: %w", name, err)
			}
			return t, nil
		}
	}
	where := ""
	if c.themesDir != "" {
		where = ", and there is no file of that name in " + c.themesDir
	}
	return theme.Theme{}, fmt.Errorf("unknown theme %q; the built-in ones are %s%s",
		name, strings.Join(theme.Names(), ", "), where)
}

// stripComment removes a trailing "# ..." unless the "#" is quoted. No setting
// needs a quoted "#" today; the check is here so adding one later does not
// quietly break.
func stripComment(line string) string {
	quoted := false
	for i, r := range line {
		switch {
		case r == '"':
			quoted = !quoted
		case r == '#' && !quoted:
			return line[:i]
		}
	}
	return line
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(v) {
	case "true", "yes", "on", "1":
		return true, nil
	case "false", "no", "off", "0":
		return false, nil
	}
	return false, fmt.Errorf("%q is not true or false", v)
}

// LoadFrom reads a specific file, with no "missing is fine" rule: if a path was
// named, failing to read it is an error.
func LoadFrom(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Default(), err
	}
	defer f.Close()

	cfg, err := parseIn(f, themesBeside(path))
	if err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}
