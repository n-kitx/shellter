# Shellter

A macOS terminal emulator written in Go, built incrementally as a learning
project. This repository is at **Phase 14: selection, copy and paste**.

## What works today

**Phase 1 — PTY foundation.** Shellter starts `zsh` on a pseudoterminal,
forwards your keystrokes to it, reads back whatever the shell (and the commands
it runs) print, and writes those bytes to the terminal Shellter itself is
running in. When the shell exits, Shellter exits with the same status code.

**Phase 2 — parser.** Those same bytes are also fed through a state machine
that turns them into terminal *events*: printable characters, control
characters, and parsed escape sequences.

**Phase 3 — screen buffer.** Those events drive a virtual screen held in
memory: a grid of cells plus a cursor. Printing writes a character and advances
the cursor; `\n`, `\r`, `\b` and the cursor/erase CSI commands move it and
clear parts of the grid.

**Phase 4 — renderer.** `shellter -gui` opens a real macOS window (AppKit, via
cgo) and draws that screen buffer: one monospaced line per row, an inverse-video
block cursor, repainting whenever the state changes. Resizing the window resizes
the screen buffer and the PTY together.

**Phase 5 — keyboard input.** Key presses are translated into the bytes a
terminal sends: characters as UTF-8, Return as CR, Backspace as DEL, Ctrl+key
by arithmetic, and the arrow keys as escape sequences whose form depends on a
mode the shell itself switched on. The loop is now closed in both directions.

**Phase 6 — ANSI behaviour.** The screen stopped merely recognising sequences
and started obeying them: SGR colours and styles are stamped onto every cell,
the full cursor-movement and erase families work, and terminal modes - cursor
visibility, autowrap, application cursor keys - are tracked. The renderer draws
all of it, batching each row into runs of identical appearance.

**Phase 7 — Unicode.** A rune stopped being a column. Wide characters take two
cells, combining marks take none, and the cursor advances by width rather than
by character. The parser already decoded UTF-8; now the grid understands it.

**Phase 8 — scrollback.** Lines that scroll off the top are kept in a bounded
ring buffer instead of being dropped, and the view can move back into them with
Shift+Page Up or the scroll wheel while the shell keeps writing underneath.

**Phase 9 — compatibility.** Real programs run: `vim`, `less`, `top` and `git`'s
pager. That took an alternate screen buffer, scroll regions, insert/delete of
characters and lines, tab stops, the line-drawing character set, mouse
reporting, function keys, and — the piece that makes terminal emulation
two-way — answering the questions programs ask. Each of those is tested by
driving the real program through a real PTY and reading our own screen back.

**Phase 10 — performance and polish.** Profiled first, then fixed what the
profile actually pointed at: a redraw got 3.6x cheaper and stopped allocating
entirely, absorbing output got 2.7x faster, and a terminal that has been open
for a while now runs without allocating at all. See *Performance* below for the
measurements and what each one bought.

**Phase 11 — tabs and chrome.** One window, several shells. Each tab is its own
session, parser, screen and scrollback — the pipeline from Phase 1 onwards,
instantiated more than once rather than changed. The bar can sit **above** the
terminal or **beside** it, switchable from the View menu, and the grid finally
got some padding.

**Phase 12 — splits, settings and chrome.** A tab now holds a *tree* of panes:
`Cmd+D` divides one in two, each half a real shell on its own PTY, told its own
size. The tabs moved into the title bar, which is hidden so the content view can
draw that row itself; the window can be translucent and blurred; and everything
adjustable lives in `~/.config/shellter/config`, with a flag for each setting.

## Why PTY output is a byte stream, not text

A program writing to a terminal has only one channel: `write(1, ...)`. It uses
that channel for two different things at once — the text to show, and
instructions to the terminal ("move the cursor left two columns", "erase the
screen", "switch to red"). Both arrive interleaved in one stream of bytes:

```text
ESC [ 3 1 m H e l l o ESC [ 0 m
└── instruction ──┘└text┘└─ instruction ─┘
```

So "read the PTY and print it" only works while something else downstream is
willing to interpret those instructions. To be a terminal, Shellter has to
interpret them itself, and the first step is telling them apart.

* **Escape sequence** — an in-band instruction to the terminal, introduced by a
  byte that cannot appear in ordinary text.
* **ESC** — that byte: `0x1b`. It means "what follows is an instruction, not
  text".
* **CSI** — *Control Sequence Introducer*, the two bytes `ESC [`. It starts the
  large, parameterised family of commands: `ESC [ 31 m` (red), `ESC [ 10;20 H`
  (move the cursor to row 10, column 20), `ESC [ 2 J` (clear the screen). The
  numbers are the parameters, separated by `;`, and the final letter is the
  command.

### Why a state machine

The meaning of a byte depends entirely on what came before it. The byte `m` is
the letter "m" in `hello`, but it is the *command* "set graphic rendition" in
`ESC [ 31 m`. `3` is a digit of a parameter in one place and a character to
print in another. There is no way to classify a byte in isolation, so the
parser has to remember where it is: `Ground`, `Escape`, `CSI` or `OSC`.

### Why the state must survive across reads

One `read()` on the PTY master returns whatever happened to be in the kernel
buffer at that moment. It has no idea what an escape sequence is and will
happily stop in the middle of one:

```text
read 1: ESC [        -> state = CSI,   emit nothing
read 2: 3 1          -> state = CSI,   emit nothing
read 3: m            -> emit CSI(params=[31], cmd='m'), state = Ground
```

Because the accumulated parameters and the current state live on the `Parser`
struct rather than on the stack, `Parse(a); Parse(b)` produces exactly the same
events as `Parse(a+b)`. A test asserts this at every possible split point of a
mixed input.

## Architecture

```text
        zsh / application
                │
               PTY
                │
            raw bytes
                │
              Parser
                │
        terminal events
                │
         Terminal state
       (grid of cells + cursor)
                │
             Renderer
                │
           macOS window
```

Input travels the other way around the same loop:

```text
key press ──► input.Encode ──► PTY ──► zsh
mouse     ──► input.EncodeMouse ──┘
```

And since Phase 9 there is a third path, because a terminal is asked questions
as well as told things:

```text
zsh ──► parser ──► screen.reply() ──► TakeReplies ──► PTY ──► zsh
```

The screen still does no I/O. It *queues* the answer; the application layer
drains the queue into the PTY on the way through the read loop, before the
redraw — a program that asked a question is blocked until it hears back.

As packages:

```text
cmd/shellter          process entry point, exit code
        │
internal/terminal     application layer: tabs, window size, the I/O
        │             pumps, lifecycle, event sink
        ├── internal/parser   bytes -> terminal events     (pure, no OS)
        ├── internal/screen   events -> grid + cursor      (pure, no OS)
        ├── internal/render   frame -> NSWindow            (cgo + AppKit)
        ├── internal/input    key press -> bytes           (pure, no OS)
        ├── internal/text     rune -> column width         (pure, no OS)
        │
internal/shell        session layer: which shell, which env, start/wait/close
        │
internal/pty          OS layer: PTY pair, fd wiring, fork+exec, signals
```

Each layer only knows about the one below it:

* `pty` knows about file descriptors and processes. Not about shells.
* `shell` knows about zsh. Not about keyboards, screens or escape sequences.
* `parser` knows about the terminal protocol and nothing else — no files, no
  PTY, no rendering. Bytes in, events out.
* `screen` knows what each event does to a grid of cells. Events in, terminal
  state out. No I/O either, so it can be tested entirely from byte strings.
* `input` is the parser's mirror image: the parser decodes escape sequences
  arriving from the shell, `input` produces the ones going the other way. Also
  pure, also testable from tables.
* `text` answers one question - how many columns does a rune occupy - and is
  the only place that knows Unicode beyond decoding. The screen stays plain
  arithmetic and the renderer never thinks about Unicode at all.
* `render` is the only package that touches AppKit. It receives a `Layout` —
  one `Frame` of plain strings per pane, each with the rectangle it occupies —
  and returns keystrokes and window sizes. It imports neither `screen` nor
  `parser`. It draws the tab bar and the lines between panes, but knows nothing
  about what a tab or a split *is*: it is handed titles and rectangles and hands
  back clicks.
* `config` reads the settings file. It knows nothing about what any setting
  means — not even that `tab-position` becomes a `render.TabPosition`, which is
  `main`'s business — only how to turn text into values and how to say clearly
  what is wrong with a line.
* `terminal` is the application layer and has two controllers: `App`
  (terminal-in-a-terminal, phases 1-3) and `GUI` (window, phases 4 onwards).
  They share every layer below and differ only at the two ends. The `GUI` owns
  the tabs, each one a tree of panes, and each pane a `shell.Session` with its
  own parser and screen — which is why nothing below this line had to change
  for either tabs or splits.

### Where bytes enter

`App.pumpOutput` in `internal/terminal/app.go` is the single place PTY output
reaches the application:

```text
sess.Read -> chunk ─┬─► a.Out            raw passthrough (phase 1 behaviour)
                    └─► parser.Parse ──► events ─┬─► App.Screen   terminal state
                                                 └─► App.OnEvent  trace / tests
```

`GUI.pump` is the same loop with the passthrough removed — with a window to
draw into, nothing needs the raw bytes — and with two more jobs: draining the
screen's queued replies back into the PTY, and pushing a changed window title
or mouse-tracking mode to the window.

## The renderer

```text
                       ┌──────────────── UI thread ────────────────┐
PTY read goroutine     │                                           │
  parser.Parse         │  drawRect: ──► shellterGoRender ──► draw()
  screen.Apply  ◄─ mu ─┼───────────────────────► Snapshot, per pane
  render.Redraw() ─────┼─► setNeedsDisplay:YES                     │
                       │  keyDown:   ──► shellterGoKey  ──► sess.Write
                       │  setFrameSize: ─► shellterGoResize ─► screen + PTY
                       └───────────────────────────────────────────┘
```

* **Two threads, one mutex.** The goroutine reading the PTY writes to the
  screen; AppKit draws from the UI thread. The lock is held only long enough to
  apply a chunk of events or to copy a `Snapshot`.
* **The frame is pulled, not pushed.** `Redraw()` only marks the view dirty;
  AppKit then asks Go for the layout when it is ready to paint. Repeated
  requests between two frames collapse into one, so a burst of shell output
  costs one repaint rather than hundreds.
* **Pixels meet cells in one place.** `setFrameSize:` divides the window size
  by the font's advance width and line height, and that is the only conversion
  in the codebase. The result resizes the screen buffer *and* the PTY, so
  programs inside see the right `stty size`.
* **The cursor** is a filled block with the character redrawn on top in the
  background colour — what an inverse-video cursor actually is.
* **AppKit is main-thread only**, so `main` calls `runtime.LockOSThread` in
  `init` and the event loop owns that thread; everything else is a goroutine.

## Scrollback

History and live state are two different things, and Shellter keeps them apart:

```text
        scrollback (history)          grid (live state)
        ┌───────────────────┐         ┌───────────────────┐
        │ finished lines,   │         │ what escape       │
        │ nothing can edit  │         │ sequences paint   │
        │ them again        │         │ into, right now   │
        └───────────────────┘         └───────────────────┘
                     ▲                          ▲
                     └──────── view ────────────┘
                        offset decides the mix
```

Scrolling never touches the grid. It moves an **offset**, and the snapshot is
composed from `offset` lines of history followed by as much of the live grid as
still fits:

```text
offset 0          offset 2
┌──────────┐      ┌──────────┐
│ grid  0  │      │ history  │
│ grid  1  │      │ history  │
│ grid  2  │      │ grid  0  │
│ grid  3  │      │ grid  1  │
└──────────┘      └──────────┘
```

Consequences worth knowing:

* **The view stays anchored.** When a new line scrolls off while you are reading
  history, the offset grows with it, so the text you are looking at does not
  slide out from under you.
* **The cursor lives in the grid**, so it slides down the view as you scroll
  back and is simply not drawn once it falls off the bottom.
* **Typing returns you to the present**, as every terminal does.
* **Storage is a ring buffer**, so dropping the oldest line is free and memory
  is bounded however long a session runs. Trailing blanks are not stored -
  terminal lines are mostly empty on the right - and padded back on the way in.
* Attributes and wide characters survive into history unchanged.
* `ESC [ 3 J` and `RIS` clear history; `ESC [ 2 J` deliberately does not.

Controls: **Shift+Page Up / Page Down** by a page, **Shift+Home / End** to the
oldest line and back, and the **scroll wheel** by lines. Without Shift those keys
are terminal keys and go to the shell as `ESC[5~`, `ESC[6~`, `ESC[H`, `ESC[F`.
Since Phase 9 those shortcuts step aside while a full-screen program owns the
screen: there is no history to scroll there, so Shift+Page Up goes to the
program and the wheel becomes arrow keys. See *What a wheel turn means*.

Size it with `-scrollback`:

```sh
go run ./cmd/shellter -gui -scrollback 5000   # default 1000, 0 disables
```

## Unicode: a rune is not a column

Decoding UTF-8 was the parser's job back in phase 2. The harder question is
*width*, and there are three answers:

```text
'a'      1 column
'é'      1 column
'漢'      2 columns  - occupies two cells
U+0301   0 columns  - an accent, living on the cell before it
```

**Wide characters take two cells.** The character sits in the first, and the
second holds a *continuation*: a real cell with the same background, marked by
rune 0, that draws nothing because the glyph beside it already covers both
columns. The consequences are all the interesting cases:

* A wide character that does not fit in the last column wraps *whole* to the
  next line rather than being split; with autowrap off it is dropped.
* Overwriting either half blanks the other, so no dangling glyph is left.
* Erasing a range that cuts a character in half blanks the orphan outside the
  range - checked before the fill, while the two halves can still recognise
  each other.
* Resizing repairs any character the new right edge cut through.
* The cursor block covers two columns when it sits on a wide character.

**Combining marks take no cell.** They attach to the character already printed
(or to the wide character's first half, never its continuation), stored on the
cell as a short string so that `Cell` stays comparable. A mark arriving with
nothing before it is dropped, and a cell will not accumulate marks without
bound.

**Width tables.** `internal/text` classifies combining marks from the standard
library's Unicode categories (`Mn`, `Me`, `Cf`) and wide characters from a
curated table of East Asian Width "W" and "F" ranges - CJK, kana, Hangul,
fullwidth forms and emoji - binary-searched. It is a readable subset rather than
a generated copy of `EastAsianWidth.txt`.

**The renderer gives every wide character a span of its own.** Grouping it with
its neighbours would mean trusting the substituted CJK or emoji font's advance
to be exactly two cells, and it usually is not - the rest of the line would
drift. One span per wide character costs a few more draw calls and keeps every
column where it belongs.

## Running real programs

A shell only ever writes down the page. `vim`, `less` and `top` do something
different: they take the screen, paint a picture on it, and give it back. Four
things make that work.

**The alternate screen.** The terminal keeps two grids. The primary one is
where the shell's output accumulates and where scrollback comes from; the
alternate one is a scratch surface a program borrows with `ESC [ ? 1049 h` and
hands back with `ESC [ ? 1049 l`. Nothing on it is ever archived, which is the
rule that makes quitting `less` leave your prompt exactly as it was.

```text
    primary                        alternate
    ┌──────────────┐               ┌──────────────┐
    │ $ ls         │   ?1049h      │              │
    │ README.md    │  ──────────►  │  vim paints  │
    │ $ vim x      │  ◄──────────  │  here        │
    └──────────────┘   ?1049l      └──────────────┘
      kept intact,                   discarded on
      never repainted                the way out
```

**Scroll regions.** `ESC [ top ; bottom r` (DECSTBM) marks off the rows that
scrolling applies to, so a line feed at the bottom of the region moves those
rows and leaves a header or status line alone. `less`, `vim` and `top` all set
one immediately on startup; without it they would have to repaint the whole
window for every line.

**Editing in place.** `ICH`/`DCH` open and close gaps in a line, `IL`/`DL` in
the region, `SU`/`SD` scroll it. An editor uses these to avoid redrawing what
has not changed.

**Answering questions.** `ESC [ 6 n` asks where the cursor is, `ESC [ c` what
kind of terminal this is. The reply is written back up the PTY, arriving at the
program as though it had been typed — which you can watch happen:

```sh
printf '\033[6n'      # the shell echoes ^[[2;1R: our answer, typed back at it
```

**Tab stops.** `\t` is not "print some spaces": it moves to the next stop, and
the stops are state a program can set (`HTS`), clear (`TBC`) and jump between
(`CHT`/`CBT`). Every eighth column is the default, and it is why `ls` lines up.

**The line-drawing character set.** Before UTF-8, terminals drew boxes by
switching alphabets: after `ESC ( 0`, `q` is `─` and `x` is `│`. ncurses still
does it, and a terminal that ignores it fills the screen with `qqqqqqqq` where
the frame should be.

### The mouse

A terminal does not hand a program mouse events — it *types* them. When a
program sets `ESC [ ? 1002 h`, every click and drag becomes an escape sequence
in its input stream.

| Mode | What the program asked for |
| --- | --- |
| `?1000` | button presses and releases |
| `?1002` | those, plus motion while a button is held |
| `?1003` | every mouse move |
| `?1006` | report them in the SGR encoding |

The two encodings are worth knowing apart. The original packs everything into
three bytes with 32 added to each, so a column past 223 cannot be expressed at
all and a release cannot say *which* button was let go. `?1006` writes the
numbers as decimal text and keeps the button, which is why every modern program
asks for it.

Until a program asks, the mouse belongs to the window: nothing is reported, and
the wheel scrolls our own history.

### What a wheel turn means

One gesture, three meanings, decided in `GUI.scroll`:

```text
a program asked for the mouse  ->  wheel buttons 64/65, reported to it
the alternate screen is up     ->  arrow keys, so less and man scroll
otherwise                      ->  our own scrollback
```

The middle case is the one that feels broken when a terminal omits it: there is
no history to scroll while `less` owns the screen, so the wheel sends what the
arrow keys would.

### Tested against the real thing

Compatibility claims are only worth what they are tested against, so
`internal/terminal/apps_live_test.go` runs the actual programs on a real PTY and
reads our own virtual screen back. Every assertion is about **our** grid, never
the program's output, so a pass means our parser agreed with what the program
intended:

| Test | What it proves |
| --- | --- |
| `less` takes the screen and gives it back | the alternate screen, both ways |
| `less` pages with Page Up/Down | the tilde key sequences arrive |
| `vim` edits and writes a file | keys, alt screen and redraw, end to end |
| `vim` draws tildes and a ruler | absolute addressing and the status line |
| `nano` takes Ctrl chords, shows an inverse footer | control characters reaching a full-screen program, and reverse video held steady on a fixed row |
| `top` draws a header and leaves | scroll regions under a live program |
| `git log` pages and colours | the pager plus SGR from a real program |
| the shell echoes `ESC [ 6 n` | the reply channel, with nothing faked |

`nano` on macOS is UW PICO wearing the name, which is why the test asks for the
behaviour and not the banner. It was worth adding for two things vim does not
prove: vim's commands are ordinary letters, so nothing else checks that a
**control character** reaches a program that owns the alternate screen; and
vim's status line is plain text, so nothing else checks that a program can hold
a **reverse-video** region steady on a fixed row while the rest of the screen
changes underneath it.

## Performance: measured, then fixed

Every change in Phase 10 came from a measurement, and each one is in the
benchmarks (`*_bench_test.go`) so it can be re-run:

```sh
go test -run xxx -bench . -benchmem ./...
```

The benchmarks model three real workloads rather than synthetic ones: plain
output like `cat` of a file, coloured output like `ls` or a build log, and the
absolute-addressing redraw pattern vim produces.

### What the profile said

The first profile of `spans` - the function that turns a frame into draw calls -
was the surprise:

```text
  flat  flat%              function
 460ms 12.71%   text.isWide
 370ms 10.22%   unicode.Is
 140ms  3.87%   text.IsCombining
                ... 38% of the time, in total, answering "how wide is 'a'?"
```

Every plain ASCII character was going through the combining-mark tables and a
binary search of the wide ranges to come back with 1. **A three-line fast path
for printable ASCII** took 37% off spans and 54% off a vim-style redraw.

The next profile pointed somewhere else entirely:

```text
2440ms 41.15%   runtime.memmove
```

That is scrolling. Moving every line up by one was copying the whole grid for
each line of output.

### The changes, and what each was worth

| Change | Why | Effect |
| --- | --- | --- |
| ASCII fast path in `text.Width` | 38% of `spans` was Unicode lookups for `a` | spans −37%, redraw −54% |
| Scroll a **row index**, not the rows | `memmove` was 41% of absorbing output | one line scrolls 4.3x faster |
| `Cell` field order | a `string` between two small fields padded it to 40 bytes | 32 bytes: apply +16%, snapshot −25% |
| Borrow the parser's slices | 3 allocations per escape sequence | parser allocates **nothing** |
| Reuse the scrollback ring's memory | a line was allocated per line scrolled | steady state allocates **nothing** |
| `Snapshot.Lines` became a method | built on every redraw, used only by tests | 24 allocations a frame gone |
| Reuse the two per-frame grids | a redraw copied the screen twice into new memory | a frame allocates **nothing** |
| Pass Go bytes to AppKit, not `C.CString` | a malloc and a free per span | 10µs a frame gone |
| 16 KB PTY reads, not 4 KB | measured on a real PTY: 155 → 193 MB/s | end to end +20% |

### Before and after

| | before | after |
| --- | --- | --- |
| parse escape sequences | 82µs, 1500 allocs | **47µs, 0 allocs** (153 → 266 MB/s) |
| absorb 500 lines of output | 1.84ms, 484 allocs | **0.68ms** (0.55ms and **0 allocs** warm) |
| vim-style full redraw | 758µs, 487 allocs | **336µs, 8 allocs** |
| scroll one line | 1188ns | **279ns** |
| build one frame | 53µs, 27 allocs, 166 KB | **19µs, 0 allocs** |
| split a frame into spans | 64µs, 194 allocs | **16µs, 98 allocs** |
| resize the grid | 87µs, 6 allocs | **32µs, 3 allocs** |
| **one redraw, end to end** | ~127µs | **~35µs** |
| **output through a real PTY** | 24 MB/s | **41 MB/s** |

The last line is the one that matters, and it is also the one that says where
the remaining headroom is: draining the same PTY without parsing runs at
193 MB/s, so the screen is still the slow part. It is fast enough that a redraw
uses a fortieth of a 60-frames-a-second budget, which is where this stops.

### Scrolling by index

```text
   before: every line moves            after: the index moves
   ┌──────────┐                        rowIndex   cells
   │ line 1   │ ──┐ copied             ┌───┐   ┌──────────┐
   │ line 2   │ ──┼─► up one           │ 1 │──►│ line 1   │
   │ line 3   │ ──┘ every line         │ 2 │──►│ line 2   │
   └──────────┘     49 KB per line     │ 0 │──►│ line 3   │
                                       └───┘   └──────────┘
                                       rotate three ints
```

`rowIndex` maps a logical row to the physical row holding it. Scrolling rotates
that array - three reversals, no allocation - and the text never moves. Every
access to the grid goes through one small method, `s.row(r)`, which is what
keeps the indirection from spreading.

### Two contracts this phase introduced

**Parser events borrow their slices.** `Event.Params` and `Event.Intermediates`
point into the parser's own buffers and are valid only until the callback
returns. Cloning them per event was three allocations per escape sequence, which
on a coloured screen was most of the allocations in the program. A consumer that
keeps an event calls `Event.Clone`; `ParseAll` does it for you. Two tests pin
both halves of this down.

**Snapshot and frame grids are borrowed too.** `SnapshotInto` and `FrameInto`
write into a buffer the caller owns, so the window reuses one grid instead of
producing 166 KB of garbage per frame. The frame being drawn owns it, and only
until the next one.

### Polish

* A drag reports a resize for every pixel, but the cell count changes only every
  font-width of them. The size is now compared before acting, because acting
  means a `SIGWINCH` to the program inside - and vim redraws its whole screen
  for every one it gets.
* A burst of output used to ask for a repaint per chunk read. One request now
  stands until the frame it asked for has been drawn.
* A PTY read error that is not a hangup is carried back out of `GUI.Run` instead
  of printed to a stderr that, in a window, nobody is watching.
* The shell is waited for even when the window failed to open, so a failure
  cannot leave a shell running on a PTY nobody is reading.

### Keeping it after Phase 12

Building a frame went from 19µs to 20.7µs when a frame became a list of panes
instead of a grid — the cost of the extra indirection, and small enough to
leave. What did *not* survive the first attempt was the allocation count:
laying out the pane tree allocated two slices per redraw, which put a frame back
to 2 allocations.

`tab.layout` now appends into slices the caller hands it, the same
`SnapshotInto`/`FrameInto` shape used since Phase 10, and the `GUI` keeps the
ones it got last time. A redraw is back to **0 allocations**, splits and all.

### What was left alone

Measured and deliberately not changed: `Palette.Frame` at 24µs a frame is
already only 1 allocation and resolving colours is what it is for; the parser's
plain-text path at 198 MB/s is nowhere near being the limit; and `spans` still
allocates one string per span, which at 98 a frame is not worth the shared-buffer
trick it would take to remove. The grid is a flat slice with an index rather than
a slice of rows, which would have been a second allocation per row for no
measured gain.

## Tabs

A tab is not a view of something shared. The unit that owns a shell is a
**pane**, and a pane is a whole pipeline of its own:

```text
pane ─► shell.Session ─► parser ─► screen.Screen ─┐
pane ─► shell.Session ─► parser ─► screen.Screen ─┼─► each drawn in its rectangle
pane ─► shell.Session ─► parser ─► screen.Screen ─┘
```

That is the whole reason these two phases were small: everything from Phase 1 to
Phase 10 was already a self-contained path from a PTY to a grid, so a second tab
— and later a second pane — is a second instance of it. Nothing in `pty`,
`shell`, `parser`, `screen`, `input` or `render` knows that tabs exist, and
nothing below `terminal` knows that splits do.

Each pane keeps its own history, its own modes, its own scroll position. A pane
you are not looking at goes on reading its PTY, so a build keeps building — and
when a background *tab* prints something, a dot appears on it.

### What a tab is called

A tab is named after the pane you are looking at, and a pane is named by the
shell inside it: zsh sends `ESC ] 2 ; user@host:~/some/path BEL` before every
prompt, the screen keeps it, and `label` trims it down to the last component,
because a path is far too long to be a tab.

Before the shell has said anything, the tab is named after the **program** —
`zsh`, from `filepath.Base` of the configured shell. That gap is real and not
short: zsh sends nothing until it is about to print its first prompt, which on a
machine with oh-my-zsh installed measured **2.55 seconds**. Naming the program
means the label goes from true to more specific. Counting the tabs, which is
what it used to do, meant `shell 2` sat there for two seconds and then turned
into something unrelated.

That worked from the first line — and the bar never heard about it. Pushing the
tab list to AppKit only happened when a *background* tab lit its activity dot,
so the front tab's name was fixed at the instant the tab opened, which is the
instant before the shell has said anything at all. Hence `shell 1`, for as long
as the tab lived.

The fix is a field, not a timer: each tab remembers the title the bar was last
told, written in `tabViews` at the moment of the push, and a print that finds
them different pushes again. Compared rather than pushed blindly, because zsh
retitles at *every* prompt and a push crosses into Objective-C and relays the
whole bar — sixty times a second for a name that changes when you `cd`.

### Two locks, on purpose

```text
GUI.mu    guards the list of tabs, which one is in front, and the shape of each
          tab's pane tree
pane.mu   guards that pane's screen
pane.io   guards that pane's file descriptor
```

One lock would have been simpler, and wrong: a pane printing at full speed would
hold it while the UI thread wanted it to draw a different one. With a lock per
pane, the only thing they contend over is the list itself, which changes when a
person opens or closes something. Where both are needed, the GUI's is taken
first — and there is a test that runs a pane's screen and the draw loop against
each other under `-race`.

`pane.io` is separate from `pane.mu` because it guards something that can block
and something that can *vanish*. A `-race` run caught the case: a shell exiting
closes its PTY in the pump goroutine at the same moment a window resize asks
that pane for a new size, and `File.Fd()` read the descriptor while `Close()`
was destroying it. Resizing or writing to a pane that has already exited is not
a bug — the tree finds out a moment later — so the fix is a flag saying the
descriptor is gone, not an attempt to make the two events ordered.

### Where the bar goes

Since Phase 12 the tabs live *in* the title bar rather than under it:

```text
       tabs on top                      tabs on the left
┌───────────────────────────┐   ┌──────────┬────────────────┐
│ ●●● (Flux) project vim + │   │ ●●●      Flux             │
├───────────────────────────┤   ├──────────┼────────────────┤
│                           │   │ (Flux)   │                │
│        terminal           │   │  project │   terminal     │
│                           │   │  +       │                │
└───────────────────────────┘   └──────────┴────────────────┘
```

The bar is shown whenever there is a tab at all, not only once there are two. A
bar that appears when a second tab opens shoves everything below it down, and a
terminal that jumps is worse than one that spends a row on saying where you are.

The window is created with `NSWindowStyleMaskFullSizeContentView` and its title
hidden. The title bar is *hidden, not removed*: the window keeps its traffic
lights, its rounded corners and its drag behaviour, but the content view now
reaches all the way up and paints that row itself. Everything else follows from
that — the strip is always there because the traffic lights have to be
somewhere, it shows the window title when there is only one tab, and empty parts
of it still drag the window because `mouseDownCanMoveWindow` says so.

How much room to leave on the left is **measured**, not guessed:
`measureTrafficLights` asks the window for its zoom button and takes the right
edge of it. A constant would have been wrong the first time macOS changed the
buttons.

Left suits a terminal better than it suits a browser: tab titles are paths, and
paths are long. Choose with `-tab-position top|left` at startup, or from
**View → Tab Bar Position** while running; the ticks in the menu follow.

### One surface, not two

The chrome has no colours of its own. It is drawn in the terminal's **own
default background** and in shades of it — `shade()` lifts that one colour
towards white for the active pill, the labels and the sidebar's edge.

This is the difference between a window and a window with a title bar stuck on
top. A chrome colour that merely looks close leaves a seam across the top of the
window that the eye finds immediately, and it drifts the moment the palette
changes. Deriving it means there is nothing to keep in agreement, and when
themes arrive the chrome will follow one without being told.

There is no line under the strip for the same reason: the two are one surface.
The sidebar still gets one, because a column of tabs beside a column of text
needs somewhere to end.

Moving the bar changes how much room the grid has, so the window re-reports its
size in cells and every tab is resized to match — including the ones you cannot
see, so that switching to one never shows a grid of the wrong shape.

### Shortcuts

| | |
| --- | --- |
| `Cmd+T` | new tab |
| `Cmd+Shift+W` | close tab, panes and all |
| `Cmd+Shift+]` / `Cmd+Shift+[` | next / previous tab |
| `Cmd+1` … `Cmd+9` | go to tab *n* |
| `Cmd+D` | split the focused pane to the right |
| `Cmd+Shift+D` | split it downwards |
| `Cmd+Option+←↑↓→` | move the focus to the neighbouring pane |
| `Cmd+W` | close the focused pane (the tab goes with its last one) |
| `Cmd+C` / `Cmd+V` | copy the selection / paste |
| `Cmd+A` | select everything this pane holds, history included |
| drag | select; double click for a word, triple for a line |
| `Shift`+drag | select even while a program has asked for the mouse |

All but `Cmd+1`…`Cmd+9` are menu items, because a shortcut nobody can find is
not a feature. `Cmd+1`…`Cmd+9` are handled in `GUI.tabKey`, ahead of the encoder —
`input.Encode` drops every `Cmd` combination anyway, since Command belongs to
the application and never to the shell.

### Closing a tab means hanging up

Closing a tab does **not** close its PTY out from under the reader. It sends
`SIGHUP` to the shell's whole process group, exactly as a real terminal does
when its window goes away, and the reader closes the PTY once it has finished
with it.

The difference matters, and a test caught it: a read already blocked on a PTY
does not reliably return when that PTY is closed, so closing the master left a
goroutine blocked forever and the tab stuck in the list. A shell that exits
closes the last slave descriptor, and *that* is what makes the read return EIO.
The group rather than the shell alone, because a shell running vim would
otherwise leave vim attached to a PTY with nobody on the other end.

So a tab disappears by exactly one route, whether you clicked its close button
or typed `exit` into it: its shell ends, its read loop ends, and the read loop
takes the tab out of the list.

### Chrome

The tab bar is drawn in Objective-C rather than through `render.Layout`, and the
split is deliberate:

```text
the grid   ── pulled ──►  Draw      sixty times a second
the tabs   ── pushed ──►  SetTabs   when somebody opens or closes one
```

Pulling the tab list every frame would be work for nothing. Pushing it means the
titles cross the cgo boundary a handful of times a session, joined by newlines
so that a list does not have to be marshalled at all.

`SetTabs` applies its change immediately when it is already on the main thread
rather than always hopping to it. That is not an optimisation: `Run` pushes the
first set of tabs *before* the window exists, and the window has to know whether
there is a sidebar to make room for before it can decide how big to be.

Three views, one container that owns the geometry:

```text
ShellterContent   the chrome surface: background, the strip, the hairline
  ShellterTabBar  the pills and the "+", and nothing else
  ShellterView    the terminal grid
```

`layoutViews` is the only place any of them is positioned, so "tabs on top or on
the left" is that one function. Each of the three draws exactly one thing, which
is why the pills can be transparent where they are not filled: the surface
behind them belongs to the container.

## Splits

`Cmd+D` divides the focused pane. The panes of a tab form a binary tree — a leaf
is a pane, a branch is a split — so a second split applies to the half you are
in rather than to the whole tab:

```text
one pane            after Cmd+D           after Cmd+Shift+D on the right
┌───────────┐       ┌─────┬─────┐         ┌─────┬─────┐
│           │       │     │     │         │     │  B  │
│     A     │       │  A  │  B  │         │  A  ├─────┤
│           │       │     │     │         │     │  C  │
└───────────┘       └─────┴─────┘         └─────┴─────┘

   leaf A            beside              beside
                    ╱      ╲            ╱      ╲
                 leaf A  leaf B      leaf A   above
                                             ╱     ╲
                                          leaf B  leaf C
```

### The layout is computed, never stored

A pane does not remember where it is. `tab.layout` walks the tree and returns
where every pane goes and where the lines between them are, and it runs whenever
anyone needs to know — drawing, resizing, a click, moving the focus.

A stored rectangle would be a second copy of the truth: it would have to be
updated on every split, close and resize, and the day one of those forgot, a
pane would be drawn somewhere it no longer is. The tree has a handful of nodes,
so recomputing costs nothing — and the slices are handed back to the caller to
reuse, so the redraw path still allocates nothing at all.

A test asserts the property that actually matters: **every cell of the grid
belongs to exactly one pane or one divider.** A gap would show as stale pixels
and an overlap as two panes writing the same cell, and both are easy to
introduce and hard to see.

### One grid, many panes

The window stays a single coordinate system. A pane owns a rectangle of it, and
`drawPane` offsets what it draws by that rectangle's corner. The renderer never
learns that panes exist beyond that offset — which is why splits needed no
change to spans, cells, colours or the cursor.

Two things the renderer does have to know:

* **Only the focused pane draws a cursor.** Two cursors would both look like the
  place typing goes. The others are covered by one translucent rectangle, which
  is cheaper than recolouring every cell and dims their backgrounds too.
* **A pane is clipped to its rectangle.** A split is arranged before the shells
  have been told their new size, so for a frame or two a pane's grid and its
  rectangle can disagree. Clipping makes that harmless instead of something that
  has to be prevented.

### Moving the focus

`Cmd+Option+→` does not walk the tree. It takes every pane entirely beyond the
edge it is leaving, keeps the ones whose rows actually overlap the pane it is
leaving, and picks the nearest — ties going to whichever is closest to the
middle of where the eye already is.

The first version aimed a single probe at the neighbouring cell level with the
pane's middle, and a test caught it landing exactly on a divider and finding
nothing. Overlap-and-nearest has no such hole, and behaves the way a person
expects when the panes are uneven: you get the one you were pointing at.

### What a split is worth proving

Three live tests run real shells in real splits: that the two halves are
separate processes whose keystrokes do not leak into each other, that each half
is told *its own* width (80 columns become 40 and 39, with one for the divider —
checked by running `tput cols` in both), and that closing one half leaves the
other running with the whole tab and its full width back.

## Selection, copy and paste

Drag to select. Double click for a word, triple click for a line, Cmd+A for
everything. Cmd+C copies, Cmd+V pastes. Hold **Shift** to select while a
full-screen program is watching the mouse — that is the only way to copy out of
`vim` or `less`, which have asked for the pointer and would otherwise keep it.

The clipboard itself was the small part. Four things had to exist first.

### A line needs a name that does not change

Everything else in `screen` works in *view* coordinates: row 0 is the top of
the window, and which text that is changes every time anything scrolls. That is
right for drawing and useless for remembering. A selection made ten seconds ago
has to still mean the same words after a screenful of output, and it has to
survive the line under it being evicted from scrollback.

So lines are now numbered absolutely, from the start of the session:

```text
line 0        the first line ever written
...           later evicted from scrollback - but the number is not reused
FirstLine()   the oldest line still readable
TopLine()     the line currently drawn at view row 0
TotalLines()  one past the last line of the live grid
```

The numbers only ever go up, which took one counter: `scrollback.dropped`, the
number of lines evicted since the session began. Without it the oldest line is
always 0, every line renumbers each time the ring evicts one, and a selection
would slide quietly up the screen as output arrived.

Converting between the two is arithmetic the caller does itself — view row `v`
is `TopLine() + v` — which is why `internal/screen/lines.go` exposes the anchors
rather than a pair of conversion functions.

### A wrapped line is one line

`screen` now records, per row, whether it ran off its right edge and continues
on the next one. Copying needs it: a long command wrapped across three rows was
typed as one line, and pasting a URL broken into thirds is not a small
annoyance.

The flag lives at each row's **physical** address, alongside the cells, so
scrolling — which only permutes `rowIndex` — carries it along with the text it
describes and nothing has to be moved. It is set at the moment the wrap actually
happens, and cleared whenever an erase reaches the right edge. That second rule
is what keeps it honest when a shell redraws a line shorter than it was:
`\r`, the new text, `ESC [ K`.

It is used for copying and **not** for reflow. Resizing still does not rewrap
history, and the flags are dropped on resize rather than carried forward to say
something untrue about a width that no longer exists.

### Extraction has three rules

`Screen.Text(from, to)` walks absolute lines and applies them:

- **Trailing blanks are dropped.** A terminal line is padded to the full width;
  the spaces after the last character were never typed.
- **A wrapped line joins the next with nothing between them.**
- **A line that ended joins the next with `\n`.**

Stopping at the start of the next line means the newline at the end of this one
was selected, so it comes back. That is what makes selecting three whole lines
and pasting them produce three lines.

### The renderer did not change at all

Not one line. A selection is an *appearance* question, and this codebase has
resolved those in the palette since Phase 6 — inverse, faint, hidden. Selection
joins them, applied last so it wins over inverse video: a marked region has to
be unmistakable even where the program was already painting with the colours
swapped.

The GUI works out the selected columns of each view row once, before the cell
loop, so the cost inside it is two integer comparisons — and `nil` when nothing
is selected, which is most frames. `BenchmarkFrame` is unchanged at 20µs and
zero allocations.

Each theme carries its own `Selection` colour rather than a computed blend: a
tint that reads well on Dracula reads as "slightly dirty background" on
Solarized Light, and selection is the one highlight that has to be unmistakable.

### Pasted text is text

Newlines become carriage returns, because Return is what a terminal sends when
you press it. Control characters are dropped, tab and return aside.

That second rule is a safety measure, not tidiness. An `ESC` in something copied
off a web page would otherwise be read as an escape sequence by whatever it was
pasted into — able to move the cursor, change colours, or, in a shell that has
not asked for bracketed paste, end the line and start running it. Where the
program *has* asked (`?2004`, tracked since Phase 9 with nothing to bracket),
the text is wrapped in the markers that let it tell a paste from typing.

### What is not tested

The `NSPasteboard` bridge itself. A test that wrote to the system clipboard
would clobber whatever the person running it had copied, so the tests cover
everything on either side of it — what a drag selects, and what a paste turns
into by the time a real shell sees it — and the bridge is left to manual use.

## Settings

Everything adjustable lives in one file, `~/.config/shellter/config` — or
`$XDG_CONFIG_HOME/shellter/config` if that is set:

```text
# one of the built-in colour schemes
theme = tokyo-night

# where the tab bar sits: top or left
tab-position = left

# 1 is opaque; below that the desktop shows through
background-opacity = 0.92
background-blur = true

# points between the window edge and the text
window-padding = 12

scrollback = 5000
font-size = 14
```

Syntax is `key = value`, one per line, `#` starts a comment. A missing file is
not an error — it is the normal case. An unknown key *is* an error, because a
misspelled setting that silently does nothing is the most annoying kind of
configuration bug, and a bad file reports every bad line rather than stopping at
the first.

Every setting is also a flag, named identically:

```sh
shellter -gui -theme nord -tab-position left -background-opacity 0.9
```

The flag and the file go through **the same validator**. `Config.Set` takes the
text on either side of an `=`, and `main` feeds it `flag.Visit`'s output, so a
value can never be accepted from one and rejected by the other. `flag.Visit`
rather than `VisitAll` is what makes the precedence work: it reports only the
flags that were actually typed, so an untouched flag never overwrites the file.

### Themes

`theme = nord` picks one of eight built-in colour schemes:

| | |
|---|---|
| `shellter` | the default: grey on a dark charcoal |
| `dracula`, `gruvbox-dark`, `nord`, `solarized-dark`, `tokyo-night` | the published dark palettes, copied exactly |
| `github-light`, `solarized-light` | light, to prove the window works either way |

They live in `internal/theme`, which is on purpose the most boring package in
the project: a name, sixteen colours, a foreground, a background, and **no
behaviour at all**. That emptiness is what lets both ends of the program depend
on it without depending on each other — `config` checks that a name exists,
`terminal` turns the winner into a `Palette` — and it is why adding a theme is
adding a struct literal and nothing else.

A theme is only about **colour**. Font size, padding and opacity are settings
about the *window*, and they stay in the config file where the other window
settings are. Nothing stops a future theme from wanting a font; when that
happens it will be a different decision, made on purpose, rather than one that
crept in.

Spelling is forgiven: `Tokyo Night`, `tokyo_night` and `tokyo-night` are the
same theme. A name that does not exist is an error that **lists the ones that
do**, because a typo should not send anyone to the source to find out what they
were allowed to say.

### A light theme is not a dark theme with different numbers

Two places had quietly assumed the background was dark, and a light theme is
what made them say so out loud.

**Faint used to mean "towards black".** That is only dimmer if black is the
low-contrast end of the range. On a light theme it is the *high*-contrast end,
so faint text came out looking bolder than the text around it. `Palette.dim` now
moves the foreground towards the **background**, which is what faint has always
actually meant.

**The chrome used to mean "lighter".** Every chrome colour is a shade of the
terminal's own background — that is what keeps the title bar from drifting out
of agreement with the grid below it — and the active tab was the background
lifted 16% towards white. On a white background that is white on white. `shade`
now asks which end the background sits at, by perceptual luminance, and moves
*away* from it. The amounts did not change; only the direction did. AppKit gets
told too, with `NSAppearanceNameAqua` instead of `NSAppearanceNameDarkAqua`, so
the traffic lights and the blur behind a translucent window come out right.

### Translucency

Three things have to be arranged at once, and missing any one of them leaves the
window stubbornly opaque: the window must not claim to be opaque, its own
backdrop must be clear, and the colours painted on top have to carry the alpha.
An `NSVisualEffectView` behind everything, in behind-window blending mode, is
what frosts the desktop rather than our own pixels.

Opacity applies to the **default background only**. Text stays solid, and so
does any cell that asked for a colour of its own — otherwise coloured output
turns milky and unreadable. The same rule governs the chrome: the strip behind
the tabs is a surface and takes the alpha; the pills, the labels and the
hairline are marks on top of it and do not.

## Colours, styles and modes

A cell now carries an `Attr`: a foreground, a background and a set of flags.
Printing stamps the current pen onto the cell, which is why changing the colour
afterwards does not repaint what is already on screen.

**Colour is kept semantic.** The screen records "colour 1" or "palette entry
196", never a shade of red. Turning that into RGB is the `Palette`'s job in the
application layer, so the screen buffer has no opinion about appearance and the
renderer has no opinion about themes:

```text
screen.Color          Palette              render.Cell
(default / indexed    resolves, and        (0xRRGGBB, bold,
 / 24-bit rgb)   ───► applies inverse ───►  italic, underline,
                      faint, hidden         strikethrough)
```

SGR support: `0-9` styles and their `21-29` counterparts, `30-37`/`40-47`,
bright `90-97`/`100-107`, `39`/`49` defaults, `38;5;n`/`48;5;n` palette entries
and `38;2;r;g;b` direct colour. Inverse, faint and hidden are resolved into
plain colours before the renderer sees them; blink is tracked but not animated.

**Sequences the screen now obeys**

| | |
| --- | --- |
| movement | `CUU/CUD/CUF/CUB` (A-D), `CNL/CPL` (E/F), `CHA/HPA` (G/\`), `VPA` (d), `CUP/HVP` (H/f) |
| erasing | `ED` (J), `EL` (K), `ECH` (X) - all filling with the current background |
| editing | `ICH` (@), `DCH` (P), `IL` (L), `DL` (M), `SU` (S), `SD` (T), `REP` (b) |
| appearance | `SGR` (m), `DECSCUSR` (` q`) cursor shape |
| tab stops | `HT`, `HTS` (`ESC H`), `TBC` (g), `CHT` (I), `CBT` (Z) |
| scroll region | `DECSTBM` (r), and `DECOM` (`?6`) for addressing inside it |
| buffers | `?1049`, and the older `?47`/`?1047`/`?1048` spellings |
| modes | `?1` application cursor keys, `?7` autowrap, `?25` cursor visibility, `?12` blink, `?2004` bracketed paste, `?1004` focus, `4` insert, `20` newline |
| mouse | `?1000`, `?1002`, `?1003` tracking, `?1006` SGR encoding |
| character sets | `ESC ( 0`/`ESC ( B`, `ESC ) 0`, `SO`/`SI` |
| queries | `DA` (c), `DSR` (n), text-area size (`18t`/`19t`), title stack (`22t`/`23t`) |
| saved state | `DECSC`/`DECRC` (`ESC 7`/`ESC 8`) and the `CSI s`/`CSI u` spelling |
| scrolling | `IND` (`ESC D`), `NEL` (`ESC E`), `RI` (`ESC M`, which scrolls *down* at the top) |
| reset | `RIS` (`ESC c`), `DECALN` (`ESC # 8`) |
| titles | `OSC 0`/`OSC 2` |

Three details that are easy to get wrong: erasing fills with the *current
background* (which is how a program paints a region); `RI` at the top row
scrolls the screen the other way instead of doing nothing; and a sequence the
terminal does not implement must be *ignored*, not guessed at — silence is a
valid answer, a wrong one is not.

## Drawing attributed cells

`drawRect:` hands control straight back to Go, which walks the frame and calls
AppKit once per **span** - a horizontal stretch of cells sharing an appearance:

```text
drawRect:  ──►  shellterGoRender  ──►  shellterClear
                (Go loops)        ──►  shellterFillCells  (per span)
                                  ──►  shellterDrawText   (per span)
                                  ──►  shellterDrawCursor
```

A shell line is usually one or two spans, so an 80x24 screen costs a few dozen
draw calls rather than nearly two thousand - and the grid never has to be
serialised across the cgo boundary.

## Keyboard input

The window layer answers only "which key was pressed"; `internal/input` decides
which bytes that means. NSEvent gives two strings and they answer two different
questions - `-characters` is what the key *types* (`A` for Shift+a), while
`-charactersIgnoringModifiers` is *which key it is*, which is what identifies an
arrow and what the Ctrl arithmetic needs.

| Key | Bytes | Why |
| --- | --- | --- |
| printable | UTF-8 | as typed |
| Return | `0x0d` (CR) | the PTY's line discipline turns CR into NL (ICRNL) |
| Backspace | `0x7f` (DEL) | not BS - check `stty -a`, erase is `^?` |
| Tab | `0x09` | the shell decides it means completion |
| Escape | `0x1b` | the same byte that introduces escape sequences |
| Up/Down/Right/Left | `ESC [ A/B/C/D` | or `ESC O A/B/C/D` - see below |
| Home/End | `ESC [ H` / `ESC [ F` | following the same mode as the arrows |
| Page Up/Down, Insert, Delete | `ESC [ 5~ 6~ 2~ 3~` | the "tilde" family |
| F1-F4 | `ESC O P/Q/R/S` | from the VT100 keypad |
| F5-F12 | `ESC [ 15~ 17~ ... 24~` | the gaps at 16 and 22 are real |
| with a modifier | `ESC [ 1 ; n A` | n is a bitfield plus one: shift 1, alt 2, ctrl 4 |
| Ctrl+C | `0x03` | the line discipline raises SIGINT |
| Ctrl+D | `0x04` | end of input |
| Ctrl+L | `0x0c` | the shell redraws; the terminal never sees it |
| Cmd+anything | nothing | belongs to the application, not the shell |

**Ctrl is arithmetic, not a table.** Clearing bits 5 and 6 of an ASCII code
maps `@ A-Z [ \ ] ^ _` onto `0x00-0x1f`, which is where the whole C0 control
set comes from: `'C' & 0x1f == 0x03`. Only Ctrl+? breaks the pattern, giving
DEL.

**A held modifier changes the shape of the sequence,** because there is nowhere
else to put it: `Shift+Up` is `ESC [ 1 ; 2 A`, where the `1` is a placeholder
row and the `2` is the modifier. It also overrides application-cursor mode,
since an SS3 sequence has no parameters to carry the modifier in.

**The arrow keys depend on a mode the shell set.** In normal mode they send CSI
sequences; after the program sends `ESC [ ? 1 h` (DECCKM) they send SS3 -
`ESC O A` - which is how a program distinguishes an arrow key from someone
typing `ESC`, `[`, `A`. zsh's line editor turns this on while it reads a line
and off before running the command; `screen.Screen` tracks it from the output
stream and `input.Encode` reads it. That is the two directions meeting:

```text
zsh output ──► parser ──► screen.appCursor ──┐
                                             ▼
key press ─────────────────────────► input.Encode ──► PTY ──► zsh
```

## The screen buffer

`screen.Screen` is a `[]Cell` plus a `Cursor{Row, Col}`, both zero-based. The
cells are addressed through `rowIndex` rather than by `row*cols+col`, so that
scrolling can rotate an index instead of moving the text — see *Scrolling by
index* above.

The terminal protocol is one-based (`ESC[1;1H` is the top-left
corner), and that conversion happens in exactly one function.

| Event | Effect |
| --- | --- |
| `PRINT` | write the character at the cursor, advance one column |
| `\n` | down one row, **column unchanged** — which is why shells send CR LF |
| `\r` | column 0 |
| `\b` | one column left, stopping at the margin |
| `CSI A/B/C/D` | move the cursor up/down/right/left, clamped to the screen |
| `CSI H`, `CSI f` | absolute position, defaulting to home |
| `CSI J` | erase display: to end, to start, or all |
| `CSI K` | erase the cursor's line: to end, to start, or all |

Two details worth knowing:

* **Deferred wrap.** Printing into the last column does *not* move the cursor
  to the next line. It sets a pending-wrap flag, and the wrap happens when the
  next character actually arrives. Wrapping immediately would scroll the screen
  the instant a full-width line was finished — one line too early.
* **Scrolling.** A line feed on the bottom row of the scroll region moves the
  region up and blanks the row it frees. When the region is the whole screen and
  the primary buffer is showing, the line that scrolls off goes to scrollback;
  when a program has carved out a region or taken the alternate screen, it is
  painting a picture rather than producing output, so nothing is kept.

Erasing never moves the cursor, and any cursor movement cancels a pending wrap.

## How PTY ↔ zsh communication works

```text
┌────────────────────────┐
│ Shellter (parent)      │
│   master fd            │
└──────────┬─────────────┘
           │  kernel PTY driver + line discipline
┌──────────▼─────────────┐
│ zsh (child, own session)│
│   slave fd = 0, 1, 2   │
└────────────────────────┘
```

1. `pty.Start` opens a PTY pair and keeps the master; the slave becomes the
   child's stdin, stdout and stderr.
2. `Setsid` + `Setctty` make the child a session leader whose controlling
   terminal is that PTY, which is what makes job control and Ctrl+C work.
3. Writing to the master looks like typing to zsh; reading from the master
   returns what zsh printed.
4. When zsh exits, the last slave descriptor goes away and reads on the master
   fail with `EIO`, which the `pty` package translates to `io.EOF`.

## Run it

In a window:

```sh
go run ./cmd/shellter -gui                         # tabs in the title bar
go run ./cmd/shellter -gui -theme nord             # one of eight colour schemes
go run ./cmd/shellter -gui -tab-position left      # tabs down the side
go run ./cmd/shellter -gui -background-opacity 0.9 # let the desktop through
```

`Cmd+T` opens another shell in the same window and `Cmd+D` splits the one you
are in; the tab bar appears once there is more than one tab to show. Settings
you want every time go in `~/.config/shellter/config` — see *Settings* above.

Inside the terminal you launched it from (phases 1-3, still works):

```sh
go run ./cmd/shellter
```

Either way, `exit` (or Ctrl+D) ends the session. In the window, closing it
hangs up the shell; Cmd+Q quits.

### As an application

`go run` gives you a bare binary: the Dock draws a blank executable for it and
the menu bar calls it whatever the file is called. An application is a bundle —
a directory with a fixed shape, holding the binary, an icon, and the plist that
ties the two together.

```sh
./scripts/bundle.sh            # -> build/Shellter.app
open build/Shellter.app        # or drag it to /Applications
```

The artwork lives in `assets/Shellter.png`, one 1024-pixel picture; the script
resizes it into the ten sizes macOS asks for and packs them with `iconutil`.
The bundle is signed ad-hoc, which is enough to give it a stable identity on
this machine — not enough to get it past Gatekeeper on anyone else's.

```sh
go test ./...
```

### Seeing the event stream

```sh
SHELLTER_TRACE=/tmp/shellter.log go run ./cmd/shellter
tail -f /tmp/shellter.log      # in another window
```

Each line is one parser event:

```text
CSI     private='?' params=[2004] cmd='h'
OSC     "0;~/PERSONAL/Flux"
CSI     params=[31] cmd='m'
PRINT   'R'
CONTROL LF  (0x0a)
```

### Seeing the virtual screen

```sh
SHELLTER_SCREEN=/tmp/screen.txt go run ./cmd/shellter
# run some commands, exit, then:
cat /tmp/screen.txt
```

That file is the screen buffer's contents at the end of the session —
reconstructed purely from events, never from the raw bytes.

## Not implemented yet (on purpose)

**Terminal features.** No left/right margins (`DECLRMM`), no double-width or
double-height lines, no `DECRQM` mode queries, no sixel or image protocols, and
no reflow of wrapped lines. Blink is recorded but never animated. The parser
still recognises more than the screen acts on, and those events are skipped
rather than mishandled.

**Text.** Grapheme clusters are approximated as "base character plus combining
marks", so emoji built from ZWJ sequences (a family, a flag) occupy one cell per
component instead of being drawn as one glyph. Scrollback lines keep the width
they were written at: there is no reflow when the window is resized.

**Input.** Option is left as macOS composes it, so Option+o types `ø` rather
than sending Meta — there is no "use Option as Meta" setting yet. The numeric
keypad sends the same bytes whether or not a program asked for application
keypad mode (`ESC =`), which is harmless but not correct. Typing with an input
method does not work: the window reads keystrokes directly instead of
implementing `NSTextInputClient`, so IME composition and dead keys are out.
Paste is not wired up, though the bracketed-paste mode it would need is tracked.

**UI.** No scrollbar or scroll-position indicator, and no search. Selection has
no keyboard mode and does not auto-scroll when a drag reaches the edge of the
window — you can select only what is on screen, then scroll and select again.
There is no rectangular (block) selection, and no middle-click paste of a
primary selection, which is an X11 convention rather than a macOS one. Splits
cannot be resized by dragging their divider — a split is always down the middle — and there is no way
to swap two panes or to turn one back into a tab. No dragging tabs to reorder
them, and no way to move a tab to another window. No GPU rendering, and no font
configuration: the font is Menlo at whatever `font-size` says, and a theme sets
colours only.

**Verification.** The GUI has never been looked at by a human in this project:
every claim about it rests on the headless tests, the live tests against real
programs, and launch smoke tests. Screen-capture permission was not available
here, so nothing in this README asserts what the window *looks* like. That
matters most for the chrome, where hiding the title bar and positioning the tabs
against the traffic lights is precisely the kind of thing only an eye can
confirm — and it is how Phase 12 shipped a tab bar that said `shell 1` and a
strip whose colour did not match the grid under it. Both were found by a person
looking at a screenshot, and neither could have been found any other way.

## Likely next phases

1. Auto-scroll while dragging a selection past the edge of the window, and a
   scrollbar or scroll-position indicator. Both are about the same thing: there
   is no way to see or reach history while the mouse is busy.
2. A chosen font family, next to `font-size` in the settings file. It is the
   single largest remaining difference from the terminals this one is modelled
   on, and reloading the settings file while running is the same problem as
   reloading a theme.
3. A scrollbar or scroll-position indicator, and draggable split dividers so a
   pane can be given more than half.
4. Real text input (`NSTextInputClient`) for input methods and dead keys, plus
   Option-as-Meta.
5. Reflow. Which line breaks were soft is now recorded, for copying; making
   resize rewrap history instead of truncating it is the other half of the job.
6. The screen is still the slowest link: 41 MB/s against a PTY that can deliver
   193. The next profile would start at `Screen.print`.
