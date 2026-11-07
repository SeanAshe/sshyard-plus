package app

import (
	"path/filepath"
	"testing"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
)

func TestCloseBehaviorSettings(t *testing.T) {
	a, tt := newTestApp(t)
	a.openSettings(prefAppearance)
	tt.Frame()
	m := locales.In("en")
	for _, choice := range []struct{ label, behavior string }{{m.CloseQuit(), settings.CloseQuit}, {m.CloseToTray(), settings.CloseToTray}} {
		current := m.CloseToTray()
		if a.Settings().CloseBehavior == settings.CloseQuit {
			current = m.CloseQuit()
		}
		mustClick(t, tt, current)
		tt.Frame()
		mustClick(t, tt, choice.label)
		tt.Frame()
		if a.Settings().CloseBehavior != choice.behavior {
			t.Fatalf("live close behavior %q, want %q", a.Settings().CloseBehavior, choice.behavior)
		}
		saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json"))
		if err != nil || saved.CloseBehavior != choice.behavior {
			t.Fatalf("saved close behavior %q, want %q, error %v", saved.CloseBehavior, choice.behavior, err)
		}
	}
}
