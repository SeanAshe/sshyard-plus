package app

import (
	"slices"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// TestDropMenu: the sidebar's "+" opens the app's own menu, not the
// system's, its port forward disabled without a host; Escape and its
// button close it; the arrows move through it and Enter chooses.
func TestDropMenu(t *testing.T) {
	a, tt := newTestApp(t)
	mustClick(t, tt, "Add")
	if got := menuOf(a); got != nil {
		t.Fatalf("a menu of the system: %q", got)
	}
	if a.menu.key == "" {
		t.Fatal("the menu did not open")
	}
	// No host: no port forward to make.
	entries := a.addEntries(locales.In("en"))
	if i := slices.IndexFunc(entries, func(e menuEntry) bool { return e.Label == "New Port Forward…" }); i < 0 || !entries[i].Disabled {
		t.Error("New Port Forward… is not disabled without a host")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.menu.key != "" {
		t.Error("Escape left the menu open")
	}
	mustClick(t, tt, "Add")
	mustClick(t, tt, "Add")
	if a.menu.key != "" {
		t.Error("its button left the menu open")
	}

	// From the keyboard: the first entry has the focus, Down moves it.
	mustClick(t, tt, "Add")
	tt.Frame()
	if !tt.Focused("New Host") {
		t.Fatal("the first entry has not the focus")
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if !tt.Focused("New group") {
		t.Fatal("Down did not move to the next entry")
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.group == nil || a.menu.key != "" {
		t.Fatalf("Enter did not choose New group, or left the menu: %q", tt.Texts())
	}
}

func TestHostGroupMenuSeparators(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		a, _ := newTestApp(t)
		h, err := a.store.Add(assets.Host{Name: "web", Address: "web", User: "u"})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"No group", "-", "New group…"}
		if grouped {
			if err := a.store.AddGroup("Prod"); err != nil {
				t.Fatal(err)
			}
			want = []string{"No group", "-", "Prod", "-", "New group…"}
		}
		entries := a.hostEntries(locales.In("en"), h)
		i := slices.IndexFunc(entries, func(e menuEntry) bool { return e.Label == "Move to group" })
		if got := menuLabels(entries[i].Sub); !slices.Equal(got, want) {
			t.Fatalf("group menu %q, want %q", got, want)
		}
	}
}

// TestDropSubmenu: a host's "more" menu opens its submenu as the pointer
// comes on to its entry, and choosing from it takes the host out of its
// group.
func TestDropSubmenu(t *testing.T) {
	a, tt := newTestApp(t)
	h, err := a.Store().Add(assets.Host{Name: "web", Address: "10.0.0.5", User: "root", Group: "Prod"})
	if err != nil {
		t.Fatal(err)
	}
	a.selected = h.ID
	tt.Frame()
	mustClick(t, tt, "More Actions")
	a.selected = ""
	tt.Move(600, 700)
	tt.Frame()
	if a.menu.sub != -1 {
		t.Fatal("submenu open before asked")
	}
	mv := mustFind(t, tt, "Move to group")
	tt.Move(mv.X+mv.W/2, mv.Y+mv.H/2)
	tt.Frame()
	tt.Frame()
	if a.menu.sub == -1 {
		t.Fatal("the submenu did not open on its entry")
	}
	mustClick(t, tt, "No group")
	if got, _ := a.Store().Get(h.ID); got.Group != "" {
		t.Errorf("group %q", got.Group)
	}
	if a.menu.key != "" {
		t.Error("the menu stayed open")
	}
}
