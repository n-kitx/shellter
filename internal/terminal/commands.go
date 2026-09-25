package terminal

import (
	"fmt"
	"strings"

	"github.com/thakurankit7/shellter/internal/config"
	"github.com/thakurankit7/shellter/internal/render"
)

// The menu commands that are not about tabs, splits or the clipboard: clearing
// a pane, opening the settings, and the question asked before closing
// something that is still running.

// clear is Cmd+K on the focused pane.
func (g *GUI) clear() {
	p := g.current()
	if p == nil {
		return
	}
	p.mu.Lock()
	p.scr.Clear()
	p.mu.Unlock()
	// A selection pointed into the history that just went.
	p.sel.clear()
	render.Redraw()
}

// openSettings opens the settings file in a text editor, writing one that
// lists every setting first if there is none yet.
func (g *GUI) openSettings() {
	path := g.ConfigPath
	if path == "" {
		path = config.Path()
	}
	if path == "" {
		return
	}
	if err := config.Create(path); err != nil {
		g.ask("Shellter could not create its settings file.", err.Error(), "OK")
		return
	}
	_ = render.OpenInEditor(path)
}

// ask shows a question and reports whether the user went ahead. Tests replace
// it through confirm, since the real one needs a window.
func (g *GUI) ask(message, detail, action string) bool {
	if g.confirm != nil {
		return g.confirm(message, detail, action)
	}
	return render.Confirm(message, detail, action)
}

// okToClose asks before ending panes that are running something, and says
// whether to go ahead. Panes sitting at a prompt are closed without a word:
// that is the common case, and a question every time would train people to
// click through the one that matters.
//
// It must be called without g.mu held - the alert is modal, and the panes'
// pumps keep running under it.
func (g *GUI) okToClose(panes []*pane, message, action string) bool {
	var running []string
	for _, p := range panes {
		if name := p.running(); name != "" {
			running = append(running, name)
		}
	}
	if len(running) == 0 {
		return true
	}
	return g.ask(message, runningDetail(running), action)
}

// runningDetail says what closing would stop, in a sentence.
func runningDetail(names []string) string {
	var what string
	switch len(names) {
	case 1:
		what = names[0] + " is"
	case 2:
		what = names[0] + " and " + names[1] + " are"
	default:
		what = fmt.Sprintf("%s and %d others are", strings.Join(names[:2], ", "), len(names)-2)
	}
	it := "it"
	if len(names) > 1 {
		it = "them"
	}
	return fmt.Sprintf("%s still running. Closing will stop %s.", what, it)
}

// canQuit is asked before the window closes or the app quits.
func (g *GUI) canQuit() bool {
	return g.okToClose(g.allPanes(), "Quit Shellter?", "Quit")
}

// allPanes is every pane in every tab.
func (g *GUI) allPanes() []*pane {
	g.mu.Lock()
	defer g.mu.Unlock()
	var panes []*pane
	for _, tb := range g.tabs {
		panes = append(panes, tb.panes()...)
	}
	return panes
}
