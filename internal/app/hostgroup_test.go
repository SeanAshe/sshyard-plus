package app

import (
	"fmt"
	"slices"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func hostGroupTestEditor(t *testing.T, edit bool) (*App, *ui.Tester) {
	t.Helper()
	a, _ := newTestApp(t)
	h := addGroupedHost(t, a, "seed", "Prod")
	if err := a.store.AddGroup("Empty"); err != nil {
		t.Fatal(err)
	}
	var original *assets.Host
	if edit {
		original = &h
	}
	a.openEditor(original)
	a.form.name, a.form.address, a.form.user = "web", "example.com", "root"
	tt := ui.NewTester(func(c *ui.Context) { a.editorView(c, locales.In(c.Locale())) }, 900, 760)
	tt.SetLocale("en")
	tt.Frame()
	return a, tt
}

func clickHostGroup(t *testing.T, tt *ui.Tester, m locales.Messages) {
	t.Helper()
	// Clicking a Field label only focuses its control; open the input itself.
	label := mustFind(t, tt, m.FieldGroup())
	tt.ClickAt(label.X+label.W+theme.Space(6), label.Y+label.H/2)
}

func TestHostGroupChooseExisting(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(fmt.Sprintf("edit=%v", edit), func(t *testing.T) {
			a, tt := hostGroupTestEditor(t, edit)
			m := locales.In("en")
			id := a.form.orig.ID
			tt.Advance(focusHoldFor)
			clickHostGroup(t, tt, m)
			tt.Type("Emp")
			if !tt.HasText("Empty") || tt.HasText("Prod") {
				t.Fatal("typing did not filter existing groups")
			}
			mustClick(t, tt, "Empty")
			if a.form.group != "Empty" {
				t.Fatalf("selected group = %q", a.form.group)
			}
			mustClick(t, tt, m.Save())
			got, ok := a.store.Get(a.selected)
			if !ok || got.Group != "Empty" || (edit && got.ID != id) {
				t.Fatalf("saved host = %+v", got)
			}
			// Reopening preserves the choice; the keyboard can clear it.
			a.openEditor(&got)
			tt.Frame()
			if a.form.group != "Empty" {
				t.Fatal("reopening lost the selected group")
			}
			tt.Advance(focusHoldFor)
			mustClick(t, tt, m.FieldGroup())
			tt.Key(0, ui.KeyDown)
			tt.Key(0, ui.KeyEnter)
			if a.form.group != "" {
				t.Fatalf("No group chose %q", a.form.group)
			}
			mustClick(t, tt, m.Save())
			got, _ = a.store.Get(got.ID)
			if got.Group != "" {
				t.Fatalf("cleared group saved as %q", got.Group)
			}
		})
	}
}

func TestHostGroupCreate(t *testing.T) {
	for _, edit := range []bool{false, true} {
		for _, finish := range []string{"button", "enter", "save"} {
			t.Run(fmt.Sprintf("edit=%v/%s", edit, finish), func(t *testing.T) {
				a, tt := hostGroupTestEditor(t, edit)
				m := locales.In("en")
				mustClick(t, tt, m.NewGroup())
				tt.Frame()
				if !tt.Focused(m.NewGroup()) {
					t.Fatal("new group does not take focus from the name field")
				}
				tt.Type("  Release  ")
				switch finish {
				case "button":
					mustClick(t, tt, m.SnippetAddGroup())
				case "enter":
					tt.Key(0, ui.KeyEnter)
				}
				if slices.Contains(a.store.Groups(), "Release") {
					t.Fatal("new group persisted before saving its host")
				}
				if finish != "save" && (a.form.addingGroup || a.form.group != "Release") {
					t.Fatalf("group draft not confirmed: %+v", a.form)
				}
				mustClick(t, tt, m.Save())
				got, ok := a.store.Get(a.selected)
				if !ok || got.Group != "Release" || a.editing || !slices.Contains(a.store.Groups(), "Release") {
					t.Fatalf("new group not saved: %+v, editing=%v", got, a.editing)
				}
			})
		}
	}
}

func TestHostGroupDraftCancelAndValidation(t *testing.T) {
	a, tt := hostGroupTestEditor(t, true)
	m := locales.In("en")
	id := a.form.orig.ID
	mustClick(t, tt, m.NewGroup())
	tt.Type("   ")
	mustClick(t, tt, m.Save())
	if !a.editing || !a.form.addingGroup || a.form.err != m.RequiredField(m.FieldGroup()) {
		t.Fatal("blank group did not keep its draft and show a required-field error")
	}
	mustClick(t, tt, m.SnippetCancelGroup())
	if a.form.addingGroup || a.form.group != "Prod" || a.form.err != "" {
		t.Fatal("cancel did not restore the previous group")
	}
	mustClick(t, tt, m.NewGroup())
	tt.Type("  pRoD  ")
	mustClick(t, tt, m.SnippetAddGroup())
	if a.form.group != "Prod" {
		t.Fatalf("existing group spelling not reused: %q", a.form.group)
	}
	mustClick(t, tt, m.NewGroup())
	tt.Type("Discarded")
	mustClick(t, tt, m.SnippetAddGroup())
	mustClick(t, tt, m.Cancel())
	got, _ := a.store.Get(id)
	if a.editing || got.Group != "Prod" || !slices.Equal(a.store.Groups(), []string{"Empty", "Prod"}) {
		t.Fatalf("cancel changed saved data: host=%+v, groups=%q", got, a.store.Groups())
	}
}
