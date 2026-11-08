package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/egoist/mygo/ui"
)

// Exercise the normal shutdown/startup boundary, including a nested mixed tree;
// pane IDs are runtime identities and must be remapped rather than reused.
func TestLayoutWorkspaceRoundTrip(t *testing.T) {
	dir, backend := t.TempDir(), keychain.NewMemory()
	rememberLayoutIn(t, dir)
	a, _ := appIn(t, dir, backend)
	h := savedHost(t, a, "saved", "127.0.0.1")
	term, err := a.makeConn(h.ID, layout.Terminal, 0)
	if err != nil {
		t.Fatal(err)
	}
	a.ws.Open(term.item()).Title = "Workspace Alpha"
	files, err := a.makeConn(h.ID, layout.Files, 0)
	if err != nil {
		t.Fatal(err)
	}
	a.ws.SplitFocused(layout.Row, files.item())
	ai, err := a.makeConn(h.ID, layout.AI, term.id)
	if err != nil {
		t.Fatal(err)
	}
	ai.ai.mode, ai.ai.provider, ai.ai.model, ai.ai.effort = "review", "provider", "model", "high"
	a.ws.SplitFocused(layout.Column, ai.item())
	first := a.ws.ActiveTab()
	first.Root.Ratio, first.Root.B.Ratio = 0.3, 0.65
	a.ws.Activate(files.id)
	other, err := a.makeConn(h.ID, layout.Terminal, 0)
	if err != nil {
		t.Fatal(err)
	}
	a.ws.Open(other.item()).Title = "Workspace Beta"
	a.ws.Active = 0
	closed, pct := false, float32(37)
	a.sections["none"], a.stackPct["host/tmux/"] = &closed, &pct
	a.panels = []inspectorKind{inspectHost, inspectTmux}
	term.cwd = "PRIVATE_CWD"
	setStartOnce(term, "PRIVATE_START_COMMAND")
	files.fs.remote.dir = "PRIVATE_REMOTE_DIRECTORY"
	ai.ai.input, ai.ai.task = "PRIVATE_AI_DRAFT", "PRIVATE_AI_TASK"
	a.Close()
	raw, err := os.ReadFile(filepath.Join(dir, "layout.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"PRIVATE_", `"path"`, `"cwd"`, `"startCmd"`, `"password"`, `"history"`, `"transfers"`} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("layout contains session content %q: %s", private, raw)
		}
	}
	again, tt := appIn(t, dir, backend)
	if len(again.ws.Tabs) != 2 || again.ws.Active != 0 {
		t.Fatalf("tabs=%d active=%d", len(again.ws.Tabs), again.ws.Active)
	}
	first = again.ws.Tabs[0]
	if first.Title != "Workspace Alpha" || again.ws.Tabs[1].Title != "Workspace Beta" || first.Focused().Kind != layout.Files {
		t.Fatalf("titles/focus: %+v %+v", first, again.ws.Tabs[1])
	}
	r := first.Root
	if r.Leaf() || r.Axis != layout.Row || r.Ratio != 0.3 || r.B.Leaf() || r.B.Axis != layout.Column || r.B.Ratio != 0.65 {
		t.Fatalf("split tree changed: %+v", r)
	}
	terminal, filePane, assistant := again.conns[r.A.Item.ID], again.conns[r.B.A.Item.ID], again.conns[r.B.B.Item.ID]
	if terminal.kind != layout.Terminal || filePane.kind != layout.Files || assistant.kind != layout.AI || assistant.ai.link != terminal.id {
		t.Fatal("mixed pane order or AI terminal association changed")
	}
	if terminal.cwd != "" || startOnce(terminal) != "" || assistant.ai.input != "" || assistant.ai.task != "" || len(assistant.ai.items) != 0 {
		t.Fatal("restored session content")
	}
	if assistant.ai.mode != "review" || assistant.ai.provider != "provider" || assistant.ai.model != "model" || assistant.ai.effort != "high" {
		t.Fatal("AI choices changed")
	}
	if again.sections["none"] == nil || *again.sections["none"] || !reflect.DeepEqual(again.panels, []inspectorKind{inspectHost, inspectTmux}) || again.stackPct["host/tmux/"] == nil || *again.stackPct["host/tmux/"] != 37 {
		t.Fatal("sidebar or inspector layout changed")
	}
	if again.prompting {
		t.Fatalf("restoration asked for a password; texts %q", tt.Texts())
	}
}

func TestLayoutLockedStartupReconnectsOnceWithoutReplay(t *testing.T) {
	srv, sh := startScript(t, "PRIVATE_OLD_OUTPUT\r\n")
	dir, backend := t.TempDir(), keychain.NewMemory()
	rememberLayoutIn(t, dir)
	a, tt := appIn(t, dir, backend)
	old := connected(t, a, tt, srv)
	waitFor(t, tt, "old output", func() bool { return strings.Contains(old.s.Term.ScreenText(), "PRIVATE_OLD_OUTPUT") })
	if err := old.s.Send([]byte("PRIVATE_OLD_COMMAND\r")); err != nil {
		t.Fatal(err)
	}
	expectRead(t, tt, sh, "PRIVATE_OLD_COMMAND")
	old.cwd = "PRIVATE_CWD"
	setStartOnce(old, "PRIVATE_START_COMMAND")
	s := a.Settings()
	s.OpenLocalOnStart = true
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	a.Close()
	b := &authorizationBackend{Memory: backend}
	again, err := New(Platform{DataDir: dir, Secrets: b, HomeDir: dir, CacheDir: filepath.Join(dir, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(again.Close)
	again.Start()
	tt = ui.NewTester(again.View, 1200, 760)
	tt.SetLocale("en")
	for range 4 {
		tt.Frame()
	}
	if len(again.ws.Tabs) != 1 || len(again.conns) != 1 || again.focused() == nil || again.focused().host.ID != old.host.ID {
		t.Fatalf("restored remote plus duplicate local: tabs=%d conns=%d", len(again.ws.Tabs), len(again.conns))
	}
	if b.calls.Load() != 0 || srv.Logins() != 1 || again.prompting || again.unlock.modal {
		t.Fatalf("startup interacted: keychain=%d logins=%d", b.calls.Load(), srv.Logins())
	}
	again.withSecrets(nil)
	waitFor(t, tt, "unlock and fresh SSH login", func() bool { return again.unlocked() && again.focused().s.Status().State == session.Connected })
	fresh := again.focused()
	if fresh.cwd != "" || startOnce(fresh) != "" || strings.Contains(fresh.s.Term.ScreenText(), "PRIVATE_") {
		t.Fatal("restarted terminal retained session content")
	}
	if err := fresh.s.Send([]byte("FRESH_PROBE\r")); err != nil {
		t.Fatal(err)
	}
	var read string
	waitFor(t, tt, "fresh shell input", func() bool { read += sh.take(); return strings.Contains(read, "FRESH_PROBE") })
	if strings.Contains(read, "PRIVATE_") || srv.Logins() != 2 || len(again.ws.Tabs) != 1 {
		t.Fatalf("replayed commands or started duplicate session: input=%q logins=%d tabs=%d", read, srv.Logins(), len(again.ws.Tabs))
	}
}

type layoutSilentBackend struct{ *authorizationBackend }

func (b layoutSilentBackend) GetSilently(key string) ([]byte, error) { return b.Memory.Get(key) }

func TestLayoutSilentUnlockStartsSavedConnection(t *testing.T) {
	srv, _ := startScript(t, "ready\r\n")
	dir, backend := t.TempDir(), keychain.NewMemory()
	rememberLayoutIn(t, dir)
	a, tt := appIn(t, dir, backend)
	connected(t, a, tt, srv)
	a.Close()
	b := layoutSilentBackend{&authorizationBackend{Memory: backend}}
	again, err := New(Platform{DataDir: dir, Secrets: b, HomeDir: dir, CacheDir: filepath.Join(dir, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(again.Close)
	again.Start()
	tt = ui.NewTester(again.View, 1200, 760)
	tt.SetLocale("en")
	waitFor(t, tt, "silent unlock and restored login", func() bool {
		return again.unlocked() && again.focused() != nil && again.focused().s.Status().State == session.Connected
	})
	if b.calls.Load() != 0 || srv.Logins() != 2 || again.prompting || again.unlock.modal {
		t.Fatalf("silent startup interacted or duplicated login: keychain=%d logins=%d", b.calls.Load(), srv.Logins())
	}
}

// A layout saved while the snippets and the command history were panels
// still restores, without them.
func TestLayoutDropsPanelsNowPanes(t *testing.T) {
	dir := t.TempDir()
	rememberLayoutIn(t, dir)
	raw, err := json.Marshal(layoutSnap{Version: 1, Panels: []inspectorKind{inspectHost, "snippets", "history", inspectTmux}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "layout.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	a, _ := appIn(t, dir, nil)
	if a.layoutBlocked || !reflect.DeepEqual(a.panels, []inspectorKind{inspectHost, inspectTmux}) {
		t.Fatalf("blocked=%v panels=%q", a.layoutBlocked, a.panels)
	}
}
