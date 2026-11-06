package app

import (
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/egoist/mygo/ui"
)

// quietControl shares the quiet pill's fill without fading its text.
func quietControl(c *ui.Context, el ui.Element) ui.Element {
	tok := currentTokens(c)
	formPointer(el).Radius(theme.Capsule).TextColor(tok.Text)
	switch {
	case el.IsDisabled():
		el.Background(tok.Fill)
	case el.Pressed():
		el.Background(tok.Text.Alpha(0.14))
	case el.Hovered():
		el.Background(tok.Text.Alpha(0.10))
	default:
		el.Background(tok.Fill)
	}
	return el
}

func formSelect(c *ui.Context, selected *string, options []string) ui.Element {
	return formSelectContent(c, selected, options, func(label string) {
		ui.Text(c, label).FontSize(theme.Body).TextColor(currentTokens(c).Text).SingleLine().Grow(1).Shrink(1).MinWidth(0)
	})
}

// SelectBase retains the native popup, keyboard, focus and Changed semantics.
func formSelectContent(c *ui.Context, selected *string, options []string, content func(string)) ui.Element {
	tok := currentTokens(c)
	sel := ui.SelectBase(c, selected)
	quietControl(c, sel.Trigger).Padding(theme.Space(1.5), theme.Space(3.5)).Gap(theme.Space(1.5)).MinWidth(theme.Space(35)).Children(func() {
		content(*selected)
		ui.Icon(c, icons.Collapse.SVG).Size(12, 12).TextColor(tok.TextMuted).Shrink(0)
	})
	sel.Popup(func(panel ui.Element) {
		panel.Padding(theme.Space(1)).Radius(theme.RadiusCard).Background(tok.Inset).Border(1, tok.Line).MaxHeight(menuHeight)
		gutterScroll(c, ui.Scroll(c).MaxHeight(menuHeight)).Children(func() {
			ui.Column(c).FillWidth().Children(func() {
				for _, option := range options {
					c.Key(option)
					item := formDisabled(sel.Item(option), false).Padding(theme.Space(1.5), theme.Space(2)).Gap(theme.Space(1.5)).Radius(theme.RadiusSmall)
					if item.Highlighted() {
						item.Background(tok.Press)
					} else if option == *selected {
						item.Background(tok.Fill)
					}
					item.Children(func() { content(option) })
				}
			})
		})
	})
	return sel.Trigger
}

func formSwitch(c *ui.Context, on *bool) ui.Element {
	return formDisabled(ui.Switch(c, on), false)
}

func formCheckbox(c *ui.Context, checked *bool, label string) ui.Element {
	return formDisabled(ui.Checkbox(c, checked, label), false)
}

// MyGo's disabled nodes keep an explicit cursor, so set both together.
func formDisabled(el ui.Element, disabled bool) ui.Element {
	el.Disabled(disabled)
	return formPointer(el)
}

func formPointer(el ui.Element) ui.Element {
	if el.IsDisabled() {
		return el.Cursor(ui.CursorDefault)
	}
	return el.Cursor(ui.CursorPointer)
}

func formTextInput(c *ui.Context, value *string) ui.Element {
	return ui.TextInput(c, value).Background(currentTokens(c).Fill).Radius(theme.RadiusControl)
}

func formTextArea(c *ui.Context, value *string) ui.Element {
	return ui.TextArea(c, value).Background(currentTokens(c).Fill).Radius(theme.RadiusControl)
}

// formCombobox lights its options in the neutral tint the selects use,
// not MyGo's solid accent: the options take the theme's Accent and
// AccentText as they build, so those are swapped for the call; the field's
// focus border and caret read the accent as they paint, after it is back.
func formCombobox(c *ui.Context, selected *string, options []string) ui.Element {
	tok := currentTokens(c)
	th := c.Theme()
	accent, accentText := th.Accent, th.AccentText
	th.Accent, th.AccentText = tok.Press, tok.Text
	defer func() { th.Accent, th.AccentText = accent, accentText }()
	return ui.Combobox(c, selected, options).Background(tok.Fill).Radius(theme.RadiusControl)
}

func formSegmented(c *ui.Context, selected *int, disabled bool, labels ...string) ui.Element {
	return segmentedControl(c, selected, disabled, labels, theme.Space(3), func(i int, col ui.Color) {
		ui.Text(c, labels[i]).FontSize(theme.Footnote).FontWeight(500).TextColor(col).SingleLine()
	})
}

// segmentedControl is the one look of a segmented choice: a capsule track
// of the segments, the selected one raised on it. content draws segment i
// in col; padX is a segment's horizontal padding.
func segmentedControl(c *ui.Context, selected *int, disabled bool, labels []string, padX float32, content func(i int, col ui.Color)) ui.Element {
	tok := currentTokens(c)
	parts := ui.SegmentedBase(c, selected, len(labels))
	parts.Track.Disabled(disabled)
	formPointer(parts.Track).AlignItems(ui.Stretch).Padding(theme.Space(0.5)).Gap(theme.Space(0.5)).Radius(theme.Capsule).Background(tok.Fill).Children(func() {
		for i, label := range labels {
			el := formPointer(parts.Segment(i)).Label(label).Padding(theme.Space(1), padX).Gap(theme.Space(1.5)).Radius(theme.Capsule).AlignItems(ui.Center)
			col := tok.TextMuted
			switch {
			case i == *selected:
				theme.Shadow(tok.Card).On(el.Background(tok.Inset))
				col = tok.Text
			case el.Hovered() && !el.IsDisabled():
				el.Background(tok.Hover)
			}
			el.Children(func() { content(i, col) })
		}
	})
	return parts.Track
}
