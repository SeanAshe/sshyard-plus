package app

import (
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func backgroundSettingsTester(a *App) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		a.drain()
		s, changed := a.settings, false
		a.backgroundSection(c, locales.In(c.Locale()), theme.For(a.settings.Theme, c.Theme().Dark), &s, &changed)
		if changed {
			if err := a.ApplySettings(s); err != nil {
				c.AddToast(ui.Toast{Title: err.Error(), Type: "error"})
			}
		}
	}, 440, 650)
}

func backgroundSettingsImage(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "photo.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 24, 16))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := errors.Join(png.Encode(f, img), f.Close()); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBackgroundSettingsWorkflow(t *testing.T) {
	a, _ := newTestApp(t)
	tt := backgroundSettingsTester(a)
	tt.SetLocale("en")
	tt.Frame()
	m := locales.In("en")
	// Cancelling leaves the current settings untouched.
	a.p.PickFile = func(string) (string, error) { return "", nil }
	mustClick(t, tt, m.BackgroundChoose())
	waitFor(t, tt, "cancelled picker", func() bool { return !a.background.busy })
	if a.settings.Background.Image != "" {
		t.Fatal("cancel changed background")
	}
	path := backgroundSettingsImage(t)
	a.p.PickFile = func(string) (string, error) { return path, nil }
	mustClick(t, tt, m.BackgroundChoose())
	waitFor(t, tt, "imported background", func() bool { return a.hasBackground() })
	tt.Frame()
	mustClick(t, tt, m.BackgroundContain())
	tt.Frame()
	saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json"))
	if err != nil || saved.Background.Fit != "contain" {
		t.Fatalf("fit not saved: %+v %v", saved.Background, err)
	}
	a.p.PickFile = func(string) (string, error) { return "", errors.New("picker rejected") }
	mustClick(t, tt, m.BackgroundReplace())
	waitFor(t, tt, "picker failure", func() bool { return !a.background.busy })
	if !a.hasBackground() || a.background.err == nil || a.background.err.Error() != m.BackgroundImportFailed("picker rejected") {
		t.Fatalf("failed replacement lost image or error: %v", a.background.err)
	}
	mustClick(t, tt, m.BackgroundRemove())
	tt.Frame()
	if a.settings.Background.Image != "" || a.hasBackground() {
		t.Fatal("remove kept the background")
	}
}

// The visible title shares the slider's accessibility label; Find returns
// that title first, so derive the track below it from the view's layout.
func backgroundSliderRect(t *testing.T, tt *ui.Tester, label string) ui.Rect {
	t.Helper()
	r := mustFind(t, tt, label)
	return ui.Rect{X: r.X, Y: r.Y + r.H + theme.Space(2), W: 440 - 2*r.X, H: theme.Space(5)}
}

func TestBackgroundOpacityCommit(t *testing.T) {
	a, _ := newTestApp(t)
	name, bmp, err := a.importBackground(backgroundSettingsImage(t))
	if err != nil {
		t.Fatal(err)
	}
	a.background.seq++
	a.backgroundImported(locales.In("en"), a.background.seq, name, bmp, nil)
	tt := backgroundSettingsTester(a)
	tt.SetLocale("en")
	tt.Frame()
	label := locales.In("en").BackgroundChromeOpacity()
	r := backgroundSliderRect(t, tt, label)
	tt.Press(r.X+r.W*.3, r.Y+r.H/2)
	tt.Move(r.X+r.W*.5, r.Y+r.H/2)
	tt.Frame()
	if a.background.preview == nil || a.effectiveBackground().ChromeOpacity == 70 {
		t.Fatal("no live slider preview")
	}
	saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json"))
	if err != nil || saved.Background.ChromeOpacity != 70 || a.settings.Background.ChromeOpacity != 70 {
		t.Fatal("drag persisted before release")
	}
	tt.Move(r.X+r.W*.6, r.Y+r.H/2)
	tt.Release(r.X+r.W*.6, r.Y+r.H/2)
	tt.Frame()
	saved, err = settings.Load(filepath.Join(a.p.DataDir, "settings.json"))
	if err != nil || saved.Background.ChromeOpacity == 70 || a.background.preview != nil {
		t.Fatalf("release failed: %+v %v", saved.Background, err)
	}
	// Keyboard adjustment commits immediately, with no pointer drag to finish.
	tt.Key(0, ui.KeyEnd)
	tt.Frame()
	if a.settings.Background.ChromeOpacity != 100 {
		t.Fatal("keyboard opacity not committed")
	}

	// A failed write keeps the committed opacity and discards the preview.
	settingsPath := filepath.Join(a.p.DataDir, "settings.json")
	if err := os.Remove(settingsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(settingsPath, 0700); err != nil {
		t.Fatal(err)
	}
	r = backgroundSliderRect(t, tt, label)
	tt.Press(r.X+r.W*.2, r.Y+r.H/2)
	tt.Move(r.X+r.W*.4, r.Y+r.H/2)
	tt.Frame()
	tt.Release(r.X+r.W*.4, r.Y+r.H/2)
	tt.Frame()
	if a.settings.Background.ChromeOpacity != 100 || a.background.preview != nil {
		t.Fatal("failed save retained a preview or changed committed settings")
	}
}

func TestCloseSettingsDiscardsBackgroundPreview(t *testing.T) {
	a, tt := newTestApp(t)
	a.openSettings(prefAppearance)
	tt.Frame()
	name, bmp, err := a.importBackground(backgroundSettingsImage(t))
	if err != nil {
		t.Fatal(err)
	}
	a.background.seq++
	a.backgroundImported(locales.In("en"), a.background.seq, name, bmp, nil)
	tt.Frame()
	bg := a.settings.Background
	bg.ChromeOpacity = 12
	a.background.preview = &bg
	a.closeSettings()
	if a.background.preview != nil {
		t.Fatal("closing settings retained preview")
	}
}

func TestBackgroundPreviewInvalidatesWindows(t *testing.T) {
	a, main := newTestApp(t)
	var svc ui.Services
	idle := ui.NewTester(func(c *ui.Context) { svc = c.Services() }, 100, 100)
	idle.Frame()
	a.mu.Lock()
	if a.winSvc == nil {
		a.winSvc = make(map[int]ui.Services)
	}
	a.winSvc[-9002] = svc
	a.mu.Unlock()
	t.Cleanup(func() { a.mu.Lock(); delete(a.winSvc, -9002); a.mu.Unlock() })
	bg := a.settings.Background
	bg.ChromeOpacity = 13
	a.setBackgroundPreview(&bg)
	if !idle.FramePending() {
		t.Fatal("live preview did not invalidate idle window")
	}
	idle.Frame()
	a.setBackgroundPreview(&bg)
	if idle.FramePending() {
		t.Fatal("unchanged preview caused redraw loop")
	}
	// The caller's later mutation must not change the displayed draft.
	bg.ChromeOpacity = 27
	if a.effectiveBackground().ChromeOpacity != 13 {
		t.Fatal("preview retained caller pointer")
	}
	a.closeSettings()
	if a.background.preview != nil || !idle.FramePending() {
		t.Fatal("closing settings did not redraw committed background")
	}
	idle.Frame()
	a.setBackgroundPreview(nil)
	if idle.FramePending() {
		t.Fatal("empty preview caused redraw loop")
	}
	a.openSettings(prefAppearance)
	a.setBackgroundPreview(&bg)
	idle.Frame()
	a.pref = prefTerminal
	main.Frame()
	if a.background.preview != nil || !idle.FramePending() {
		t.Fatal("leaving appearance did not restore idle window")
	}
}
