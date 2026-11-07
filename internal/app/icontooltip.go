package app

import (
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/egoist/mygo/ui"
)

// instantIconTooltip uses an overlay instead of MyGo's fixed 600ms delay.
func instantIconTooltip(c *ui.Context, anchor ui.Element, label, key string) {
	if label == "" || anchor.Pressed() || !(anchor.Hovered() || anchor.FocusVisible()) {
		return
	}
	tok := currentTokens(c)
	width, _ := c.Size()
	ui.Overlay(c, func() {
		// AttachTo resolves against the window, flips to the other side when
		// needed and clamps along the anchor, including the rightmost button.
		tip := ui.Box(c.Key("instant-tip:"+key)).AttachTo(anchor, ui.AnchorBottomEnd, ui.AnchorTopEnd).
			Margin(theme.Space(1), 0, 0, 0).MaxWidth(max(0, width-theme.Space(4))).
			Padding(theme.Space(1), theme.Space(2)).Radius(theme.RadiusSmall).
			Background(tok.Inset).Border(1, tok.GlassEdge).TextColor(tok.Text).
			PassThrough().Role(ui.RoleTooltip)
		tok.Lift.On(tip)
		tip.Children(func() { ui.Text(c, label).FontSize(theme.Footnote) })
	})
}
