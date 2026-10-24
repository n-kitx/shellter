package terminal

import (
	"strings"
	"testing"
	"time"

	"github.com/n-kitx/shellter/internal/render"
)

// Selection and paste against a real shell on a real PTY. The system
// pasteboard is deliberately not touched: a test that wrote to it would
// clobber whatever the person running it had copied. What is tested here is
// everything on either side of it - what a drag selects, and what a paste
// turns into by the time the shell sees it.

// rowShowing finds the view row whose whole content is want.
//
// Whole content, not "contains": the command the shell echoed back mentions
// every word of its own output, so a substring search finds the echo rather
// than the result.
func rowShowing(t *testing.T, p *pane, want string) int {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	for row, line := range p.scr.Snapshot().Lines() {
		if strings.TrimSpace(line) == want {
			return row
		}
	}
	t.Fatalf("no row reads exactly %q", want)
	return 0
}

func TestSelectingRealShellOutput(t *testing.T) {
	g := newLiveGUI(t)
	tb, err := g.openTab()
	if err != nil {
		t.Fatal(err)
	}
	p := tb.sole()
	waitForTab(t, tb, "the first prompt", func(s string) bool {
		return strings.Contains(s, "PROMPT>")
	})

	p.write([]byte("echo pick-this-up\r"))
	waitForTab(t, tb, "the output", func(s string) bool {
		// The echoed command contains the text too, so wait for both lines.
		return strings.Count(s, "pick-this-up") >= 2
	})
	// Give the shell a moment to finish drawing the next prompt, so the row
	// we pick is the output and not a line still being written.
	time.Sleep(200 * time.Millisecond)

	// Triple click selects the whole line the output is on.
	row := rowShowing(t, p, "pick-this-up")
	g.mouse(render.Mouse{Action: mousePress, Row: row, Col: 2, Clicks: 3})
	g.mouse(render.Mouse{Action: mouseRelease, Row: row, Col: 2})

	p.mu.Lock()
	got := p.sel.text(p.scr)
	p.mu.Unlock()

	if !strings.Contains(got, "pick-this-up") {
		t.Fatalf("the selection is %q, want the line under the pointer", got)
	}
	// Trailing padding must not come with it: a terminal line is blanked out
	// to the full width, and none of those spaces were ever typed.
	if strings.Contains(got, "   ") {
		t.Fatalf("the selection carried the line's padding: %q", got)
	}
}

func TestDraggingSelectsAcrossRealOutput(t *testing.T) {
	g := newLiveGUI(t)
	tb, err := g.openTab()
	if err != nil {
		t.Fatal(err)
	}
	p := tb.sole()
	waitForTab(t, tb, "the first prompt", func(s string) bool {
		return strings.Contains(s, "PROMPT>")
	})

	p.write([]byte("printf 'aaa\\nbbb\\nccc\\n'\r"))
	waitForTab(t, tb, "three lines of output", func(s string) bool {
		return strings.Contains(s, "ccc\n") || strings.Contains(s, "ccc")
	})
	time.Sleep(200 * time.Millisecond)

	first := rowShowing(t, p, "aaa")
	last := rowShowing(t, p, "ccc")
	if last <= first {
		t.Fatalf("expected ccc below aaa, got rows %d and %d", first, last)
	}

	g.mouse(render.Mouse{Action: mousePress, Row: first, Col: 0, Clicks: 1})
	g.mouse(render.Mouse{Action: mouseMove, Row: last, Col: 3})
	g.mouse(render.Mouse{Action: mouseRelease, Row: last, Col: 3})

	p.mu.Lock()
	got := p.sel.text(p.scr)
	p.mu.Unlock()

	if got != "aaa\nbbb\nccc" {
		t.Fatalf("selection = %q, want three lines", got)
	}
}

// What a paste turns into has to be something a shell will actually run.
func TestAPasteRunsInTheShell(t *testing.T) {
	g := newLiveGUI(t)
	tb, err := g.openTab()
	if err != nil {
		t.Fatal(err)
	}
	p := tb.sole()
	waitForTab(t, tb, "the first prompt", func(s string) bool {
		return strings.Contains(s, "PROMPT>")
	})

	// Copied off a web page, complete with the escape sequence that made it
	// red and the newline that ends it.
	g.writeTo(p, pasteBytes("echo \x1b[31mpasted-and-ran\n", false))

	waitForTab(t, tb, "the pasted command to run", func(s string) bool {
		return strings.Count(s, "pasted-and-ran") >= 2
	})
	// The escape must have been dropped on the way in, not passed through to
	// be interpreted.
	if strings.Contains(tabText(tb), "\x1b") {
		t.Fatal("an escape sequence survived the paste")
	}
}
