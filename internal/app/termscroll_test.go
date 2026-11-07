package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/vt"
)

// TestTerminalScrollbarDrag: the scrollbar in the right padding drags
// through the scrollback without selecting text, a press on its strip
// brings the thumb there, and on the alternate screen, which has no
// scrollback, the strip is the program's.
func TestTerminalScrollbarDrag(t *testing.T) {
	var out strings.Builder
	for i := range 200 {
		fmt.Fprintf(&out, "line %d\r\n", i)
	}
	srv, sh := startScript(t, out.String())
	a, tt := newTestApp(t)
	c := connected(t, a, tt, srv)
	tv := c.tv
	waitFor(t, tt, "the scrollback", func() bool { return tv.snap.Scrollback > 100 })
	tt.Frame()

	r := mustFind(t, tt, "Terminal box")
	x := r.X + r.W - termPad/2
	_, thumb, ok := tv.scrollBar()
	if !ok {
		t.Fatal("no scrollbar with a scrollback")
	}
	sb := tv.snap.Scrollback

	// the thumb, at the bottom, dragged to the top
	tt.Move(x, r.Y+thumb.Y+thumb.H/2)
	tt.Frame()
	tt.Press(x, r.Y+thumb.Y+thumb.H/2)
	tt.Move(x, r.Y+r.H/2)
	if tv.offset <= 0 || tv.offset >= sb {
		t.Errorf("dragged halfway the offset is %d of %d", tv.offset, sb)
	}
	tt.Move(x, r.Y-100)
	if tv.offset != sb {
		t.Errorf("dragged past the top the offset is %d, want %d", tv.offset, sb)
	}
	tt.Release(x, r.Y-100)
	tt.Frame()
	if tv.barDrag || tv.selected || tv.selecting {
		t.Errorf("after the drag: dragging %v, selected %v, selecting %v", tv.barDrag, tv.selected, tv.selecting)
	}

	// a press at the strip's bottom, off the thumb, goes back to the screen
	tt.ClickAt(x, r.Y+r.H-1)
	tt.Frame()
	if tv.offset != 0 || tv.selected {
		t.Errorf("a press at the bottom: offset %d, selected %v", tv.offset, tv.selected)
	}

	// the alternate screen has no scrollback: the press is the program's
	sh.take()
	sh.out <- "\x1b[?1049h\x1b[?1000h\x1b[?1006hprogram"
	waitFor(t, tt, "the alternate screen", func() bool {
		m := c.s.Term.Modes()
		return m.AltScreen && m.Mouse != vt.MouseOff
	})
	tt.Frame()
	if _, _, ok := tv.scrollBar(); ok {
		t.Error("a scrollbar on the alternate screen")
	}
	tt.ClickAt(x, r.Y+r.H/2)
	expectRead(t, tt, sh, "\x1b[<0;")
}
