package screen

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/thakurankit7/shellter/internal/parser"
)

// Printing a run of ASCII at once (printASCII, from a parser with TextRuns)
// must leave the screen exactly as printing it a character at a time does:
// every cell, the cursor, the modes, the history and which lines wrapped.

// runsAndChars feeds data to two screens, one through each path, in the same
// chunks, and fails if they differ.
func runsAndChars(t *testing.T, data []byte, rows, cols int, chunk int) {
	t.Helper()
	one, many := NewWithScrollback(rows, cols, 50), NewWithScrollback(rows, cols, 50)
	slow, fast := parser.New(), parser.New()
	fast.TextRuns = true
	for len(data) > 0 {
		n := min(chunk, len(data))
		slow.Parse(data[:n], one.Apply)
		fast.Parse(data[:n], many.Apply)
		data = data[n:]
	}
	// REP repeats the last character printed, so it checks that too.
	slow.Parse([]byte("\x1b[2b"), one.Apply)
	fast.Parse([]byte("\x1b[2b"), many.Apply)

	a, b := one.Snapshot(), many.Snapshot()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("%dx%d, chunks of %d: the screens differ\none at a time:\n%s\nin runs:\n%s",
			rows, cols, chunk, one.String(), many.String())
	}
	if one.ScrollbackLen() != many.ScrollbackLen() {
		t.Fatalf("history: %d lines one at a time, %d in runs", one.ScrollbackLen(), many.ScrollbackLen())
	}
	// The transcript writes wrapped lines as one, so it shows the wrap flags.
	if x, y := one.Transcript(1000), many.Transcript(1000); x != y {
		t.Fatalf("transcripts differ:\n%q\n%q", x, y)
	}
}

func TestTextRunsPrintLikeCharacters(t *testing.T) {
	cases := map[string]string{
		"plain":          "hello world\r\nsecond line\r\n",
		"wraps":          "0123456789abcdefghijklmnopqrstuvwxyz0123456789",
		"exactly fills":  "0123456789\r\n0123456789",
		"after a wrap":   "0123456789X",
		"no autowrap":    "\x1b[?7l0123456789abcdefghij\r\nxy",
		"insert mode":    "abcdefgh\r\x1b[4hXY\x1b[4l",
		"line drawing":   "\x1b(0lqqk\r\nx  x\x1b(B ok",
		"shifted set":    "\x1b)0\x0eqq\x0fqq",
		"over wide":      "中文中文中文\r\nab中文\r\x1b[1Cxy",
		"wide at end":    "abcdefgh中x",
		"colours":        "\x1b[31mred\x1b[42m on green\x1b[0m plain",
		"combining next": "e\xcc\x81 cafe\xcc\x81",
		"scroll region":  "\x1b[2;3r\x1b[2H1\r\n2\r\n3\r\n4 long enough to wrap twice over",
		"utf8 between":   "ab\xc3\xa9cd\xe2\x82\xacef",
		"split utf8":     "x\xe4\xb8",
	}
	for name, c := range cases {
		for _, w := range []int{1, 2, 3, 10, 80} {
			for _, chunk := range []int{1, 3, 7, 1 << 20} {
				t.Run(name, func(t *testing.T) { runsAndChars(t, []byte(c), 4, w, chunk) })
			}
		}
	}
}

// Random bytes biased towards text, controls and the sequences that change how
// text is printed.
func TestTextRunsPrintLikeCharactersRandomly(t *testing.T) {
	pieces := []string{
		"abc", "0123456789", " ", "~", "\r", "\n", "\b", "\t", "\x1b[?7l", "\x1b[?7h",
		"\x1b[4h", "\x1b[4l", "\x1b(0", "\x1b(B", "\x0e", "\x0f", "中", "é", "e\xcc\x81",
		"\x1b[31m", "\x1b[0m", "\x1b[H", "\x1b[3;5H", "\x1b[K", "\x1b[2J", "\x1b[1@", "\x1b[2P",
		"\x1b[2;4r", "\x1b[r", "\xe4", "\x1b[?1049h", "\x1b[?1049l", "\x1b[3b",
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		var data []byte
		for n := r.Intn(40); n > 0; n-- {
			data = append(data, pieces[r.Intn(len(pieces))]...)
		}
		runsAndChars(t, data, 1+r.Intn(6), 1+r.Intn(12), 1+r.Intn(16))
	}
}

// go test ./internal/screen -fuzz FuzzTextRuns
func FuzzTextRuns(f *testing.F) {
	f.Add([]byte("hello\x1b[?7lworld\x1b[4hX中e\xcc\x81"), uint8(3), uint8(5), uint8(4))
	f.Fuzz(func(t *testing.T, data []byte, rows, cols, chunk uint8) {
		runsAndChars(t, data, int(rows%8)+1, int(cols%20)+1, int(chunk%32)+1)
	})
}
