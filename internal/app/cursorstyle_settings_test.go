package app

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestTerminalCursorStyleSettings(t *testing.T) {
	a, tt := newTestApp(t)
	a.openSettings(prefTerminal)
	tt.Frame()
	m := locales.In("en")
	names := []string{m.CursorBlock(), m.CursorBar(), m.CursorUnderline()}
	codes := []string{settings.CursorBlock, settings.CursorBar, settings.CursorUnderline}
	for _, next := range []int{1, 2, 0} {
		current := names[slices.Index(codes, a.Settings().TerminalCursorStyle)]
		scrollToSetting(t, tt, current)
		mustClick(t, tt, current)
		tt.Frame()
		mustClick(t, tt, names[next])
		tt.Frame()
		if got := a.Settings().TerminalCursorStyle; got != codes[next] {
			t.Fatalf("live cursor = %q, want %q", got, codes[next])
		}
		saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json"))
		if err != nil || saved.TerminalCursorStyle != codes[next] {
			t.Fatalf("saved cursor = %q, want %q, error %v", saved.TerminalCursorStyle, codes[next], err)
		}
	}
}

// scrollToSetting scrolls the settings so the control labelled label,
// and the popup it opens, sit inside the 1200 x 760 window for clicking.
func scrollToSetting(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	r := mustFind(t, tt, label)
	if r.Y < 160 || r.Y+r.H > 560 {
		tt.Scroll(900, 600, 0, r.Y-360)
		tt.Advance(300 * time.Millisecond)
		tt.Frame()
	}
}
