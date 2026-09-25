// Package pty owns the operating-system layer: it creates a pseudoterminal
// pair and starts a child process attached to the slave side.
//
// It deliberately knows nothing about shells, input handling, escape-sequence
// parsing or rendering. Everything above this package sees a PTY as a
// bidirectional byte stream plus a child-process lifecycle.
//
// macOS/BSD specific behaviour (e.g. read() on the master returning EIO once
// the last slave fd is gone) is contained here so that upper layers can rely
// on plain io semantics.
package pty

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	creack "github.com/creack/pty"
)

// Size is the window size reported to the child process. The kernel stores it
// on the PTY, and delivers SIGWINCH to the foreground process group when it
// changes. Programs like `ls` and `vim` query it via ioctl(TIOCGWINSZ).
type Size struct {
	Rows uint16
	Cols uint16
}

// Process is a child process running on the slave side of a pseudoterminal,
// together with the master side that we (the parent) own.
//
//	our process                      child process
//	  master fd  <---- kernel ---->  slave fd == stdin/stdout/stderr
type Process struct {
	master *os.File
	cmd    *exec.Cmd

	closeOnce sync.Once
	closeErr  error
}

// Start creates a PTY pair, wires the child's stdin/stdout/stderr to the slave
// side, makes the child a session leader with the PTY as its controlling
// terminal, and then forks+execs it.
//
// On success the caller owns the returned Process and must Close it.
func Start(cmd *exec.Cmd, size Size) (*Process, error) {
	// 1. Ask the kernel for a new PTY pair: one master fd, one slave fd.
	master, slave, err := creack.Open()
	if err != nil {
		return nil, fmt.Errorf("pty: open: %w", err)
	}
	// From here on, any failure must close both fds.
	defer slave.Close() // the parent never needs the slave; only the child does.

	if size.Rows > 0 && size.Cols > 0 {
		if err := setSize(master, size); err != nil {
			master.Close()
			return nil, err
		}
	}

	// 2. The child's three standard file descriptors all point at the slave.
	//    fd 0 = stdin, fd 1 = stdout, fd 2 = stderr.
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave

	// 3. Terminal semantics: put the child in a new session (Setsid) and make
	//    the PTY its controlling terminal (Setctty on child fd 0). Without
	//    this, job control and signal keys (Ctrl+C -> SIGINT) do not work,
	//    and zsh would not consider itself interactive.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
	cmd.SysProcAttr.Setctty = true
	cmd.SysProcAttr.Ctty = 0

	// 4. fork + exec. After this call the child is a separate process.
	if err := cmd.Start(); err != nil {
		master.Close()
		return nil, fmt.Errorf("pty: start %s: %w", cmd.Path, err)
	}

	// The deferred slave.Close() now runs in the parent: the child has its own
	// duplicated descriptors. Keeping it open here would mean we never see EOF.
	return &Process{master: master, cmd: cmd}, nil
}

// Read reads bytes the child wrote to its stdout/stderr.
//
// On macOS, once the child is gone the master read fails with EIO rather than
// returning 0/EOF. We translate that here so callers can use normal io code.
func (p *Process) Read(b []byte) (int, error) {
	n, err := p.master.Read(b)
	if err != nil && isTerminalHangup(err) {
		return n, io.EOF
	}
	return n, err
}

// Write sends bytes to the child's stdin. The line discipline in the kernel
// sees them first: it is what turns 0x03 into SIGINT for the foreground
// process group, and (in cooked mode) what echoes characters back.
func (p *Process) Write(b []byte) (int, error) {
	return p.master.Write(b)
}

// Resize updates the window size stored on the PTY and causes the kernel to
// send SIGWINCH to the child's foreground process group.
func (p *Process) Resize(size Size) error {
	return setSize(p.master, size)
}

// Pid is the child's process id.
func (p *Process) Pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Signal sends a signal directly to the child process.
func (p *Process) Signal(sig os.Signal) error {
	if p.cmd.Process == nil {
		return errors.New("pty: process not started")
	}
	return p.cmd.Process.Signal(sig)
}

// Hangup sends SIGHUP to the child's whole process group, which is what a real
// terminal does when its window goes away.
//
// The group, not just the shell: if the shell is running vim, killing only the
// shell would leave vim attached to a PTY with nobody on the other end. The
// child was made a session leader at startup (Setsid), so its pid names the
// group, and a negative pid addresses the group as a whole.
//
// Closing the master is not a substitute. A read already blocked on it does not
// necessarily return when it is closed, whereas a shell that exits closes the
// last slave descriptor, and that is what makes the read return EIO.
func (p *Process) Hangup() error {
	pid := p.Pid()
	if pid <= 0 {
		return nil
	}
	err := syscall.Kill(-pid, syscall.SIGHUP)
	if err == syscall.ESRCH {
		return nil // already gone, which is the outcome we wanted
	}
	return err
}

// Foreground returns the process group the terminal is currently giving the
// keyboard to - the job the shell started, or the shell itself when it is
// sitting at a prompt. It is the same question `ps` answers with the "+" in its
// STAT column, asked of the kernel directly.
//
// The descriptor is borrowed through SyscallConn rather than Fd, because Fd
// puts the file back into blocking mode, and the pump's read on it must stay
// interruptible.
func (p *Process) Foreground() (int, error) {
	conn, err := p.master.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("pty: foreground: %w", err)
	}
	var pgid int32
	var ioctlErr syscall.Errno
	err = conn.Control(func(fd uintptr) {
		_, _, ioctlErr = syscall.Syscall(syscall.SYS_IOCTL, fd,
			uintptr(syscall.TIOCGPGRP), uintptr(unsafe.Pointer(&pgid)))
	})
	if err != nil {
		return 0, fmt.Errorf("pty: foreground: %w", err)
	}
	if ioctlErr != 0 {
		return 0, fmt.Errorf("pty: foreground: %w", ioctlErr)
	}
	return int(pgid), nil
}

// Wait blocks until the child exits and returns its exit status. A non-zero
// exit status is not an error: it is reported through the int return.
func (p *Process) Wait() (int, error) {
	err := p.cmd.Wait()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return -1, fmt.Errorf("pty: wait: %w", err)
	}
	return 0, nil
}

// Close releases the master file descriptor. Closing it makes any blocked
// Read return, and sends SIGHUP to the child's session if it is still alive.
// Safe to call more than once.
func (p *Process) Close() error {
	p.closeOnce.Do(func() {
		p.closeErr = p.master.Close()
	})
	return p.closeErr
}

func setSize(f *os.File, size Size) error {
	ws := &creack.Winsize{Rows: size.Rows, Cols: size.Cols}
	if err := creack.Setsize(f, ws); err != nil {
		return fmt.Errorf("pty: setsize: %w", err)
	}
	return nil
}

// isTerminalHangup reports whether err means "the other side of the PTY is
// gone". On Darwin that surfaces as EIO; a closed fd shows up as ErrClosed.
func isTerminalHangup(err error) bool {
	return errors.Is(err, syscall.EIO) || errors.Is(err, os.ErrClosed)
}
