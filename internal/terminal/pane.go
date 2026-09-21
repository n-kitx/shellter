package terminal

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"github.com/thakurankit7/shellter/internal/parser"
	"github.com/thakurankit7/shellter/internal/pty"
	"github.com/thakurankit7/shellter/internal/render"
	"github.com/thakurankit7/shellter/internal/screen"
	"github.com/thakurankit7/shellter/internal/shell"
)

// A pane is one shell session and everything that belongs to it: its PTY, its
// parser, and its own screen with its own scrollback.
//
// Panes share nothing but the window. That is the whole design: everything up
// to Phase 10 was already a self-contained pipeline from a PTY to a grid, so a
// second pane is a second instance of it rather than a change to any of it.
// Tabs were the first thing to make that plural; splits are the second, and
// needed nothing new down here.
//
//	pane ─► shell.Session ─► parser ─► screen.Screen ─┐
//	pane ─► shell.Session ─► parser ─► screen.Screen ─┼─► each drawn in its rect
//	pane ─► shell.Session ─► parser ─► screen.Screen ─┘
type pane struct {
	sess *shell.Session

	// mu guards scr. Each pane has its own, so a pane printing at full speed
	// never holds up the frame being drawn for the others.
	mu  sync.Mutex
	scr *screen.Screen

	// io guards the session's file descriptor against being used after the
	// pump has closed it. Asking a pane that has just exited to resize is not
	// a bug - the tree and the window find out a moment later - but it must not
	// race with the close, and a shell exiting while the window is being
	// resized is exactly when it would.
	//
	// Separate from mu, and never held at the same time: mu guards a buffer in
	// memory, io guards a descriptor that can block.
	io   sync.Mutex
	gone bool

	// Written by this pane's pump goroutine, read after it has finished.
	readErr error

	// snapCells and frameCells are this pane's drawing scratch, reused frame
	// after frame so a redraw allocates nothing. Only the UI thread touches
	// them, which is why they need no lock.
	// sel is the text marked in this pane with the mouse. Like the scratch
	// buffers below it is touched only on the UI thread - mouse events and
	// drawing both arrive there - so it needs no lock of its own.
	sel      selection
	selSpans []span
	// words is scratch for reading a line while expanding a double click.
	words []rune

	snapCells  []screen.Cell
	frameCells []render.Cell
}

// newPane starts a shell and gives it a screen of the given size.
func newPane(cfg shell.Config, rows, cols, scrollback int) (*pane, error) {
	cfg.Size = pty.Size{Rows: uint16(rows), Cols: uint16(cols)}
	sess, err := shell.Start(cfg)
	if err != nil {
		return nil, err
	}
	return &pane{sess: sess, scr: screen.NewWithScrollback(rows, cols, scrollback)}, nil
}

// pump reads this pane's PTY until the shell hangs up. It is the Phase 10 read
// loop, unchanged except that what it does with the result is now the GUI's
// business rather than the window's: onChunk says a chunk arrived, so that the
// GUI can redraw if this pane is on screen and light the activity dot on its
// tab if it is not.
func (p *pane) pump(r io.Reader, onChunk func()) {
	ps := parser.New()
	buf := make([]byte, readBufferSize)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			p.mu.Lock()
			ps.Parse(buf[:n], p.scr.Apply)
			replies := p.scr.TakeReplies()
			p.mu.Unlock()

			// A program that asked a question is blocked until it is answered.
			if len(replies) > 0 {
				p.write(replies)
			}
			onChunk()
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				p.readErr = fmt.Errorf("terminal: reading from the shell: %w", err)
			}
			// The reader owns the PTY, so the reader is what closes it.
			p.io.Lock()
			p.gone = true
			if p.sess != nil {
				_ = p.sess.Close()
			}
			p.io.Unlock()
			return
		}
	}
}

// currentTitle is what the shell in this pane last asked to be called.
//
// Read from the screen rather than copied out of it when it changes: the screen
// is where a title comes from, and one source of truth cannot go stale.
func (p *pane) currentTitle() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scr.Title()
}

// resize keeps three things in agreement for this pane: its screen buffer, the
// size the kernel reports to programs on its PTY, and the rectangle the window
// gave it. The session is absent in tests, which drive a pane's screen
// directly; nothing else about a pane is optional.
func (p *pane) resize(rows, cols int) {
	p.mu.Lock()
	p.scr.Resize(rows, cols)
	p.mu.Unlock()

	p.io.Lock()
	defer p.io.Unlock()
	if p.sess != nil && !p.gone {
		_ = p.sess.Resize(pty.Size{Rows: uint16(rows), Cols: uint16(cols)})
	}
}

// write sends bytes to this pane's shell, or drops them if it has exited.
func (p *pane) write(b []byte) {
	p.io.Lock()
	defer p.io.Unlock()
	if p.sess != nil && !p.gone {
		_, _ = p.sess.Write(b)
	}
}

// close ends the session. The shell is hung up on rather than having its PTY
// pulled out from under it: the pump is blocked reading that PTY, and closing
// it does not reliably wake the read, while a shell exiting does. The pump
// closes the master itself once it has finished with it.
func (p *pane) close() {
	if p.sess == nil {
		return
	}
	_ = p.sess.Hangup()
}

// label is what the tab bar shows for a tab, taken from its focused pane.
//
// A shell's title is a path, and a path is too long for a tab, so it is trimmed
// to its last component - "Flux" rather than "/Users/me/PERSONAL/Flux". The bar
// truncates from the head as well, so what survives is always the end.
//
// fallback is used until the shell has said anything, which is the second or
// two it spends reading its startup files: zsh does not send a title until it
// is about to print its first prompt. Naming the program rather than counting
// the tabs means the label changes from true to more specific, instead of from
// a placeholder to the truth.
func label(title, fallback string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return fallback
	}
	// Titles are conventionally "user@host:path" or just a path.
	if _, after, found := strings.Cut(title, ":"); found {
		title = after
	}
	if base := filepath.Base(title); base != "" && base != "." && base != "/" {
		return base
	}
	return title
}
