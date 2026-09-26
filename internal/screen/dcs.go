package screen

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// Version is what Shellter says it is when a program asks (XTVERSION).
const Version = "Shellter 0.1.0"

// dcs handles a Device Control String. Two are questions worth answering:
//
//	"+q" names   XTGETTCAP: what is terminfo capability X, in this terminal?
//	"$q" setting DECRQSS: what is setting X set to right now?
//
// Both are how a program checks a feature before relying on it, so an
// honest "no" matters as much as a "yes". Every other DCS - tmux passthrough,
// sixel images - arrives here too, and is dropped rather than printed.
func (s *Screen) dcs(data string) {
	switch {
	case strings.HasPrefix(data, "+q"):
		s.getTermcap(data[2:])
	case strings.HasPrefix(data, "$q"):
		s.requestSetting(data[2:])
	}
}

// termcaps are the capabilities XTGETTCAP answers for. Only facts: the name
// the shell was given in TERM, and colour depth, which is what programs
// actually ask about.
var termcaps = map[string]string{
	"TN":     "xterm-256color",
	"name":   "xterm-256color",
	"Co":     "256",
	"colors": "256",
	"RGB":    "8/8/8",
}

// getTermcap answers XTGETTCAP. Names arrive hex-encoded and separated by ';',
// and each is answered on its own: "1+r name=value" for one we know, "0+r"
// for one we do not.
func (s *Screen) getTermcap(names string) {
	for _, h := range strings.Split(names, ";") {
		raw, err := hex.DecodeString(h)
		if err != nil {
			s.reply("\x1bP0+r\x1b\\")
			continue
		}
		value, ok := termcaps[string(raw)]
		if !ok {
			s.reply("\x1bP0+r%s\x1b\\", h)
			continue
		}
		s.reply("\x1bP1+r%s=%s\x1b\\", h, hex.EncodeToString([]byte(value)))
	}
}

// requestSetting answers DECRQSS for the settings that have an answer here.
func (s *Screen) requestSetting(what string) {
	switch what {
	case "m": // SGR: reported as the reset state, which the pen always has a way back to
		s.reply("\x1bP1$r0m\x1b\\")
	case "r": // DECSTBM: the scroll region
		s.reply("\x1bP1$r%d;%dr\x1b\\", s.top+1, s.bottom+1)
	case " q": // DECSCUSR: the cursor shape, as the code that would set it
		code := map[CursorStyle]int{CursorBlock: 2, CursorUnderline: 4, CursorBar: 6}[s.cursorStyle]
		s.reply("\x1bP1$r%d q\x1b\\", code)
	default:
		s.reply("\x1bP0$r\x1b\\")
	}
}

// maxClipboard bounds one OSC 52 write, and maxClipboardQueue how many can wait
// to be taken: a program cannot make us hold more than a few megabytes of
// clipboard however it misbehaves.
const (
	maxClipboard      = 1 << 20
	maxClipboardQueue = 4
)

// setClipboard handles OSC 52, "52;c;<base64>": a program putting text on the
// clipboard. It is how copying in vim or tmux reaches the Mac's clipboard -
// and over ssh it is the only way, since the program is on another machine.
//
// Reading the clipboard ("52;c;?") is refused: any program, including one on
// a server you do not control, could quietly collect whatever you last
// copied. Writing only ever gives a program what it already had.
func (s *Screen) setClipboard(text string) {
	_, payload, ok := strings.Cut(text, ";")
	if !ok || payload == "?" {
		return
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		// Some programs leave the padding off.
		decoded, err = base64.RawStdEncoding.DecodeString(payload)
	}
	if err != nil || len(decoded) > maxClipboard || len(s.clipboard) >= maxClipboardQueue {
		return
	}
	s.clipboard = append(s.clipboard, string(decoded))
}

// TakeClipboard returns the text programs have asked to put on the clipboard
// since it was last called, oldest first. Like TakeReplies it is drained by
// the layer above, which is the one that can reach the real clipboard.
func (s *Screen) TakeClipboard() []string {
	out := s.clipboard
	s.clipboard = nil
	return out
}
