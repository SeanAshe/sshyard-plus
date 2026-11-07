//go:build darwin

package main

import (
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// The application-defined popover follows the app appearance, while explicit
// event monitors handle dismissal, including accessory-app desktop clicks.
type desktopPanelNative struct {
	popover        objc.ID
	stopDismiss    func()
	fill           desktopPopoverFill
	materialFailed bool
}

func (d *desktop) observePanel() (func(), error) {
	lib, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("load AppKit popover notification: %w", err)
	}
	sym, err := purego.Dlsym(lib, "NSPopoverWillShowNotification")
	if err != nil {
		purego.Dlclose(lib)
		return nil, fmt.Errorf("load popover notification: %w", err)
	}
	// The exported symbol is the address of an NSString* constant.
	name := *(*objc.ID)(*(*unsafe.Pointer)(unsafe.Pointer(&sym)))
	center := objc.ID(objc.GetClass("NSNotificationCenter")).Send(objc.RegisterName("defaultCenter"))
	block := objc.NewBlock(func(_ objc.Block, notification objc.ID) {
		d.releasePanel()
		d.panelNative.popover = notification.Send(objc.RegisterName("object")).Send(objc.RegisterName("retain"))
		d.panelNative.popover.Send(objc.RegisterName("setBehavior:"), int64(0)) // applicationDefined
		d.matchPanelAppearance()
	})
	token := center.Send(objc.RegisterName("addObserverForName:object:queue:usingBlock:"), name, objc.ID(0), objc.ID(0), block)
	return func() {
		center.Send(objc.RegisterName("removeObserver:"), token)
		block.Release()
		purego.Dlclose(lib)
	}, nil
}

func (d *desktop) matchPanelAppearance() {
	if d.panelNative.popover == 0 {
		return
	}
	app := objc.ID(objc.GetClass("NSApplication")).Send(objc.RegisterName("sharedApplication"))
	d.panelNative.popover.Send(objc.RegisterName("setAppearance:"), app.Send(objc.RegisterName("effectiveAppearance")))
}

func (d *desktop) releasePanel() {
	if d.panelNative.stopDismiss != nil {
		d.panelNative.stopDismiss()
	}
	d.panelNative.fill.release()
	if d.panelNative.popover != 0 {
		d.panelNative.popover.Send(objc.RegisterName("release"))
	}
	d.panelNative = desktopPanelNative{}
}

// NSPopover's contentSize is in native DIPs, independent of UI zoom.
func (d *desktop) panelContentSize() (int, int) {
	size := objc.Send[panelNativeSize](d.panelNative.popover, objc.RegisterName("contentSize"))
	return int(size.Width), int(size.Height)
}
