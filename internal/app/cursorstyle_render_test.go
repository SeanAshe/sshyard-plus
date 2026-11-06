package app

import (
	"image"
	"runtime"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/vt"
	"github.com/egoist/mygo/ui"
)

// Check the blank cursor cell's interior and the area beyond its bounds,
// so a bar, underline and block cannot pass as each other's rendering.
func assertCursorPixels(t *testing.T, img *image.RGBA, r ui.Rect, v *termView, pal theme.Terminal, style vt.CursorStyle) {
	t.Helper()
	ox, oy := r.X+termPad, r.Y+termPad
	for _, p := range []struct {
		x, y   float32
		cursor bool
	}{
		{1, v.cellH / 2, style == vt.CursorBlock || style == vt.CursorBar},
		{v.cellW / 2, v.cellH / 2, style == vt.CursorBlock},
		{v.cellW / 2, v.cellH - 1, style == vt.CursorBlock || style == vt.CursorUnderline},
		{-2, v.cellH / 2, false},
		{v.cellW + 2, v.cellH / 2, false},
		{v.cellW / 2, v.cellH + 2, false},
	} {
		want := pal.Background
		// Odd terminal rows are a step from the background when stripes are on.
		row := 0
		if p.y >= v.cellH {
			row = 1
		}
		if v.stripe && !v.clear && (v.first+row)%2 == 1 {
			want = pal.Stripe()
		}
		if p.cursor {
			want = pal.Cursor
		}
		got := img.RGBAAt(int(ox+p.x), int(oy+p.y))
		if got.R != want.R || got.G != want.G || got.B != want.B {
			t.Errorf("style %v pixel (%v,%v) = %v, want %v", style, p.x, p.y, got, want)
		}
	}
}

func TestTerminalCursorStylePaint(t *testing.T) {
	for _, dark := range []bool{false, true} {
		pal := theme.For(theme.Auto, dark).Terminal
		term := vt.New(20, 4)
		v := newTermView(paintTarget{term}, 13)
		var bounds ui.Rect
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
				bounds = r
				v.paint(p, r, &pal, true)
			})
		}, 300, 120)
		tt.SetDark(dark)
		for _, tc := range []struct {
			style vt.CursorStyle
			seq   string
			want  vt.CursorStyle
		}{
			{vt.CursorBlock, "", vt.CursorBlock},
			{vt.CursorBar, "", vt.CursorBar},
			{vt.CursorUnderline, "", vt.CursorUnderline},
			{vt.CursorBar, "\x1b[2 q", vt.CursorBlock},
			{vt.CursorBar, "\x1b[99 q", vt.CursorBlock},
			{vt.CursorBar, "\x1bc", vt.CursorBar},
			{vt.CursorUnderline, "\x1b[0 q", vt.CursorBlock},
			{vt.CursorUnderline, "\x1bc", vt.CursorUnderline},
		} {
			v.blink.at = time.Time{}
			v.cursorStyle = tc.style
			term.WriteString(tc.seq)
			tt.Frame()
			assertCursorPixels(t, tt.Image(), bounds, v, pal, tc.want)
		}
	}
}

func TestTerminalCursorStyleWorkspace(t *testing.T) {
	t.Run("SSH", testTerminalCursorStyleSSH)
	t.Run("LocalAndSplit", testTerminalCursorStyleLocal)
}

func testTerminalCursorStyleSSH(t *testing.T) {
	srv, _ := startScript(t, "cursor-ready")
	a, tt := newTestApp(t)
	ssh := connected(t, a, tt, srv)
	shownText(t, tt, ssh, "cursor-ready")
	original := ssh.s
	applyCursorSetting(t, a, tt, settings.CursorBar)
	assertWorkspaceCursor(t, a, tt, ssh, vt.CursorBar)
	ssh.s.Term.WriteString("\x1b[2 q")
	applyCursorSetting(t, a, tt, settings.CursorUnderline)
	assertWorkspaceCursor(t, a, tt, ssh, vt.CursorBlock)
	ssh.s.Term.WriteString("\x1bc")
	assertWorkspaceCursor(t, a, tt, ssh, vt.CursorUnderline)
	if ssh.s != original {
		t.Error("changing the cursor style replaced the SSH session")
	}
}

func testTerminalCursorStyleLocal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the controlled local shell fixture uses POSIX sh and cat")
	}
	withShells(t, session.Shell{ID: "test", Name: "Test shell", Path: "/bin/sh", Args: []string{"-c", "printf cursor-ready; exec cat"}})
	a, tt := newTestApp(t)
	applyCursorSetting(t, a, tt, settings.CursorUnderline)
	if _, err := a.OpenLocalTerminal(); err != nil {
		t.Fatal(err)
	}
	local := a.focused()
	waitFor(t, tt, "local shell output", func() bool { return local.s.Term.ScreenText() == "cursor-ready" })
	assertWorkspaceCursor(t, a, tt, local, vt.CursorUnderline)
	a.SplitFocused(layout.Right)
	split := a.focused()
	waitFor(t, tt, "split shell output", func() bool { return split.s.Term.ScreenText() == "cursor-ready" })
	assertWorkspaceCursor(t, a, tt, split, vt.CursorUnderline)
	applyCursorSetting(t, a, tt, settings.CursorBar)
	for _, cn := range []*conn{local, split} {
		assertWorkspaceCursor(t, a, tt, cn, vt.CursorBar)
	}
}

func applyCursorSetting(t *testing.T, a *App, tt *ui.Tester, style string) {
	t.Helper()
	s := a.Settings()
	s.Theme, s.TerminalCursorStyle = theme.Light, style
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}

func assertWorkspaceCursor(t *testing.T, a *App, tt *ui.Tester, cn *conn, style vt.CursorStyle) {
	t.Helper()
	if !a.ws.Activate(cn.id) {
		t.Fatalf("pane %d is absent", cn.id)
	}
	cn.tv.blink.at = time.Time{}
	cn.s.Term.WriteString("\x1b[2J\x1b[H")
	a.focusReq = cn.id
	tt.Frame()
	tt.Frame()
	if cn.s.Status().State != session.Connected {
		t.Fatalf("pane %d is no longer connected", cn.id)
	}
	r := a.paneBounds[cn.id]
	// The pane border and header separator each consume one DIP
	r.X++
	r.Y += paneHeadHeight + 2
	if len(a.ws.ActiveTab().Panes()) == 1 {
		actual := mustFind(t, tt, "Terminal "+cn.host.Label())
		if r.X != actual.X || r.Y != actual.Y {
			t.Fatalf("terminal origin (%v,%v), pane-derived (%v,%v)", actual.X, actual.Y, r.X, r.Y)
		}
	}
	assertCursorPixels(t, tt.Image(), r, cn.tv, a.termPalette(theme.LightTokens()), style)
}
