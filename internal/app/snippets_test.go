package app

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestSnippetUIManageWithoutTerminal(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	mustClick(t, tt, m.Snippets())
	tt.Frame()
	if a.snippetTarget() != nil {
		t.Fatal("a target without a terminal")
	}
	mustClick(t, tt, m.SnippetNew())
	tt.Frame()
	tt.Type("deploy")
	tt.Frame()
	if a.snippets.form.meta.Name != "deploy" {
		t.Fatal("autofocus input failed")
	}
	// TextInput owns the bound values, invoke the save handler directly.
	a.snippets.form.meta.Group = "work"
	a.snippets.form.content = "echo first\necho second"
	if !a.saveSnippetForm(m) {
		t.Fatal(a.snippets.form.err)
	}
	tt.Frame()
	n, _ := a.store.Snippet(a.snippets.selected)
	if n.Name != "deploy" || n.SendEnter {
		t.Fatal(n)
	}
	mustClick(t, tt, m.SnippetPreview(n.Name))
	mustClick(t, tt, m.SnippetPreview(n.Name))
	tt.Frame()
	if a.snippets.err != "" {
		t.Fatal("row should preview without sending", a.snippets.err)
	}
	mustClick(t, tt, m.SnippetEditAction(n.Name))
	tt.Frame()
	mustClick(t, tt, m.SnippetImmediate())
	tt.Frame()
	if !a.snippets.form.meta.SendEnter || a.snippets.err != "" {
		t.Fatal("switch sent or failed to toggle")
	}
	a.snippets.form.meta.Name = "deploy edited"
	if !a.saveSnippetForm(m) {
		t.Fatal("edit save failed")
	}
	tt.Frame()
	n, _ = a.store.Snippet(n.ID)
	if !n.SendEnter {
		t.Fatal("mode not saved")
	}
	mustClick(t, tt, m.SnippetDuplicateAction(n.Name))
	tt.Frame()
	if a.snippets.form.meta.ID != "" || a.snippets.form.meta.SendEnter || a.snippets.form.content != "echo first\necho second" {
		t.Fatal("bad duplicate")
	}
	a.snippets.form.meta.Name = "copy"
	if !a.saveSnippetForm(m) {
		t.Fatal("copy save failed")
	}
	tt.Frame()
	if len(a.store.Snippets()) != 2 {
		t.Fatal("copy missing")
	}
	mustClick(t, tt, m.SnippetDeleteAction("copy"))
	tt.Frame()
	if a.snippets.deleteID == "" || len(a.store.Snippets()) != 2 {
		t.Fatal("deleted without a confirmation")
	}
	mustClick(t, tt, m.Delete())
	tt.Frame()
	if len(a.store.Snippets()) != 1 {
		t.Fatal("delete failed")
	}
}

func TestSnippetUISearchGroupAndMissing(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	n, err := a.saveSnippet(assets.Snippet{Name: "alpha", Group: "Ops"}, "echo private-body-needle")
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.saveSnippet(assets.Snippet{Name: "beta", Group: "Other", SendEnter: true}, "echo different")
	if err != nil {
		t.Fatal(err)
	}
	mustClick(t, tt, m.Snippets())
	tt.Frame()
	mustClick(t, tt, m.SnippetSearch())
	tt.Type("BODY-NEEDLE")
	tt.Frame()
	if !tt.HasText("alpha") || tt.HasText("beta") {
		t.Fatal("body search", tt.Texts())
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Ops")
	tt.Frame()
	if !tt.HasText("alpha") || tt.HasText("beta") {
		t.Fatal("group search", tt.Texts())
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("ALPHA")
	tt.Frame()
	if !tt.HasText("alpha") || tt.HasText("beta") {
		t.Fatal("name search", tt.Texts())
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("")
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	// Close and reopen search input so a fresh query can be set through its handler.
	a.panels = nil
	tt.Frame()
	a.snippets.query = ""
	a.panels = []inspectorKind{inspectSnippets}
	tt.Frame()
	mustClick(t, tt, "Other")
	tt.Frame()
	if tt.HasText("alpha") || !tt.HasText("beta") {
		t.Fatal("group filter", tt.Texts())
	}
	a.snippets.grouped, a.snippets.group = false, ""
	a.snippets.selected = n.ID
	if err := a.kc.Delete(keychain.SnippetContent(n.ID)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(m.SnippetMissing()) {
		t.Fatal("missing body not visible")
	}
	a.openSnippet(m, n, false)
	tt.Frame()
	if a.snippets.form == nil || a.snippets.form.content != "" {
		t.Fatal("missing content cannot be edited")
	}
}

func TestSnippetUIErrorsRedacted(t *testing.T) {
	m := locales.In("en")
	for _, err := range []error{errors.New("/private/path secret command"), &snippetStoreError{stage: "metadata", err: errors.New("secret body")}, &snippetStoreError{stage: "rollback", err: errors.New("secret path")}, errSnippetUnsafePaste, errSnippetSend} {
		got := snippetErrorText(m, err)
		if got == "" || strings.Contains(got, "secret") || strings.Contains(got, "/private") {
			t.Fatalf("unsafe %q", got)
		}
	}
}

func TestSnippetUIFocusedTargetAndSend(t *testing.T) {
	spectatorSrv, spectatorShell := startScript(t, "ready")
	srv, shell := startScript(t, "\x1b[?2004hready")
	a, tt := newTestApp(t)
	spectatorHost := addHost(t, a, spectatorSrv, "spectator", "secret")
	a.known.Add(spectatorSrv.Addr, spectatorSrv.HostKey.PublicKey())
	a.Connect(spectatorHost.ID, openAt{})
	spectatorConn := a.focused()
	waitFor(t, tt, "spectator connected", func() bool { return spectatorConn.s.Status().State == session.Connected })
	cn := connected(t, a, tt, srv)
	m := locales.In("en")
	waitFor(t, tt, "bracketed paste", func() bool { return cn.s.Term.Modes().BracketedPaste })
	n, err := a.saveSnippet(assets.Snippet{Name: "send action"}, "echo UI marker")
	if err != nil {
		t.Fatal(err)
	}
	mustClick(t, tt, m.Snippets())
	tt.Frame()
	shell.take()
	spectatorShell.take()
	if a.snippetTarget() != cn {
		t.Fatal("target is not the focused terminal")
	}
	mustClick(t, tt, m.SnippetPreview(n.Name))
	mustClick(t, tt, m.SnippetPreview(n.Name))
	tt.Frame()
	if got := shell.take(); got != "" {
		t.Fatalf("selection sent %q", got)
	}
	mustClick(t, tt, m.SnippetInsertAction(n.Name))
	got := ""
	waitFor(t, tt, "UI snippet bytes", func() bool { got += shell.take(); return strings.Contains(got, "echo UI marker") })
	if got != "\x1b[200~echo UI marker\x1b[201~" || !a.panelOpen(inspectSnippets) {
		t.Fatalf("send %q panels %q", got, a.panels)
	}

	tt.Key(0, ui.KeyEnter)
	entered := ""
	waitFor(t, tt, "manual Enter after insert", func() bool { entered += shell.take(); return entered != "" })
	if entered != "\r" {
		t.Fatalf("manual Enter sent %q", entered)
	}
	waitFor(t, tt, "insert completed", func() bool { return !cn.tv.snippetSending })
	n.SendEnter = true
	if _, err := a.saveSnippet(n, "echo UI marker"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	mustClick(t, tt, m.SnippetSendAction(n.Name))
	immediate := ""
	wantImmediate := "\x1b[200~echo UI marker\x1b[201~\r"
	waitFor(t, tt, "immediate send bytes", func() bool { immediate += shell.take(); return len(immediate) >= len(wantImmediate) })
	if immediate != wantImmediate {
		t.Fatalf("immediate send %q", immediate)
	}
	if extra := spectatorShell.take(); extra != "" {
		t.Fatalf("unfocused terminal received %q", extra)
	}
	waitFor(t, tt, "immediate completed", func() bool { return !cn.tv.snippetSending })
	n.SendEnter = false
	if _, err := a.saveSnippet(n, "echo UI marker"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// Render the panel alone while checking each pane kind, the terminal fixture
	// intentionally has no file/editor/AI backing state.
	panel := ui.NewTester(func(c *ui.Context) { a.snippetsPanel(c, m, currentTokens(c)) }, 360, 560)
	// Any nonterminal focused pane must disable sending without guessing a target.
	for _, kind := range []layout.Kind{layout.Files, layout.Editor, layout.AI} {
		cn.kind = kind
		panel.Frame()
		if a.snippetTarget() != nil {
			t.Fatalf("nonterminal %s has target", kind)
		}
		mustClick(t, panel, m.SnippetInsertAction(n.Name))
		panel.Frame()
		if extra := shell.take(); extra != "" {
			t.Fatalf("nonterminal sent %q", extra)
		}
	}
	cn.kind = layout.Terminal
}

func TestSnippetUIFilterFollowsSavedAndDeletedGroup(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	n, err := a.saveSnippet(assets.Snippet{Name: "move", Group: "old"}, "echo move")
	if err != nil {
		t.Fatal(err)
	}
	a.panels = []inspectorKind{inspectSnippets}
	a.snippets.grouped, a.snippets.group = true, "old"
	a.openSnippet(m, n, false)
	tt.Frame()
	a.snippets.form.meta.Group = "new"
	if !a.saveSnippetForm(m) {
		t.Fatal("save failed")
	}
	tt.Frame()
	if !a.snippets.grouped || a.snippets.group != "new" {
		t.Fatal("filter did not follow the saved group")
	}
	a.snippets.deleteID = n.ID
	a.confirmSnippetDelete(m)
	tt.Frame()
	if a.snippets.grouped || a.snippets.group != "" {
		t.Fatal("stale group filter")
	}
}

func TestSnippetUIWelcomeActionsWithPanel(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	a.panels = []inspectorKind{inspectSnippets}
	tt.SetSize(820, 520)
	tt.Frame()
	for _, label := range []string{m.QuickMigration(), m.QuickConnect(), m.NewHost()} {
		mustClick(t, tt, label)
		tt.Frame()
		if label == m.QuickMigration() {
			if a.page != pageSettings || a.prefNow() != prefMigration || a.importing {
				t.Fatal("migration settings unavailable")
			}
			mustClick(t, tt, m.Back())
		}
		if label == m.QuickConnect() {
			if !a.quicking {
				t.Fatal("quick connect unavailable")
			}
			a.quicking = false
			a.quick = nil
		}
		if label == m.NewHost() {
			if !a.editing {
				t.Fatal("new host unavailable")
			}
			a.editing = false
			a.form = nil
		}
		tt.Frame()
	}
}

func TestSnippetUIDefaultGroup(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	if _, err := a.saveSnippet(assets.Snippet{Name: "named", Group: "Ops"}, "echo named"); err != nil {
		t.Fatal(err)
	}
	mustClick(t, tt, m.Snippets())
	tt.Frame()
	mustClick(t, tt, m.SnippetNew())
	tt.Frame()
	if a.snippets.form.meta.Group != "" {
		t.Fatal("new snippet not in the default group")
	}
	tt.Type("plain")
	tt.Frame()
	a.snippets.form.content = "echo plain"
	if !a.saveSnippetForm(m) {
		t.Fatal(a.snippets.form.err)
	}
	tt.Frame()
	n, _ := a.store.Snippet(a.snippets.selected)
	if n.Name != "plain" || n.Group != "" {
		t.Fatal(n)
	}
	mustClick(t, tt, m.SnippetDefaultGroup())
	tt.Frame()
	if !a.snippets.grouped || a.snippets.group != "" || !tt.HasText("plain") || tt.HasText("named") {
		t.Fatal("default group filter", tt.Texts())
	}
	// A new entry starts in the group the list shows.
	a.snippets.grouped, a.snippets.group = true, "Ops"
	mustClick(t, tt, m.SnippetNew())
	tt.Frame()
	if a.snippets.form.meta.Group != "Ops" {
		t.Fatal("new entry not in the filtered group")
	}
	// Searching for the default group's name finds its entries.
	a.snippets.showing = false
	tt.Frame()
	a.snippets.grouped, a.snippets.group = false, ""
	if !a.snippetMatches(m, n, "") || !a.snippetMatches(m, assets.Snippet{Name: "x"}, "") {
		t.Fatal("blank query")
	}
	a.snippets.query = "default"
	if !a.snippetMatches(m, n, "echo plain") || a.snippetMatches(m, assets.Snippet{Name: "named", Group: "Ops"}, "echo named") {
		t.Fatal("default group search")
	}
}

func TestSnippetUIAddGroupInForm(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	if _, err := a.saveSnippet(assets.Snippet{Name: "named", Group: "Ops"}, "echo named"); err != nil {
		t.Fatal(err)
	}
	mustClick(t, tt, m.Snippets())
	tt.Frame()
	mustClick(t, tt, m.SnippetNew())
	tt.Frame()
	tt.Type("deploy")
	tt.Frame()
	mustClick(t, tt, m.NewGroup())
	tt.Frame()
	if !a.snippets.form.adding {
		t.Fatal("new group field missing")
	}
	// The name field's focus hold yields to the new field.
	tt.Type("Release")
	tt.Frame()
	mustClick(t, tt, m.SnippetAddGroup())
	tt.Frame()
	f := a.snippets.form
	if f.adding || f.meta.Group != "Release" || f.err != "" {
		t.Fatal("group not chosen", f.meta.Group, f.err)
	}
	// Enter adds the group as the button does.
	mustClick(t, tt, m.NewGroup())
	tt.Frame()
	tt.Type("Hotfix")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if f.adding || f.meta.Group != "Hotfix" || !a.snippets.showing {
		t.Fatal("enter new group", f.adding, f.meta.Group, a.snippets.showing)
	}
	mustClick(t, tt, m.NewGroup())
	tt.Frame()
	tt.Type("Release")
	mustClick(t, tt, m.SnippetAddGroup())
	tt.Frame()
	f.content = "echo deploy"
	if !a.saveSnippetForm(m) {
		t.Fatal(f.err)
	}
	tt.Frame()
	n, _ := a.store.Snippet(a.snippets.selected)
	if n.Name != "deploy" || n.Group != "Release" {
		t.Fatal(n)
	}
	if got := a.snippetGroups(); !slices.Equal(got, []string{"Ops", "Release"}) {
		t.Fatal(got)
	}

	// Names are checked and matched by the handler, the input owning its text.
	a.openSnippet(m, n, false)
	tt.Frame()
	f = a.snippets.form
	for _, c := range []struct{ in, want, err string }{
		{"  ", "Release", m.RequiredField(m.FieldGroup())},
		{strings.Repeat("g", 81), "Release", m.SnippetTooLong(m.FieldGroup())},
		{" ops ", "Ops", ""},
		{"DEFAULT", "", ""},
		{"默认", "", ""},
		{"New", "New", ""},
	} {
		f.meta.Group = "Release"
		f.adding, f.newGroup = true, c.in
		ok := a.addSnippetGroup(m, f)
		if ok != (c.err == "") || f.meta.Group != c.want || f.err != c.err || f.adding != !ok {
			t.Fatalf("%q: ok=%v group=%q err=%q adding=%v", c.in, ok, f.meta.Group, f.err, f.adding)
		}
	}
	// Cancel leaves the choice as it was.
	f.adding, f.newGroup, f.err = true, "", ""
	tt.Frame()
	mustClick(t, tt, m.SnippetCancelGroup())
	tt.Frame()
	if f.adding || f.meta.Group != "New" || !a.snippets.showing {
		t.Fatal("cancel new group", f.adding, f.meta.Group, a.snippets.showing)
	}
}

func TestSnippetUITabsAndSearch(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	a.panels = []inspectorKind{inspectSnippets}
	tt.Frame()
	a.snippets.form, a.snippets.showing = nil, false
	if _, err := a.saveSnippet(assets.Snippet{Name: "plain"}, "echo plain"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []assets.Snippet{{Name: "restart", Group: "Ops"}, {Name: "logs", Group: "Ops"}} {
		if _, err := a.saveSnippet(n, "echo "+n.Name); err != nil {
			t.Fatal(err)
		}
	}
	tt.Frame()
	mustClick(t, tt, "Ops")
	tt.Frame()
	if !a.snippets.grouped || a.snippets.group != "Ops" || tt.HasText("plain") || !tt.HasText("restart") {
		t.Fatal("tab filter", tt.Texts())
	}
	mustClick(t, tt, m.SnippetAll())
	tt.Frame()
	if a.snippets.grouped || !tt.HasText("plain") {
		t.Fatal("all tab", tt.Texts())
	}

	// The search narrows the list; its button clears it.
	a.panels = nil
	tt.Frame()
	a.snippets.query = "restart"
	a.panels = []inspectorKind{inspectSnippets}
	tt.Frame()
	if tt.HasText("plain") || tt.HasText("logs") || !tt.HasText("restart") {
		t.Fatal("search", tt.Texts())
	}
	mustClick(t, tt, m.ClearSearch())
	tt.Frame()
	if a.snippets.query != "" || !tt.HasText("plain") {
		t.Fatal("clear search", a.snippets.query)
	}
}

func TestSnippetGroupNameLanguages(t *testing.T) {
	for _, lang := range settings.Languages {
		if got := snippetGroupName("  " + locales.In(lang).SnippetDefaultGroup() + "  "); got != "" {
			t.Errorf("%s default group = %q", lang, got)
		}
	}
	if got := snippetGroupName(" Ops "); got != "Ops" {
		t.Errorf("named group = %q", got)
	}
}
