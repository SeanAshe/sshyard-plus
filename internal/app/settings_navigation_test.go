package app

import (
	"testing"

	"github.com/Licoy/sshyard/locales"
)

func TestSettingsExitNavigation(t *testing.T) {
	a, tt := newTestApp(t)
	tt.Frame()
	m := locales.In("en")
	mustClick(t, tt, m.Settings())
	mustClick(t, tt, m.TerminalSection())
	mustClick(t, tt, m.Back())
	if a.page != pageWorkspace {
		t.Fatal("back did not restore the workspace")
	}
	mustClick(t, tt, m.Settings())
	if a.prefNow() != prefTerminal {
		t.Fatal("reopening settings lost the selected page")
	}
	mustClick(t, tt, m.CloseSettings())
	if a.page != pageWorkspace {
		t.Fatal("close did not restore the workspace")
	}
}
