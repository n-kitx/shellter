package terminal

import (
	"os"
	"runtime"
	"testing"
	"time"
)

// Opening and closing tabs and splits, over and over, leaves nothing behind:
// no goroutines, no file descriptors, and no memory held by panes that have
// gone.
func TestClosedPanesLeaveNothingBehind(t *testing.T) {
	g := newLiveGUI(t)
	if _, err := g.openTab(); err != nil { // the one that stays
		t.Fatal(err)
	}
	settle := func() {
		for i := 0; i < 3; i++ {
			runtime.GC()
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	round := func() {
		tb, err := g.openTab()
		if err != nil {
			t.Fatal(err)
		}
		g.splitPane(0)
		g.splitPane(1)
		for _, p := range tb.panes() {
			// Some output, so each screen has history to hold on to.
			p.write([]byte("seq 300\r"))
		}
		g.mu.Lock()
		index := len(g.tabs) - 1
		g.mu.Unlock()
		g.closeTab(index)
		waitFor("the tab to close", func() bool {
			g.mu.Lock()
			defer g.mu.Unlock()
			return len(g.tabs) == 1
		})
	}

	round() // warm up: first-use allocations are not leaks
	settle()
	goroutines, fds := runtime.NumGoroutine(), openFDs(t)
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	const rounds = 20
	for i := 0; i < rounds; i++ {
		round()
	}
	settle()
	waitFor("the pumps to stop", func() bool { return runtime.NumGoroutine() <= goroutines })
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	if n := openFDs(t); n > fds {
		t.Errorf("%d file descriptors open after %d rounds, %d before", n, rounds, fds)
	}
	// 60 panes came and went. Each screen alone is tens of kilobytes; if they
	// were being kept, the heap would have grown by megabytes.
	grew := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("heap %d -> %d bytes (%+d) over %d panes", before.HeapAlloc, after.HeapAlloc, grew, 3*rounds)
	if grew > 2<<20 {
		t.Errorf("the heap grew by %d bytes over %d closed panes", grew, 3*rounds)
	}
}

func openFDs(t *testing.T) int {
	// Names only: lstat on the directory's own descriptor fails on macOS.
	d, err := os.Open("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	names, err := d.Readdirnames(-1)
	if err != nil {
		t.Fatal(err)
	}
	return len(names)
}
