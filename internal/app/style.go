package app

import (
	"time"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/egoist/mygo/ui"
)

// The app's own building blocks over MyGo's native elements, after the
// design language in AGENTS.md: capsules for controls, rounded cards with
// hairlines for groups, tiles for identity.

// toolbarHeight is the height of the unified title and toolbar band.
const toolbarHeight = 40

// bandHeight is the toolbar band's height, at least what the window
// controls take.
func bandHeight(c *ui.Context) float32 {
	return max(c.TitleBar().Height, toolbarHeight)
}

// canvasAlpha and paneAlpha are how much of their fill the canvas of the
// content and the panes on it keep over the window's material when the
// content is translucent: the canvas lets most of it through, the panes,
// whose text must read, less.
const (
	canvasAlpha = 0.35
	paneAlpha   = 0.6
)

// contentFill is col, a fill of the content, as it shows: lightened to
// alpha over the window's material while the content is translucent.
func (a *App) contentFill(col ui.Color, alpha float32) ui.Color {
	if a.hasBackground() {
		if alpha == canvasAlpha {
			return ui.Transparent
		}
		return a.backgroundContentFill(col)
	}
	if !a.clearContent {
		return col
	}
	return col.Alpha(alpha)
}

// tile is a rounded square of tint holding a white icon, like the macOS
// sidebar's colored icons.
func tile(c *ui.Context, ic *icons.Icon, tint ui.Color, size float32) ui.Element {
	return ui.Box(c).Size(size, size).Shrink(0).Radius(size * 0.28).Background(tint).Center().Children(func() {
		ui.Icon(c, ic.SVG).Size(size*0.58, size*0.58).TextColor(ui.Hex("#FFFFFF"))
	})
}

// softTile is a tile tinted lightly with the icon in the tint, for rows
// that are not chosen.
func softTile(c *ui.Context, ic *icons.Icon, tint ui.Color, size float32) ui.Element {
	return ui.Box(c).Size(size, size).Shrink(0).Radius(size * 0.28).Background(tint.Alpha(0.15)).Center().Children(func() {
		ui.Icon(c, ic.SVG).Size(size*0.58, size*0.58).TextColor(tint)
	})
}

// glyph is an icon alone in one color, in the space a tile of that size
// takes.
func glyph(c *ui.Context, ic *icons.Icon, col ui.Color, size float32) ui.Element {
	return ui.Box(c).Size(size, size).Shrink(0).Center().Children(func() {
		ui.Icon(c, ic.SVG).Size(size*0.82, size*0.82).TextColor(col)
	})
}

// dot is a status dot.
func dot(c *ui.Context, col ui.Color) ui.Element {
	return ui.Box(c).Size(7, 7).Shrink(0).Radius(3.5).Background(col)
}

// card is a rounded inset card on the canvas: a group of settings, a
// pane. Its children go in fn.
func card(c *ui.Context, tok theme.Tokens) ui.Element {
	el := ui.Column(c).Radius(theme.RadiusCard).Background(tok.Inset).Border(1, tok.Line)
	return tok.Card.On(el)
}

// pill is a capsule text button: quiet (a tint of the text) or primary
// (the accent), for the actions of a dialog or a form. It returns whether
// it was clicked while enabled.
func pill(c *ui.Context, label string, primary, danger, disabled bool) bool {
	el, ok := pillEl(c, label, primary, danger, disabled)
	_ = el
	return ok
}

// pillEl is pill, and the element, so a dialog can hold focus on it.
func pillEl(c *ui.Context, label string, primary, danger, disabled bool) (ui.Element, bool) {
	tok := currentTokens(c)
	b := ui.ButtonBase(c.Key("pill:"+label)).Label(label).Padding(6, 14).Radius(theme.Capsule).Disabled(disabled).
		AlignItems(ui.Center).Justify(ui.Center).MinWidth(72)
	fg := tok.Text
	switch {
	case primary:
		fg = tok.AccentText
		switch {
		case b.Pressed():
			b.Background(tok.AccentPressed)
		case b.Hovered():
			b.Background(tok.AccentHover)
		default:
			b.Background(tok.Accent)
		}
	default:
		if danger {
			fg = tok.Danger
		}
		switch {
		case b.Pressed():
			b.Background(tok.Text.Alpha(0.14))
		case b.Hovered():
			b.Background(tok.Text.Alpha(0.10))
		default:
			b.Background(tok.Fill)
		}
	}
	if disabled {
		b.Opacity(0.45)
	} else {
		b.Cursor(ui.CursorPointer)
	}
	b.Children(func() { ui.Text(c, label).FontSize(theme.Body).FontWeight(500).TextColor(fg).SingleLine() })
	return b, b.Clicked() && !disabled
}

// currentTokens are the tokens of the theme in use.
func currentTokens(c *ui.Context) theme.Tokens {
	if c.Theme().Dark {
		return theme.DarkTokens()
	}
	return theme.LightTokens()
}

// sheetTitle is the title of a dialog or sheet.
func sheetTitle(c *ui.Context, s string) {
	ui.Text(c, s).FontSize(theme.Title2).FontWeight(600)
}

// sheet styles the panel MyGo's Modal returns as a sheet: the larger
// radius, a hairline and a lifted shadow.
func sheet(el ui.Element, tok theme.Tokens) ui.Element {
	el.Radius(theme.RadiusSheet).Background(tok.Inset).Border(1, tok.GlassEdge).Padding(theme.Space(6))
	return tok.Lift.On(el)
}

// stickyModal is ui.Modal for a sheet holding typing or choices worth
// keeping: a click on the dimmed window around it, easily a slip, leaves
// it open, while Escape and its own buttons still close it.
func stickyModal(c *ui.Context, open *bool, fn func()) ui.Element {
	t := c.Theme()
	w, h := c.Size()
	missed := false
	panel := ui.DialogBase(c, open, func(back, panel ui.Element) {
		missed = back.Clicked()
		// Modal's own look.
		back.Background(ui.RGBA(0, 0, 0, 0.4))
		panel.Padding(t.Space(5)).Gap(t.Space(3)).Radius(t.Space(2.5)).Background(t.Background).
			MaxWidth(w - t.Space(10)).MaxHeight(h - t.Space(10))
		panel.Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.3))
		fn()
	})
	if missed {
		*open = true
	}
	return panel
}

// scrollGutter is the room a vertical Scroll keeps at its end for its
// scroll bar, which MyGo draws over the content (the theme's 8 DIPs, 3 in
// from the edge) and some space before it: the bar never covers or
// crowds a field's border, a pill or a row's trailing button. In a sheet
// it sits about midway between the content and the sheet's edge.
var scrollGutter = theme.Space(4.5)

// sheetScroll lets a vertical Scroll reach scrollGutter into the padding
// of the column holding it, as a sheet's, and keeps its scroll bar there:
// the content stays as wide as the column, in line with the title and the
// buttons. The column must have that much padding at its end.
func sheetScroll(el ui.Element) ui.Element {
	return el.AlignSelf(ui.Stretch).MarginEnd(-scrollGutter).PaddingEnd(scrollGutter)
}

// scrollGutterKey keys the state gutterScroll keeps on a Scroll.
type scrollGutterKey struct{}

// gutterScroll keeps the content of a vertical Scroll with no padding to
// spare, as a menu's, clear of its scroll bar while it has one: while the
// content overflowed in the last frame. A Scroll that just appeared asks
// for a second frame, which knows.
func gutterScroll(c *ui.Context, el ui.Element) ui.Element {
	st := ui.Local(el, scrollGutterKey{}, func() *ui.ScrollState {
		c.AnimationFrame()
		return &ui.ScrollState{}
	})
	el.TrackScroll(*st)
	if (*st).MaxY > 0 {
		el.PaddingEnd(scrollGutter)
	}
	return el
}

// focusHold keeps the keyboard on a dialog's first field for a moment
// after the dialog opens (or shows a new error), whatever else claims the
// focus as the overlay settles: the field that had it, the window coming
// to the front. AutoFocus alone acts only in the frame the field is born,
// which may come before the window is ready for keys; the password prompt
// then took no typing until it was clicked.
type focusHold struct {
	key   any
	until time.Time
}

const focusHoldFor = 600 * time.Millisecond

// apply focuses in while key is new or the hold lasts.
func (h *focusHold) apply(c *ui.Context, key any, in ui.Element) {
	now := c.Now()
	if key != h.key {
		h.key, h.until = key, now.Add(focusHoldFor)
	}
	if now.Before(h.until) {
		if !in.Focused() {
			in.Focus()
		}
		c.After(50 * time.Millisecond)
	}
}
