package app

import (
	"slices"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
)

// TestMultiSelectPicks: checking a group checks its subgroups and their
// hosts, unchecking it unchecks them; a group is checked only while all it
// holds is, mixed while some is. A click on a host while choosing several
// checks it, and leaving forgets the checks.
func TestMultiSelectPicks(t *testing.T) {
	a, tt := newTestApp(t)
	web := addGroupedHost(t, a, "web", "Prod/Web")
	db := addGroupedHost(t, a, "db", "Prod")
	dev := addGroupedHost(t, a, "dev", "Dev")
	tt.Frame()

	mustClick(t, tt, "Select multiple")
	if !a.multi.on {
		t.Fatal("not choosing several")
	}
	a.pickGroup("Prod", true)
	hosts, groups, tops := a.picked()
	if len(hosts) != 2 || !slices.Equal(groups, []string{"Prod", "Prod/Web"}) || !slices.Equal(tops, []string{"Prod"}) {
		t.Fatalf("picked %v %q %q", hosts, groups, tops)
	}
	a.pickGroup("Prod/Web", false)
	if a.multi.hosts[web.ID] || !a.multi.hosts[db.ID] || a.multi.groups["Prod/Web"] {
		t.Fatalf("subgroup unchecked: %+v", a.multi)
	}
	if a.groupPick("Prod") != pickSome || a.groupPick("Prod/Web") != pickNone {
		t.Fatalf("group partly checked shows %v, its subgroup %v", a.groupPick("Prod"), a.groupPick("Prod/Web"))
	}
	if _, _, tops := a.picked(); len(tops) != 0 {
		t.Fatalf("partly checked group picked as a whole: %q", tops)
	}
	// Its last host checked, the group is checked again; one unchecked, not.
	a.pickHost(web.ID, true)
	if a.groupPick("Prod") != pickFull || a.groupPick("Prod/Web") != pickFull {
		t.Fatal("group of all checked not checked")
	}
	a.pickHost(db.ID, false)
	if a.groupPick("Prod") != pickSome || a.groupPick("Prod/Web") != pickFull {
		t.Fatal("group with a host unchecked still checked")
	}
	a.pickHost(web.ID, false)
	if a.groupPick("Prod") != pickNone {
		t.Fatal("group of none checked not unchecked")
	}

	tt.Frame()
	mustClick(t, tt, "dev")
	if !a.multi.hosts[dev.ID] {
		t.Fatal("click did not check the host")
	}
	a.pickAll(true)
	if !a.allPicked() {
		t.Fatal("not all checked")
	}
	mustClick(t, tt, "Exit selection")
	if a.multi.on || len(a.multi.hosts) != 0 {
		t.Fatal("checks kept after leaving")
	}
}

// TestMultiSelectMove: the groups checked move with their subtree, the
// other hosts checked on their own; a host inside a group moved stays in
// it, and a group never moves into itself.
func TestMultiSelectMove(t *testing.T) {
	a, _ := newTestApp(t)
	web := addGroupedHost(t, a, "web", "Prod/Web")
	db := addGroupedHost(t, a, "db", "Prod")
	loose := addGroupedHost(t, a, "loose", "")
	addGroupedHost(t, a, "dev", "Dev")
	a.OpenMultiSelect()

	a.pickGroup("Prod/Web", true)
	a.pickHost(loose.ID, true)
	if err := a.MovePicked("Dev"); err != nil {
		t.Fatal(err)
	}
	if got := a.store.Groups(); !slices.Equal(got, []string{"Dev", "Dev/Web", "Prod"}) {
		t.Fatalf("groups %q", got)
	}
	for id, want := range map[string]string{web.ID: "Dev/Web", loose.ID: "Dev", db.ID: "Prod"} {
		if h, _ := a.store.Get(id); h.Group != want {
			t.Fatalf("%s in %q, want %q", h.Name, h.Group, want)
		}
	}
	if len(a.multi.hosts)+len(a.multi.groups) != 0 {
		t.Fatal("checks kept after moving")
	}

	// Into itself: refused; the rest still moves, db with its group,
	// checked as a whole once all it holds is.
	a.pickGroup("Dev", true)
	a.pickHost(db.ID, true)
	if err := a.MovePicked("Dev/Web"); err == nil {
		t.Fatal("group moved into its own subgroup")
	}
	if h, _ := a.store.Get(db.ID); h.Group != "Dev/Web/Prod" {
		t.Fatalf("host of a group checked as a whole in %q", h.Group)
	}
	if got := a.store.Groups(); !slices.Contains(got, "Dev/Web") {
		t.Fatalf("groups %q", got)
	}

	// To no group: the groups go to the top level.
	a.pickGroup("Dev/Web", true)
	if err := a.MovePicked(""); err != nil {
		t.Fatal(err)
	}
	if h, _ := a.store.Get(web.ID); h.Group != "Web" {
		t.Fatalf("web in %q", h.Group)
	}
}

// TestMultiSelectDelete: the hosts checked go, one jumped through by
// another checked after it, then the groups checked as a whole with their
// subgroups; a group partly checked stays with its hosts unchecked, and a
// host used by one kept is not deleted.
func TestMultiSelectDelete(t *testing.T) {
	a, _ := newTestApp(t)
	bastion := addGroupedHost(t, a, "bastion", "Prod")
	inner, err := a.store.Add(assets.Host{Name: "inner", Address: "inner", User: "u", Group: "Prod/Web", JumpChain: []string{bastion.ID}})
	if err != nil {
		t.Fatal(err)
	}
	kept := addGroupedHost(t, a, "kept", "Prod/Web")
	gate := addGroupedHost(t, a, "gate", "")
	user, err := a.store.Add(assets.Host{Name: "user", Address: "user", User: "u", JumpChain: []string{gate.ID}})
	if err != nil {
		t.Fatal(err)
	}
	addGroupedHost(t, a, "other", "Dev")
	a.OpenMultiSelect()

	a.pickGroup("Prod", true)
	a.pickHost(kept.ID, false)
	a.pickHost(gate.ID, true)
	if err := a.DeletePicked(); err == nil {
		t.Fatal("a host still jumped through was deleted without error")
	}
	for _, id := range []string{bastion.ID, inner.ID} {
		if _, ok := a.store.Get(id); ok {
			t.Fatalf("checked host %s kept", id)
		}
	}
	if _, ok := a.store.Get(gate.ID); !ok {
		t.Fatal("host jumped through by a kept host deleted")
	}
	if _, ok := a.store.Get(user.ID); !ok {
		t.Fatal("unchecked host deleted")
	}
	if h, ok := a.store.Get(kept.ID); !ok || h.Group != "Prod/Web" {
		t.Fatalf("unchecked host of a partly checked group: %+v %v", h, ok)
	}
	if got := a.store.Groups(); !slices.Equal(got, []string{"Dev", "Prod", "Prod/Web"}) {
		t.Fatalf("groups %q", got)
	}

	// Checked as a whole, a group goes with all it holds.
	a.pickGroup("Prod", true)
	if err := a.DeletePicked(); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.store.Get(kept.ID); ok {
		t.Fatal("host of a group deleted as a whole kept")
	}
	if got := a.store.Groups(); !slices.Equal(got, []string{"Dev"}) {
		t.Fatalf("groups %q", got)
	}
}
