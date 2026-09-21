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
	}
}

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

// Parse layers a settings file over Default.
//
// It reads the whole file even after a bad line, so one typo reports one error
// rather than hiding the next twenty.
func Parse(r io.Reader) (Config, error) {
	cfg := Default()
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
		t, ok := theme.Get(value)
		if !ok {
			return fmt.Errorf("unknown theme %q; the built-in ones are %s",
				value, strings.Join(theme.Names(), ", "))
		}
		c.Theme = t
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

	cfg, err := Parse(f)
	if err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}
