package terminal

import (
	"strings"
	"testing"
	"time"

	"github.com/ankitthakur/shellter/internal/pty"
	"github.com/ankitthakur/shellter/internal/render"
	"github.com/ankitthakur/shellter/internal/shell"
)

// Two real shells in one window, without the window: openTab starts a genuine
// zsh on a genuine PTY, so this exercises everything except the drawing.

func newLiveGUI(t *testing.T) *GUI {
	t.Helper()
	cfg := shell.DefaultConfig()
	cfg.Args = []string{"-f"}
	cfg.Env = append(cfg.Env, "PS1=PROMPT> ")
	cfg.Size = pty.Size{Rows: 24, Cols: 80}

	g := &GUI{
		Config:          cfg,
		Title:           "Shellter",
		Palette:         DefaultPalette(),
		ScrollbackLines: 200,
		TabPosition:     render.TabsTop,
		rows:            24,
		cols:            80,
		lastRows:        24,
		lastCols:        80,
	}
	t.Cleanup(g.closeWindow)
	return g
}

// text returns what a tab's screen currently shows.
func tabText(tb *tab) string {
	tb.sole().mu.Lock()
	defer tb.sole().mu.Unlock()
	return tb.sole().scr.String()
}

func waitForTab(t *testing.T, tb *tab, what string, cond func(string) bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond(tabText(tb)) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; tab showed:\n%s", what, tabText(tb))
}

func TestTwoTabsRunSeparateShells(t *testing.T) {
	g := newLiveGUI(t)

	first, err := g.openTab()
	if err != nil {
		t.Fatal(err)
	}
	second, err := g.openTab()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("the second tab reused the first one's session")
	}
	if first.sole().sess.Pid() == second.sole().sess.Pid() {
		t.Fatal("both tabs are the same shell process")
	}

	waitForTab(t, first, "the first tab's prompt", func(s string) bool {
		return strings.Contains(s, "PROMPT>")
	})
	waitForTab(t, second, "the second tab's prompt", func(s string) bool {
		return strings.Contains(s, "PROMPT>")
	})

	// Each shell gets its own command...
	if _, err := first.sole().sess.Write([]byte("echo one-here\r")); err != nil {
		t.Fatal(err)
	}
	if _, err := second.sole().sess.Write([]byte("echo two-here\r")); err != nil {
		t.Fatal(err)
	}

	waitForTab(t, first, "the first tab's output", func(s string) bool {
		return strings.Contains(s, "one-here")
	})
	waitForTab(t, second, "the second tab's output", func(s string) bool {
		return strings.Contains(s, "two-here")
	})

	// ...and neither sees the other's.
	if strings.Contains(tabText(first), "two-here") {
		t.Fatalf("the first tab shows the second tab's output:\n%s", tabText(first))
	}
	if strings.Contains(tabText(second), "one-here") {
		t.Fatalf("the second tab shows the first tab's output:\n%s", tabText(second))
	}
}

// A shell that exits takes its tab with it, whoever asked for it to go.
func TestExitingAShellClosesItsTab(t *testing.T) {
	g := newLiveGUI(t)
	if _, err := g.openTab(); err != nil {
		t.Fatal(err)
	}
	second, err := g.openTab()
	if err != nil {
		t.Fatal(err)
	}
	waitForTab(t, second, "the second tab's prompt", func(s string) bool {
		return strings.Contains(s, "PROMPT>")
	})

	if _, err := second.sole().sess.Write([]byte("exit\r")); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		n := len(g.tabs)
		g.mu.Unlock()
		if n == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the tab outlived its shell")
}

// Closing a tab from the bar hangs its shell up, the same way the window does.
func TestClosingATabEndsItsShell(t *testing.T) {
	g := newLiveGUI(t)
	if _, err := g.openTab(); err != nil {
		t.Fatal(err)
	}
	second, err := g.openTab()
	if err != nil {
		t.Fatal(err)
	}
	waitForTab(t, second, "the second tab's prompt", func(s string) bool {
		return strings.Contains(s, "PROMPT>")
	})

	g.closeTab(1)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		n := len(g.tabs)
		g.mu.Unlock()
		if n == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("closing the tab did not end its shell")
}

// A resize reaches the shells themselves, not just our grids: `tput cols` asks
// the kernel, which only knows what we told it.
func TestResizeReachesEveryShell(t *testing.T) {
	g := newLiveGUI(t)
	first, err := g.openTab()
	if err != nil {
		t.Fatal(err)
	}
	second, err := g.openTab()
	if err != nil {
		t.Fatal(err)
	}
	waitForTab(t, second, "the second tab's prompt", func(s string) bool {
		return strings.Contains(s, "PROMPT>")
	})

	g.resize(30, 100)

	for i, tb := range []*tab{first, second} {
		if _, err := tb.sole().sess.Write([]byte("tput cols\r")); err != nil {
			t.Fatal(err)
		}
		waitForTab(t, tb, "tab "+string(rune('1'+i))+" to report its width", func(s string) bool {
			for _, line := range strings.Split(s, "\n") {
				if strings.TrimSpace(line) == "100" {
					return true
				}
			}
			return false
		})
	}
}
