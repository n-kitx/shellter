package parser

import (
	"strings"
	"testing"
)

// Phase 10 baseline for the parser: every byte off the PTY goes through it.

func benchParse(b *testing.B, data []byte) {
	b.Helper()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	p := New()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Parse(data, func(Event) {})
	}
}

func BenchmarkParsePlain(b *testing.B) {
	benchParse(b, []byte(strings.Repeat("the quick brown fox jumps over the lazy dog\r\n", 500)))
}

func BenchmarkParseEscapes(b *testing.B) {
	benchParse(b, []byte(strings.Repeat("\x1b[1;34m\x1b[2;10H\x1b[Ktext\x1b[0m", 500)))
}

func BenchmarkParseUTF8(b *testing.B) {
	benchParse(b, []byte(strings.Repeat("日本語のテキストと絵文字 ☃ と combining é\r\n", 200)))
}
