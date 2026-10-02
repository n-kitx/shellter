package parser

import (
	"reflect"
	"testing"
)

// The parser never panics on anything a program can print, and where a read
// happens to split the bytes makes no difference to what it recognises.
//
//	go test ./internal/parser -fuzz FuzzParse
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"hello\r\n",
		"\x1b[1;31mred\x1b[0m",
		"\x1b[38:2::255:0:0m\x1b[4:3m",
		"\x1b]0;title\x07\x1b]52;c;aGk=\x1b\\",
		"\x1bP+q544e\x1b\\",
		"\x1b_Gf=24;AAAA\x1b\\",
		"\xe4\xb8\xad\xf0\x9f\x98\x80\xff\xc3",
		"\x1b[?1049h\x1b[2J\x1b[H\x1b[?25l",
		"\x1b[999999999999;-1;;;;;;;;;;;;;;;;;;;;;;;;;;H",
	} {
		f.Add([]byte(seed), uint8(3))
	}
	f.Fuzz(func(t *testing.T, data []byte, step uint8) {
		whole := collect(func(p *Parser, emit func(Event)) { p.Parse(data, emit) })
		n := int(step%16) + 1
		pieces := collect(func(p *Parser, emit func(Event)) {
			for i := 0; i < len(data); i += n {
				p.Parse(data[i:min(i+n, len(data))], emit)
			}
		})
		if !reflect.DeepEqual(whole, pieces) {
			t.Fatalf("split every %d bytes, %q parsed differently:\n whole  %v\n pieces %v", n, data, whole, pieces)
		}
		for _, e := range whole {
			if len(e.Params) > maxParams || len(e.Data) > maxStringLen {
				t.Fatalf("an event outgrew the limits: %d params, %d bytes", len(e.Params), len(e.Data))
			}
		}
	})
}

func collect(run func(*Parser, func(Event))) []Event {
	var out []Event
	run(New(), func(e Event) { out = append(out, e.Clone()) })
	return out
}
