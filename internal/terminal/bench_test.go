package terminal

import (
	"strings"
	"testing"

	"github.com/ankitthakur/shellter/internal/parser"
	"github.com/ankitthakur/shellter/internal/screen"
)

// Phase 10 baseline for the whole pipeline: the work one redraw costs, and the
// work one chunk of PTY output costs.

func colouredOutput() []byte {
	var b strings.Builder
	for i := 0; i < 500; i++ {
		b.WriteString("\x1b[0m\x1b[1;34mdirectory\x1b[0m  \x1b[32mexecutable\x1b[0m  plain.txt\r\n")
	}
	return []byte(b.String())
}

// BenchmarkFrame is what the UI thread does on every redraw: snapshot the
// screen under the lock, then resolve every cell's colours.
func BenchmarkFrame(b *testing.B) {
	g := newTestGUI(24, 80)
	parser.New().Parse(colouredOutput(), g.screen().Apply)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = g.frame()
	}
}

// BenchmarkPumpChunk is the read loop's share: parse a chunk into the screen,
// with the lock traffic a real pump has.
func BenchmarkPumpChunk(b *testing.B) {
	data := colouredOutput()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g := newTestGUI(24, 80)
		p := parser.New()
		g.mu.Lock()
		p.Parse(data, g.screen().Apply)
		_ = g.screen().TakeReplies()
		g.mu.Unlock()
	}
}

// The palette alone, separated from the snapshot, to see which half costs what.
func BenchmarkPaletteFrame(b *testing.B) {
	g := newTestGUI(24, 80)
	parser.New().Parse(colouredOutput(), g.screen().Apply)
	snap := g.screen().Snapshot()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = g.Palette.Frame(snap)
	}
}

var _ = screen.DefaultScrollback
