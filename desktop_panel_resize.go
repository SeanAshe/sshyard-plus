package main

import (
	"log"
	"math"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type desktopPanelResize struct {
	start, pointerY, delta                 int
	x, y, bottom, chrome, width, available int
	screenHeight                           int
	top, dragging, moved                   bool
	revision                               uint64
}

func (d *desktop) panelResizeHandle(c *ui.Context) {
	s := &d.panelResize
	grip := ui.Box(c).Absolute().Left(0).Right(0).Height(theme.Space(2)).Cursor(ui.CursorResizeNS).Role(ui.RoleNone)
	if s.top {
		grip.Top(0)
	} else {
		grip.Bottom(0)
	}
	_, _, held := grip.Dragged()
	pointerY := mygo.Screen.CursorScreenPoint().Y
	if held {
		if !s.dragging {
			d.beginPanelResize(pointerY)
		}
		if delta := pointerY - s.pointerY; delta != s.delta {
			s.delta, s.moved = delta, true
			d.resizePanelHeight(false)
		}
	} else if s.dragging {
		s.dragging = false
		s.delta = pointerY - s.pointerY
		if s.moved || s.delta != 0 {
			d.resizePanelHeight(true)
		}
	}
	color := ui.Transparent
	if grip.Hovered() || held {
		color = c.Theme().TextMuted.Alpha(.55)
	}
	grip.AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		ui.Box(c).Size(theme.Space(9), theme.Space(.75)).Radius(theme.RadiusSmall).Background(color)
	})
}

func (d *desktop) beginPanelResize(pointerY int) {
	if d.panel == nil {
		return
	}
	s := &d.panelResize
	bounds := d.panel.Bounds()
	width, height := d.panelContentSize()
	display := mygo.Screen.DisplayMatching(bounds)
	area := display.WorkArea
	*s = desktopPanelResize{
		start: height, pointerY: pointerY, width: width,
		dragging: true, top: s.top, revision: s.revision,
		x: bounds.X, y: bounds.Y, bottom: bounds.Y + bounds.Height,
		chrome: max(0, bounds.Height-height), screenHeight: display.Bounds.Height,
	}
	s.available = max(1, area.Y+area.Height-bounds.Y-s.chrome)
	if s.top {
		s.available = max(1, s.bottom-area.Y-s.chrome)
	}
}

// Pick the edge opposite the tray; Linux has no icon bounds and uses bottom.
func (d *desktop) syncPanelResizeEdge() {
	if d.panel == nil {
		return
	}
	icon := d.tray.Bounds()
	if icon.Width == 0 && icon.Height == 0 {
		return
	}
	bounds := d.panel.Bounds()
	d.panelResize.top = bounds.Y+bounds.Height/2 < icon.Y+icon.Height/2
}

func (d *desktop) resizePanelHeight(persist bool) {
	s := &d.panelResize
	growth := s.delta
	if s.top {
		growth = -growth
	}
	height := min(max(s.start+growth, settings.MinTrayHeight), settings.MaxTrayHeight)
	limit := panelHeightLimit(s.screenHeight, s.available)
	// The saved preference keeps its data minimum; a small display may need
	// a temporarily shorter window to stay entirely inside its work area.
	height = min(height, max(1, limit))
	x, y, width := s.x, s.y, s.width
	if s.top {
		y = s.bottom - s.chrome - height
	}
	s.revision++
	panel, revision := d.panel, s.revision
	mygo.App.WhenReady(func() {
		if panel == nil || d.panel != panel || panel.IsDestroyed() {
			return
		}
		if d.panelResize.revision == revision {
			panel.SetContentSize(width, height)
			panel.SetPosition(x, y)
		}
		if persist {
			d.savePanelHeight(height)
		}
	})
}

func (d *desktop) savePanelHeight(height int) {
	s := d.app.Settings()
	s.TrayHeight = settings.ClampTrayHeight(height)
	if err := d.app.ApplySettings(s); err != nil {
		log.Printf("save tray panel height: %v", err)
		d.reportError(locales.ErrGeneric(err.Error()))
	}
}

func panelHeightLimit(screenHeight, available int) int {
	limit := min(settings.MaxTrayHeight, max(1, available-int(theme.Space(4))))
	if screenHeight > 0 {
		limit = min(limit, max(1, int(math.Floor(float64(screenHeight)*.75))))
	}
	return limit
}

func (d *desktop) initialPanelHeight() int {
	display := mygo.Screen.PrimaryDisplay()
	icon := d.tray.Bounds()
	anchored := icon.Width > 0 && icon.Height > 0
	if anchored {
		display = mygo.Screen.DisplayMatching(icon)
	}
	area := display.WorkArea
	available := area.Height
	if anchored {
		available = max(area.Y+area.Height-icon.Y-icon.Height, icon.Y-area.Y)
	}
	return min(settings.ClampTrayHeight(d.app.Settings().TrayHeight), panelHeightLimit(display.Bounds.Height, available))
}
