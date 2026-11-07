//go:build darwin

package main

import (
	"fmt"
	"structs"

	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
)

// These monitors live only as long as the main panel. Local events cover
// other windows in this app, global events cover the desktop and other apps.
func (d *desktop) watchPanelDismiss() error {
	if d.panelNative.popover == 0 {
		return fmt.Errorf("native tray popover was not captured")
	}
	panel := d.panel
	closeLater := func() {
		// Leave the current event callback before releasing its monitor.
		mygo.App.WhenReady(func() {
			if d.panel == panel {
				d.closePanel()
			}
		})
	}
	localBlock := objc.NewBlock(func(_ objc.Block, event objc.ID) objc.ID {
		if d.panelNative.shouldDismiss(event) {
			closeLater()
		}
		return event
	})
	globalBlock := objc.NewBlock(func(_ objc.Block, event objc.ID) {
		if d.panelNative.shouldDismiss(event) {
			closeLater()
		}
	})
	events := objc.ID(objc.GetClass("NSEvent"))
	const mouseDown = uint64(1<<1 | 1<<3 | 1<<25) // left, right, other
	local := events.Send(objc.RegisterName("addLocalMonitorForEventsMatchingMask:handler:"), mouseDown, localBlock)
	global := events.Send(objc.RegisterName("addGlobalMonitorForEventsMatchingMask:handler:"), mouseDown, globalBlock)
	stopActive := mygo.App.OnDidResignActive(closeLater)
	d.panelNative.stopDismiss = func() {
		stopActive()
		for _, monitor := range []objc.ID{local, global} {
			if monitor != 0 {
				events.Send(objc.RegisterName("removeMonitor:"), monitor)
			}
		}
		localBlock.Release()
		globalBlock.Release()
	}
	if local == 0 || global == 0 {
		return fmt.Errorf("tray panel outside-click monitor could not be installed")
	}
	return nil
}

func (s desktopPanelNative) shouldDismiss(event objc.ID) bool {
	if s.popover == 0 || !objc.Send[bool](s.popover, objc.RegisterName("isShown")) {
		return false
	}
	view := s.popover.Send(objc.RegisterName("contentViewController")).Send(objc.RegisterName("view"))
	window := view.Send(objc.RegisterName("window"))
	if window == 0 {
		return false
	}
	if eventWindowInPanel(event.Send(objc.RegisterName("window")), window) {
		return false
	}
	point := objc.Send[panelNativePoint](objc.ID(objc.GetClass("NSEvent")), objc.RegisterName("mouseLocation"))
	if containsNativePoint(objc.Send[panelNativeRect](window, objc.RegisterName("frame")), point) {
		return false
	}
	// Its own status item remains owned by TogglePanel, avoiding a close
	// followed by an immediate reopen on the same click.
	if button := panelTrayButton(); button != 0 {
		statusWindow := button.Send(objc.RegisterName("window"))
		if statusWindow != 0 && containsNativePoint(objc.Send[panelNativeRect](statusWindow, objc.RegisterName("frame")), point) {
			return false
		}
	}
	return true
}

func eventWindowInPanel(window, panel objc.ID) bool {
	for window != 0 {
		if window == panel {
			return true
		}
		parent := window.Send(objc.RegisterName("sheetParent"))
		if parent == 0 {
			parent = window.Send(objc.RegisterName("parentWindow"))
		}
		window = parent
	}
	return false
}

func containsNativePoint(r panelNativeRect, p panelNativePoint) bool {
	return p.X >= r.Origin.X && p.Y >= r.Origin.Y && p.X < r.Origin.X+r.Size.Width && p.Y < r.Origin.Y+r.Size.Height
}

// Native AppKit geometry is used only to distinguish outside clicks.
type panelNativePoint struct {
	_    structs.HostLayout
	X, Y float64
}
type panelNativeSize struct {
	_             structs.HostLayout
	Width, Height float64
}
type panelNativeRect struct {
	_      structs.HostLayout
	Origin panelNativePoint
	Size   panelNativeSize
}

func panelTrayButton() objc.ID {
	app := objc.ID(objc.GetClass("NSApplication")).Send(objc.RegisterName("sharedApplication"))
	windows := app.Send(objc.RegisterName("windows"))
	for i := range objc.Send[uint](windows, objc.RegisterName("count")) {
		w := windows.Send(objc.RegisterName("objectAtIndex:"), i)
		if button := findPanelTrayButton(w.Send(objc.RegisterName("contentView"))); button != 0 {
			return button
		}
	}
	return 0
}

func findPanelTrayButton(view objc.ID) objc.ID {
	if view == 0 {
		return 0
	}
	if objc.Send[bool](view, objc.RegisterName("respondsToSelector:"), objc.RegisterName("action")) && objc.Send[objc.SEL](view, objc.RegisterName("action")) == objc.RegisterName("mygoTrayClicked:") {
		tip := view.Send(objc.RegisterName("toolTip"))
		sshyard := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("alloc")).Send(objc.RegisterName("initWithUTF8String:"), "SSHYard")
		matches := objc.Send[bool](tip, objc.RegisterName("isEqualToString:"), sshyard)
		sshyard.Send(objc.RegisterName("release"))
		if matches {
			return view
		}
	}
	subviews := view.Send(objc.RegisterName("subviews"))
	for i := range objc.Send[uint](subviews, objc.RegisterName("count")) {
		if button := findPanelTrayButton(subviews.Send(objc.RegisterName("objectAtIndex:"), i)); button != 0 {
			return button
		}
	}
	return 0
}
