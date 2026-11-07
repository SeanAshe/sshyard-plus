package app

import (
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/vt"
	"github.com/egoist/mygo/ui"
)

type cursorBlinkFixture struct {
	term   *vt.Terminal
	v      *termView
	tt     *ui.Tester
	pal    theme.Terminal
	paints int
}

func newCursorBlinkFixture() *cursorBlinkFixture {
	f := &cursorBlinkFixture{term: vt.New(20, 4), pal: theme.For(theme.Auto, false).Terminal}
	f.v = newTermView(paintTarget{f.term}, 13)
	f.tt = ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
			f.paints++
			f.v.paint(p, r, &f.pal, true)
		})
	}, 300, 120)
	return f
}

func TestTerminalCursorBlinkPreferenceDisabled(t *testing.T) {
	for _, seq := range []string{"\x1b[0 q", "\x1b[1 q", "\x1b[5 q", "\x1b[3 q"} {
		f := newCursorBlinkFixture()
		f.v.cursorBlink = false
		f.term.WriteString(seq)
		f.tt.Frame()
		f.tt.Advance(750 * time.Millisecond)
		paints := f.paints
		f.tt.Advance(750 * time.Millisecond)
		if f.paints != paints || !f.v.blink.at.IsZero() {
			t.Fatalf("%q: disabled cursor blinking kept scheduling repaints", seq)
		}
		f.v.cursorBlink = true
		f.tt.Frame()
		if f.v.blink.at.IsZero() {
			t.Fatalf("%q: enabled cursor blinking did not start", seq)
		}
	}
}

func TestTerminalCursorBlinkPreferenceWorkspace(t *testing.T) {
	srv, _ := startScript(t, "cursor-ready")
	a, tt := newTestApp(t)
	cn := connected(t, a, tt, srv)
	shownText(t, tt, cn, "cursor-ready")
	original := cn.s
	s := a.Settings()
	s.TerminalCursorStyle = settings.CursorBar
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	a.focusReq = cn.id
	tt.Frame()
	tt.Frame()
	if cn.tv.cursorStyle != vt.CursorBar || cn.tv.blink.at.IsZero() {
		t.Fatalf("cursor style %v blinking %v", cn.tv.cursorStyle, !cn.tv.blink.at.IsZero())
	}
	applyBlinkPreference(t, a, tt, false)
	cn.s.Term.WriteString("\x1b[5 q")
	tt.Frame()
	tt.Advance(750 * time.Millisecond)
	if cn.tv.cursorBlink || !cn.tv.blink.at.IsZero() {
		t.Fatal("disabled preference left the cursor blinking")
	}
	applyBlinkPreference(t, a, tt, true)
	if !cn.tv.cursorBlink || cn.tv.blink.at.IsZero() {
		t.Fatal("enabled preference did not resume blinking")
	}
	if cn.s != original || cn.s.Status().State != session.Connected {
		t.Fatal("changing cursor blinking replaced or disconnected the SSH session")
	}
}

func TestTerminalCursorBlinkPreferenceProgramSteady(t *testing.T) {
	for _, seq := range []string{"\x1b[2 q", "\x1b[4 q", "\x1b[6 q"} {
		f := newCursorBlinkFixture()
		f.term.WriteString(seq)
		f.v.cursorBlink = true
		f.tt.Frame()
		f.tt.Advance(750 * time.Millisecond)
		if f.term.Snapshot(0).CursorBlink || !f.v.blink.at.IsZero() {
			t.Fatal("enabling blinking overrode the program's steady cursor")
		}
	}
}

func applyBlinkPreference(t *testing.T, a *App, tt *ui.Tester, blink bool) {
	t.Helper()
	s := a.Settings()
	s.TerminalCursorBlink = blink
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}
