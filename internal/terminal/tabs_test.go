package terminal

import (
	"testing"

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
	}{
		{"", "zsh"},
		{"   ", "zsh"},
		{"ankitthakur@Ankits-MacBook-Pro:~/PERSONAL/Flux", "Flux"},
		{"~/PERSONAL/Flux", "Flux"},
		{"/usr/local/lib", "lib"},
		{"vim", "vim"},
		{"/", "/"},
	}
	for _, c := range cases {
		if got := label(c.title, "zsh"); got != c.want {
			t.Fatalf("label(%q) = %q, want %q", c.title, got, c.want)
		}
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
