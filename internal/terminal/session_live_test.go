package terminal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A session saved with real shells in it comes back: the tabs in order with
// their names, the split with its proportions, the focus, each pane's text
// and each pane's directory.
func TestSessionComesBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	g := newLiveGUI(t)
	g.RestoreSession, g.SessionPath = true, path
	tb, err := g.openTab()
	if err != nil {
		t.Fatal(err)
	}
	left := tb.focus
	g.splitPane(0) // beside
	g.mu.Lock()
	right := tb.focus
	tb.root.ratio = 0.3
	tb.name = "work"
	tb.focus = left
	g.mu.Unlock()
	if _, err := g.openTab(); err != nil {
		t.Fatal(err)
	}
	g.selectTab(0)

	waitForPane(t, right, "a prompt", func(s string) bool { return strings.Contains(s, "PROMPT>") })
	right.write([]byte("cd " + dir + " && printf '\\033[31mred-marker\\033[0m\\n'\r"))
	waitForPane(t, right, "the marker", func(s string) bool { return hasLine(s, "red-marker") })
	g.saveSession()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no session file: %v", err)
	}

	h := newLiveGUI(t)
	h.RestoreSession, h.SessionPath = true, path
	if !h.restoreSession() {
		t.Fatal("the session did not come back")
	}
	h.mu.Lock()
	tabs, active := len(h.tabs), h.active
	back := h.tabs[0]
	panes := back.panes()
	dir2, ratio, name, focus := back.root.dir, back.root.ratio, back.name, back.focus
	behind := h.tabs[1].focus
	h.mu.Unlock()
	if tabs != 2 || active != 0 {
		t.Fatalf("%d tabs with %d active, want 2 with the first", tabs, active)
	}
	if len(panes) != 2 || dir2 != splitBeside || ratio != 0.3 {
		t.Fatalf("the split came back as %d panes, dir %v, ratio %v", len(panes), dir2, ratio)
	}
	if name != "work" || focus != panes[0] {
		t.Errorf("name %q, focus on the right pane %v; want \"work\" and the left pane", name, focus == panes[1])
	}

	restored := panes[1]
	text := paneText(restored)
	if !strings.Contains(text, "red-marker") || !strings.Contains(text, "Restored session:") {
		t.Fatalf("the right pane's text did not come back:\n%s", text)
	}
	restored.mu.Lock()
	var red bool
	for r := 0; r < 24; r++ {
		if strings.Contains(restored.scr.Line(r), "red-marker") {
			col := strings.Index(restored.scr.Line(r), "red-marker")
			red = restored.scr.Cell(r, col).Attr.FG.Index == 1
		}
	}
	restored.mu.Unlock()
	if !red {
		t.Error("the marker lost its colour")
	}
	waitForPane(t, restored, "the new shell's prompt", func(s string) bool {
		return strings.Count(s, "PROMPT>") >= 2
	})
	if got := restored.directory(); got != dir {
		t.Errorf("the new shell is in %q, want %q", got, dir)
	}

	// The tab behind has a shell starting up too; its prompt is not activity.
	waitForPane(t, behind, "the background tab's prompt", func(s string) bool {
		return strings.Contains(s, "PROMPT>")
	})
	h.mu.Lock()
	activity := h.tabs[1].activity
	h.mu.Unlock()
	if activity {
		t.Error("a restored background tab lit its activity dot for its first prompt")
	}
}

// Closing the last tab leaves nothing to come back to; turning the setting
// off stops the file being kept.
func TestSessionForgotten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	g := newLiveGUI(t)
	g.SessionPath = path // RestoreSession off
	if g.restoreSession() {
		t.Fatal("restored with the setting off")
	}
	g.saveSession()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the setting is off, yet the session file is still there")
	}

	g.RestoreSession = true
	for _, bad := range []string{"", "not json", `{"version":99,"tabs":[{"root":{}}]}`, `{"version":1,"tabs":[]}`} {
		if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if g.restoreSession() {
			t.Errorf("restored from %q", bad)
		}
	}
}
