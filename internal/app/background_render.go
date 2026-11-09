package app

import (
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/egoist/mygo/ui"
)

// drawBackground paints one image in window coordinates, before all regions.
// Regions own their tint; pane backgrounds replace it rather than multiplying it.
func (a *App) drawBackground(c *ui.Context, tok theme.Tokens) {
	if !a.hasBackground() {
		return
	}
	z := c.Zoom()
	c.Root().Background(tok.Canvas).Draw(func(p *ui.Painter, r ui.Rect) {
		if a.zoom.desktop > 0 {
			a.zoom.device = p.Scale() / z
		}
		a.paintBackground(p, r, tok)
	})
}

func (a *App) chromeFill(col ui.Color) ui.Color {
	if a.hasBackground() {
		col.A = 255
		return col.Alpha(float32(a.effectiveBackground().ChromeOpacity) / 100)
	}
	return col
}

func (a *App) backgroundContentFill(col ui.Color) ui.Color {
	if a.hasBackground() {
		col.A = 255
		return col.Alpha(float32(a.effectiveBackground().ContentOpacity) / 100)
	}
	return col
}

func (a *App) contentRegionFill(col ui.Color) ui.Color {
	if a.hasBackground() {
		return a.backgroundContentFill(col)
	}
	return ui.Transparent
}

func (a *App) editorWindowFill(col ui.Color) ui.Color {
	if a.hasBackground() {
		return a.backgroundContentFill(col)
	}
	return ui.Transparent
}

func (a *App) editorChromeFill(col ui.Color) ui.Color {
	if a.hasBackground() {
		return a.chromeFill(col)
	}
	return ui.Transparent
}

// contentSurfaceFill leaves the owning content region as the only image tint;
// cards must not hide the image or compound that region's configured opacity.
// Controls and overlays retain their own readable fills.
func (a *App) contentSurfaceFill(col ui.Color) ui.Color {
	if a.hasBackground() || a.clearContent {
		return ui.Transparent
	}
	return col
}

func (a *App) contentCard(c *ui.Context, tok theme.Tokens) ui.Element {
	return card(c, tok).Background(a.contentSurfaceFill(tok.Inset))
}

// paintBackground resets even alpha images and contain letterboxes to the same
// opaque canvas before drawing the image in full-window coordinates.
func (a *App) paintBackground(p *ui.Painter, window ui.Rect, tok theme.Tokens) {
	fit := ui.Cover
	if a.effectiveBackground().Fit == "contain" {
		fit = ui.Contain
	}
	p.Fill(window, tok.Canvas, 0)
	p.Image(a.background.bmp, window, fit)
}

// paneBackground replaces the region's tint inside the card, preserving each
// pane's palette without compounding ContentOpacity with the region beneath it.
func (a *App) paneBackground(c *ui.Context, pane ui.Element, tok theme.Tokens, col ui.Color) {
	if !a.hasBackground() {
		return
	}
	w, h := c.Size()
	window := ui.Rect{W: w, H: h}
	pane.Background(ui.Transparent).Draw(func(p *ui.Painter, r ui.Rect) {
		p.Clip(r, theme.RadiusCard, func() {
			a.paintBackground(p, window, tok)
			p.Fill(r, a.backgroundContentFill(col), 0)
		})
	})
}

// backgroundSeparator gives a standalone edge the same image tint as its band.
func (a *App) backgroundSeparator(c *ui.Context, edge, fill ui.Color) ui.Element {
	el := ui.Box(c).Background(edge)
	if a.hasBackground() {
		el.Background(fill).Draw(func(p *ui.Painter, r ui.Rect) { p.Fill(r, edge, 0) })
	}
	return el
}
