package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestMigrationNativeFileFlow(t *testing.T) {
	a, tt := newTestApp(t)
	path := filepath.Join(t.TempDir(), "sessions.csv")
	if err := os.WriteFile(path, []byte("Name,Hostname,Protocol,Port,Username\nserver,host.example,SSH2,22,root\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a.p.PickFiles = func(string) ([]string, error) { return []string{path}, nil }
	m := locales.In("en")
	a.openSettings(prefAppearance)
	tt.Frame()
	mustClick(t, tt, m.Migration())
	tt.Frame()
	if a.prefNow() != prefMigration {
		t.Fatal("navigation did not open migration")
	}
	a.p.PickFolder = func(string, string) (string, error) { return "", nil }
	tt.Scroll(800, 600, 0, 500)
	tt.Frame()
	mustClick(t, tt, m.MigrationChooseSourceFiles("mRemoteNG"))
	waitFor(t, tt, "migration preview", func() bool { return a.migration != nil && !a.migration.reading && len(a.migration.rows) == 1 })
	mustClick(t, tt, m.ImportButton("1"))
	waitFor(t, tt, "migration save", func() bool { return a.migration == nil })
	reopened, err := assets.Open(filepath.Join(a.p.DataDir, "hosts.json"))
	if err != nil || reopened.Len() != 1 {
		t.Fatalf("saved store reopen %v", err)
	}
	h := reopened.List()[0]
	if h.Name != "server" || h.Address != "host.example" || h.User != "root" || h.Auth != assets.AuthAsk {
		t.Fatalf("saved host %+v", h)
	}
}
func TestMigrationUnlockCancelPreservesDraft(t *testing.T) {
	backend := &authorizationBackend{Memory: keychain.NewMemory()}
	backend.fail.Store(true)
	a, tt := lockedTestApp(t, backend)
	e := migrationEntry("host", "host")
	e.Password = []byte("transient")
	f := migrationDraft(a, e)
	f.saveSecrets = true
	a.migration, a.migrationOpen = f, true
	tt.Frame()
	a.startMigrationSave(locales.In("en"))
	waitFor(t, tt, "migration authorization failure", func() bool { return a.unlock.modal })
	// Render the actual unlock modal alone so its Cancel has a unique target.
	unlock := ui.NewTester(func(c *ui.Context) { a.unlockModal(c, locales.In("en"), currentTokens(c)) }, 700, 500)
	unlock.Frame()
	mustClick(t, unlock, locales.In("en").Cancel())
	unlock.Frame()
	if a.migration != f || f.saving || string(f.rows[0].entry.Password) != "transient" || a.unlock.after != nil || a.store.Len() != 0 {
		t.Fatal("cancel lost draft or wrote data")
	}
	f.rows[0].saveSecret = false
	if f.needsSecrets() {
		t.Fatal("per-row opt out requests authorization")
	}
	a.closeMigration()
}
