package app

import (
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/locales"
)

func TestWelcomeQuickMigrationStates(t *testing.T) {
	m := locales.In("en")
	for _, saved := range []bool{false, true} {
		a, tt := newTestApp(t)
		if saved {
			if _, err := a.store.Add(assets.Host{Name: "saved", Address: "host.example", User: "root"}); err != nil {
				t.Fatal(err)
			}
		}
		tt.Frame()
		mustClick(t, tt, m.QuickMigration())
		tt.Frame()
		if a.page != pageSettings || a.prefNow() != prefMigration {
			t.Fatalf("saved=%v: quick migration did not open migration settings", saved)
		}
		if a.importing || a.imp != nil || a.migrationOpen || a.unlock.modal || a.unlock.busy {
			t.Fatal("navigation opened an import or authorization dialog")
		}
		mustClick(t, tt, m.Back())
		tt.Frame()
		mustClick(t, tt, m.QuickConnect())
		tt.Frame()
		if !a.quicking {
			t.Fatal("quick connect unavailable")
		}
		mustClick(t, tt, m.Cancel())
		tt.Frame()
		if a.quicking {
			t.Fatal("quick connect cancel did not return to welcome")
		}
		mustClick(t, tt, m.NewHost())
		tt.Frame()
		if !a.editing {
			t.Fatal("new host unavailable")
		}
		mustClick(t, tt, m.Cancel())
		tt.Frame()
		if a.editing {
			t.Fatal("host editor cancel did not return to welcome")
		}
	}
}

func TestWelcomeMigrationWhileSecretsLocked(t *testing.T) {
	backend := &authorizationBackend{Memory: keychain.NewMemory()}
	backend.fail.Store(true)
	a, tt := lockedTestApp(t, backend)
	if _, err := a.store.Add(assets.Host{Name: "saved", Address: "host.example", User: "root"}); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	calls := backend.calls.Load()
	mustClick(t, tt, "Quick Migration")
	tt.Frame()
	if a.page != pageSettings || a.prefNow() != prefMigration || a.importing || a.unlock.busy || a.unlock.modal || a.unlock.after != nil || backend.calls.Load() != calls {
		t.Fatal("migration navigation requested secret authorization")
	}
}
