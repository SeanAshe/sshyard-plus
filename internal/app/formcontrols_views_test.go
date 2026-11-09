package app

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/fonts"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestFormSettingsSelectHitAreaAndPersistence(t *testing.T) {
	a, tt := newTestApp(t)
	tt.SetSize(1200, 1100)
	a.openSettings(prefAppearance)
	tt.Frame()
	m := locales.In("en")
	for _, region := range []string{"text", "middle", "arrow"} {
		current, other := m.CloseToTray(), m.CloseQuit()
		if a.settings.CloseBehavior == settings.CloseQuit {
			current, other = other, current
		}
		clickFormSelectRegion(t, tt, current, 180, region)
		mustClick(t, tt, other)
		want := settings.CloseToTray
		if other == m.CloseQuit() {
			want = settings.CloseQuit
		}
		saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json"))
		if err != nil || a.settings.CloseBehavior != want || saved.CloseBehavior != want {
			t.Fatalf("selection did not persist: current=%q saved=%q err=%v", a.settings.CloseBehavior, saved.CloseBehavior, err)
		}
	}
	before := a.settings.RememberLayout
	// The switch is at the trailing edge of the same settings control column.
	r := mustFind(t, tt, m.CloseQuit())
	label := mustFind(t, tt, m.RememberLayout())
	note := mustFind(t, tt, m.RememberLayoutNote())
	y := (label.Y + note.Y + note.H) / 2
	tt.ClickAt(r.X+140, y)
	if a.settings.RememberLayout == before {
		t.Fatal("settings switch body did not change the preference")
	}
}

// The settings call site fixes the trigger width; use its visible value to
// locate body regions instead of clicking the Field label, which only focuses.
func clickFormSelectRegion(t *testing.T, tt *ui.Tester, value string, width float32, region string) {
	t.Helper()
	r := mustFind(t, tt, value)
	x := r.X + 3
	if region == "middle" {
		x = r.X + width/2 - 12
	} else if region == "arrow" {
		x = r.X + width - 28
	}
	tt.ClickAt(x, r.Y+r.H/2)
}

func TestFormHostSelectCreateEditPersistence(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(fmt.Sprintf("edit=%v", edit), func(t *testing.T) {
			a, tt := hostGroupTestEditor(t, edit)
			tt.SetSize(1000, 1200)
			m := locales.In("en")
			proxy, err := a.store.SaveProxy(assets.Proxy{Name: "form proxy", Kind: assets.ProxySOCKS5, Address: "127.0.0.1", Port: 1080})
			if err != nil {
				t.Fatal(err)
			}
			a.form.netOpen = true
			tt.Frame()
			tt.Advance(focusHoldFor)
			clickFormSelectRegion(t, tt, a.form.auth, 140, "text")
			mustClick(t, tt, m.AuthAgent())
			clickFormSelectRegion(t, tt, m.ProxyNone(), 140, "middle")
			mustClick(t, tt, proxy.Name)
			mustClick(t, tt, m.Save())
			saved, ok := a.store.Get(a.selected)
			if !ok || a.editing || saved.Auth != assets.AuthAgent || saved.ProxyID != proxy.ID {
				t.Fatalf("select choices not saved: %+v editing=%v", saved, a.editing)
			}
			a.openEditor(&saved)
			tt.Frame()
			if a.form.auth != m.AuthAgent() || a.form.proxy != proxy.ID {
				t.Fatal("reopening lost authentication or proxy choice")
			}
		})
	}
}

func TestFormFontComboboxRemainsSearchable(t *testing.T) {
	old := listFonts
	listFonts = func() []fonts.Family { return []fonts.Family{{Name: "Form Mono", Mono: true}, {Name: "Form Sans"}} }
	t.Cleanup(func() { listFonts = old })
	a, tt := newTestApp(t)
	waitFor(t, tt, "font list", func() bool { return a.fonts.loaded })
	tt.SetSize(1200, 1500)
	a.openSettings(prefAppearance)
	tt.Frame()
	m := locales.In("en")
	field := mustFind(t, tt, m.InterfaceFont())
	selectValue := mustFind(t, tt, m.CloseToTray())
	tt.ClickAt(selectValue.X+80, field.Y+field.H/2)
	if !tt.Focused(m.InterfaceFont()) {
		t.Fatal("font combobox body did not focus its editable input")
	}
	tt.Type("Form San")
	if !tt.HasText("Form Sans") || tt.HasText("Form Mono") {
		t.Fatalf("font search did not filter choices: %q", tt.Texts())
	}
	mustClick(t, tt, "Form Sans")
	if a.settings.UIFont != "Form Sans" {
		t.Fatalf("selected UI font = %q", a.settings.UIFont)
	}
	saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json"))
	if err != nil || saved.UIFont != "Form Sans" {
		t.Fatalf("font selection not persisted: %q, %v", saved.UIFont, err)
	}
}
