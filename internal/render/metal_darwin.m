// The Metal renderer. metal_darwin.h says what it is for; this says how.
//
// A frame is built as plain arrays of instances while Go walks the grid -
// appending to an array is all a primitive costs - and handed to the GPU in
// one go at the end: six instanced draw calls, whatever is on screen.
//
// Text is drawn from a texture atlas. The first time a character is seen in a
// face it is laid out and rasterised by Core Text, once, into a free slot of
// the atlas; every frame after that, drawing it is one instance pointing at
// the slot. Cells are a whole number of pixels wide (window_darwin.m sees to
// that), so a character looks the same in every cell and one slot serves them
// all - and box drawing, rasterised the same way, meets the next cell exactly.
//
// Compiled without ARC, like the rest of the package.

#import "metal_darwin.h"
#import <CoreText/CoreText.h>
#import <Metal/Metal.h>
#include <stdlib.h>
#include <string.h>

void shellterRedraw(void); // window_darwin.m

// ---------------------------------------------------------------------------
// Shaders
// ---------------------------------------------------------------------------
//
// Compiled from source when Shellter starts: the Command Line Tools have no
// Metal compiler, and the source is small enough that compiling it costs a few
// milliseconds. Positions arrive in pixels, top-left origin, like the view.

static NSString *const kShaders = @
    "#include <metal_stdlib>\n"
    "using namespace metal;\n"
    "struct RectInst { packed_float4 box; packed_float4 clip; uint color; float radius; };\n"
    "struct GlyphInst { packed_float4 box; packed_float4 uv; packed_float4 clip; uint color; uint flags; };\n"
    "struct RectOut { float4 pos [[position]]; float4 color; float4 box; float4 clip; float radius; };\n"
    "struct GlyphOut { float4 pos [[position]]; float4 color; float2 uv; float4 clip; uint flags [[flat]]; };\n"
    "static float4 ndc(float2 p, float2 vp) {\n"
    "    return float4(p.x / vp.x * 2.0 - 1.0, 1.0 - p.y / vp.y * 2.0, 0.0, 1.0);\n"
    "}\n"
    "static bool clipped(float2 p, float4 c) {\n"
    "    return p.x < c.x || p.y < c.y || p.x >= c.z || p.y >= c.w;\n"
    "}\n"
    "vertex RectOut rectVertex(uint vid [[vertex_id]], uint iid [[instance_id]],\n"
    "                          const device RectInst *in [[buffer(0)]],\n"
    "                          constant float2 &vp [[buffer(1)]]) {\n"
    "    RectInst r = in[iid];\n"
    "    float4 box = float4(r.box);\n"
    "    float2 corner = float2(float(vid & 1), float(vid >> 1));\n"
    "    RectOut o;\n"
    "    o.pos = ndc(box.xy + corner * box.zw, vp);\n"
    "    o.color = unpack_unorm4x8_to_float(r.color);\n"
    "    o.box = box;\n"
    "    o.clip = float4(r.clip);\n"
    "    o.radius = r.radius;\n"
    "    return o;\n"
    "}\n"
    "fragment float4 rectFragment(RectOut in [[stage_in]]) {\n"
    "    float2 p = in.pos.xy;\n"
    "    if (clipped(p, in.clip)) discard_fragment();\n"
    "    float a = in.color.a;\n"
    "    if (in.radius > 0.0) {\n"
    "        float2 half_ = in.box.zw * 0.5;\n"
    "        float2 q = abs(p - (in.box.xy + half_)) - half_ + in.radius;\n"
    "        float d = length(max(q, 0.0)) + min(max(q.x, q.y), 0.0) - in.radius;\n"
    "        a *= clamp(0.5 - d, 0.0, 1.0);\n"
    "    }\n"
    "    return float4(in.color.rgb * a, a);\n"
    "}\n"
    "vertex GlyphOut glyphVertex(uint vid [[vertex_id]], uint iid [[instance_id]],\n"
    "                            const device GlyphInst *in [[buffer(0)]],\n"
    "                            constant float2 &vp [[buffer(1)]]) {\n"
    "    GlyphInst g = in[iid];\n"
    "    float4 box = float4(g.box);\n"
    "    float4 uv = float4(g.uv);\n"
    "    float2 corner = float2(float(vid & 1), float(vid >> 1));\n"
    "    GlyphOut o;\n"
    "    o.pos = ndc(box.xy + corner * box.zw, vp);\n"
    "    o.uv = uv.xy + corner * uv.zw;\n"
    "    o.color = unpack_unorm4x8_to_float(g.color);\n"
    "    o.clip = float4(g.clip);\n"
    "    o.flags = g.flags;\n"
    "    return o;\n"
    "}\n"
    "fragment float4 glyphFragment(GlyphOut in [[stage_in]],\n"
    "                              texture2d<float> mask [[texture(0)]],\n"
    "                              texture2d<float> colour [[texture(1)]]) {\n"
    "    if (clipped(in.pos.xy, in.clip)) discard_fragment();\n"
    "    constexpr sampler s(coord::pixel, filter::nearest, address::clamp_to_edge);\n"
    "    if (in.flags & 1u) {\n"
    "        return colour.sample(s, in.uv) * in.color.a;\n"
    "    }\n"
    "    float a = mask.sample(s, in.uv).r * in.color.a;\n"
    "    return float4(in.color.rgb * a, a);\n"
    "}\n";

// The instances, laid out exactly as the shaders read them.
typedef struct {
    float box[4];  // x, y, width, height in pixels
    float clip[4]; // x0, y0, x1, y1 in pixels
    uint32_t color; // RGBA, red in the low byte
    float radius;
} RectInst;

typedef struct {
    float box[4];
    float uv[4]; // x, y, width, height in the atlas, in pixels
    float clip[4];
    uint32_t color;
    uint32_t flags; // 1: a colour glyph, drawn in its own colours
} GlyphInst;

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

static id<MTLDevice> gDevice = nil;
static id<MTLCommandQueue> gQueue = nil;
static id<MTLRenderPipelineState> gRectPipe = nil, gGlyphPipe = nil;

// Frames in flight: the CPU builds one while the GPU draws up to two others.
enum { kInFlight = 3 };
static dispatch_semaphore_t gInFlight;
static id<MTLBuffer> gBuffers[kInFlight];
static int gBufferIndex = 0;

// A growable array of instances, one per layer and kind.
typedef struct {
    void *items;
    int len, cap, size;
} Array;

static Array gRects[3], gGlyphs[3];

static void *push(Array *a) {
    if (a->len == a->cap) {
        a->cap = a->cap ? a->cap * 2 : 1024;
        a->items = realloc(a->items, (size_t)a->cap * a->size);
    }
    return (char *)a->items + (size_t)(a->len++) * a->size;
}

// The frame being built.
static double gScale = 0;            // pixels per point
static double gViewW = 0, gViewH = 0; // the view, in points
static float gClip[4];                // in pixels
static double gClearRGB[3] = {0, 0, 0};
static double gClearAlpha = 1;

// The fonts and cell the glyph cache was made for.
static NSFont *gFaces[4] = {nil, nil, nil, nil};
static double gCellW = 0, gCellH = 0, gInset = 0;

// ---------------------------------------------------------------------------
// The atlas
// ---------------------------------------------------------------------------
//
// Two pages: an 8-bit one of coverage for everything drawn in one colour, and
// a colour one, made only when the first emoji turns up. Slots are packed in
// shelves, rows as tall as the tallest thing on them. When a page is full the
// whole cache starts again at the next frame - which at terminal sizes takes
// thousands of different characters, so in practice never.

typedef struct {
    uint16_t x, y, w, h; // the slot, in the page's pixels
    int16_t ox, oy;      // where the slot's top-left sits from the cell's, in pixels
    uint8_t page;        // 0 coverage, 1 colour
    uint8_t valid;
} Glyph;

typedef struct {
    id<MTLTexture> tex;
    int size, bpp;
    int shelfX, shelfY, shelfH;
} Page;

enum { kPageSize = 2048 };
static Page gPages[2];
static id<MTLTexture> gNoColour = nil; // bound while there is no colour page
static BOOL gAtlasFull = NO;

// What a glyph is looked up by: the bytes of its key - a kind, its face and
// width, then its text or its shape's numbers.
typedef struct {
    uint64_t hash;
    uint32_t keyOff;
    uint32_t keyLen;
    Glyph glyph;
} Entry;

static Entry *gTable = NULL;
static uint32_t gTableCap = 0, gTableLen = 0;
static unsigned char *gArena = NULL;
static uint32_t gArenaLen = 0, gArenaCap = 0;

// The common case without hashing anything: one ASCII character, one cell.
static Glyph gASCII[4][128];

static uint64_t hashBytes(const unsigned char *p, int n) {
    uint64_t h = 1469598103934665603ULL;
    for (int i = 0; i < n; i++) {
        h ^= p[i];
        h *= 1099511628211ULL;
    }
    return h;
}

static Entry *lookup(const unsigned char *key, int n, uint64_t h) {
    if (gTableCap == 0) {
        return NULL;
    }
    for (uint32_t i = (uint32_t)h & (gTableCap - 1);; i = (i + 1) & (gTableCap - 1)) {
        Entry *e = &gTable[i];
        if (e->keyLen == 0) {
            return NULL;
        }
        if (e->hash == h && e->keyLen == (uint32_t)n && memcmp(gArena + e->keyOff, key, n) == 0) {
            return e;
        }
    }
}

static void insert(const unsigned char *key, int n, uint64_t h, Glyph g) {
    if ((gTableLen + 1) * 2 > gTableCap) {
        uint32_t oldCap = gTableCap;
        Entry *old = gTable;
        gTableCap = oldCap ? oldCap * 2 : 1024;
        gTable = calloc(gTableCap, sizeof(Entry));
        for (uint32_t i = 0; i < oldCap; i++) {
            if (old[i].keyLen == 0) {
                continue;
            }
            uint32_t j = (uint32_t)old[i].hash & (gTableCap - 1);
            while (gTable[j].keyLen != 0) {
                j = (j + 1) & (gTableCap - 1);
            }
            gTable[j] = old[i];
        }
        free(old);
    }
    if (gArenaLen + (uint32_t)n > gArenaCap) {
        gArenaCap = MAX(gArenaCap * 2, gArenaLen + (uint32_t)n + 4096);
        gArena = realloc(gArena, gArenaCap);
    }
    memcpy(gArena + gArenaLen, key, n);
    uint32_t i = (uint32_t)h & (gTableCap - 1);
    while (gTable[i].keyLen != 0) {
        i = (i + 1) & (gTableCap - 1);
    }
    gTable[i] = (Entry){.hash = h, .keyOff = gArenaLen, .keyLen = (uint32_t)n, .glyph = g};
    gArenaLen += (uint32_t)n;
    gTableLen++;
}

static void makePage(int index) {
    Page *p = &gPages[index];
    p->size = kPageSize;
    p->bpp = index == 0 ? 1 : 4;
    MTLTextureDescriptor *d = [MTLTextureDescriptor
        texture2DDescriptorWithPixelFormat:index == 0 ? MTLPixelFormatR8Unorm : MTLPixelFormatBGRA8Unorm
                                     width:p->size
                                    height:p->size
                                 mipmapped:NO];
    [d setStorageMode:MTLStorageModeManaged];
    [d setUsage:MTLTextureUsageShaderRead];
    [p->tex release];
    p->tex = [gDevice newTextureWithDescriptor:d];
    p->shelfX = p->shelfY = p->shelfH = 0;
}

// resetCache forgets every glyph. The GPU may still be drawing frames that use
// the old slots, so it waits for them first.
static void resetCache(void) {
    for (int i = 0; i < kInFlight; i++) {
        dispatch_semaphore_wait(gInFlight, DISPATCH_TIME_FOREVER);
    }
    for (int i = 0; i < kInFlight; i++) {
        dispatch_semaphore_signal(gInFlight);
    }
    memset(gASCII, 0, sizeof gASCII);
    if (gTable != NULL) {
        memset(gTable, 0, sizeof(Entry) * gTableCap);
    }
    gTableLen = 0;
    gArenaLen = 0;
    for (int i = 0; i < 2; i++) {
        gPages[i].shelfX = gPages[i].shelfY = gPages[i].shelfH = 0;
    }
    gAtlasFull = NO;
}

// place finds room for a w x h slot on a page, or reports that there is none.
static BOOL place(Page *p, int w, int h, int *x, int *y) {
    if (w > p->size || h > p->size) {
        return NO;
    }
    if (p->shelfX + w > p->size) {
        p->shelfY += p->shelfH;
        p->shelfX = 0;
        p->shelfH = 0;
    }
    if (p->shelfY + h > p->size) {
        gAtlasFull = YES;
        return NO;
    }
    *x = p->shelfX;
    *y = p->shelfY;
    p->shelfX += w + 1; // a pixel of gutter: nothing samples across it
    p->shelfH = MAX(p->shelfH, h + 1);
    return YES;
}

// upload copies a rasterised bitmap into a new slot. pixels are the bitmap
// context's: four bytes a pixel, rows top first. For the coverage page the
// green channel is kept - the bitmap is white ink on black.
static Glyph upload(int page, const unsigned char *pixels, int w, int h, int stride, int ox, int oy) {
    Glyph g = {0};
    if (page == 1 && gPages[1].tex == nil) {
        makePage(1);
    }
    Page *p = &gPages[page];
    int x, y;
    if (!place(p, w, h, &x, &y)) {
        return g;
    }
    MTLRegion region = MTLRegionMake2D(x, y, w, h);
    if (page == 0) {
        static unsigned char *scratch = NULL;
        static size_t scratchCap = 0;
        size_t need = (size_t)w * h;
        if (need > scratchCap) {
            scratchCap = need;
            scratch = realloc(scratch, scratchCap);
        }
        for (int r = 0; r < h; r++) {
            const unsigned char *src = pixels + (size_t)r * stride;
            unsigned char *dst = scratch + (size_t)r * w;
            for (int c = 0; c < w; c++) {
                dst[c] = src[c * 4 + 1];
            }
        }
        [p->tex replaceRegion:region mipmapLevel:0 withBytes:scratch bytesPerRow:w];
    } else {
        [p->tex replaceRegion:region mipmapLevel:0 withBytes:pixels bytesPerRow:stride];
    }
    g = (Glyph){.x = x, .y = y, .w = w, .h = h, .ox = ox, .oy = oy, .page = page, .valid = 1};
    return g;
}

// A bitmap to rasterise into, reused.
static unsigned char *gBits = NULL;
static size_t gBitsCap = 0;

static CGContextRef bitmap(int w, int h, BOOL colour) {
    size_t need = (size_t)w * h * 4;
    if (need > gBitsCap) {
        gBitsCap = need;
        gBits = realloc(gBits, gBitsCap);
    }
    memset(gBits, 0, need);
    CGColorSpaceRef space = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
    // Coverage is drawn white on opaque black, so that the font is smoothed
    // as it is on screen; colour glyphs on clear, premultiplied, BGRA.
    uint32_t info = colour ? (kCGImageAlphaPremultipliedFirst | kCGBitmapByteOrder32Little)
                           : (uint32_t)kCGImageAlphaNoneSkipLast;
    CGContextRef ctx = CGBitmapContextCreate(gBits, w, h, 8, (size_t)w * 4, space, info);
    CGColorSpaceRelease(space);
    if (!colour) {
        CGContextSetRGBFillColor(ctx, 0, 0, 0, 1);
        CGContextFillRect(ctx, CGRectMake(0, 0, w, h));
    }
    return ctx;
}

// isColour reports whether a line is drawn in a colour font - an emoji.
static BOOL isColour(CTLineRef line) {
    CFArrayRef runs = CTLineGetGlyphRuns(line);
    for (CFIndex i = 0; i < CFArrayGetCount(runs); i++) {
        CTRunRef run = CFArrayGetValueAtIndex(runs, i);
        CTFontRef font = CFDictionaryGetValue(CTRunGetAttributes(run), kCTFontAttributeName);
        if (font != NULL && (CTFontGetSymbolicTraits(font) & kCTFontTraitColorGlyphs)) {
            return YES;
        }
    }
    return NO;
}

// rasterText lays text out in a face and draws it into a new slot: cells wide,
// with room around it for what overhangs the cell - italics lean out of it,
// and some glyphs reach above or below the row.
static Glyph rasterText(NSString *text, NSFont *font, int cells, BOOL cellAligned) {
    Glyph none = {0};
    if (text == nil || font == nil) {
        return none;
    }
    CGColorRef white = CGColorCreateSRGB(1, 1, 1, 1);
    CFStringRef keys[] = {kCTFontAttributeName, kCTForegroundColorAttributeName};
    CFTypeRef values[] = {(CTFontRef)font, white};
    CFDictionaryRef attrs = CFDictionaryCreate(NULL, (const void **)keys, (const void **)values, 2,
                                               &kCFTypeDictionaryKeyCallBacks,
                                               &kCFTypeDictionaryValueCallBacks);
    CFAttributedStringRef str = CFAttributedStringCreate(NULL, (CFStringRef)text, attrs);
    CTLineRef line = CTLineCreateWithAttributedString(str);
    CFRelease(str);
    CFRelease(attrs);
    CGColorRelease(white);

    BOOL colour = isColour(line);
    double width = cellAligned ? cells * gCellW : CTLineGetTypographicBounds(line, NULL, NULL, NULL);
    int padX = (int)ceil(gCellW * gScale / 2), padY = (int)ceil(gCellH * gScale / 4);
    int w = (int)ceil(width * gScale) + 2 * padX;
    int h = (int)ceil(gCellH * gScale) + 2 * padY;
    CGContextRef ctx = bitmap(w, h, colour);
    CGContextSetAllowsFontSmoothing(ctx, !colour);
    CGContextSetShouldSmoothFonts(ctx, !colour);
    CGContextSetShouldAntialias(ctx, true);
    CGContextSetShouldSubpixelPositionFonts(ctx, false);
    CGContextSetShouldSubpixelQuantizeFonts(ctx, false);
    CGContextScaleCTM(ctx, gScale, gScale);
    // The baseline is where the view puts it: the row's top, lowered by the
    // inset, then the regular face's ascent - for every face and every
    // fallback, so a row of mixed scripts shares one baseline.
    double baseline = h / gScale - (padY / gScale + gInset + [gFaces[0] ascender]);
    CGContextSetTextPosition(ctx, padX / gScale, baseline);
    CTLineDraw(line, ctx);
    CFRelease(line);
    CGContextFlush(ctx);
    Glyph g = upload(colour ? 1 : 0, gBits, w, h, w * 4, -padX, -padY);
    CGContextRelease(ctx);
    return g;
}

// ---------------------------------------------------------------------------
// Setting up
// ---------------------------------------------------------------------------

static id<MTLRenderPipelineState> pipeline(id<MTLLibrary> lib, NSString *vertex, NSString *fragment) {
    MTLRenderPipelineDescriptor *d = [[[MTLRenderPipelineDescriptor alloc] init] autorelease];
    id<MTLFunction> v = [lib newFunctionWithName:vertex];
    id<MTLFunction> f = [lib newFunctionWithName:fragment];
    [d setVertexFunction:v];
    [d setFragmentFunction:f];
    [v release];
    [f release];
    MTLRenderPipelineColorAttachmentDescriptor *c = [d colorAttachments][0];
    [c setPixelFormat:MTLPixelFormatBGRA8Unorm];
    // Premultiplied source-over, as everything above is.
    [c setBlendingEnabled:YES];
    [c setSourceRGBBlendFactor:MTLBlendFactorOne];
    [c setSourceAlphaBlendFactor:MTLBlendFactorOne];
    [c setDestinationRGBBlendFactor:MTLBlendFactorOneMinusSourceAlpha];
    [c setDestinationAlphaBlendFactor:MTLBlendFactorOneMinusSourceAlpha];
    NSError *err = nil;
    id<MTLRenderPipelineState> p = [gDevice newRenderPipelineStateWithDescriptor:d error:&err];
    if (p == nil) {
        NSLog(@"Shellter: Metal pipeline %@: %@", fragment, err);
    }
    return p;
}

BOOL mtrInit(void) {
    if (gDevice != nil) {
        return YES;
    }
    gDevice = MTLCreateSystemDefaultDevice();
    if (gDevice == nil) {
        return NO;
    }
    gQueue = [gDevice newCommandQueue];
    NSError *err = nil;
    id<MTLLibrary> lib = [gDevice newLibraryWithSource:kShaders options:nil error:&err];
    if (lib == nil) {
        NSLog(@"Shellter: Metal shaders: %@", err);
        return NO;
    }
    gRectPipe = pipeline(lib, @"rectVertex", @"rectFragment");
    gGlyphPipe = pipeline(lib, @"glyphVertex", @"glyphFragment");
    [lib release];
    if (gRectPipe == nil || gGlyphPipe == nil) {
        return NO;
    }
    gInFlight = dispatch_semaphore_create(kInFlight);
    for (int i = 0; i < 3; i++) {
        gRects[i].size = sizeof(RectInst);
        gGlyphs[i].size = sizeof(GlyphInst);
    }
    makePage(0);
    MTLTextureDescriptor *d = [MTLTextureDescriptor texture2DDescriptorWithPixelFormat:MTLPixelFormatBGRA8Unorm
                                                                                 width:1
                                                                                height:1
                                                                             mipmapped:NO];
    gNoColour = [gDevice newTextureWithDescriptor:d];
    return YES;
}

CAMetalLayer *mtrMakeLayer(void) {
    CAMetalLayer *layer = [CAMetalLayer layer];
    [layer setDevice:gDevice];
    [layer setPixelFormat:MTLPixelFormatBGRA8Unorm];
    [layer setFramebufferOnly:YES];
    [layer setOpaque:NO];
    CGColorSpaceRef space = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
    [layer setColorspace:space];
    CGColorSpaceRelease(space);
    // While the window is being resized the old frame stays where it is
    // rather than being stretched to the new size.
    [layer setContentsGravity:kCAGravityTopLeft];
    [layer setNeedsDisplayOnBoundsChange:YES];
    return layer;
}

void mtrSetFonts(NSFont *fonts[4], double cellWidth, double cellHeight, double textInset) {
    for (int i = 0; i < 4; i++) {
        [fonts[i] retain];
        [gFaces[i] release];
        gFaces[i] = fonts[i];
    }
    gCellW = cellWidth;
    gCellH = cellHeight;
    gInset = textInset;
    if (gDevice != nil) {
        resetCache();
    }
}

// ---------------------------------------------------------------------------
// Building a frame
// ---------------------------------------------------------------------------

void mtrBeginFrame(double width, double height, double scale) {
    if (scale != gScale || gAtlasFull) {
        // Glyphs are rasterised for a scale; a window moved to another screen
        // needs them again.
        gScale = scale;
        resetCache();
    }
    gViewW = width;
    gViewH = height;
    for (int i = 0; i < 3; i++) {
        gRects[i].len = 0;
        gGlyphs[i].len = 0;
    }
    mtrNoClip();
}

static uint32_t pack(unsigned int rgb, double alpha) {
    uint32_t a = (uint32_t)lround(MAX(0, MIN(1, alpha)) * 255);
    return ((rgb >> 16) & 0xff) | (((rgb >> 8) & 0xff) << 8) | ((rgb & 0xff) << 16) | (a << 24);
}

void mtrClear(unsigned int rgb, double opacity) {
    gClearRGB[0] = ((rgb >> 16) & 0xff) / 255.0;
    gClearRGB[1] = ((rgb >> 8) & 0xff) / 255.0;
    gClearRGB[2] = (rgb & 0xff) / 255.0;
    gClearAlpha = opacity;
}

void mtrClip(NSRect r) {
    gClip[0] = (float)(NSMinX(r) * gScale);
    gClip[1] = (float)(NSMinY(r) * gScale);
    gClip[2] = (float)(NSMaxX(r) * gScale);
    gClip[3] = (float)(NSMaxY(r) * gScale);
}

void mtrNoClip(void) {
    gClip[0] = gClip[1] = -1e9f;
    gClip[2] = gClip[3] = 1e9f;
}

void mtrRect(int layer, NSRect r, unsigned int rgb, double alpha, double radius) {
    RectInst *in = push(&gRects[layer]);
    in->box[0] = (float)(NSMinX(r) * gScale);
    in->box[1] = (float)(NSMinY(r) * gScale);
    in->box[2] = (float)(NSWidth(r) * gScale);
    in->box[3] = (float)(NSHeight(r) * gScale);
    memcpy(in->clip, gClip, sizeof gClip);
    in->color = pack(rgb, alpha);
    in->radius = (float)(radius * gScale);
}

static void emit(int layer, NSPoint origin, Glyph g, unsigned int rgb) {
    GlyphInst *in = push(&gGlyphs[layer]);
    in->box[0] = (float)(round(origin.x * gScale) + g.ox);
    in->box[1] = (float)(round(origin.y * gScale) + g.oy);
    in->box[2] = g.w;
    in->box[3] = g.h;
    in->uv[0] = g.x;
    in->uv[1] = g.y;
    in->uv[2] = g.w;
    in->uv[3] = g.h;
    memcpy(in->clip, gClip, sizeof gClip);
    in->color = pack(rgb, 1);
    in->flags = g.page == 1 ? 1 : 0;
}

void mtrText(int layer, NSPoint origin, int cells, const char *utf8, int len, int face,
             unsigned int rgb) {
    if (len <= 0 || face < 0 || face > 3) {
        return;
    }
    // One ASCII character in one cell: a table, no hashing.
    if (len == 1 && cells == 1 && (unsigned char)utf8[0] < 128) {
        unsigned char c = (unsigned char)utf8[0];
        if (c == ' ') {
            return; // nothing to draw; the background is a rect of its own
        }
        Glyph *g = &gASCII[face][c];
        if (!g->valid) {
            NSString *s = [[[NSString alloc] initWithBytes:utf8 length:1 encoding:NSUTF8StringEncoding] autorelease];
            *g = rasterText(s, gFaces[face], 1, YES);
            if (!g->valid) {
                return;
            }
        }
        emit(layer, origin, *g, rgb);
        return;
    }

    unsigned char keyBuf[256];
    if (len > (int)sizeof keyBuf - 3) {
        return; // a cell with more combining marks than anyone means
    }
    keyBuf[0] = 'T';
    keyBuf[1] = (unsigned char)face;
    keyBuf[2] = (unsigned char)cells;
    memcpy(keyBuf + 3, utf8, len);
    int n = len + 3;
    uint64_t h = hashBytes(keyBuf, n);
    Entry *e = lookup(keyBuf, n, h);
    Glyph g;
    if (e != NULL) {
        g = e->glyph;
    } else {
        NSString *s = [[[NSString alloc] initWithBytes:utf8 length:len encoding:NSUTF8StringEncoding] autorelease];
        g = rasterText(s, gFaces[face], cells, YES);
        if (!g.valid) {
            return;
        }
        insert(keyBuf, n, h, g);
    }
    emit(layer, origin, g, rgb);
}

void mtrShape(int layer, NSPoint origin, NSSize size, const unsigned char *key, int keyLen,
              unsigned int rgb, void (^draw)(NSRect cell)) {
    unsigned char keyBuf[64];
    if (keyLen > (int)sizeof keyBuf - 1) {
        return;
    }
    keyBuf[0] = 'S';
    memcpy(keyBuf + 1, key, keyLen);
    int n = keyLen + 1;
    uint64_t h = hashBytes(keyBuf, n);
    Entry *e = lookup(keyBuf, n, h);
    Glyph g;
    if (e != NULL) {
        g = e->glyph;
    } else {
        // A point of room on every side, for the shapes that reach a little
        // past their cell so that neighbours meet without a seam.
        int pad = (int)ceil(gScale);
        int w = (int)ceil(size.width * gScale) + 2 * pad;
        int hgt = (int)ceil(size.height * gScale) + 2 * pad;
        CGContextRef ctx = bitmap(w, hgt, NO);
        // Flipped, in points, the way the view draws.
        CGContextTranslateCTM(ctx, 0, hgt);
        CGContextScaleCTM(ctx, gScale, -gScale);
        NSGraphicsContext *gc = [NSGraphicsContext graphicsContextWithCGContext:ctx flipped:YES];
        [NSGraphicsContext saveGraphicsState];
        [NSGraphicsContext setCurrentContext:gc];
        draw(NSMakeRect(pad / gScale, pad / gScale, size.width, size.height));
        [NSGraphicsContext restoreGraphicsState];
        CGContextFlush(ctx);
        g = upload(0, gBits, w, hgt, w * 4, -pad, -pad);
        CGContextRelease(ctx);
        if (!g.valid) {
            return;
        }
        insert(keyBuf, n, h, g);
    }
    emit(layer, origin, g, rgb);
}

double mtrString(int layer, NSPoint origin, NSString *text, NSFont *font, unsigned int rgb) {
    NSData *utf8 = [text dataUsingEncoding:NSUTF8StringEncoding];
    int len = (int)[utf8 length];
    double width = [text sizeWithAttributes:@{NSFontAttributeName : font}].width;
    if (len == 0 || len > 1000) {
        return width;
    }
    unsigned char *keyBuf = malloc(len + 1);
    keyBuf[0] = 'M';
    memcpy(keyBuf + 1, [utf8 bytes], len);
    uint64_t h = hashBytes(keyBuf, len + 1);
    Entry *e = lookup(keyBuf, len + 1, h);
    Glyph g = {0};
    if (e != NULL) {
        g = e->glyph;
    } else {
        g = rasterText(text, font, 0, NO);
        if (g.valid) {
            insert(keyBuf, len + 1, h, g);
        }
    }
    free(keyBuf);
    if (g.valid) {
        emit(layer, origin, g, rgb);
    }
    return width;
}

// ---------------------------------------------------------------------------
// Drawing a frame
// ---------------------------------------------------------------------------

// encode draws the frame into target with the buffer the instances were
// copied into.
static void encode(id<MTLCommandBuffer> cb, id<MTLTexture> target, id<MTLBuffer> buf,
                   NSUInteger *offsets) {
    MTLRenderPassDescriptor *pass = [MTLRenderPassDescriptor renderPassDescriptor];
    MTLRenderPassColorAttachmentDescriptor *c = [pass colorAttachments][0];
    [c setTexture:target];
    [c setLoadAction:MTLLoadActionClear];
    [c setStoreAction:MTLStoreActionStore];
    // Premultiplied, like everything drawn over it.
    [c setClearColor:MTLClearColorMake(gClearRGB[0] * gClearAlpha, gClearRGB[1] * gClearAlpha,
                                       gClearRGB[2] * gClearAlpha, gClearAlpha)];
    id<MTLRenderCommandEncoder> enc = [cb renderCommandEncoderWithDescriptor:pass];
    float vp[2] = {(float)[target width], (float)[target height]};
    id<MTLTexture> colour = gPages[1].tex != nil ? gPages[1].tex : gNoColour;
    for (int layer = 0; layer < 3; layer++) {
        if (gRects[layer].len > 0) {
            [enc setRenderPipelineState:gRectPipe];
            [enc setVertexBuffer:buf offset:offsets[layer * 2] atIndex:0];
            [enc setVertexBytes:vp length:sizeof vp atIndex:1];
            [enc drawPrimitives:MTLPrimitiveTypeTriangleStrip
                    vertexStart:0
                    vertexCount:4
                  instanceCount:gRects[layer].len];
        }
        if (gGlyphs[layer].len > 0) {
            [enc setRenderPipelineState:gGlyphPipe];
            [enc setVertexBuffer:buf offset:offsets[layer * 2 + 1] atIndex:0];
            [enc setVertexBytes:vp length:sizeof vp atIndex:1];
            [enc setFragmentTexture:gPages[0].tex atIndex:0];
            [enc setFragmentTexture:colour atIndex:1];
            [enc drawPrimitives:MTLPrimitiveTypeTriangleStrip
                    vertexStart:0
                    vertexCount:4
                  instanceCount:gGlyphs[layer].len];
        }
    }
    [enc endEncoding];
}

// fill copies the frame's instances into the next free buffer, growing it if
// the frame is bigger than any before, and says where each array starts.
static id<MTLBuffer> fill(NSUInteger *offsets) {
    NSUInteger total = 0;
    for (int layer = 0; layer < 3; layer++) {
        Array *arrays[2] = {&gRects[layer], &gGlyphs[layer]};
        for (int k = 0; k < 2; k++) {
            offsets[layer * 2 + k] = total;
            total += (NSUInteger)arrays[k]->len * arrays[k]->size;
            total = (total + 255) & ~(NSUInteger)255;
        }
    }
    total = MAX(total, 256);
    id<MTLBuffer> buf = gBuffers[gBufferIndex];
    if (buf == nil || [buf length] < total) {
        [buf release];
        buf = [gDevice newBufferWithLength:total + total / 2 options:MTLResourceStorageModeShared];
        gBuffers[gBufferIndex] = buf;
    }
    gBufferIndex = (gBufferIndex + 1) % kInFlight;
    char *base = [buf contents];
    for (int layer = 0; layer < 3; layer++) {
        Array *arrays[2] = {&gRects[layer], &gGlyphs[layer]};
        for (int k = 0; k < 2; k++) {
            memcpy(base + offsets[layer * 2 + k], arrays[k]->items, (size_t)arrays[k]->len * arrays[k]->size);
        }
    }
    return buf;
}

void mtrEndFrame(CAMetalLayer *layer, BOOL synchronous) {
    if (layer == nil || gViewW <= 0 || gViewH <= 0) {
        return;
    }
    CGSize size = CGSizeMake(round(gViewW * gScale), round(gViewH * gScale));
    if (!CGSizeEqualToSize([layer drawableSize], size)) {
        [layer setDrawableSize:size];
    }
    if ([layer contentsScale] != gScale) {
        [layer setContentsScale:gScale];
    }
    dispatch_semaphore_wait(gInFlight, DISPATCH_TIME_FOREVER);
    id<CAMetalDrawable> drawable = [layer nextDrawable];
    if (drawable == nil) {
        dispatch_semaphore_signal(gInFlight);
        return;
    }
    NSUInteger offsets[6];
    id<MTLBuffer> buf = fill(offsets);
    id<MTLCommandBuffer> cb = [gQueue commandBuffer];
    encode(cb, [drawable texture], buf, offsets);
    dispatch_semaphore_t sem = gInFlight;
    [cb addCompletedHandler:^(id<MTLCommandBuffer> done) {
      dispatch_semaphore_signal(sem);
    }];
    if (synchronous) {
        // While the window is being resized the frame has to arrive in the
        // same transaction as the new size, or the edge jitters.
        [layer setPresentsWithTransaction:YES];
        [cb commit];
        [cb waitUntilScheduled];
        [drawable present];
    } else {
        [layer setPresentsWithTransaction:NO];
        [cb presentDrawable:drawable];
        [cb commit];
    }
    if (gAtlasFull) {
        shellterRedraw(); // drawn again from a fresh cache, with what was missed
    }
}

NSBitmapImageRep *mtrRenderToImage(void) {
    int w = (int)round(gViewW * gScale), h = (int)round(gViewH * gScale);
    if (w <= 0 || h <= 0) {
        return nil;
    }
    MTLTextureDescriptor *d = [MTLTextureDescriptor texture2DDescriptorWithPixelFormat:MTLPixelFormatBGRA8Unorm
                                                                                 width:w
                                                                                height:h
                                                                             mipmapped:NO];
    [d setUsage:MTLTextureUsageRenderTarget];
    [d setStorageMode:MTLStorageModeManaged];
    id<MTLTexture> target = [[gDevice newTextureWithDescriptor:d] autorelease];
    dispatch_semaphore_wait(gInFlight, DISPATCH_TIME_FOREVER);
    NSUInteger offsets[6];
    id<MTLBuffer> buf = fill(offsets);
    id<MTLCommandBuffer> cb = [gQueue commandBuffer];
    encode(cb, target, buf, offsets);
    id<MTLBlitCommandEncoder> blit = [cb blitCommandEncoder];
    [blit synchronizeResource:target];
    [blit endEncoding];
    [cb commit];
    [cb waitUntilCompleted];
    dispatch_semaphore_signal(gInFlight);

    NSBitmapImageRep *rep = [[[NSBitmapImageRep alloc] initWithBitmapDataPlanes:NULL
                                                                     pixelsWide:w
                                                                     pixelsHigh:h
                                                                  bitsPerSample:8
                                                                samplesPerPixel:4
                                                                       hasAlpha:YES
                                                                       isPlanar:NO
                                                                 colorSpaceName:NSDeviceRGBColorSpace
                                                                    bytesPerRow:(NSInteger)w * 4
                                                                   bitsPerPixel:32] autorelease];
    unsigned char *px = [rep bitmapData];
    [target getBytes:px bytesPerRow:(NSUInteger)w * 4 fromRegion:MTLRegionMake2D(0, 0, w, h) mipmapLevel:0];
    for (size_t i = 0; i < (size_t)w * h; i++) { // BGRA to RGBA
        unsigned char t = px[i * 4];
        px[i * 4] = px[i * 4 + 2];
        px[i * 4 + 2] = t;
    }
    return rep;
}
