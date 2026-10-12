// The Metal renderer: what window_darwin.m's drawing primitives turn into.
//
// Internal to the render package - Go never calls these. A frame is a list of
// instances, each either a rectangle or a glyph out of a texture atlas, and the
// GPU draws the lot in six instanced draw calls: three layers, rectangles then
// glyphs in each. Nothing is drawn on the CPU but a glyph the first time it is
// seen.
//
// Everything here runs on the main thread.

#ifndef SHELLTER_METAL_DARWIN_H
#define SHELLTER_METAL_DARWIN_H

#import <Cocoa/Cocoa.h>
#import <QuartzCore/QuartzCore.h>

// The layers a frame is drawn in, bottom to top.
enum {
    kLayerBack = 0, // cell backgrounds
    kLayerText = 1, // text, box drawing, underlines
    kLayerTop = 2,  // cursor, dividers, scroll indicator, visual bell, composition
};

// mtrInit creates the device, the pipelines and the shaders. It reports
// whether there is a GPU to draw with at all.
BOOL mtrInit(void);

// mtrMakeLayer is the layer the terminal view draws into.
CAMetalLayer *mtrMakeLayer(void);

// mtrSetFonts says which faces text is drawn in, and the cell they fill, in
// points: everything the glyph cache depends on, so it starts again. fonts is
// regular, bold, italic, bold italic.
void mtrSetFonts(NSFont *fonts[4], double cellWidth, double cellHeight, double textInset);

// mtrBeginFrame starts a frame for a view of the given size in points at the
// given scale, and mtrEndFrame draws it into the layer. A frame that could not
// get a drawable is dropped, as a frame should be.
void mtrBeginFrame(double width, double height, double scale);
void mtrEndFrame(CAMetalLayer *layer, BOOL synchronous);

// mtrRenderToImage draws the frame just built into an image instead of the
// layer, for tests and for checking the renderer by eye.
NSBitmapImageRep *mtrRenderToImage(void);

// mtrClear is the colour behind everything; opacity below 1 lets the desktop
// show through it.
void mtrClear(unsigned int rgb, double opacity);

// mtrClip limits what follows to a rectangle in points, until mtrNoClip.
void mtrClip(NSRect r);
void mtrNoClip(void);

// mtrRect fills a rectangle in points, its corners rounded by radius.
void mtrRect(int layer, NSRect r, unsigned int rgb, double alpha, double radius);

// mtrText draws one cell's text - a character and whatever combines with it -
// with its top-left at origin (points), cells wide, in a face (0 regular, 1
// bold, 2 italic, 3 both) and a colour. A colour emoji keeps its own colours.
void mtrText(int layer, NSPoint origin, int cells, const char *utf8, int len, int face,
             unsigned int rgb);

// mtrShape draws a shape the cache has never seen with draw - given the cell to
// draw in, inside a bitmap where white is ink and black is none - and the
// cached one from then on. key identifies the shape; size is the cell in
// points. Box drawing and the patterned underlines are drawn this way.
void mtrShape(int layer, NSPoint origin, NSSize size, const unsigned char *key, int keyLen,
              unsigned int rgb, void (^draw)(NSRect cell));

// mtrString draws a whole string at origin, laid out as text rather than as
// cells: an input method's composition, which belongs to no cell yet. It
// returns the width it took, in points.
double mtrString(int layer, NSPoint origin, NSString *text, NSFont *font, unsigned int rgb);

#endif
