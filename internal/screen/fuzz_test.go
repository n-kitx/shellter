package screen

import "testing"

// Whatever a program prints, and however the window is resized around it, the
// screen does not panic, keeps its cursor on the grid, and can still be drawn,
// searched and saved.
//
//	go test ./internal/screen -fuzz FuzzScreen
func FuzzScreen(f *testing.F) {
	for _, seed := range []string{
		"hello\r\nworld",
		"\x1b[1;31mred\x1b[0m\x1b[38;2;1;2;3mx\x1b[48;5;200my",
		"\x1b[?1049h\x1b[10;5r\x1b[5;1H\x1b[3L\x1b[2M\x1b[?1049l",
		"\x1b[65535;65535H\x1b[65535@\x1b[65535P\x1b[65535X\x1b[65535b",
		"\xe4\xb8\xad\xe6\x96\x87\xcc\x81\xf0\x9f\x98\x80\x1b[1;79H\xe4\xb8\xad",
		"\x1b]7;file://host/tmp\x07\x1b]0;t\x07\x1b[22t\x1b[23t",
		"\x1b[5 q\x1b=\x1b[?66h\x1b[?2026h\x1b#8",
	} {
		f.Add([]byte(seed), uint8(3), uint8(7))
	}
	f.Fuzz(func(t *testing.T, data []byte, rows, cols uint8) {
		s := NewWithScrollback(5, 12, 30)
		feed(s, string(data))
		check(t, s, data)

		s.Resize(int(rows%20)+1, int(cols%40)+1)
		check(t, s, data)
		feed(s, string(data))
		check(t, s, data)

		s.ScrollBy(int(rows) % 40)
		_ = s.Snapshot()
		_ = s.Text(Pos{Line: s.FirstLine()}, Pos{Line: s.TotalLines()})

		// What is saved for a session can be put back.
		restored := NewWithScrollback(5, 12, 30)
		feed(restored, s.Transcript(100))
		check(t, restored, data)
	})
}

func check(t *testing.T, s *Screen, data []byte) {
	t.Helper()
	rows, cols := s.Size()
	if c := s.Cursor(); c.Row < 0 || c.Row >= rows || c.Col < 0 || c.Col >= cols {
		t.Fatalf("cursor %d,%d off a %dx%d grid after %q", c.Row, c.Col, rows, cols, data)
	}
	snap := s.Snapshot()
	if len(snap.Cells) != rows*cols {
		t.Fatalf("a %dx%d snapshot has %d cells after %q", rows, cols, len(snap.Cells), data)
	}
	_ = s.TakeReplies()
}
