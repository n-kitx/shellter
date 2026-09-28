package terminal

import (
	"time"

	"github.com/thakurankit7/shellter/internal/render"
	"github.com/thakurankit7/shellter/internal/screen"
)

// Blinking: the cursor, when the settings or the program ask for it, and text
// drawn with SGR 5.
//
// One timer serves every pane. It flips a phase twice a second, and each
// frame draws blinking things according to it; nothing is redrawn for
// blinking unless the frame before had something blinking in it, so a
// terminal with a steady cursor costs no redraws while idle.

// blinkInterval is half a blink: how long blinking things stay shown, and then
// hidden. Terminal.app and xterm both use about this.
const blinkInterval = 530 * time.Millisecond

// blink applies the cursor settings and the blink phase to a pane's frame, and
// reports whether anything in it blinks.
//
// The cursor is the program's if it chose one (DECSCUSR), and the user's
// otherwise. It blinks only in the focused pane of a focused window, and is
// held steady while keys arrive - a cursor that vanishes mid-word is hard to
// follow.
func (g *GUI) blink(f *render.Frame, snap screen.Snapshot, focused bool) bool {
	style, blinks := g.CursorStyle, g.CursorBlink
	if snap.CursorChosen {
		style, blinks = render.CursorStyle(snap.CursorStyle), snap.CursorBlink
	}
	f.CursorStyle = style

	off := g.blinkOff.Load()
	blinking := false
	if blinks && focused && f.CursorVisible && g.windowFocused.Load() {
		blinking = true
		if off {
			f.CursorVisible = false
		}
	}
	for i := range f.Cells {
		c := &f.Cells[i]
		if c.Flags&render.FlagBlink == 0 {
			continue
		}
		blinking = true
		if off {
			c.FG = c.BG // hidden, but still taking its place and its background
		}
	}
	return blinking
}

// typed holds the blink visible while the user types: the phase goes back to
// shown now, and the timer leaves it there until keys stop.
func (g *GUI) typed() {
	g.typedAt.Store(time.Now().UnixNano())
	if g.blinkOff.Swap(false) {
		render.Redraw()
	}
}

// blinker runs the blink phase until stop is closed.
func (g *GUI) blinker(stop <-chan struct{}) {
	tick := time.NewTicker(blinkInterval)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		g.blinkTick(time.Now())
	}
}

// blinkTick moves the phase on, if there is anything to blink and nobody has
// typed for a whole interval.
func (g *GUI) blinkTick(now time.Time) {
	still := now.Sub(time.Unix(0, g.typedAt.Load())) < blinkInterval
	if !g.blinkWanted.Load() || still {
		if g.blinkOff.Swap(false) {
			g.requestRedraw()
		}
		return
	}
	g.blinkOff.Store(!g.blinkOff.Load())
	g.requestRedraw()
}
