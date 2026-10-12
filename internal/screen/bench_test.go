package screen

import (
	"strings"
	"testing"

	"github.com/thakurankit7/shellter/internal/parser"
)

// Phase 10 baseline. These model the three things a terminal actually spends
// its time on: absorbing output, keeping the grid, and handing a copy to the
// renderer. Nothing here is optimised yet - the numbers come first.

// plainOutput is what `cat` of a source file looks like: long lines, no escapes.
func plainOutput() []byte {
	var b strings.Builder
	for i := 0; i < 500; i++ {
		b.WriteString("the quick brown fox jumps over the lazy dog 0123456789\r\n")
	}
	return []byte(b.String())
}

// colouredOutput is what `ls --color` or a build log looks like: short runs of
// text between SGR changes.
func colouredOutput() []byte {
	var b strings.Builder
	for i := 0; i < 500; i++ {
		b.WriteString("\x1b[0m\x1b[1;34mdirectory\x1b[0m  \x1b[32mexecutable\x1b[0m  plain.txt\r\n")
	}
	return []byte(b.String())
}

// fullScreenRedraw is what vim does: absolute cursor moves, erase to end of
// line, and a line of text, over and over.
func fullScreenRedraw() []byte {
	var b strings.Builder
	for frame := 0; frame < 20; frame++ {
		for row := 1; row <= 24; row++ {
			b.WriteString("\x1b[")
			b.WriteString(itoa(row))
			b.WriteString(";1H\x1b[K")
			b.WriteString("line of text in an editor, row ")
			b.WriteString(itoa(row))
		}
	}
	return []byte(b.String())
}

func benchApply(b *testing.B, data []byte) {
	b.Helper()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := New(24, 80)
		p := newParser()
		p.Parse(data, s.Apply)
	}
}

func BenchmarkApplyPlain(b *testing.B) { benchApply(b, plainOutput()) }

// The same work on a terminal that has been open for a while: the scrollback
// ring is full, so every line pushed into it evicts one and can reuse its
// memory. This is the state a real session spends almost all of its time in.
func BenchmarkApplyPlainWarm(b *testing.B) {
	data := plainOutput()
	s := NewWithScrollback(24, 80, 200)
	p := newParser()
	p.Parse(data, s.Apply) // fill the ring

	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Parse(data, s.Apply)
	}
}
func BenchmarkApplyColoured(b *testing.B) { benchApply(b, colouredOutput()) }

// Coloured output on a terminal that has been open a while, like
// BenchmarkApplyPlainWarm.
func BenchmarkApplyColouredWarm(b *testing.B) {
	data := colouredOutput()
	s := NewWithScrollback(24, 80, 200)
	p := newParser()
	p.Parse(data, s.Apply)

	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Parse(data, s.Apply)
	}
}
func BenchmarkApplyRedraw(b *testing.B) { benchApply(b, fullScreenRedraw()) }

// Snapshot runs once per frame - up to 60 times a second while output flows.
func BenchmarkSnapshot(b *testing.B) {
	s := New(24, 80)
	p := newParser()
	p.Parse(colouredOutput(), s.Apply)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Snapshot()
	}
}

// Scrolled back, the snapshot has to compose history with the live grid.
func BenchmarkSnapshotScrolledBack(b *testing.B) {
	s := New(24, 80)
	p := newParser()
	p.Parse(plainOutput(), s.Apply)
	s.ScrollBy(12)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Snapshot()
	}
}

// One line scrolling off the top: the most frequent single operation there is.
func BenchmarkScrollUp(b *testing.B) {
	s := New(24, 80)
	p := newParser()
	p.Parse(plainOutput(), s.Apply)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.shiftUp(0, 23, 1, true)
	}
}

// Dragging a window edge produces a resize per frame.
func BenchmarkResize(b *testing.B) {
	s := New(24, 80)
	p := newParser()
	p.Parse(plainOutput(), s.Apply)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			s.Resize(40, 120)
		} else {
			s.Resize(24, 80)
		}
	}
}

// A resize reflows the whole history, so its cost grows with it. This is the
// worst ordinary case: a full default scrollback of wrapped lines, resized
// back and forth the way dragging a divider does.
func BenchmarkResizeFullHistory(b *testing.B) {
	s := NewWithScrollback(40, 120, DefaultScrollback)
	line := strings.Repeat("0123456789", 18) + "\r\n" // wraps once at 120
	for i := 0; i < DefaultScrollback; i++ {
		feed(s, line)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			s.Resize(40, 119)
		} else {
			s.Resize(40, 120)
		}
	}
}

// newParser is a parser set up the way a pane's is: text in runs.
func newParser() *parser.Parser {
	p := parser.New()
	p.TextRuns = true
	return p
}
