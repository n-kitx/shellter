#ifndef SHELLTER_WINDOW_H
#define SHELLTER_WINDOW_H

// The C surface the Go side calls. Everything here must be called from the
// main thread except shellterRedraw and shellterStop, which hop to it
// themselves.

// shellterRun opens the window and runs the AppKit event loop. It returns when
// the loop is stopped. padding is the gap in points between the window edge and
// the text, opacity how solid the window is, and blur whether to frost what
// shows through a translucent one.
void shellterRun(const char *title, int rows, int cols, const char *fontFamily,
                 double fontSize, double lineHeight, double padding, double opacity,
                 int blur, unsigned int background);

// shellterSetFont and shellterSetChrome change the font and the window's dress
// while it is open. lineHeight scales the rows: 1 is the font's own spacing.
// Main thread only.
void shellterSetFont(const char *family, double size, double lineHeight);
void shellterSetChrome(double padding, double opacity, int blur, unsigned int background);

// shellterSetOptionAsMeta says whether Option+key is passed on unmodified, as
// Meta. Safe to call from any thread.
void shellterSetOptionAsMeta(int on);

// shellterPost asks the main thread to run what Go has queued with Post. Safe
// to call from any thread.
void shellterPost(void);

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

// shellterBeep plays the system alert sound. Safe to call from any thread.
void shellterBeep(void);

// shellterAttention bounces the Dock icon once if the app is not active. Safe
// to call from any thread.
void shellterAttention(void);

// shellterContextMenu pops up the right-click menu at the current event and
// returns when it closes. canCopy enables Copy. Main thread only.
void shellterContextMenu(int canCopy);

// shellterSetCanSplit enables or disables the split menu items. Safe to call
// from any thread.
void shellterSetCanSplit(int can);

// shellterConfirm shows a modal alert with the action and Cancel, and returns 1
// if the action was chosen. Main thread only.
int shellterConfirm(const char *message, const char *detail, const char *action);

// shellterPrompt shows a modal alert with a text field holding initial, and
// returns what was typed as a copy the caller must free, or NULL on Cancel.
// Main thread only.
char *shellterPrompt(const char *message, const char *detail, const char *initial);

// shellterSetLinkHover sets the pointer and tooltip for a link under it.
// Main thread only.
void shellterSetLinkHover(int link, int command);

// shellterSetFindStatus shows a search's result beside the search field.
// Safe to call from any thread.
void shellterSetFindStatus(const char *status);

// The clipboard. shellterClipboard returns a copy the caller must free, or
// NULL when the pasteboard holds no text.
void shellterSetClipboard(const char *text, int len);
char *shellterClipboard(void);

// shellterSetTabs replaces the contents of the tab bar and says where it goes.
//
// The titles arrive as one string with '\n' between them, which saves
// marshalling an array across cgo for something that changes a few times a
// session. busy, dirs and bells are strings of '0'/'1', one character per tab:
// busy marks a tab that has printed since it was last looked at, dirs one
// whose title is a directory, bells one that rang while in the background.
// position is 0 for top and 1 for left. Safe to call from any thread.
void shellterSetTabs(const char *titles, int len, const char *busy,
                     const char *dirs, const char *bells, int count, int active,
                     int position);

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
// shellterDrawCursor draws the cursor. fg and bg are the *cell's* own colours;
// a block cursor inverts them, an underline or bar is painted in fg.
// style: 0 block, 1 underline, 2 bar.
void shellterDrawCursor(int row, int col, int width, const char *utf8, int len, unsigned int fg, unsigned int bg, int style);
// shellterDrawScrollbar draws a pane's scroll indicator: a thumb along its
// right edge, placed by how many of total lines are above the visible ones.
// When shown is false it is drawn only while the pointer is over it.
void shellterDrawScrollbar(int row, int col, int rows, int cols, int above,
                           int visible, int total, unsigned int rgb, int shown);
// shellterFlash washes a pane in a colour, for the visual bell.
void shellterFlash(int row, int col, int rows, int cols, unsigned int rgb);

#endif
