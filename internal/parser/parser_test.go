package parser

import (
	"reflect"
	"strings"
	"testing"
)

// printed collapses a run of print events into a string, so tests can say
// "Hello" instead of five separate assertions.
func printed(events []Event) string {
	var s []rune
	for _, e := range events {
		if e.Kind == EventPrint {
			s = append(s, e.Rune)
		}
	}
	return string(s)
}

func TestPlainText(t *testing.T) {
	got := New().ParseAll([]byte("hello"))
	if len(got) != 5 {
		t.Fatalf("got %d events, want 5: %v", len(got), got)
	}
	for i, want := range "hello" {
		if got[i].Kind != EventPrint || got[i].Rune != want {
			t.Fatalf("event %d = %v, want PRINT %q", i, got[i], want)
		}
	}
}

func TestNewlineIsAControlEvent(t *testing.T) {
	got := New().ParseAll([]byte("hi\nyo"))

	if printed(got) != "hiyo" {
		t.Fatalf("printed %q, want %q", printed(got), "hiyo")
	}
	nl := got[2]
	if nl.Kind != EventControl || nl.Rune != '\n' {
		t.Fatalf("event 2 = %v, want CONTROL LF", nl)
	}
	// The point of the test: \n must never show up as a printable character.
	for _, e := range got {
		if e.Kind == EventPrint && e.Rune == '\n' {
			t.Fatal("newline was emitted as a printable character")
		}
	}
}

func TestCarriageReturnAndBackspace(t *testing.T) {
	got := New().ParseAll([]byte("a\rb\bc"))
	want := []Event{
		{Kind: EventPrint, Rune: 'a'},
		{Kind: EventControl, Rune: '\r'},
		{Kind: EventPrint, Rune: 'b'},
		{Kind: EventControl, Rune: '\b'},
		{Kind: EventPrint, Rune: 'c'},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSimpleCSI(t *testing.T) {
	got := New().ParseAll([]byte("\x1b[31m"))
	want := []Event{{Kind: EventCSI, Params: []int{31}, Command: 'm'}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCSIMultipleParameters(t *testing.T) {
	got := New().ParseAll([]byte("\x1b[10;20H"))
	want := []Event{{Kind: EventCSI, Params: []int{10, 20}, Command: 'H'}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCSIFinalBytesOfThisPhase(t *testing.T) {
	// The parser must recognise the shape of each of these; their meaning is a
	// later phase's problem.
	cases := []struct {
		in     string
		params []int
		cmd    byte
	}{
		{"\x1b[m", nil, 'm'},
		{"\x1b[2J", []int{2}, 'J'},
		{"\x1b[K", nil, 'K'},
		{"\x1b[5A", []int{5}, 'A'},
		{"\x1b[3B", []int{3}, 'B'},
		{"\x1b[7C", []int{7}, 'C'},
		{"\x1b[2D", []int{2}, 'D'},
		{"\x1b[1;1f", []int{1, 1}, 'f'},
		{"\x1b[1;31;42m", []int{1, 31, 42}, 'm'},
	}
	for _, c := range cases {
		got := New().ParseAll([]byte(c.in))
		want := []Event{{Kind: EventCSI, Params: c.params, Command: c.cmd}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%q: got %v, want %v", c.in, got, want)
		}
	}
}

func TestCSIOmittedParameterIsZero(t *testing.T) {
	// ESC[;5H means "default row, column 5". The parser reports the omitted
	// parameter as 0 and lets the consumer decide what the default is.
	got := New().ParseAll([]byte("\x1b[;5H"))
	want := []Event{{Kind: EventCSI, Params: []int{0, 5}, Command: 'H'}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if row := got[0].Param(0, 1); row != 1 {
		t.Fatalf("Param(0, 1) = %d, want the default 1", row)
	}
}

func TestCSIPrivateMarker(t *testing.T) {
	// ESC[?25l (hide cursor) - zsh emits private sequences constantly.
	got := New().ParseAll([]byte("\x1b[?25l"))
	want := []Event{{Kind: EventCSI, Private: '?', Params: []int{25}, Command: 'l'}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTextAroundCSISequences(t *testing.T) {
	got := New().ParseAll([]byte("\x1b[31mHello\x1b[0m"))

	if got[0].Kind != EventCSI || got[0].Command != 'm' || !reflect.DeepEqual(got[0].Params, []int{31}) {
		t.Fatalf("first event = %v, want CSI [31] m", got[0])
	}
	last := got[len(got)-1]
	if last.Kind != EventCSI || last.Command != 'm' || !reflect.DeepEqual(last.Params, []int{0}) {
		t.Fatalf("last event = %v, want CSI [0] m", last)
	}
	if printed(got) != "Hello" {
		t.Fatalf("printed %q, want %q", printed(got), "Hello")
	}
	if len(got) != 7 {
		t.Fatalf("got %d events, want 7 (CSI + 5 prints + CSI): %v", len(got), got)
	}
}

func TestSequenceSplitAcrossWrites(t *testing.T) {
	// The PTY hands us whatever happened to be in the kernel buffer, so a
	// sequence can be torn at any byte. State lives on the Parser precisely so
	// that this is indistinguishable from a single write.
	p := New()
	var got []Event
	// Collected, so cloned: see TestEventCloneCopiesItsSlices.
	emit := func(e Event) { got = append(got, e.Clone()) }

	p.Parse([]byte("\x1b["), emit)
	if len(got) != 0 {
		t.Fatalf("nothing should be emitted yet, got %v", got)
	}
	if p.State() != StateCSI {
		t.Fatalf("state = %v, want CSI", p.State())
	}

	p.Parse([]byte("31"), emit)
	if len(got) != 0 {
		t.Fatalf("still nothing to emit, got %v", got)
	}
	if p.State() != StateCSI {
		t.Fatalf("state = %v, want CSI", p.State())
	}

	p.Parse([]byte("m"), emit)
	want := []Event{{Kind: EventCSI, Params: []int{31}, Command: 'm'}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if p.State() != StateGround {
		t.Fatalf("state = %v, want Ground", p.State())
	}
}

func TestChunkingDoesNotChangeTheEventStream(t *testing.T) {
	input := []byte("\x1b[1;31mred\x1b[0m\r\nplain \x1b[2Jok\x1b]0;title\x07done")
	want := New().ParseAll(input)

	// Every possible split point, then one byte at a time.
	for cut := 0; cut <= len(input); cut++ {
		p := New()
		var got []Event
		// Collected, so cloned: see TestEventCloneCopiesItsSlices.
		emit := func(e Event) { got = append(got, e.Clone()) }
		p.Parse(input[:cut], emit)
		p.Parse(input[cut:], emit)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("split at %d: got %v, want %v", cut, got, want)
		}
	}

	p := New()
	var byteAtATime []Event
	for i := range input {
		p.Parse(input[i:i+1], func(e Event) { byteAtATime = append(byteAtATime, e.Clone()) })
	}
	if !reflect.DeepEqual(byteAtATime, want) {
		t.Fatalf("byte at a time: got %v, want %v", byteAtATime, want)
	}
}

func TestMixedSequencesInOneWrite(t *testing.T) {
	got := New().ParseAll([]byte("\x1b[2J\x1b[10;20Hhi\r\n\x1b[0m!"))
	want := []Event{
		{Kind: EventCSI, Params: []int{2}, Command: 'J'},
		{Kind: EventCSI, Params: []int{10, 20}, Command: 'H'},
		{Kind: EventPrint, Rune: 'h'},
		{Kind: EventPrint, Rune: 'i'},
		{Kind: EventControl, Rune: '\r'},
		{Kind: EventControl, Rune: '\n'},
		{Kind: EventCSI, Params: []int{0}, Command: 'm'},
		{Kind: EventPrint, Rune: '!'},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTwoByteEscape(t *testing.T) {
	// ESC = (keypad application mode) - emitted by zsh on every prompt.
	got := New().ParseAll([]byte("\x1b=x"))
	want := []Event{
		{Kind: EventEscape, Command: '='},
		{Kind: EventPrint, Rune: 'x'},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestOSCString(t *testing.T) {
	// Both terminators: BEL, and ST (ESC \).
	for _, in := range []string{"\x1b]0;my title\x07", "\x1b]0;my title\x1b\\"} {
		got := New().ParseAll([]byte(in))
		want := []Event{{Kind: EventOSC, Data: "0;my title"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%q: got %v, want %v", in, got, want)
		}
	}
}

func TestUTF8SplitAcrossWrites(t *testing.T) {
	// "é" is 0xC3 0xA9. One character, two bytes, possibly two reads.
	p := New()
	var got []Event
	// Collected, so cloned: see TestEventCloneCopiesItsSlices.
	emit := func(e Event) { got = append(got, e.Clone()) }

	p.Parse([]byte{0xc3}, emit)
	if len(got) != 0 {
		t.Fatalf("half a rune should emit nothing, got %v", got)
	}
	p.Parse([]byte{0xa9}, emit)

	want := []Event{{Kind: EventPrint, Rune: 'é'}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestEscapeInterruptsSequence(t *testing.T) {
	// A fresh ESC abandons whatever was being collected.
	got := New().ParseAll([]byte("\x1b[12\x1b[5m"))
	want := []Event{{Kind: EventCSI, Params: []int{5}, Command: 'm'}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestResetClearsPartialSequence(t *testing.T) {
	p := New()
	p.Parse([]byte("\x1b[31"), func(Event) {})
	p.Reset()
	if p.State() != StateGround {
		t.Fatalf("state = %v, want Ground", p.State())
	}
	got := p.ParseAll([]byte("m"))
	want := []Event{{Kind: EventPrint, Rune: 'm'}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParamsDoNotAliasParserState(t *testing.T) {
	// Events outlive the call that produced them; their slices must not be
	// recycled by the next sequence.
	got := New().ParseAll([]byte("\x1b[1;2m\x1b[9;9H"))
	if !reflect.DeepEqual(got[0].Params, []int{1, 2}) {
		t.Fatalf("first event params were overwritten: %v", got[0].Params)
	}
}

// The parser hands out its own buffers rather than copying them for every
// event, so a consumer that keeps an event has to say so. These two tests pin
// down both halves of that contract.
func TestParseAllReturnsIndependentEvents(t *testing.T) {
	got := New().ParseAll([]byte("\x1b[31m\x1b[1;2H\x1b[0m"))
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	if len(got[0].Params) != 1 || got[0].Params[0] != 31 {
		t.Fatalf("first event = %v, want params [31] intact after later sequences", got[0].Params)
	}
	if len(got[1].Params) != 2 || got[1].Params[0] != 1 || got[1].Params[1] != 2 {
		t.Fatalf("second event = %v, want params [1 2]", got[1].Params)
	}
}

func TestEventCloneCopiesItsSlices(t *testing.T) {
	var kept Event
	p := New()
	p.Parse([]byte("\x1b[31m"), func(e Event) { kept = e.Clone() })
	p.Parse([]byte("\x1b[7m"), func(Event) {})

	if len(kept.Params) != 1 || kept.Params[0] != 31 {
		t.Fatalf("kept event = %v, want the clone unaffected by later parsing", kept.Params)
	}
}

// A DCS runs to ST and arrives as one event; BEL inside it is only a byte.
func TestDCSString(t *testing.T) {
	got := New().ParseAll([]byte("a\x1bP+q544e\x1b\\b"))
	if len(got) != 3 || got[1].Kind != EventDCS || got[1].Data != "+q544e" {
		t.Fatalf("got %v, want a, DCS \"+q544e\", b", got)
	}
	got = New().ParseAll([]byte("\x1bPx\x07y\x1b\\"))
	if len(got) != 1 || got[0].Data != "x\x07y" {
		t.Fatalf("BEL ended a DCS: %v", got)
	}
}

// APC, PM and SOS strings vanish: a kitty image must not print as base64.
func TestIgnoredStringsAreSwallowed(t *testing.T) {
	for _, intro := range []string{"\x1b_", "\x1b^", "\x1bX"} {
		got := New().ParseAll([]byte("a" + intro + "Gf=100;iVBORw0KGgo\x1b\\b"))
		if len(got) != 2 || got[0].Rune != 'a' || got[1].Rune != 'b' {
			t.Fatalf("%q string leaked: %v", intro, got)
		}
	}
}

// A string longer than the limit is dropped whole - never spilled as text.
func TestOversizedStringIsSwallowed(t *testing.T) {
	huge := strings.Repeat("A", maxStringLen+10)
	got := New().ParseAll([]byte("\x1b]52;c;" + huge + "\x07z"))
	if len(got) != 1 || got[0].Kind != EventPrint || got[0].Rune != 'z' {
		t.Fatalf("an oversized OSC leaked %d events, first %v", len(got), got[0])
	}
}

// A ':' separates sub-parameters: the SGR forms for underline styles and
// colours. Which parameters followed a ':' is in Sub.
func TestColonSubParameters(t *testing.T) {
	for _, c := range []struct {
		in     string
		params []int
		sub    uint32
	}{
		{"\x1b[4:3m", []int{4, 3}, 0b10},
		{"\x1b[4;3m", []int{4, 3}, 0},
		{"\x1b[38:2::255:128:0m", []int{38, 2, 0, 255, 128, 0}, 0b111110},
		{"\x1b[1;58:5:196;4:0m", []int{1, 58, 5, 196, 4, 0}, 0b101100},
		{"\x1b[4:m", []int{4, 0}, 0b10},
	} {
		var got []Event
		New().Parse([]byte(c.in), func(e Event) { got = append(got, e.Clone()) })
		if len(got) != 1 || got[0].Kind != EventCSI {
			t.Fatalf("%q: %v", c.in, got)
		}
		if !reflect.DeepEqual(got[0].Params, c.params) || got[0].Sub != c.sub {
			t.Errorf("%q: params %v sub %b, want %v %b", c.in, got[0].Params, got[0].Sub, c.params, c.sub)
		}
	}
}
