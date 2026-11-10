package vt

// history is the scrollback: a ring of lines, oldest first, so that a
// line scrolling in when it is full takes the oldest one's place instead
// of moving every other.
type history struct {
	buf  []Line
	head int // where the oldest line is in buf
}

// len is the number of lines.
func (h *history) len() int { return len(h.buf) }

// at is line i, 0 the oldest.
func (h *history) at(i int) Line {
	i += h.head
	if i >= len(h.buf) {
		i -= len(h.buf)
	}
	return h.buf[i]
}

// push adds l as the newest line, keeping at most max; it reports
// whether the oldest line went for it.
func (h *history) push(l Line, max int) bool {
	if len(h.buf) < max {
		if h.head != 0 {
			h.set(h.lines())
		}
		if len(h.buf) == cap(h.buf) {
			// Grow as append would, but never past max.
			buf := make([]Line, len(h.buf), min(2*cap(h.buf)+64, max))
			copy(buf, h.buf)
			h.buf = buf
		}
		h.buf = append(h.buf, l)
		return false
	}
	if len(h.buf) == 0 {
		return true
	}
	h.buf[h.head] = l
	h.head++
	if h.head == len(h.buf) {
		h.head = 0
	}
	return true
}

// lines returns the lines in order, in a slice of their own.
func (h *history) lines() []Line {
	out := make([]Line, len(h.buf))
	n := copy(out, h.buf[h.head:])
	copy(out[n:], h.buf[:h.head])
	return out
}

// set makes ls the lines; it keeps ls, which its caller must not append
// to.
func (h *history) set(ls []Line) {
	h.buf, h.head = ls[:len(ls):len(ls)], 0
}

// trim drops the oldest lines past max and returns how many went.
func (h *history) trim(max int) int {
	extra := len(h.buf) - max
	if extra <= 0 {
		return 0
	}
	ls := h.lines()
	h.set(append([]Line(nil), ls[extra:]...))
	return extra
}

// trimmed is l compacted, or l when it is already.
func trimmed(l Line) Line {
	if n := len(l.Cells); n > 0 && !l.Wrapped && isBlank(l.Cells[n-1]) && l.Cells[n-1].BG.IsDefault() {
		return compact(l)
	}
	return l
}

// compact is l as the scrollback keeps it: a line of its own, without
// the blanks at its end that only erased, so that a short line of a wide
// terminal takes little room. A wrapped line, full to the margin, keeps
// its cells, as reflowing and copying read its last.
func compact(l Line) Line {
	n := len(l.Cells)
	if !l.Wrapped {
		for n > 0 && isBlank(l.Cells[n-1]) && l.Cells[n-1].BG.IsDefault() {
			n--
		}
	}
	return Line{Cells: append([]Cell(nil), l.Cells[:n]...), Wrapped: l.Wrapped, widePad: l.widePad, Time: l.Time}
}
