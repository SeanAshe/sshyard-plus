package updater

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/plugins/updater/native"
)

// Plugin is MyGo's updater plugin drawn in native UI, which reads the
// manifest `mygo build` publishes to the latest release of the repository
// in mygo.json (updates.github = SeanAshe/sshyard-plus) and verifies signatures.
// It checks soon after each launch and every interval after while
// automatic is on. The interval is read once: a change applies at the
// next launch.
func Plugin(interval time.Duration, automatic bool) mygo.Plugin {
	return native.New(updater.Options{Interval: interval, DisableAutomaticChecks: !automatic})
}

// MygoBackend is the Backend of the app, over the plugin Plugin returns,
// which must be used.
type MygoBackend struct{}

func (MygoBackend) Enabled() bool                   { return mygo.Updater.Enabled() }
func (MygoBackend) Check()                          { updater.CheckForUpdates() }
func (MygoBackend) SetAutomatic(on bool)            { updater.SetAutomaticChecks(on) }
func (MygoBackend) Available() string               { return updater.AvailableVersion() }
func (MygoBackend) OnChange(fn func()) (off func()) { return updater.OnChange(fn) }
