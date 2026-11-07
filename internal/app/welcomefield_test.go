package app

import (
	"image"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/egoist/mygo/ui"
)

func TestWelcomeFieldFollowsPointer(t *testing.T) {
	a, tt := newTestApp(t)
	x, y := a.sbWidth+60, float32(toolbarHeight+60)
	tt.Move(x, y)
	tt.Advance(50 * time.Millisecond)
	tt.Move(x+40, y)
	if a.field.hover == nil || len(a.field.trail) != 2 {
		t.Fatalf("pointer path not marked: hover=%v trail=%v", a.field.hover, a.field.trail)
	}
	if a.field.trail[1].force <= a.field.trail[0].force {
		t.Fatalf("a quick move should mark stronger than the first: %v", a.field.trail)
	}
	pill := mustFind(t, tt, "Quick Migration")
	if len(a.field.controls) != 3 || !a.field.content.Contains(pill.X+pill.W/2, pill.Y+pill.H/2) ||
		a.field.content.Contains(x, y) {
		t.Fatalf("content %+v, controls %v", a.field.content, a.field.controls)
	}
	tt.ClickAt(x, y)
	if len(a.field.rings) != 1 {
		t.Fatalf("click on the page sent %d rings", len(a.field.rings))
	}

	// A press on a button is the button's: no ring, and it still works.
	mustClick(t, tt, "Quick Migration")
	if len(a.field.rings) != 1 || a.page != pageSettings {
		t.Fatalf("button press: rings=%d page=%v", len(a.field.rings), a.page)
	}
}

// The page sees every press first, the buttons' too: a button works even
// when the field's note of where the buttons are is stale.
func TestWelcomeFieldLeavesButtonPresses(t *testing.T) {
	a, tt := newTestApp(t)
	a.field.controls = nil
	mustClick(t, tt, "Quick Migration")
	if a.page != pageSettings {
		t.Fatalf("button press taken by the field: page=%v", a.page)
	}
}

func TestWelcomeFieldStillWithReducedMotionOrBackground(t *testing.T) {
	a, tt := newTestApp(t)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	x, y := a.sbWidth+60, float32(toolbarHeight+60)
	tt.Move(x, y)
	tt.ClickAt(x, y)
	if a.field.hover != nil || len(a.field.trail)+len(a.field.rings) != 0 {
		t.Fatalf("reduced motion still stirred the dots: %+v", a.field)
	}

	tt.SetPreferences(ui.Preferences{})
	a.background.bmp, a.background.active, a.settings.Background.Image = ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 1, 1))), "bg.png", "bg.png"
	tt.Frame()
	tt.Move(x, y)
	if a.field.hover != nil {
		t.Fatal("dots drawn over the user's background image")
	}
}

func TestWelcomeFieldResetsWithTab(t *testing.T) {
	a, tt := newTestApp(t)
	x, y := a.sbWidth+60, float32(toolbarHeight+60)
	tt.Move(x, y)
	if a.field.hover == nil {
		t.Fatal("pointer not marked")
	}
	h, err := a.store.Add(assets.Host{Name: "box", Address: "127.0.0.1", Port: 1, User: "root"})
	if err != nil {
		t.Fatal(err)
	}
	a.Connect(h.ID, openAt{})
	tt.Frame()
	if a.field.hover != nil || !a.field.origin.IsZero() {
		t.Fatal("field kept its marks behind an open tab")
	}
}

func TestWelcomeFieldSettles(t *testing.T) {
	var f welcomeField
	t0 := time.Unix(1000, 0)
	f.move(10, 10, t0)
	f.tap(10, 10, t0)
	f.step(t0)
	for d := 100 * time.Millisecond; d <= fieldIdle+fieldSettle*time.Second+time.Second; d += 100 * time.Millisecond {
		f.step(t0.Add(d))
	}
	if !f.calm || f.hover != nil || len(f.trail)+len(f.rings) != 0 {
		t.Fatalf("field did not settle: %+v", f)
	}
	if lo, hi := fieldIdle.Seconds(), fieldIdle.Seconds()+fieldSettle; f.drift < lo || f.drift > hi {
		t.Fatalf("current drifted %.1fs, want between %.0f and %.0f", f.drift, lo, hi)
	}
	f.move(20, 20, t0.Add(time.Minute))
	if f.calm {
		t.Fatal("a move did not wake the field")
	}
}
