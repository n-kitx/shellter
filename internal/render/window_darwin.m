// The AppKit half of the renderer.
//
// Three objects: an NSWindow, a custom NSView that draws the frame, and a
// delegate that notices the window closing. The view pulls the frame from Go
// when it draws, and pushes keystrokes and resizes back.
//
// Compiled without ARC (cgo does not enable it), so the few long-lived objects
// here are simply never released - they live as long as the process.

#import <Cocoa/Cocoa.h>
#import <Carbon/Carbon.h> // EnableSecureEventInput
#include <stdlib.h>
#include <string.h>
#include "window_darwin.h"
#include "_cgo_export.h" // declarations of the Go functions exported below

// Four font variants, so bold and italic do not have to be faked.
static NSFont *gFont = nil;
static NSFont *gFontBold = nil;
static NSFont *gFontItalic = nil;
static NSFont *gFontBoldItalic = nil;
static NSString *gFontName = @"Menlo"; // the family the setting asked for
static double gFontSize = 0;     // the size the fonts above are at now
static double gBaseFontSize = 0; // the configured size, which Cmd+0 returns to
static double gCharWidth = 0;  // advance width of one cell
static double gLineHeight = 0; // height of one row
static NSView *gView = nil;      // the terminal grid
static NSView *gTabBar = nil;    // the chrome beside or above it
static NSView *gContent = nil;   // the container holding both
static NSView *gGlass = nil;     // frosts the desktop behind a translucent window
static BOOL gBlur = NO;          // whether the setting asks for that
static BOOL gOptionMeta = NO;    // Option+key goes to Go unmodified, as Meta
static NSView *gFindBar = nil;   // the search bar, over the grid's top-right corner
static NSSearchField *gFindField = nil;
static NSTextField *gFindStatus = nil;
static NSWindow *gWindow = nil;
static NSTrackingArea *gTracking = nil;
// gHoverArea follows the pointer over the grid all the time, for the
// terminal's own hover effects. gTracking, above, exists only while a program
// has asked for motion reports, and is what decides whether a move is reported
// to the program.
static NSTrackingArea *gHoverArea = nil;
static BOOL gLinkHover = NO, gLinkCommand = NO;

// Tab bar state. Pushed in from Go, read on the main thread only.
static NSArray<NSString *> *gTabTitles = nil;
static NSString *gTabBusy = @"";
static NSString *gTabDirs = @"";
static NSString *gTabBells = @"";
static int gActiveTab = 0;
static int gTabPosition = 0; // 0 top, 1 left
static int gHoverTab = -1;   // the tab under the pointer, or -1
static BOOL gHoverNew = NO;  // the pointer is over the "+"
static BOOL gHoverClose = NO; // the pointer is over gHoverTab's close cross
static NSString *gTitle = @"Shellter";
static BOOL gCanSplit = YES; // the focused tab has room for another pane
static id gDelegate = nil;   // the target of every menu item, context menu included

// Chrome settings, pushed in from the configuration file at startup.
static double gPad = 12;      // points between the window edge and the text
static double gOpacity = 1.0; // how solid the window is
static unsigned int gChromeRGB = 0x1b1d22; // the terminal's own background
static double gLeadInset = 78;   // room the traffic lights need, measured at setup
static double gStripHeight = 30; // the title/tab row, which is the titlebar
static double gTabBarWidth = 190; // the sidebar; the user can drag its edge

// The look of the chrome, in one place so a theme has somewhere to land later.
enum {
    kTabBarMinWidth = 120, // the sidebar, when the tabs are on the left,
    kTabBarMaxWidth = 480, // is as wide as the user dragged it, within these
    kTabBarGrip = 5,       // the strip along its right edge that drags it
    kGridMinWidth = 200,   // what a dragged sidebar always leaves the terminal
    kTabHeightLeft = 32, // one row of the sidebar
    kTabMinWidth = 110,  // a top tab never gets narrower than this
    kTabMaxWidth = 240,
    kTabGap = 4,      // between pills
    kTabInset = 5,    // between a pill and the edge of the strip
    kTabRadius = 7,   // corner radius of a pill
    kNewTabSize = 30, // the "+" button
    kFontMin = 6,     // how far Cmd+- and Cmd+= will go
    kFindWidth = 380, // the search bar
    kFindHeight = 34,
    kFontMax = 72,
};

static NSColor *rgba(int r, int g, int b, double a) {
    return [NSColor colorWithSRGBRed:r / 255.0 green:g / 255.0 blue:b / 255.0 alpha:a];
}

static NSColor *rgb(int r, int g, int b) { return rgba(r, g, b, 1.0); }

// A surface is anything the desktop shows through when the window is
// translucent: the strip behind the tabs and the terminal background. Marks on
// top of a surface - text, the active pill, the hairline - stay solid, because
// a translucent glyph on a translucent background is unreadable.
static NSColor *surface(int r, int g, int b) { return rgba(r, g, b, gOpacity); }

// themeIsLight says which end of the range the background sits at. Every
// decision below that used to be "lighter" or "darker" is really "away from the
// background": on a light theme the same amount has to go the other way, or the
// active tab comes out white on white.
//
// The weights are the usual perceptual ones - the eye counts green for most of
// brightness and blue for almost none.
static BOOL themeIsLight(unsigned int rgb) {
    double r = ((rgb >> 16) & 0xff) / 255.0;
    double g = ((rgb >> 8) & 0xff) / 255.0;
    double b = (rgb & 0xff) / 255.0;
    return (0.2126 * r + 0.7152 * g + 0.0722 * b) > 0.5;
}

// shade moves a colour away from the background by amount, from 0 (leave it) to
// 1 (all the way to the contrasting end).
// Every chrome colour is derived from one background this way, so the window is
// one surface however that background is set.
static NSColor *shade(unsigned int base, double amount, double alpha) {
    double r = ((base >> 16) & 0xff) / 255.0;
    double g = ((base >> 8) & 0xff) / 255.0;
    double b = (base & 0xff) / 255.0;
    double towards = themeIsLight(base) ? 0.0 : 1.0;
    double t = fabs(amount);
    r += (towards - r) * t;
    g += (towards - g) * t;
    b += (towards - b) * t;
    return [NSColor colorWithSRGBRed:r green:g blue:b alpha:alpha];
}

// The chrome is the terminal's own background, not a colour of its own. That is
// the difference between a window and a window with a title bar stuck on top:
// there is no seam to see, because there are not two surfaces.
static NSColor *chromeBG(void) { return shade(gChromeRGB, 0, gOpacity); }
static NSColor *chromeActiveBG(void) { return shade(gChromeRGB, 0.16, 1.0); }
static NSColor *chromeHoverBG(void) { return shade(gChromeRGB, 0.08, 1.0); }
// A button under the pointer has to show up against an active pill as well as
// against the bare strip, so it is lighter than either.
static NSColor *chromeButtonHoverBG(void) { return shade(gChromeRGB, 0.26, 1.0); }
static NSColor *chromeText(void) { return shade(gChromeRGB, 0.92, 1.0); }
static NSColor *chromeDim(void) { return shade(gChromeRGB, 0.55, 1.0); }
static NSColor *chromeAccent(void) { return rgb(0x4c, 0x8e, 0xda); }
static NSColor *chromeEdge(void) { return shade(gChromeRGB, 0.18, 1.0); }

// The line between two panes.
static NSColor *dividerColor(void) { return rgba(0x7a, 0x82, 0x94, 0.55); }

// Cursor shapes, matching render.CursorStyle.
enum {
    kCursorBlock = 0,
    kCursorUnderline = 1,
    kCursorBar = 2,
};

// Mouse buttons and actions, matching input.MouseButton and input.MouseAction.
enum {
    kMouseLeft = 0,
    kMouseMiddle = 1,
    kMouseRight = 2,
    kMouseNone = 5,
};
enum {
    kMousePress = 0,
    kMouseRelease = 1,
    kMouseMove = 2,
};

// modsFromEvent normalises NSEvent's flags to the bit values of
// input.Modifiers on the Go side. Keys and mouse clicks both need it.
static unsigned int modsFromEvent(NSEvent *event) {
    NSEventModifierFlags flags = [event modifierFlags];
    unsigned int mods = 0;
    if (flags & NSEventModifierFlagShift) {
        mods |= 1 << 0;
    }
    if (flags & NSEventModifierFlagControl) {
        mods |= 1 << 1;
    }
    if (flags & NSEventModifierFlagOption) {
        mods |= 1 << 2;
    }
    if (flags & NSEventModifierFlagCommand) {
        mods |= 1 << 3;
    }
    return mods;
}

// Flag bits, matching render.Flags on the Go side.
enum {
    kFlagBold = 1 << 0,
    kFlagItalic = 1 << 1,
    kFlagUnderline = 1 << 2,
    kFlagStrike = 1 << 3,
};

static NSColor *colorFromRGB(unsigned int rgb) {
    return [NSColor colorWithSRGBRed:((rgb >> 16) & 0xff) / 255.0
                               green:((rgb >> 8) & 0xff) / 255.0
                                blue:(rgb & 0xff) / 255.0
                               alpha:1.0];
}

// colorWithOpacity is colorFromRGB with the window's translucency applied. Only
// the default background uses it.
static NSColor *colorWithOpacity(unsigned int rgb) {
    return [colorFromRGB(rgb) colorWithAlphaComponent:gOpacity];
}

static NSFont *fontForFlags(unsigned int flags) {
    if ((flags & kFlagBold) && (flags & kFlagItalic)) {
        return gFontBoldItalic;
    }
    if (flags & kFlagBold) {
        return gFontBold;
    }
    if (flags & kFlagItalic) {
        return gFontItalic;
    }
    return gFont;
}

// ---------------------------------------------------------------------------
// The view
// ---------------------------------------------------------------------------

// cellOrigin is the top-left pixel of a cell, padding included. Everything that
// draws goes through it, so the inset is defined once.
static NSPoint cellOrigin(int row, int col) {
    return NSMakePoint(gPad + col * gCharWidth, gPad + row * gLineHeight);
}

// Text being composed by an input method - a Japanese reading before it is
// converted, or the accent of a dead key waiting for its letter. It belongs to
// the input method until it is committed, so it is drawn over the grid at the
// cursor rather than sent to the shell.
static NSAttributedString *gMarked = nil;
static NSRange gMarkedSelection = {0, 0};
// The key event being interpreted, for doCommandBySelector: to fall back on.
static NSEvent *gKeyEvent = nil;
// Where the cursor was last drawn, and the colours of the cell under it: the
// composition is drawn there, and the candidate window placed next to it.
static int gCursorRow = 0, gCursorCol = 0;
static unsigned int gCursorFG = 0xffffff, gCursorBG = 0;

// The dividers between split panes, as the last frame drew them. They are
// Go's to place, and the only way this side learns where they are is by being
// asked to draw them; keeping the rectangles lets the pointer turn into a
// resize cursor over them, which is what says they can be dragged.
enum { kMaxDividers = 64 };
static NSRect gDividers[kMaxDividers];
static int gDividerCount = 0;
static NSRect gCursorDividers[kMaxDividers]; // what the cursor rects were built from
static int gCursorDividerCount = 0;

// The drag being auto-scrolled, and the timer repeating it. See autoScroll:.
static NSEvent *gDragEvent = nil;
static NSTimer *gDragTimer = nil;

@interface ShellterView : NSView <NSTextInputClient>
@end

@implementation ShellterView

// Terminals count rows downwards from the top; AppKit's default origin is the
// bottom-left. Flipping the view makes row 0 the top row and removes a
// coordinate conversion from every drawing call.
- (BOOL)isFlipped {
    return YES;
}

- (BOOL)acceptsFirstResponder {
    return YES;
}

// The grid paints its own background, which may be translucent, so AppKit must
// not assume it covers what is behind it.
- (BOOL)isOpaque {
    return NO;
}

// A click in the terminal belongs to the terminal, never to the window's drag
// handling - otherwise selecting text would move the window.
- (BOOL)mouseDownCanMoveWindow {
    return NO;
}

- (void)drawRect:(NSRect)dirtyRect {
    // Go drives the painting: it calls shellterClear, then one fill and one
    // text call per span, then the cursor. Keeping the loop on the Go side
    // means the grid never has to be copied across the boundary.
    gDividerCount = 0;
    shellterGoRender();
    [self drawMarkedText];

    // Cursor rects are rebuilt only when a divider actually moved: most frames
    // are text arriving, and change nothing about where the panes are.
    if (gDividerCount != gCursorDividerCount ||
        memcmp(gDividers, gCursorDividers, sizeof(NSRect) * gDividerCount) != 0) {
        memcpy(gCursorDividers, gDividers, sizeof(NSRect) * gDividerCount);
        gCursorDividerCount = gDividerCount;
        [[self window] invalidateCursorRectsForView:self];
    }
}

- (void)resetCursorRects {
    for (int i = 0; i < gCursorDividerCount; i++) {
        NSRect r = gCursorDividers[i];
        NSCursor *c = r.size.height > r.size.width ? [NSCursor resizeLeftRightCursor]
                                                   : [NSCursor resizeUpDownCursor];
        [self addCursorRect:r cursor:c];
    }
}

// drawMarkedText paints a composition in progress over the cells at the
// cursor, underlined the way every Mac text field marks one. It covers the
// cells rather than pushing them along: nothing has been typed yet.
- (void)drawMarkedText {
    if ([gMarked length] == 0) {
        return;
    }
    NSString *text = [gMarked string];
    NSDictionary *attrs = @{
        NSFontAttributeName : gFont,
        NSForegroundColorAttributeName : colorFromRGB(gCursorFG),
        NSUnderlineStyleAttributeName : @(NSUnderlineStyleSingle),
    };
    double w = ceil([text sizeWithAttributes:attrs].width / gCharWidth) * gCharWidth;
    NSPoint o = cellOrigin(gCursorRow, gCursorCol);
    [colorFromRGB(gCursorBG) setFill];
    NSRectFill(NSMakeRect(o.x, o.y, w, gLineHeight));
    [text drawAtPoint:o withAttributes:attrs];
}

// ---------------------------------------------------------------------------
// Input
// ---------------------------------------------------------------------------

// isSpecialKey reports whether a key is a command to the terminal rather than
// text: Return, Tab, Escape, Delete, the arrows, the function keys. AppKit
// reports those as control characters or in the private-use block.
static BOOL isSpecialKey(NSEvent *event) {
    NSString *base = [event charactersIgnoringModifiers];
    if ([base length] == 0) {
        return YES; // a dead key alone has no characters; see keyDown:
    }
    unichar c = [base characterAtIndex:0];
    return c < 0x20 || c == 0x7f || (c >= 0xF700 && c <= 0xF8FF);
}

// keyDown: decides who a key belongs to.
//
// Most keys go to macOS's text input system first, which is what makes input
// methods and dead keys work at all: Option+E then E is one "é", and a
// Japanese reading is converted before anything reaches the shell. What comes
// out the other end arrives in insertText:.
//
// Keys that are commands rather than text go straight to Go, as they always
// did - Ctrl+C must not wait on an input method, and Return must not be
// turned into "insertNewline:". While a composition is open, though, every key
// is the input method's: Return confirms it, Escape abandons it, and the arrows
// move between candidates.
- (void)keyDown:(NSEvent *)event {
    unsigned int mods = modsFromEvent(event);
    BOOL command = (mods & ((1 << 1) | (1 << 3))) != 0; // Control or Command
    // With Option as Meta, Option is a modifier for Go to encode, not a way to
    // compose a character, so the input system is kept out of it.
    BOOL meta = gOptionMeta && (mods & (1 << 2)) != 0;
    if (![self hasMarkedText] && (command || meta ||
                                  (isSpecialKey(event) && [[event characters] length] > 0))) {
        [self sendKey:event];
        return;
    }
    gKeyEvent = event;
    [self interpretKeyEvents:@[ event ]];
    gKeyEvent = nil;
}

// sendKey hands a key to Go as the key it is.
- (void)sendKey:(NSEvent *)event {
    // Two strings, because they answer two different questions:
    //   -characters                 what the key types  ('A' for Shift+a)
    //   -charactersIgnoringModifiers which key it is    ('a', or 0xF702 for Left)
    // Everything else - which bytes to send - is decided in Go.
    NSString *chars = [event characters];
    NSString *base = [event charactersIgnoringModifiers];
    // Option as Meta sends the key as it would be without Option - Shift
    // still counts, so Option+Shift+B is Meta+B, capital - and Go adds the ESC.
    if (gOptionMeta && ([event modifierFlags] & NSEventModifierFlagOption)) {
        chars = [event charactersByApplyingModifiers:([event modifierFlags] & NSEventModifierFlagShift)];
    }
    if (chars == nil) {
        chars = @"";
    }
    if (base == nil) {
        base = @"";
    }

    unsigned int mods = modsFromEvent(event);

    const char *charsUTF8 = [chars UTF8String];
    const char *baseUTF8 = [base UTF8String];
    if (charsUTF8 == NULL || baseUTF8 == NULL) {
        return;
    }
    shellterGoKey((char *)charsUTF8, (int)strlen(charsUTF8),
                  (char *)baseUTF8, (int)strlen(baseUTF8), mods);
}

// sendText hands Go text the input system has committed. It is typed text and
// nothing else, so it travels as characters with no modifiers: Go sends it to
// the shell as UTF-8, exactly as it does a plain keystroke.
static void sendText(NSString *text) {
    const char *utf8 = [text UTF8String];
    if (utf8 == NULL || *utf8 == 0) {
        return;
    }
    shellterGoKey((char *)utf8, (int)strlen(utf8), (char *)utf8, (int)strlen(utf8), 0);
}

// ---------------------------------------------------------------------------
// NSTextInputClient: the conversation with input methods
// ---------------------------------------------------------------------------

static NSString *plainString(id string) {
    return [string isKindOfClass:[NSAttributedString class]] ? [string string] : string;
}

- (void)insertText:(id)string replacementRange:(NSRange)replacementRange {
    [self clearMarkedText];
    sendText(plainString(string));
}

- (void)setMarkedText:(id)string
        selectedRange:(NSRange)selectedRange
     replacementRange:(NSRange)replacementRange {
    NSAttributedString *marked = [string isKindOfClass:[NSAttributedString class]]
                                     ? [[string copy] autorelease]
                                     : [[[NSAttributedString alloc] initWithString:string] autorelease];
    [gMarked release];
    gMarked = [marked length] > 0 ? [marked retain] : nil;
    gMarkedSelection = selectedRange;
    [self setNeedsDisplay:YES];
}

// unmarkText is AppKit ending a composition from outside - the window losing
// focus, say. What was composed is kept, the way a text field keeps it.
- (void)unmarkText {
    NSString *text = [[[gMarked string] copy] autorelease];
    [self clearMarkedText];
    if ([text length] > 0) {
        sendText(text);
    }
}

- (void)clearMarkedText {
    if (gMarked != nil) {
        [gMarked release];
        gMarked = nil;
        [self setNeedsDisplay:YES];
    }
}

- (BOOL)hasMarkedText {
    return [gMarked length] > 0;
}

- (NSRange)markedRange {
    return [self hasMarkedText] ? NSMakeRange(0, [gMarked length]) : NSMakeRange(NSNotFound, 0);
}

- (NSRange)selectedRange {
    return [self hasMarkedText] ? gMarkedSelection : NSMakeRange(0, 0);
}

// A key the input system recognised as a command - Return while nothing was
// being composed, say, or one the input method passed on. It goes to Go as the
// key it was, since Go already knows what every key means to a terminal.
- (void)doCommandBySelector:(SEL)selector {
    if (gKeyEvent != nil) {
        [self sendKey:gKeyEvent];
    }
}

// The candidate window sits under the cursor, which is where the text will go.
- (NSRect)firstRectForCharacterRange:(NSRange)range actualRange:(NSRangePointer)actualRange {
    NSPoint o = cellOrigin(gCursorRow, gCursorCol);
    NSRect r = NSMakeRect(o.x, o.y, gCharWidth, gLineHeight);
    return [[self window] convertRectToScreen:[self convertRect:r toView:nil]];
}

// The terminal has no document for an input method to read back or point into:
// the shell owns the line being edited, not us.
- (NSArray<NSAttributedStringKey> *)validAttributesForMarkedText {
    return @[];
}

- (NSAttributedString *)attributedSubstringForProposedRange:(NSRange)range
                                                actualRange:(NSRangePointer)actualRange {
    return nil;
}

- (NSUInteger)characterIndexForPoint:(NSPoint)point {
    return NSNotFound;
}

// cellForEvent converts a mouse location into grid coordinates. This is the
// only place pixels become cells for input, as setFrameSize: is for the window.
- (void)cellForEvent:(NSEvent *)event row:(int *)row col:(int *)col {
    NSPoint p = [self convertPoint:[event locationInWindow] fromView:nil];
    int r = (int)floor((p.y - gPad) / gLineHeight);
    int c = (int)floor((p.x - gPad) / gCharWidth);
    // A drag can leave the window. The column stops at the edge; the row does
    // not, because a selection dragged above the top or below the bottom is
    // how the view is scrolled along with it. Go clamps it for programs.
    if (c < 0) {
        c = 0;
    }
    *row = r;
    *col = c;
}

- (void)reportMouse:(NSEvent *)event button:(int)button action:(int)action {
    int row = 0, col = 0;
    [self cellForEvent:event row:&row col:&col];

    // clickCount is what separates a click from a double or a triple, and only
    // AppKit knows it: it owns the timing and the slop radius that decide when
    // two clicks are one gesture. It is meaningless on a release or a drag.
    int clicks = (action == kMousePress) ? (int)[event clickCount] : 0;
    shellterGoMouse(button, action, row, col, modsFromEvent(event), clicks);
}

// scrollWheel goes to Go as a scroll gesture. Whether that becomes a scroll
// through history or a wheel button reported to the program depends on modes
// this layer does not know about, so the decision is made up there; the pointer
// position travels with it because the program needs to know where the wheel
// turned.
- (void)scrollWheel:(NSEvent *)event {
    static double accumulated = 0;

    double delta = [event scrollingDeltaY];
    if ([event hasPreciseScrollingDeltas]) {
        delta /= gLineHeight; // pixels to lines
    }
    accumulated += delta;

    // Only whole lines are sent, so a trackpad's fractional deltas accumulate
    // instead of being thrown away.
    int lines = (int)accumulated;
    if (lines != 0) {
        accumulated -= lines;
        int row = 0, col = 0;
        [self cellForEvent:event row:&row col:&col];
        shellterGoScroll(lines, MAX(row, 0), col);
    }
}

// The mouse. AppKit delivers one selector per button and per phase; they all
// funnel into the same report.
- (void)mouseDown:(NSEvent *)event {
    [self reportMouse:event button:kMouseLeft action:kMousePress];
}
- (void)mouseUp:(NSEvent *)event {
    [self stopAutoScroll];
    [self reportMouse:event button:kMouseLeft action:kMouseRelease];
}
- (void)mouseDragged:(NSEvent *)event {
    [self reportMouse:event button:kMouseLeft action:kMouseMove];
    [self autoScroll:event];
}

// A drag held still above or below the grid keeps being reported, twenty
// times a second, so that a selection carries on scrolling the view while the
// pointer waits there - AppKit only reports a drag when the mouse moves. Go
// decides what a report out there means; to a program it means nothing new.
- (void)autoScroll:(NSEvent *)event {
    NSPoint p = [self convertPoint:[event locationInWindow] fromView:nil];
    int rows = (int)floor((NSHeight([self bounds]) - 2 * gPad) / gLineHeight);
    if (p.y >= gPad && p.y < gPad + rows * gLineHeight) {
        [self stopAutoScroll];
        return;
    }
    [gDragEvent release];
    gDragEvent = [event retain];
    if (gDragTimer == nil) {
        // Common modes, so it fires while AppKit is tracking the drag.
        gDragTimer = [NSTimer timerWithTimeInterval:0.05
                                             target:self
                                           selector:@selector(autoScrollTick:)
                                           userInfo:nil
                                            repeats:YES];
        [[NSRunLoop currentRunLoop] addTimer:gDragTimer forMode:NSRunLoopCommonModes];
    }
}

- (void)autoScrollTick:(NSTimer *)timer {
    if (gDragEvent == nil || ([NSEvent pressedMouseButtons] & 1) == 0) {
        [self stopAutoScroll];
        return;
    }
    [self reportMouse:gDragEvent button:kMouseLeft action:kMouseMove];
}

- (void)stopAutoScroll {
    [gDragTimer invalidate];
    gDragTimer = nil;
    [gDragEvent release];
    gDragEvent = nil;
}
- (void)rightMouseDown:(NSEvent *)event {
    [self reportMouse:event button:kMouseRight action:kMousePress];
}
- (void)rightMouseUp:(NSEvent *)event {
    [self reportMouse:event button:kMouseRight action:kMouseRelease];
}
- (void)rightMouseDragged:(NSEvent *)event {
    [self reportMouse:event button:kMouseRight action:kMouseMove];
}
- (void)otherMouseDown:(NSEvent *)event {
    [self reportMouse:event button:kMouseMiddle action:kMousePress];
}
- (void)otherMouseUp:(NSEvent *)event {
    [self reportMouse:event button:kMouseMiddle action:kMouseRelease];
}
- (void)otherMouseDragged:(NSEvent *)event {
    [self reportMouse:event button:kMouseMiddle action:kMouseMove];
}

// Motion with no button held goes to the program only while it has asked for
// ?1003. The hover underneath - links - is the terminal's own, and always on.
- (void)mouseMoved:(NSEvent *)event {
    if (gTracking != nil) {
        [self reportMouse:event button:kMouseNone action:kMouseMove];
    }
    [self reportHover:event];
}

- (void)reportHover:(NSEvent *)event {
    int row = 0, col = 0;
    [self cellForEvent:event row:&row col:&col];
    shellterGoHover(MAX(row, 0), col, modsFromEvent(event)); // -1 means "left"
}

- (void)mouseExited:(NSEvent *)event {
    shellterGoHover(-1, -1, 0);
}

// Pressing or letting go of Command over a link changes what a click would
// do, so it is reported as a hover at the same cell - no need to move.
- (void)flagsChanged:(NSEvent *)event {
    NSPoint p = [self convertPoint:[[self window] mouseLocationOutsideOfEventStream] fromView:nil];
    if (NSPointInRect(p, [self bounds])) {
        int row = (int)floor((p.y - gPad) / gLineHeight);
        int col = (int)floor((p.x - gPad) / gCharWidth);
        shellterGoHover(MAX(row, 0), MAX(col, 0), modsFromEvent(event));
    }
    [super flagsChanged:event];
}

- (void)updateTrackingAreas {
    [super updateTrackingAreas];
    if (gHoverArea != nil) {
        [self removeTrackingArea:gHoverArea];
        [gHoverArea release];
    }
    gHoverArea = [[NSTrackingArea alloc]
        initWithRect:[self bounds]
             options:(NSTrackingMouseMoved | NSTrackingMouseEnteredAndExited |
                      NSTrackingActiveInKeyWindow | NSTrackingInVisibleRect)
               owner:self
            userInfo:nil];
    [self addTrackingArea:gHoverArea];
}

// ---------------------------------------------------------------------------
// The clipboard
// ---------------------------------------------------------------------------

// The pasteboard is the one piece of shared state in this file that other
// applications can change underneath us, so it is read at the moment of the
// paste and never cached.

void shellterSetClipboard(const char *text, int len) {
    NSString *s = [[[NSString alloc] initWithBytes:text
                                            length:(NSUInteger)len
                                          encoding:NSUTF8StringEncoding] autorelease];
    if (s == nil) {
        return;
    }
    NSPasteboard *pb = [NSPasteboard generalPasteboard];
    [pb clearContents];
    [pb writeObjects:@[s]];
}

void shellterSetFindStatus(const char *status) {
    NSString *text = [NSString stringWithUTF8String:status];
    void (^apply)(void) = ^{
      [gFindStatus setStringValue:text != nil ? text : @""];
    };
    if ([NSThread isMainThread]) {
        apply();
    } else {
        dispatch_async(dispatch_get_main_queue(), apply);
    }
}

void shellterSetOptionAsMeta(int on) {
    dispatch_async(dispatch_get_main_queue(), ^{
      gOptionMeta = on != 0;
    });
}

void shellterPost(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
      shellterGoRunPosted();
    });
}

void shellterSetLinkHover(int link, int command) {
    BOOL l = link != 0, c = command != 0;
    if (gWindow == nil || (l == gLinkHover && c == gLinkCommand)) {
        return;
    }
    gLinkHover = l;
    gLinkCommand = c;
    // The hand appears only when a click would open the link; without
    // Command a click still selects text, and a hand would promise otherwise.
    if (l && c) {
        [[NSCursor pointingHandCursor] set];
    } else {
        [[NSCursor arrowCursor] set];
    }
    [gView setToolTip:l ? @"\u2318-click to open" : nil];
}

void shellterBeep(void) {
    if ([NSThread isMainThread]) {
        NSBeep();
        return;
    }
    dispatch_async(dispatch_get_main_queue(), ^{
      NSBeep();
    });
}

void shellterAttention(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
      if (![NSApp isActive]) {
          [NSApp requestUserAttention:NSInformationalRequest];
      }
    });
}

void shellterSetCanSplit(int can) {
    dispatch_async(dispatch_get_main_queue(), ^{
      gCanSplit = can != 0;
    });
}

int shellterConfirm(const char *message, const char *detail, const char *action) {
    NSAlert *alert = [[[NSAlert alloc] init] autorelease];
    [alert setMessageText:[NSString stringWithUTF8String:message]];
    [alert setInformativeText:[NSString stringWithUTF8String:detail]];
    [alert addButtonWithTitle:[NSString stringWithUTF8String:action]];
    [alert addButtonWithTitle:@"Cancel"];
    [alert setAlertStyle:NSAlertStyleWarning];
    return [alert runModal] == NSAlertFirstButtonReturn;
}

char *shellterPrompt(const char *message, const char *detail, const char *initial) {
    NSAlert *alert = [[[NSAlert alloc] init] autorelease];
    [alert setMessageText:[NSString stringWithUTF8String:message]];
    [alert setInformativeText:[NSString stringWithUTF8String:detail]];
    [alert addButtonWithTitle:@"OK"];
    [alert addButtonWithTitle:@"Cancel"];
    NSTextField *field =
        [[[NSTextField alloc] initWithFrame:NSMakeRect(0, 0, 260, 24)] autorelease];
    [field setStringValue:[NSString stringWithUTF8String:initial]];
    [alert setAccessoryView:field];
    // The field, not the OK button, has the keyboard, with its text selected:
    // typing replaces the name and Return accepts it.
    [[alert window] setInitialFirstResponder:field];
    [field selectText:nil];
    if ([alert runModal] != NSAlertFirstButtonReturn) {
        return NULL;
    }
    const char *utf8 = [[field stringValue] UTF8String];
    return strdup(utf8 != NULL ? utf8 : "");
}

// shellterClipboard returns a copy the caller must free, or NULL when there is
// no text on the pasteboard - an image, or nothing at all.
char *shellterClipboard(void) {
    NSString *s = [[NSPasteboard generalPasteboard] stringForType:NSPasteboardTypeString];
    if (s == nil) {
        return NULL;
    }
    const char *utf8 = [s UTF8String];
    if (utf8 == NULL) {
        return NULL;
    }
    return strdup(utf8);
}

// ---------------------------------------------------------------------------
// Resizing
// ---------------------------------------------------------------------------

- (void)setFrameSize:(NSSize)newSize {
    [super setFrameSize:newSize];

    // Pixels in, character cells out. This is the only place the two units meet
    // for the window; the padding is subtracted first, so the text never sits
    // flush against the edge of the glass.
    int cols = (int)floor((newSize.width - 2 * gPad) / gCharWidth);
    int rows = (int)floor((newSize.height - 2 * gPad) / gLineHeight);
    if (cols < 1) {
        cols = 1;
    }
    if (rows < 1) {
        rows = 1;
    }
    shellterGoResize(rows, cols);
}

// ---------------------------------------------------------------------------
// Drag and drop
// ---------------------------------------------------------------------------

// Files dropped on the grid are typed as their paths, the way every Mac
// terminal does it; dropped text is pasted. Go quotes the paths and decides
// which pane they go to.
- (NSDragOperation)draggingEntered:(id<NSDraggingInfo>)sender {
    return NSDragOperationCopy;
}

- (BOOL)performDragOperation:(id<NSDraggingInfo>)sender {
    NSPasteboard *pb = [sender draggingPasteboard];
    NSArray<NSURL *> *urls = [pb readObjectsForClasses:@[ [NSURL class] ]
                                               options:@{NSPasteboardURLReadingFileURLsOnlyKey : @YES}];
    NSString *text = nil;
    int files = 0;
    if ([urls count] > 0) {
        NSMutableArray<NSString *> *paths = [NSMutableArray array];
        for (NSURL *u in urls) {
            if ([u path] != nil) {
                [paths addObject:[u path]];
            }
        }
        text = [paths componentsJoinedByString:@"\n"];
        files = 1;
    } else {
        text = [pb stringForType:NSPasteboardTypeString];
    }
    const char *utf8 = [text UTF8String];
    if (utf8 == NULL || *utf8 == 0) {
        return NO;
    }
    NSPoint p = [self convertPoint:[sender draggingLocation] fromView:nil];
    int row = MAX(0, (int)floor((p.y - gPad) / gLineHeight));
    int col = MAX(0, (int)floor((p.x - gPad) / gCharWidth));
    shellterGoDrop(row, col, (char *)utf8, (int)strlen(utf8), files);
    [[self window] makeFirstResponder:self];
    return YES;
}

@end

// ---------------------------------------------------------------------------
// The tab bar
// ---------------------------------------------------------------------------
//
// Drawn here rather than in Go, because it is chrome: it does not change with
// the terminal's output, and the geometry it needs (pixels, fonts, the pointer)
// is all on this side. Go says which tabs exist; this decides what that looks
// like and reports what was clicked.

static int tabCount(void) { return (int)[gTabTitles count]; }

// The bar is shown whenever there is a tab at all, not only once there are two.
// A tab that appears when a second one opens moves everything below it, and a
// terminal that jumps is worse than one that spends a row on saying where you
// are.
static BOOL tabBarVisible(void) { return tabCount() > 0; }

// The per-tab flag strings arrive as one character each - see shellterSetTabs.
static BOOL tabFlag(NSString *flags, int i) {
    return i >= 0 && i < (int)[flags length] && [flags characterAtIndex:i] == '1';
}

static BOOL tabIsBusy(int i) { return tabFlag(gTabBusy, i); }
static BOOL tabRang(int i) { return tabFlag(gTabBells, i); }

// A tab named after a directory is a shell waiting at a prompt, and gets a
// ">_" in front of it. A tab named by the program running in it does not: Go
// decides which is which.
static BOOL tabIsDir(int i) { return tabFlag(gTabDirs, i); }

// tabWidthTop divides the bar between the tabs, within sensible limits: wide
// enough to read, narrow enough that ten tabs still fit.
static double tabWidthTop(double barWidth) {
    int n = tabCount();
    if (n <= 0) {
        return kTabMinWidth;
    }
    double available = barWidth - kNewTabSize - 2 * kTabInset;
    double w = available / n;
    if (w > kTabMaxWidth) {
        w = kTabMaxWidth;
    }
    if (w < kTabMinWidth) {
        w = kTabMinWidth;
    }
    return w;
}

// tabRect is where tab i sits inside the bar, in the bar's own (flipped)
// coordinates. One function, used by both drawing and hit-testing, so the two
// can never disagree.
//
// The bar's frame already starts after the traffic lights in top mode, so
// nothing here has to know they exist.
static NSRect tabRect(int i, NSSize barSize) {
    if (gTabPosition == 1) {
        return NSMakeRect(kTabInset, kTabInset + i * (kTabHeightLeft + kTabGap),
                          barSize.width - 2 * kTabInset, kTabHeightLeft);
    }
    double w = tabWidthTop(barSize.width);
    return NSMakeRect(kTabInset + i * w, kTabInset,
                      w - kTabGap, barSize.height - 2 * kTabInset);
}

static NSRect newTabRect(NSSize barSize) {
    if (tabCount() <= 0) {
        return NSMakeRect(kTabInset, kTabInset, kNewTabSize, barSize.height - 2 * kTabInset);
    }
    NSRect last = tabRect(tabCount() - 1, barSize);
    if (gTabPosition == 1) {
        return NSMakeRect(kTabInset, NSMaxY(last) + kTabGap,
                          barSize.width - 2 * kTabInset, kTabHeightLeft);
    }
    return NSMakeRect(NSMaxX(last) + kTabGap, kTabInset,
                      kNewTabSize, barSize.height - 2 * kTabInset);
}

// closeRect is the little x at the trailing edge of a tab.
static NSRect closeRect(NSRect tab) {
    double size = 16;
    return NSMakeRect(NSMaxX(tab) - size - 7, NSMidY(tab) - size / 2, size, size);
}

// pill is the rounded rectangle every clickable thing in the bar is drawn as.
static void fillPill(NSRect r, NSColor *color) {
    [color setFill];
    [[NSBezierPath bezierPathWithRoundedRect:r xRadius:kTabRadius yRadius:kTabRadius] fill];
}

// drawCross and drawPlus are the two glyphs the bar draws by hand. SF Symbols
// would need a bundle; two bezier paths do not.
static void drawCross(NSRect box, NSColor *color) {
    [color setStroke];
    NSBezierPath *x = [NSBezierPath bezierPath];
    [x setLineWidth:1.3];
    [x setLineCapStyle:NSLineCapStyleRound];
    double inset = 5;
    [x moveToPoint:NSMakePoint(NSMinX(box) + inset, NSMinY(box) + inset)];
    [x lineToPoint:NSMakePoint(NSMaxX(box) - inset, NSMaxY(box) - inset)];
    [x moveToPoint:NSMakePoint(NSMaxX(box) - inset, NSMinY(box) + inset)];
    [x lineToPoint:NSMakePoint(NSMinX(box) + inset, NSMaxY(box) - inset)];
    [x stroke];
}

static void drawPlus(NSRect box, NSColor *color) {
    [color setStroke];
    NSBezierPath *p = [NSBezierPath bezierPath];
    [p setLineWidth:1.3];
    [p setLineCapStyle:NSLineCapStyleRound];
    double cx = NSMidX(box), cy = NSMidY(box), arm = 5;
    [p moveToPoint:NSMakePoint(cx - arm, cy)];
    [p lineToPoint:NSMakePoint(cx + arm, cy)];
    [p moveToPoint:NSMakePoint(cx, cy - arm)];
    [p lineToPoint:NSMakePoint(cx, cy + arm)];
    [p stroke];
}

// drawPrompt draws the shell prompt mark in front of a tab sitting at one: a
// chevron and a cursor rule, ">_" as two strokes rather than two characters.
// Drawn rather than typed so it scales with the pill and not with the label's
// font, and so it lines up the same way the "x" and the "+" do.
static void drawPrompt(NSRect box, NSColor *color) {
    double w = 13, h = 10;
    double x = NSMinX(box), y = NSMidY(box) - h / 2;

    [color setStroke];
    NSBezierPath *p = [NSBezierPath bezierPath];
    [p setLineWidth:1.5];
    [p setLineCapStyle:NSLineCapStyleRound];
    [p setLineJoinStyle:NSLineJoinStyleRound];

    // Flipped coordinates: y grows downwards, so the chevron opens rightwards
    // by going down-then-up from the top.
    [p moveToPoint:NSMakePoint(x + 1, y + 1.5)];
    [p lineToPoint:NSMakePoint(x + 5.5, y + h / 2)];
    [p lineToPoint:NSMakePoint(x + 1, y + h - 1.5)];

    // The cursor rule sits on the same baseline the chevron ends on.
    [p moveToPoint:NSMakePoint(x + 7.5, y + h - 1.5)];
    [p lineToPoint:NSMakePoint(x + w - 0.5, y + h - 1.5)];
    [p stroke];
}

// drawBell draws a small bell at the left of box, in color, and returns how
// wide it was. It is the system's own symbol, tinted: the mark for a tab whose
// program rang the bell while nobody was looking.
static double drawBell(NSRect box, NSColor *color) {
    NSImage *symbol = [NSImage imageWithSystemSymbolName:@"bell.fill" accessibilityDescription:@"Bell"];
    symbol = [symbol imageWithSymbolConfiguration:
        [NSImageSymbolConfiguration configurationWithPointSize:10 weight:NSFontWeightSemibold]];
    if (symbol == nil) {
        return 0;
    }
    NSSize size = [symbol size];
    NSImage *tinted = [NSImage imageWithSize:size flipped:NO drawingHandler:^BOOL(NSRect dst) {
      [symbol drawInRect:dst];
      [color set];
      NSRectFillUsingOperation(dst, NSCompositingOperationSourceAtop);
      return YES;
    }];
    NSRect r = NSMakeRect(NSMinX(box), NSMidY(box) - size.height / 2, size.width, size.height);
    [tinted drawInRect:r fromRect:NSZeroRect operation:NSCompositingOperationSourceOver
              fraction:1 respectFlipped:YES hints:nil];
    return size.width;
}

// drawLabel centres one line of text vertically in a rect, truncating the
// front: a tab is usually a path, and the end of a path is the useful half.
static void drawLabel(NSString *text, NSRect box, NSColor *color, double size, BOOL centred) {
    if (box.size.width < 8) {
        return;
    }
    NSMutableParagraphStyle *style = [[[NSMutableParagraphStyle alloc] init] autorelease];
    [style setLineBreakMode:NSLineBreakByTruncatingHead];
    [style setAlignment:centred ? NSTextAlignmentCenter : NSTextAlignmentLeft];
    NSFont *font = [NSFont systemFontOfSize:size weight:NSFontWeightMedium];
    NSDictionary *attrs = @{
        NSFontAttributeName : font,
        NSForegroundColorAttributeName : color,
        NSParagraphStyleAttributeName : style,
    };
    double h = [font ascender] - [font descender];
    NSRect line = NSMakeRect(box.origin.x, NSMidY(box) - h / 2, box.size.width, h);
    [text drawInRect:line withAttributes:attrs];
}

// ---------------------------------------------------------------------------
// Dragging a tab
// ---------------------------------------------------------------------------
//
// The dragged tab is drawn under the pointer, lifted above the others, and the
// ones it passes slide out of its way. Only positions are animated, and only
// while something is actually moving: a timer runs while any tab is off its
// place and stops the moment they have all arrived.

enum { kMaxTabs = 256 };
static double gTabShift[kMaxTabs]; // how far each tab is drawn from its place
static int gDragTab = -1;          // the tab under the pointer, or -1
static double gDragAt = 0;         // where its leading edge is, along the bar
static NSTimer *gTabAnim = nil;

// Everything about a drag happens along one axis: across a bar on top, down
// a bar on the left.
static double along(NSPoint p) { return gTabPosition == 1 ? p.y : p.x; }
static double leading(NSRect r) { return gTabPosition == 1 ? r.origin.y : r.origin.x; }
static double extent(NSRect r) { return gTabPosition == 1 ? r.size.height : r.size.width; }

static NSRect shiftedAlong(NSRect r, double by) {
    if (gTabPosition == 1) {
        r.origin.y += by;
    } else {
        r.origin.x += by;
    }
    return r;
}

static NSRect placedAt(NSRect r, double at) {
    return shiftedAlong(r, at - leading(r));
}

// titlebarDoubleClicked does what a double click on a titlebar does anywhere
// else on this Mac. The real titlebar is hidden under our strip, so it never
// sees the click and we have to answer it ourselves - and the answer is the
// user's, from System Settings > Desktop & Dock, not ours to pick.
static void titlebarDoubleClicked(NSWindow *window) {
    NSUserDefaults *d = [NSUserDefaults standardUserDefaults];
    NSString *action = [d stringForKey:@"AppleActionOnDoubleClick"];
    if (action == nil) {
        // The setting before macOS 11 was a yes/no for minimising.
        action = [d boolForKey:@"AppleMiniaturizeOnDoubleClick"] ? @"Minimize" : @"Maximize";
    }
    if ([action isEqualToString:@"Minimize"]) {
        [window performMiniaturize:nil];
    } else if ([action isEqualToString:@"Maximize"] || [action isEqualToString:@"Fill"]) {
        [window performZoom:nil];
    }
    // "None" does nothing, which is what it says.
}

// Declared ahead of the bar, which lays the window out again when its edge is
// dragged.
@interface ShellterContent : NSView
- (void)layoutViews;
@end

@interface ShellterTabBar : NSView
@end

@implementation ShellterTabBar

- (BOOL)isFlipped {
    return YES;
}

- (void)drawRect:(NSRect)dirtyRect {
    // The strip behind the bar is painted by the container, which owns the
    // whole chrome surface. This view draws the tabs and nothing else, so a
    // pill can be transparent where it is not filled.
    NSRect bounds = [self bounds];

    // Every tab at its place, give or take the slide it is part way through;
    // the dragged one last, so it passes over the others rather than under.
    for (int i = 0; i < tabCount(); i++) {
        if (i == gDragTab) {
            continue;
        }
        double shift = i < kMaxTabs ? gTabShift[i] : 0;
        [self drawTab:i in:shiftedAlong(tabRect(i, bounds.size), shift) lifted:NO];
    }
    if (gDragTab >= 0 && gDragTab < tabCount()) {
        [self drawTab:gDragTab in:placedAt(tabRect(gDragTab, bounds.size), gDragAt) lifted:YES];
    }

    NSRect plus = newTabRect(bounds.size);
    if (gHoverNew) {
        fillPill(plus, chromeButtonHoverBG());
    }
    drawPlus(plus, gHoverNew ? chromeText() : chromeDim());
}

// drawTab draws one tab in r. A lifted tab is the one being dragged: it gets
// the active fill and a soft shadow, the usual Mac way of saying "picked up".
- (void)drawTab:(int)i in:(NSRect)r lifted:(BOOL)lifted {
    BOOL active = (i == gActiveTab) || lifted;

    if (lifted) {
        [NSGraphicsContext saveGraphicsState];
        NSShadow *shadow = [[[NSShadow alloc] init] autorelease];
        [shadow setShadowColor:rgba(0, 0, 0, 0.45)];
        [shadow setShadowBlurRadius:8];
        [shadow setShadowOffset:NSMakeSize(0, -2)];
        [shadow set];
        fillPill(r, chromeActiveBG());
        [NSGraphicsContext restoreGraphicsState];
    } else if (active) {
        fillPill(r, chromeActiveBG());
    } else if (i == gHoverTab) {
        fillPill(r, chromeHoverBG());
    }

    // A dot for a tab that is still running something, then a prompt mark
    // if the title is a directory. Both sit ahead of the label, so the
    // label starts wherever they left off.
    double textLeft = r.origin.x + 11;
    if (tabRang(i)) {
        // The bell says more than the dot, so it stands in for it.
        textLeft += drawBell(NSMakeRect(textLeft, r.origin.y, 12, r.size.height),
                             rgb(0xe8, 0xa8, 0x38)) + 6;
    } else if (tabIsBusy(i)) {
        NSRect dot = NSMakeRect(textLeft, NSMidY(r) - 3, 6, 6);
        [chromeAccent() setFill];
        [[NSBezierPath bezierPathWithOvalInRect:dot] fill];
        textLeft += 12;
    }
    if (tabIsDir(i)) {
        drawPrompt(NSMakeRect(textLeft, r.origin.y, 13, r.size.height),
                   active ? chromeText() : chromeDim());
        textLeft += 18;
    }

    // The close cross appears only where it can be used, and the label
    // gives up its space only then, so titles do not jump on hover. A tab in
    // flight has no cross: it cannot be clicked while it is being carried.
    BOOL closable = !lifted && (active || i == gHoverTab);
    NSRect label = NSMakeRect(textLeft, r.origin.y,
                              NSMaxX(r) - textLeft - (closable ? 26 : 11), r.size.height);
    drawLabel(gTabTitles[i], label, active ? chromeText() : chromeDim(), 12, NO);
    if (closable) {
        NSRect cross = closeRect(r);
        BOOL hot = (i == gHoverTab && gHoverClose);
        if (hot) {
            [chromeButtonHoverBG() setFill];
            [[NSBezierPath bezierPathWithRoundedRect:cross xRadius:4 yRadius:4] fill];
        }
        drawCross(cross, (active || hot) ? chromeText() : chromeDim());
    }
}

// animate starts the slide timer if it is not already running. The timer is
// in the common modes so that it keeps firing while dragTab: is tracking the
// mouse, which runs the loop in its own mode.
- (void)animate {
    if (gTabAnim != nil) {
        return;
    }
    gTabAnim = [NSTimer timerWithTimeInterval:1.0 / 60.0
                                       target:self
                                     selector:@selector(step:)
                                     userInfo:nil
                                      repeats:YES];
    [[NSRunLoop currentRunLoop] addTimer:gTabAnim forMode:NSRunLoopCommonModes];
}

// step moves every tab part of the way home. Each frame closes the same
// fraction of what is left, which starts fast and settles gently - about a
// fifth of a second from a full slot away - and needs no clock to be kept.
- (void)step:(NSTimer *)timer {
    BOOL moving = NO;
    for (int i = 0; i < kMaxTabs; i++) {
        if (gTabShift[i] == 0) {
            continue;
        }
        gTabShift[i] *= 0.62;
        if (fabs(gTabShift[i]) < 0.5) {
            gTabShift[i] = 0;
        } else {
            moving = YES;
        }
    }
    [self setNeedsDisplay:YES];
    if (!moving) {
        [gTabAnim invalidate];
        gTabAnim = nil;
    }
}

// hitTab returns the tab under p, or -1, and says whether the close cross was
// hit rather than the tab itself.
- (int)hitTab:(NSPoint)p closed:(BOOL *)closed {
    *closed = NO;
    for (int i = 0; i < tabCount(); i++) {
        NSRect r = tabRect(i, [self bounds].size);
        if (!NSPointInRect(p, r)) {
            continue;
        }
        if (NSPointInRect(p, closeRect(r))) {
            *closed = YES;
        }
        return i;
    }
    return -1;
}

// gripRect is the sidebar's right edge, which drags to make it wider or
// narrower. Along the top there is nothing to drag.
- (NSRect)gripRect {
    if (gTabPosition != 1) {
        return NSZeroRect;
    }
    NSRect b = [self bounds];
    return NSMakeRect(NSMaxX(b) - kTabBarGrip, 0, kTabBarGrip, b.size.height);
}

- (void)resetCursorRects {
    [self addCursorRect:[self gripRect] cursor:[NSCursor resizeLeftRightCursor]];
}

// dragEdge follows the pointer until the button comes up, laying the window out
// again at every step so the terminal reflows under the drag rather than after
// it - the same as resizing the window does.
- (void)dragEdge:(NSEvent *)event {
    ShellterContent *content = (ShellterContent *)[self superview];
    double start = [event locationInWindow].x;
    double from = gTabBarWidth;
    for (;;) {
        NSEvent *e = [[self window]
            nextEventMatchingMask:(NSEventMaskLeftMouseDragged | NSEventMaskLeftMouseUp)];
        if ([e type] == NSEventTypeLeftMouseUp) {
            break;
        }
        double w = from + [e locationInWindow].x - start;
        double most = MIN(kTabBarMaxWidth, NSWidth([content bounds]) - kGridMinWidth);
        w = MAX(kTabBarMinWidth, MIN(w, most));
        if (w != gTabBarWidth) {
            gTabBarWidth = w;
            [content layoutViews];
        }
    }
    [[NSUserDefaults standardUserDefaults] setDouble:gTabBarWidth forKey:@"ShellterSidebarWidth"];
}

- (void)mouseDown:(NSEvent *)event {
    NSPoint p = [self convertPoint:[event locationInWindow] fromView:nil];
    if (NSPointInRect(p, [self gripRect])) {
        [self dragEdge:event];
        return;
    }
    if (NSPointInRect(p, newTabRect([self bounds].size))) {
        shellterGoNewTab();
        return;
    }
    BOOL closed = NO;
    int i = [self hitTab:p closed:&closed];
    if (i < 0) {
        // Along the top the bar is the titlebar, gaps and all; down the side
        // it is only a list.
        if (gTabPosition == 0 && [event clickCount] == 2) {
            titlebarDoubleClicked([self window]);
        }
        return;
    }
    if (closed) {
        shellterGoTabClose(i);
        return;
    }
    shellterGoTabSelect(i);
    if ([event clickCount] == 2) {
        shellterGoTabRename(i);
        return;
    }
    [self dragTab:i from:event];
}

// dragTab carries a tab along the bar until the button comes up.
//
// The tab stays under the pointer at the spot it was picked up by, held
// within the bar. When its middle crosses into another tab's place, Go is told
// to reorder - and pushes the new order straight back, since this is the main
// thread - and the tabs that were jumped over start a slide from where they
// were to where they now belong. On release the dragged tab slides the last
// little way into its own place. A press that never moves more than a few
// points was a click, and has already selected the tab.
- (void)dragTab:(int)index from:(NSEvent *)down {
    NSSize size = [self bounds].size;
    NSPoint start = [self convertPoint:[down locationInWindow] fromView:nil];
    double grab = along(start) - leading(tabRect(index, size));
    BOOL dragging = NO;

    for (;;) {
        NSEvent *e = [[self window]
            nextEventMatchingMask:(NSEventMaskLeftMouseDragged | NSEventMaskLeftMouseUp)];
        if ([e type] == NSEventTypeLeftMouseUp) {
            break;
        }
        NSPoint p = [self convertPoint:[e locationInWindow] fromView:nil];
        if (!dragging) {
            if (hypot(p.x - start.x, p.y - start.y) < 4) {
                continue;
            }
            dragging = YES;
            gHoverTab = -1;
            gHoverNew = NO;
        }

        int n = tabCount();
        if (index >= n) {
            break; // a tab closed under the drag; let go
        }
        double first = leading(tabRect(0, size));
        double last = leading(tabRect(n - 1, size));
        double step = n > 1 ? leading(tabRect(1, size)) - first : 1;
        gDragAt = MAX(first, MIN(along(p) - grab, last));
        gDragTab = index;

        int to = (int)floor((gDragAt - first) / step + 0.5);
        to = MAX(0, MIN(to, n - 1));
        if (to != index && n <= kMaxTabs) {
            [self jumpFrom:index to:to step:step];
            shellterGoTabMove(index, to);
            index = to;
            gDragTab = to;
            [self animate];
        }
        [self setNeedsDisplay:YES];
    }

    if (dragging) {
        if (index < tabCount() && index < kMaxTabs) {
            gTabShift[index] = gDragAt - leading(tabRect(index, size));
        }
        gDragTab = -1;
        [self animate];
    }
}

// jumpFrom:to: moves the slide offsets along with the tabs they belong to, and
// gives each tab that was jumped over a slide of one place - it starts where
// it was drawn a moment ago and eases into its new place.
- (void)jumpFrom:(int)from to:(int)to step:(double)step {
    double moved = gTabShift[from];
    if (from < to) {
        for (int j = from; j < to; j++) {
            gTabShift[j] = gTabShift[j + 1] + step;
        }
    } else {
        for (int j = from; j > to; j--) {
            gTabShift[j] = gTabShift[j - 1] - step;
        }
    }
    gTabShift[to] = moved;
}

- (void)mouseMoved:(NSEvent *)event {
    NSPoint p = [self convertPoint:[event locationInWindow] fromView:nil];
    BOOL closed = NO;
    int wasTab = gHoverTab;
    BOOL wasNew = gHoverNew;
    BOOL wasClose = gHoverClose;
    gHoverTab = [self hitTab:p closed:&closed];
    gHoverClose = closed;
    gHoverNew = NSPointInRect(p, newTabRect([self bounds].size));
    if (wasTab != gHoverTab || wasNew != gHoverNew || wasClose != gHoverClose) {
        [self setNeedsDisplay:YES];
    }
}

- (void)mouseExited:(NSEvent *)event {
    if (gHoverTab != -1 || gHoverNew || gHoverClose) {
        gHoverTab = -1;
        gHoverNew = NO;
        gHoverClose = NO;
        [self setNeedsDisplay:YES];
    }
}

// Clicks in the bar belong to the bar; only the empty strip around it drags
// the window.
- (BOOL)mouseDownCanMoveWindow {
    return NO;
}

- (void)updateTrackingAreas {
    [super updateTrackingAreas];
    for (NSTrackingArea *a in [self trackingAreas]) {
        [self removeTrackingArea:a];
    }
    NSTrackingArea *area = [[[NSTrackingArea alloc]
        initWithRect:[self bounds]
             options:(NSTrackingMouseMoved | NSTrackingMouseEnteredAndExited |
                      NSTrackingActiveInKeyWindow | NSTrackingInVisibleRect)
               owner:self
            userInfo:nil] autorelease];
    [self addTrackingArea:area];
}

@end

// ---------------------------------------------------------------------------
// The search bar
// ---------------------------------------------------------------------------

// ShellterFindBar is the panel the search field sits in: a rounded pill in the
// chrome's own colours, so it reads as part of the window rather than a
// system sheet dropped over it.
@interface ShellterFindBar : NSView
@end

@implementation ShellterFindBar

- (void)drawRect:(NSRect)dirtyRect {
    NSRect r = NSInsetRect([self bounds], 0.5, 0.5);
    NSBezierPath *pill = [NSBezierPath bezierPathWithRoundedRect:r xRadius:8 yRadius:8];
    [chromeActiveBG() setFill];
    [pill fill];
    [chromeEdge() setStroke];
    [pill stroke];
}

@end

// findQuery is what the search field holds, as Go receives it.
static void sendFind(int dir) {
    const char *q = [[gFindField stringValue] UTF8String];
    if (q == NULL) {
        q = "";
    }
    shellterGoFind((char *)q, (int)strlen(q), dir);
}

static BOOL findOpen(void) { return gFindBar != nil && ![gFindBar isHidden]; }

// editingFind reports whether the keyboard is in the search field, so the
// Edit menu acts on the field's text rather than the terminal's.
static BOOL editingFind(void) {
    id fr = [gWindow firstResponder];
    return findOpen() && [fr isKindOfClass:[NSTextView class]] &&
           [(NSTextView *)fr delegate] == (id)gFindField;
}

static void openFind(void) {
    [gFindBar setHidden:NO];
    [gWindow makeFirstResponder:gFindField];
    [[gFindField currentEditor] selectAll:nil];
    if ([[gFindField stringValue] length] > 0) {
        sendFind(0);
    }
}

static void closeFind(void) {
    [gFindBar setHidden:YES];
    [gFindStatus setStringValue:@""];
    [gWindow makeFirstResponder:gView];
}

// ---------------------------------------------------------------------------
// Layout
// ---------------------------------------------------------------------------

@implementation ShellterContent

- (BOOL)isFlipped {
    return YES;
}

// The chrome surface. The strip along the top is the window's titlebar - the
// real one is hidden - so it holds the traffic lights, and the window title
// when there are no tabs to show instead.
- (void)drawRect:(NSRect)dirtyRect {
    NSRect b = [self bounds];
    [chromeBG() setFill];
    NSRectFillUsingOperation(b, NSCompositingOperationSourceOver);

    // With the tabs on the left the strip has nothing in it but the traffic
    // lights, so it carries the title. With them on top they are the title.
    if (gTabPosition == 1) {
        NSRect strip = NSMakeRect(gLeadInset, 0, b.size.width - 2 * gLeadInset, gStripHeight);
        drawLabel(gTitle, strip, chromeDim(), 12.5, YES);
    }

    // A hairline under the strip, the full width of the window, so the title
    // row reads as a title row and not as the first line of output.
    [chromeEdge() setFill];
    NSRectFillUsingOperation(NSMakeRect(0, gStripHeight - 1, b.size.width, 1),
                             NSCompositingOperationSourceOver);

    // The sidebar gets one too, because a column of tabs beside a column of
    // text needs somewhere to end.
    if (tabBarVisible() && gTabPosition == 1) {
        NSRect grid = [gView frame];
        [chromeEdge() setFill];
        NSRectFillUsingOperation(NSMakeRect(grid.origin.x - 1, grid.origin.y, 1, grid.size.height),
                                 NSCompositingOperationSourceOver);
    }
}

// Empty chrome drags the window, the way a titlebar does, and the strip answers
// a double click the way one does too. The drag is asked for explicitly rather
// than left to mouseDownCanMoveWindow, because a view that lets the window
// server move the window is not reliably told about the click at all.
- (void)mouseDown:(NSEvent *)event {
    NSPoint p = [self convertPoint:[event locationInWindow] fromView:nil];
    if (p.y < gStripHeight && [event clickCount] == 2) {
        titlebarDoubleClicked([self window]);
        return;
    }
    [[self window] performWindowDragWithEvent:event];
}

- (BOOL)mouseDownCanMoveWindow {
    return NO;
}

// layoutViews gives the chrome its edges and the terminal everything else. It
// is the only place the three views are positioned, so where the tabs live is
// this function and nothing else.
//
//	tabs on top                  tabs on the left
//	+---------------------+      +---------------------+
//	| oo0 [tab][tab] +    |      | oo0     title       |
//	+---------------------+      +------+--------------+
//	|                     |      | tabs |              |
//	|       terminal      |      |  +   |   terminal   |
//	+---------------------+      +------+--------------+
//
// The strip is there either way: it is the titlebar, and the traffic lights
// have to sit somewhere.
- (void)layoutViews {
    NSRect b = [self bounds];
    double top = gStripHeight;

    if (!tabBarVisible()) {
        [gTabBar setHidden:YES];
        [gView setFrame:NSMakeRect(0, top, b.size.width, b.size.height - top)];
    } else if (gTabPosition == 1) {
        [gTabBar setHidden:NO];
        [gTabBar setFrame:NSMakeRect(0, top, gTabBarWidth, b.size.height - top)];
        [gView setFrame:NSMakeRect(gTabBarWidth, top,
                                   b.size.width - gTabBarWidth, b.size.height - top)];
    } else {
        [gTabBar setHidden:NO];
        // The bar starts where the traffic lights end, so it never has to
        // leave a gap it cannot measure.
        [gTabBar setFrame:NSMakeRect(gLeadInset, 0, b.size.width - gLeadInset, top)];
        [gView setFrame:NSMakeRect(0, top, b.size.width, b.size.height - top)];
    }
    // The search bar floats over the grid's top-right corner, wherever the
    // grid is.
    NSRect grid = [gView frame];
    [gFindBar setFrame:NSMakeRect(NSMaxX(grid) - kFindWidth - 12, NSMinY(grid) + 8,
                                  kFindWidth, kFindHeight)];
    [self setNeedsDisplay:YES];
}

- (void)setFrameSize:(NSSize)newSize {
    [super setFrameSize:newSize];
    [self layoutViews];
}

@end

static BOOL gQuitAllowed = NO; // the user already agreed to end everything

// Secure Keyboard Entry: whether the user asked for it, and whether it is on
// now. The system counts enables and disables, so they must pair exactly.
static NSString *const kSecureInputKey = @"ShellterSecureKeyboardEntry";
static BOOL gSecureOn = NO;
static NSMenuItem *gSecureItem = nil; // its menu item, which shows the setting

static void setSecureInput(BOOL on) {
    if (on == gSecureOn) {
        return;
    }
    if (on) {
        EnableSecureEventInput();
    } else {
        DisableSecureEventInput();
    }
    gSecureOn = on;
}

static void applySecureInput(void) {
    BOOL wanted = [[NSUserDefaults standardUserDefaults] boolForKey:kSecureInputKey];
    setSecureInput(wanted && [NSApp isActive]);
    [gSecureItem setState:wanted ? NSControlStateValueOn : NSControlStateValueOff];
}

// setFontSize loads the four faces at a size and measures the cell they make.
// The fonts are kept for the whole session, and replaced when Cmd+= or Cmd+-
// changes the size, so they are retained here and the old ones released.
static void setFontSize(double size) {
    // The family the settings name, if it is installed; Menlo if not, and
    // the system's fixed-pitch font if even that is missing.
    NSFont *regular = [NSFont fontWithName:gFontName size:size];
    if (regular == nil) {
        regular = [NSFont fontWithName:@"Menlo" size:size];
    }
    if (regular == nil) {
        regular = [NSFont userFixedPitchFontOfSize:size];
    }
    NSFontManager *fm = [NSFontManager sharedFontManager];
    NSFont *bold = [fm convertFont:regular toHaveTrait:NSBoldFontMask];
    NSFont *italic = [fm convertFont:regular toHaveTrait:NSItalicFontMask];
    NSFont *boldItalic = [fm convertFont:bold toHaveTrait:NSItalicFontMask];

    [gFont release];
    [gFontBold release];
    [gFontItalic release];
    [gFontBoldItalic release];
    gFont = [regular retain];
    gFontBold = [bold retain];
    gFontItalic = [italic retain];
    gFontBoldItalic = [boldItalic retain];
    gFontSize = size;

    gCharWidth = [@"M" sizeWithAttributes:@{NSFontAttributeName : gFont}].width;
    NSLayoutManager *layout = [[[NSLayoutManager alloc] init] autorelease];
    gLineHeight = [layout defaultLineHeightForFont:gFont];
}

// zoomText changes the font size and keeps the window where it is: the grid
// gains or loses rows and columns instead, the way Terminal.app does it. The
// grid view re-reports its size in cells, which is how Go and every shell hear
// about it.
static void zoomText(double size) {
    size = MAX(kFontMin, MIN(size, kFontMax));
    if (size == gFontSize) {
        return;
    }
    setFontSize(size);
    [gView setFrameSize:[gView frame].size];
    [gView setNeedsDisplay:YES];

    // Remembered with the size it was zoomed from, so that changing font-size
    // in the settings file later wins over a zoom made against the old one.
    NSUserDefaults *d = [NSUserDefaults standardUserDefaults];
    [d setDouble:gFontSize forKey:@"ShellterFontSize"];
    [d setDouble:gBaseFontSize forKey:@"ShellterFontBase"];
}

// restoredFontSize is the zoom the last session ended at, if it was made
// against the same configured size; otherwise the configured size itself.
static double restoredFontSize(double configured) {
    NSUserDefaults *d = [NSUserDefaults standardUserDefaults];
    double saved = [d doubleForKey:@"ShellterFontSize"];
    if (saved >= kFontMin && saved <= kFontMax && [d doubleForKey:@"ShellterFontBase"] == configured) {
        return saved;
    }
    return configured;
}

// dressWindow applies the chrome settings the window itself carries: whether
// it is opaque, what it is filled with behind the views, whether the desktop
// behind it is frosted, and whether AppKit draws its own parts - the traffic
// lights, menus - light or dark.
static void dressWindow(NSWindow *window) {
    [window setAppearance:[NSAppearance appearanceNamed:
        themeIsLight(gChromeRGB) ? NSAppearanceNameAqua : NSAppearanceNameDarkAqua]];
    // Translucency has to be arranged in three places at once: the window
    // must not claim to be opaque, its own backdrop must be clear, and the
    // colours painted on top carry the alpha. Miss one and the desktop never
    // shows through.
    if (gOpacity < 1.0) {
        [window setOpaque:NO];
        [window setBackgroundColor:[NSColor clearColor]];
    } else {
        [window setOpaque:YES];
        [window setBackgroundColor:chromeBG()];
    }
    [gGlass setHidden:!(gBlur && gOpacity < 1.0)];
}

// ---------------------------------------------------------------------------
// Delegate
// ---------------------------------------------------------------------------

@interface ShellterDelegate : NSObject <NSApplicationDelegate, NSWindowDelegate, NSSearchFieldDelegate>
@end

@implementation ShellterDelegate

// The menu items. AppKit wants a target and a selector, and the delegate is the
// one object that lives for the whole session, so they live here. Each one does
// nothing but tell Go what was chosen.
- (void)newTab:(id)sender {
    shellterGoNewTab();
}

- (void)closeTab:(id)sender {
    shellterGoTabClose(gActiveTab);
}

- (void)nextTab:(id)sender {
    if (tabCount() > 0) {
        shellterGoTabSelect((gActiveTab + 1) % tabCount());
    }
}

- (void)previousTab:(id)sender {
    if (tabCount() > 0) {
        shellterGoTabSelect((gActiveTab + tabCount() - 1) % tabCount());
    }
}

- (void)splitBeside:(id)sender {
    shellterGoSplit(0);
}

- (void)splitAbove:(id)sender {
    shellterGoSplit(1);
}

- (void)closePane:(id)sender {
    shellterGoClosePane();
}

- (void)copyText:(id)sender {
    if (editingFind()) {
        [(NSTextView *)[gWindow firstResponder] copy:nil];
        return;
    }
    shellterGoCopy();
}

- (void)pasteText:(id)sender {
    if (editingFind()) {
        [(NSTextView *)[gWindow firstResponder] paste:nil];
        return;
    }
    shellterGoPaste();
}

- (void)selectAllText:(id)sender {
    if (editingFind()) {
        [(NSTextView *)[gWindow firstResponder] selectAll:nil];
        return;
    }
    shellterGoSelectAll();
}

// Find, Find Next and Find Previous. The last two open the bar if it is not
// already open, which is what Cmd+G does everywhere else on a Mac.
- (void)showFind:(id)sender {
    openFind();
}

- (void)findNext:(id)sender {
    if (!findOpen()) {
        openFind();
        return;
    }
    sendFind(1);
}

- (void)hideFind:(id)sender {
    closeFind();
}

- (void)findPrevious:(id)sender {
    if (!findOpen()) {
        openFind();
        return;
    }
    sendFind(-1);
}

// Typing in the search field searches as you go, from the newest match.
- (void)controlTextDidChange:(NSNotification *)note {
    if ([note object] == gFindField) {
        sendFind(0);
    }
}

// Return steps to the next match up, Shift+Return back down, and Escape puts
// the bar away and the keyboard back in the terminal.
- (BOOL)control:(NSControl *)control textView:(NSTextView *)view doCommandBySelector:(SEL)command {
    if (control != gFindField) {
        return NO;
    }
    if (command == @selector(insertNewline:)) {
        BOOL shift = ([[NSApp currentEvent] modifierFlags] & NSEventModifierFlagShift) != 0;
        sendFind(shift ? -1 : 1);
        return YES;
    }
    if (command == @selector(cancelOperation:)) {
        closeFind();
        return YES;
    }
    return NO;
}

- (void)focusLeft:(id)sender {
    shellterGoFocusPane(0);
}

- (void)focusRight:(id)sender {
    shellterGoFocusPane(1);
}

- (void)focusUp:(id)sender {
    shellterGoFocusPane(2);
}

- (void)focusDown:(id)sender {
    shellterGoFocusPane(3);
}

- (void)renameTab:(id)sender {
    shellterGoTabRename(gActiveTab);
}

- (void)swapLeft:(id)sender {
    shellterGoSwapPane(0);
}

- (void)swapRight:(id)sender {
    shellterGoSwapPane(1);
}

- (void)swapUp:(id)sender {
    shellterGoSwapPane(2);
}

- (void)swapDown:(id)sender {
    shellterGoSwapPane(3);
}

- (void)paneToTab:(id)sender {
    shellterGoPaneToTab();
}

- (void)tabsOnTop:(id)sender {
    shellterGoTabPosition(0);
}

- (void)tabsOnLeft:(id)sender {
    shellterGoTabPosition(1);
}

// Split Right and Split Down are greyed out once the tab is full. Everything
// else the delegate answers for is always available.
- (BOOL)validateMenuItem:(NSMenuItem *)item {
    SEL action = [item action];
    if (action == @selector(splitBeside:) || action == @selector(splitAbove:)) {
        return gCanSplit;
    }
    return YES;
}

// Secure Keyboard Entry stops other programs from reading keystrokes while
// Shellter is in front - a password typed at a sudo prompt included. It is on
// only while Shellter is the active app, as in Terminal.app: left on, it would
// also stop every other app's global shortcuts working.
- (void)toggleSecureInput:(id)sender {
    NSUserDefaults *d = [NSUserDefaults standardUserDefaults];
    [d setBool:![d boolForKey:kSecureInputKey] forKey:kSecureInputKey];
    applySecureInput();
}

- (void)applicationDidBecomeActive:(NSNotification *)notification {
    applySecureInput();
}

- (void)applicationDidResignActive:(NSNotification *)notification {
    setSecureInput(NO);
}

- (void)clearBuffer:(id)sender {
    shellterGoClear();
}

- (void)openSettings:(id)sender {
    shellterGoSettings();
}

- (void)biggerText:(id)sender {
    zoomText(gFontSize + 1);
}

- (void)smallerText:(id)sender {
    zoomText(gFontSize - 1);
}

- (void)actualSizeText:(id)sender {
    zoomText(gBaseFontSize);
}

// Closing the window and quitting both end every shell, so both ask Go first:
// it knows whether anything is running, and asks the user if so. Closing the
// window goes on to quit, and the answer already given is the answer to that
// too - nobody wants to be asked twice.
- (BOOL)windowShouldClose:(NSWindow *)sender {
    gQuitAllowed = shellterGoCanQuit() != 0;
    return gQuitAllowed;
}

- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)app {
    if (!gQuitAllowed) {
        gQuitAllowed = shellterGoCanQuit() != 0;
    }
    return gQuitAllowed ? NSTerminateNow : NSTerminateCancel;
}

- (void)windowWillClose:(NSNotification *)notification {
    shellterGoClosed();
}

// Focus changes are reported because a program may have asked for them (?1004);
// vim uses them to check whether the file changed under it.
- (void)windowDidBecomeKey:(NSNotification *)notification {
    shellterGoFocus(1);
}

- (void)windowDidResignKey:(NSNotification *)notification {
    shellterGoFocus(0);
}

- (BOOL)applicationShouldTerminateAfterLastWindowClosed:(NSApplication *)app {
    return YES;
}

@end

// ---------------------------------------------------------------------------
// Entry points called from Go
// ---------------------------------------------------------------------------

// The menu bar. Small, but it is where the tab commands and the choice of
// where the bar lives are discoverable - a keyboard shortcut nobody can find is
// not a feature.
static NSMenuItem *item(NSMenu *menu, NSString *title, SEL action, NSString *key,
                        NSEventModifierFlags mods, id target) {
    NSMenuItem *m = [menu addItemWithTitle:title action:action keyEquivalent:key];
    [m setKeyEquivalentModifierMask:mods];
    [m setTarget:target];
    return m;
}

static NSMenuItem *gTopItem = nil;
static NSMenuItem *gLeftItem = nil;

static void syncTabPositionMenu(void) {
    [gTopItem setState:(gTabPosition == 0) ? NSControlStateValueOn : NSControlStateValueOff];
    [gLeftItem setState:(gTabPosition == 1) ? NSControlStateValueOn : NSControlStateValueOff];
}

// measureTrafficLights records how much room the close/minimise/zoom buttons
// need on the left of the strip.
//
// Measured rather than hardcoded: the buttons are the system's, their size and
// spacing are its business, and a constant here would be wrong the first time
// macOS changed them.
static void measureTrafficLights(NSWindow *window) {
    NSButton *zoom = [window standardWindowButton:NSWindowZoomButton];
    if (zoom == nil) {
        return; // keep the default; a window without them is not one we make
    }
    gLeadInset = NSMaxX([zoom frame]) + 12;
}

static void installMenu(id target) {
    NSMenu *bar = [[NSMenu alloc] init];

    NSMenuItem *appItem = [[NSMenuItem alloc] init];
    [bar addItem:appItem];
    // The application menu every Mac app has. A nil target sends the action
    // down the responder chain to NSApp, which implements all but Settings.
    NSEventModifierFlags cmd = NSEventModifierFlagCommand;
    NSMenu *appMenu = [[NSMenu alloc] initWithTitle:@"Shellter"];
    item(appMenu, @"About Shellter", @selector(orderFrontStandardAboutPanel:), @"", 0, nil);
    [appMenu addItem:[NSMenuItem separatorItem]];
    item(appMenu, @"Settings\u2026", @selector(openSettings:), @",", cmd, target);
    gSecureItem = item(appMenu, @"Secure Keyboard Entry", @selector(toggleSecureInput:), @"", 0, target);
    [appMenu addItem:[NSMenuItem separatorItem]];
    item(appMenu, @"Hide Shellter", @selector(hide:), @"h", cmd, nil);
    item(appMenu, @"Hide Others", @selector(hideOtherApplications:), @"h",
         cmd | NSEventModifierFlagOption, nil);
    item(appMenu, @"Show All", @selector(unhideAllApplications:), @"", 0, nil);
    [appMenu addItem:[NSMenuItem separatorItem]];
    item(appMenu, @"Quit Shellter", @selector(terminate:), @"q", cmd, nil);
    [appItem setSubmenu:appMenu];

    NSMenuItem *shellItem = [[NSMenuItem alloc] init];
    [bar addItem:shellItem];
    NSMenu *shellMenu = [[NSMenu alloc] initWithTitle:@"Shell"];
    item(shellMenu, @"New Tab", @selector(newTab:), @"t", NSEventModifierFlagCommand, target);
    item(shellMenu, @"Close Tab", @selector(closeTab:), @"w",
         NSEventModifierFlagCommand | NSEventModifierFlagShift, target);
    [shellMenu addItem:[NSMenuItem separatorItem]];
    item(shellMenu, @"Next Tab", @selector(nextTab:), @"]",
         NSEventModifierFlagCommand | NSEventModifierFlagShift, target);
    item(shellMenu, @"Previous Tab", @selector(previousTab:), @"[",
         NSEventModifierFlagCommand | NSEventModifierFlagShift, target);
    [shellMenu addItem:[NSMenuItem separatorItem]];
    item(shellMenu, @"Rename Tab\u2026", @selector(renameTab:), @"", 0, target);
    [shellItem setSubmenu:shellMenu];

    // Splits get their own menu: there are six commands, and buried in View
    // they would never be found.
    NSMenuItem *splitItem = [[NSMenuItem alloc] init];
    [bar addItem:splitItem];
    NSMenu *splitMenu = [[NSMenu alloc] initWithTitle:@"Split"];
    item(splitMenu, @"Split Right", @selector(splitBeside:), @"d",
         NSEventModifierFlagCommand, target);
    item(splitMenu, @"Split Down", @selector(splitAbove:), @"d",
         NSEventModifierFlagCommand | NSEventModifierFlagShift, target);
    [splitMenu addItem:[NSMenuItem separatorItem]];
    // Cmd+Option+arrow, the same shape as moving between windows. The arrows
    // are the private-use characters AppKit reports for them.
    NSEventModifierFlags focusMods = NSEventModifierFlagCommand | NSEventModifierFlagOption;
    item(splitMenu, @"Focus Left", @selector(focusLeft:), @"\uF702", focusMods, target);
    item(splitMenu, @"Focus Right", @selector(focusRight:), @"\uF703", focusMods, target);
    item(splitMenu, @"Focus Up", @selector(focusUp:), @"\uF700", focusMods, target);
    item(splitMenu, @"Focus Down", @selector(focusDown:), @"\uF701", focusMods, target);
    [splitMenu addItem:[NSMenuItem separatorItem]];
    // The same arrows with Shift added carry the pane itself rather than the
    // focus.
    NSEventModifierFlags swapMods = focusMods | NSEventModifierFlagShift;
    item(splitMenu, @"Swap Pane Left", @selector(swapLeft:), @"\uF702", swapMods, target);
    item(splitMenu, @"Swap Pane Right", @selector(swapRight:), @"\uF703", swapMods, target);
    item(splitMenu, @"Swap Pane Up", @selector(swapUp:), @"\uF700", swapMods, target);
    item(splitMenu, @"Swap Pane Down", @selector(swapDown:), @"\uF701", swapMods, target);
    item(splitMenu, @"Move Pane to New Tab", @selector(paneToTab:), @"", 0, target);
    [splitMenu addItem:[NSMenuItem separatorItem]];
    item(splitMenu, @"Close Pane", @selector(closePane:), @"w",
         NSEventModifierFlagCommand, target);
    [splitItem setSubmenu:splitMenu];

    // Copy and Paste keep their usual keys. They are ours rather than the
    // responder chain's because only the application knows which pane has the
    // selection, and because a paste has to be handed to a shell rather than
    // inserted into a text view.
    NSMenuItem *editItem = [[NSMenuItem alloc] init];
    [bar addItem:editItem];
    NSMenu *editMenu = [[NSMenu alloc] initWithTitle:@"Edit"];
    item(editMenu, @"Copy", @selector(copyText:), @"c", NSEventModifierFlagCommand, target);
    item(editMenu, @"Paste", @selector(pasteText:), @"v", NSEventModifierFlagCommand, target);
    [editMenu addItem:[NSMenuItem separatorItem]];
    item(editMenu, @"Select All", @selector(selectAllText:), @"a",
         NSEventModifierFlagCommand, target);
    [editMenu addItem:[NSMenuItem separatorItem]];
    item(editMenu, @"Find\u2026", @selector(showFind:), @"f", cmd, target);
    item(editMenu, @"Find Next", @selector(findNext:), @"g", cmd, target);
    item(editMenu, @"Find Previous", @selector(findPrevious:), @"g",
         cmd | NSEventModifierFlagShift, target);
    [editMenu addItem:[NSMenuItem separatorItem]];
    item(editMenu, @"Clear", @selector(clearBuffer:), @"k", cmd, target);
    [editItem setSubmenu:editMenu];

    NSMenuItem *viewItem = [[NSMenuItem alloc] init];
    [bar addItem:viewItem];
    NSMenu *viewMenu = [[NSMenu alloc] initWithTitle:@"View"];
    NSMenuItem *where = [viewMenu addItemWithTitle:@"Tab Bar Position" action:nil keyEquivalent:@""];
    NSMenu *whereMenu = [[NSMenu alloc] initWithTitle:@"Tab Bar Position"];
    gTopItem = item(whereMenu, @"Top", @selector(tabsOnTop:), @"", 0, target);
    gLeftItem = item(whereMenu, @"Left", @selector(tabsOnLeft:), @"", 0, target);
    [where setSubmenu:whereMenu];
    [viewMenu addItem:[NSMenuItem separatorItem]];
    item(viewMenu, @"Bigger", @selector(biggerText:), @"=", cmd, target);
    item(viewMenu, @"Smaller", @selector(smallerText:), @"-", cmd, target);
    item(viewMenu, @"Actual Size", @selector(actualSizeText:), @"0", cmd, target);
    // No Enter Full Screen here: AppKit adds its own to a menu titled "View",
    // with the system's shortcut, and a second one would sit beside it.
    [viewItem setSubmenu:viewMenu];

    // The Window menu. Handing it to NSApp is what makes AppKit list the
    // window in it, and add the tiling items macOS offers on its own.
    NSMenuItem *windowItem = [[NSMenuItem alloc] init];
    [bar addItem:windowItem];
    NSMenu *windowMenu = [[NSMenu alloc] initWithTitle:@"Window"];
    item(windowMenu, @"Minimize", @selector(performMiniaturize:), @"m", cmd, nil);
    item(windowMenu, @"Zoom", @selector(performZoom:), @"", 0, nil);
    [windowMenu addItem:[NSMenuItem separatorItem]];
    item(windowMenu, @"Bring All to Front", @selector(arrangeInFront:), @"", 0, nil);
    [windowItem setSubmenu:windowMenu];
    [NSApp setWindowsMenu:windowMenu];

    [NSApp setMainMenu:bar];
    syncTabPositionMenu();
}

void shellterRun(const char *title, int rows, int cols, const char *fontFamily,
                 double fontSize, double padding, double opacity, int blur,
                 unsigned int background) {
    @autoreleasepool {
        gPad = padding;
        gOpacity = opacity;
        gChromeRGB = background;
        gBlur = blur != 0;
        if (fontFamily != NULL && *fontFamily != 0) {
            gFontName = [[NSString stringWithUTF8String:fontFamily] retain];
        }
        [NSApplication sharedApplication];
        // Keys go through the text input system now, and with press-and-hold
        // on, holding one down offers accented letters instead of repeating
        // it - which is not what holding j in vim is for. Only our default is
        // changed; `defaults write` can still turn it back on for Shellter.
        [[NSUserDefaults standardUserDefaults]
            registerDefaults:@{@"ApplePressAndHoldEnabled" : @NO}];
        // Without a .app bundle the process is an "accessory" by default: no
        // Dock icon, no keyboard focus. Terminals need both.
        [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];

        gBaseFontSize = fontSize;
        setFontSize(restoredFontSize(fontSize));

        // The sidebar is as wide as it was last dragged to.
        double width = [[NSUserDefaults standardUserDefaults] doubleForKey:@"ShellterSidebarWidth"];
        if (width >= kTabBarMinWidth && width <= kTabBarMaxWidth) {
            gTabBarWidth = width;
        }

        // The window is sized for the grid plus its padding, and for the bar
        // if one is showing.
        // Room for the grid and its padding, plus the chrome: the strip is
        // always there, and the sidebar only when the tabs are on the left.
        NSRect frame = NSMakeRect(0, 0, cols * gCharWidth + 2 * gPad,
                                  rows * gLineHeight + 2 * gPad + gStripHeight);
        if (tabBarVisible() && gTabPosition == 1) {
            frame.size.width += gTabBarWidth;
        }
        NSWindow *window = [[NSWindow alloc]
            initWithContentRect:frame
                      styleMask:(NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
                                 NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable |
                                 NSWindowStyleMaskFullSizeContentView)
                        backing:NSBackingStoreBuffered
                          defer:NO];
        gTitle = [[NSString stringWithUTF8String:title] retain];
        [window setTitle:gTitle];

        // The titlebar is hidden, not removed: the window keeps its traffic
        // lights, its rounded corners and its drag behaviour, but the content
        // view now reaches all the way up and draws that row itself. That is
        // the whole trick behind tabs that sit in the titlebar.
        [window setTitlebarAppearsTransparent:YES];
        [window setTitleVisibility:NSWindowTitleHidden];
        [window setMovableByWindowBackground:YES];

        gStripHeight = NSHeight([window frame]) - NSHeight([window contentLayoutRect]);
        if (gStripHeight < 30) {
            gStripHeight = 30; // enough room for a pill
        }
        measureTrafficLights(window);

        gWindow = window;

        // Three siblings in a container: the blur behind everything, the tab
        // bar and the grid. The container owns the layout, which is the whole
        // of "tabs on top or on the left".
        ShellterContent *content = [[ShellterContent alloc] initWithFrame:frame];
        gContent = content;
        {
            // Always there, and shown only when the settings ask for blur
            // behind a translucent window - so that turning either on while
            // Shellter runs has something to show.
            NSVisualEffectView *glass = [[NSVisualEffectView alloc] initWithFrame:frame];
            [glass setMaterial:NSVisualEffectMaterialUnderWindowBackground];
            // Behind-window is what frosts the desktop rather than our own
            // pixels; "active" keeps it frosted when the window is not focused.
            [glass setBlendingMode:NSVisualEffectBlendingModeBehindWindow];
            [glass setState:NSVisualEffectStateActive];
            [glass setAutoresizingMask:(NSViewWidthSizable | NSViewHeightSizable)];
            [content addSubview:glass];
            gGlass = glass;
        }
        ShellterTabBar *tabBar = [[ShellterTabBar alloc] initWithFrame:NSZeroRect];
        gTabBar = tabBar;
        ShellterView *view = [[ShellterView alloc] initWithFrame:frame];
        gView = view;
        [content addSubview:tabBar];
        [content addSubview:view];

        ShellterFindBar *bar = [[ShellterFindBar alloc]
            initWithFrame:NSMakeRect(0, 0, kFindWidth, kFindHeight)];
        //  [ search field        ] 3 of 12  ^  v  x
        NSSearchField *field = [[NSSearchField alloc]
            initWithFrame:NSMakeRect(6, 5, kFindWidth - 176, 24)];
        [field setPlaceholderString:@"Find"];
        [[field cell] setSendsSearchStringImmediately:YES];
        NSTextField *status = [NSTextField labelWithString:@""];
        [status setFrame:NSMakeRect(kFindWidth - 166, 9, 70, 16)];
        [status setAlignment:NSTextAlignmentRight];
        [status setFont:[NSFont monospacedDigitSystemFontOfSize:11 weight:NSFontWeightRegular]];
        [status setTextColor:chromeDim()];
        [bar addSubview:field];
        [bar addSubview:status];
        // The buttons do what the keys do - up is Return, towards older
        // output; down is Shift+Return - and use the system's own symbols so
        // they look like every other find bar on the Mac. A nil target sends
        // the action down the responder chain to the delegate.
        struct { NSString *symbol, *tip; SEL action; } buttons[] = {
            {@"chevron.up", @"Older match (Return)", @selector(findNext:)},
            {@"chevron.down", @"Newer match (Shift-Return)", @selector(findPrevious:)},
            {@"xmark", @"Close (Esc)", @selector(hideFind:)},
        };
        for (int i = 0; i < 3; i++) {
            NSImage *img = [NSImage imageWithSystemSymbolName:buttons[i].symbol
                                     accessibilityDescription:buttons[i].tip];
            NSButton *b = [NSButton buttonWithImage:img target:nil action:buttons[i].action];
            [b setBordered:NO];
            [b setContentTintColor:chromeText()];
            [b setToolTip:buttons[i].tip];
            [b setRefusesFirstResponder:YES];
            [b setFrame:NSMakeRect(kFindWidth - 90 + i * 28, 5, 26, 24)];
            [bar addSubview:b];
        }
        [bar setHidden:YES];
        [content addSubview:bar];
        gFindBar = bar;
        gFindField = field;
        gFindStatus = status;
        [window setContentView:content];
        dressWindow(window);
        [content layoutViews];
        [window makeFirstResponder:view];

        ShellterDelegate *delegate = [[ShellterDelegate alloc] init];
        gDelegate = delegate;
        [view registerForDraggedTypes:@[ NSPasteboardTypeFileURL, NSPasteboardTypeString ]];
        [window setDelegate:delegate];
        [NSApp setDelegate:delegate];
        [gFindField setDelegate:delegate];

        installMenu(delegate);
        applySecureInput();
        [window center];
        // Where the window was and how big, the last time: AppKit saves the
        // frame under this name whenever it changes, and restores it here.
        [window setFrameAutosaveName:@"Shellter"];
        [window makeKeyAndOrderFront:nil];
        [NSApp activateIgnoringOtherApps:YES];

        [NSApp run];
    }
}

void shellterRedraw(void) {
    // Called from the goroutine reading the PTY; AppKit is main-thread only.
    // Repeated calls between two frames collapse into one redraw.
    dispatch_async(dispatch_get_main_queue(), ^{
      [gView setNeedsDisplay:YES];
    });
}

void shellterSetTitle(const char *title) {
    // Copied now: the C string belongs to the Go caller, which is free to
    // release it the moment this returns.
    NSString *copy = [NSString stringWithUTF8String:title];
    if (copy == nil) {
        return;
    }
    dispatch_async(dispatch_get_main_queue(), ^{
      // The window still gets the title, for the Dock and the window menu,
      // but the one a person sees is drawn in the strip.
      [gWindow setTitle:copy];
      [gTitle release];
      gTitle = [copy retain];
      [gContent setNeedsDisplay:YES];
    });
}

void shellterSetTabs(const char *titles, int len, const char *busy,
                     const char *dirs, const char *bells, int count, int active,
                     int position) {
    NSString *joined = (len > 0 && titles != NULL)
                           ? [[[NSString alloc] initWithBytes:titles
                                                       length:(NSUInteger)len
                                                     encoding:NSUTF8StringEncoding] autorelease]
                           : @"";
    NSString *busyStr = (busy != NULL) ? [NSString stringWithUTF8String:busy] : @"";
    NSString *dirsStr = (dirs != NULL) ? [NSString stringWithUTF8String:dirs] : @"";
    NSString *bellsStr = (bells != NULL) ? [NSString stringWithUTF8String:bells] : @"";
    NSArray *parts = (count > 0) ? [joined componentsSeparatedByString:@"\n"] : @[];

    // Copied out of the arguments before hopping threads: the Go side frees
    // them as soon as this returns.
    [parts retain];
    [busyStr retain];
    [dirsStr retain];
    [bellsStr retain];

    void (^apply)(void) = ^{
      BOOL wasVisible = tabBarVisible();
      int wasPosition = gTabPosition;
      // A tab opening or closing renumbers them all, so slides in flight no
      // longer belong to the tabs they were started for.
      if ((int)[parts count] != tabCount()) {
          memset(gTabShift, 0, sizeof gTabShift);
          gDragTab = -1;
      }

      [gTabTitles release];
      gTabTitles = parts;
      [gTabBusy release];
      gTabBusy = busyStr;
      [gTabDirs release];
      gTabDirs = dirsStr;
      [gTabBells release];
      gTabBells = bellsStr;
      gActiveTab = active;
      gTabPosition = position;
      if (gHoverTab >= tabCount()) {
          gHoverTab = -1;
          gHoverClose = NO;
      }

      syncTabPositionMenu();
      // Appearing, disappearing or moving all change how much room the grid
      // has, which re-reports the size to Go through setFrameSize:.
      if (wasVisible != tabBarVisible() || wasPosition != gTabPosition) {
          [(ShellterContent *)gContent layoutViews];
      }
      [gTabBar setNeedsDisplay:YES];
      [gContent setNeedsDisplay:YES];
    };

    // Run it now when we are already on the main thread. That is not just an
    // optimisation: Run pushes the first set of tabs before the window exists,
    // and the window has to know whether there is a sidebar to make room for
    // before it decides how big to be.
    if ([NSThread isMainThread]) {
        apply();
        return;
    }
    dispatch_async(dispatch_get_main_queue(), apply);
}

void shellterSetMouseTracking(int enabled) {
    dispatch_async(dispatch_get_main_queue(), ^{
      if (gTracking != nil) {
          [gView removeTrackingArea:gTracking];
          gTracking = nil;
      }
      if (!enabled) {
          return;
      }
      gTracking = [[NSTrackingArea alloc]
          initWithRect:[gView bounds]
               options:(NSTrackingMouseMoved | NSTrackingActiveInKeyWindow |
                        NSTrackingInVisibleRect)
                 owner:gView
              userInfo:nil];
      [gView addTrackingArea:gTracking];
    });
}

void shellterStop(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
      [NSApp stop:nil];
      // -stop: only takes effect once the loop processes another event, so
      // give it one. Without this the window would linger after the shell died.
      NSEvent *wake = [NSEvent otherEventWithType:NSEventTypeApplicationDefined
                                         location:NSZeroPoint
                                    modifierFlags:0
                                        timestamp:0
                                     windowNumber:0
                                          context:nil
                                          subtype:0
                                           data1:0
                                           data2:0];
      [NSApp postEvent:wake atStart:YES];
    });
}

// ---------------------------------------------------------------------------
// Drawing primitives, called back from Go during drawRect:
// ---------------------------------------------------------------------------

void shellterClear(unsigned int rgb) {
    // Source-over, and the colour carries the window's opacity: the default
    // background is the one surface the desktop shows through. Cells that ask
    // for a colour of their own stay solid, or coloured output would turn
    // milky.
    [colorWithOpacity(rgb) setFill];
    NSRectFillUsingOperation([gView bounds], NSCompositingOperationSourceOver);
}

void shellterFillCells(int row, int col, int width, unsigned int rgb) {
    NSPoint o = cellOrigin(row, col);
    NSRect r = NSMakeRect(o.x, o.y, width * gCharWidth, gLineHeight);
    [colorFromRGB(rgb) setFill];
    NSRectFill(r);
}

// cellRect is the pixel rectangle a block of cells covers.
static NSRect cellRect(int row, int col, int rows, int cols) {
    NSPoint o = cellOrigin(row, col);
    return NSMakeRect(o.x, o.y, cols * gCharWidth, rows * gLineHeight);
}

void shellterBeginPane(int row, int col, int rows, int cols) {
    [NSGraphicsContext saveGraphicsState];
    // A pane's own grid and the rectangle the layout gave it can disagree for
    // a single frame - a split is arranged before the shells have been told
    // their new size - and a pane drawing over its neighbour would be visible.
    // Clipping makes the disagreement harmless instead of having to be
    // prevented. The extra point of room is for glyphs that hang below the
    // last row, which clipping to exact cells would shave off.
    NSRectClip(NSInsetRect(cellRect(row, col, rows, cols), -1, -1));
}

void shellterEndPane(void) {
    [NSGraphicsContext restoreGraphicsState];
}

void shellterDrawDivider(int row, int col, int rows, int cols) {
    NSRect gap = cellRect(row, col, rows, cols);
    if (gDividerCount < kMaxDividers) {
        gDividers[gDividerCount++] = gap;
    }
    // A hairline down the middle of the gap rather than a filled cell: the
    // whole cell would be a thick bar, and the point of the line is to separate
    // the two panes without taking the eye off either.
    //
    // Each end of the line either reaches the edge of the grid or meets
    // another divider. At the grid's edge it carries on through the padding to
    // the edge of the view, so it divides the window and not just the text.
    // Where it meets another divider it runs half a cell further, into that
    // divider's gap, to join its hairline rather than stop short of it.
    NSRect bounds = [gView bounds];
    int gridRows = (int)floor((NSHeight(bounds) - 2 * gPad) / gLineHeight);
    int gridCols = (int)floor((NSWidth(bounds) - 2 * gPad) / gCharWidth);
    NSRect line = gap;
    if (cols <= rows) { // a tall, narrow gap: the panes are side by side
        line.origin.x = NSMidX(gap) - 0.5;
        line.size.width = 1;
        double top = row == 0 ? 0 : NSMinY(gap) - gLineHeight / 2;
        double bottom = row + rows >= gridRows ? NSHeight(bounds) : NSMaxY(gap) + gLineHeight / 2;
        line.origin.y = top;
        line.size.height = bottom - top;
    } else {
        line.origin.y = NSMidY(gap) - 0.5;
        line.size.height = 1;
        double left = col == 0 ? 0 : NSMinX(gap) - gCharWidth / 2;
        double right = col + cols >= gridCols ? NSWidth(bounds) : NSMaxX(gap) + gCharWidth / 2;
        line.origin.x = left;
        line.size.width = right - left;
    }
    [dividerColor() setFill];
    NSRectFillUsingOperation(line, NSCompositingOperationSourceOver);
}

// stringFrom wraps borrowed bytes without needing them NUL-terminated. The
// NSString copies them, so Go's memory is free the moment this returns.
static NSString *stringFrom(const char *utf8, int len) {
    if (utf8 == NULL || len <= 0) {
        return nil;
    }
    return [[[NSString alloc] initWithBytes:utf8
                                     length:(NSUInteger)len
                                   encoding:NSUTF8StringEncoding] autorelease];
}

void shellterDrawText(int row, int col, const char *utf8, int len,
                      unsigned int rgb, unsigned int flags) {
    NSString *text = stringFrom(utf8, len);
    if (text == nil) {
        return;
    }
    NSMutableDictionary *attrs = [@{
        NSFontAttributeName : fontForFlags(flags),
        NSForegroundColorAttributeName : colorFromRGB(rgb),
    } mutableCopy];
    if (flags & kFlagUnderline) {
        attrs[NSUnderlineStyleAttributeName] = @(NSUnderlineStyleSingle);
    }
    if (flags & kFlagStrike) {
        attrs[NSStrikethroughStyleAttributeName] = @(NSUnderlineStyleSingle);
    }
    [text drawAtPoint:cellOrigin(row, col) withAttributes:attrs];
}

// The cursor is drawn last, over the text.
//
// A block inverts the cell - its background becomes the text colour, and the
// glyph is redrawn in the background colour so it stays readable. An underline
// or a bar is a thin rectangle in the text colour, drawn on top of a cell that
// the span pass has already painted correctly.
void shellterDrawCursor(int row, int col, int width, const char *utf8, int len,
                        unsigned int fg, unsigned int bg, int style) {
    gCursorRow = row;
    gCursorCol = col;
    gCursorFG = fg;
    gCursorBG = bg;
    NSPoint o = cellOrigin(row, col);
    double x = o.x, y = o.y;

    if (style == kCursorUnderline) {
        [colorFromRGB(fg) setFill];
        NSRectFill(NSMakeRect(x, y + gLineHeight - 2, width * gCharWidth, 2));
        return;
    }
    if (style == kCursorBar) {
        [colorFromRGB(fg) setFill];
        NSRectFill(NSMakeRect(x, y, 2, gLineHeight));
        return;
    }

    // width is 2 when the cursor sits on a wide character, so the block covers
    // the whole glyph rather than half of it.
    shellterFillCells(row, col, width, fg);
    NSString *text = stringFrom(utf8, len);
    if (text == nil) {
        return;
    }
    [text drawAtPoint:NSMakePoint(x, y)
       withAttributes:@{
           NSFontAttributeName : gFont,
           NSForegroundColorAttributeName : colorFromRGB(bg),
       }];
}

// ---------------------------------------------------------------------------
// Settings changed while running
// ---------------------------------------------------------------------------

void shellterSetFont(const char *family, double size) {
    NSString *name = (family != NULL && *family != 0) ? [NSString stringWithUTF8String:family] : @"Menlo";
    [gFontName release];
    gFontName = [name retain];
    gBaseFontSize = size;
    setFontSize(size);
    // Re-reporting the grid's size in cells is what tells Go, and every
    // shell, that the same window now holds a different number of them.
    [gView setFrameSize:[gView frame].size];
    [gView setNeedsDisplay:YES];
}

void shellterSetChrome(double padding, double opacity, int blur, unsigned int background) {
    gPad = padding;
    gOpacity = opacity;
    gBlur = blur != 0;
    gChromeRGB = background;
    dressWindow(gWindow);
    [(ShellterContent *)gContent layoutViews];
    [gView setFrameSize:[gView frame].size]; // padding changes the cell count
    [gContent setNeedsDisplay:YES];
    [gTabBar setNeedsDisplay:YES];
    [gView setNeedsDisplay:YES];
}

// ---------------------------------------------------------------------------
// The context menu, the scroll indicator, the visual bell
// ---------------------------------------------------------------------------

void shellterContextMenu(int canCopy) {
    NSEvent *event = [NSApp currentEvent];
    if (event == nil || gView == nil) {
        return;
    }
    NSMenu *menu = [[[NSMenu alloc] initWithTitle:@""] autorelease];
    [menu setAutoenablesItems:NO];
    NSEventModifierFlags cmd = NSEventModifierFlagCommand;
    BOOL canPaste = [[NSPasteboard generalPasteboard] canReadObjectForClasses:@[ [NSString class] ]
                                                                      options:nil];
    [item(menu, @"Copy", @selector(copyText:), @"c", cmd, gDelegate) setEnabled:canCopy != 0];
    [item(menu, @"Paste", @selector(pasteText:), @"v", cmd, gDelegate) setEnabled:canPaste];
    item(menu, @"Select All", @selector(selectAllText:), @"a", cmd, gDelegate);
    [menu addItem:[NSMenuItem separatorItem]];
    [item(menu, @"Split Right", @selector(splitBeside:), @"d", cmd, gDelegate) setEnabled:gCanSplit];
    [item(menu, @"Split Down", @selector(splitAbove:), @"d", cmd | NSEventModifierFlagShift, gDelegate)
        setEnabled:gCanSplit];
    [menu addItem:[NSMenuItem separatorItem]];
    item(menu, @"Clear", @selector(clearBuffer:), @"k", cmd, gDelegate);
    [NSMenu popUpContextMenu:menu withEvent:event forView:gView];
}

// paneArea is a pane's rectangle, carried out to the edge of the view on any
// side where the pane meets the edge of the grid - the padding belongs to it.
static NSRect paneArea(int row, int col, int rows, int cols) {
    NSRect b = [gView bounds];
    int gridRows = (int)floor((NSHeight(b) - 2 * gPad) / gLineHeight);
    int gridCols = (int)floor((NSWidth(b) - 2 * gPad) / gCharWidth);
    NSRect r = cellRect(row, col, rows, cols);
    double minX = col == 0 ? 0 : NSMinX(r), minY = row == 0 ? 0 : NSMinY(r);
    double maxX = col + cols >= gridCols ? NSWidth(b) : NSMaxX(r);
    double maxY = row + rows >= gridRows ? NSHeight(b) : NSMaxY(r);
    return NSMakeRect(minX, minY, maxX - minX, maxY - minY);
}

// The scroll indicator is a thin rounded thumb, the shape of a Mac overlay
// scroller: in the padding beside the text where there is room for it,
// otherwise just inside the pane's right edge.
void shellterDrawScrollbar(int row, int col, int rows, int cols, int above,
                           int visible, int total, unsigned int rgb) {
    NSRect cells = cellRect(row, col, rows, cols);
    NSRect area = paneArea(row, col, rows, cols);
    double w = 5;
    double room = NSMaxX(area) - NSMaxX(cells);
    double x = room >= w + 4 ? NSMaxX(cells) + (room - w) / 2 : NSMaxX(cells) - w - 2;

    double h = NSHeight(cells);
    double th = MIN(h, MAX(h * visible / total, 24));
    double ty = NSMinY(cells) + (h - th) * above / MAX(total - visible, 1);
    [[colorFromRGB(rgb) colorWithAlphaComponent:0.4] setFill];
    [[NSBezierPath bezierPathWithRoundedRect:NSMakeRect(x, ty, w, th) xRadius:w / 2 yRadius:w / 2] fill];
}

void shellterFlash(int row, int col, int rows, int cols, unsigned int rgb) {
    [[colorFromRGB(rgb) colorWithAlphaComponent:0.18] setFill];
    NSRectFillUsingOperation(paneArea(row, col, rows, cols), NSCompositingOperationSourceOver);
}
