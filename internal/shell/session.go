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

	"github.com/ankitthakur/shellter/internal/pty"
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
// login zsh, inheriting our environment with TERM set.
//
// TERM tells the shell and the programs it runs which escape sequences they
// may emit. We do not interpret those sequences yet - in this phase the
// terminal we are running inside does that for us.
func DefaultConfig() Config {
	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		shellPath = "/bin/zsh"
	}
	return Config{
		Path: shellPath,
		Args: []string{"-l"},
		Env:  append(os.Environ(), "TERM=xterm-256color"),
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

// Hangup asks the shell and everything it is running to exit, the way closing
// a terminal window does.
func (s *Session) Hangup() error { return s.proc.Hangup() }

// Close releases the PTY. Call it after Wait, or to force the session down.
func (s *Session) Close() error { return s.proc.Close() }
