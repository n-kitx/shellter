package screen

import "fmt"

// Replies: the answers a terminal sends back up the PTY.
//
// Terminal emulation is not one-way. A program asks questions - "where is the
// cursor?", "what kind of terminal are you?" - by writing an escape sequence,
// and expects the answer to arrive as if it had been typed. less asks for the
// cursor position to find out how tall the window is; vim asks for device
// attributes before it trusts any of its fancier sequences. A terminal that
// never answers leaves them waiting, then guessing.
//
// The screen does no I/O, so it queues the bytes and the application layer
// writes them:
//
//	program ──► parser ──► screen.reply() ──► TakeReplies ──► PTY ──► program

// maxReplies caps the queue, so that a program spinning on queries cannot make
// us grow without bound.
const maxReplies = 4096

// reply queues bytes to be sent back to the program.
func (s *Screen) reply(format string, args ...any) {
	if len(s.replies) >= maxReplies {
		return
	}
	s.replies = append(s.replies, fmt.Sprintf(format, args...)...)
}

// TakeReplies returns the queued answers and empties the queue. The caller
// writes them to the PTY. It returns nil when there is nothing to send, which
// is almost always.
func (s *Screen) TakeReplies() []byte {
	if len(s.replies) == 0 {
		return nil
	}
	out := s.replies
	s.replies = nil
	return out
}

// deviceAttributes implements DA, "ESC [ c" and "ESC [ > c".
//
// We answer as a VT100 with the Advanced Video Option, the same identity xterm
// gives at its default compatibility level. Claiming more would invite
// sequences we do not implement.
func (s *Screen) deviceAttributes(e csiQuery) {
	switch e.private {
	case 0: // primary DA: what are you?
		s.reply("\x1b[?1;2c")
	case '>': // secondary DA: which version?
		s.reply("\x1b[>0;95;0c")
	}
}

// csiQuery is the little that the report helpers need from an event.
type csiQuery struct {
	private byte
	param   int
}

// deviceStatus implements DSR, "ESC [ n n".
func (s *Screen) deviceStatus(q csiQuery) {
	row, col := s.reportedCursor()
	switch {
	case q.private == '?' && q.param == 6:
		// DECXCPR, the private form: same answer plus a page number.
		s.reply("\x1b[?%d;%d;1R", row, col)
	case q.private != 0:
		// Other private status requests are left unanswered rather than
		// answered wrongly: silence is a valid outcome, a lie is not.
	case q.param == 5:
		s.reply("\x1b[0n") // "I am fine"
	case q.param == 6:
		// CPR. This is the one less uses, and it is why a terminal that does
		// not answer shows the wrong number of lines.
		s.reply("\x1b[%d;%dR", row, col)
	}
}

// reportedCursor is the cursor position as the protocol states it: one-based,
// and relative to the scroll region when origin mode is on.
func (s *Screen) reportedCursor() (row, col int) {
	row, col = s.cur.Row+1, s.cur.Col+1
	if s.mode.Origin {
		row -= s.top
	}
	return row, col
}

// maxTitleStack caps the saved-title stack. A program that pushes without ever
// popping must not grow it without bound.
const maxTitleStack = 8

// windowOp implements the subset of "ESC [ ... t" worth having: the size
// reports, and the title stack. The rest of that family moves and resizes real
// windows, which is the window manager's business, not a terminal's.
func (s *Screen) windowOp(e csiQuery) {
	switch e.param {
	case 18: // text area size, in characters
		s.reply("\x1b[8;%d;%dt", s.rows, s.cols)
	case 19: // screen size, in characters: we have no desktop, so the same
		s.reply("\x1b[9;%d;%dt", s.rows, s.cols)

	// 22 and 23 are how a program borrows the window title politely: push it
	// on the way in, pop it on the way out. vim does exactly this, which is
	// why quitting it leaves the title as the shell had it rather than as the
	// file you were editing.
	//
	// The second parameter distinguishes the icon name from the window title.
	// We have only a title, so both spellings push and pop the same thing.
	case 22:
		s.pushTitle()
	case 23:
		s.popTitle()
	}
}

func (s *Screen) pushTitle() {
	if len(s.titleStack) >= maxTitleStack {
		// Drop the oldest rather than refuse: an unbalanced program should
		// still get its most recent title back.
		copy(s.titleStack, s.titleStack[1:])
		s.titleStack = s.titleStack[:len(s.titleStack)-1]
	}
	s.titleStack = append(s.titleStack, s.title)
}

func (s *Screen) popTitle() {
	if len(s.titleStack) == 0 {
		return
	}
	s.title = s.titleStack[len(s.titleStack)-1]
	s.titleStack = s.titleStack[:len(s.titleStack)-1]
}
