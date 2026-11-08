package app

import (
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
)

// TestPrivacyMode masks the addresses the app shows, from the status
// bar's switch, and keeps the setting.
func TestPrivacyMode(t *testing.T) {
	a, tt := newTestApp(t)
	h, err := a.Store().Add(assets.Host{Address: "12.32.43.55", User: "root"})
	if err != nil {
		t.Fatal(err)
	}
	a.selected = h.ID
	tt.Frame()
	shows := func(s string) bool {
		for _, x := range tt.Texts() {
			if strings.Contains(x, s) {
				return true
			}
		}
		return false
	}
	if !shows("12.32.43.55") {
		t.Fatalf("no address in %q", tt.Texts())
	}
	mustClick(t, tt, "Privacy mode: mask IP addresses")
	tt.Frame()
	if !a.settings.Privacy {
		t.Fatal("privacy mode not on")
	}
	if shows("12.32.43.55") || !shows("1*.**.**.*5") {
		t.Errorf("address not masked: %q", tt.Texts())
	}
	mustClick(t, tt, "Privacy mode: mask IP addresses")
	tt.Frame()
	if a.settings.Privacy || !shows("12.32.43.55") {
		t.Errorf("privacy mode not off: %q", tt.Texts())
	}
}

// TestSidebarToolsAndGroups puts the add button beside the search, the
// host count at the foot, and makes, fills, renames and deletes a group.
func TestSidebarToolsAndGroups(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	h, err := a.Store().Add(assets.Host{Name: "web", Address: "10.0.0.5", User: "root"})
	if err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	add, _ := tt.Find("Add")
	search, _ := tt.Find("Search hosts")
	if d := (add.Y + add.H/2) - (search.Y + search.H/2); d < -6 || d > 6 {
		t.Errorf("add button at %v, search at %v: not one row", add, search)
	}
	if r, ok := tt.Find("1 host"); !ok || r.Y < 700 {
		t.Errorf("host count %v, %v: not at the foot", r, ok)
	}

	// A new group, from the add menu: it shows while empty.
	mustClick(t, tt, "Add")
	mustClick(t, tt, "New group")
	tt.Frame()
	if a.group == nil || !tt.HasText("New group") {
		t.Fatalf("no group sheet in %q", tt.Texts())
	}
	a.group.name = "Prod"
	if !a.SaveGroup(m) {
		t.Fatalf("not saved: %q", a.group.err)
	}
	tt.Frame()
	if !tt.HasText("Prod") || !tt.HasText("No hosts. Drag hosts here.") {
		t.Fatalf("no empty group in %q", tt.Texts())
	}
	// The same name again is refused.
	a.openGroupForm("", "")
	a.group.name = "prod"
	if a.SaveGroup(m) || a.group.err != m.GroupTaken() {
		t.Errorf("taken name: err %q", a.group.err)
	}
	a.grouping, a.group = false, nil
	tt.Frame()

	// The host moves in from its menu.
	if err := tt.RightClick("web"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(a, tt, "Move to group", "Prod"); err != nil {
		t.Fatalf("%v menu %q", err, menuOf(a))
	}
	tt.Frame()
	if got, _ := a.Store().Get(h.ID); got.Group != "Prod" || tt.HasText("No hosts. Drag hosts here.") {
		t.Fatalf("host in %q; texts %q", got.Group, tt.Texts())
	}

	// Renamed from the section's edit sheet, the host goes along.
	if err := tt.RightClick("Prod"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(a, tt, "Edit group"); err != nil {
		t.Fatalf("%v menu %q", err, menuOf(a))
	}
	tt.Frame()
	if a.group == nil || a.group.old != "Prod" {
		t.Fatal("no rename sheet")
	}
	a.group.name = "Live"
	if !a.SaveGroup(m) {
		t.Fatalf("not renamed: %q", a.group.err)
	}
	tt.Frame()
	if got, _ := a.Store().Get(h.ID); got.Group != "Live" || !tt.HasText("Live") || tt.HasText("Prod") {
		t.Fatalf("host in %q; texts %q", got.Group, tt.Texts())
	}

	// Deleted: the host stays, without a group.
	if err := tt.RightClick("Live"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(a, tt, "Delete group"); err != nil {
		t.Fatalf("%v menu %q", err, menuOf(a))
	}
	tt.Frame()
	if !tt.HasText("Delete the group Live? Its hosts are kept, without a group.") {
		t.Fatalf("no confirmation in %q", tt.Texts())
	}
	mustClick(t, tt, "Delete")
	tt.Frame()
	if got, ok := a.Store().Get(h.ID); !ok || got.Group != "" || len(a.Store().Groups()) != 0 {
		t.Errorf("after delete: host %+v, groups %q", got, a.Store().Groups())
	}
}

// TestHostAppearance sets a host's icon and color in the editor, and an
// emoji instead; a host without either looks the same in every run.
func TestHostAppearance(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	h, err := a.Store().Add(assets.Host{Name: "db", Address: "10.0.0.7", User: "root"})
	if err != nil {
		t.Fatal(err)
	}
	a.openEditor(&h)
	tt.Frame()
	mustClick(t, tt, "Database")
	mustClick(t, tt, "Purple")
	tt.Frame()
	if !a.SaveForm(m) {
		t.Fatalf("not saved: %q", a.form.err)
	}
	got, _ := a.Store().Get(h.ID)
	if got.Icon != assets.IconPrefix+"database" || got.Color != "purple" {
		t.Fatalf("saved icon %q, color %q", got.Icon, got.Color)
	}
	tok := theme.LightTokens()
	if ic, text := hostIcon(got); ic != icons.HostIcons[1].Icon || text != "" {
		t.Errorf("icon %v, text %q", ic, text)
	}
	if want, _ := tok.TintNamed("purple"); hostTint(tok, got) != want {
		t.Error("tint is not the one chosen")
	}

	// An emoji instead, typed.
	a.openEditor(&got)
	tt.Frame()
	tt.Advance(focusHoldFor) // the name field lets the keyboard go
	tt.Frame()
	mustClick(t, tt, "Emoji or text")
	tt.Type("🚀")
	tt.Frame()
	tt.Frame()
	if a.form.icon != "🚀" {
		t.Fatalf("form icon %q", a.form.icon)
	}
	mustClick(t, tt, "Automatic color")
	if !a.SaveForm(m) {
		t.Fatalf("not saved: %q", a.form.err)
	}
	got, _ = a.Store().Get(h.ID)
	if ic, text := hostIcon(got); ic != nil || text != "🚀" || got.Color != "" {
		t.Errorf("icon %v, text %q, color %q", ic, text, got.Color)
	}
	tt.Frame()
	if !tt.HasText("🚀") {
		t.Errorf("no emoji in the sidebar: %q", tt.Texts())
	}

	// Automatic: from the address, whatever the name or ID.
	one := assets.Host{ID: "a", Name: "x", Address: "192.168.1.20"}
	two := assets.Host{ID: "b", Name: "y", Address: "192.168.1.20"}
	ic1, _ := hostIcon(one)
	ic2, _ := hostIcon(two)
	if ic1 != ic2 || hostTint(tok, one) != hostTint(tok, two) {
		t.Error("one address, two looks")
	}
}

// TestLocalTerminal opens a shell on this computer from the status bar,
// splits it, and offers no files for it but an assistant that types
// into it.
func TestLocalTerminal(t *testing.T) {
	a, tt := newTestApp(t)
	mustClick(t, tt, "Local terminal")
	tt.Frame()
	cn := a.focused()
	if cn == nil || !isLocal(cn.host.ID) || cn.kind != layout.Terminal {
		t.Fatalf("no local terminal: %v", a.ws.Items())
	}
	waitForIn(t, tt, slowWait, "shell", func() bool { return cn.s.Status().State == session.Connected })
	if !tt.HasText("Local terminal") {
		t.Errorf("no title in %q", tt.Texts())
	}
	a.SplitFocused(layout.Right)
	tt.Frame()
	tab := a.ws.ActiveTab()
	if tab.Count(layout.Terminal) != 2 || !isLocal(a.focused().host.ID) {
		t.Fatalf("split: %v", a.ws.Items())
	}
	mustClick(t, tt, "Open Files for This Host")
	a.OpenFilesBeside()
	a.OpenAIBeside()
	tt.Frame()
	if tab.Count(layout.Files) != 0 || tab.Count(layout.AI) != 1 {
		t.Errorf("files, or no assistant, on this computer: %v", a.ws.Items())
	}
	if _, err := a.OpenFiles(localID, openAt{}, nil); err != errLocalTerminalOnly {
		t.Errorf("open files: %v", err)
	}

	// Another, with a tab open: under the focused pane, in its tab.
	above := a.focused()
	tabs := len(a.ws.Tabs)
	if _, err := a.OpenLocalTerminal(); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	below := a.focused()
	if len(a.ws.Tabs) != tabs || tab.Count(layout.Terminal) != 3 || below == above {
		t.Fatalf("not split in the tab: %d tabs, %v", len(a.ws.Tabs), a.ws.Items())
	}
	if r1, r2 := a.paneBounds[above.id], a.paneBounds[below.id]; r2.Y <= r1.Y+r1.H/2 || r2.X != r1.X {
		t.Errorf("new pane at %v, not under %v", r2, r1)
	}
}
