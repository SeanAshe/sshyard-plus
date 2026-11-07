package app

import (
	"testing"

	"github.com/Licoy/sshyard/locales"
)

func TestSidebarResizeAndHide(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	y := float32(400)

	// Dragging the line beside the sidebar sizes it, within its bounds,
	// and the width is saved once it rests.
	x := a.sbWidth + 0.5
	tt.Press(x, y)
	tt.Move(x+100, y)
	tt.Release(x+100, y)
	tt.Frame()
	if a.sbWidth != 340 {
		t.Fatalf("width = %v, want 340", a.sbWidth)
	}
	x = a.sbWidth + 0.5
	tt.Press(x, y)
	tt.Move(x+400, y)
	tt.Release(x+400, y)
	tt.Frame()
	tt.Frame()
	if a.sbWidth != sidebarMax {
		t.Fatalf("width = %v, want %v", a.sbWidth, sidebarMax)
	}
	tt.Advance(splitRest)
	tt.Frame()
	if a.settings.SidebarWidth != sidebarMax {
		t.Fatalf("saved width = %v, want %v", a.settings.SidebarWidth, sidebarMax)
	}

	// The sidebar's foot hides it; the status bar's first button shows it.
	mustClick(t, tt, m.HideSidebar())
	tt.Frame()
	if !a.settings.SidebarHidden {
		t.Fatal("sidebar not hidden")
	}

	// The settings' pages keep their sidebar.
	a.openSettings(a.prefNow())
	tt.Frame()
	if a.sidebarHidden() {
		t.Fatal("settings hid their sidebar")
	}
	a.closeSettings()
	tt.Frame()

	mustClick(t, tt, m.ShowSidebar())
	tt.Frame()
	if a.settings.SidebarHidden {
		t.Fatal("sidebar not shown again")
	}
	if a.sbWidth != sidebarMax {
		t.Fatalf("width after showing = %v, want %v", a.sbWidth, sidebarMax)
	}
}
