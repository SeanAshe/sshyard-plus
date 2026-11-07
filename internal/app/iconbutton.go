package app

import (
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/egoist/mygo/ui"
)

// IconButton is the app's one icon-only button: every action shown as an
// icon (toolbar, tab, pane header, sidebar row, files toolbar) is one, and
// a text Button is kept only for the primary actions of a dialog or form.
// Label is required and localized: it is both the tooltip and the name
// assistive technology reads. The look follows the theme in use (light or
// dark): a circle, clear and muted at rest; a quiet tint of the brand
// accent (#1984FC) with the icon in the accent on hover, press and while
// Active; MyGo's focus ring in the accent's focus color from the keyboard.
type IconButton struct {
	Icon *icons.Icon
	// On is the icon while Active, as the "-fill" of a "-line" icon; nil
	// keeps Icon.
	On *icons.Icon
	// Label names the action, from the locale catalog.
	Label string
	// InstantTooltip shows a tip immediately on hover or keyboard focus.
	InstantTooltip bool
	// Active shows the button on: a toggle that is set, a view that shows.
	Active bool
	// Toggle reports Active to assistive technology as a toggle's state.
	Toggle bool
	// Disabled dims the button; it reports no clicks.
	Disabled bool
	// Danger tints the hover red, for a destructive action.
	Danger bool
	// Prominent keeps the accent (or danger tint) visible at rest.
	Prominent bool
	// Size is the icon's size; 0 is 16. Small is 14, for dense rows, Tiny
	// 12, for pane headers and the files toolbar.
	Size float32
	// Badge is a count shown on the button's corner, as transfers to go;
	// "" shows none. BadgeDanger draws it red, for failures.
	Badge       string
	BadgeDanger bool
}

// Small is the icon size of IconButtons in dense rows (tabs, sidebar
// rows), Tiny in the slimmest (pane headers, the files toolbar).
const (
	Small = 14
	Tiny  = 12
)

// Show builds the button under key and returns it, for Clicked.
func (b IconButton) Show(c *ui.Context, key string) ui.Element {
	th := c.Theme()
	size := b.Size
	if size == 0 {
		size = 16
	}
	// 28 DIPs round for the 16 icon, 24 for the small one, 20 for the
	// tiny one: a circle.
	pad := float32(6)
	switch {
	case size <= Tiny:
		pad = 4
	case size <= Small:
		pad = 5
	}
	el := ui.ButtonBase(c.Key("ib:" + key)).Label(b.Label).Padding(pad).Radius(theme.Capsule).Disabled(b.Disabled)
	if !b.InstantTooltip {
		el.Tooltip(b.Label)
	}
	if !el.IsDisabled() {
		el.Cursor(ui.CursorPointer)
	}
	if b.Toggle {
		el.Checked(b.Active)
	}
	strong := float32(0.12)
	if th.Dark {
		strong = 0.22
	}
	hue := th.Accent
	if b.Danger {
		hue = th.Danger
	}
	fg := th.TextMuted
	if b.Prominent {
		fg = hue
	}
	switch {
	case b.Disabled:
		el.Opacity(0.4)
	case el.Pressed():
		el.Background(hue.Alpha(strong * 1.8))
		fg = hue
	case el.Hovered():
		el.Background(hue.Alpha(strong))
		fg = hue
	case b.Active:
		el.Background(th.Accent.Alpha(strong))
		fg = th.Accent
	}
	if b.Active && !b.Disabled {
		fg = th.Accent
	}
	ic := b.Icon
	if b.Active && b.On != nil {
		ic = b.On
	}
	el.Children(func() {
		ui.Icon(c, ic.SVG).Width(size).Height(size).TextColor(fg)
		if b.Badge != "" {
			bg := th.Accent
			if b.BadgeDanger {
				bg = th.Danger
			}
			ui.Box(c).Absolute().Top(-4).Right(-5).Height(15).MinWidth(15).Padding(0, 4).Radius(theme.Capsule).Background(bg).Center().Children(func() {
				ui.Text(c, b.Badge).FontSize(theme.Caption).FontWeight(600).TextColor(ui.Hex("#FFFFFF")).SingleLine()
			})
		}
	})
	if b.InstantTooltip && !b.Disabled {
		instantIconTooltip(c, el, b.Label, key)
	}
	return el
}

// Clicked reports whether an enabled button was clicked; it saves callers
// from checking Disabled again.
func (b IconButton) Clicked(c *ui.Context, key string) bool {
	el := b.Show(c, key)
	return el.Clicked() && !b.Disabled
}

// iconButton is the common case: an icon and its label.
func iconButton(c *ui.Context, key string, ic *icons.Icon, label string) bool {
	return IconButton{Icon: ic, Label: label}.Clicked(c, key)
}
