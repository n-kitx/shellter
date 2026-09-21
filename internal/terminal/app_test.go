package terminal

import (
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/ankitthakur/shellter/internal/parser"
	"github.com/ankitthakur/shellter/internal/screen"
	"github.com/ankitthakur/shellter/internal/shell"
)

// Run is exercised with pipes instead of a real terminal: raw mode is skipped
// when stdin is not a tty, so the test only covers the byte pumping and the
// exit path.
func TestRunPumpsShellOutput(t *testing.T) {
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inW.Close()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	cfg := shell.DefaultConfig()
	cfg.Args = []string{"-c", "echo from-shell"}

	app := &App{In: inR, Out: outW, Config: cfg}

	collected := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(outR)
		collected <- string(b)
	}()

	code, err := app.Run()
	outW.Close()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := <-collected; !strings.Contains(got, "from-shell") {
		t.Fatalf("output %q missing shell output", got)
	}
}

// The parser sits in the output path: whatever the shell prints must show up
// both verbatim on Out and as events on OnEvent.
func TestRunFeedsShellOutputToTheParser(t *testing.T) {
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inW.Close()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	cfg := shell.DefaultConfig()
	// ESC [ 31 m  R E D  ESC [ 0 m
	cfg.Args = []string{"-c", `printf '\033[31mRED\033[0m\n'`}

	var mu sync.Mutex
	var events []parser.Event
	app := &App{
		In:  inR,
		Out: outW,
		OnEvent: func(e parser.Event) {
			mu.Lock()
			// Cloned: an event's Params are only borrowed for the length of the
			// callback, and this test keeps them.
			events = append(events, e.Clone())
			mu.Unlock()
		},
		Config: cfg,
	}

	drained := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(outR)
		drained <- string(b)
	}()

	if _, err := app.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	outW.Close()

	// 1. raw bytes still reach the output unchanged.
	if raw := <-drained; !strings.Contains(raw, "\x1b[31mRED") {
		t.Fatalf("raw output %q lost its escape sequence", raw)
	}

	// 2. the same bytes were understood as events.
	mu.Lock()
	defer mu.Unlock()
	var sawColor, sawReset bool
	var text []rune
	for _, e := range events {
		switch {
		case e.Kind == parser.EventCSI && e.Command == 'm' && e.Param(0, 0) == 31:
			sawColor = true
		case e.Kind == parser.EventCSI && e.Command == 'm' && len(e.Params) == 1 && e.Params[0] == 0:
			sawReset = true
		case e.Kind == parser.EventPrint:
			text = append(text, e.Rune)
		}
	}
	if !sawColor || !sawReset {
		t.Fatalf("missing SGR events (color=%v reset=%v) in %v", sawColor, sawReset, events)
	}
	if !strings.Contains(string(text), "RED") {
		t.Fatalf("printed text %q does not contain RED", string(text))
	}
}

// The screen is fed from the same event stream: after a real zsh prints
// through the PTY, the virtual screen must hold what a terminal would show.
func TestRunUpdatesTheScreen(t *testing.T) {
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inW.Close()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, outR) }()

	cfg := shell.DefaultConfig()
	// Prints "one", then CR LF, then "two" with the cursor walked backwards
	// and a character overwritten: XY should end up as "twp".
	cfg.Args = []string{"-c", `printf 'one\r\ntwo\033[1DX\n'`}

	scr := screen.New(10, 20)
	app := &App{In: inR, Out: outW, Config: cfg, Screen: scr}

	if _, err := app.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	outW.Close()

	if got := scr.Line(0); got != "one" {
		t.Fatalf("line 0 = %q, want %q", got, "one")
	}
	if got := scr.Line(1); got != "twX" {
		t.Fatalf("line 1 = %q, want %q", got, "twX")
	}
	// The program printed a bare \n, but the cursor is at column 0: the PTY's
	// line discipline translates LF into CR LF on output (ONLCR), so the screen
	// never sees a lone LF from a real terminal.
	if got := scr.Cursor(); got.Row != 2 || got.Col != 0 {
		t.Fatalf("cursor = %+v, want {Row:2 Col:0}", got)
	}
}
