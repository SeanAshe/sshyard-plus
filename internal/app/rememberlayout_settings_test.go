package app

import (
	"testing"

	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestRememberLayoutAppearanceSwitch(t *testing.T) {
	a, _ := newTestApp(t)
	s, changed := a.Settings(), false
	m := locales.In("en")
	tt := ui.NewTester(func(c *ui.Context) {
		a.prefAppearance(c, m, currentTokens(c), &s, &changed)
	}, 900, 1400)
	tt.SetLocale("en")
	tt.Frame()
	if a.Settings().RememberLayout {
		t.Fatal("layout preference must default off")
	}
	label := mustFind(t, tt, m.RememberLayout())
	note := mustFind(t, tt, m.RememberLayoutNote())
	// The switch sits at the right edge of its settings row, vertically
	// centered across the label and note, while Find returns the row text first.
	x, y := float32(900-14-18), (label.Y+note.Y+note.H)/2
	tt.ClickAt(x, y)
	tt.Frame()
	if !s.RememberLayout || !changed {
		t.Fatal("layout switch did not turn on")
	}
	changed = false
	tt.ClickAt(x, y)
	tt.Frame()
	if s.RememberLayout || !changed {
		t.Fatal("layout switch did not turn back off")
	}
}
