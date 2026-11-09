package app

import (
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

type backgroundView struct {
	a     *App
	tt    *ui.Tester
	check func()
}

// Controls keep working over a background image with transparent content.
func TestBackgroundContentViews(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func(*testing.T) backgroundView
	}{
		{"settings", backgroundSettingsView},
		{"host", backgroundHostView},
		{"ai", backgroundAIView},
		{"snippets", backgroundSnippetsView},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.make(t)
			renderBackgroundFixture(v.a)
			v.a.settings.Background.ChromeOpacity = 0
			v.a.settings.Background.ContentOpacity = 0
			v.tt.Frame()
			v.check()
		})
	}
}

func backgroundSettingsView(t *testing.T) backgroundView {
	a, tt := newTestApp(t)
	a.openSettings(prefAppearance)
	tt.Frame()
	return backgroundView{a, tt, func() {
		mustClick(t, tt, "Back")
		if a.page != pageWorkspace {
			t.Fatal("settings Back did not restore the workspace")
		}
	}}
}

func backgroundHostView(t *testing.T) backgroundView {
	a, tt := newTestApp(t)
	h := savedHost(t, a, "background host", "10.0.0.8")
	a.selected = h.ID
	a.ShowTool(layout.HostInfo)
	tt.Frame()
	return backgroundView{a, tt, func() {
		m := locales.In("en")
		mustClick(t, tt, m.AddFavorite())
		saved, _ := a.store.Get(h.ID)
		if !saved.Favorite {
			t.Fatal("host favorite action failed over background")
		}
		mustClick(t, tt, m.EditHost())
		tt.Frame()
		if !a.editing || !tt.Focused(m.FieldName()) {
			t.Fatal("host edit sheet did not open and focus the name")
		}
		mustClick(t, tt, "Cancel")
		if a.editing {
			t.Fatal("host edit Cancel did not close the sheet")
		}
	}}
}

func backgroundAIView(t *testing.T) backgroundView {
	a, tt := newTestApp(t)
	h := savedHost(t, a, "AI host", "10.0.0.8")
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := attachData("background-notes.txt", []byte("Disk usage notes"))
	if err != nil {
		t.Fatal(err)
	}
	a.conns[id].ai.items = []aiItem{
		{kind: "user", text: "Check the attached notes", files: []*aiAttach{attachment}},
		{kind: "text", text: "Read the disk usage\n\n```sh\ndf -h\n```\n"},
	}
	tt.Frame()
	return backgroundView{a, tt, func() {
		mustClick(t, tt, "Copy")
		if tt.Clipboard() != "df -h" {
			t.Fatalf("AI code copy: %q", tt.Clipboard())
		}
	}}
}

func backgroundSnippetsView(t *testing.T) backgroundView {
	a, tt := newTestApp(t)
	n, err := a.saveSnippet(assets.Snippet{Name: "background snippet"}, "echo background\npwd")
	if err != nil {
		t.Fatal(err)
	}
	a.ToggleTool(layout.Snippets)
	a.snippets.selected = n.ID
	tt.Frame()
	return backgroundView{a, tt, func() {
		m := locales.In("en")
		mustClick(t, tt, m.SnippetEditAction(n.Name))
		tt.Frame()
		if a.snippets.form == nil || a.snippets.form.content != "echo background\npwd" {
			t.Fatal("snippet edit lost the selected body")
		}
		mustClick(t, tt, m.Cancel())
	}}
}
