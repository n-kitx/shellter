package parser

import (
	"fmt"
	"unicode/utf8"
)

// State is the parser's position in the terminal protocol.
//
//	               ESC              '['
//	┌────────┐  ────────►  ┌───────┐ ────► ┌─────┐
//	│ Ground │             │ Escape│       │ CSI │
//	└────────┘  ◄────────  └───────┘ ◄──── └─────┘
//	     ▲       final byte    │      final byte
//	     │                     │ ']'
//	     │                 ┌───▼───┐
//	     └───── BEL/ST ────│  OSC  │
//	                       └───────┘
type State int

const (
	// StateGround: ordinary output. Bytes are characters until proven otherwise.
	StateGround State = iota
	// StateEscape: we saw ESC and are waiting to learn what kind of sequence
	// this is.
	StateEscape
	// StateCSI: we saw "ESC [" and are collecting parameters until the final
	// byte arrives.
	StateCSI
	// StateOSC: we saw "ESC ]" and are collecting a string payload until it is
	// terminated.
	StateOSC
)

func (s State) String() string {
	switch s {
	case StateGround:
		return "Ground"
	case StateEscape:
		return "Escape"
	case StateCSI:
		return "CSI"
	case StateOSC:
		return "OSC"
	default:
		return fmt.Sprintf("State(%d)", int(s))
	}
}

const (
	esc = 0x1b // ESC, starts every escape sequence
	bel = 0x07 // BEL, one of the two ways to end an OSC string
	can = 0x18 // CAN, abandons the sequence in progress
	sub = 0x1a // SUB, same
	del = 0x7f
)

// Limits borrowed from real terminals: a sequence that exceeds them is
// malformed, and we must not let a hostile or buggy program grow our buffers.
const (
	maxParams   = 16
	maxParamVal = 65535
	maxOSCLen   = 4096
)

// Parser is the byte-stream state machine. It is NOT safe for concurrent use;
// one PTY reader owns one Parser.
//
// All the interesting state lives here rather than on the stack, which is
// exactly what makes the parser resumable: Parse can return in the middle of a
// half-read sequence and pick it up on the next call.
type Parser struct {
	state State

	// CSI accumulators.
	private       byte
	params        []int
	param         int
	paramPending  bool // have we seen digits or ';' for the current parameter?
	intermediates []byte

	// OSC accumulators.
	osc    []byte
	oscESC bool // last byte was ESC, so a '\' now would terminate the string

	// Partial UTF-8 sequence carried across Parse calls.
	utf8 []byte
}

// New returns a parser in the Ground state.
func New() *Parser {
	return &Parser{
		params:        make([]int, 0, maxParams),
		intermediates: make([]byte, 0, 4),
	}
}

// State reports where the machine currently is. Useful in tests and when
// reasoning about a sequence split across reads.
func (p *Parser) State() State { return p.state }

// Reset returns the parser to Ground and drops any half-read sequence.
func (p *Parser) Reset() {
	p.state = StateGround
	p.resetSequence()
	p.osc = p.osc[:0]
	p.oscESC = false
	p.utf8 = p.utf8[:0]
}

// Parse feeds a chunk of PTY output through the machine, calling emit once per
// recognised event, in stream order.
//
// The chunk boundaries are meaningless: Parse(a); Parse(b) produces exactly the
// same events as Parse(a+b). That is the whole point of keeping state on the
// Parser. A read() from a PTY returns whatever happened to be in the kernel
// buffer at that instant, so "ESC [ 31 m" really can arrive as three reads.
func (p *Parser) Parse(data []byte, emit func(Event)) {
	for _, b := range data {
		switch p.state {
		case StateGround:
			p.ground(b, emit)
		case StateEscape:
			p.escape(b, emit)
		case StateCSI:
			p.csi(b, emit)
		case StateOSC:
			p.oscString(b, emit)
		}
	}
}

// ParseAll is Parse collected into a slice. Convenient for tests and debugging;
// the streaming callback is what production code should use.
//
// The events are cloned, because collecting them is exactly the case the
// borrowed slices on Event do not survive.
func (p *Parser) ParseAll(data []byte) []Event {
	var events []Event
	p.Parse(data, func(e Event) { events = append(events, e.Clone()) })
	return events
}

// ---------------------------------------------------------------------------
// States
// ---------------------------------------------------------------------------

// ground: ordinary output.
//
//	ESC        -> switch to Escape, emit nothing yet
//	C0 or DEL  -> a control event (\n, \r, \b, ...)
//	otherwise  -> a printable character
func (p *Parser) ground(b byte, emit func(Event)) {
	if b < 0x80 {
		// Anything in the ASCII range cannot be a UTF-8 continuation byte, so
		// a half-decoded rune here was truncated garbage.
		p.dropPartialRune(emit)
	}

	switch {
	case b == esc:
		p.state = StateEscape
		p.resetSequence()
	case b < 0x20 || b == del:
		emit(Event{Kind: EventControl, Rune: rune(b)})
	case b < 0x80:
		emit(Event{Kind: EventPrint, Rune: rune(b)})
	default:
		// Multi-byte UTF-8. Like escape sequences, a character can be split
		// across two reads, so the bytes are buffered until the rune is whole.
		// Character *width* is a later phase; this only decodes.
		p.utf8 = append(p.utf8, b)
		if utf8.FullRune(p.utf8) {
			r, _ := utf8.DecodeRune(p.utf8)
			p.utf8 = p.utf8[:0]
			emit(Event{Kind: EventPrint, Rune: r})
		} else if len(p.utf8) >= utf8.UTFMax {
			p.dropPartialRune(emit)
		}
	}
}

// escape: we have seen ESC. The next byte says what this sequence is.
//
//	'['        -> CSI, the parameterised family
//	']'        -> OSC, a string payload such as a window title
//	0x30-0x7E  -> a complete two-byte escape, e.g. ESC = (keypad mode)
func (p *Parser) escape(b byte, emit func(Event)) {
	switch {
	case b == '[':
		p.state = StateCSI
	case b == ']':
		p.state = StateOSC
		p.osc = p.osc[:0]
		p.oscESC = false
	case b == esc:
		// A fresh ESC restarts the sequence.
		p.resetSequence()
	case b >= 0x20 && b <= 0x2f:
		p.addIntermediate(b)
	case b >= 0x30 && b <= 0x7e:
		// The slices are handed over borrowed, not copied. Cloning them here
		// cost three allocations per escape sequence, which on a screen full of
		// colour was most of the allocations in the program; the consumers all
		// read and return. See the note on Event.
		emit(Event{
			Kind:          EventEscape,
			Intermediates: orNilBytes(p.intermediates),
			Command:       b,
		})
		p.toGround()
	default:
		// A control byte interrupting a sequence is executed immediately by a
		// real terminal, so pass it through and abandon the sequence.
		emit(Event{Kind: EventControl, Rune: rune(b)})
		p.toGround()
	}
}

// csi: we have seen "ESC [". Collect parameters, then emit on the final byte.
//
//	'0'-'9'    -> digits of the current parameter
//	';'        -> end of the current parameter
//	0x3C-0x3F  -> private marker, e.g. the '?' in ESC[?25l
//	0x20-0x2F  -> intermediate byte
//	0x40-0x7E  -> final byte: the command. Emit and return to Ground.
func (p *Parser) csi(b byte, emit func(Event)) {
	switch {
	case b >= '0' && b <= '9':
		if p.param < maxParamVal {
			p.param = p.param*10 + int(b-'0')
			if p.param > maxParamVal {
				p.param = maxParamVal
			}
		}
		p.paramPending = true
	case b == ';':
		p.pushParam()
	case b >= 0x3c && b <= 0x3f:
		p.private = b
	case b >= 0x20 && b <= 0x2f:
		p.addIntermediate(b)
	case b >= 0x40 && b <= 0x7e:
		if p.paramPending {
			p.pushParam()
		}
		emit(Event{
			Kind:          EventCSI,
			Private:       p.private,
			Params:        orNilInts(p.params),
			Intermediates: orNilBytes(p.intermediates),
			Command:       b,
		})
		p.toGround()
	case b == esc:
		p.state = StateEscape
		p.resetSequence()
	case b == can || b == sub:
		// Explicit "forget that sequence" bytes.
		p.toGround()
	default:
		emit(Event{Kind: EventControl, Rune: rune(b)})
	}
}

// oscString: collect the payload until BEL or ST (ESC \) terminates it.
// zsh and oh-my-zsh use these constantly to set the window title.
func (p *Parser) oscString(b byte, emit func(Event)) {
	if p.oscESC {
		p.oscESC = false
		if b == '\\' { // ST: the standard terminator
			p.emitOSC(emit)
			return
		}
		// It was not a terminator, so the ESC starts something new.
		p.emitOSC(emit)
		p.state = StateEscape
		p.escape(b, emit)
		return
	}

	switch {
	case b == bel:
		p.emitOSC(emit)
	case b == esc:
		p.oscESC = true
	case b == can || b == sub:
		p.osc = p.osc[:0]
		p.toGround()
	case len(p.osc) >= maxOSCLen:
		// Runaway string: drop it rather than grow without bound.
		p.osc = p.osc[:0]
		p.toGround()
	default:
		p.osc = append(p.osc, b)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (p *Parser) emitOSC(emit func(Event)) {
	emit(Event{Kind: EventOSC, Data: string(p.osc)})
	p.osc = p.osc[:0]
	p.toGround()
}

func (p *Parser) toGround() {
	p.state = StateGround
	p.resetSequence()
}

func (p *Parser) resetSequence() {
	p.private = 0
	p.params = p.params[:0]
	p.param = 0
	p.paramPending = false
	p.intermediates = p.intermediates[:0]
}

func (p *Parser) pushParam() {
	if len(p.params) < maxParams {
		p.params = append(p.params, p.param)
	}
	p.param = 0
	p.paramPending = false
}

// orNilInts and orNilBytes hand out nil rather than an empty slice, so that
// "this sequence had no parameters" looks the same to a consumer as it always
// has. The parser's buffers are never nil, only empty.
func orNilInts(s []int) []int {
	if len(s) == 0 {
		return nil
	}
	return s
}

func orNilBytes(s []byte) []byte {
	if len(s) == 0 {
		return nil
	}
	return s
}

func (p *Parser) addIntermediate(b byte) {
	if len(p.intermediates) < 4 {
		p.intermediates = append(p.intermediates, b)
	}
}

// dropPartialRune reports an incomplete UTF-8 sequence as the replacement
// character, the same thing terminals show for undecodable input.
func (p *Parser) dropPartialRune(emit func(Event)) {
	if len(p.utf8) == 0 {
		return
	}
	p.utf8 = p.utf8[:0]
	emit(Event{Kind: EventPrint, Rune: utf8.RuneError})
}
