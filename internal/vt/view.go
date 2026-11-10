package vt

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Pos is a position in the terminal's history: Line is absolute, counting
// every line that ever scrolled into the scrollback, so it stays put as
// output arrives; Col is a cell column.
type Pos struct{ Line, Col int }

// Less orders positions.
func (p Pos) Less(q Pos) bool { return p.Line < q.Line || (p.Line == q.Line && p.Col < q.Col) }

// Snapshot is a copy of what the terminal shows, for drawing.
type Snapshot struct {
	Cols, Rows int
	// Lines are Rows lines from line First of the history.
	Lines []Line
	First int
	// Cursor is where the cursor is on the screen, in Lines' coordinates;
	// CursorVisible is false when the program hid it or it is scrolled
	// out of view.
	CursorX, CursorY int
	CursorVisible    bool
	CursorStyle      CursorStyle
	CursorStyleSet   bool // the program selected CursorStyle with DECSCUSR
	CursorBlink      bool
	// Scrollback is how many lines are above the screen; Offset how many
	// of them the view is scrolled up.
	Scrollback, Offset int
	Modes              Modes
}

// Snapshot returns the screen, scrolled up offset lines into the
// scrollback (0 is the live screen).
func (t *Terminal) Snapshot(offset int) Snapshot {
	var s Snapshot
	t.SnapshotInto(&s, offset)
	return s
}

// SnapshotInto is Snapshot into s, reusing the cells of its lines: for a
// view that takes one every frame. Every line has Cols cells, the
// scrollback's short ones filled with blanks.
func (t *Terminal) SnapshotInto(s *Snapshot, offset int) {
	lines := s.Lines
	t.mu.Lock()
	defer t.mu.Unlock()
	offset = t.offset(offset)
	*s = Snapshot{
		Cols: t.cols, Rows: t.rows,
		Scrollback: t.scrollbackLen(), Offset: offset,
		CursorStyle: t.style, CursorStyleSet: t.styleSet, CursorBlink: t.cursorBlink,
		Modes: t.modes,
	}
	s.Modes.AltScreen = t.altActive
	s.First = t.firstLine() + t.historyLen() - t.rows - offset
	if cap(lines) < t.rows {
		lines = make([]Line, t.rows)
	}
	s.Lines = lines[:t.rows]
	for y := range s.Lines {
		src := t.lineAt(s.First + y)
		cells := append(s.Lines[y].Cells[:0], src.Cells[:min(len(src.Cells), t.cols)]...)
		for len(cells) < t.cols {
			cells = append(cells, blank(Cell{}))
		}
		s.Lines[y] = Line{Cells: cells, Wrapped: src.Wrapped, widePad: src.widePad, Time: src.Time}
	}
	s.CursorX, s.CursorY = t.cur.x, t.cur.y+offset
	s.CursorVisible = t.modes.CursorVisible && s.CursorY < t.rows
}

// Cursor is where Snapshot(offset) has the cursor, without copying the
// screen.
func (t *Terminal) Cursor(offset int) (x, y int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cur.x, t.cur.y + t.offset(offset)
}

// scrollbackLen is how many lines are above the screen shown.
func (t *Terminal) scrollbackLen() int {
	if t.altActive {
		return 0
	}
	return t.scrollback.len()
}

// offset is offset kept within the scrollback.
func (t *Terminal) offset(offset int) int { return max(0, min(offset, t.scrollbackLen())) }

// historyLen is the number of lines of history, scrollback and screen.
func (t *Terminal) historyLen() int {
	if t.altActive {
		return t.rows
	}
	return t.scrollback.len() + t.rows
}

// lineAt returns the absolute line n, or an empty line.
func (t *Terminal) lineAt(n int) Line {
	i := n - t.dropped
	if t.altActive {
		i = n - t.dropped - t.scrollback.len()
		if i >= 0 && i < t.rows {
			return t.alt.lines[i]
		}
		return Line{}
	}
	switch {
	case i < 0:
		return Line{}
	case i < t.scrollback.len():
		return t.scrollback.at(i)
	case i-t.scrollback.len() < t.rows:
		return t.primary.lines[i-t.scrollback.len()]
	}
	return Line{}
}

// firstLine is the absolute number of the oldest line kept.
func (t *Terminal) firstLine() int {
	if t.altActive {
		return t.dropped + t.scrollback.len()
	}
	return t.dropped
}

// Text returns the text from a to b, inclusive of b's cell. Lines that
// wrapped are joined; others end with a newline.
func (t *Terminal) Text(a, b Pos) string {
	if b.Less(a) {
		a, b = b, a
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var sb strings.Builder
	a.Line = max(a.Line, t.firstLine())
	last := t.firstLine() + t.historyLen() - 1
	b.Line = min(b.Line, last)
	for n := a.Line; n <= b.Line; n++ {
		l := t.lineAt(n)
		from, to := 0, len(l.Cells)
		if n == a.Line {
			from = min(a.Col, to)
		}
		if n == b.Line {
			to = min(b.Col+1, to)
		} else if l.Wrapped && l.widePad {
			to = max(from, to-1) // the blank a wide character left
		}
		seg := Line{Cells: l.Cells[from:to]}.text(n == b.Line || !l.Wrapped)
		sb.WriteString(seg)
		if n < b.Line && !l.Wrapped {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// ScreenText returns the text of the screen, one line per row, without
// trailing blank lines: for tests and for copying all.
func (t *Terminal) ScreenText() string {
	s := t.Snapshot(0)
	out := make([]string, len(s.Lines))
	for i, l := range s.Lines {
		out[i] = l.String()
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// Match is a search result: Len cells from Pos.
type Match struct {
	Pos
	Len int
}

// Find returns every match of query in the history, oldest first. It
// ignores case unless caseSensitive. A match does not span lines.
func (t *Terminal) Find(query string, caseSensitive bool) []Match {
	if query == "" {
		return nil
	}
	// Decomposed on both sides, so é typed matches e + U+0301 on screen
	// and the other way round.
	q := []rune(norm.NFD.String(query))
	if !caseSensitive {
		for i := range q {
			q[i] = unicode.ToLower(q[i])
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []Match
	// The runes of a line, with the cells each is in, in buffers the
	// lines share.
	var rs []rune
	var cols, ends []int
	add := func(r rune, x int, w uint8) {
		if !caseSensitive {
			r = unicode.ToLower(r)
		}
		rs = append(rs, r)
		cols = append(cols, x)
		ends = append(ends, x+int(w))
	}
	first := t.firstLine()
	for n := first; n < first+t.historyLen(); n++ {
		l := t.lineAt(n)
		rs, cols, ends = rs[:0], cols[:0], ends[:0]
		for x, c := range l.Cells {
			switch {
			case c.Width == 0:
			case c.Comb == 0 && c.Rune < 0x80:
				add(c.Rune, x, c.Width)
			default:
				for _, r := range norm.NFD.String(c.Text()) {
					add(r, x, c.Width)
				}
			}
		}
		for i := 0; i+len(q) <= len(rs); i++ {
			ok := true
			for j := range q {
				if rs[i+j] != q[j] {
					ok = false
					break
				}
			}
			if ok {
				out = append(out, Match{Pos{n, cols[i]}, ends[i+len(q)-1] - cols[i]})
				i += len(q) - 1
			}
		}
	}
	return out
}

// OffsetFor returns the scroll offset that brings absolute line n into
// view, keeping the view still when it already is.
func (t *Terminal) OffsetFor(n, current int) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.altActive {
		return 0
	}
	sb := t.scrollback.len()
	first := t.dropped + sb - current
	if n >= first && n < first+t.rows {
		return current
	}
	off := t.dropped + sb - n + t.rows/2
	return max(0, min(off, sb))
}

// WordAt returns the word around p, for double-click selection.
func (t *Terminal) WordAt(p Pos) (Pos, Pos) {
	t.mu.Lock()
	defer t.mu.Unlock()
	l := t.lineAt(p.Line)
	if p.Col < 0 || p.Col >= len(l.Cells) {
		return p, p
	}
	isWord := func(c Cell) bool {
		return c.Width == 0 || (c.Rune != ' ' && c.Rune != 0 && !strings.ContainsRune("\"'`()[]{}<>|;,", c.Rune))
	}
	if !isWord(l.Cells[p.Col]) {
		return p, p
	}
	a, b := p.Col, p.Col
	for a > 0 && isWord(l.Cells[a-1]) {
		a--
	}
	for b < len(l.Cells)-1 && isWord(l.Cells[b+1]) {
		b++
	}
	return Pos{p.Line, a}, Pos{p.Line, b}
}
