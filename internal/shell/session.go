// Package shell turns "a PTY with some process on it" into "a shell session".
//
// It decides which program to run (zsh), with which arguments and environment,
// and exposes the session as a byte stream plus a lifecycle. It knows nothing
// about how those bytes are produced (keyboard, network, test) or consumed
// (stdout today, a parser and renderer later).
package shell

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/n-kitx/shellter/internal/pty"
)

// Config describes the shell process to start.
type Config struct {
	// Path is the shell binary, e.g. /bin/zsh.
	Path string
	// Args are the arguments after argv[0]. "-l" makes zsh a login shell so it
	// picks up the same PATH/env a Terminal.app session would have.
	Args []string
	// Env is the child environment. nil means "inherit ours".
	Env []string
	// Dir is the working directory. "" means "inherit ours".
	Dir string
	// Size is the initial window size reported to the shell.
	Size pty.Size
}

// DefaultConfig returns the configuration Shellter uses today: an interactive
// login shell in the user's home directory, with an environment that says
// what kind of terminal it is running in (see environment).
//
// The home directory is set rather than inherited because what we would
// inherit depends on how we were started: the repository under `go run`, and
// "/" from the Dock. A terminal opens where Terminal.app does.
func DefaultConfig() Config {
	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		shellPath = "/bin/zsh"
	}
	home, _ := os.UserHomeDir() // "" inherits ours, which is the best left
	return Config{
		Path: shellPath,
		Args: []string{"-l"},
		Env:  environment(os.Environ(), systemLocale),
		Dir:  home,
		Size: pty.Size{Rows: 24, Cols: 80},
	}
}

// Session is a running shell attached to a pseudoterminal.
//
// Read returns what the shell (and any command it runs) printed.
// Write delivers keystrokes to the shell.
type Session struct {
	proc *pty.Process
	cfg  Config
}

// Start launches the shell described by cfg on a fresh PTY.
func Start(cfg Config) (*Session, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("shell: no shell path configured")
	}
	cmd := exec.Command(cfg.Path, cfg.Args...)
	cmd.Env = cfg.Env
	cmd.Dir = cfg.Dir

	proc, err := pty.Start(cmd, cfg.Size)
	if err != nil {
		return nil, err
	}
	return &Session{proc: proc, cfg: cfg}, nil
}

// Read reads shell output. It returns io.EOF once the shell is gone.
func (s *Session) Read(b []byte) (int, error) { return s.proc.Read(b) }

// Write sends input to the shell.
func (s *Session) Write(b []byte) (int, error) { return s.proc.Write(b) }

// Resize reports a new window size to the shell.
func (s *Session) Resize(size pty.Size) error { return s.proc.Resize(size) }

// Pid is the shell's process id, useful for `ps` while experimenting.
func (s *Session) Pid() int { return s.proc.Pid() }

// Wait blocks until the shell exits and returns its exit code.
func (s *Session) Wait() (int, error) { return s.proc.Wait() }

// Running names the program the shell is waiting on - "vim", "npm" - or
// returns "" when the shell is at its prompt, which is what makes closing it
// harmless.
//
// The shell was made a session leader, so its pid is its own process group; a
// job it starts gets a group of its own and the terminal is handed to that. A
// foreground group other than the shell's is therefore exactly "something else
// is running".
func (s *Session) Running() string {
	fg, err := s.proc.Foreground()
	if err != nil || fg <= 0 || fg == s.Pid() {
		return ""
	}
	// The group is named after its leader, the program the user typed.
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(fg)).Output()
	name := filepath.Base(strings.TrimSpace(string(out)))
	if err != nil || name == "" || name == "." {
		return "a program"
	}
	return name
}

// Hangup asks the shell and everything it is running to exit, the way closing
// a terminal window does.
func (s *Session) Hangup() error { return s.proc.Hangup() }

// Close releases the PTY. Call it after Wait, or to force the session down.
func (s *Session) Close() error { return s.proc.Close() }
