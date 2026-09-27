package shell

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thakurankit7/shellter/internal/pty"
)

// A non-interactive one-shot shell: enough to prove the session starts a real
// zsh, that we can read what it printed, and that we see its exit status.
func testConfig(script string) Config {
	cfg := DefaultConfig()
	cfg.Args = []string{"-c", script}
	cfg.Size = pty.Size{Rows: 24, Cols: 80}
	return cfg
}

func TestSessionRunsShellCommand(t *testing.T) {
	s, err := Start(testConfig("echo shellter-ok"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if s.Pid() <= 0 {
		t.Fatalf("expected a pid, got %d", s.Pid())
	}
	out, err := io.ReadAll(s)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(out), "shellter-ok") {
		t.Fatalf("output %q missing marker", out)
	}
	if code, err := s.Wait(); err != nil || code != 0 {
		t.Fatalf("wait: code=%d err=%v", code, err)
	}
}

func TestSessionReportsFailingCommand(t *testing.T) {
	s, err := Start(testConfig("exit 7"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _ = io.ReadAll(s)

	code, err := s.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
}

func TestStartRejectsEmptyPath(t *testing.T) {
	if _, err := Start(Config{}); err == nil {
		t.Fatal("expected an error for an empty shell path")
	}
}

// Closing twice happens on every normal exit: the window's close handler closes
// the session, and Run closes it again on the way out.
func TestCloseIsIdempotent(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Args = []string{"-f", "-c", "exit 0"}
	sess, err := Start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := sess.Close(); err != nil {
			t.Fatalf("close %d: %v", i+1, err)
		}
	}
}

// The shell's environment says what terminal it is in, whatever terminal (or
// none) Shellter itself was started from.
func TestEnvironment(t *testing.T) {
	locale := func() string { return "en_IN.UTF-8" }
	lookup := func(env []string, key string) (vals []string) {
		for _, kv := range env {
			if k, v, _ := strings.Cut(kv, "="); k == key {
				vals = append(vals, v)
			}
		}
		return vals
	}

	// Started from iTerm: its TERM is replaced, not joined by a second one.
	env := environment([]string{"TERM=xterm-kitty", "HOME=/h", "TERM=screen"}, locale)
	if got := lookup(env, "TERM"); len(got) != 1 || got[0] != "xterm-256color" {
		t.Fatalf("TERM = %q", got)
	}
	if got := lookup(env, "COLORTERM"); len(got) != 1 || got[0] != "truecolor" {
		t.Fatalf("COLORTERM = %q", got)
	}
	if got := lookup(env, "HOME"); len(got) != 1 || got[0] != "/h" {
		t.Fatalf("HOME was disturbed: %q", got)
	}

	// Another terminal's name for itself goes; Shellter's replaces it.
	env = environment([]string{"TERM_PROGRAM=iTerm.app", "ITERM_SESSION_ID=w0t0p0",
		"KITTY_WINDOW_ID=1", "KITTYCAT=1", "LC_TERMINAL=iTerm2"}, locale)
	if got := lookup(env, "TERM_PROGRAM"); len(got) != 1 || got[0] != "Shellter" {
		t.Fatalf("TERM_PROGRAM = %q", got)
	}
	if got := lookup(env, "TERM_PROGRAM_VERSION"); len(got) != 1 || got[0] != Version {
		t.Fatalf("TERM_PROGRAM_VERSION = %q", got)
	}
	for _, k := range []string{"ITERM_SESSION_ID", "KITTY_WINDOW_ID", "LC_TERMINAL"} {
		if got := lookup(env, k); len(got) != 0 {
			t.Fatalf("%s survived as %q", k, got)
		}
	}
	if got := lookup(env, "KITTYCAT"); len(got) != 1 {
		t.Fatalf("a prefix match took an unrelated variable: %q", got)
	}

	// Started from the Dock: no locale, so the system's is supplied.
	env = environment([]string{"TERM=xterm-kitty", "HOME=/h", "TERM=screen"}, locale)
	if got := lookup(env, "LANG"); len(got) != 1 || got[0] != "en_IN.UTF-8" {
		t.Fatalf("LANG = %q", got)
	}

	// A locale the user chose is theirs, whichever variable they chose it in.
	if got := lookup(environment([]string{"LANG=fr_FR.UTF-8"}, locale), "LANG"); len(got) != 1 || got[0] != "fr_FR.UTF-8" {
		t.Fatalf("the user's LANG became %q", got)
	}
	for _, kv := range []string{"LC_ALL=C", "LC_CTYPE=UTF-8"} {
		if got := lookup(environment([]string{kv}, locale), "LANG"); len(got) != 0 {
			t.Fatalf("with %s, LANG was added as %q", kv, got)
		}
	}
}

func TestSystemLocaleIsUTF8(t *testing.T) {
	if got := systemLocale(); !strings.HasSuffix(got, ".UTF-8") {
		t.Fatalf("systemLocale() = %q", got)
	}
}

// A shell at its prompt is idle; one that has started a job is not, and says
// which job.
func TestRunningNamesTheForegroundJob(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Args = []string{"-f", "-i"} // interactive, so it does job control
	s, err := Start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	defer s.Hangup()
	go io.Copy(io.Discard, s)

	waitFor := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if s.Running() == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("Running() = %q, want %q", s.Running(), want)
	}

	waitFor("")
	s.Write([]byte("sleep 30\r"))
	waitFor("sleep")
	s.Write([]byte{0x03}) // Ctrl+C: back to the prompt
	waitFor("")
}

// Dir follows the shell as it moves, without the shell having to say anything.
func TestDirFollowsTheShell(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Args = []string{"-f", "-i"}
	s, err := Start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	defer s.Hangup()
	go io.Copy(io.Discard, s)

	want, _ := filepath.EvalSymlinks(t.TempDir())
	s.Write([]byte("cd " + want + "\r"))
	deadline := time.Now().Add(5 * time.Second)
	for s.Dir() != want {
		if time.Now().After(deadline) {
			t.Fatalf("Dir() = %q, want %q", s.Dir(), want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
