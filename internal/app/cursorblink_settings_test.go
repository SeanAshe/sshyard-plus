package app

import (
	"path/filepath"
	"testing"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestTerminalCursorBlinkSettings(t *testing.T) {
	a, tt := newTestApp(t)
	a.openSettings(prefTerminal)
	if !a.Settings().TerminalCursorBlink {
		t.Fatal("cursor blinking must default on")
	}
	tt.Frame()
	m := locales.In("en")
	scrollToCursorChoice(t, tt, m.CursorBlock())
	for _, on := range []bool{false, true} {
		r := cursorBlinkSwitchBounds(t, tt, m)
		tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
		tt.Frame()
		if got := a.Settings().TerminalCursorBlink; got != on {
			t.Fatalf("live cursor blink = %v, want %v", got, on)
		}
		saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json"))
		if err != nil || saved.TerminalCursorBlink != on {
			t.Fatalf("saved cursor blink = %v, want %v, error %v", saved.TerminalCursorBlink, on, err)
		}
	}
}

// cursorBlinkSwitchBounds is where the cursor blink switch is: its row's
// title shares the switch's label, which Find returns first, so derive the
// switch from the row and the shape select above it.
func cursorBlinkSwitchBounds(t *testing.T, tt *ui.Tester, m locales.Messages) ui.Rect {
	t.Helper()
	label := mustFind(t, tt, m.TerminalCursorBlink())
	note := mustFind(t, tt, m.TerminalCursorBlinkNote())
	shape := mustFind(t, tt, m.CursorBlock())
	// Both controls align to the row's right edge, the shape select is 180 wide
	// with 14 DIP horizontal padding; Switch is 36 x 20 and centered in its row.
	return ui.Rect{X: shape.X - theme.Space(3.5) + 180 - theme.Space(9), Y: (label.Y+note.Y+note.H)/2 - theme.Space(2.5), W: theme.Space(9), H: theme.Space(5)}
}
