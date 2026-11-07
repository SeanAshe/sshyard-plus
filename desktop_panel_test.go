package main

import (
	"time"

	"github.com/Licoy/sshyard/internal/app"
	"github.com/egoist/mygo"
)

// This runs inside the existing opt-in native helper, using its isolated
// credentials, real background MCP listener and original main-window draft.
// It exceeds 50 lines to keep popup opening, background MCP and main-window
// restoration in visible sequence rather than scattering the scenario steps.
func driveNativePanel(d *desktop, draft *string, base, token string) {
	// A real tray click cannot precede AppKit realizing the newly created
	// status item; the helper invokes its handler directly, so let that happen.
	time.Sleep(300 * time.Millisecond)
	mygo.RunOnMain(func() {
		d.togglePanel()
		nativeCheck(d.panel != nil && d.panel.IsVisible() && d.panel.IsFlyout(), "tray toggle did not open a native flyout")
		nativeCheck(!d.window.IsVisible(), "opening popup restored hidden main window")
		nativeCheck(len(mygo.Windows()) == 2, "popup duplicated main window")
		nativeDock(false)
	})
	// Return to the native event loop so the visible panel executes its View.
	time.Sleep(50 * time.Millisecond)
	nativeCheck(nativeMCP(base, token) == nil, "open popup blocked background MCP")
	mygo.RunOnMain(func() {
		d.togglePanel()
		nativeCheck(d.panel == nil && d.panelView == nil, "tray toggle did not dismiss popup state")
		nativeCheck(!d.window.IsVisible() && !d.window.IsDestroyed(), "popup dismissal closed or restored main window")
	})
	nativeCheck(nativeMCP(base, token) == nil, "popup dismissal stopped background MCP")
	mygo.RunOnMain(func() {
		d.openPanel()
		panel, view := d.panel, d.panelView
		d.openPanel()
		nativeCheck(d.panel == panel && d.panelView == view, "opening existing popup recreated it")
		d.openingPanel = true
		d.activate()
		d.openingPanel = false
		nativeCheck(!d.window.IsVisible(), "activation during popup creation restored main window")
		d.activate()
		nativeCheck(panel.IsDestroyed() && d.window.IsVisible(), "explicit reopen did not close popup and restore main")
		d.window.Close()
		d.openPanel()
		panel = d.panel
		d.trayAction(app.TrayOpenMain)
		nativeCheck(panel.IsDestroyed() && d.panel == nil, "main-window action kept popup alive")
		nativeCheck(d.window.IsVisible() && *draft == "retained native draft", "popup action lost original main state")
		d.window.Close()
		d.openPanel()
		nativeCheck(d.panelView != nil && d.panelView != view, "new popup retained ephemeral view state")
		// Native Escape/outside-click dismissal reaches the same Closed callback.
		d.panel.Close()
		nativeCheck(d.panel == nil && d.panelView == nil, "native close retained popup state")
		nativeCheck(!d.window.IsVisible(), "native popup close restored main window")
	})
}
