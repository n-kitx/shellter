# Roadmap

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
- [x] Session restore after quit (`restore-session`, default on): tabs, names,
      splits and ratios, focus, each pane's directory and last 2,000 lines
      with colours, into ~/Library/Application Support/Shellter/session.json.
      Running programs do not come back; closing the last tab starts fresh.
      Restores never stack: an untouched pane re-saves what it was restored
      with, a used one only its own session (`pane.transcript`). Also saved
      every 30 s while anything changes, so a crash loses at most that.
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
- [x] Application keypad mode (`ESC =`, `?66`): the keypad sends `ESC O p`...
      to full-screen programs. Not at a shell prompt, where oh-my-zsh's smkx
      turns it on without binding any of it.
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
- [x] Colon sub-parameters (`parser.Event.Sub`): `4:3`, `38:2::r:g:b`,
      `58:5:n`. Underline styles - single, double (`4:2`, `21`), wavy, dotted,
      dashed - and underline colour (`58`/`59`), drawn by `shellterDrawText`
      (the wave is a path). Cells grew 32 -> 40 bytes for the colour: ~7-10%
      off Apply throughput, ~25% more history memory.
- [x] Blink: SGR 5 text blinks, hidden on the off beat (`terminal/blink.go`).
      One 530 ms timer, which redraws only while something blinks.

## Scrollback and selection

- [x] Search (Cmd+F, Cmd+G, Cmd+Shift+G, and Aa ↑ ↓ × buttons in the bar):
      whole history, case-insensitive unless Aa is on, current match selected
      and every other match on screen tinted while the bar is open. Matches
      do not cross a row boundary. No regex yet.
- [x] Scroll thumb: shown while the view is in history and for a second after
      it moves, and whenever the pointer is over the pane's right edge, where
      it widens; drag it, or click its track to jump there.
- [x] Clear scrollback (Cmd+K, see Mac app behaviour).
- [x] Auto-scroll when a selection drag goes past the top or bottom; the
      window repeats a drag held still there 20 times a second. A drag stays
      with the pane it started in.
- [x] Rectangular selection (Option+drag), copied a line of text per row
      (`screen.BlockText`).
- [x] Copy on select (`copy-on-select = true`).
- [x] Right-click / Ctrl+click menu: Copy, Paste, Select All, Split Right/Down,
      Clear. Goes to the program instead when it asked for the mouse (Shift
      takes it back, as for selecting).
- [x] Cmd+click to open URLs and paths (relative to the pane's directory,
      `:line:col` stripped); runnable files are revealed, not opened. Hover
      underlines a link in blue with a tooltip; Cmd brightens it and shows
      the hand pointer.
- [x] Multi-line paste warning (`paste-warning`, default on): asks before
      pasting several lines where each would run on arrival - no bracketed
      paste, no full-screen program. zsh at its prompt is never asked about.
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
- [x] Zoom a pane (Cmd+Shift+Return, Split > Zoom Pane, checked while on):
      it fills the tab, the others keep their shells and places. Moving the
      focus, swapping, splitting or closing the zoomed pane ends it.
- [x] Swap panes (Cmd+Option+Shift+arrows); Split > Move Pane to New Tab.
      Nothing restarts - the pane moves between trees, and its reader looks
      its tab up rather than remembering it (`GUI.tabOf`).

## Text and rendering

- [x] Font family setting (`font-family`), falling back to Menlo.
- [x] Line height (`line-height`, 0.8-3), text centred in the row.
- [x] Box drawing (U+2500-257F), block elements (U+2580-259F) and Powerline
      (U+E0B0-E0B7) drawn as shapes, not glyphs (`render/box.go`,
      `shellterDrawGlyph`): lines reach the cell edges at any line height,
      double lines join properly, fills have no seams. ~15% on `spans`.
- [ ] Ligatures, bold-as-bright.
- [ ] Grapheme clusters: width is per rune plus combining marks, so ZWJ emoji
      and flags take one cell per component (`text/width.go`).
- [x] GPU (Metal) rendering (`metal_darwin.m`): instanced rectangles and
      glyphs from a Core Text-rasterised atlas, six draw calls a frame; cells
      are whole pixels wide. Streaming at 120 fps: main thread 47% -> ~12% of
      a core, memory 228 -> 78 MB. Next: the Go side of a frame (`spans`,
      `Palette.FrameInto`) is now most of what is left.
- [x] Throughput: ASCII text is parsed and printed in runs (`TextRuns`,
      `printASCII`), and the PTY is read on its own goroutine (`readAhead`) so
      reading and parsing overlap. `cat` of 53 MB: 40 -> 170 MB/s; the PTY
      alone drains at ~236. What is left is the per-line work (scrollback
      trimming, `isPlainBlank`) and escape sequences.

## Configuration

- [x] Live reload of the settings file and theme files (1 s poll).
- [x] Custom themes: a file in `themes/` beside the settings file.
- [x] Follow macOS light/dark: `theme = light:NAME,dark:NAME` switches as the
      system does (KVO on the app's effectiveAppearance).
- [x] Cursor style and blink settings (`cursor-style`, `cursor-blink`); a
      program's DECSCUSR wins until it sends `ESC [ 0 SP q`. Blinks only in the
      focused pane of a focused window, held steady while typing.
- [ ] Key binding settings.
- [ ] Profiles; a Preferences window.

## Accessibility

- [ ] VoiceOver: the grid is not exposed to the accessibility API.

## Debugging and tooling

- [ ] `SHELLTER_TRACE` / `SHELLTER_SCREEN` work only headless (`app.go`), not
      in the GUI (`gui.go`).
- [ ] Run vttest / esctest against Shellter and record the results.
- [x] Fuzz tests: `FuzzParse` (no panics; how reads split the bytes changes
      nothing) and `FuzzScreen` (no panics through resizes, cursor on the
      grid, a transcript replays). The first run found a reflow crash on a
      wide character at one column; crashers stay in `testdata/fuzz`.

## Security

From a review on 2026-10-24: nothing exploitable found, these two to tighten.

- [ ] Cmd+click: add `.fileloc`, `.webloc`, `.inetloc` (shortcuts that can
      point at an app or another kind of link), `.prefPane` and
      `.mobileconfig` (they start an install) to `runnable` in
      `internal/terminal/links.go`, so they are shown in the Finder rather
      than opened. Needs such a file on disk and a Cmd+click on its path.
- [ ] Update `golang.org/x/sys` (v0.20.0) and `golang.org/x/term` (v0.20.0).
      govulncheck flags x/sys for a Windows-only issue Shellter cannot reach,
      but scanners and readers will see the old versions.

## Distribution

- [ ] Developer ID signing and notarisation (the bundle is ad-hoc signed, so
      Gatekeeper warns on any other Mac).
- [ ] Universal (arm64 + x86_64) build.
- [x] Build with a current Go: go.mod asks for go1.25.8, the first with the
      fixes for the five standard library issues govulncheck found reachable
      from go1.22.6 (url.Parse on OSC 7, LookPath behind `open`, three in os).
      Built with go1.27.2, govulncheck (2026-10-12) finds nothing reachable.
- [ ] Auto-update (Sparkle), crash reporting.
- [ ] `LICENSE` file.
