package terminal

import (
	"strings"
	"testing"
	"time"
)

// Splits with real shells in them. The window is absent, so what this proves is
// everything up to the drawing: two zsh processes in one tab, each on its own
// PTY, each told the size of its own half.

// paneText is what one pane's screen currently shows.
func paneText(p *pane) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scr.String()
}

func waitForPane(t *testing.T, p *pane, what string, cond func(string) bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond(paneText(p)) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; the pane showed:\n%s", what, paneText(p))
}

func TestASplitRunsASecondShellInTheSameTab(t *testing.T) {
	g := newLiveGUI(t)

	tb, err := g.openTab()
	if err != nil {
		t.Fatal(err)
	}
	left := tb.focus

	g.splitPane(0) // render.SplitBeside
	right := tb.focus
	if right == left {
		t.Fatal("splitting did not start a new pane")
	}
	if left.sess.Pid() == right.sess.Pid() {
		t.Fatal("the split reused the first pane's shell")
	}

	waitForPane(t, left, "the first prompt", func(s string) bool {
		return strings.Contains(s, "PROMPT>")
	})
	waitForPane(t, right, "the second prompt", func(s string) bool {
		return strings.Contains(s, "PROMPT>")
	})

	// Each half gets its own keystrokes. The focus is in the right-hand pane,
	// so that is where typing lands.
	if _, err := right.sess.Write([]byte("echo right-here\r")); err != nil {
		t.Fatal(err)
	}
	if _, err := left.sess.Write([]byte("echo left-here\r")); err != nil {
		t.Fatal(err)
	}
	waitForPane(t, right, "the right pane's output", func(s string) bool {
		return hasLine(s, "right-here")
	})
	waitForPane(t, left, "the left pane's output", func(s string) bool {
		return hasLine(s, "left-here")
	})
	if strings.Contains(paneText(left), "right-here") {
		t.Error("output from the right pane appeared in the left one")
	}
}

// The point of a split is that the program inside it fills it. A shell that
// still thinks it has the whole window wraps its lines in the wrong place.
func TestEachPaneTellsItsShellItsOwnWidth(t *testing.T) {
	g := newLiveGUI(t)

	tb, err := g.openTab()
	if err != nil {
		t.Fatal(err)
	}
	left := tb.focus
	g.splitPane(0) // beside: 80 columns become 40 and 39, with one between
	right := tb.focus

	for _, p := range []*pane{left, right} {
		waitForPane(t, p, "a prompt", func(s string) bool {
			return strings.Contains(s, "PROMPT>")
		})
		if _, err := p.sess.Write([]byte("tput cols\r")); err != nil {
			t.Fatal(err)
		}
	}

	// The layout gives the two halves 40 and 39 columns: 80 less the divider,
	// split down the middle.
	want := map[*pane]string{left: "40", right: "39"}
	for p, cols := range want {
		waitForPane(t, p, "the shell to report "+cols+" columns", func(s string) bool {
			return hasLine(s, cols)
		})
	}
}

// Closing one half of a split must leave the other running, with the whole tab
// to itself.
func TestClosingOneHalfLeavesTheOtherRunning(t *testing.T) {
	g := newLiveGUI(t)

	tb, err := g.openTab()
	if err != nil {
		t.Fatal(err)
	}
	left := tb.focus
	g.splitPane(0)
	right := tb.focus

	waitForPane(t, right, "a prompt", func(s string) bool {
		return strings.Contains(s, "PROMPT>")
	})
	if _, err := right.sess.Write([]byte("exit\r")); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		g.mu.Lock()
		panes := tb.panes()
		focus := tb.focus
		g.mu.Unlock()
		if len(panes) == 1 && panes[0] == left && focus == left {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the closed pane is still in the tree: %d panes left", len(panes))
		}
		time.Sleep(20 * time.Millisecond)
	}

	g.mu.Lock()
	tabs := len(g.tabs)
	g.mu.Unlock()
	if tabs != 1 {
		t.Fatalf("got %d tabs, want the tab to survive its pane closing", tabs)
	}

	// And the survivor got its columns back.
	waitForPane(t, left, "a prompt", func(s string) bool {
		return strings.Contains(s, "PROMPT>")
	})
	if _, err := left.sess.Write([]byte("tput cols\r")); err != nil {
		t.Fatal(err)
	}
	waitForPane(t, left, "the shell to report the full width", func(s string) bool {
		return hasLine(s, "80")
	})
}
