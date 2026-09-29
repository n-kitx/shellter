package terminal

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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

	// lastFrame is the frame this pane last drew, and syncSince when the
	// program last started a synchronised update (?2026). While one is under
	// way the pane goes on showing lastFrame, so a half-redrawn screen never
	// reaches the glass. UI thread only.
	lastFrame render.Frame
	syncSince time.Time

	// rang is set by the pump when the program rings the bell, for the GUI to
	// take in onChunk. flashUntil is when the visual bell stops showing, in
	// Unix nanoseconds.
	rang       atomic.Bool
	flashUntil atomic.Int64

	// scrollOffset is how far back the view was on the last frame, and
	// scrolledAt when that last changed: the scroll indicator shows while the
	// view is in history and for a moment after it moves. UI thread only.
	scrollOffset int
	scrolledAt   time.Time

	// restored is the text this pane was restored with, and restoredNote the
	// "Restored session" line put under it; both "" for a pane that was not
	// restored. touched says something has been typed into it since. Together
	// they decide what the pane saves (transcript): a pane nobody used keeps
	// the text it came back with, and one that was used keeps only this
	// session's, so restored text is never saved again on top of itself.
	restored     string
	restoredNote string
	touched      atomic.Bool
}

// bellInterval is the least time between two bells from one pane. A program
// printing BEL in a loop would otherwise play the alert sound hundreds of
// times a second.
const bellInterval = 200 * time.Millisecond

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
	var lastBell time.Time
	for {
		n, err := r.Read(buf)
		if n > 0 {
			p.mu.Lock()
			ps.Parse(buf[:n], p.scr.Apply)
			replies := p.scr.TakeReplies()
			clips := p.scr.TakeClipboard()
			bell := p.scr.TakeBell()
			p.mu.Unlock()

			if bell && time.Since(lastBell) >= bellInterval {
				lastBell = time.Now()
				p.rang.Store(true)
			}

			// A program that asked a question is blocked until it is answered.
			if len(replies) > 0 {
				p.write(replies)
			}
			// OSC 52: a program copying to the clipboard, from vim, from tmux,
			// or from the far side of ssh.
			for _, text := range clips {
				render.SetClipboard(text)
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

// running names what the shell in this pane is busy with, or "" when it is at
// its prompt - or has already gone, or never existed, as in tests.
func (p *pane) running() string {
	p.io.Lock()
	defer p.io.Unlock()
	if p.sess == nil || p.gone {
		return ""
	}
	return p.sess.Running()
}

// directory is where this pane's shell is working, for a new tab or split to
// start in, or "" when that cannot be known - including for a nil pane, so that
// "the focused pane's directory" needs no check that there is one.
//
// The shell's own report (OSC 7) comes first, but only when it names this
// machine: over ssh it names a directory on the other one, and the local
// kernel's answer - the directory ssh itself was started in - is the right
// one. The kernel is also the answer for a shell that reports nothing, which
// is zsh as it comes.
func (p *pane) directory() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	host, path := p.scr.Directory()
	p.mu.Unlock()
	if path != "" && isLocalHost(host) && isDir(path) {
		return path
	}

	p.io.Lock()
	defer p.io.Unlock()
	if p.sess == nil || p.gone {
		return ""
	}
	return p.sess.Dir()
}

// isLocalHost reports whether an OSC 7 host names this machine. Shells send
// the short name, the .local name or nothing at all, so the comparison is on
// the first label and ignores case.
func isLocalHost(host string) bool {
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	name, err := os.Hostname()
	if err != nil {
		return false
	}
	short := func(h string) string {
		h, _, _ = strings.Cut(h, ".")
		return strings.ToLower(h)
	}
	return short(host) == short(name)
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// syncTimeout is how long a synchronised update may hold a pane's picture. A
// program that starts one and crashes, or forgets to end it, must not leave
// the pane frozen: after this long it is drawn anyway.
const syncTimeout = 500 * time.Millisecond

// heldFrame is the frame to show instead of a fresh one while the program is
// part way through a synchronised update, and whether there is one. The frame
// it returns is the last one drawn, which is a complete picture by
// definition: it was drawn before the update began.
func (p *pane) heldFrame() (render.Frame, bool) {
	p.mu.Lock()
	syncing := p.scr.Modes().SyncOutput
	rows, cols := p.scr.Size()
	p.mu.Unlock()

	if !syncing {
		p.syncSince = time.Time{}
		return render.Frame{}, false
	}
	if p.syncSince.IsZero() {
		p.syncSince = time.Now()
	}
	// A resize mid-update changes the grid's shape, and the old picture would
	// no longer fit: draw the real thing.
	held := p.lastFrame.Rows == rows && p.lastFrame.Cols == cols && p.lastFrame.Cells != nil
	return p.lastFrame, held && time.Since(p.syncSince) < syncTimeout
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
// dir reports whether the title named a directory, so that the bar can mark a
// shell waiting at a prompt and not a program that named itself.
func label(title, fallback string) (name string, dir bool) {
	title = strings.TrimSpace(title)
	if title == "" {
		return fallback, false
	}
	// Titles are conventionally "user@host:path" or just a path.
	if _, after, found := strings.Cut(title, ":"); found {
		title = after
	}
	if !isPath(title) {
		return title, false
	}
	if base := filepath.Base(title); base != "" && base != "." && base != "/" {
		return base, true
	}
	return title, true
}

// isPath reports whether a title is a bare path, the shape a shell gives it.
// A program's title is prose: vim calls itself "main.go (~/src) - VIM", which
// has a slash in it and a last component too, and is not a directory.
func isPath(title string) bool {
	if strings.ContainsAny(title, " \t") {
		return false
	}
	return strings.HasPrefix(title, "~") || strings.Contains(title, "/")
}
