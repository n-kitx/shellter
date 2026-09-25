#ifndef SHELLTER_WINDOW_H
#define SHELLTER_WINDOW_H

// The C surface the Go side calls. Everything here must be called from the
// main thread except shellterRedraw and shellterStop, which hop to it
// themselves.

// shellterRun opens the window and runs the AppKit event loop. It returns when
// the loop is stopped. padding is the gap in points between the window edge and
// the text, opacity how solid the window is, and blur whether to frost what
// shows through a translucent one.
void shellterRun(const char *title, int rows, int cols, double fontSize, double padding,
                 double opacity, int blur, unsigned int background);

// shellterRedraw marks the view dirty. Safe to call from any thread.
void shellterRedraw(void);

// shellterStop makes shellterRun return. Safe to call from any thread.
void shellterStop(void);

// shellterSetTitle changes the window title. Safe to call from any thread.
void shellterSetTitle(const char *title);

// shellterSetMouseTracking turns plain mouse-motion reporting on and off. It
// costs a callback per mouse move, so it is only installed while a program has
// actually asked for motion (?1003). Safe to call from any thread.
void shellterSetMouseTracking(int enabled);

// shellterConfirm shows a modal alert with the action and Cancel, and returns 1
// if the action was chosen. Main thread only.
int shellterConfirm(const char *message, const char *detail, const char *action);

// The clipboard. shellterClipboard returns a copy the caller must free, or
// NULL when the pasteboard holds no text.
void shellterSetClipboard(const char *text, int len);
char *shellterClipboard(void);

// shellterSetTabs replaces the contents of the tab bar and says where it goes.
//
// The titles arrive as one string with '\n' between them, which saves
// marshalling an array across cgo for something that changes a few times a
// session. busy and dirs are strings of '0'/'1', one character per tab: busy
// marks a tab that has printed since it was last looked at, dirs one whose
// title is a directory. position is 0 for top and 1 for left. Safe to call from
// any thread.
void shellterSetTabs(const char *titles, int len, const char *busy,
                     const char *dirs, int count, int active, int position);

// The drawing primitives Go calls back into while a frame is being painted.
// Positions are in character cells, colours are 0xRRGGBB, and flags match
// render.Flags: 1 bold, 2 italic, 4 underline, 8 strikethrough.
void shellterClear(unsigned int rgb);
void shellterFillCells(int row, int col, int width, unsigned int rgb);
// Text arrives as a pointer and a length into Go's own memory, valid only for
// the duration of the call. Copying it into a C string first cost a malloc and
// a free per span, measured at 10us a frame.
void shellterDrawText(int row, int col, const char *utf8, int len, unsigned int rgb, unsigned int flags);
// shellterBeginPane clips everything drawn until shellterEndPane to one pane's
// rectangle, so a pane whose grid is momentarily the wrong size cannot paint
// over its neighbour.
void shellterBeginPane(int row, int col, int rows, int cols);
void shellterEndPane(void);
// shellterDrawDivider draws the line between two panes, inside the one-cell gap
// the layout left for it.
void shellterDrawDivider(int row, int col, int rows, int cols);
// shellterDimCells lays a translucent wash over a pane that does not have the
// focus, so the one that does stands out.
void shellterDimCells(int row, int col, int rows, int cols);
// shellterDrawCursor draws the cursor. fg and bg are the *cell's* own colours;
// a block cursor inverts them, an underline or bar is painted in fg.
// style: 0 block, 1 underline, 2 bar.
void shellterDrawCursor(int row, int col, int width, const char *utf8, int len, unsigned int fg, unsigned int bg, int style);

#endif
