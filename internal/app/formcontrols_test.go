package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestFormSelectBodyAndChoices(t *testing.T) {
	for _, fraction := range []float32{0.05, 0.5, 0.95} {
		t.Run(string(rune('A'+int(fraction*10))), func(t *testing.T) {
			selected := "first"
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Padding(20).Children(func() {
					formSelect(c, &selected, []string{"first", "second", "third"}).Label("Choice").Width(240)
				})
			}, 400, 400)
			tt.Frame()
			for _, want := range []string{"second", "third"} {
				r := mustFind(t, tt, "Choice")
				tt.ClickAt(r.X+r.W*fraction, r.Y+r.H/2)
				mustClick(t, tt, want)
				if selected != want {
					t.Fatalf("selected %q want %q", selected, want)
				}
			}
		})
	}
}

func TestFormControlsDisabled(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		selected, on, checked, segment := "first", false, false, 0
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Column(c).Padding(20).Gap(20).Children(func() {
				formDisabled(formSelect(c.Key("select"), &selected, []string{"first", "second", "third"}).Label("Choice").Width(240), disabled)
				formDisabled(formSwitch(c.Key("switch"), &on).Label("Switch"), disabled)
				formDisabled(formCheckbox(c.Key("check"), &checked, "Checkbox"), disabled)
				formSegmented(c.Key("segment"), &segment, disabled, "One", "Two")
			})
		}, 500, 500)
		tt.Frame()
		for _, label := range []string{"Choice", "Switch", "Checkbox", "Two"} {
			mustClick(t, tt, label)
			if label == "Choice" {
				if tt.HasText("second") == disabled {
					t.Fatalf("disabled=%v select opened=%v", disabled, !disabled)
				}
				if !disabled {
					tt.Key(0, ui.KeyEscape)
				}
			}
		}
		if on != !disabled || checked != !disabled || segment != boolIndex(!disabled) {
			t.Fatalf("disabled=%v switch=%v check=%v segment=%d", disabled, on, checked, segment)
		}
	}
}

func boolIndex(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestFormControlsDisabledAncestor(t *testing.T) {
	selected, on, checked, segment := "first", false, false, 0
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(20).Gap(20).Disabled(true).Children(func() {
			formSelect(c.Key("select"), &selected, []string{"first", "second"}).Label("Choice")
			formSwitch(c.Key("switch"), &on).Label("Switch")
			formCheckbox(c.Key("check"), &checked, "Checkbox")
			formSegmented(c.Key("segment"), &segment, false, "One", "Two")
		})
	}, 400, 400)
	tt.Frame()
	for _, label := range []string{"Choice", "Switch", "Checkbox", "Two"} {
		mustClick(t, tt, label)
	}
	if on || checked || segment != 0 || selected != "first" || tt.HasText("second") {
		t.Fatal("disabled ancestor allowed changes")
	}
}

func TestFormSelectKeyboard(t *testing.T) {
	selected, changed := "first", 0
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(20).Children(func() {
			if formSelect(c, &selected, []string{"first", "second", "third"}).Label("Choice").AutoFocus().Changed() {
				changed++
			}
		})
	}, 400, 400)
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	if selected != "second" || changed != 1 {
		t.Fatalf("keyboard selection=%q changed=%d", selected, changed)
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEscape)
	if tt.HasText("third") || selected != "second" {
		t.Fatal("Escape did not close popup without changing selection")
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnd)
	tt.Key(0, ui.KeySpace)
	if selected != "third" || changed != 2 {
		t.Fatalf("Space selection=%q changed=%d", selected, changed)
	}
}

func TestFormEditableControls(t *testing.T) {
	text, area, selected := "", "", "first"
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(20).Gap(20).Children(func() {
			formTextInput(c.Key("text"), &text).Label("Text").Width(240)
			formTextArea(c.Key("area"), &area).Label("Area").Width(240)
			formCombobox(c.Key("combo"), &selected, []string{"first", "second", "third"}).Label("Search").Width(240)
		})
	}, 400, 500)
	tt.Frame()
	for _, label := range []string{"Text", "Area", "Search"} {
		mustClick(t, tt, label)
		tt.Command("selectAll")
		tt.Type("second")
		if label == "Search" {
			if tt.HasText("third") {
				t.Fatal("editable combobox did not filter")
			}
			tt.Key(0, ui.KeyEnter)
		}
	}
	if text != "second" || area != "second" || selected != "second" {
		t.Fatalf("edited values=%q %q %q", text, area, selected)
	}
}
