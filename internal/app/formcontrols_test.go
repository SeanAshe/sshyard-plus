package app

import (
	"image"
	"image/color"
	"testing"

	"github.com/Licoy/sshyard/internal/theme"
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
				for _, option := range []string{"second", "third"} {
					if !tt.HasText(option) {
						t.Fatalf("body click at %v missing %q: %v", fraction, option, tt.Texts())
					}
				}
				mustClick(t, tt, want)
				if selected != want {
					t.Fatalf("selected %q want %q", selected, want)
				}
			}
		})
	}
}

func TestFormControlsCursorAndDisabled(t *testing.T) {
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
			r := mustFind(t, tt, label)
			tt.Move(r.X+r.W/2, r.Y+r.H/2)
			if got := tt.Cursor(); (got == ui.CursorPointer) == disabled {
				t.Fatalf("disabled=%v %s cursor=%v", disabled, label, got)
			}
			mustClick(t, tt, label)
			if label == "Choice" {
				if disabled {
					if tt.HasText("second") {
						t.Fatal("disabled select opened")
					}
				} else {
					r := mustFind(t, tt, "second")
					tt.Move(r.X+r.W/2, r.Y+r.H/2)
					if tt.Cursor() != ui.CursorPointer {
						t.Fatal("option has no pointer")
					}
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
		r := mustFind(t, tt, label)
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		if tt.Cursor() == ui.CursorPointer {
			t.Fatalf("disabled ancestor leaves %s with hand cursor", label)
		}
		mustClick(t, tt, label)
	}
	if on || checked || segment != 0 || selected != "first" || tt.HasText("second") {
		t.Fatal("disabled ancestor allowed changes")
	}
}

func TestFormSelectNarrowWindow(t *testing.T) {
	selected := "first"
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(12).Children(func() {
			formSelect(c, &selected, []string{"first", "second", "third"}).Label("Choice").Width(140)
		})
	}, 180, 200)
	tt.Frame()
	mustClick(t, tt, "Choice")
	for _, option := range []string{"second", "third"} {
		r := mustFind(t, tt, option)
		if r.X < 0 || r.Y < 0 || r.X+r.W > 180 || r.Y+r.H > 200 {
			t.Fatalf("narrow popup option outside window: %s %v", option, r)
		}
	}
	mustClick(t, tt, "third")
	if selected != "third" {
		t.Fatalf("narrow selected %q", selected)
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
		r := mustFind(t, tt, label)
		tt.Move(r.X+20, r.Y+r.H/2)
		if tt.Cursor() != ui.CursorText {
			t.Fatalf("%s lost text cursor", label)
		}
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

func TestFormSelectTranslucentFill(t *testing.T) {
	for _, dark := range []bool{false, true} {
		var pixels []color.RGBA
		for _, background := range []ui.Color{ui.RGB(240, 220, 200), ui.RGB(40, 80, 120)} {
			selected := "first"
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Background(background).Padding(20).Children(func() {
					formSelect(c, &selected, []string{"first", "second"}).Label("Choice").Width(240)
				})
			}, 400, 400)
			tt.SetDark(dark)
			tt.Frame()
			r := mustFind(t, tt, "Choice")
			pixels = append(pixels, tt.Image().RGBAAt(int(r.X+r.W*0.7), int(r.Y+r.H/2)))
		}
		if pixels[0] == pixels[1] {
			t.Fatalf("dark=%v control hides different backgrounds with opaque fill: %v", dark, pixels)
		}
	}
}

func TestFormSegmentedRaisesSelected(t *testing.T) {
	for _, dark := range []bool{false, true} {
		tok := theme.LightTokens()
		if dark {
			tok = theme.DarkTokens()
		}
		selected := 0
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Column(c).Fill().Background(tok.Canvas).Padding(20).Children(func() {
				formSegmented(c, &selected, false, "One", "Two")
			})
		}, 400, 200)
		tt.SetDark(dark)
		tt.Frame()
		one, two := mustFind(t, tt, "One"), mustFind(t, tt, "Two")
		at := func(r ui.Rect) color.RGBA { return tt.Image().RGBAAt(int(r.X+4), int(r.Y+r.H/2)) }
		got, want := at(one), tok.Inset
		if absDiff(got.R, want.R) > 2 || absDiff(got.G, want.G) > 2 || absDiff(got.B, want.B) > 2 {
			t.Fatalf("dark=%v selected segment fill = %v, want inset %v", dark, got, want)
		}
		if at(one) == at(two) {
			t.Fatalf("dark=%v selected segment is not raised over the track", dark)
		}
	}
}

func TestFormPopupsLightNeutral(t *testing.T) {
	for _, dark := range []bool{false, true} {
		tok := theme.LightTokens()
		if dark {
			tok = theme.DarkTokens()
		}
		choice, search := "first", "first"
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(tok.Apply(*c.Theme()))
			ui.Column(c).Fill().Background(tok.Canvas).Padding(20).Gap(200).Children(func() {
				formSelect(c.Key("select"), &choice, []string{"first", "second", "third"}).Label("Choice").Width(240)
				formCombobox(c.Key("combo"), &search, []string{"first", "second", "third"}).Label("Search").Width(240)
			})
		}, 400, 500)
		tt.SetDark(dark)
		tt.Frame()
		for _, label := range []string{"Choice", "Search"} {
			mustClick(t, tt, label)
			field, second, third := mustFind(t, tt, label), mustFind(t, tt, "second"), mustFind(t, tt, "third")
			tt.Move(second.X+second.W/2, second.Y+second.H/2)
			tt.Frame()
			img := tt.Image()
			// Left of the text, in the rows' padding.
			lit := img.RGBAAt(int(second.X-5), int(second.Y+second.H/2))
			rest := img.RGBAAt(int(third.X-5), int(third.Y+third.H/2))
			if lit == rest || absDiff(lit.R, lit.B) > 6 || absDiff(lit.R, lit.G) > 6 {
				t.Fatalf("dark=%v %s highlighted option %v, others %v: want a neutral tint", dark, label, lit, rest)
			}
			box := image.Rect(int(field.X), int(field.Y), int(field.X+field.W), int(field.Y+field.H))
			if label == "Search" && bluePixels(img, box) == 0 {
				t.Fatalf("dark=%v combobox lost its accent focus border", dark)
			}
			tt.Key(0, ui.KeyEscape)
		}
	}
}

// bluePixels counts the pixels of img in b tinted with the accent.
func bluePixels(img *image.RGBA, b image.Rectangle) int {
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if p := img.RGBAAt(x, y); int(p.B)-int(p.R) > 24 {
				n++
			}
		}
	}
	return n
}

func absDiff(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}
