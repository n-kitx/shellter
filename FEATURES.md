# Feature gaps

What Shellter is missing next to a production terminal (Terminal.app, iTerm2,
Ghostty, WezTerm, kitty), and where in the code each piece would go. This is the
one list: tick things off here when they land, and add to it when a gap is found,
rather than rediscovering the state by reading the code.

`[x]` done · `[ ]` missing · `[~]` partly there

## Environment and startup

- [x] Shell starts in `$HOME`, not in whatever directory launched us (`/` from
      the Dock). `shell.DefaultConfig`.
- [x] `LANG` supplied from System Settings when launchd gave none, so the shell
      is not in the C locale. `shell/env.go` (`systemLocale`).
- [x] `COLORTERM=truecolor`, so bat/delta/vim themes use 24-bit colour.
      `shell/env.go`.
- [x] `TERM_PROGRAM=Shellter`, `TERM_PROGRAM_VERSION`; other terminals'
      variables (`ITERM_SESSION_ID`, `LC_TERMINAL`, `KITTY_*`, `TMUX`, ...)
      are dropped, so imgcat and friends do not think they are in iTerm.
      `shell/env.go`.
- [ ] A Shellter terminfo entry (today `xterm-256color` everywhere).

## Mac app behaviour

- [x] App menu: About, Settings… (Cmd+,), Hide (Cmd+H), Hide Others, Show
      All, Quit. Settings… opens the config file in the default text editor,
      writing a template listing every key first if there is none
      (`config.Create`). There is no settings window.
- [x] Window menu: Minimize (Cmd+M), Zoom, Bring All to Front; View menu:
      Enter Full Screen (Ctrl+Cmd+F).
- [ ] Cmd+N new window. There is exactly one window; `gView`/`gTabBar` etc. are
      globals in `window_darwin.m`, so this is a real refactor.
- [x] Cmd+K clear: history and the rows above the prompt go, the prompt line
      moves to the top (`screen.Clear`). In vim/less only history goes.
- [x] Cmd+= / Cmd+- / Cmd+0 font size. The window keeps its size and the grid
      gains or loses cells. Remembered (see below).
- [x] Confirm before closing a pane, tab, the window or the app while a job is
      running, naming it ("vim is still running"). Idle prompts close silently.
      `pty.Foreground` (TIOCGPGRP) vs the shell's pid, `shell.Session.Running`.
- [x] Remember window size and position (frame autosave), the sidebar width
      and the zoom (NSUserDefaults). A zoom is dropped if font-size changes.
- [ ] Session restore after quit.
- [x] Double click on the title strip zooms / minimises per System Settings.
- [x] Sidebar width is draggable (120-480 pt), and remembered.

## Keyboard input

- [x] `NSTextInputClient`: input methods (Chinese/Japanese/Korean), dead keys
      (Option+E, E -> é), the emoji picker. The composition is drawn
      underlined at the cursor. Ctrl/Cmd combinations and special keys skip
      the input system while nothing is being composed. Press-and-hold is
      off by default so held keys repeat.
- [x] Option-as-Meta setting (`option-as-meta = true`): ESC prefix for
      letters, Return, Tab, Delete, Escape; arrows keep their parameter.
- [ ] Application keypad mode (`ESC =`): keypad sends the same bytes either way.
- [ ] Kitty keyboard protocol (Ctrl+I vs Tab, Shift+Enter vs Enter).
- [x] Secure Keyboard Entry (Shellter menu): on while Shellter is the active
      app, off while it is not, remembered in NSUserDefaults.

## Escape sequences

- [x] DCS parsing; APC/PM/SOS swallowed; 1 MB string limit that drops an
      oversized string whole. XTGETTCAP and DECRQSS answered.
- [ ] tmux passthrough (`DCS tmux; ... ST`) is dropped, not forwarded.
- [x] OSC 7 - current directory; new tabs and splits open where you are.
      zsh sends nothing by default, so the kernel's answer for the shell's
      pid (`proc_pidinfo`, `shell.Session.Dir`) is the fallback; an OSC 7
      report wins only when its host is this machine.
- [ ] OSC 8 - hyperlinks.
- [x] OSC 52 - clipboard writes; reads refused on purpose.
- [ ] OSC 4 / 10 / 11 / 12 - palette and fg/bg/cursor colour query and set
      (nvim picks light/dark from this).
- [ ] OSC 133 - shell integration: prompt marks, jump between prompts,
      select a command's output.
- [ ] OSC 9 / 777 - desktop notifications.
- [x] Mode 2026 - synchronised output, with a 500 ms safety timeout.
- [x] `DECRQM` mode queries, `XTVERSION`.
- [ ] `DECLRMM` left/right margins; double-width/height lines.
- [ ] Sixel / kitty / iTerm image protocols.
- [x] BEL: `bell = sound | flash | both | off`; a background tab gets a bell
      mark until looked at, the Dock bounces once if Shellter is not in front.
      At most one bell per pane per 200 ms (`terminal/overlays.go`).
- [~] Blink: recorded, never animated (`render/frame.go`).

## Scrollback and selection

- [x] Search (Cmd+F, Cmd+G, Cmd+Shift+G, and ↑ ↓ × buttons in the bar):
      whole history, case-insensitive, current match selected. Matches do not cross a row boundary; only the
      current match is highlighted.
- [~] Scroll-position indicator: an overlay thumb while the view is in history
      and for a second after it moves. Not draggable (`terminal/overlays.go`).
- [x] Clear scrollback (Cmd+K, see Mac app behaviour).
- [x] Auto-scroll when a selection drag goes past the top or bottom; the
      window repeats a drag held still there 20 times a second. A drag stays
      with the pane it started in.
- [ ] Rectangular selection (Option+drag).
- [x] Copy on select (`copy-on-select = true`).
- [x] Right-click / Ctrl+click menu: Copy, Paste, Select All, Split Right/Down,
      Clear. Goes to the program instead when it asked for the mouse (Shift
      takes it back, as for selecting).
- [x] Cmd+click to open URLs and paths (relative to the pane's directory,
      `:line:col` stripped); runnable files are revealed, not opened. Hover
      underlines a link in blue with a tooltip; Cmd brightens it and shows
      the hand pointer.
- [x] Drop files on the window to type their paths, backslash-escaped, into
      the pane under the drop; dropped text is pasted.
- [x] Reflow on resize, history included (`screen/reflow.go`). Wide
      characters never straddle the edge. Alternate screen is cropped; the
      primary behind it is reflowed. ~0.9 ms per resize at the default 1000
      lines of history.

## Tabs and splits

- [x] Rename a tab: double click it, or Shell > Rename Tab... An empty name
      hands naming back to the shell. A renamed tab keeps its `>_` whenever an
      unnamed one would.
- [x] Drag to reorder tabs: the tab is lifted (shadow) and follows the
      pointer; the tabs it passes slide aside, and it settles into place on
      release. A 60 Hz timer runs only while something is moving.
- [ ] Tear a tab off into a new window - needs Cmd+N / multiple windows first.
- [x] Drag a split divider, with a resize cursor over it; double click puts it
      back in the middle. Not remembered. Divider lines run to the window's
      edge and join each other at junctions.
- [x] At most 4 panes per tab: the Split items grey out, the shortcuts beep.
- [x] Unfocused panes fade their colours towards the background instead of
      being covered by a dark wash (which left boxed-in panes with light
      borders).
- [x] Swap panes (Cmd+Option+Shift+arrows); Split > Move Pane to New Tab.
      Nothing restarts - the pane moves between trees, and its reader looks
      its tab up rather than remembering it (`GUI.tabOf`).

## Text and rendering

- [x] Font family setting (`font-family`), falling back to Menlo.
- [ ] Ligatures, line height, bold-as-bright.
- [ ] Grapheme clusters: width is per rune plus combining marks, so ZWJ emoji
      and flags take one cell per component (`text/width.go`).
- [ ] GPU (Metal) rendering. Today it is `NSString drawAtPoint:` per run.
- [ ] Throughput: the screen parses at ~41 MB/s against a PTY that can deliver
      ~193. The next profile starts at `Screen.print`.

## Configuration

- [x] Live reload of the settings file and theme files (1 s poll).
- [x] Custom themes: a file in `themes/` beside the settings file.
- [ ] Settings for cursor style and key bindings.
- [ ] Profiles; a Preferences window.

## Accessibility

- [ ] VoiceOver: the grid is not exposed to the accessibility API.

## Debugging and tooling

- [ ] `SHELLTER_TRACE` / `SHELLTER_SCREEN` work only headless (`app.go`), not
      in the GUI (`gui.go`).
- [ ] Run vttest / esctest against Shellter and record the results.

## Distribution

- [ ] Developer ID signing and notarisation (the bundle is ad-hoc signed, so
      Gatekeeper warns on any other Mac).
- [ ] Universal (arm64 + x86_64) build.
- [ ] Auto-update (Sparkle), crash reporting.
- [ ] `LICENSE` file.
