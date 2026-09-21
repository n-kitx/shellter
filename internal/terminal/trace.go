package terminal

import (
	"bufio"
	"fmt"
	"os"

	"github.com/ankitthakur/shellter/internal/parser"
)

// TraceEnvVar names a file to write the parser's event stream to. It exists
// because this phase has no consumer for events yet: the trace is how you see
// what the parser makes of real zsh output.
//
//	SHELLTER_TRACE=/tmp/shellter.log go run ./cmd/shellter
//	tail -f /tmp/shellter.log     # in another window
const TraceEnvVar = "SHELLTER_TRACE"

// ScreenEnvVar names a file that receives the final contents of the virtual
// screen when the session ends. Like the trace, it exists only because no
// renderer reads the screen yet.
//
//	SHELLTER_SCREEN=/tmp/screen.txt go run ./cmd/shellter
const ScreenEnvVar = "SHELLTER_SCREEN"

// newTraceSink returns an event handler that appends one line per event to
// path, plus a close function.
//
// Only the output-pump goroutine calls the handler, so no locking is needed.
func newTraceSink(path string) (func(parser.Event), func(), error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("terminal: trace file: %w", err)
	}
	w := bufio.NewWriter(f)
	emit := func(e parser.Event) {
		fmt.Fprintln(w, e)
		// Flushed per event so `tail -f` shows the stream live; this path is
		// only active while debugging.
		_ = w.Flush()
	}
	return emit, func() { _ = w.Flush(); _ = f.Close() }, nil
}
