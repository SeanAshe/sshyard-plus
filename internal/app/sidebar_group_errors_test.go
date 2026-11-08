package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSidebarGroupDropSaveError(t *testing.T) {
	a, tt := newTestApp(t)
	addGroupedHost(t, a, "alpha", "A")
	addGroupedHost(t, a, "bravo", "B")
	tt.Frame()
	// Make atomic writes fail without touching the user's data.
	path := filepath.Join(a.p.DataDir, "hosts.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	dst := mustFind(t, tt, "A")
	x, y := dst.X+8, dst.Y+1
	dragSidebarTo(t, tt, "B", x, y)
	tt.Release(x, y)
	tt.Frame()
	if !slices.Equal(a.store.Groups(), []string{"A", "B"}) {
		t.Fatal("failed write left a changed order")
	}
	if !strings.Contains(strings.Join(tt.Texts(), "\n"), "hosts.json") {
		t.Fatal("failed write did not show its error")
	}
}

func TestSidebarSectionsKeyboardAndEmpty(t *testing.T) {
	a, tt := newTestApp(t)
	mustClick(t, tt, "Expand all groups") // Empty sidebar: disabled, no state change.
	if len(a.sections) != 0 {
		t.Fatal("empty sidebar created section state")
	}
	if err := a.store.AddGroup("Empty"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// Tab from the search button reaches the toggle, past measuring the
	// hosts, which have none.
	mustClick(t, tt, "Search hosts")
	mustClick(t, tt, "Close search")
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("Collapse all groups") {
		t.Fatal("toggle is not keyboard accessible")
	}
	tt.Key(0, ui.KeyEnter)
	if *a.section("g:Empty") || !tt.HasText("Expand all groups") {
		t.Fatal("Enter did not collapse")
	}
	tt.Key(0, ui.KeySpace)
	if !*a.section("g:Empty") || !tt.HasText("Collapse all groups") {
		t.Fatal("Space did not expand")
	}
}
