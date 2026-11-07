package app

import (
	"testing"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestIconButton(t *testing.T) {
	var clicks, disabledClicks int
	view := func(c *ui.Context) {
		m := locales.In(c.Locale())
		ui.Row(c).Padding(40).Gap(8).Children(func() {
			if (IconButton{Icon: icons.SplitRight, Label: m.SplitRight()}).Clicked(c, "split") {
				clicks++
			}
			if (IconButton{Icon: icons.Delete, Label: m.DeleteHost(), Disabled: true}).Clicked(c, "del") {
				disabledClicks++
			}
		})
	}
	tt := ui.NewTester(view, 400, 200)
	tt.SetLocale("en")
	tt.Frame()
	if err := tt.Click("Split Right"); err != nil {
		t.Fatal(err)
	}
	if clicks != 1 {
		t.Errorf("%d clicks", clicks)
	}
	tt.Click("Delete Host")
	if disabledClicks != 0 {
		t.Error("a disabled IconButton reported a click")
	}
}
