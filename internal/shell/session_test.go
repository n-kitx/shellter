package shell

import (
	"io"
	"strings"
	"testing"

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
