package terminal

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/thakurankit7/shellter/internal/input"
	"github.com/thakurankit7/shellter/internal/render"
	"github.com/thakurankit7/shellter/internal/screen"
)

// Phase 9: the application layer's share of terminal compatibility - deciding
// what a wheel turn means, reporting the mouse, and answering queries.

// sink replaces the shell session, so what Shellter would write to the PTY can
// be read back.
func (g *GUI) sink() *bytes.Buffer {
	buf := &bytes.Buffer{}
	g.out = buf
	return buf
}

func TestMouseIsDroppedUntilAProgramAsks(t *testing.T) {
	g := newTestGUI(10, 20)
	out := g.sink()

	g.mouse(render.Mouse{Button: input.MouseLeft, Action: input.MousePress, Row: 2, Col: 3})
	if out.Len() != 0 {
		t.Fatalf("sent %q; the mouse belongs to the window until a program asks", out.Bytes())
	}

	g.feed("\x1b[?1000h\x1b[?1006h")
	g.mouse(render.Mouse{Button: input.MouseLeft, Action: input.MousePress, Row: 2, Col: 3})
	if got := out.String(); got != "\x1b[<0;4;3M" {
		t.Fatalf("mouse report = %q", got)
	}
}

func TestMouseCoordinatesAreClampedToTheGrid(t *testing.T) {
	g := newTestGUI(5, 10)
	out := g.sink()
	g.feed("\x1b[?1000h\x1b[?1006h")

	// A drag can run past the edge of the window.
	g.mouse(render.Mouse{Button: input.MouseLeft, Action: input.MousePress, Row: 99, Col: 99})
	if got := out.String(); got != "\x1b[<0;10;5M" {
		t.Fatalf("report = %q, want the bottom-right cell", got)
	}
}

func TestWheelBecomesButtonsWhenTheProgramWantsTheMouse(t *testing.T) {
	g := newTestGUI(5, 10)
	out := g.sink()
	g.feed("\x1b[?1000h\x1b[?1006h")

	g.scroll(2, 1, 1)
	if got := out.String(); got != "\x1b[<64;2;2M\x1b[<64;2;2M" {
		t.Fatalf("wheel = %q, want two wheel-up reports", got)
	}
	out.Reset()
	g.scroll(-1, 1, 1)
	if got := out.String(); got != "\x1b[<65;2;2M" {
		t.Fatalf("wheel = %q, want a wheel-down report", got)
	}
}

// On the alternate screen there is no history to scroll, so the wheel sends
// what the arrow keys would - which is what makes it work in less and man.
func TestWheelBecomesArrowKeysOnTheAlternateScreen(t *testing.T) {
	g := newTestGUI(5, 10)
	out := g.sink()
	g.feed("\x1b[?1049h")

	g.scroll(2, 0, 0)
	if got := out.String(); got != "\x1b[A\x1b[A" {
		t.Fatalf("wheel = %q, want two cursor-up sequences", got)
	}

	// And it follows application-cursor mode, like a real arrow key.
	out.Reset()
	g.feed("\x1b[?1h")
	g.scroll(-1, 0, 0)
	if got := out.String(); got != "\x1bOB" {
		t.Fatalf("wheel = %q, want an SS3 cursor-down", got)
	}
}

func TestWheelStillScrollsHistoryOnThePrimaryScreen(t *testing.T) {
	g := newTestGUI(3, 8)
	out := g.sink()
	for i := 0; i < 6; i++ {
		g.feed("line\r\n")
	}

	g.scroll(2, 0, 0)
	if g.screen().ScrollOffset() != 2 {
		t.Fatalf("offset = %d, want the view scrolled back", g.screen().ScrollOffset())
	}
	if out.Len() != 0 {
		t.Fatalf("sent %q to the shell; scrollback is ours", out.Bytes())
	}
}

func TestShiftPageKeysReachTheProgramOnTheAlternateScreen(t *testing.T) {
	g := newTestGUI(5, 10)
	g.feed("\x1b[?1049h")

	if g.scrollKey(input.Key{Code: input.CodePageUp, Mods: input.ModShift}) {
		t.Fatal("Shift+PageUp should reach vim, not scroll a history that is not there")
	}
	if got := string(g.encode(input.Key{Code: input.CodePageUp, Mods: input.ModShift})); got != "\x1b[5;2~" {
		t.Fatalf("encoded as %q", got)
	}
}

func TestFocusIsReportedOnlyWhenAsked(t *testing.T) {
	g := newTestGUI(5, 10)
	out := g.sink()

	g.focus(true)
	if out.Len() != 0 {
		t.Fatalf("sent %q without ?1004", out.Bytes())
	}

	g.feed("\x1b[?1004h")
	g.focus(true)
	g.focus(false)
	if got := out.String(); got != "\x1b[I\x1b[O" {
		t.Fatalf("focus reports = %q", got)
	}
}

// A query is answered on the way through the pump, before the redraw: the
// program asking is blocked until it hears back.
func TestQueriesAreAnsweredThroughThePump(t *testing.T) {
	g := newTestGUI(24, 80)
	out := g.sink()

	g.feed("\x1b[5;9H\x1b[6n")
	g.write(g.screen().TakeReplies())

	if got := out.String(); got != "\x1b[5;9R" {
		t.Fatalf("reply = %q, want the cursor position", got)
	}
}

func TestTitleFallsBackToOurOwnName(t *testing.T) {
	g := newTestGUI(5, 10)
	g.Title = "Shellter"

	g.feed("\x1b]0;vim README.md\x07")
	if got := g.titleFor(g.screen().Title()); got != "vim README.md" {
		t.Fatalf("title = %q, want what the program asked for", got)
	}
	g.feed("\x1b]2;\x07") // cleared
	if got := g.titleFor(g.screen().Title()); got != "Shellter" {
		t.Fatalf("title = %q, want our own name back", got)
	}
}

// A zsh prompt sets the title on every line, and each push is a hop to the UI
// thread, so only real changes travel.
func TestTitleIsPushedOnlyWhenItChanges(t *testing.T) {
	g := newTestGUI(5, 10)
	g.lastTitle = "same"

	g.feed("\x1b]0;same\x07")
	g.updateWindowTitle(g.current())
	if g.lastTitle != "same" {
		t.Fatalf("lastTitle = %q, want the title taken from the tab", g.lastTitle)
	}
	g.feed("\x1b]0;different\x07")
	g.updateWindowTitle(g.current())
	if g.lastTitle != "different" {
		t.Fatalf("lastTitle = %q, want the new title remembered", g.lastTitle)
	}
}

// Motion tracking costs a callback per mouse move, so the tracking area is only
// installed for ?1003 - not for the click and drag modes that do not need it.
func TestMotionTrackingFollowsTheMode(t *testing.T) {
	g := newTestGUI(5, 10)

	g.feed("\x1b[?1002h")
	g.updateTracking()
	if g.lastTracking != screen.MouseDrag {
		t.Fatalf("tracking = %v, want drag", g.lastTracking)
	}
	g.feed("\x1b[?1003h")
	g.updateTracking()
	if g.lastTracking != screen.MouseMotion {
		t.Fatalf("tracking = %v, want motion", g.lastTracking)
	}
}

// --- Phase 10: the polish, tested ---

// A burst of output must not queue a repaint per chunk. One request stands
// until the frame it asked for has been drawn.
func TestRedrawRequestsCoalesce(t *testing.T) {
	g := newTestGUI(5, 10)

	if g.redrawPending.Load() {
		t.Fatal("a fresh window should have no repaint outstanding")
	}

	// Ten chunks arrive before the window gets a chance to draw.
	for i := 0; i < 10; i++ {
		g.requestRedraw()
	}
	if !g.redrawPending.Load() {
		t.Fatal("a request should be outstanding")
	}

	// Drawing the frame clears it, so the next chunk asks again.
	g.frame()
	if g.redrawPending.Load() {
		t.Fatal("drawing a frame should clear the pending request")
	}
	g.requestRedraw()
	if !g.redrawPending.Load() {
		t.Fatal("output after a frame should ask for another one")
	}
}

// The window reports a resize for every pixel of a drag; only a change in the
// number of cells is worth acting on, because acting means a SIGWINCH to the
// program inside.
func TestResizeIgnoresUnchangedSize(t *testing.T) {
	g := newTestGUI(24, 80)

	if !g.sizeChanged(30, 100) {
		t.Fatal("the first size should count as a change")
	}
	if g.sizeChanged(30, 100) {
		t.Fatal("the same size again should be ignored")
	}
	if !g.sizeChanged(30, 101) {
		t.Fatal("one more column is a change")
	}
}

// A read failure that is not a hangup has to reach Run. In a window there is no
// stderr anyone is watching.
func TestReadErrorsAreCarriedNotPrinted(t *testing.T) {
	g := newTestGUI(5, 10)
	t0 := g.current()
	t0.pump(&failingReader{err: errors.New("disk on fire")}, func() {})

	if t0.readErr == nil || !strings.Contains(t0.readErr.Error(), "disk on fire") {
		t.Fatalf("readErr = %v, want the underlying failure", t0.readErr)
	}
}

// A hangup is how every session ends, so it is not an error.
func TestHangupIsNotAnError(t *testing.T) {
	g := newTestGUI(5, 10)
	t0 := g.current()
	t0.pump(&failingReader{err: io.EOF}, func() {})

	if t0.readErr != nil {
		t.Fatalf("readErr = %v, want nil for a normal hangup", t0.readErr)
	}
}

// failingReader yields one chunk, then fails.
type failingReader struct {
	done bool
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		return copy(p, "hello"), nil
	}
	return 0, r.err
}
