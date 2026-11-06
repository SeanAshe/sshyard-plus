package app

import (
	"image/color"
	"path/filepath"
	"testing"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/vt"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestTerminalStripePaint(t *testing.T) {
	for _, dark := range []bool{false, true} {
		pal := theme.For(theme.Auto, dark).Terminal
		term := vt.New(20, 4)
		term.WriteString("\x1b[41m \x1b[0m plain")
		v := newTermView(paintTarget{term}, 13)
		v.stripe = true
		var bounds ui.Rect
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
				bounds = r
				v.paint(p, r, &pal, false)
			})
		}, 400, 200)
		tt.SetDark(dark)
		tt.Frame()
		at := func(row, col int) color.RGBA {
			return tt.Image().RGBAAt(int(bounds.X+termPad+float32(col)*v.cellW+v.cellW/2), int(bounds.Y+termPad+float32(row)*v.cellH+v.cellH/2))
		}
		same := func(got color.RGBA, want ui.Color) bool {
			return got.R == want.R && got.G == want.G && got.B == want.B
		}
		if !same(at(0, 16), pal.Background) {
			t.Errorf("dark %v even row %v, want background %v", dark, at(0, 16), pal.Background)
		}
		if !same(at(1, 16), pal.Stripe()) {
			t.Errorf("dark %v odd row %v, want stripe %v", dark, at(1, 16), pal.Stripe())
		}
		if !same(at(0, 0), pal.ANSI[1]) {
			t.Errorf("dark %v program background %v, want %v", dark, at(0, 0), pal.ANSI[1])
		}
		v.selA, v.selB, v.selected = vt.Pos{Line: 1, Col: 1}, vt.Pos{Line: 1, Col: 1}, true
		tt.Frame()
		if same(at(1, 1), pal.Stripe()) {
			t.Errorf("dark %v selection left the stripe", dark)
		}
		v.stripe = false
		v.selected = false
		tt.Frame()
		if !same(at(1, 3), pal.Background) {
			t.Errorf("dark %v stripe off, odd row %v", dark, at(1, 3))
		}
	}
}

func TestTerminalStripeSettings(t *testing.T) {
	a, tt := newTestApp(t)
	a.openSettings(prefTerminal)
	tt.Frame()
	if !a.Settings().TerminalStripe {
		t.Fatal("striped background must default on")
	}
	for _, lang := range settings.Languages {
		tt.SetLocale(lang)
		tt.Frame()
		m := locales.In(lang)
		if !tt.HasText(m.TerminalStripe()) || !tt.HasText(m.TerminalStripeNote()) {
			t.Fatalf("%s missing stripe setting in %q", lang, tt.Texts())
		}
	}
	tt.SetLocale("en")
	tt.Frame()
	m := locales.In("en")
	for _, on := range []bool{false, true} {
		label := mustFind(t, tt, m.TerminalStripe())
		note := mustFind(t, tt, m.TerminalStripeNote())
		x := float32(a.settings.SidebarWidth) + 720 - theme.Space(8) - 14 - theme.Space(4.5)
		y := (label.Y + note.Y + note.H) / 2
		tt.ClickAt(x, y)
		tt.Frame()
		if got := a.Settings().TerminalStripe; got != on {
			t.Fatalf("live stripe = %v, want %v (click %v,%v label %+v)", got, on, x, y, label)
		}
		saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json"))
		if err != nil || saved.TerminalStripe != on {
			t.Fatalf("saved stripe = %v, want %v, error %v", saved.TerminalStripe, on, err)
		}
	}
}
