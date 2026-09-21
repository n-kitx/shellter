// Package terminal is the application layer for this phase.
//
// It connects the outside world (our own stdin/stdout, the window size, the
// user pressing keys) to a shell.Session. In later phases the "outside world"
// becomes a real UI and this package is where an input encoder, a parser, a
// screen buffer and a renderer will be plugged in. Neither shell nor pty needs
// to change for that.
package terminal

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thakurankit7/shellter/internal/parser"
	"github.com/thakurankit7/shellter/internal/pty"
	"github.com/thakurankit7/shellter/internal/screen"
	"github.com/thakurankit7/shellter/internal/shell"
	"golang.org/x/term"
)

// App wires one input source and one output sink to one shell session.
type App struct {
	In     *os.File
	Out    *os.File
	Config shell.Config

	// OnEvent, when set, receives every event the parser recognises in the
	// shell's output. It is the seam every consumer of the event stream hangs
	// off: the screen buffer, and the optional trace file.
	OnEvent func(parser.Event)

	// Screen, when set, is updated from those same events. It is the terminal
	// state a renderer will eventually draw; nothing draws it yet.
	Screen *screen.Screen
}

// New returns an App that runs the default shell against this process's
// stdin/stdout.
func New() *App {
	return &App{In: os.Stdin, Out: os.Stdout, Config: shell.DefaultConfig()}
}

// Run starts the shell and pumps bytes until it exits. It returns the shell's
// exit code.
func (a *App) Run() (int, error) {
	// Raw mode on OUR terminal. Normally the kernel's line discipline buffers
	// a whole line, echoes it, and turns Ctrl+C into a signal for us. We want
	// none of that: every keystroke must reach the shell's PTY untouched, so
	// that Ctrl+C becomes a signal over there, not here.
	restore, err := a.enterRawMode()
	if err != nil {
		return -1, err
	}
	defer restore()

	if size, err := a.windowSize(); err == nil {
		a.Config.Size = size
	}

	sess, err := shell.Start(a.Config)
	if err != nil {
		return -1, err
	}
	defer sess.Close()

	fmt.Fprintf(a.Out, "shellter: %s pid=%d (exit the shell to quit)\r\n", a.Config.Path, sess.Pid())

	// Debug-only: with SHELLTER_SCREEN set, maintain the virtual screen and
	// write its final contents to that file on exit. Nothing renders the
	// screen yet, so this is the only way to see that it is being kept.
	if a.Screen == nil {
		if path := os.Getenv(ScreenEnvVar); path != "" {
			a.Screen = screen.New(int(a.Config.Size.Rows), int(a.Config.Size.Cols))
			defer func() { _ = os.WriteFile(path, []byte(a.Screen.String()+"\n"), 0o644) }()
		}
	}

	// Debug-only: with SHELLTER_TRACE set, dump the parsed event stream to a
	// file. An explicitly configured OnEvent wins over the environment.
	if a.OnEvent == nil {
		if path := os.Getenv(TraceEnvVar); path != "" {
			sink, closeSink, err := newTraceSink(path)
			if err != nil {
				return -1, err
			}
			defer closeSink()
			a.OnEvent = sink
		}
	}

	stopResize := a.watchResize(sess)
	defer stopResize()

	// Two directions, two goroutines. Both pumps block on a read, so a
	// single-threaded loop could only ever service one direction at a time:
	// while we waited for the user to press a key we could not print shell
	// output, and while we waited for shell output we could not accept keys.
	outDone := make(chan error, 1)
	go func() {
		// shell -> parser -> our stdout. Ends when the PTY reports hangup.
		outDone <- a.pumpOutput(sess)
	}()
	go func() {
		// our stdin -> shell. There is no portable way to interrupt a blocked
		// read on stdin, so this goroutine simply lives until the process
		// exits. It owns no resources beyond the fds we are about to close.
		_, _ = io.Copy(sess, a.In)
	}()

	// The child is the source of truth for "we are done".
	code, waitErr := sess.Wait()

	// Give the output pump a moment to drain whatever the shell printed on its
	// way out, then stop waiting: a stray background process could still hold
	// the slave side open, which would keep the copy blocked forever.
	select {
	case err := <-outDone:
		if err != nil && !errors.Is(err, io.EOF) {
			return code, err
		}
	case <-time.After(200 * time.Millisecond):
	}

	return code, waitErr
}

// pumpOutput is where PTY bytes enter the application.
//
//	sess.Read -> chunk ─┬─► a.Out          raw passthrough (phase 1 behaviour)
//	                    └─► parser.Parse ─► events -> a.OnEvent
//
// The passthrough stays because nothing consumes events yet: the terminal we
// are running inside is still doing the actual emulation. When the screen
// buffer and renderer land, the passthrough is what they replace.
func (a *App) pumpOutput(r io.Reader) error {
	p := parser.New()
	buf := make([]byte, readBufferSize)
	for {
		// One read returns whatever happened to be in the kernel buffer: it can
		// stop in the middle of an escape sequence. The parser carries its
		// state across calls, so that does not matter to it.
		n, err := r.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if _, werr := a.Out.Write(chunk); werr != nil {
				return werr
			}
			if a.Screen != nil || a.OnEvent != nil {
				p.Parse(chunk, a.handleEvent)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// handleEvent is the fan-out point of the event stream:
//
//	events ─┬─► a.Screen    terminal state  (a renderer will read this)
//	        └─► a.OnEvent   trace / tests
func (a *App) handleEvent(e parser.Event) {
	if a.Screen != nil {
		a.Screen.Apply(e)
	}
	if a.OnEvent != nil {
		a.OnEvent(e)
	}
}

// enterRawMode switches our controlling terminal to raw mode and returns a
// function that puts it back. If stdin is not a terminal (tests, pipes) it is
// a no-op.
func (a *App) enterRawMode() (func(), error) {
	fd := int(a.In.Fd())
	if !term.IsTerminal(fd) {
		return func() {}, nil
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("terminal: raw mode: %w", err)
	}
	return func() { _ = term.Restore(fd, state) }, nil
}

func (a *App) windowSize() (pty.Size, error) {
	cols, rows, err := term.GetSize(int(a.Out.Fd()))
	if err != nil {
		return pty.Size{}, err
	}
	return pty.Size{Rows: uint16(rows), Cols: uint16(cols)}, nil
}

// watchResize forwards our own window-size changes to the shell's PTY, so that
// programs running inside it lay out for the right number of columns.
func (a *App) watchResize(sess *shell.Session) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		for range ch {
			if size, err := a.windowSize(); err == nil {
				_ = sess.Resize(size)
				if a.Screen != nil {
					a.Screen.Resize(int(size.Rows), int(size.Cols))
				}
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(ch)
	}
}
