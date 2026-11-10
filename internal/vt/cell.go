// Package vt is SSHYard's terminal emulator: a parser of the escape
// sequences a VT100/xterm-compatible terminal understands and the grid of
// cells they draw on, with scrollback, the alternate screen, 256 colors
// and truecolor. It has no UI; package app draws its cells natively with
// MyGo's Painter.
package vt

import "sync"

// Color is a cell's color: the default, an entry of the 256-color
// palette, or a 24-bit RGB value.
type Color uint32

const (
	colorDefault = 0
	colorIndexed = 1 << 24
	colorRGB     = 2 << 24
)

// DefaultColor is the terminal's default foreground or background.
const DefaultColor Color = colorDefault

// Indexed returns entry i of the 256-color palette.
func Indexed(i uint8) Color { return Color(colorIndexed | uint32(i)) }

// RGB returns a truecolor color.
func RGB(r, g, b uint8) Color {
	return Color(colorRGB | uint32(r)<<16 | uint32(g)<<8 | uint32(b))
}

// IsDefault reports whether c is the default color.
func (c Color) IsDefault() bool { return c == colorDefault }

// Index returns the palette index of an indexed color.
func (c Color) Index() (uint8, bool) { return uint8(c), c&0xff000000 == colorIndexed }

// RGB returns the components of a truecolor color.
func (c Color) RGB() (r, g, b uint8, ok bool) {
	return uint8(c >> 16), uint8(c >> 8), uint8(c), c&0xff000000 == colorRGB
}

// Attr is a set of cell attributes.
type Attr uint16

const (
	Bold Attr = 1 << iota
	Faint
	Italic
	Underline
	Blink
	Inverse
	Hidden
	Strike
)

// Cell is one cell of the grid. A wide character (CJK, most emoji) takes
// two cells: the first has Width 2, the second Width 0 and no rune.
// Comb holds what joins Rune in its cell: combining marks, variation
// selectors, emoji modifiers, and ZWJ with what it joins.
//
// A cell holds no pointer, so the garbage collector does not scan the
// cells of the scrollback, megabytes of them.
type Cell struct {
	Rune  rune
	Comb  Comb
	FG    Color
	BG    Color
	Attr  Attr
	Width uint8
}

// Comb is what joins a cell's rune, interned: 0 is nothing.
type Comb uint32

// maxCombs bounds the interned sequences, which output could otherwise
// grow without end: past it a new sequence is not joined.
const maxCombs = 1 << 16

var combs struct {
	sync.RWMutex
	ids  map[string]Comb
	strs []string // strs[id-1]
}

// CombOf interns s; it is 0 for "", or when the table is full.
func CombOf(s string) Comb {
	if s == "" {
		return 0
	}
	combs.RLock()
	id, ok := combs.ids[s]
	combs.RUnlock()
	if ok {
		return id
	}
	combs.Lock()
	defer combs.Unlock()
	if id, ok := combs.ids[s]; ok {
		return id
	}
	if len(combs.strs) >= maxCombs {
		return 0
	}
	if combs.ids == nil {
		combs.ids = map[string]Comb{}
	}
	combs.strs = append(combs.strs, s)
	id = Comb(len(combs.strs))
	combs.ids[s] = id
	return id
}

// String is the sequence.
func (c Comb) String() string {
	if c == 0 {
		return ""
	}
	combs.RLock()
	defer combs.RUnlock()
	return combs.strs[c-1]
}

// blank is an erased cell in the colors of the pen (background color
// erase, as xterm does).
func blank(pen Cell) Cell {
	return Cell{Rune: ' ', Width: 1, BG: pen.BG}
}

// Line is a row of cells. Wrapped is set when the text continued on the
// next line because it reached the right margin.
type Line struct {
	Cells   []Cell
	Wrapped bool
	// widePad is set on a wrapped line whose last cell is the blank left
	// where a wide character did not fit: it is not part of the text.
	widePad bool
	// Time is when output first reached the line, in Unix seconds; 0 for
	// a line it never did.
	Time int64
}

func newLine(cols int, pen Cell) Line {
	l := Line{Cells: make([]Cell, cols)}
	b := blank(pen)
	for i := range l.Cells {
		l.Cells[i] = b
	}
	return l
}

// Text is the cell's text: its rune and what joins it.
func (c Cell) Text() string {
	if c.Comb == 0 {
		return string(c.Rune)
	}
	return string(c.Rune) + c.Comb.String()
}

// String returns the line's text, without trailing blanks.
func (l Line) String() string { return l.text(true) }

func (l Line) text(trim bool) string {
	rs := make([]rune, 0, len(l.Cells))
	for _, c := range l.Cells {
		if c.Width == 0 {
			continue
		}
		r := c.Rune
		if r == 0 {
			r = ' '
		}
		rs = append(rs, r)
		if c.Comb != 0 {
			rs = append(rs, []rune(c.Comb.String())...)
		}
	}
	n := len(rs)
	for trim && n > 0 && rs[n-1] == ' ' {
		n--
	}
	return string(rs[:n])
}
