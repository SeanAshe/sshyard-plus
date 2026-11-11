package app

import (
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/vt"
	"github.com/egoist/mygo/ui"
)

// TestCursorMoveBytes: the arrows for a move, in the cursor key mode.
func TestCursorMoveBytes(t *testing.T) {
	app := vt.Modes{AppCursorKeys: true}
	for _, c := range []struct {
		steps int
		m     vt.Modes
		want  string
	}{
		{0, vt.Modes{}, ""},
		{3, vt.Modes{}, "\x1b[C\x1b[C\x1b[C"},
		{-2, vt.Modes{}, "\x1b[D\x1b[D"},
		{2, app, "\x1bOC\x1bOC"},
		{-1, app, "\x1bOD"},
	} {
		if got := string(cursorMoveBytes(c.steps, c.m)); got != c.want {
			t.Errorf("%d steps (DECCKM %v): %q, want %q", c.steps, c.m.AppCursorKeys, got, c.want)
		}
	}
}

// TestTerminalAltClickMovesCursor: a ⌥/Alt click on the cursor's line
// sends the arrows that take the cursor there; a plain click, a drag, a
// double click, a program reading the mouse and the alternate screen
// send none.
func TestTerminalAltClickMovesCursor(t *testing.T) {
	term := vt.New(20, 4)
	var out []byte
	v := newTermView(recordTarget{term, &out}, 13)
	v.cellW, v.cellH, v.cols, v.rows = 10, 20, 20, 4
	at := func(col, row int) (float32, float32) {
		return termPad + float32(col)*10 + 5, termPad + float32(row)*20 + 5
	}
	click := func(col, row int, mods ui.Modifiers, clicks int) string {
		t.Helper()
		out = out[:0]
		x, y := at(col, row)
		v.input(ui.InputEvent{Kind: ui.InputPointerDown, X: x, Y: y, Mods: mods, Clicks: clicks}, nil)
		v.input(ui.InputEvent{Kind: ui.InputPointerUp, X: x, Y: y, Mods: mods}, nil)
		return string(out)
	}

	term.WriteString("$ hello world") // the cursor at column 13
	if got, want := click(2, 0, ui.Alt, 1), strings.Repeat("\x1b[D", 11); got != want {
		t.Fatalf("Alt click: sent %q, want %q", got, want)
	}
	if got := click(2, 0, 0, 1); got != "" {
		t.Errorf("plain click sent %q", got)
	}
	if got := click(18, 0, ui.Alt, 1); got != "" {
		t.Errorf("Alt click past the text sent %q", got)
	}
	if got := click(2, 0, ui.Alt, 2); got != "" {
		t.Errorf("Alt double click sent %q", got)
	}
	if got := click(2, 0, ui.Alt|ui.Shift, 1); got != "" {
		t.Errorf("Alt+Shift click sent %q", got)
	}

	// A drag selects, and moves nothing.
	out = out[:0]
	x, y := at(2, 0)
	x2, _ := at(6, 0)
	v.input(ui.InputEvent{Kind: ui.InputPointerDown, X: x, Y: y, Mods: ui.Alt, Clicks: 1}, nil)
	v.input(ui.InputEvent{Kind: ui.InputPointerMove, X: x2, Y: y, Mods: ui.Alt}, nil)
	v.input(ui.InputEvent{Kind: ui.InputPointerUp, X: x2, Y: y, Mods: ui.Alt}, nil)
	if len(out) != 0 || v.SelectedText() != "hello" {
		t.Errorf("Alt drag: sent %q, selected %q", out, v.SelectedText())
	}

	// Another line is left alone.
	term.WriteString("\r\n$ ")
	if got := click(5, 0, ui.Alt, 1); got != "" {
		t.Errorf("Alt click on another line sent %q", got)
	}
	term.WriteString("echo hi\r") // the cursor at column 0 of row 1
	if got, want := click(4, 1, ui.Alt, 1), strings.Repeat("\x1b[C", 4); got != want {
		t.Fatalf("Alt click to the right: sent %q, want %q", got, want)
	}

	// DECCKM: the application's arrows.
	term.WriteString("\x1b[?1h")
	if got, want := click(4, 1, ui.Alt, 1), strings.Repeat("\x1bOC", 4); got != want {
		t.Fatalf("Alt click under DECCKM: sent %q, want %q", got, want)
	}
	term.WriteString("\x1b[?1l")

	// A program reading the mouse keeps it.
	term.WriteString("\x1b[?1000h\x1b[?1006h")
	if got := click(4, 1, ui.Alt, 1); strings.Contains(got, "\x1b[C") {
		t.Errorf("Alt click with mouse tracking sent arrows: %q", got)
	}
	term.WriteString("\x1b[?1000l")

	// The alternate screen.
	term.WriteString("\x1b[?1049h$ hello")
	if got := click(2, 0, ui.Alt, 1); got != "" {
		t.Errorf("Alt click on the alternate screen sent %q", got)
	}
}
