package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/locales"
)

func TestLayoutLocalShellRestoresWithoutStartupDuplicate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture runs /bin/sh")
	}
	withShells(t, session.Shell{ID: "fixture-sh", Name: "Fixture shell", Path: "/bin/sh"})
	dir, backend := t.TempDir(), keychain.NewMemory()
	rememberLayoutIn(t, dir)
	a, tt := appIn(t, dir, backend)
	id, err := a.OpenLocalShell("fixture-sh")
	c := shellPane(t, a, id, err)
	waitFor(t, tt, "local shell", func() bool { return c.s.Status().State == session.Connected })
	a.ws.ActiveTab().Title = "Local workspace"
	s := a.Settings()
	s.OpenLocalOnStart = true
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	a.Close()
	again, tt := appIn(t, dir, backend)
	waitFor(t, tt, "restored local shell", func() bool {
		return again.focused() != nil && again.focused().s.Status().State == session.Connected
	})
	if len(again.conns) != 1 || len(again.ws.Tabs) != 1 || again.focused().host.ID != localID+"fixture-sh" || again.ws.ActiveTab().Title != "Local workspace" {
		t.Fatalf("local shell identity or tab changed: tabs=%d panes=%d", len(again.ws.Tabs), len(again.conns))
	}
	if err := again.focused().s.Send([]byte("printf 'RESTORED_%s\\n' 'LOCAL_OK'\n")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "restored shell command", func() bool {
		return bytes.Contains([]byte(again.focused().s.Term.ScreenText()), []byte("RESTORED_LOCAL_OK"))
	})
}

func TestLayoutViewDebouncesWorkspaceChanges(t *testing.T) {
	dir := t.TempDir()
	rememberLayoutIn(t, dir)
	a, tt := newTestAppIn(t, dir)
	h := savedHost(t, a, "saved", "127.0.0.1")
	for range 2 {
		c, err := a.makeConn(h.ID, layout.Terminal, 0)
		if err != nil {
			t.Fatal(err)
		}
		a.ws.SplitFocused(layout.Row, c.item())
	}
	first := a.ws.ActiveTab()
	other, err := a.makeConn(h.ID, layout.AI, 0)
	if err != nil {
		t.Fatal(err)
	}
	a.ws.Open(other.item())
	tt.Frame()
	a.saveLayout()
	file := filepath.Join(a.p.DataDir, "layout.json")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	a.RenameTab(first.ID, "Live workspace")
	a.ws.Activate(first.Root.A.Item.ID)
	*a.ratio(first.Root) = 33
	*a.section("none") = false
	a.openPanel(inspectHost)
	a.openPanel(inspectTmux)
	pct := float32(42)
	a.stackPct["host/tmux/"] = &pct
	tt.Frame()
	tt.Advance(100 * time.Millisecond)
	after, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("layout saved before debounce: err=%v", err)
	}
	tt.Advance(time.Second)
	after, err = os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var saved layoutSnap
	if err := json.Unmarshal(after, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Active != 0 || len(saved.Tabs) != 2 || saved.Tabs[0].Title != "Live workspace" || saved.Tabs[0].Focus != first.Root.A.ID || saved.Tabs[0].Root.Ratio != float32(33)/100 {
		t.Fatalf("live title, focus, active or ratio not saved: %s", after)
	}
	closed, exists := saved.Sections["none"]
	if !exists || closed || !reflect.DeepEqual(saved.Panels, []inspectorKind{inspectHost, inspectTmux}) || saved.StackPct["host/tmux/"] != 42 {
		t.Fatalf("live sidebar or inspector not saved: %s", after)
	}
}

func TestLayoutWriteFailureShowsErrorAndPreservesFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires POSIX directory write permissions")
	}
	dir := t.TempDir()
	rememberLayoutIn(t, dir)
	a, tt := newTestAppIn(t, dir)
	h := savedHost(t, a, "saved", "127.0.0.1")
	if _, err := a.OpenAI(h.ID, openAt{}, 0); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(a.p.DataDir, "layout.json")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(a.p.DataDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(a.p.DataDir, 0o700)
	a.RenameTab(a.ws.ActiveTab().ID, "Unsaved change")
	a.saveLayout()
	tt.Frame()
	if !tt.HasText(locales.In("en").ErrLayoutSave()) {
		t.Fatalf("write failure not reported: %q", tt.Texts())
	}
	after, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed save changed previous file: err=%v", err)
	}
}

func TestLayoutToggleFailureRollsBackSnapshotAndSession(t *testing.T) {
	for _, failure := range []string{"native", "settings", "success"} {
		t.Run(failure, func(t *testing.T) {
			srv, _ := startScript(t, "ready\r\n")
			dir := t.TempDir()
			rememberLayoutIn(t, dir)
			a, tt := newTestAppIn(t, dir)
			c := connected(t, a, tt, srv)
			a.saveLayout()
			file := filepath.Join(a.p.DataDir, "layout.json")
			before, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var nativeCalls []bool
			nativeEnabled := true
			nativeOriginal := []byte("original native geometry bytes, including unknown or damaged fields")
			nativeBytes := bytes.Clone(nativeOriginal)
			undoCalls := 0
			a.p.SetRememberLayout = func(enabled bool) (func() error, error) {
				nativeCalls = append(nativeCalls, enabled)
				if failure == "native" && !enabled {
					return nil, errors.New("injected native persistence failure")
				}
				wasEnabled, original := nativeEnabled, bytes.Clone(nativeBytes)
				nativeEnabled = enabled
				nativeBytes = nil
				return func() error {
					undoCalls++
					nativeEnabled, nativeBytes = wasEnabled, bytes.Clone(original)
					return nil
				}, nil
			}
			if failure == "settings" {
				path := filepath.Join(a.p.DataDir, "settings.json")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			s := a.Settings()
			s.RememberLayout = false
			err = a.ApplySettings(s)
			if failure == "success" && err != nil || failure != "success" && err == nil {
				t.Fatalf("toggle result for %s: %v", failure, err)
			}
			if a.focused() != c || c.s.Status().State != session.Connected || srv.Logins() != 1 {
				t.Fatal("toggle changed the current connection")
			}
			if !reflect.DeepEqual(nativeCalls, []bool{false}) {
				t.Fatalf("setter called again instead of undo: %v", nativeCalls)
			}
			if failure == "success" {
				if a.Settings().RememberLayout || nativeEnabled || nativeBytes != nil || undoCalls != 0 {
					t.Fatal("successful toggle was undone")
				}
				if _, err := os.Stat(file); !os.IsNotExist(err) {
					t.Fatalf("successful disable retained layout: %v", err)
				}
				return
			}
			if !a.Settings().RememberLayout || !nativeEnabled || !bytes.Equal(nativeBytes, nativeOriginal) {
				t.Fatal("failed toggle changed preference or original native bytes")
			}
			after, err := os.ReadFile(file)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("failed transaction lost original layout: err=%v", err)
			}
			wantUndo := 0
			if failure == "settings" {
				wantUndo = 1
			}
			if undoCalls != wantUndo {
				t.Fatalf("native undo calls %d, want %d", undoCalls, wantUndo)
			}
		})
	}
}
