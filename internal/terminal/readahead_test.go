package terminal

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

// Everything read comes out once, in order, and then the error that ended it.
func TestReadAheadDeliversEverythingThenTheError(t *testing.T) {
	r, w := io.Pipe()
	ra := startReadAhead(r)
	var want bytes.Buffer
	go func() {
		for i := 0; i < 1000; i++ {
			line := []byte("line " + string(rune('a'+i%26)) + "\n")
			want.Write(line)
			w.Write(line)
		}
		w.CloseWithError(io.EOF)
	}()

	var got []byte
	var data []byte
	for {
		var err error
		data, err = ra.next(data)
		got = append(got, data...)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("ended with %v, want EOF", err)
			}
			break
		}
	}
	if !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("got %d bytes, want %d, or out of order", len(got), want.Len())
	}
}

// A consumer that stops taking stops the reader too, once readAheadMax is
// waiting: the program printing is held up rather than memory growing.
func TestReadAheadStopsReadingWhenFull(t *testing.T) {
	r, w := io.Pipe()
	ra := startReadAhead(r)
	chunk := make([]byte, 16<<10)
	written := make(chan int, 1)
	go func() {
		n := 0
		for n < 4*readAheadMax {
			if _, err := w.Write(chunk); err != nil {
				break
			}
			n += len(chunk)
		}
		written <- n
	}()

	time.Sleep(200 * time.Millisecond)
	select {
	case n := <-written:
		t.Fatalf("the writer got %d bytes through with nobody reading", n)
	default:
	}
	ra.mu.Lock()
	queued := len(ra.queue)
	ra.mu.Unlock()
	if queued > readAheadMax+len(chunk) {
		t.Fatalf("%d bytes queued, more than the %d allowed", queued, readAheadMax)
	}

	// Taking what is queued lets it go on.
	var data []byte
	total := 0
	for total < 4*readAheadMax {
		data, _ = ra.next(data)
		total += len(data)
	}
	if n := <-written; n != 4*readAheadMax {
		t.Fatalf("the writer stopped at %d", n)
	}
	w.Close()
}
