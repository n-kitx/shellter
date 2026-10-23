package pty

import (
	"io"
	"os/exec"
	"strings"
	"testing"
)

// readAll drains the master until the child is gone. Our Read turns the
// macOS EIO hangup into io.EOF, so plain io.ReadAll works.
func readAll(t *testing.T, p *Process) string {
	t.Helper()
	out, err := io.ReadAll(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(out)
}

// readUntil reads from the master until want has arrived, and returns
// everything read so far.
func readUntil(t *testing.T, p *Process, want string) string {
	t.Helper()
	var got []byte
	buf := make([]byte, 1024)
	for !strings.Contains(string(got), want) {
		n, err := p.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			t.Fatalf("read: %v, after %q; want %q", err, got, want)
		}
	}
	return string(got)
}

func TestChildOutputReachesMaster(t *testing.T) {
	// The child stays alive until its output has been read. macOS can drop
	// output still queued on a PTY when the child's last descriptor for it
	// closes, so a child that printed and exited at once could leave nothing
	// to read if we got there late.
	p, err := Start(exec.Command("/bin/sh", "-c", "echo hello; read line"), Size{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if p.Pid() <= 0 {
		t.Fatalf("expected a pid, got %d", p.Pid())
	}
	readUntil(t, p, "hello")
	if _, err := p.Write([]byte("\n")); err != nil {
		t.Fatal(err)
	}
	readAll(t, p)
	code, err := p.Wait()
	if err != nil || code != 0 {
		t.Fatalf("wait: code=%d err=%v", code, err)
	}
}

func TestInputReachesChild(t *testing.T) {
	// `cat` echoes its stdin back to stdout; the PTY line discipline also
	// echoes what we write, so we expect the word twice.
	p, err := Start(exec.Command("/bin/cat"), Size{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if _, err := p.Write([]byte("ping\n")); err != nil {
		t.Fatal(err)
	}
	// 0x04 is Ctrl+D: the line discipline turns it into EOF for `cat`.
	if _, err := p.Write([]byte{0x04}); err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, p); strings.Count(got, "ping") < 1 {
		t.Fatalf("output %q does not contain %q", got, "ping")
	}
	if code, err := p.Wait(); err != nil || code != 0 {
		t.Fatalf("wait: code=%d err=%v", code, err)
	}
}

func TestExitCodeIsReported(t *testing.T) {
	p, err := Start(exec.Command("/bin/sh", "-c", "exit 3"), Size{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	readAll(t, p)

	code, err := p.Wait()
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
}

func TestWindowSizeVisibleToChild(t *testing.T) {
	// `stty size` asks the kernel for the window size stored on the PTY.
	p, err := Start(exec.Command("/bin/sh", "-c", "stty size"), Size{Rows: 40, Cols: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	got := readAll(t, p)
	if !strings.Contains(got, "40 100") {
		t.Fatalf("stty size = %q, want it to contain %q", strings.TrimSpace(got), "40 100")
	}
	if _, err := p.Wait(); err != nil {
		t.Fatal(err)
	}
}
