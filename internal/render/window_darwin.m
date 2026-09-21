// The AppKit half of the renderer.
//
// Three objects: an NSWindow, a custom NSView that draws the frame, and a
// delegate that notices the window closing. The view pulls the frame from Go
// when it draws, and pushes keystrokes and resizes back.
//
// Compiled without ARC (cgo does not enable it), so the few long-lived objects
// here are simply never released - they live as long as the process.

#import <Cocoa/Cocoa.h>
#include <stdlib.h>
#include <string.h>
#include "window_darwin.h"
#include "_cgo_export.h" // declarations of the Go functions exported below

// Four font variants, so bold and italic do not have to be faked.
static NSFont *gFont = nil;
static NSFont *gFontBold = nil;
static NSFont *gFontItalic = nil;
static NSFont *gFontBoldItalic = nil;
static double gCharWidth = 0;  // advance width of one cell
static double gLineHeight = 0; // height of one row
static NSView *gView = nil;      // the terminal grid
static NSView *gTabBar = nil;    // the chrome beside or above it
static NSView *gContent = nil;   // the container holding both
static NSWindow *gWindow = nil;
static NSTrackingArea *gTracking = nil;

// Tab bar state. Pushed in from Go, read on the main thread only.
static NSArray<NSString *> *gTabTitles = nil;
static NSString *gTabBusy = @"";
static int gActiveTab = 0;
static int gTabPosition = 0; // 0 top, 1 left
static int gHoverTab = -1;   // the tab under the pointer, or -1
static BOOL gHoverNew = NO;  // the pointer is over the "+"
static NSString *gTitle = @"Shellter";

// Chrome settings, pushed in from the configuration file at startup.
static double gPad = 12;      // points between the window edge and the text
static double gOpacity = 1.0; // how solid the window is
static unsigned int gChromeRGB = 0x1b1d22; // the terminal's own background
static double gLeadInset = 78;   // room the traffic lights need, measured at setup
static double gStripHeight = 30; // the title/tab row, which is the titlebar

// The look of the chrome, in one place so a theme has somewhere to land later.
enum {
    kTabBarWidth = 190,  // the sidebar, when the tabs are on the left
    kTabHeightLeft = 32, // one row of the sidebar
    kTabMinWidth = 110,  // a top tab never gets narrower than this
    kTabMaxWidth = 240,
    kTabGap = 4,      // between pills
    kTabInset = 5,    // between a pill and the edge of the strip
    kTabRadius = 7,   // corner radius of a pill
    kNewTabSize = 30, // the "+" button
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
static NSColor *chromeText(void) { return shade(gChromeRGB, 0.92, 1.0); }
static NSColor *chromeDim(void) { return shade(gChromeRGB, 0.55, 1.0); }
static NSColor *chromeAccent(void) { return rgb(0x4c, 0x8e, 0xda); }
static NSColor *chromeEdge(void) { return shade(gChromeRGB, 0.18, 1.0); }

// The line between two panes, and the wash over the ones without the focus.
static NSColor *dividerColor(void) { return rgba(0x7a, 0x82, 0x94, 0.55); }
static NSColor *dimColor(void) { return rgba(0x00, 0x00, 0x00, 0.28); }

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

@interface ShellterView : NSView
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
    shellterGoRender();
}

// ---------------------------------------------------------------------------
// Input
// ---------------------------------------------------------------------------

- (void)keyDown:(NSEvent *)event {
    // Two strings, because they answer two different questions:
    //   -characters                 what the key types  ('A' for Shift+a)
    //   -charactersIgnoringModifiers which key it is    ('a', or 0xF702 for Left)
    // Everything else - which bytes to send - is decided in Go.
    NSString *chars = [event characters];
    NSString *base = [event charactersIgnoringModifiers];
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

// cellForEvent converts a mouse location into grid coordinates. This is the
// only place pixels become cells for input, as setFrameSize: is for the window.
- (void)cellForEvent:(NSEvent *)event row:(int *)row col:(int *)col {
    NSPoint p = [self convertPoint:[event locationInWindow] fromView:nil];
    int r = (int)floor((p.y - gPad) / gLineHeight);
    int c = (int)floor((p.x - gPad) / gCharWidth);
    // A drag can leave the window; programs expect the edge, not a negative.
    if (r < 0) {
        r = 0;
    }
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
        shellterGoScroll(lines, row, col);
    }
}

// The mouse. AppKit delivers one selector per button and per phase; they all
// funnel into the same report.
- (void)mouseDown:(NSEvent *)event {
    [self reportMouse:event button:kMouseLeft action:kMousePress];
}
- (void)mouseUp:(NSEvent *)event {
    [self reportMouse:event button:kMouseLeft action:kMouseRelease];
}
- (void)mouseDragged:(NSEvent *)event {
    [self reportMouse:event button:kMouseLeft action:kMouseMove];
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

// Motion with no button held only arrives while a tracking area is installed,
// which happens when a program asks for ?1003.
- (void)mouseMoved:(NSEvent *)event {
    [self reportMouse:event button:kMouseNone action:kMouseMove];
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

static BOOL tabIsBusy(int i) {
    return i >= 0 && i < (int)[gTabBusy length] && [gTabBusy characterAtIndex:i] == '1';
}

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

    for (int i = 0; i < tabCount(); i++) {
        NSRect r = tabRect(i, bounds.size);
        BOOL active = (i == gActiveTab);

        if (active) {
            fillPill(r, chromeActiveBG());
        } else if (i == gHoverTab) {
            fillPill(r, chromeHoverBG());
        }

        // A dot for a tab that is still running something.
        double textLeft = r.origin.x + 11;
        if (tabIsBusy(i)) {
            NSRect dot = NSMakeRect(textLeft, NSMidY(r) - 3, 6, 6);
            [chromeAccent() setFill];
            [[NSBezierPath bezierPathWithOvalInRect:dot] fill];
            textLeft += 12;
        }

        // The close cross appears only where it can be used, and the label
        // gives up its space only then, so titles do not jump on hover.
        BOOL closable = active || i == gHoverTab;
        NSRect label = NSMakeRect(textLeft, r.origin.y,
                                  NSMaxX(r) - textLeft - (closable ? 26 : 11), r.size.height);
        drawLabel(gTabTitles[i], label, active ? chromeText() : chromeDim(), 12, NO);
        if (closable) {
            drawCross(closeRect(r), active ? chromeText() : chromeDim());
        }
    }

    NSRect plus = newTabRect(bounds.size);
    if (gHoverNew) {
        fillPill(plus, chromeHoverBG());
    }
    drawPlus(plus, chromeDim());
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

- (void)mouseDown:(NSEvent *)event {
    NSPoint p = [self convertPoint:[event locationInWindow] fromView:nil];
    if (NSPointInRect(p, newTabRect([self bounds].size))) {
        shellterGoNewTab();
        return;
    }
    BOOL closed = NO;
    int i = [self hitTab:p closed:&closed];
    if (i < 0) {
        return;
    }
    if (closed) {
        shellterGoTabClose(i);
    } else {
        shellterGoTabSelect(i);
    }
}

- (void)mouseMoved:(NSEvent *)event {
    NSPoint p = [self convertPoint:[event locationInWindow] fromView:nil];
    BOOL closed = NO;
    int wasTab = gHoverTab;
    BOOL wasNew = gHoverNew;
    gHoverTab = [self hitTab:p closed:&closed];
    gHoverNew = NSPointInRect(p, newTabRect([self bounds].size));
    if (wasTab != gHoverTab || wasNew != gHoverNew) {
        [self setNeedsDisplay:YES];
    }
}

- (void)mouseExited:(NSEvent *)event {
    if (gHoverTab != -1 || gHoverNew) {
        gHoverTab = -1;
        gHoverNew = NO;
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
// Layout
// ---------------------------------------------------------------------------

@interface ShellterContent : NSView
@end

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

    // No line under the strip: the chrome and the terminal are the same colour
    // and meant to read as one surface. The sidebar does get one, because a
    // column of tabs beside a column of text needs somewhere to end.
    if (tabBarVisible() && gTabPosition == 1) {
        NSRect grid = [gView frame];
        [chromeEdge() setFill];
        NSRectFillUsingOperation(NSMakeRect(grid.origin.x - 1, grid.origin.y, 1, grid.size.height),
                                 NSCompositingOperationSourceOver);
    }
}

// Empty chrome drags the window, the way a titlebar does.
- (BOOL)mouseDownCanMoveWindow {
    return YES;
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
        [gTabBar setFrame:NSMakeRect(0, top, kTabBarWidth, b.size.height - top)];
        [gView setFrame:NSMakeRect(kTabBarWidth, top,
                                   b.size.width - kTabBarWidth, b.size.height - top)];
    } else {
        [gTabBar setHidden:NO];
        // The bar starts where the traffic lights end, so it never has to
        // leave a gap it cannot measure.
        [gTabBar setFrame:NSMakeRect(gLeadInset, 0, b.size.width - gLeadInset, top)];
        [gView setFrame:NSMakeRect(0, top, b.size.width, b.size.height - top)];
    }
    [self setNeedsDisplay:YES];
}

- (void)setFrameSize:(NSSize)newSize {
    [super setFrameSize:newSize];
    [self layoutViews];
}

@end

// ---------------------------------------------------------------------------
// Delegate
// ---------------------------------------------------------------------------

@interface ShellterDelegate : NSObject <NSApplicationDelegate, NSWindowDelegate>
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
    shellterGoCopy();
}

- (void)pasteText:(id)sender {
    shellterGoPaste();
}

- (void)selectAllText:(id)sender {
    shellterGoSelectAll();
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

- (void)tabsOnTop:(id)sender {
    shellterGoTabPosition(0);
}

- (void)tabsOnLeft:(id)sender {
    shellterGoTabPosition(1);
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
    NSMenu *appMenu = [[NSMenu alloc] initWithTitle:@"Shellter"];
    [appMenu addItemWithTitle:@"Quit Shellter" action:@selector(terminate:) keyEquivalent:@"q"];
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
    [editItem setSubmenu:editMenu];

    NSMenuItem *viewItem = [[NSMenuItem alloc] init];
    [bar addItem:viewItem];
    NSMenu *viewMenu = [[NSMenu alloc] initWithTitle:@"View"];
    NSMenuItem *where = [viewMenu addItemWithTitle:@"Tab Bar Position" action:nil keyEquivalent:@""];
    NSMenu *whereMenu = [[NSMenu alloc] initWithTitle:@"Tab Bar Position"];
    gTopItem = item(whereMenu, @"Top", @selector(tabsOnTop:), @"", 0, target);
    gLeftItem = item(whereMenu, @"Left", @selector(tabsOnLeft:), @"", 0, target);
    [where setSubmenu:whereMenu];
    [viewItem setSubmenu:viewMenu];

    [NSApp setMainMenu:bar];
    syncTabPositionMenu();
}

void shellterRun(const char *title, int rows, int cols, double fontSize, double padding,
                 double opacity, int blur, unsigned int background) {
    @autoreleasepool {
        gPad = padding;
        gOpacity = opacity;
        gChromeRGB = background;
        [NSApplication sharedApplication];
        // Without a .app bundle the process is an "accessory" by default: no
        // Dock icon, no keyboard focus. Terminals need both.
        [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];

        gFont = [NSFont fontWithName:@"Menlo" size:fontSize];
        if (gFont == nil) {
            gFont = [NSFont userFixedPitchFontOfSize:fontSize];
        }
        NSFontManager *fm = [NSFontManager sharedFontManager];
        gFontBold = [fm convertFont:gFont toHaveTrait:NSBoldFontMask];
        gFontItalic = [fm convertFont:gFont toHaveTrait:NSItalicFontMask];
        gFontBoldItalic = [fm convertFont:gFontBold toHaveTrait:NSItalicFontMask];
        gCharWidth = [@"M" sizeWithAttributes:@{NSFontAttributeName : gFont}].width;
        NSLayoutManager *layout = [[NSLayoutManager alloc] init];
        gLineHeight = [layout defaultLineHeightForFont:gFont];

        // The window is sized for the grid plus its padding, and for the bar
        // if one is showing.
        // Room for the grid and its padding, plus the chrome: the strip is
        // always there, and the sidebar only when the tabs are on the left.
        NSRect frame = NSMakeRect(0, 0, cols * gCharWidth + 2 * gPad,
                                  rows * gLineHeight + 2 * gPad + gStripHeight);
        if (tabBarVisible() && gTabPosition == 1) {
            frame.size.width += kTabBarWidth;
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
        // The traffic lights, the menu bar's idea of us and the blur behind a
        // translucent window are AppKit's to draw, not ours, so the theme has
        // to tell it which world we are in.
        [window setAppearance:[NSAppearance appearanceNamed:
            themeIsLight(gChromeRGB) ? NSAppearanceNameAqua : NSAppearanceNameDarkAqua]];

        // Translucency has to be arranged in three places at once: the window
        // must not claim to be opaque, its own backdrop must be clear, and the
        // colours painted on top carry the alpha. Miss one and the desktop
        // never shows through.
        if (gOpacity < 1.0) {
            [window setOpaque:NO];
            [window setBackgroundColor:[NSColor clearColor]];
        } else {
            [window setBackgroundColor:chromeBG()];
        }

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
        if (blur && gOpacity < 1.0) {
            NSVisualEffectView *glass = [[NSVisualEffectView alloc] initWithFrame:frame];
            [glass setMaterial:NSVisualEffectMaterialUnderWindowBackground];
            // Behind-window is what frosts the desktop rather than our own
            // pixels; "active" keeps it frosted when the window is not focused.
            [glass setBlendingMode:NSVisualEffectBlendingModeBehindWindow];
            [glass setState:NSVisualEffectStateActive];
            [glass setAutoresizingMask:(NSViewWidthSizable | NSViewHeightSizable)];
            [content addSubview:glass];
        }
        ShellterTabBar *tabBar = [[ShellterTabBar alloc] initWithFrame:NSZeroRect];
        gTabBar = tabBar;
        ShellterView *view = [[ShellterView alloc] initWithFrame:frame];
        gView = view;
        [content addSubview:tabBar];
        [content addSubview:view];
        [window setContentView:content];
        [content layoutViews];
        [window makeFirstResponder:view];

        ShellterDelegate *delegate = [[ShellterDelegate alloc] init];
        [window setDelegate:delegate];
        [NSApp setDelegate:delegate];

        installMenu(delegate);
        [window center];
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

void shellterSetTabs(const char *titles, int len, const char *busy, int count,
                     int active, int position) {
    NSString *joined = (len > 0 && titles != NULL)
                           ? [[[NSString alloc] initWithBytes:titles
                                                       length:(NSUInteger)len
                                                     encoding:NSUTF8StringEncoding] autorelease]
                           : @"";
    NSString *busyStr = (busy != NULL) ? [NSString stringWithUTF8String:busy] : @"";
    NSArray *parts = (count > 0) ? [joined componentsSeparatedByString:@"\n"] : @[];

    // Copied out of the arguments before hopping threads: the Go side frees
    // them as soon as this returns.
    [parts retain];
    [busyStr retain];

    void (^apply)(void) = ^{
      BOOL wasVisible = tabBarVisible();
      int wasPosition = gTabPosition;

      [gTabTitles release];
      gTabTitles = parts;
      [gTabBusy release];
      gTabBusy = busyStr;
      gActiveTab = active;
      gTabPosition = position;
      if (gHoverTab >= tabCount()) {
          gHoverTab = -1;
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

// cellOrigin is the top-left pixel of a cell, padding included. Everything that
// draws goes through it, so the inset is defined once.
static NSPoint cellOrigin(int row, int col) {
    return NSMakePoint(gPad + col * gCharWidth, gPad + row * gLineHeight);
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
    // A hairline down the middle of the gap rather than a filled cell: the
    // whole cell would be a thick bar, and the point of the line is to separate
    // the two panes without taking the eye off either.
    NSRect line = gap;
    if (cols <= rows) { // a tall, narrow gap: the panes are side by side
        line.origin.x = NSMidX(gap) - 0.5;
        line.size.width = 1;
    } else {
        line.origin.y = NSMidY(gap) - 0.5;
        line.size.height = 1;
    }
    [dividerColor() setFill];
    NSRectFillUsingOperation(line, NSCompositingOperationSourceOver);
}

void shellterDimCells(int row, int col, int rows, int cols) {
    [dimColor() setFill];
    NSRectFillUsingOperation(cellRect(row, col, rows, cols), NSCompositingOperationSourceOver);
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
