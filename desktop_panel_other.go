//go:build !darwin

package main

import "github.com/egoist/mygo/ui"

// MyGo owns focus loss, outside-click and Escape dismissal on these platforms.
type desktopPanelNative struct{}

func (d *desktop) observePanel() (func(), error) { return func() {}, nil }
func (d *desktop) matchPanelAppearance()         {}
func (d *desktop) releasePanel()                 {}

func (d *desktop) watchPanelDismiss() error { return nil }

func (d *desktop) syncPanelMaterial(ui.Color)   {}
func (d *desktop) panelContentSize() (int, int) { return d.panel.ContentSize() }
