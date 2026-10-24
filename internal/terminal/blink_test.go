package terminal

import (
	"testing"
	"time"

	"github.com/n-kitx/shellter/internal/render"
)

// focusedFrame draws and returns the focused pane's frame.
func focusedFrame(g *GUI) render.Frame {
	for _, p := range g.draw().Panes {
		if p.Focused {
			return p.Frame
		}
	}
	return render.Frame{}
}

// The cursor is the user's until a program picks one, and the user's again
// when the program hands it back.
func TestCursorSettingAndProgramChoice(t *testing.T) {
	g := newTestGUI(3, 10)
	g.CursorStyle = render.CursorBar
	if got := focusedFrame(g).CursorStyle; got != render.CursorBar {
		t.Fatalf("style = %v, want the setting's bar", got)
	}
	g.feed("\x1b[4 q") // vim: steady underline
	if got := focusedFrame(g).CursorStyle; got != render.CursorUnderline {
		t.Fatalf("style = %v, want the program's underline", got)
	}
	g.feed("\x1b[0 q")
	if got := focusedFrame(g).CursorStyle; got != render.CursorBar {
		t.Fatalf("style = %v after DECSCUSR 0, want the setting back", got)
	}
}

// A blinking cursor disappears on the off beat, only in a focused window, and
// stays put while keys arrive.
func TestCursorBlinks(t *testing.T) {
	g := newTestGUI(3, 10)
	g.CursorBlink = true
	g.windowFocused.Store(true)

	if !focusedFrame(g).CursorVisible || !g.blinkWanted.Load() {
		t.Fatal("a blinking cursor did not ask for the timer")
	}
	now := time.Now()
	g.blinkTick(now)
	if focusedFrame(g).CursorVisible {
		t.Fatal("the cursor showed on the off beat")
	}
	g.blinkTick(now)
	if !focusedFrame(g).CursorVisible {
		t.Fatal("the cursor did not come back")
	}

	// Typing holds it on.
	g.blinkTick(now)
	g.typed()
	if !focusedFrame(g).CursorVisible {
		t.Fatal("typing did not bring the cursor back")
	}
	g.blinkTick(time.Now())
	if !focusedFrame(g).CursorVisible {
		t.Fatal("the cursor blinked while keys were arriving")
	}

	// An unfocused window's cursor is steady, and asks nothing of the timer.
	g.windowFocused.Store(false)
	g.blinkOff.Store(true)
	if !focusedFrame(g).CursorVisible || g.blinkWanted.Load() {
		t.Fatal("an unfocused window's cursor blinked")
	}

	// A steady program cursor overrides a blinking setting.
	g.windowFocused.Store(true)
	g.feed("\x1b[2 q")
	if f := focusedFrame(g); !f.CursorVisible || g.blinkWanted.Load() {
		t.Fatal("DECSCUSR 2 did not stop the blink")
	}
}

// Text drawn with SGR 5 is hidden on the off beat, background kept.
func TestBlinkingText(t *testing.T) {
	g := newTestGUI(3, 10)
	g.feed("\x1b[5;41mab\x1b[0mcd")
	f := focusedFrame(g)
	if !g.blinkWanted.Load() {
		t.Fatal("blinking text did not ask for the timer")
	}
	shown := f.CellAt(0, 0)
	g.blinkOff.Store(true)
	f = focusedFrame(g)
	if c := f.CellAt(0, 0); c.FG != c.BG || c.BG != shown.BG {
		t.Fatalf("off beat: %+v, want fg == bg == %06x", c, shown.BG)
	}
	if c := f.CellAt(0, 2); c.FG == c.BG {
		t.Fatal("steady text was hidden too")
	}

	g.feed("\x1b[2J")
	g.blinkOff.Store(false)
	focusedFrame(g)
	if g.blinkWanted.Load() {
		t.Fatal("nothing blinks, yet the timer is wanted")
	}
}
