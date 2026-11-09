package app

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/updater"
	"github.com/egoist/mygo/ui"
)

// fakeUpdates is the update window in tests: it counts the checks the
// user asked for and records the automatic checks the app chose.
type fakeUpdates struct {
	enabled   bool
	checks    int
	automatic []bool
	available string
	route     *updater.Route
}

func (f *fakeUpdates) Enabled() bool                { return f.enabled }
func (f *fakeUpdates) Check()                       { f.checks++ }
func (f *fakeUpdates) SetAutomatic(on bool)         { f.automatic = append(f.automatic, on) }
func (f *fakeUpdates) SetRoute(r updater.Route)     { f.route = &r }
func (f *fakeUpdates) Available() string            { return f.available }
func (f *fakeUpdates) OnChange(func()) (off func()) { return func() {} }

func newUpdatesApp(t *testing.T, upd *fakeUpdates) (*App, *ui.Tester) {
	t.Helper()
	dir := t.TempDir()
	a, err := New(Platform{DataDir: dir, Secrets: keychain.NewMemory(), HomeDir: dir, CacheDir: filepath.Join(dir, "cache"), Updates: upd})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	a.Start()
	tt := ui.NewTester(a.View, 1200, 1600)
	tt.SetLocale("en")
	tt.Frame()
	return a, tt
}

func TestUpdateSettings(t *testing.T) {
	upd := &fakeUpdates{enabled: true}
	a, tt := newUpdatesApp(t, upd)
	if !slices.Equal(upd.automatic, []bool{true}) {
		t.Fatalf("automatic checks %v, want the settings' on", upd.automatic)
	}
	a.openSettings(prefAbout)
	tt.Frame()
	mustClick(t, tt, "6 h")
	tt.Frame()
	if a.Settings().UpdateIntervalHours != 6 {
		t.Fatalf("interval %d, want 6", a.Settings().UpdateIntervalHours)
	}
	if s, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json")); err != nil || s.UpdateIntervalHours != 6 {
		t.Fatalf("saved interval %d %v", s.UpdateIntervalHours, err)
	}

	a.updateCheck = false
	s := a.Settings()
	s.CheckUpdates = false
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(upd.automatic, []bool{true, false}) {
		t.Fatalf("automatic checks %v after turning them off", upd.automatic)
	}

	mustClick(t, tt, "Check for Updates")
	if upd.checks != 1 || a.page != pageSettings {
		t.Fatalf("about check: %d checks, page %v", upd.checks, a.page)
	}

	// A newer version is offered in the about page and the status bar;
	// each opens the update window with its notes.
	upd.available = "9.9.9"
	tt.Frame()
	mustClick(t, tt, "View Update")
	a.closeSettings()
	tt.Frame()
	mustClick(t, tt, "Update")
	if upd.checks != 3 {
		t.Fatalf("%d checks after view and status bar", upd.checks)
	}
}
