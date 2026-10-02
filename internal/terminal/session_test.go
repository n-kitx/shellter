package terminal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thakurankit7/shellter/internal/screen"
)

// relaunch is one quit and start again for a pane: what it saved comes back
// under a "Restored session" line, and a new shell prints its startup.
func relaunch(text string, when time.Time) *pane {
	p := &pane{scr: screen.NewWithScrollback(10, 40, 500)}
	p.restoreText(text, when)
	feedPane(p, "startup noise\r\n$ ")
	return p
}

// Restored text is never saved again on top of itself: opening and quitting
// without using a pane changes nothing, and using it keeps only that session.
func TestRestoredTextDoesNotPileUp(t *testing.T) {
	g := newTestGUI(10, 40)
	first := g.current()
	feedPane(first, "$ make\r\nbuilt ok\r\n$ ")
	saved := first.transcript()

	day := time.Date(2026, 9, 28, 20, 7, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		p := relaunch(saved, day.Add(time.Duration(i)*time.Hour))
		if got := p.transcript(); got != saved {
			t.Fatalf("launch %d: an untouched pane saved %q, want what it was restored with, %q", i, got, saved)
		}
	}

	p := relaunch(saved, day)
	g.writeTo(p, []byte("ls\r"))
	feedPane(p, "ls\r\nREADME.md\r\n$ ")
	used := p.transcript()
	if strings.Contains(used, "built ok") || strings.Contains(used, "Restored session") {
		t.Fatalf("a used pane saved the old session too: %q", used)
	}
	if !strings.Contains(used, "startup noise") || !strings.Contains(used, "README.md") {
		t.Fatalf("a used pane lost its own session: %q", used)
	}

	// And again from there: still one session deep, one note on screen.
	again := relaunch(used, day.Add(24*time.Hour))
	g.writeTo(again, []byte("pwd\r"))
	feedPane(again, "pwd\r\n/tmp\r\n$ ")
	if got := again.transcript(); strings.Contains(got, "README.md") || !strings.Contains(got, "/tmp") {
		t.Fatalf("the second session saved %q", got)
	}
	if n := strings.Count(paneText(again), "Restored session"); n != 1 {
		t.Fatalf("%d restored-session lines on screen, want 1", n)
	}
}

// A session saved by the version that stacked restores comes back unstacked:
// only what followed the last "Restored session" line.
func TestStackedSessionsAreUnstacked(t *testing.T) {
	stacked := "old work\r\n\x1b[0;2mRestored session: Mon 28 Sep 20:07\x1b[0m\r\nin-between\r\n" +
		"\x1b[0;2mRestored session: Mon 28 Sep 20:57\x1b[0m\r\nlast session\r\n$ "
	p := relaunch(stacked, time.Now())
	text := paneText(p)
	if strings.Contains(text, "old work") || strings.Contains(text, "in-between") || !strings.Contains(text, "last session") {
		t.Fatalf("restored as:\n%s", text)
	}
	if n := strings.Count(text, "Restored session"); n != 1 {
		t.Fatalf("%d restored-session lines, want 1:\n%s", n, text)
	}
}

// The session is saved while Shellter runs, only when something changed, and
// never again once the last tab has closed.
func TestSessionAutosave(t *testing.T) {
	defer func(d time.Duration) { autosaveInterval = d }(autosaveInterval)
	autosaveInterval = 10 * time.Millisecond

	path := filepath.Join(t.TempDir(), "session.json")
	g := newTestGUI(5, 20)
	g.RestoreSession, g.SessionPath = true, path
	stop := make(chan struct{})
	defer close(stop)
	go g.autosave(stop)

	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("saved with nothing changed")
	}
	feedPane(g.current(), "work in progress")
	g.sessionDirty.Store(true)
	waitUntil(t, "an autosave", func() bool {
		data, err := os.ReadFile(path)
		return err == nil && strings.Contains(string(data), "work in progress")
	})

	g.endSession()
	g.sessionDirty.Store(true)
	g.saveSession()
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the session came back after the last tab closed")
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
