package screen

// Tab stops.
//
// A Tab character is not "print some spaces": it moves the cursor to the next
// stop, and the stops are terminal state a program can change. Every eighth
// column is the default every terminal has shipped with since hardware ones,
// and it is why `ls` output lines up.
//
//	HT   0x09           move to the next stop
//	HTS  "ESC H"        set a stop at the cursor
//	TBC  "ESC [ g"      clear the stop here (0) or all of them (3)
//	CHT  "ESC [ n I"    forward n stops
//	CBT  "ESC [ n Z"    back n stops

const tabWidth = 8

// defaultTabs returns a stop every eight columns.
func defaultTabs(cols int) []bool {
	tabs := make([]bool, cols)
	for col := tabWidth; col < cols; col += tabWidth {
		tabs[col] = true
	}
	return tabs
}

// tabForward moves the cursor to the n-th next stop, stopping at the last
// column when there are none left.
func (s *Screen) tabForward(n int) {
	s.pendingWrap = false
	for ; n > 0; n-- {
		col := s.cur.Col
		for col++; col < s.cols-1 && !s.tabs[col]; col++ {
		}
		s.cur.Col = clamp(col, 0, s.cols-1)
	}
}

// tabBackward moves the cursor to the n-th previous stop, stopping at column 0.
func (s *Screen) tabBackward(n int) {
	s.pendingWrap = false
	for ; n > 0; n-- {
		col := s.cur.Col
		for col--; col > 0 && !s.tabs[col]; col-- {
		}
		s.cur.Col = clamp(col, 0, s.cols-1)
	}
}

// clearTabs implements TBC: 0 clears the stop under the cursor, 3 clears all.
func (s *Screen) clearTabs(mode int) {
	switch mode {
	case 0:
		if s.cur.Col < len(s.tabs) {
			s.tabs[s.cur.Col] = false
		}
	case 3:
		for i := range s.tabs {
			s.tabs[i] = false
		}
	}
}

// resizeTabs keeps the stops that still fit and gives the new columns the
// default spacing.
func (s *Screen) resizeTabs(cols int) {
	tabs := defaultTabs(cols)
	for col := 0; col < min(cols, len(s.tabs)); col++ {
		tabs[col] = s.tabs[col]
	}
	s.tabs = tabs
}
