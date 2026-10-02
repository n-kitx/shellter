package terminal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thakurankit7/shellter/internal/parser"
)

// Session restore: what was open when Shellter quit comes back the next time
// it starts - the tabs in their order and with their names, each tab's splits
// in their proportions, every pane in the directory it was in and with the
// text it was showing, and the tab and pane that had the focus.
//
// What cannot come back is what was running. A shell is a process, and the
// process has gone; each pane gets a new shell, in the old one's directory,
// under the old one's text. That is what Terminal.app does, and it is the
// honest version: a restored vim that was really a picture of vim would be
// worse than none.
//
// The session is written when the user quits, or closes the window, with tabs
// still open. When the last tab closes on its own - every shell exited, every
// pane closed - there is nothing to come back to, and the file is removed.

// sessionVersion is the file's format. A file of any other version is ignored.
const sessionVersion = 1

// autosaveInterval is how often the session is saved while Shellter runs, so
// that a crash or a forced quit loses at most this much rather than all of it.
// Only when something changed: an idle window writes nothing.
var autosaveInterval = 30 * time.Second // a variable so tests can shorten it

// startupQuiet is how long a restored tab's printing is not activity: long
// enough for a shell with a heavy startup file to reach its first prompt.
const startupQuiet = 3 * time.Second

// transcriptLines is the most text kept per pane. Enough to scroll back
// through what was going on; not so much that a pane with a hundred thousand
// lines of build log makes quitting slow.
const transcriptLines = 2000

type savedSession struct {
	Version int        `json:"version"`
	Saved   time.Time  `json:"saved"`
	Active  int        `json:"active"`
	Tabs    []savedTab `json:"tabs"`
}

type savedTab struct {
	Name string `json:"name,omitempty"`
	// Focus is the focused pane's index in layout order - tab.panes().
	Focus int        `json:"focus"`
	Root  *savedNode `json:"root"`
}

// A savedNode is a node: a leaf, with a directory and text, or a split, with
// a direction, a ratio and two children.
type savedNode struct {
	Dir   string     `json:"dir,omitempty"`
	Text  string     `json:"text,omitempty"`
	Split string     `json:"split,omitempty"` // "beside" or "above"
	Ratio float64    `json:"ratio,omitempty"`
	A     *savedNode `json:"a,omitempty"`
	B     *savedNode `json:"b,omitempty"`
}

// SessionPath is where the session is kept:
// ~/Library/Application Support/Shellter/session.json.
func SessionPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Shellter", "session.json")
}

// sessionOn reports whether sessions are saved and restored at all.
func (g *GUI) sessionOn() bool { return g.RestoreSession && g.SessionPath != "" }

// saveSession writes what is open now. The shape is copied under the GUI's
// lock; each pane's directory and text are read after it is let go, one pane
// lock at a time, like everything else that visits every pane.
func (g *GUI) saveSession() {
	// One save at a time - the timer's and the quit's both write the same
	// file - and none once the session has been ended (endSession).
	g.saveMu.Lock()
	defer g.saveMu.Unlock()
	if g.sessionEnded {
		return
	}
	if !g.sessionOn() {
		g.forgetSession()
		return
	}
	leaves := map[*savedNode]*pane{}
	g.mu.Lock()
	s := savedSession{Version: sessionVersion, Saved: time.Now(), Active: g.active}
	for _, tb := range g.tabs {
		if tb.root == nil {
			continue
		}
		focus := 0
		for i, p := range tb.panes() {
			if p == tb.focus {
				focus = i
			}
		}
		s.Tabs = append(s.Tabs, savedTab{Name: tb.name, Focus: focus, Root: saveNode(tb.root, leaves)})
	}
	g.mu.Unlock()
	if len(s.Tabs) == 0 {
		g.forgetSession()
		return
	}

	for leaf, p := range leaves {
		leaf.Dir = p.directory()
		leaf.Text = p.transcript()
	}
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	// Private, and written whole or not at all: it holds whatever was on
	// screen, and a half-written file would lose the session it replaces.
	if err := os.MkdirAll(filepath.Dir(g.SessionPath), 0o700); err != nil {
		return
	}
	tmp := g.SessionPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, g.SessionPath)
}

func saveNode(n *node, leaves map[*savedNode]*pane) *savedNode {
	if n.pane != nil {
		leaf := &savedNode{}
		leaves[leaf] = n.pane
		return leaf
	}
	split := "above"
	if n.dir == splitBeside {
		split = "beside"
	}
	return &savedNode{
		Split: split,
		Ratio: n.ratio,
		A:     saveNode(n.a, leaves),
		B:     saveNode(n.b, leaves),
	}
}

// forgetSession removes the saved session, so the next start is a fresh one.
func (g *GUI) forgetSession() {
	if g.SessionPath != "" {
		_ = os.Remove(g.SessionPath)
	}
}

// loadSession reads the saved session, or reports false when there is none
// to use: no file, a damaged one, or one from another version.
func loadSession(path string) (savedSession, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return savedSession{}, false
	}
	var s savedSession
	if json.Unmarshal(data, &s) != nil || s.Version != sessionVersion || len(s.Tabs) == 0 {
		return savedSession{}, false
	}
	return s, true
}

// restoreSession opens the saved session's tabs, and reports whether it
// opened any. Before the window, so the first frame is already the restored
// one; the panes' readers start once every tab is in place.
func (g *GUI) restoreSession() bool {
	if !g.sessionOn() {
		return false
	}
	s, ok := loadSession(g.SessionPath)
	if !ok {
		return false
	}
	var started []*pane
	var tabs []*tab
	activeTab := 0
	for i, st := range s.Tabs {
		budget := maxPanes
		root, panes := g.restoreNode(st.Root, &budget, s.Saved)
		if root == nil {
			continue
		}
		tb := &tab{root: root, name: st.Name, focus: panes[0], quietUntil: time.Now().Add(startupQuiet)}
		if st.Focus >= 0 && st.Focus < len(panes) {
			tb.focus = tb.panes()[st.Focus]
		}
		if i == s.Active {
			activeTab = len(tabs)
		}
		tabs = append(tabs, tb)
		started = append(started, panes...)
	}
	if len(tabs) == 0 {
		return false
	}

	g.mu.Lock()
	g.tabs = append(g.tabs, tabs...)
	g.active = activeTab
	g.mu.Unlock()
	for _, p := range started {
		g.startPane(p)
	}
	g.resizeAll() // splits: each pane its share, and its text reflowed to fit
	return true
}

// restoreNode rebuilds a saved node, starting a shell for each leaf. A leaf
// whose shell will not start is left out, and its split with it; budget stops
// a doctored file asking for more panes than a tab may have.
func (g *GUI) restoreNode(n *savedNode, budget *int, saved time.Time) (*node, []*pane) {
	if n == nil {
		return nil, nil
	}
	if n.Split == "" {
		if *budget <= 0 {
			return nil, nil
		}
		cfg := g.Config
		if n.Dir != "" && isDir(n.Dir) {
			cfg.Dir = n.Dir
		}
		p, err := newPane(cfg, g.rows, g.cols, g.ScrollbackLines)
		if err != nil {
			return nil, nil
		}
		*budget--
		p.restoreText(n.Text, saved)
		return &node{pane: p}, []*pane{p}
	}

	a, pa := g.restoreNode(n.A, budget, saved)
	b, pb := g.restoreNode(n.B, budget, saved)
	switch {
	case a == nil:
		return b, pb
	case b == nil:
		return a, pa
	}
	dir := splitAbove
	if n.Split == "beside" {
		dir = splitBeside
	}
	ratio := n.Ratio
	if ratio <= 0 || ratio >= 1 {
		ratio = 0.5
	}
	return &node{dir: dir, ratio: ratio, a: a, b: b}, append(pa, pb...)
}

// restoreText puts a pane's old text back on its screen, with a faint line
// under it saying when it is from, before its new shell prints anything: the
// shell's output waits in the PTY until the pane's reader starts.
func (p *pane) restoreText(text string, saved time.Time) {
	if text == "" {
		return
	}
	text = afterOldNotes(text)
	p.restored = text
	p.restoredNote = "Restored session: " + saved.Format("Mon 2 Jan 15:04")
	note := "\r\n\x1b[0;2m" + p.restoredNote + "\x1b[0m\r\n"
	ps := parser.New()
	p.mu.Lock()
	defer p.mu.Unlock()
	ps.Parse([]byte(text), p.scr.Apply)
	ps.Parse([]byte(note), p.scr.Apply)
	// Nothing in a transcript asks a question, but a hand-edited file could;
	// there is no program to hear the answer.
	p.scr.TakeReplies()
	p.scr.TakeClipboard()
	p.scr.TakeBell()
}

// transcript is the text this pane saves. A restored pane that nobody typed
// into saves what it was restored with, unchanged: opening Shellter and
// quitting again must not add a new shell's startup to it every time. One
// that was used saves only what came after its "Restored session" line - this
// session - so the text is at most one session deep, however many times it
// is restored. Any other pane saves everything it has.
func (p *pane) transcript() string {
	if p.restored != "" && !p.touched.Load() {
		return p.restored
	}
	p.mu.Lock()
	text := p.scr.Transcript(transcriptLines)
	p.mu.Unlock()
	if p.restoredNote == "" {
		return text
	}
	// The last one: output printed since could, just, contain the same words.
	// Not found means it has scrolled out of what is kept, and all that is
	// left is this session's.
	if i := strings.LastIndex(text, p.restoredNote); i >= 0 {
		if j := strings.Index(text[i:], "\r\n"); j >= 0 {
			return text[i+j+2:]
		}
		return ""
	}
	return text
}

// oldNote is how a "Restored session" line reads in a saved transcript: faint
// text, from a reset.
const oldNote = "\x1b[0;2mRestored session: "

// afterOldNotes drops everything up to and including the last "Restored
// session" line in a saved text. Sessions saved before transcript kept restored
// text out stacked one restore on another; this is what unstacks them.
func afterOldNotes(text string) string {
	i := strings.LastIndex(text, oldNote)
	if i < 0 {
		return text
	}
	if j := strings.Index(text[i:], "\r\n"); j >= 0 {
		return text[i+j+2:]
	}
	return ""
}

// autosave saves the session every autosaveInterval while anything has
// changed - output, or the tabs rearranged - until stop is closed.
func (g *GUI) autosave(stop <-chan struct{}) {
	tick := time.NewTicker(autosaveInterval)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			if g.sessionDirty.Swap(false) && !g.quitting.Load() {
				g.saveSession()
			}
		}
	}
}

// endSession forgets the saved session for good - the last tab closed, so the
// next start is a fresh one - and stops any later save writing it back.
func (g *GUI) endSession() {
	g.saveMu.Lock()
	defer g.saveMu.Unlock()
	g.sessionEnded = true
	g.forgetSession()
}
