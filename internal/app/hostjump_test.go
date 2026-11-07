package app

import (
	"fmt"
	"slices"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func hostJumpTestEditor(t *testing.T, edit bool, count int) (*App, *ui.Tester, []string) {
	t.Helper()
	a, _ := newTestApp(t)
	var jumps []string
	for i := 0; i < count; i++ {
		h := addGroupedHost(t, a, fmt.Sprintf("hop-%d", i), "")
		jumps = append(jumps, h.ID)
	}
	var original *assets.Host
	if edit {
		h := addGroupedHost(t, a, "target", "")
		h.JumpChain = slices.Clone(jumps)
		if _, err := a.store.Update(h); err != nil {
			t.Fatal(err)
		}
		original = &h
	}
	a.openEditor(original)
	a.form.name, a.form.address, a.form.user = "target", "target", "u"
	a.form.jumps = slices.Clone(jumps)
	// The deletion happens inside the list's row builder, so exercise
	// its real input/build cycle and assert only on host data.
	tt := ui.NewTester(func(c *ui.Context) {
		if a.form != nil {
			a.networkFields(c, locales.In("en"), currentTokens(c), a.form)
		}
	}, 600, 360)
	tt.Frame()
	return a, tt, jumps
}

func TestHostJumpRemove(t *testing.T) {
	for _, edit := range []bool{false, true} {
		for _, tc := range []struct {
			name          string
			count, remove int
		}{{"single", 1, 0}, {"first", 3, 0}, {"middle", 3, 1}, {"last", 3, 2}} {
			t.Run(fmt.Sprintf("edit=%v/%s", edit, tc.name), func(t *testing.T) {
				a, tt, jumps := hostJumpTestEditor(t, edit, tc.count)
				f, m := a.form, locales.In("en")
				button := mustFind(t, tt, m.RemoveJumpHost())
				row := mustFind(t, tt, a.hostLabel(jumps[tc.remove]))
				tt.ClickAt(button.X+button.W/2, row.Y+row.H/2)
				want := slices.Delete(slices.Clone(jumps), tc.remove, tc.remove+1)
				if !slices.Equal(f.jumps, want) {
					t.Fatalf("jump draft = %v, want %v", f.jumps, want)
				}
				if edit {
					got, _ := a.store.Get(f.orig.ID)
					if !slices.Equal(got.JumpChain, jumps) || !slices.Equal(f.orig.JumpChain, jumps) {
						t.Fatal("removing a draft jump changed the original chain")
					}
				}
				if !a.SaveForm(m) {
					t.Fatalf("save failed: %s", f.err)
				}
				got, ok := a.store.Get(a.selected)
				if !ok || !slices.Equal(got.JumpChain, want) {
					t.Fatalf("saved jump chain = %v, want %v", got.JumpChain, want)
				}
				a.openEditor(&got)
				tt.Frame()
				if !slices.Equal(a.form.jumps, want) {
					t.Fatalf("reopened jump chain = %v, want %v", a.form.jumps, want)
				}
			})
		}
	}
}

func TestHostJumpRemoveCancel(t *testing.T) {
	a, tt, jumps := hostJumpTestEditor(t, true, 3)
	id, m := a.form.orig.ID, locales.In("en")
	for i := range jumps {
		mustClick(t, tt, m.RemoveJumpHost())
		if !slices.Equal(a.form.jumps, jumps[i+1:]) {
			t.Fatalf("remaining jump chain = %v, want %v", a.form.jumps, jumps[i+1:])
		}
	}
	// Use the real Cancel action to discard the now-empty draft.
	editor := ui.NewTester(func(c *ui.Context) { a.editorView(c, m) }, 900, 760)
	editor.Frame()
	mustClick(t, editor, m.Cancel())
	got, _ := a.store.Get(id)
	if a.editing || a.form != nil || !slices.Equal(got.JumpChain, jumps) {
		t.Fatal("cancel did not discard only the draft")
	}
	a.openEditor(&got)
	tt.Frame()
	if !slices.Equal(a.form.jumps, jumps) {
		t.Fatalf("reopened jump chain = %v, want %v", a.form.jumps, jumps)
	}
}

func TestHostJumpReorderAfterRemove(t *testing.T) {
	a, tt, jumps := hostJumpTestEditor(t, true, 3)
	mustClick(t, tt, locales.In("en").RemoveJumpHost())
	a.form.jumpList.Reorder([]int{0}, 2)
	tt.Frame()
	want := []string{jumps[2], jumps[1]}
	if !slices.Equal(a.form.jumps, want) {
		t.Fatalf("reordered jump chain = %v, want %v", a.form.jumps, want)
	}
	mustClick(t, tt, locales.In("en").RemoveJumpHost())
	if !slices.Equal(a.form.jumps, want[1:]) {
		t.Fatalf("jump chain after reorder and remove = %v, want %v", a.form.jumps, want[1:])
	}
}
