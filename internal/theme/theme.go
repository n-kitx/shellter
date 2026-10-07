// Package theme holds the colours a terminal draws with.
//
// It is deliberately the most boring package in Shellter: a name, sixteen
// colours, a foreground and a background, and no behaviour at all. That is what
// lets both ends of the program depend on it - package config, to check that a
// name in the settings file exists, and package terminal, to turn one of these
// into the Palette it resolves cells against - without either depending on the
// other.
//
// What a theme does NOT contain is as deliberate: no font, no padding, no
// opacity. Those are settings about the window; a theme is only about colour.
package theme

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// A Theme is a complete set of terminal colours.
//
// Foreground and Background are what "the default colour" means when a program
// has not asked for anything else. ANSI holds the sixteen named colours in the
// order every terminal agrees on: 0-7 normal, 8-15 bright. Everything else a
// program can ask for - the 6x6x6 cube, the grey ramp, direct RGB - is
// computed, not stored, so there is nothing here that could disagree with it.
type Theme struct {
	Name       string
	Foreground uint32
	Background uint32
	// Selection is the background behind marked text. It is part of a theme
	// rather than computed because a blend that reads well on one palette
	// reads as "slightly dirty background" on another, and selection is the
	// one highlight that has to be unmistakable.
	Selection uint32
	// SelectionText, when HasSelectionText is set, is the colour of marked
	// text, whatever colour it was. A theme whose selection is light behind
	// light text (Moonfly's) needs it to stay readable; the rest leave text
	// its own colour.
	SelectionText    uint32
	HasSelectionText bool
	ANSI             [16]uint32
}

// Default is the theme used when the settings file says nothing.
//
// It is a dark grey rather than pure black on purpose. Black is not a colour a
// screen shows so much as one it fails to show: the window stops looking like a
// window and starts looking like a hole cut in the desktop, and the title bar
// above it has nothing to be a shade of.
func Default() Theme { return all[0] }

// Get finds a theme by name, forgiving the spelling: case does not matter, and
// spaces and underscores count as hyphens, so "Tokyo Night", "tokyo_night" and
// "tokyo-night" are the same theme.
func Get(name string) (Theme, bool) {
	want := normalize(name)
	for _, t := range all {
		if t.Name == want {
			return t, true
		}
	}
	return Theme{}, false
}

// Names lists every theme, in the order they are defined: the default first,
// then the rest alphabetically. Used for -h and for the error a bad name gets.
func Names() []string {
	out := make([]string, len(all))
	for i, t := range all {
		out[i] = t.Name
	}
	return out
}

func normalize(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, " ", "-")
	return name
}

// all is every built-in theme. The default comes first because Default returns
// all[0]; the rest are alphabetical so that -h reads like a list.
//
// The palettes after the first are the published ones from the projects they
// are named after, copied rather than approximated: a theme that is nearly Nord
// is worse than no Nord at all, because the thing people recognise is the exact
// colour.
var all = []Theme{
	{
		Name:       "shellter",
		Selection:  0x2f4a6d,
		Foreground: 0xd4d4d4,
		Background: 0x17191e,
		ANSI: [16]uint32{
			0x1e2027, 0xcd3131, 0x0dbc79, 0xe5e510,
			0x2472c8, 0xbc3fbc, 0x11a8cd, 0xd4d4d4,
			0x5a5f6e, 0xf14c4c, 0x23d18b, 0xf5f543,
			0x3b8eea, 0xd670d6, 0x29b8db, 0xffffff,
		},
	},
	{
		Name:       "dracula",
		Selection:  0x44475a,
		Foreground: 0xf8f8f2,
		Background: 0x282a36,
		ANSI: [16]uint32{
			0x21222c, 0xff5555, 0x50fa7b, 0xf1fa8c,
			0xbd93f9, 0xff79c6, 0x8be9fd, 0xf8f8f2,
			0x6272a4, 0xff6e6e, 0x69ff94, 0xffffa5,
			0xd6acff, 0xff92df, 0xa4ffff, 0xffffff,
		},
	},
	{
		Name:       "github-light",
		Selection:  0xbcd7ff,
		Foreground: 0x24292e,
		Background: 0xffffff,
		ANSI: [16]uint32{
			0x24292e, 0xd73a49, 0x22863a, 0xb08800,
			0x0366d6, 0x6f42c1, 0x1b7c83, 0x6a737d,
			0x959da5, 0xcb2431, 0x28a745, 0xdbab09,
			0x2188ff, 0x8a63d2, 0x3192aa, 0xd1d5da,
		},
	},
	{
		Name:       "gruvbox-dark",
		Selection:  0x504945,
		Foreground: 0xebdbb2,
		Background: 0x282828,
		ANSI: [16]uint32{
			0x282828, 0xcc241d, 0x98971a, 0xd79921,
			0x458588, 0xb16286, 0x689d6a, 0xa89984,
			0x928374, 0xfb4934, 0xb8bb26, 0xfabd2f,
			0x83a598, 0xd3869b, 0x8ec07c, 0xebdbb2,
		},
	},
	{
		// Moonfly, by Dennis B (bluz71/vim-moonfly-colors), as Ghostty ships it.
		Name:             "moonfly",
		Selection:        0xb2ceee,
		SelectionText:    0x080808,
		HasSelectionText: true,
		Foreground:       0xbdbdbd,
		Background:       0x080808,
		ANSI: [16]uint32{
			0x323437, 0xff5454, 0x8cc85f, 0xe3c78a,
			0x80a0ff, 0xcf87e8, 0x79dac8, 0xc6c6c6,
			0x949494, 0xff5189, 0x36c692, 0xc6c684,
			0x74b2ff, 0xae81ff, 0x85dc85, 0xe4e4e4,
		},
	},
	{
		Name:       "nord",
		Selection:  0x434c5e,
		Foreground: 0xd8dee9,
		Background: 0x2e3440,
		ANSI: [16]uint32{
			0x3b4252, 0xbf616a, 0xa3be8c, 0xebcb8b,
			0x81a1c1, 0xb48ead, 0x88c0d0, 0xe5e9f0,
			0x4c566a, 0xbf616a, 0xa3be8c, 0xebcb8b,
			0x81a1c1, 0xb48ead, 0x8fbcbb, 0xeceff4,
		},
	},
	{
		Name:       "solarized-dark",
		Selection:  0x073642,
		Foreground: 0x93a1a1,
		Background: 0x002b36,
		ANSI: [16]uint32{
			0x073642, 0xdc322f, 0x859900, 0xb58900,
			0x268bd2, 0xd33682, 0x2aa198, 0xeee8d5,
			0x586e75, 0xcb4b16, 0x93a1a1, 0x839496,
			0x657b83, 0x6c71c4, 0x93a1a1, 0xfdf6e3,
		},
	},
	{
		Name:       "solarized-light",
		Selection:  0xeee8d5,
		Foreground: 0x586e75,
		Background: 0xfdf6e3,
		ANSI: [16]uint32{
			0x073642, 0xdc322f, 0x859900, 0xb58900,
			0x268bd2, 0xd33682, 0x2aa198, 0xeee8d5,
			0x586e75, 0xcb4b16, 0x93a1a1, 0x839496,
			0x657b83, 0x6c71c4, 0x93a1a1, 0xfdf6e3,
		},
	},
	{
		// Srcery (srcery-colors), as Ghostty ships it.
		Name:             "srcery",
		Selection:        0xfce8c3,
		SelectionText:    0x1c1b19,
		HasSelectionText: true,
		Foreground:       0xfce8c3,
		Background:       0x1c1b19,
		ANSI: [16]uint32{
			0x1c1b19, 0xef2f27, 0x519f50, 0xfbb829,
			0x2c78bf, 0xe02c6d, 0x0aaeb3, 0xbaa67f,
			0x918175, 0xf75341, 0x98bc37, 0xfed06e,
			0x68a8e4, 0xff5c8f, 0x2be4d0, 0xfce8c3,
		},
	},
	{
		Name:       "tokyo-night",
		Selection:  0x33467c,
		Foreground: 0xc0caf5,
		Background: 0x1a1b26,
		ANSI: [16]uint32{
			0x15161e, 0xf7768e, 0x9ece6a, 0xe0af68,
			0x7aa2f7, 0xbb9af7, 0x7dcfff, 0xa9b1d6,
			0x414868, 0xf7768e, 0x9ece6a, 0xe0af68,
			0x7aa2f7, 0xbb9af7, 0x7dcfff, 0xc0caf5,
		},
	},
}

// Parse reads a theme file: "key = value" lines, "#" comments, colours as
// "#rrggbb". The keys are background, foreground, selection and color0 to
// color15; anything left out comes from the default theme, so a file can be as
// short as the two colours someone cares about.
//
// It is the only way to have colours that are not built in. The format is the
// settings file's on purpose: one syntax for everything a user edits.
func Parse(name string, r io.Reader) (Theme, error) {
	t := Default()
	t.Name = normalize(name)
	sc := bufio.NewScanner(r)
	var problems []string
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			problems = append(problems, fmt.Sprintf("line %d: %q is not \"key = value\"", n, line))
			continue
		}
		// A colour is one word, and it may itself start with "#", so a
		// trailing comment is whatever follows that word.
		key = strings.ToLower(strings.TrimSpace(key))
		if fields := strings.Fields(value); len(fields) > 0 {
			value = fields[0]
		}
		rgb, err := parseColor(value)
		if err != nil {
			problems = append(problems, fmt.Sprintf("line %d: %s: %v", n, key, err))
			continue
		}
		switch {
		case key == "background":
			t.Background = rgb
		case key == "foreground":
			t.Foreground = rgb
		case key == "selection":
			t.Selection = rgb
		case key == "selection-text":
			t.SelectionText, t.HasSelectionText = rgb, true
		case strings.HasPrefix(key, "color"):
			i, err := strconv.Atoi(strings.TrimPrefix(key, "color"))
			if err != nil || i < 0 || i > 15 {
				problems = append(problems, fmt.Sprintf("line %d: %q is not color0 to color15", n, key))
				continue
			}
			t.ANSI[i] = rgb
		default:
			problems = append(problems, fmt.Sprintf("line %d: unknown key %q", n, key))
		}
	}
	if err := sc.Err(); err != nil {
		return t, err
	}
	if len(problems) > 0 {
		return t, errors.New(strings.Join(problems, "\n"))
	}
	return t, nil
}

// parseColor reads "#rrggbb", "rrggbb" or "0xrrggbb".
func parseColor(s string) (uint32, error) {
	hex := strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(s), "#"), "0x")
	if len(hex) != 6 {
		return 0, fmt.Errorf("%q is not a colour like #1e1e2e", s)
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("%q is not a colour like #1e1e2e", s)
	}
	return uint32(v), nil
}
