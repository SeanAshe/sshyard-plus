package app

import (
	"strconv"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func (a *App) effectiveBackground() settings.Background {
	if a.background.preview != nil {
		return *a.background.preview
	}
	return a.settings.Background
}

// Preview changes also rebuild idle editor windows; repeated frames do not.
func (a *App) setBackgroundPreview(next *settings.Background) {
	old := a.background.preview
	if old == nil && next == nil || old != nil && next != nil && *old == *next {
		return
	}
	if next == nil {
		a.background.preview = nil
	} else {
		copy := *next
		a.background.preview = &copy
	}
	a.invalidate()
}

func (a *App) backgroundSection(c *ui.Context, m locales.Messages, tok theme.Tokens, s *settings.Settings, changed *bool) {
	a.settingsSection(c, tok, icons.Image, tok.TintAt(2), m.BackgroundSection(), func(g *settingsGroup) {
		label := m.BackgroundChoose()
		if s.Background.Image != "" {
			label = m.BackgroundReplace()
		}
		note := m.BackgroundNote()
		if a.background.busy {
			note = m.BackgroundBusy()
		}
		g.row(m.BackgroundSection(), note, func() {
			if (IconButton{Icon: icons.Image, Label: label, Disabled: a.background.busy}).Clicked(c, "background:choose") {
				a.pickBackground(m)
			}
			if s.Background.Image != "" || a.background.err != nil {
				if (IconButton{Icon: icons.Delete, Label: m.BackgroundRemove(), Danger: true, Disabled: a.background.busy}).Clicked(c, "background:remove") {
					a.removeBackground(m)
					// Removal saves immediately; keep this frame's other settings current.
					s.Background = a.settings.Background
				}
			}
		})
		if a.background.err != nil {
			ui.Text(c, a.background.err.Error()).FontSize(theme.Footnote).TextColor(tok.Danger).Padding(theme.Space(3)).FillWidth()
		}
		if !a.hasBackground() {
			return
		}
		bg := a.effectiveBackground()
		fit := ui.Cover
		if bg.Fit == "contain" {
			fit = ui.Contain
		}
		ui.Image(c, a.background.bmp).Label(m.BackgroundPreview()).Fit(fit).Height(theme.Space(24)).FillWidth().Radius(theme.RadiusControl)
		g.row(m.BackgroundFit(), "", func() {
			idx := 0
			if bg.Fit == "contain" {
				idx = 1
			}
			if formSegmented(c.Key("background:fit"), &idx, a.background.busy, m.BackgroundCover(), m.BackgroundContain()).Label(m.BackgroundFit()).Changed() {
				bg.Fit = []string{"cover", "contain"}[idx]
				s.Background, *changed = bg, true
				a.setBackgroundPreview(nil)
			}
		})
		held := a.backgroundOpacity(c, m.BackgroundChromeOpacity(), "background:chrome", &bg.ChromeOpacity, tok)
		held = a.backgroundOpacity(c, m.BackgroundContentOpacity(), "background:content", &bg.ContentOpacity, tok) || held
		if bg != a.effectiveBackground() {
			a.setBackgroundPreview(&bg)
		}
		if a.background.preview != nil && !held {
			s.Background, *changed = *a.background.preview, true
			a.setBackgroundPreview(nil)
		}
	})
}

// Keep sliders on their own line so long translations fit narrow windows.
func (a *App) backgroundOpacity(c *ui.Context, label, key string, value *int, tok theme.Tokens) bool {
	held := false
	ui.Column(c).Padding(theme.Space(3), theme.Space(3.5)).Gap(theme.Space(2)).FillWidth().Children(func() {
		ui.Row(c).Gap(theme.Space(2)).FillWidth().Children(func() {
			ui.Text(c, label).FontSize(theme.Body).Grow(1).Shrink(1)
			ui.Text(c, strconv.Itoa(*value)+"%").FontSize(theme.Footnote).TextColor(tok.TextMuted)
		})
		v := float64(*value)
		slider := ui.Slider(c.Key(key), &v, 0, 100).Label(label).FillWidth().Disabled(a.background.busy)
		if !slider.IsDisabled() {
			slider.Cursor(ui.CursorPointer)
		}
		if slider.Changed() {
			*value = int(v + .5)
		}
		held = slider.Pressed() || slider.Dragging()
	})
	return held
}
