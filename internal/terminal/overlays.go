package terminal

import (
	"time"

	"github.com/thakurankit7/shellter/internal/render"
	"github.com/thakurankit7/shellter/internal/screen"
)

// ---------------------------------------------------------------------------
// The bell
// ---------------------------------------------------------------------------

// flashTime is how long the visual bell shows: long enough to catch the eye,
// short enough not to be read as the screen changing.
const flashTime = 150 * time.Millisecond

// ring answers a program ringing the bell in pane p of tab tb. It runs on the
// main thread - the settings it reads are changed there - posted by the pane's
// pump, at most once per bellInterval.
//
// The sound or the flash is the setting's to choose. Whatever it says, a tab
// in the background is marked until it is looked at, and the Dock icon
// bounces once if Shellter is not in front - a bell is a program asking for
// attention, and that is where the attention is.
func (g *GUI) ring(tb *tab, p *pane) {
	if g.Bell == BellOff {
		return
	}
	if g.Bell == BellSound || g.Bell == BellBoth {
		render.Beep()
	}
	if g.Bell == BellFlash || g.Bell == BellBoth {
		p.flashUntil.Store(time.Now().Add(flashTime).UnixNano())
		g.requestRedraw()
		time.AfterFunc(flashTime, g.requestRedraw) // and again to take it away
	}

	g.mu.Lock()
	background := !(g.active < len(g.tabs) && g.tabs[g.active] == tb)
	changed := background && !tb.rang
	if changed {
		tb.rang = true
	}
	g.mu.Unlock()
	if changed {
		g.syncTabs()
	}
	render.Attention()
}

// flash marks a pane to be drawn with the visual bell over it, while it lasts.
func (g *GUI) flash(rp *render.Pane, p *pane) {
	if p.flashUntil.Load() > time.Now().UnixNano() {
		rp.Flash, rp.FlashColor = true, g.Palette.Foreground
	}
}

// ---------------------------------------------------------------------------
// The scroll indicator
// ---------------------------------------------------------------------------

// scrollLinger is how long the indicator stays after the view stops moving,
// once it is back at the live screen.
const scrollLinger = time.Second

// scrollIndicator says where the view is in the pane's history. It shows
// whenever the view is scrolled back - so that it is always clear the screen
// being looked at is not the live one - and for a moment after any scroll.
// A full-screen program has no history to place, and gets none.
func (g *GUI) scrollIndicator(rp *render.Pane, p *pane, snap screen.Snapshot, alt bool) {
	now := time.Now()
	if snap.ScrollOffset != p.scrollOffset {
		p.scrollOffset = snap.ScrollOffset
		p.scrolledAt = now
		time.AfterFunc(scrollLinger, g.requestRedraw) // to put it away again
	}
	if alt || snap.ScrollbackLen == 0 {
		return
	}
	if snap.ScrollOffset == 0 && now.Sub(p.scrolledAt) >= scrollLinger {
		return
	}
	rp.Scroll = render.Scroll{
		Above:   snap.ScrollbackLen - snap.ScrollOffset,
		Visible: snap.Rows,
		Total:   snap.ScrollbackLen + snap.Rows,
		Color:   g.Palette.Foreground,
	}
}
