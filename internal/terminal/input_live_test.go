package terminal

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thakurankit7/shellter/internal/input"
	"github.com/thakurankit7/shellter/internal/parser"
	"github.com/thakurankit7/shellter/internal/pty"
	"github.com/thakurankit7/shellter/internal/render"
	"github.com/thakurankit7/shellter/internal/screen"
	"github.com/thakurankit7/shellter/internal/shell"
)

// harness is the GUI's data flow without the window: real zsh, real PTY, real
// parser and screen, keys encoded by package input. It is how the keyboard
// path can be tested end to end on a machine with no display.
type harness struct {
	t    *testing.T
	sess *shell.Session
	mu   sync.Mutex
	scr  *screen.Screen
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	cfg := shell.DefaultConfig()
	// zsh whatever the login shell is, since the tests type zsh. -f skips the
	// startup files so the test does not depend on whatever is in ~/.zshrc,
	// and PS1 gives us a prompt we can recognise.
	cfg.Path = "/bin/zsh"
	cfg.Args = []string{"-f"}
	cfg.Env = append(cfg.Env, "PS1=PROMPT> ")
	cfg.Size = pty.Size{Rows: 24, Cols: 80}

	sess, err := shell.Start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, sess: sess, scr: screen.New(24, 80)}
	t.Cleanup(func() { _ = sess.Close() })

	go func() {
		p := parser.New()
		buf := make([]byte, 4096)
		for {
			n, err := sess.Read(buf)
			if n > 0 {
				h.mu.Lock()
				p.Parse(buf[:n], h.scr.Apply)
				replies := h.scr.TakeReplies()
				h.mu.Unlock()

				// The same answering the GUI does. Without it, a program that
				// asks where the cursor is waits forever - which is exactly
				// what less does on startup.
				if len(replies) > 0 {
					_, _ = sess.Write(replies)
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return h
}

// send writes bytes straight to the PTY, for the times a test wants to say
// something the key encoder would not produce.
func (h *harness) send(s string) {
	if _, err := h.sess.Write([]byte(s)); err != nil && err != io.EOF {
		h.t.Fatalf("write: %v", err)
	}
}

// alt reports whether the program running has taken over the screen.
func (h *harness) alt() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.scr.AltScreen()
}

// waitUntil is waitFor for conditions that are not about the text on screen.
func (h *harness) waitUntil(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %s; screen was:\n%s", what, h.text())
}

// press encodes a key exactly as the window would and writes it to the PTY.
func (h *harness) press(k input.Key) {
	h.mu.Lock()
	mode := input.Mode{ApplicationCursor: h.scr.ApplicationCursorKeys()}
	h.mu.Unlock()

	if b := input.Encode(k, mode); len(b) > 0 {
		if _, err := h.sess.Write(b); err != nil && err != io.EOF {
			h.t.Fatalf("write: %v", err)
		}
	}
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.press(input.Key{Chars: []rune{r}, Base: r})
	}
}

func (h *harness) text() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.scr.String()
}

// waitFor polls the virtual screen until cond holds, which is the only honest
// way to synchronise with another process.
func (h *harness) waitFor(what string, cond func(string) bool) string {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := h.text(); cond(s) {
			return s
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %s; screen was:\n%s", what, h.text())
	return ""
}

func countPrompts(s string) int { return strings.Count(s, "PROMPT>") }

// Typing a command and pressing Return must reach zsh and produce output.
func TestTypingACommandRunsIt(t *testing.T) {
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })

	h.typeText("echo hello-keys")
	h.press(input.Key{Code: input.CodeReturn})

	h.waitFor("the command output", func(s string) bool {
		for _, line := range strings.Split(s, "\n") {
			if strings.TrimSpace(line) == "hello-keys" {
				return true
			}
		}
		return false
	})
}

// Backspace must erase, which means zsh received DEL rather than BS.
func TestBackspaceErasesBeforeTheCommandRuns(t *testing.T) {
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })

	h.typeText("echo backspaceXY")
	h.press(input.Key{Code: input.CodeBackspace})
	h.press(input.Key{Code: input.CodeBackspace})
	h.press(input.Key{Code: input.CodeReturn})

	out := h.waitFor("the corrected output", func(s string) bool {
		for _, line := range strings.Split(s, "\n") {
			if strings.TrimSpace(line) == "backspace" {
				return true
			}
		}
		return false
	})
	if strings.Contains(out, "backspaceXY\n") {
		t.Fatalf("the erased characters still ran:\n%s", out)
	}
}

// Ctrl+C must interrupt a running command. The byte is 0x03; the PTY's line
// discipline turns it into SIGINT for the foreground process group.
func TestCtrlCInterruptsARunningCommand(t *testing.T) {
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })
	before := countPrompts(h.text())

	h.typeText("sleep 45")
	h.press(input.Key{Code: input.CodeReturn})
	// Let the command actually start, otherwise we would interrupt the shell.
	time.Sleep(500 * time.Millisecond)

	h.press(input.Key{Chars: []rune{0x03}, Base: 'c', Mods: input.ModControl})

	// The prompt the command was typed at is already on screen, so the
	// interrupt is visible as one *additional* prompt.
	h.waitFor("a new prompt after the interrupt", func(s string) bool {
		return countPrompts(s) > before
	})
	if !strings.Contains(h.text(), "^C") {
		t.Fatalf("expected the shell to echo ^C; screen was:\n%s", h.text())
	}
}

// Ctrl+D at an empty prompt is end-of-input: the shell exits.
func TestCtrlDEndsTheSession(t *testing.T) {
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })

	h.press(input.Key{Chars: []rune{0x04}, Base: 'd', Mods: input.ModControl})

	done := make(chan int, 1)
	go func() {
		code, _ := h.sess.Wait()
		done <- code
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("the shell did not exit on Ctrl+D; screen was:\n%s", h.text())
	}
}

// Tab must reach zsh as 0x09 and trigger completion.
func TestTabTriggersCompletion(t *testing.T) {
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })

	// With -f there is no completion system loaded, but zsh's default binding
	// still expands a unique filename match.
	h.typeText("ls /usr/bin/zipde")
	h.press(input.Key{Code: input.CodeTab})

	h.waitFor("the completed path", func(s string) bool {
		return strings.Contains(s, "zipdetails")
	})
}

// The Up arrow must recall the previous command, which only works if the bytes
// we send match the mode zsh's line editor put the terminal in.
func TestArrowUpRecallsHistory(t *testing.T) {
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })

	h.typeText("echo arrow-history")
	h.press(input.Key{Code: input.CodeReturn})
	h.waitFor("the first run", func(s string) bool {
		for _, line := range strings.Split(s, "\n") {
			if strings.TrimSpace(line) == "arrow-history" {
				return true
			}
		}
		return false
	})
	runs := strings.Count(h.text(), "arrow-history")

	h.press(input.Key{Code: input.CodeUp})
	h.press(input.Key{Code: input.CodeReturn})

	h.waitFor("the recalled command to run again", func(s string) bool {
		return strings.Count(s, "arrow-history") > runs
	})
}

// Left arrow must move the cursor within the line being edited.
func TestArrowLeftEditsWithinTheLine(t *testing.T) {
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })

	// Type "echo abZc", walk left past the 'c', delete the 'Z', run it.
	h.typeText("echo abZc")
	h.press(input.Key{Code: input.CodeLeft})
	h.press(input.Key{Code: input.CodeBackspace})
	h.press(input.Key{Code: input.CodeReturn})

	h.waitFor("the edited command's output", func(s string) bool {
		for _, line := range strings.Split(s, "\n") {
			if strings.TrimSpace(line) == "abc" {
				return true
			}
		}
		return false
	})
}

// Phase 6 end to end: a real zsh emits SGR, the screen records it, and the
// palette turns it into the pixels the renderer would draw.
func TestColorsSurviveTheWholePipeline(t *testing.T) {
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })

	h.typeText(`printf '\033[1;31mRED\033[0m\033[42mGREEN\033[0m\n'`)
	h.press(input.Key{Code: input.CodeReturn})

	h.waitFor("the coloured output", func(s string) bool {
		return strings.Contains(s, "REDGREEN")
	})

	h.mu.Lock()
	frame := DefaultPalette().Frame(h.scr.Snapshot())
	h.mu.Unlock()

	p := DefaultPalette()
	red, green := findRun(t, frame, "REDGREEN")

	if red.FG != p.ANSI[1] {
		t.Fatalf("R is %06x, want red %06x", red.FG, p.ANSI[1])
	}
	if !red.Flags.Has(render.FlagBold) {
		t.Fatalf("R should be bold, flags = %b", red.Flags)
	}
	if green.BG != p.ANSI[2] {
		t.Fatalf("G background is %06x, want green %06x", green.BG, p.ANSI[2])
	}
	if green.Flags.Has(render.FlagBold) {
		t.Fatal("SGR 0 should have cleared bold before GREEN")
	}
}

// findRun locates "REDGREEN" in the output and returns the cells under 'R' and
// under the 'G' that starts GREEN.
func findRun(t *testing.T, f render.Frame, want string) (render.Cell, render.Cell) {
	t.Helper()
	for row := 0; row < f.Rows; row++ {
		var line []rune
		for col := 0; col < f.Cols; col++ {
			line = append(line, f.CellAt(row, col).Rune)
		}
		if at := strings.Index(string(line), want); at >= 0 {
			// Skip the echoed command line, which contains the literal text.
			if strings.Contains(string(line), "printf") {
				continue
			}
			return f.CellAt(row, at), f.CellAt(row, at+3)
		}
	}
	t.Fatalf("%q not found in the frame", want)
	return render.Cell{}, render.Cell{}
}

// Phase 7 end to end: non-ASCII text survives the whole loop - encoded as UTF-8
// on the way in, decoded by the parser on the way back, and laid out by width.
func TestUnicodeSurvivesTheWholePipeline(t *testing.T) {
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })

	h.typeText("echo 'aé漢字🚀'")
	h.press(input.Key{Code: input.CodeReturn})

	h.waitFor("the echoed text", func(s string) bool {
		for _, line := range strings.Split(s, "\n") {
			if strings.TrimSpace(line) == "aé漢字🚀" {
				return true
			}
		}
		return false
	})

	h.mu.Lock()
	snap := h.scr.Snapshot()
	h.mu.Unlock()

	row := -1
	for r := 0; r < snap.Rows; r++ {
		if strings.TrimSpace(snap.Lines()[r]) == "aé漢字🚀" {
			row = r
			break
		}
	}
	if row < 0 {
		t.Fatalf("output line not found in:\n%s", strings.Join(snap.Lines(), "\n"))
	}

	// a=1 é=1 漢=2 字=2 🚀=2 - eight columns for five characters.
	want := []struct {
		col  int
		text string
	}{
		{0, "a"},
		{1, "é"},
		{2, "漢"},
		{4, "字"},
		{6, "🚀"},
	}
	for _, w := range want {
		if got := snap.CellAt(row, w.col).Text(); got != w.text {
			t.Fatalf("cell(%d,%d) = %q, want %q", row, w.col, got, w.text)
		}
	}
	for _, col := range []int{3, 5, 7} {
		if !snap.CellAt(row, col).IsContinuation() {
			t.Fatalf("cell(%d,%d) should be a continuation half", row, col)
		}
	}
	if got := snap.CellAt(row, 8).Rune; got != ' ' {
		t.Fatalf("cell(%d,8) = %q, want a blank after eight columns", row, got)
	}
}

// Phase 8 end to end: output that scrolls off a real session is still there.
func TestScrollbackHoldsRealShellOutput(t *testing.T) {
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })

	// Far more lines than the 24-row screen can hold.
	h.typeText("for i in $(seq 1 60); do echo line-$i; done")
	h.press(input.Key{Code: input.CodeReturn})

	h.waitFor("the last line", func(s string) bool {
		return strings.Contains(s, "line-60")
	})

	h.mu.Lock()
	defer h.mu.Unlock()

	// The visible screen has scrolled past the beginning.
	if strings.Contains(h.scr.String(), "line-1\n") {
		t.Fatal("line-1 should have scrolled off the visible screen")
	}
	// 60 lines of output plus the prompt and command line, minus the 24 rows
	// still on screen: everything above the fold went to history.
	if got := h.scr.ScrollbackLen(); got < 35 {
		t.Fatalf("ScrollbackLen = %d, want the scrolled-off lines kept", got)
	}

	// Scrolling back finds it again.
	h.scr.ScrollToTop()
	view := strings.Join(h.scr.Snapshot().Lines(), "\n")
	if !strings.Contains(view, "line-1") {
		t.Fatalf("history does not contain line-1:\n%s", view)
	}

	// And the live grid was never disturbed by looking at it.
	if !strings.Contains(h.scr.String(), "line-60") {
		t.Fatal("scrolling changed the live screen")
	}

	h.scr.ScrollToBottom()
	if got := h.scr.Snapshot().Lines(); !strings.Contains(strings.Join(got, "\n"), "line-60") {
		t.Fatal("returning to the bottom did not show the live screen")
	}
}
