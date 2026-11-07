package app

import (
	"runtime"
	"slices"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func (a *App) trayPreferences(c *ui.Context, m locales.Messages, g *settingsGroup, s *settings.Settings, changed *bool) {
	g.row(m.TrayClick(), m.TrayClickNote(), func() {
		cur := m.TrayClickPanel()
		if s.TrayClick == settings.TrayClickWindow {
			cur = m.TrayClickWindow()
		}
		if formSelect(c.Key("trayclick"), &cur, []string{m.TrayClickPanel(), m.TrayClickWindow()}).Label(m.TrayClick()).Width(180).Changed() {
			s.TrayClick = settings.TrayClickPanel
			if cur == m.TrayClickWindow() {
				s.TrayClick = settings.TrayClickWindow
			}
			*changed = true
		}
	})
	// The tray's native material support does not depend on main-window vibrancy.
	off := runtime.GOOS != "darwin"
	note := m.TrayMaterialNote()
	if off {
		note = m.MaterialUnsupported()
	}
	g.row(m.TrayMaterial(), note, func() {
		idx := max(slices.Index(materials, s.TrayMaterial), 0)
		if formSegmented(c.Key("traymaterial"), &idx, off, m.MaterialTranslucent(), m.MaterialOpaque()).Label(m.TrayMaterial()).Changed() && !off {
			s.TrayMaterial, *changed = materials[idx], true
		}
	})
}
