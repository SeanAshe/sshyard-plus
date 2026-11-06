package app

import (
	"strings"

	"github.com/Licoy/sshyard/internal/aibrands"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func brandImage(c *ui.Context, icon *aibrands.Icon, size float32) {
	bitmap := icon.Light
	if currentTokens(c).Dark {
		bitmap = icon.Dark
	}
	ui.Image(c, bitmap()).Label(icon.Name).Size(size, size).Shrink(0)
}

func providerFormatControl(c *ui.Context, m locales.Messages, selected *int) ui.Element {
	labels := []string{m.AiFormatChat(), m.AiFormatResponses(), m.AiFormatAnthropic(), m.AiFormatTypeSafe()}
	return segmentedControl(c, selected, false, labels, theme.Space(2), func(i int, col ui.Color) {
		brandImage(c, aibrands.Provider(settings.AIProvider{Format: providerFormats[i]}), 14)
		ui.Text(c, labels[i]).FontSize(theme.Footnote).FontWeight(500).TextColor(col).SingleLine()
	}).Label(m.AiFormat())
}

// brandSelect keeps the native select's pointer, keyboard and accessibility behavior.
func brandSelect(c *ui.Context, selected *string, options []string, brand func(string) *aibrands.Icon) ui.Element {
	tok := currentTokens(c)
	return formSelectContent(c, selected, options, func(label string) {
		if icon := brand(label); icon != nil {
			brandImage(c, icon, 16)
		}
		ui.Text(c, label).FontSize(theme.Body).TextColor(tok.Text).SingleLine().Grow(1).Shrink(1).MinWidth(0)
	})
}

func brandAutocomplete(c *ui.Context, value *string, models []string, provider settings.AIProvider) ui.Element {
	tok := currentTokens(c)
	cb := ui.ComboboxBase(c, value)
	in := cb.Input.Padding(theme.Space(1.5), theme.Space(3.5)).Radius(theme.RadiusControl).Background(tok.Fill).Border(1, tok.Line).TextColor(tok.Text)
	cb.Popup(func(panel ui.Element) {
		panel.Padding(theme.Space(1)).Radius(theme.RadiusCard).Background(tok.Inset).Border(1, tok.Line)
		gutterScroll(c, ui.Scroll(c).MaxHeight(240)).Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				for _, model := range models {
					if cb.Filtering() && !strings.Contains(strings.ToLower(model), strings.ToLower(strings.TrimSpace(*value))) {
						continue
					}
					c.Key(model)
					item := formPointer(cb.Item(model)).Padding(theme.Space(1.5), theme.Space(2)).Gap(theme.Space(1.5)).Radius(theme.RadiusSmall)
					if item.Highlighted() {
						item.Background(tok.Press)
					}
					item.Children(func() {
						brandImage(c, aibrands.Model(model, provider), 16)
						ui.Text(c, model).FontSize(theme.Body).TextColor(tok.Text).SingleLine()
					})
				}
			})
		})
	})
	if chosen, ok := cb.Chosen(); ok {
		*value = chosen
		in.SetChanged()
	}
	return in
}
