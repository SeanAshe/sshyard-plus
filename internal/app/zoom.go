package app

import (
	"math"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// The interface zooms the whole window's content (ui.Context.SetZoom):
// laid out smaller and drawn larger at the screen's resolution, so text,
// icons and the terminal's cells stay sharp. The zoom is the one chosen
// in the settings, or follows the system: the text size Windows and
// GNOME let the user set, which the app's own type scale would otherwise
// ignore, and on X11 the part of the desktop's scale that the toolkit's
// whole scale leaves out (150% shows as 1×).

// zoomState is what the zoom follows besides the settings.
type zoomState struct {
	// desktop is the scale the X11 desktop asks for (Xft.dpi), 0 where
	// it says none; device is the window's scale, as its last frame drew;
	// text is the toolkit's own text scale (GDK_DPI_SCALE), 0 for none.
	desktop, device, text float32
	// now is the main window's zoom in effect, which the shortcuts step
	// from.
	now float32
}

// zoomFor is the zoom of a window: the one chosen (percent, 0 for the
// system's), else the system's text size and the desktop's scale the
// window's misses.
func zoomFor(chosen int, textScale, desktop, device float32) float32 {
	if chosen > 0 {
		return float32(chosen) / 100
	}
	z := textScale
	if z <= 0 {
		z = 1
	}
	if desktop > 0 && device > 0 && desktop/device > 1.05 {
		z *= desktop / device
	}
	// In steps of 5%, so a desktop's 1.4999 does not blur the grid.
	z = float32(math.Round(float64(z)*20) / 20)
	return max(0.5, min(z, 3))
}

// applyZoom zooms the content of the window c builds.
func (a *App) applyZoom(c *ui.Context, main bool) {
	text := c.Preferences().TextScale
	if a.zoom.text > 0 {
		text *= a.zoom.text
	}
	z := zoomFor(a.settings.UIZoom, text, a.zoom.desktop, a.zoom.device)
	c.SetZoom(z)
	if main {
		a.zoom.now = z
	}
	if a.zoom.desktop > 0 {
		c.Root().Draw(func(p *ui.Painter, _ ui.Rect) { a.zoom.device = p.Scale() / z })
	}
}

// probeDesktop learns the desktop's scale off the UI thread, where the
// window's does not tell it all (Linux).
func (a *App) probeDesktop() {
	if runtime.GOOS != "linux" {
		return
	}
	go func() {
		scale, text := desktopScale()
		a.post(func() { a.zoom.desktop, a.zoom.text = scale, text })
	}()
}

// stepZoom sets the zoom to the next of settings.UIZooms larger (dir > 0)
// or smaller than the one in effect, or back to following the system (dir
// 0), and says so.
func (a *App) stepZoom(dir int) {
	s := a.settings
	switch cur := int(math.Round(float64(a.zoom.now) * 100)); {
	case dir == 0:
		s.UIZoom = 0
	case dir > 0:
		i := slices.IndexFunc(settings.UIZooms, func(z int) bool { return z > cur })
		if i < 0 {
			return
		}
		s.UIZoom = settings.UIZooms[i]
	default:
		i := slices.IndexFunc(settings.UIZooms, func(z int) bool { return z >= cur })
		if i == 0 {
			return
		}
		if i < 0 {
			i = len(settings.UIZooms)
		}
		s.UIZoom = settings.UIZooms[i-1]
	}
	if s.UIZoom == a.settings.UIZoom {
		return
	}
	if err := a.ApplySettings(s); err != nil {
		a.toastError(err.Error())
		return
	}
	a.toast(func(m locales.Messages) ui.Toast {
		return ui.Toast{Title: m.ZoomNow(zoomLabel(m, s.UIZoom))}
	})
}

// stepTermFont makes the terminals' font n points larger (smaller when n
// is negative), within the settings' bounds, and says so in one toast
// that each step changes.
func (a *App) stepTermFont(n int) {
	s := a.settings
	s.TerminalFontSize = min(max(s.TerminalFontSize+n, settings.MinFontSize), settings.MaxFontSize)
	if s.TerminalFontSize == a.settings.TerminalFontSize {
		return
	}
	if err := a.ApplySettings(s); err != nil {
		a.toastError(err.Error())
		return
	}
	a.toast(func(m locales.Messages) ui.Toast {
		return ui.Toast{ID: "termfont", Title: m.FontSizeNow(strconv.Itoa(s.TerminalFontSize))}
	})
}

// zoomLabel names a zoom of the settings: "125%", or following the system.
func zoomLabel(m locales.Messages, z int) string {
	if z == 0 {
		return m.ZoomSystem()
	}
	return strconv.Itoa(z) + "%"
}

// parseXftDPI reads Xft.dpi from what xrdb -query prints, as a scale of
// 96 DPI; 0 when it is not there.
func parseXftDPI(out string) float32 {
	for line := range strings.Lines(out) {
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(k) != "Xft.dpi" {
			continue
		}
		dpi, err := strconv.ParseFloat(strings.TrimSpace(v), 32)
		if err != nil || dpi < 48 || dpi > 960 {
			return 0
		}
		return float32(dpi / 96)
	}
	return 0
}
