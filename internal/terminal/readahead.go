package terminal

import (
	"io"
	"sync"
)

// readAhead reads a PTY on a goroutine of its own, so that reading and
// parsing overlap instead of taking turns.
//
// They have to: macOS hands a PTY's output over about a kilobyte per read,
// and a read is a system call through the tty layer. Done one after the other,
// a pane spent as long reading as it did parsing - 236 MB/s of reading and
// ~190 of parsing came out at 105. Overlapped, the slower of the two is the
// limit rather than their sum.
//
// While the parser is busy the reader keeps going and its reads pile up in a
// queue, which the parser then takes whole: a flood is parsed in large pieces,
// and an interactive echo still arrives on its own the moment it is read.
type readAhead struct {
	mu    sync.Mutex
	room  sync.Cond // signalled when the queue is taken, for a reader that is waiting for room
	queue []byte
	err   error // the read error that ended the stream, delivered once the queue is empty

	// ready holds a token whenever there may be something to take, so the
	// consumer never sleeps through a read.
	ready chan struct{}
}

// readAheadMax is how much may wait unparsed. Beyond it the reader stops
// reading, the PTY's buffer fills, and the program writing to it is made to
// wait - the flow control a terminal is supposed to have - rather than memory
// growing for as long as the program can print.
const readAheadMax = 256 << 10

func startReadAhead(r io.Reader) *readAhead {
	ra := &readAhead{ready: make(chan struct{}, 1)}
	ra.room.L = &ra.mu
	go ra.read(r)
	return ra
}

func (ra *readAhead) read(r io.Reader) {
	buf := make([]byte, readBufferSize)
	for {
		n, err := r.Read(buf)
		ra.mu.Lock()
		for len(ra.queue) >= readAheadMax {
			ra.room.Wait()
		}
		ra.queue = append(ra.queue, buf[:n]...)
		ra.err = err
		ra.mu.Unlock()
		select {
		case ra.ready <- struct{}{}:
		default:
		}
		if err != nil {
			return
		}
	}
}

// next waits for output and returns everything read since it was last
// called, or the error that ended the stream once there is nothing left. spare
// is the buffer the previous call returned, handed back to be reused: the two
// take turns, so a steady stream allocates nothing.
func (ra *readAhead) next(spare []byte) ([]byte, error) {
	for {
		ra.mu.Lock()
		if len(ra.queue) > 0 {
			data := ra.queue
			ra.queue = spare[:0]
			ra.room.Signal()
			ra.mu.Unlock()
			return data, nil
		}
		if err := ra.err; err != nil {
			ra.mu.Unlock()
			return nil, err
		}
		ra.mu.Unlock()
		<-ra.ready
	}
}
