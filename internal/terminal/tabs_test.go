package terminal

import (
	"os"
	"strings"
	"testing"

	"github.com/thakurankit7/shellter/internal/parser"

	"github.com/thakurankit7/shellter/internal/input"
	"github.com/thakurankit7/shellter/internal/render"
)

// Phase 11: the window holds several sessions. These drive the tab list
// directly - there is no window and no shell, which is exactly the point: the
// bookkeeping is separable from both.

func TestTabsStartWithOne(t *testing.T) {
	g := newTestGUI(24, 80)
	if got := len(g.tabs); got != 1 {
		t.Fatalf("got %d tabs, want 1", got)
	}
	if got := g.tabViews(); len(got) != 1 {
		t.Fatalf("bar shows %d tabs, want 1", len(got))
	}
}

// Closing the tab you are on should land you on its neighbour to the left, not
// throw you to the end of the list.
func TestClosingATabLandsOnTheNeighbour(t *testing.T) {
	g := newTestGUI(10, 20)
	second := g.addTab(10, 20)
	third := g.addTab(10, 20)
	g.active = 2

	g.forgetTab(third)
	if g.active != 1 || g.currentTab() != second {
		t.Fatalf("active = %d, want the tab to the left", g.active)
	}

	g.forgetTab(second)
	if g.active != 0 {
		t.Fatalf("active = %d, want the first tab", g.active)
	}
}

// Closing a tab *behind* the active one must not change which tab you are
// looking at, only its index.
func TestClosingAnEarlierTabKeepsYouWhereYouAre(t *testing.T) {
	g := newTestGUI(10, 20)
	first := g.tabs[0]
	second := g.addTab(10, 20)
	g.active = 1

	g.forgetTab(first)
	if g.currentTab() != second {
		t.Fatal("closing an earlier tab moved the view to a different one")
	}
	if g.active != 0 {
		t.Fatalf("active = %d, want the index shifted down to 0", g.active)
	}
}

func TestSelectingATabClearsItsActivityDot(t *testing.T) {
	g := newTestGUI(10, 20)
	second := g.addTab(10, 20)

	// Output arrives in the background tab.
	g.panePrinted(second, second.sole())
	if !second.activity {
		t.Fatal("a background tab that printed should be marked")
	}

	g.selectTab(1)
	if second.activity {
		t.Fatal("looking at a tab should clear its mark")
	}
}

// The visible tab never lights the dot: you are already looking at it.
func TestTheVisibleTabNeverLightsTheDot(t *testing.T) {
	g := newTestGUI(10, 20)
	g.panePrinted(g.currentTab(), g.current())
	if g.currentTab().activity {
		t.Fatal("the tab in front should not be marked as having activity")
	}
}

// Every tab is kept at the window's size, so switching to one that was in the
// background does not show a grid of the wrong shape.
func TestResizeAppliesToEveryTab(t *testing.T) {
	g := newTestGUI(10, 20)
	background := g.addTab(10, 20)

	g.resize(30, 100)

	for i, tb := range g.tabs {
		rows, cols := tb.sole().scr.Size()
		if rows != 30 || cols != 100 {
			t.Fatalf("tab %d is %dx%d, want 30x100", i, rows, cols)
		}
	}
	if rows, cols := background.sole().scr.Size(); rows != 30 || cols != 100 {
		t.Fatalf("the background tab is %dx%d", rows, cols)
	}
}

// Keys go to the tab in front, and to no other.
func TestKeysGoToTheActiveTab(t *testing.T) {
	g := newTestGUI(5, 10)
	g.addTab(5, 10)
	g.active = 1
	out := g.sink()

	g.key(input.Key{Chars: []rune{'x'}, Base: 'x'})
	if got := out.String(); got != "x" {
		t.Fatalf("wrote %q, want the keystroke", got)
	}
}

// Cmd+2 selects the second tab rather than typing a 2.
func TestCommandDigitSelectsATab(t *testing.T) {
	g := newTestGUI(5, 10)
	g.addTab(5, 10)
	out := g.sink()

	if !g.tabKey(input.Key{Chars: []rune{'2'}, Base: '2', Mods: input.ModCommand}) {
		t.Fatal("Cmd+2 should have been consumed")
	}
	if g.active != 1 {
		t.Fatalf("active = %d, want the second tab", g.active)
	}

	// Without Cmd it is just a digit, and it reaches the shell.
	g.key(input.Key{Chars: []rune{'2'}, Base: '2'})
	if got := out.String(); got != "2" {
		t.Fatalf("wrote %q, want a plain 2 to be typed", got)
	}
}

func TestCommandDigitOutOfRangeIsHarmless(t *testing.T) {
	g := newTestGUI(5, 10)
	if !g.tabKey(input.Key{Chars: []rune{'9'}, Base: '9', Mods: input.ModCommand}) {
		t.Fatal("Cmd+9 should be consumed even with no ninth tab")
	}
	if g.active != 0 {
		t.Fatalf("active = %d, want to stay on the only tab", g.active)
	}
}

// Each tab has its own scrollback, which is the whole reason they are separate
// screens rather than one screen with several cursors.
func TestTabsKeepSeparateHistory(t *testing.T) {
	g := newTestGUI(3, 10)
	other := g.addTab(3, 10)

	g.feed("first tab\r\n\r\n\r\n\r\n")
	if g.screen().ScrollbackLen() == 0 {
		t.Fatal("the first tab should have history")
	}
	if other.sole().scr.ScrollbackLen() != 0 {
		t.Fatal("the second tab picked up the first tab's history")
	}
}

// The bar's labels come from what the shell set as a title, trimmed to
// something that fits: a path's last component, not the whole path. Until the
// shell says anything, the tab is named after the program itself.
func TestTabLabels(t *testing.T) {
	cases := []struct {
		title string
		want  string
		dir   bool
	}{
		{"", "zsh", false},
		{"   ", "zsh", false},
		{"ankitthakur@Ankits-MacBook-Pro:~/PERSONAL/Shellter", "Shellter", true},
		{"~/PERSONAL/Shellter", "Shellter", true},
		{"/usr/local/lib", "lib", true},
		{"~", "~", true},
		{"/", "/", true},
		// Programs name themselves, and a prompt mark in front of that would
		// lie - even when the name has a path inside it.
		{"vim", "vim", false},
		{"main.go (~/src) - VIM", "main.go (~/src) - VIM", false},
		{"less", "less", false},
	}
	for _, c := range cases {
		got, dir := label(c.title, "zsh")
		if got != c.want || dir != c.dir {
			t.Fatalf("label(%q) = %q, %v, want %q, %v", c.title, got, dir, c.want, c.dir)
		}
	}
}

// The bar draws a ">_" in front of a tab whose title is a directory, so
// tabViews has to be the thing that knows which tabs those are.
func TestTabViewsMarkDirectoriesAndNotPrograms(t *testing.T) {
	g := newTestGUI(5, 20)
	g.feed("\x1b]0;~/work/project\x07")
	second := g.addTab(5, 20)
	parser.New().Parse([]byte("\x1b]0;main.go (~/work) - VIM\x07"), second.focus.scr.Apply)

	views := g.tabViews()
	if !views[0].Directory {
		t.Fatalf("%q should be a directory", views[0].Title)
	}
	if views[1].Directory {
		t.Fatalf("%q is vim, not a directory", views[1].Title)
	}
}

func TestTabViewsCarryTitlesAndActivity(t *testing.T) {
	g := newTestGUI(5, 20)
	g.feed("\x1b]0;~/work/project\x07")
	second := g.addTab(5, 20)
	second.activity = true

	views := g.tabViews()
	if len(views) != 2 {
		t.Fatalf("got %d tabs", len(views))
	}
	if views[0].Title != "project" {
		t.Fatalf("first label = %q", views[0].Title)
	}
	if views[0].Activity {
		t.Fatal("the active tab should not be marked")
	}
	if !views[1].Activity {
		t.Fatal("the second tab's activity was lost")
	}
}

func TestTabPositionParsing(t *testing.T) {
	if p, ok := render.ParseTabPosition("left"); !ok || p != render.TabsLeft {
		t.Fatalf("left parsed as %v, %v", p, ok)
	}
	if p, ok := render.ParseTabPosition("top"); !ok || p != render.TabsTop {
		t.Fatalf("top parsed as %v, %v", p, ok)
	}
	if _, ok := render.ParseTabPosition("sideways"); ok {
		t.Fatal("an unknown position should be rejected, not guessed at")
	}
	if got := render.TabsLeft.String(); got != "left" {
		t.Fatalf("String() = %q", got)
	}
}

// The question before closing names what would be stopped, in a sentence.
func TestRunningDetail(t *testing.T) {
	cases := []struct {
		names []string
		want  string
	}{
		{[]string{"vim"}, "vim is still running. Closing will stop it."},
		{[]string{"vim", "npm"}, "vim and npm are still running. Closing will stop them."},
		{[]string{"vim", "npm", "top", "ssh"}, "vim, npm and 2 others are still running. Closing will stop them."},
	}
	for _, c := range cases {
		if got := runningDetail(c.names); got != c.want {
			t.Fatalf("runningDetail(%q) = %q, want %q", c.names, got, c.want)
		}
	}
}

// Panes at a prompt close without a question: asking every time would teach
// people to click through the one time it matters.
func TestIdlePanesCloseWithoutAsking(t *testing.T) {
	g := newTestGUI(5, 10)
	g.confirm = func(string, string, string) bool {
		t.Fatal("asked about panes with nothing running")
		return false
	}
	if !g.canQuit() {
		t.Fatal("quitting with nothing running was refused")
	}
	g.closeTab(0)
	g.closePane()
}

// Cmd+K takes the selection with the history it pointed into.
func TestClearDropsTheSelection(t *testing.T) {
	g := newTestGUI(3, 10)
	g.feed("a\r\nb\r\nc\r\nd\r\n$ ")
	g.selectAll()
	g.clear()
	if g.current().sel.active {
		t.Fatal("a selection survived the history it selected")
	}
	if n := g.screen().ScrollbackLen(); n != 0 {
		t.Fatalf("%d lines of history survived Cmd+K", n)
	}
}

// A shell's OSC 7 report is believed when it is about this machine and names a
// directory that exists; a report from the far side of ssh is not.
func TestPaneDirectoryPrefersALocalReport(t *testing.T) {
	g := newTestGUI(5, 20)
	p := g.current()
	dir := t.TempDir()
	host, _ := os.Hostname()

	g.feed("\x1b]7;file://" + host + dir + "\x07")
	if got := p.directory(); got != dir {
		t.Fatalf("directory() = %q, want the reported %q", got, dir)
	}

	// Another machine's path: there is no kernel to fall back on in a test
	// pane, so nothing is known.
	g.feed("\x1b]7;file://build-server.example.com" + dir + "\x07")
	if got := p.directory(); got != "" {
		t.Fatalf("believed a remote directory: %q", got)
	}

	// A local path that does not exist here is no better.
	g.feed("\x1b]7;file:///no/such/place\x07")
	if got := p.directory(); got != "" {
		t.Fatalf("believed a missing directory: %q", got)
	}

	var none *pane
	if got := none.directory(); got != "" {
		t.Fatalf("a nil pane has directory %q", got)
	}
}

func TestIsLocalHost(t *testing.T) {
	name, _ := os.Hostname()
	short, _, _ := strings.Cut(name, ".")
	for _, h := range []string{"", "localhost", name, short, short + ".local", strings.ToUpper(short)} {
		if !isLocalHost(h) {
			t.Fatalf("%q should be this machine (%q)", h, name)
		}
	}
	if isLocalHost("build-server.example.com") {
		t.Fatal("a different machine was taken for this one")
	}
}

// Drawing with no tabs left must not crash: the window can ask for a frame
// between the last shell exiting and the event loop stopping.
func TestFrameWithNoTabs(t *testing.T) {
	g := newTestGUI(5, 10)
	g.tabs = nil

	f := g.frame()
	if f.Rows != 5 || f.Cols != 10 {
		t.Fatalf("frame = %dx%d, want the window's size", f.Rows, f.Cols)
	}
	// And the rest of the callbacks must be equally unbothered.
	g.key(input.Key{Chars: []rune{'x'}})
	g.scroll(1, 0, 0)
	g.focus(true)
	g.mouse(render.Mouse{})
	g.selectTab(0)
	g.closeTab(0)
}
