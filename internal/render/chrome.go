package render

// The window's chrome: the parts that are not the terminal grid.
//
// Phase 11 adds the tab bar, and it is deliberately not part of Frame. A Frame
// is pulled sixty times a second because the grid changes constantly; tabs
// change when someone opens or closes one, which is rare. So tabs are *pushed*
// with SetTabs and the window keeps them until told otherwise.
//
//	grid   ── pulled ──►  Frame     (every redraw)
//	tabs   ── pushed ──►  SetTabs   (only when they change)

// TabPosition is where the tab bar sits. The user chooses; nothing here has an
// opinion beyond the default.
type TabPosition int

const (
	// TabsTop is a horizontal bar above the terminal, as in most browsers.
	TabsTop TabPosition = iota
	// TabsLeft is a vertical list beside it, which suits long titles and tall
	// windows - a terminal's titles are paths, and paths are long.
	TabsLeft
)

func (p TabPosition) String() string {
	if p == TabsLeft {
		return "left"
	}
	return "top"
}

// ParseTabPosition reads the name a user would type or pick from a menu.
func ParseTabPosition(s string) (TabPosition, bool) {
	switch s {
	case "top":
		return TabsTop, true
	case "left":
		return TabsLeft, true
	}
	return TabsTop, false
}

// Tab is what the bar needs to know about one session: what to call it, and
// whether it wants attention. Everything else about a tab - its shell, its
// screen, its scrollback - belongs to the application layer.
type Tab struct {
	Title string
	// Activity marks a tab that has printed something since it was last
	// looked at. Drawn as a dot, so a build finishing in a background tab is
	// visible without switching to it.
	//
	// It is not "is a command running": a terminal cannot know that without
	// tracking the foreground process group, which is a different feature.
	Activity bool
	// Directory says the title names a directory rather than a program: the
	// tab is a shell waiting at a prompt, and the bar marks it with a ">_".
	// A pane running vim is titled by vim, and a prompt mark there would lie.
	Directory bool
}

// Chrome is how the window itself looks, as opposed to what the terminal puts
// inside it. The values come from the configuration file; this package only
// has to draw them.
type Chrome struct {
	// Opacity is how solid the window is, from 0 to 1. Below 1 the desktop
	// shows through the terminal's default background - and only that: text and
	// cells with a colour of their own stay solid, or output would turn milky.
	Opacity float64
	// Blur frosts whatever shows through. It does nothing at full opacity.
	Blur bool
	// Padding is the gap in points between the window edge and the first cell.
	Padding int
	// Background is the terminal's own default background, as 0xRRGGBB. The
	// chrome is drawn in it and in shades of it, so the title bar and the grid
	// are one surface rather than two that nearly match.
	Background uint32
}

// DefaultChrome is what a window looks like with no configuration at all.
func DefaultChrome() Chrome {
	return Chrome{Opacity: 1, Blur: true, Padding: 12}
}

// SplitDirection says which way a pane is divided.
type SplitDirection int

const (
	// SplitBeside puts the new pane to the right of the old one.
	SplitBeside SplitDirection = iota
	// SplitAbove puts the new pane below the old one. The name is the
	// relationship between the two children, not where the new one lands.
	SplitAbove
)

// Direction is which way the focus moves between panes.
type Direction int

const (
	DirLeft Direction = iota
	DirRight
	DirUp
	DirDown
)
