package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/highlight"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/vt"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// TestHighlightPaint: a frame colors the keywords of the output in the
// cells it draws, never the terminal's own, leaves the program's colors
// and, unless asked, full-screen programs as they are.
func TestHighlightPaint(t *testing.T) {
	term := vt.New(40, 4)
	term.WriteString("ERROR boom\r\n\x1b[32mERROR\x1b[0m green")
	v := newTermView(paintTarget{term}, 13)
	v.hl = highlight.New(nil, highlight.PresetIDs())
	tok := theme.DarkTokens()
	pal := &tok.Terminal
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c.Key("t")).Grow(1).FillWidth().Draw(func(p *ui.Painter, r ui.Rect) { v.paint(p, r, pal, true) })
	}, 600, 200)
	tt.Frame()
	red := vt.Indexed(1)
	if c := v.snap.Lines[0].Cells[0]; c.FG != red || c.Attr&vt.Bold == 0 {
		t.Errorf("ERROR drawn %+v", c)
	}
	if c := v.snap.Lines[0].Cells[6]; !c.FG.IsDefault() {
		t.Errorf("boom drawn %+v", c)
	}
	if c := v.snap.Lines[1].Cells[0]; c.FG != vt.Indexed(2) {
		t.Errorf("the program's green drawn %+v", c)
	}
	if c := term.Snapshot(0).Lines[0].Cells[0]; !c.FG.IsDefault() {
		t.Errorf("the terminal's own cell colored %+v", c)
	}
	v.hlOverride = true
	tt.Frame()
	if c := v.snap.Lines[1].Cells[0]; c.FG != red {
		t.Errorf("override drawn %+v", c)
	}

	term.WriteString("\x1b[?1049h\x1b[HERROR full")
	tt.Frame()
	if c := v.snap.Lines[0].Cells[0]; !c.FG.IsDefault() {
		t.Errorf("full-screen program colored %+v", c)
	}
	v.hlFullScreen = true
	tt.Frame()
	if c := v.snap.Lines[0].Cells[0]; c.FG != red {
		t.Errorf("full screen asked for, drawn %+v", c)
	}
}

// TestHighlightSettings: the Terminal settings turn the highlighting and
// its built-in rules on and off, add the user's own rules, which match
// first, and refuse an invalid one; the panes follow.
func TestHighlightSettings(t *testing.T) {
	srv, _ := startScript(t, "ERROR boom")
	a, tt := newTestApp(t)
	tt.SetSize(1300, 900)
	cn := connected(t, a, tt, srv)
	waitFor(t, tt, "the output", func() bool {
		tt.Frame()
		return cn.tv.snap.Lines != nil && cn.tv.snap.Lines[0].Cells[0].FG == vt.Indexed(1)
	})

	a.openSettings(prefTerminal)
	tt.Frame()
	a.openHighlightRule(-1)
	tt.Frame()
	if !a.hlEditing {
		t.Fatal("no rule sheet")
	}
	m := locales.In("en")
	a.hlForm.rule.Pattern, a.hlForm.rule.Regex = "(", true
	a.saveHighlightRule(m)
	tt.Frame()
	if !a.hlEditing || a.hlForm.err != "Invalid regular expression: error parsing regexp: missing closing ): `(`" {
		t.Fatalf("invalid rule saved: %v %q %q", a.hlEditing, a.hlForm.err, a.hlForm.rule.Pattern)
	}
	a.hlForm.rule.Pattern, a.hlForm.rule.Regex, a.hlForm.rule.Color = "boom", false, 5
	a.saveHighlightRule(m)
	tt.Frame()
	if a.hlEditing || len(a.settings.Highlight.Rules) != 1 {
		t.Fatalf("rule not saved: %+v", a.settings.Highlight.Rules)
	}
	b, _ := os.ReadFile(filepath.Join(a.p.DataDir, "settings.json"))
	if !strings.Contains(string(b), `"pattern": "boom"`) {
		t.Errorf("settings file %s", b)
	}

	a.page = pageWorkspace
	tt.Frame()
	if c := cn.tv.snap.Lines[0].Cells[6]; c.FG != vt.Indexed(5) {
		t.Errorf("the rule's match drawn %+v", c)
	}

	s := a.Settings()
	s.Highlight.PresetsOff = []string{"errors"}
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if c := cn.tv.snap.Lines[0].Cells[0]; !c.FG.IsDefault() {
		t.Errorf("errors off, drawn %+v", c)
	}
	s = a.Settings()
	s.Highlight.On = false
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if c := cn.tv.snap.Lines[0].Cells[6]; !c.FG.IsDefault() || a.highlighter() != nil {
		t.Errorf("off, drawn %+v", c)
	}
}
