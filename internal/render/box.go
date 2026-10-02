package render

// Box drawing, block elements and Powerline symbols are drawn as shapes, not
// as the font's glyphs.
//
// A font draws them to its own idea of a cell. With any line spacing but the
// font's own, or a font whose box glyphs are a touch short, the lines in a
// table, a tmux border or lazygit's panels stop meeting the next row's, and a
// Powerline prompt's arrows stop meeting their segments. Drawn here, every
// line runs to the cell's edges whatever the cell's size, which is what every
// serious terminal does (Ghostty, kitty, iTerm, WezTerm).

// glyphKind is what sort of shape a character is. The four numbers in a glyph
// mean something different for each.
type glyphKind uint8

const (
	glyphNone glyphKind = iota
	// glyphLines: arms up, right, down, left, each 0 none, 1 light, 2 heavy,
	// 3 double. Lines, corners, tees and crosses.
	glyphLines
	// glyphDashes: vertical (1) or not, weight, number of dashes.
	glyphDashes
	// glyphArc: a rounded corner, 0 ╭ 1 ╮ 2 ╯ 3 ╰.
	glyphArc
	// glyphDiagonal: 1 ╱, 2 ╲, 3 both.
	glyphDiagonal
	// glyphBlock: a filled rectangle, x0 y0 x1 y1 in eighths of the cell,
	// from its top left.
	glyphBlock
	// glyphShade: the whole cell at this percentage of the colour.
	glyphShade
	// glyphQuadrants: a mask of filled quarters, 1 top left, 2 top right,
	// 4 bottom left, 8 bottom right.
	glyphQuadrants
	// glyphPowerline: 0  1  2  3  4  5  6  7
	glyphPowerline
)

// glyph is one character's shape.
type glyph struct {
	kind       glyphKind
	a, b, c, d uint8
}

// boxGlyph is the shape to draw for r, if it is one of those drawn as shapes.
func boxGlyph(r rune) (glyph, bool) {
	if !isBoxRune(r) {
		return glyph{}, false
	}
	g, ok := glyphs[r]
	return g, ok
}

// isBoxRune is the cheap test, two comparisons, that every cell of every
// frame goes through; the table is only consulted for the few that pass.
func isBoxRune(r rune) bool {
	return (r >= 0x2500 && r <= 0x259F) || (r >= 0xE0B0 && r <= 0xE0B7)
}

var glyphs = map[rune]glyph{
	0x2500: {glyphLines, 0, 1, 0, 1},      // ─
	0x2501: {glyphLines, 0, 2, 0, 2},      // ━
	0x2502: {glyphLines, 1, 0, 1, 0},      // │
	0x2503: {glyphLines, 2, 0, 2, 0},      // ┃
	0x2504: {glyphDashes, 0, 1, 3, 0},     // ┄
	0x2505: {glyphDashes, 0, 2, 3, 0},     // ┅
	0x2506: {glyphDashes, 1, 1, 3, 0},     // ┆
	0x2507: {glyphDashes, 1, 2, 3, 0},     // ┇
	0x2508: {glyphDashes, 0, 1, 4, 0},     // ┈
	0x2509: {glyphDashes, 0, 2, 4, 0},     // ┉
	0x250A: {glyphDashes, 1, 1, 4, 0},     // ┊
	0x250B: {glyphDashes, 1, 2, 4, 0},     // ┋
	0x250C: {glyphLines, 0, 1, 1, 0},      // ┌
	0x250D: {glyphLines, 0, 2, 1, 0},      // ┍
	0x250E: {glyphLines, 0, 1, 2, 0},      // ┎
	0x250F: {glyphLines, 0, 2, 2, 0},      // ┏
	0x2510: {glyphLines, 0, 0, 1, 1},      // ┐
	0x2511: {glyphLines, 0, 0, 1, 2},      // ┑
	0x2512: {glyphLines, 0, 0, 2, 1},      // ┒
	0x2513: {glyphLines, 0, 0, 2, 2},      // ┓
	0x2514: {glyphLines, 1, 1, 0, 0},      // └
	0x2515: {glyphLines, 1, 2, 0, 0},      // ┕
	0x2516: {glyphLines, 2, 1, 0, 0},      // ┖
	0x2517: {glyphLines, 2, 2, 0, 0},      // ┗
	0x2518: {glyphLines, 1, 0, 0, 1},      // ┘
	0x2519: {glyphLines, 1, 0, 0, 2},      // ┙
	0x251A: {glyphLines, 2, 0, 0, 1},      // ┚
	0x251B: {glyphLines, 2, 0, 0, 2},      // ┛
	0x251C: {glyphLines, 1, 1, 1, 0},      // ├
	0x251D: {glyphLines, 1, 2, 1, 0},      // ┝
	0x251E: {glyphLines, 2, 1, 1, 0},      // ┞
	0x251F: {glyphLines, 1, 1, 2, 0},      // ┟
	0x2520: {glyphLines, 2, 1, 2, 0},      // ┠
	0x2521: {glyphLines, 2, 2, 1, 0},      // ┡
	0x2522: {glyphLines, 1, 2, 2, 0},      // ┢
	0x2523: {glyphLines, 2, 2, 2, 0},      // ┣
	0x2524: {glyphLines, 1, 0, 1, 1},      // ┤
	0x2525: {glyphLines, 1, 0, 1, 2},      // ┥
	0x2526: {glyphLines, 2, 0, 1, 1},      // ┦
	0x2527: {glyphLines, 1, 0, 2, 1},      // ┧
	0x2528: {glyphLines, 2, 0, 2, 1},      // ┨
	0x2529: {glyphLines, 2, 0, 1, 2},      // ┩
	0x252A: {glyphLines, 1, 0, 2, 2},      // ┪
	0x252B: {glyphLines, 2, 0, 2, 2},      // ┫
	0x252C: {glyphLines, 0, 1, 1, 1},      // ┬
	0x252D: {glyphLines, 0, 1, 1, 2},      // ┭
	0x252E: {glyphLines, 0, 2, 1, 1},      // ┮
	0x252F: {glyphLines, 0, 2, 1, 2},      // ┯
	0x2530: {glyphLines, 0, 1, 2, 1},      // ┰
	0x2531: {glyphLines, 0, 1, 2, 2},      // ┱
	0x2532: {glyphLines, 0, 2, 2, 1},      // ┲
	0x2533: {glyphLines, 0, 2, 2, 2},      // ┳
	0x2534: {glyphLines, 1, 1, 0, 1},      // ┴
	0x2535: {glyphLines, 1, 1, 0, 2},      // ┵
	0x2536: {glyphLines, 1, 2, 0, 1},      // ┶
	0x2537: {glyphLines, 1, 2, 0, 2},      // ┷
	0x2538: {glyphLines, 2, 1, 0, 1},      // ┸
	0x2539: {glyphLines, 2, 1, 0, 2},      // ┹
	0x253A: {glyphLines, 2, 2, 0, 1},      // ┺
	0x253B: {glyphLines, 2, 2, 0, 2},      // ┻
	0x253C: {glyphLines, 1, 1, 1, 1},      // ┼
	0x253D: {glyphLines, 1, 1, 1, 2},      // ┽
	0x253E: {glyphLines, 1, 2, 1, 1},      // ┾
	0x253F: {glyphLines, 1, 2, 1, 2},      // ┿
	0x2540: {glyphLines, 2, 1, 1, 1},      // ╀
	0x2541: {glyphLines, 1, 1, 2, 1},      // ╁
	0x2542: {glyphLines, 2, 1, 2, 1},      // ╂
	0x2543: {glyphLines, 2, 1, 1, 2},      // ╃
	0x2544: {glyphLines, 2, 2, 1, 1},      // ╄
	0x2545: {glyphLines, 1, 1, 2, 2},      // ╅
	0x2546: {glyphLines, 1, 2, 2, 1},      // ╆
	0x2547: {glyphLines, 2, 2, 1, 2},      // ╇
	0x2548: {glyphLines, 1, 2, 2, 2},      // ╈
	0x2549: {glyphLines, 2, 1, 2, 2},      // ╉
	0x254A: {glyphLines, 2, 2, 2, 1},      // ╊
	0x254B: {glyphLines, 2, 2, 2, 2},      // ╋
	0x254C: {glyphDashes, 0, 1, 2, 0},     // ╌
	0x254D: {glyphDashes, 0, 2, 2, 0},     // ╍
	0x254E: {glyphDashes, 1, 1, 2, 0},     // ╎
	0x254F: {glyphDashes, 1, 2, 2, 0},     // ╏
	0x2550: {glyphLines, 0, 3, 0, 3},      // ═
	0x2551: {glyphLines, 3, 0, 3, 0},      // ║
	0x2552: {glyphLines, 0, 3, 1, 0},      // ╒
	0x2553: {glyphLines, 0, 1, 3, 0},      // ╓
	0x2554: {glyphLines, 0, 3, 3, 0},      // ╔
	0x2555: {glyphLines, 0, 0, 1, 3},      // ╕
	0x2556: {glyphLines, 0, 0, 3, 1},      // ╖
	0x2557: {glyphLines, 0, 0, 3, 3},      // ╗
	0x2558: {glyphLines, 1, 3, 0, 0},      // ╘
	0x2559: {glyphLines, 3, 1, 0, 0},      // ╙
	0x255A: {glyphLines, 3, 3, 0, 0},      // ╚
	0x255B: {glyphLines, 1, 0, 0, 3},      // ╛
	0x255C: {glyphLines, 3, 0, 0, 1},      // ╜
	0x255D: {glyphLines, 3, 0, 0, 3},      // ╝
	0x255E: {glyphLines, 1, 3, 1, 0},      // ╞
	0x255F: {glyphLines, 3, 1, 3, 0},      // ╟
	0x2560: {glyphLines, 3, 3, 3, 0},      // ╠
	0x2561: {glyphLines, 1, 0, 1, 3},      // ╡
	0x2562: {glyphLines, 3, 0, 3, 1},      // ╢
	0x2563: {glyphLines, 3, 0, 3, 3},      // ╣
	0x2564: {glyphLines, 0, 3, 1, 3},      // ╤
	0x2565: {glyphLines, 0, 1, 3, 1},      // ╥
	0x2566: {glyphLines, 0, 3, 3, 3},      // ╦
	0x2567: {glyphLines, 1, 3, 0, 3},      // ╧
	0x2568: {glyphLines, 3, 1, 0, 1},      // ╨
	0x2569: {glyphLines, 3, 3, 0, 3},      // ╩
	0x256A: {glyphLines, 1, 3, 1, 3},      // ╪
	0x256B: {glyphLines, 3, 1, 3, 1},      // ╫
	0x256C: {glyphLines, 3, 3, 3, 3},      // ╬
	0x256D: {glyphArc, 0, 0, 0, 0},        // ╭
	0x256E: {glyphArc, 1, 0, 0, 0},        // ╮
	0x256F: {glyphArc, 2, 0, 0, 0},        // ╯
	0x2570: {glyphArc, 3, 0, 0, 0},        // ╰
	0x2571: {glyphDiagonal, 1, 0, 0, 0},   // ╱
	0x2572: {glyphDiagonal, 2, 0, 0, 0},   // ╲
	0x2573: {glyphDiagonal, 3, 0, 0, 0},   // ╳
	0x2574: {glyphLines, 0, 0, 0, 1},      // ╴
	0x2575: {glyphLines, 1, 0, 0, 0},      // ╵
	0x2576: {glyphLines, 0, 1, 0, 0},      // ╶
	0x2577: {glyphLines, 0, 0, 1, 0},      // ╷
	0x2578: {glyphLines, 0, 0, 0, 2},      // ╸
	0x2579: {glyphLines, 2, 0, 0, 0},      // ╹
	0x257A: {glyphLines, 0, 2, 0, 0},      // ╺
	0x257B: {glyphLines, 0, 0, 2, 0},      // ╻
	0x257C: {glyphLines, 0, 2, 0, 1},      // ╼
	0x257D: {glyphLines, 1, 0, 2, 0},      // ╽
	0x257E: {glyphLines, 0, 1, 0, 2},      // ╾
	0x257F: {glyphLines, 2, 0, 1, 0},      // ╿
	0x2580: {glyphBlock, 0, 0, 8, 4},      // ▀
	0x2581: {glyphBlock, 0, 7, 8, 8},      // ▁
	0x2582: {glyphBlock, 0, 6, 8, 8},      // ▂
	0x2583: {glyphBlock, 0, 5, 8, 8},      // ▃
	0x2584: {glyphBlock, 0, 4, 8, 8},      // ▄
	0x2585: {glyphBlock, 0, 3, 8, 8},      // ▅
	0x2586: {glyphBlock, 0, 2, 8, 8},      // ▆
	0x2587: {glyphBlock, 0, 1, 8, 8},      // ▇
	0x2588: {glyphBlock, 0, 0, 8, 8},      // █
	0x2589: {glyphBlock, 0, 0, 7, 8},      // ▉
	0x258A: {glyphBlock, 0, 0, 6, 8},      // ▊
	0x258B: {glyphBlock, 0, 0, 5, 8},      // ▋
	0x258C: {glyphBlock, 0, 0, 4, 8},      // ▌
	0x258D: {glyphBlock, 0, 0, 3, 8},      // ▍
	0x258E: {glyphBlock, 0, 0, 2, 8},      // ▎
	0x258F: {glyphBlock, 0, 0, 1, 8},      // ▏
	0x2590: {glyphBlock, 4, 0, 8, 8},      // ▐
	0x2591: {glyphShade, 25, 0, 0, 0},     // ░
	0x2592: {glyphShade, 50, 0, 0, 0},     // ▒
	0x2593: {glyphShade, 75, 0, 0, 0},     // ▓
	0x2594: {glyphBlock, 0, 0, 8, 1},      // ▔
	0x2595: {glyphBlock, 7, 0, 8, 8},      // ▕
	0x2596: {glyphQuadrants, 4, 0, 0, 0},  // ▖
	0x2597: {glyphQuadrants, 8, 0, 0, 0},  // ▗
	0x2598: {glyphQuadrants, 1, 0, 0, 0},  // ▘
	0x2599: {glyphQuadrants, 13, 0, 0, 0}, // ▙
	0x259A: {glyphQuadrants, 9, 0, 0, 0},  // ▚
	0x259B: {glyphQuadrants, 7, 0, 0, 0},  // ▛
	0x259C: {glyphQuadrants, 11, 0, 0, 0}, // ▜
	0x259D: {glyphQuadrants, 2, 0, 0, 0},  // ▝
	0x259E: {glyphQuadrants, 6, 0, 0, 0},  // ▞
	0x259F: {glyphQuadrants, 14, 0, 0, 0}, // ▟
	0xE0B0: {glyphPowerline, 0, 0, 0, 0},  // 
	0xE0B1: {glyphPowerline, 1, 0, 0, 0},  // 
	0xE0B2: {glyphPowerline, 2, 0, 0, 0},  // 
	0xE0B3: {glyphPowerline, 3, 0, 0, 0},  // 
	0xE0B4: {glyphPowerline, 4, 0, 0, 0},  // 
	0xE0B5: {glyphPowerline, 5, 0, 0, 0},  // 
	0xE0B6: {glyphPowerline, 6, 0, 0, 0},  // 
	0xE0B7: {glyphPowerline, 7, 0, 0, 0},  // 
}

// isBoxGlyph reports whether a cell is drawn as a shape: it ends a run of text.
func isBoxGlyph(c Cell) bool {
	return isBoxRune(c.Rune) && c.Comb == ""
}
