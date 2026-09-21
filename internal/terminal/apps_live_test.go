package terminal

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ankitthakur/shellter/internal/input"
	"github.com/ankitthakur/shellter/internal/render"
	"github.com/ankitthakur/shellter/internal/screen"
)

// Phase 9, tested the only way that means anything: by running the real
// programs. Every assertion here is about what a genuine vim, less, top or git
// put on our virtual screen, through a real PTY, with our own keystrokes going
// back the other way.

// needs skips a test when the program is not installed.
func needs(t *testing.T, program string) {
	t.Helper()
	if _, err := exec.LookPath(program); err != nil {
		t.Skipf("%s is not installed", program)
	}
}

// inDir moves the shell to dir and waits for the next prompt.
func (h *harness) inDir(dir string) {
	before := countPrompts(h.text())
	h.typeText("cd " + dir)
	h.press(input.Key{Code: input.CodeReturn})
	h.waitFor("the prompt after cd", func(s string) bool { return countPrompts(s) > before })
}

// run types a command and presses Return, without waiting for it to finish.
func (h *harness) run(command string) {
	h.typeText(command)
	h.press(input.Key{Code: input.CodeReturn})
}

// hasLine reports whether any line on the screen ends with want. Trailing
// blanks are trimmed off the screen's lines, so "commit number 1" must not be
// allowed to match "commit number 12".
func hasLine(screen, want string) bool {
	for _, line := range strings.Split(screen, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), want) {
			return true
		}
	}
	return false
}

// bigFile writes a numbered file that is taller than the screen.
func bigFile(t *testing.T, lines int) string {
	t.Helper()
	dir := t.TempDir()
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		b.WriteString("line-")
		b.WriteString(itoa(i))
		b.WriteString("\n")
	}
	path := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

// --- less -----------------------------------------------------------------

// The headline of this phase: a program borrows the screen and gives it back
// untouched.
func TestLessTakesTheScreenAndGivesItBack(t *testing.T) {
	needs(t, "less")
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })

	path := bigFile(t, 200)
	h.run("less " + path)

	h.waitUntil("less to switch to the alternate screen", h.alt)
	h.waitFor("the top of the file", func(s string) bool { return strings.Contains(s, "line-1\n") })

	// The shell's output is gone from the screen while less is running...
	if strings.Contains(h.text(), "PROMPT>") {
		t.Fatalf("the shell's prompt is still visible under less:\n%s", h.text())
	}

	h.typeText("q")
	h.waitUntil("less to hand the screen back", func() bool { return !h.alt() })

	// ...and back once it leaves, which is the whole point of the alternate
	// screen: quitting less does not leave the file behind.
	h.waitFor("the shell's screen to return", func(s string) bool {
		return strings.Contains(s, "PROMPT>") && !strings.Contains(s, "line-50")
	})
}

func TestLessPagesThroughAFile(t *testing.T) {
	needs(t, "less")
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })

	h.run("less " + bigFile(t, 200))
	h.waitFor("the top of the file", func(s string) bool { return strings.Contains(s, "line-1\n") })

	// Page Down is "ESC [ 6 ~", which less only understands because the key
	// encoder sends the sequence its terminfo entry describes.
	h.press(input.Key{Code: input.CodePageDown})
	h.waitFor("the second page", func(s string) bool { return strings.Contains(s, "line-30") })

	h.press(input.Key{Code: input.CodePageUp})
	h.waitFor("the first page again", func(s string) bool { return strings.Contains(s, "line-1\n") })

	h.typeText("q")
	h.waitUntil("less to exit", func() bool { return !h.alt() })
}

// less lays its page out to the window size, which it gets from the kernel
// (TIOCGWINSZ) rather than by asking the terminal - so this checks the size we
// set on the PTY and the size of our own grid are the same 24 rows. They are
// two separate pieces of state, and a terminal where they disagree draws a page
// that is subtly too long or too short.
func TestLessFillsTheWindow(t *testing.T) {
	needs(t, "less")
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })

	h.run("less " + bigFile(t, 200))
	h.waitFor("the top of the file", func(s string) bool { return strings.Contains(s, "line-1\n") })

	// 24 rows: 23 lines of the file, then the status line.
	h.waitFor("a full screen of the file", func(s string) bool {
		return strings.Contains(s, "line-23")
	})
	if strings.Contains(h.text(), "line-24") {
		t.Fatalf("less drew %d lines; it has the wrong idea of the window height:\n%s",
			strings.Count(h.text(), "line-"), h.text())
	}

	h.typeText("q")
	h.waitUntil("less to exit", func() bool { return !h.alt() })
}

// --- vim ------------------------------------------------------------------

func TestVimEditsAndWritesAFile(t *testing.T) {
	needs(t, "vim")
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })

	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	h.inDir(dir)

	// -u NONE keeps the test independent of whatever is in the user's vimrc,
	// and a bare filename keeps vim's "[New File]" message on one line.
	h.run("vim -u NONE note.txt")
	h.waitUntil("vim to take the screen", h.alt)
	h.waitFor("vim's empty buffer", func(s string) bool { return strings.Contains(s, "~") })

	h.typeText("iShellter runs vim")
	h.press(input.Key{Code: input.CodeEscape})
	h.typeText(":wq")
	h.press(input.Key{Code: input.CodeReturn})

	h.waitUntil("vim to exit", func() bool { return !h.alt() })

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("vim did not write the file: %v", err)
	}
	if strings.TrimSpace(string(got)) != "Shellter runs vim" {
		t.Fatalf("file = %q, want what we typed", got)
	}
}

// vim fills the unused part of the window with '~' and keeps a status line at
// the bottom. Getting that picture right needs the alternate screen, absolute
// cursor addressing and erase-to-end-of-line all behaving.
func TestVimDrawsItsOwnScreen(t *testing.T) {
	needs(t, "vim")
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })

	path := bigFile(t, 5)
	// From the file's own directory: vim announces the file it opened, and a
	// long temporary path would wrap that message onto a second line and stop
	// for "Press ENTER".
	h.inDir(filepath.Dir(path))

	// The ruler is vim's own status line, drawn at a fixed place on the last
	// row - which only lands correctly if absolute cursor addressing does.
	h.run("vim -u NONE -c 'set ruler' big.txt")
	h.waitUntil("vim to take the screen", h.alt)
	h.waitFor("the file's contents", func(s string) bool { return strings.Contains(s, "line-5") })

	h.waitFor("vim's filler column", func(s string) bool {
		tildes := 0
		for _, line := range strings.Split(s, "\n") {
			if strings.TrimSpace(line) == "~" {
				tildes++
			}
		}
		// 24 rows, 5 lines of file, one status line: the rest are tildes.
		return tildes > 10
	})

	// Arrow keys move vim's cursor, and the ruler follows: "3,1" is line three,
	// column one. Nothing but a working key encoder and a redrawn status line
	// can produce that.
	h.press(input.Key{Code: input.CodeDown})
	h.press(input.Key{Code: input.CodeDown})
	h.waitFor("vim's ruler to follow the cursor", func(s string) bool {
		return strings.Contains(s, "3,1")
	})

	h.typeText(":q!")
	h.press(input.Key{Code: input.CodeReturn})
	h.waitUntil("vim to exit", func() bool { return !h.alt() })
}

// --- nano -----------------------------------------------------------------

// ctrl builds the keystroke for a Ctrl chord, exactly as keyDown: reports one:
// the control code as the character, the plain letter as the base, and the
// modifier alongside. Clearing bits 5 and 6 of the letter is what Ctrl has
// meant since the teletype - 'o' 0x6f becomes 0x0f.
func ctrl(letter rune) input.Key {
	return input.Key{Chars: []rune{letter &^ 0x60}, Base: letter, Mods: input.ModControl}
}

// lineWith returns the row number of the first line containing want, and the
// line itself, so a test can then ask about the cells in it.
func lineWith(t *testing.T, f render.Frame, want string) (int, string) {
	t.Helper()
	for row := 0; row < f.Rows; row++ {
		line := make([]rune, f.Cols)
		for col := 0; col < f.Cols; col++ {
			line[col] = f.CellAt(row, col).Rune
		}
		if strings.Contains(string(line), want) {
			return row, string(line)
		}
	}
	t.Fatalf("no line on screen contains %q", want)
	return 0, ""
}

// nano is the only program here driven entirely by control characters, and the
// only one that keeps a reverse-video footer pinned to the bottom rows. vim
// proves neither: its commands are ordinary letters, and its status line is
// drawn in plain text.
//
// On macOS "nano" is UW PICO wearing the name. The shortcuts and the footer are
// the same either way, so this asks for the behaviour and not the banner.
func TestNanoTakesCtrlChordsAndDrawsAnInverseFooter(t *testing.T) {
	needs(t, "nano")
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })

	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	h.inDir(dir)

	h.run("nano note.txt")
	h.waitUntil("nano to take the screen", h.alt)

	// The footer names its own shortcuts on the last two rows. Finding it means
	// those rows were addressed absolutely and erased to the end of the line
	// while the rest of the screen stayed put.
	h.waitFor("nano's shortcut footer", func(s string) bool {
		return strings.Contains(s, "Exit") && strings.Contains(s, "Get Help")
	})

	// Each hint is drawn "^X" in reverse video with its label in plain text.
	// Nothing else in these tests checks that a program can hold an inverse
	// region steady on a fixed row.
	h.mu.Lock()
	frame := DefaultPalette().Frame(h.scr.Snapshot())
	h.mu.Unlock()

	p := DefaultPalette()
	row, line := lineWith(t, frame, "Exit")
	at := strings.Index(line, "^X")
	if at < 0 {
		t.Fatalf("the Exit hint does not name its key; the line was %q", line)
	}
	if c := frame.CellAt(row, at); c.FG != p.Background || c.BG != p.Foreground {
		t.Fatalf("the ^X hint should be inverse, got fg %06x bg %06x", c.FG, c.BG)
	}
	// ...and the label beside it must not be, or the whole row would be one
	// inverse block and the check above would prove nothing.
	if c := frame.CellAt(row, at+3); c.FG != p.Foreground || c.BG != p.Background {
		t.Fatalf("the Exit label should be plain, got fg %06x bg %06x", c.FG, c.BG)
	}

	h.typeText("Shellter runs nano")

	// Ctrl+O writes the file, and nano asks which name to use. The one it
	// already has is offered, so Return accepts it.
	h.press(ctrl('o'))
	h.waitFor("nano's filename prompt", func(s string) bool {
		return strings.Contains(s, "note.txt") && strings.Contains(s, "File Name")
	})
	h.press(input.Key{Code: input.CodeReturn})

	// Ctrl+X leaves. The buffer was saved a moment ago, so nothing is asked.
	h.press(ctrl('x'))
	h.waitUntil("nano to give the screen back", func() bool { return !h.alt() })

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("nano did not write the file: %v", err)
	}
	if strings.TrimSpace(string(got)) != "Shellter runs nano" {
		t.Fatalf("file = %q, want what we typed", got)
	}
}

// --- top ------------------------------------------------------------------

// top keeps a header still while the process list underneath it changes, which
// is what a scroll region is for.
func TestTopDrawsAHeaderAndLeavesCleanly(t *testing.T) {
	needs(t, "top")
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })

	h.run("top -s 1")
	h.waitFor("top's header", func(s string) bool { return strings.Contains(s, "Processes:") })
	h.waitFor("top's process table", func(s string) bool { return strings.Contains(s, "PID") })

	h.typeText("q")
	h.waitFor("the shell to come back", func(s string) bool {
		return strings.Contains(s, "PROMPT>") && !strings.Contains(s, "Processes:")
	})
}

// --- git ------------------------------------------------------------------

// gitRepo builds a small repository with enough history to fill a screen.
func gitRepo(t *testing.T, commits int) string {
	t.Helper()
	dir := t.TempDir()
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=Shellter", "GIT_AUTHOR_EMAIL=shellter@example.com",
		"GIT_COMMITTER_NAME=Shellter", "GIT_COMMITTER_EMAIL=shellter@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	for i := 1; i <= commits; i++ {
		name := "file" + itoa(i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("contents\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", name)
		git("commit", "-q", "-m", "commit number "+itoa(i))
	}
	return dir
}

// git sends its output through a pager, so this exercises the same paging less
// does, and the coloured output git produces when it sees a terminal.
func TestGitLogPagesAndColours(t *testing.T) {
	needs(t, "git")
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })
	h.inDir(gitRepo(t, 40))

	h.run("git -c color.ui=always log --oneline")
	h.waitFor("the newest commits", func(s string) bool { return strings.Contains(s, "commit number 40") })

	// A screenful, not the lot: the pager stopped at the bottom of the window.
	if hasLine(h.text(), "commit number 1") {
		t.Fatalf("the whole log arrived at once; the pager is not paging:\n%s", h.text())
	}

	// git paints the abbreviated hash in colour when it is talking to a
	// terminal, and it reaches us as an attribute on the cell, not as text.
	h.mu.Lock()
	coloured := colouredCells(h.scr)
	h.mu.Unlock()
	if coloured == 0 {
		t.Fatal("git's coloured output arrived with no colour on any cell")
	}

	h.press(input.Key{Code: input.CodePageDown})
	h.waitFor("an older page", func(s string) bool { return hasLine(s, "commit number 1") })

	h.typeText("q")
	h.waitFor("the shell to come back", func(s string) bool {
		return strings.Count(s, "PROMPT>") >= 2
	})
}

// colouredCells counts the cells carrying a foreground colour other than the
// default.
func colouredCells(s *screen.Screen) int {
	rows, cols := s.Size()
	n := 0
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			if s.Cell(row, col).Attr.FG.Kind != screen.ColorDefault {
				n++
			}
		}
	}
	return n
}

// --- answering queries ----------------------------------------------------

// The reply channel, end to end and with nothing faked: the shell asks for the
// cursor position, our screen queues the answer, the harness writes it back up
// the PTY, and zsh's line editor receives it as if it had been typed - which is
// exactly how a terminal answers a question.
//
// The shell reads the answer back and prints the coordinates out of it, so the
// assertion is on the exact position we reported: the command sits on row 2,
// column 1, and that is what has to come back.
//
//	printf '\033[6n'   ask
//	read -d R pos      read the answer up to its final 'R'
//	print "CPR-2;1"    what the shell saw, with the ESC and '[' dropped
func TestCursorPositionReportReachesTheShell(t *testing.T) {
	h := newHarness(t)
	h.waitFor("the first prompt", func(s string) bool { return countPrompts(s) >= 1 })

	h.run(`printf '\033[6n'; read -s -t 2 -d R pos; print "CPR-${pos:2}"`)

	h.waitFor("the shell to read back our answer", func(s string) bool {
		return strings.Contains(s, "CPR-2;1")
	})
}
