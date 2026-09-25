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
- [ ] `TERM_PROGRAM=Shellter` (and drop `TERM_PROGRAM_VERSION`). Under `go run`
      from iTerm we inherit `TERM_PROGRAM=iTerm.app` and `ITERM_SESSION_ID`, so
      tools like `imgcat` think they are in iTerm. `shell/env.go`.
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
      gains or loses cells. Not remembered across launches.
- [x] Confirm before closing a pane, tab, the window or the app while a job is
      running, naming it ("vim is still running"). Idle prompts close silently.
      `pty.Foreground` (TIOCGPGRP) vs the shell's pid, `shell.Session.Running`.
- [ ] Remember window size and position, and the sidebar width.
- [ ] Session restore after quit.
- [x] Double click on the title strip zooms / minimises per System Settings.
- [x] Sidebar width is draggable (120-480 pt), not persisted.

## Keyboard input

- [ ] `NSTextInputClient`: IME (Chinese/Japanese/Korean), dead keys
      (Option+E, E -> é), emoji picker. `keyDown:` goes straight to Go today.
- [ ] Option-as-Meta setting. Option is left as macOS composes it (`ø`).
- [ ] Application keypad mode (`ESC =`): keypad sends the same bytes either way.
- [ ] Kitty keyboard protocol (Ctrl+I vs Tab, Shift+Enter vs Enter).
- [ ] Secure Keyboard Entry (`EnableSecureEventInput`).

## Escape sequences

Only OSC 0 and 2 (title) are acted on: `screen.(*Screen).osc` in
`screen/screen.go`. DCS is not a parser state at all (`parser/parser.go`), so
DCS payloads print as visible garbage.

- [ ] DCS parsing (prerequisite for XTGETTCAP, tmux passthrough, sixel).
- [ ] OSC 7 - current directory; new tabs and splits open where you are.
- [ ] OSC 8 - hyperlinks.
- [ ] OSC 52 - clipboard (copy from vim/tmux over SSH).
- [ ] OSC 4 / 10 / 11 / 12 - palette and fg/bg/cursor colour query and set
      (nvim picks light/dark from this).
- [ ] OSC 133 - shell integration: prompt marks, jump between prompts,
      select a command's output.
- [ ] OSC 9 / 777 - desktop notifications.
- [ ] Mode 2026 - synchronised output (stops TUI flicker).
- [ ] `DECRQM` mode queries, `XTVERSION`.
- [ ] `DECLRMM` left/right margins; double-width/height lines.
- [ ] Sixel / kitty / iTerm image protocols.
- [ ] BEL: sound or visual bell, Dock bounce, mark on a background tab. Ignored
      today (`screen/screen.go`).
- [~] Blink: recorded, never animated (`render/frame.go`).

## Scrollback and selection

- [ ] Search (Cmd+F).
- [ ] Scrollbar / scroll-position indicator.
- [ ] Clear scrollback.
- [ ] Auto-scroll when a selection drag reaches the edge.
- [ ] Rectangular selection (Option+drag).
- [ ] Copy on select (option).
- [ ] Right-click menu (Copy, Paste, Clear, Split).
- [ ] URL detection and Cmd+click to open URLs / paths.
- [ ] Drag a file onto the window to paste its path.
- [ ] Reflow on resize. Soft line breaks are recorded (`wrapped []bool`); the
      rewrap is the missing half.

## Tabs and splits

- [ ] Rename a tab.
- [ ] Drag to reorder tabs; tear a tab off into a new window.
- [ ] Drag a split divider (splits are always down the middle).
- [ ] Swap panes; turn a pane back into a tab.

## Text and rendering

- [ ] Font family setting (Menlo is hard-coded in `window_darwin.m`).
- [ ] Ligatures, line height, bold-as-bright.
- [ ] Grapheme clusters: width is per rune plus combining marks, so ZWJ emoji
      and flags take one cell per component (`text/width.go`).
- [ ] GPU (Metal) rendering. Today it is `NSString drawAtPoint:` per run.
- [ ] Throughput: the screen parses at ~41 MB/s against a PTY that can deliver
      ~193. The next profile starts at `Screen.print`.

## Configuration

- [ ] Live reload of the settings file.
- [ ] Settings for cursor style, Option-as-Meta, key bindings, font family.
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
