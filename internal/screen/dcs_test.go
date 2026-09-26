package screen

import (
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func replies(s *Screen) string { return string(s.TakeReplies()) }

// XTGETTCAP: known capabilities are answered, unknown ones refused, each on
// its own - and names and values travel hex-encoded both ways.
func TestXTGETTCAP(t *testing.T) {
	s := New(4, 20)
	tn, xyz := hex.EncodeToString([]byte("TN")), hex.EncodeToString([]byte("xyz"))
	feed(s, "\x1bP+q"+tn+";"+xyz+"\x1b\\")
	want := "\x1bP1+r" + tn + "=" + hex.EncodeToString([]byte("xterm-256color")) + "\x1b\\" +
		"\x1bP0+r" + xyz + "\x1b\\"
	if got := replies(s); got != want {
		t.Fatalf("reply = %q\nwant    %q", got, want)
	}
}

func TestDECRQSS(t *testing.T) {
	s := New(10, 20)
	feed(s, "\x1b[3;8r\x1bP$qr\x1b\\")
	if got := replies(s); got != "\x1bP1$r3;8r\x1b\\" {
		t.Fatalf("scroll region reply = %q", got)
	}
	feed(s, "\x1bP$qnonsense\x1b\\")
	if got := replies(s); got != "\x1bP0$r\x1b\\" {
		t.Fatalf("unknown setting reply = %q", got)
	}
}

// Other DCS strings, tmux passthrough included, leave nothing on screen.
func TestUnhandledDCSPrintsNothing(t *testing.T) {
	s := New(2, 20)
	feed(s, "a\x1bPtmux;\x1b\x1b]0;x\x07\x1b\\b")
	assertLine(t, s, 0, "ab")
}

// Synchronised output: set, reset, and reported through DECRQM - which is how
// programs discover it before using it.
func TestSyncOutputAndDECRQM(t *testing.T) {
	s := New(4, 20)
	feed(s, "\x1b[?2026$p")
	if got := replies(s); got != "\x1b[?2026;2$y" {
		t.Fatalf("DECRQM before = %q, want reset (2)", got)
	}
	feed(s, "\x1b[?2026h")
	if !s.Modes().SyncOutput {
		t.Fatal("?2026h did not start synchronised output")
	}
	feed(s, "\x1b[?2026$p\x1b[?9999$p\x1b[4$p")
	if got := replies(s); got != "\x1b[?2026;1$y\x1b[?9999;0$y\x1b[4;2$y" {
		t.Fatalf("DECRQM replies = %q", got)
	}
	feed(s, "\x1b[?2026l")
	if s.Modes().SyncOutput {
		t.Fatal("?2026l did not end it")
	}
}

func TestXTVERSION(t *testing.T) {
	s := New(4, 20)
	feed(s, "\x1b[>q")
	if got := replies(s); got != "\x1bP>|"+Version+"\x1b\\" {
		t.Fatalf("XTVERSION reply = %q", got)
	}
}

// OSC 52 writes reach the clipboard queue; reads are refused.
func TestOSC52(t *testing.T) {
	s := New(4, 20)
	enc := base64.StdEncoding.EncodeToString([]byte("copied from vim"))
	feed(s, "\x1b]52;c;"+enc+"\x07")
	feed(s, "\x1b]52;c;"+base64.RawStdEncoding.EncodeToString([]byte("no pad"))+"\x1b\\")
	feed(s, "\x1b]52;c;?\x07")
	feed(s, "\x1b]52;c;!!!not base64\x07")

	got := s.TakeClipboard()
	if len(got) != 2 || got[0] != "copied from vim" || got[1] != "no pad" {
		t.Fatalf("clipboard = %q", got)
	}
	if replies(s) != "" {
		t.Fatal("a clipboard read was answered")
	}
	if s.TakeClipboard() != nil {
		t.Fatal("the queue was not drained")
	}
}
