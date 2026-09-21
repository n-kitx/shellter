package render

import (
	"testing"

	"github.com/ankitthakur/shellter/internal/input"
)

// classify is the whole Go side of keyDown:, and it is pure, so it can be
// tested without a window.
func TestClassifyRecognisesSpecialKeys(t *testing.T) {
	cases := []struct {
		name  string
		chars string
		base  string
		want  input.Code
	}{
		{"letter", "a", "a", input.CodeNone},
		{"up", "", "", input.CodeUp},
		{"down", "", "", input.CodeDown},
		{"left", "", "", input.CodeLeft},
		{"right", "", "", input.CodeRight},
		{"return", "\r", "\r", input.CodeReturn},
		{"tab", "\t", "\t", input.CodeTab},
		{"escape", "\x1b", "\x1b", input.CodeEscape},
		{"backspace", "\x7f", "\x7f", input.CodeBackspace},
		{"f1", "", "", input.CodeF1},
		{"f12", "", "", input.CodeF12},
		{"insert", "", "", input.CodeInsert},
		{"forward delete", "", "", input.CodeDelete},
		// F13 is past the keys any terminal encodes, so it is recognised
		// and dropped rather than sent as something else.
		{"f13", "", "", input.CodeOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classify(c.chars, c.base, 0).Code; got != c.want {
				t.Fatalf("code = %d, want %d", got, c.want)
			}
		})
	}
}

func TestClassifyKeepsBothStrings(t *testing.T) {
	// Shift+a: 'A' is what it types, 'A' is the key. Ctrl+C: AppKit folds the
	// control into -characters, but -charactersIgnoringModifiers stays 'c',
	// which is what the encoder needs.
	k := classify("\x03", "c", input.ModControl)
	if k.Base != 'c' {
		t.Fatalf("Base = %q, want 'c'", k.Base)
	}
	if string(k.Chars) != "\x03" {
		t.Fatalf("Chars = %q, want 0x03", k.Chars)
	}
	if !k.Mods.Has(input.ModControl) {
		t.Fatal("modifiers lost")
	}
}

func TestClassifyHandlesEmptyStrings(t *testing.T) {
	// A dead key can report no characters at all.
	if k := classify("", "", 0); k.Base != 0 || len(k.Chars) != 0 {
		t.Fatalf("got %+v, want an empty key", k)
	}
}

func TestClassifyPagingKeys(t *testing.T) {
	cases := []struct {
		base rune
		want input.Code
	}{
		{keyPageUp, input.CodePageUp},
		{keyPageDown, input.CodePageDown},
		{keyHome, input.CodeHome},
		{keyEnd, input.CodeEnd},
	}
	for _, c := range cases {
		if got := classify("", string(c.base), 0).Code; got != c.want {
			t.Fatalf("classify(%U) = %d, want %d", c.base, got, c.want)
		}
	}
}
