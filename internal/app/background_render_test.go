package app

import (
	"image"
	"image/color"
	"testing"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/egoist/mygo/ui"
)

func renderBackgroundFixture(a *App) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 200; x++ {
			if x < 100 {
				img.SetRGBA(x, y, color.RGBA{R: 240, G: 30, B: 20, A: 255})
			} else {
				img.SetRGBA(x, y, color.RGBA{R: 20, G: 30, B: 240, A: 255})
			}
		}
	}
	a.settings.Background = settings.DefaultBackground()
	a.settings.Background.Image = "background-render.png"
	a.background.active, a.background.requested = a.settings.Background.Image, a.settings.Background.Image
	a.background.bmp = ui.NewBitmap(img)
}

func TestBackgroundMainWithoutVibrancy(t *testing.T) {
	a, tt := newTestApp(t)
	renderBackgroundFixture(a)
	a.settings.Background.ChromeOpacity = 0
	a.settings.Background.ContentOpacity = 0
	tt.SetVibrancy(false)
	tt.Frame()
	if !a.clearContent {
		t.Error("image did not clear default terminal fill without vibrancy")
	}
	// Image rendering retains the root painter's device scale observation.
	a.zoom.desktop = 1.5
	a.settings.UIZoom = 100
	tt.SetScale(2)
	tt.Frame()
	if a.zoom.device != 2 {
		t.Fatalf("device scale observation lost: %v", a.zoom.device)
	}
}

func TestBackgroundEditorWindow(t *testing.T) {
	a, tt, _, _ := localPane(t, "background.txt")
	renderBackgroundFixture(a)
	a.settings.Background.ChromeOpacity = 0
	a.settings.Background.ContentOpacity = 0
	wins := fakeWindows(a)
	waitFor(t, tt, "local file", func() bool { return tt.HasText("background.txt") })
	mustClick(t, tt, "background.txt")
	mustClick(t, tt, "background.txt")
	_, et, ec := openedWindow(t, a, tt, wins, 1)
	et.SetVibrancy(false)
	et.Frame()
	ed := shown(ec)
	if ed.st.Theme == nil || ed.st.Theme.Background.A != 0 {
		t.Fatal("editor default background stayed opaque")
	}
	a.settings.Background.Image = ""
	et.Frame()
	if ed.st.Theme != nil {
		t.Fatal("removing image did not restore default editor theme")
	}
}
