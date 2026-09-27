package terminal

import "strings"

// drop types what was dropped on the grid into the pane under it: files as
// their paths, quoted for the shell and separated by spaces, text as it is. It
// arrives as a paste would - a program that asked for bracketed paste knows
// it was not typed - and the pane it lands in takes the focus.
func (g *GUI) drop(row, col int, paths []string, text string) {
	p, _ := g.paneAt(row, col)
	if p == nil {
		return
	}
	g.focusOn(p)
	if len(paths) > 0 {
		quoted := make([]string, len(paths))
		for i, path := range paths {
			quoted[i] = shellQuote(path)
		}
		// A space after the last one, so the next thing typed is a new word.
		text = strings.Join(quoted, " ") + " "
	}
	if text == "" {
		return
	}
	p.mu.Lock()
	bracketed := p.scr.Modes().BracketedPaste
	p.mu.Unlock()
	g.writeTo(p, pasteBytes(text, bracketed))
}

// shellQuote makes a path one word to a shell, backslash-escaping each
// character the shell would otherwise act on, as Terminal.app does:
// "/Users/me/My Files" becomes "/Users/me/My\ Files". Letters beyond ASCII
// mean nothing to a shell and are left alone.
func shellQuote(path string) string {
	var b strings.Builder
	for _, r := range path {
		if r < 0x80 && !safeInWord(r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func safeInWord(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	// Not "=": zsh expands "=cmd" at the start of a word to cmd's path.
	return strings.ContainsRune("/._-+,:@%", r)
}
