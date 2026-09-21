// Package parser turns the raw byte stream coming off a PTY into terminal
// events.
//
// It is deliberately free of any dependency: no PTY, no files, no rendering.
// Bytes go in, events come out. That makes it trivially testable and means the
// layers above (a screen buffer, later a renderer) can be developed against
// events instead of against bytes.
package parser

import "fmt"

// Kind says which shape of event this is. Check Kind first, then read only the
// fields that belong to it.
type Kind int

const (
	// EventPrint is one printable character. Field: Rune.
	EventPrint Kind = iota
	// EventControl is a C0 control byte such as \n, \r or \b. Field: Rune.
	EventControl
	// EventCSI is a parsed "ESC [ ... final" sequence.
	// Fields: Private, Params, Intermediates, Command.
	EventCSI
	// EventEscape is a short escape sequence with no CSI parameters, e.g.
	// ESC = or ESC M. Fields: Intermediates, Command.
	EventEscape
	// EventOSC is an Operating System Command payload, e.g. a window title.
	// Field: Data.
	EventOSC
)

func (k Kind) String() string {
	switch k {
	case EventPrint:
		return "PRINT"
	case EventControl:
		return "CONTROL"
	case EventCSI:
		return "CSI"
	case EventEscape:
		return "ESC"
	case EventOSC:
		return "OSC"
	default:
		return fmt.Sprintf("Kind(%d)", int(k))
	}
}

// Event is one thing the parser recognised in the byte stream.
//
// One struct with a Kind, rather than an interface plus five types: the set is
// small, closed, and callers will switch on Kind anyway.
//
// **Params and Intermediates are borrowed.** They point into the parser's own
// buffers and are only valid until the emit callback returns; the next sequence
// reuses them. Every consumer in Shellter reads them and moves on, which is the
// normal shape for a streaming callback. A consumer that wants to keep an event
// must copy those slices - Clone does it.
type Event struct {
	Kind Kind

	// Rune is the character (EventPrint) or the control byte (EventControl).
	Rune rune

	// Private is the marker byte right after "ESC [", when there is one:
	// '?', '<', '=' or '>'. Zero when absent. ESC[?25l is the common case.
	Private byte

	// Params are the numeric parameters of a CSI sequence. Omitted parameters
	// arrive as 0; what "0" means (often "use the default") is the consumer's
	// business, not the parser's.
	Params []int

	// Intermediates are the rarely used bytes in the 0x20-0x2F range that may
	// appear before the final byte, e.g. the '$' in ESC[1$p.
	Intermediates []byte

	// Command is the final byte: 'm', 'H', 'A' ... for CSI, or the whole
	// instruction for EventEscape.
	Command byte

	// Data is the OSC payload, e.g. "0;my window title".
	Data string
}

// Clone returns a copy that owns its slices, for a caller that keeps an event
// past the callback it arrived in.
func (e Event) Clone() Event {
	if len(e.Params) > 0 {
		e.Params = append([]int(nil), e.Params...)
	}
	if len(e.Intermediates) > 0 {
		e.Intermediates = append([]byte(nil), e.Intermediates...)
	}
	return e
}

// Param returns parameter i, or def when it is missing or zero. Terminal
// commands almost always define 0 as "use the default", so this is the
// convenience the next phase will want.
func (e Event) Param(i, def int) int {
	if i >= len(e.Params) || e.Params[i] == 0 {
		return def
	}
	return e.Params[i]
}

// String renders an event in a form meant for reading a trace by eye.
func (e Event) String() string {
	switch e.Kind {
	case EventPrint:
		return fmt.Sprintf("PRINT   %q", e.Rune)
	case EventControl:
		return fmt.Sprintf("CONTROL %-3s (0x%02x)", controlName(byte(e.Rune)), e.Rune)
	case EventCSI:
		s := "CSI     "
		if e.Private != 0 {
			s += fmt.Sprintf("private=%q ", rune(e.Private))
		}
		s += fmt.Sprintf("params=%v ", e.Params)
		if len(e.Intermediates) > 0 {
			s += fmt.Sprintf("inter=%q ", string(e.Intermediates))
		}
		return s + fmt.Sprintf("cmd=%q", rune(e.Command))
	case EventEscape:
		return fmt.Sprintf("ESC     cmd=%q", rune(e.Command))
	case EventOSC:
		return fmt.Sprintf("OSC     %q", e.Data)
	default:
		return fmt.Sprintf("UNKNOWN %+v", struct{ Kind Kind }{e.Kind})
	}
}

func controlName(b byte) string {
	switch b {
	case 0x07:
		return "BEL"
	case 0x08:
		return "BS"
	case 0x09:
		return "HT"
	case 0x0a:
		return "LF"
	case 0x0d:
		return "CR"
	case 0x7f:
		return "DEL"
	default:
		return "C0"
	}
}
