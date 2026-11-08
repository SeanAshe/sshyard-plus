package assets

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestSubgroups: a subgroup is a path in its group, which shows while only
// its subgroups hold hosts; renaming or deleting a group takes its
// subgroups along, and groups nest one level only.
func TestSubgroups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.json")
	s, _ := Open(path)
	web, _ := s.Add(Host{Name: "web", Address: "10.0.0.5", User: "u", Group: " Prod / Web "})
	db, _ := s.Add(Host{Name: "db", Address: "10.0.0.6", User: "u", Group: "Prod"})
	if web.Group != "Prod/Web" {
		t.Fatalf("host group not cleaned: %q", web.Group)
	}
	if err := s.AddGroup("Dev"); err != nil {
		t.Fatal(err)
	}
	// The group is spelled as it already is.
	if err := s.AddGroup("prod / Cache"); err != nil {
		t.Fatal(err)
	}
	checkGroupOrder(t, s, "Dev", "Prod", "Prod/Cache", "Prod/Web")

	var fe *FieldError
	for name, reason := range map[string]string{
		"prod/web":   "taken",
		"Prod/":      "required",
		"/Web":       "required",
		"Prod/Web/x": "depth",
	} {
		if err := s.AddGroup(name); !errors.As(err, &fe) || fe.Reason != reason {
			t.Fatalf("AddGroup(%q): %v, want %s", name, err, reason)
		}
	}
	if err := s.RenameGroup("Prod", "Dev/Prod"); !errors.As(err, &fe) || fe.Reason != "depth" {
		t.Fatalf("a group with subgroups made a subgroup: %v", err)
	}

	if err := s.RenameGroup("Prod", "Live"); err != nil {
		t.Fatal(err)
	}
	s, _ = Open(path)
	checkGroupOrder(t, s, "Dev", "Live", "Live/Cache", "Live/Web")
	if h, _ := s.Get(web.ID); h.Group != "Live/Web" {
		t.Fatalf("subgroup host after renaming its group: %q", h.Group)
	}
	if err := s.RenameGroup("Live/Web", "Dev/Web"); err != nil {
		t.Fatal(err)
	}
	checkGroupOrder(t, s, "Dev", "Dev/Web", "Live", "Live/Cache")

	// Siblings reorder; a group never moves among another level's.
	if err := s.MoveGroup("Live", "Dev", false); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveGroup("Live/Cache", "Dev", false); err != nil {
		t.Fatal(err)
	}
	checkGroupOrder(t, s, "Live", "Live/Cache", "Dev", "Dev/Web")

	if err := s.DeleteGroup("Dev"); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.Get(web.ID); h.Group != "" {
		t.Fatalf("host of a deleted subgroup is in %q", h.Group)
	}
	if h, _ := s.Get(db.ID); h.Group != "Live" {
		t.Fatalf("host of another group moved to %q", h.Group)
	}
	checkGroupOrder(t, s, "Live", "Live/Cache")
}

// TestSubgroupLegacyPath: a deeper path, as imported, is a subgroup named
// by all after its group, and renaming it does not touch others.
func TestSubgroupLegacyPath(t *testing.T) {
	s, _ := Open("")
	a, _ := s.Add(Host{Name: "a", Address: "a", User: "u", Group: "Org/Team/App"})
	b, _ := s.Add(Host{Name: "b", Address: "b", User: "u", Group: "Org/Team"})
	if p, l := GroupParent(a.Group), GroupLeaf(a.Group); p != "Org" || l != "Team/App" {
		t.Fatalf("parent %q leaf %q", p, l)
	}
	if err := s.RenameGroup("Org/Team", "Org/Ops"); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.Get(a.ID); h.Group != "Org/Team/App" {
		t.Fatalf("sibling subgroup renamed: %q", h.Group)
	}
	if h, _ := s.Get(b.ID); h.Group != "Org/Ops" {
		t.Fatalf("renamed subgroup: %q", h.Group)
	}
}

// TestGroupLooks: a group's look is saved with it, normalized, follows
// it and its subgroups when renamed, goes when deleted, and is restored
// for the groups a backup brings.
func TestGroupLooks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.json")
	s, _ := Open(path)
	if err := s.AddGroupLook("Prod", GroupLook{Icon: " icon:database ", Color: "nope"}); err != nil {
		t.Fatal(err)
	}
	if got := s.GroupLook("Prod"); got != (GroupLook{Icon: "icon:database"}) {
		t.Fatalf("normalized look %+v", got)
	}
	if err := s.AddGroupLook("Prod/Web", GroupLook{Icon: "🚀", Color: "green"}); err != nil {
		t.Fatal(err)
	}
	// Renamed and given a new look in one edit; the subgroup's look follows.
	if err := s.EditGroup("Prod", "Live", GroupLook{Color: "red"}); err != nil {
		t.Fatal(err)
	}
	s, _ = Open(path)
	if got := s.GroupLook("Live"); got != (GroupLook{Color: "red"}) {
		t.Fatalf("edited look %+v", got)
	}
	if got := s.GroupLook("Live/Web"); got != (GroupLook{Icon: "🚀", Color: "green"}) {
		t.Fatalf("subgroup look after renaming its group %+v", got)
	}
	if s.GroupLook("Prod") != (GroupLook{}) || s.GroupLook("Prod/Web") != (GroupLook{}) {
		t.Fatal("look left under the old name")
	}
	// Renaming alone keeps the look; the plain look is no entry.
	if err := s.RenameGroup("Live", "Prod"); err != nil {
		t.Fatal(err)
	}
	if got := s.GroupLook("Prod"); got != (GroupLook{Color: "red"}) {
		t.Fatalf("look after renaming %+v", got)
	}
	if err := s.EditGroup("Prod/Web", "Prod/Web", GroupLook{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Snapshot().GroupLooks["Prod/Web"]; ok {
		t.Fatal("plain look kept")
	}

	snap := s.Snapshot()
	restored, _ := Open("")
	if err := restored.Restore(Snapshot{Groups: []string{"Other"}, GroupLooks: map[string]GroupLook{"Missing": {Color: "red"}, "Other": {Color: "blue"}}}); err != nil {
		t.Fatal(err)
	}
	if err := restored.Restore(snap); err != nil {
		t.Fatal(err)
	}
	if restored.GroupLook("Prod") != (GroupLook{Color: "red"}) || restored.GroupLook("Other") != (GroupLook{Color: "blue"}) {
		t.Fatalf("restored looks %+v", restored.Snapshot().GroupLooks)
	}
	if _, ok := restored.Snapshot().GroupLooks["Missing"]; ok {
		t.Fatal("look of a group not restored kept")
	}

	if err := s.DeleteGroup("Prod"); err != nil {
		t.Fatal(err)
	}
	if looks := s.Snapshot().GroupLooks; len(looks) != 0 {
		t.Fatalf("looks after deleting %+v", looks)
	}
}

func TestGroupLookWriteRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.json")
	s, _ := Open(path)
	if err := s.AddGroupLook("A", GroupLook{Color: "red"}); err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	for _, fn := range []func() error{
		func() error { return s.EditGroup("A", "Z", GroupLook{Color: "blue"}) },
		func() error { return s.EditGroup("A", "A", GroupLook{}) },
		func() error { return s.DeleteGroup("A") },
		func() error { return s.AddGroupLook("B", GroupLook{Color: "blue"}) },
		func() error { return s.Restore(Snapshot{GroupLooks: map[string]GroupLook{"A": {Color: "blue"}}}) },
	} {
		if err := fn(); err == nil {
			t.Fatal("write unexpectedly succeeded")
		}
		if !reflect.DeepEqual(before, s.Snapshot()) {
			t.Fatal("failed write changed the store")
		}
	}
}
