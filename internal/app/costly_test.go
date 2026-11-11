package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"sync/atomic"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/keys"
	"github.com/Licoy/sshyard/internal/vault"
)

// costlyPPK stands in for a PuTTY key whose derivation costs more than
// usual: Import says so, ImportCostly (counted) opens it with "pw" as the
// key returned. Nothing is derived.
func costlyPPK(t *testing.T) ([]byte, keys.Key, *atomic.Int32) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	k, err := keys.Encode(priv, "costly", nil)
	if err != nil {
		t.Fatal(err)
	}
	n := new(atomic.Int32)
	plain, costly := importPlainKey, importCostlyKey
	importPlainKey = func(data, pass []byte) (keys.Key, error) {
		if keys.IsPPK(data) && len(pass) > 0 {
			return keys.Key{}, &keys.CostError{Memory: 768 << 10, Passes: 4}
		}
		return plain(data, pass)
	}
	importCostlyKey = func(data, pass []byte) (keys.Key, error) {
		n.Add(1)
		if string(pass) != "pw" {
			return keys.Key{}, keys.ErrPassphraseWrong
		}
		return k, nil
	}
	t.Cleanup(func() { importPlainKey, importCostlyKey = plain, costly })
	return []byte("PuTTY-User-Key-File-3: ssh-ed25519\n"), k, n
}

// costlyCredForm is the credential sheet about to save data with "pw".
func costlyCredForm(a *App, data []byte) *credForm {
	a.openCredForm(nil, nil)
	f := a.credForm
	f.name, f.kind, f.keyData, f.preview, f.passphrase = "costly", 1, data, keys.Key{Encrypted: true}, "pw"
	return f
}

// TestCostlyKeyAsksBeforeDeriving: a PuTTY key past the usual cost is
// derived only once the user agrees, off the UI thread; cancelled, it
// saves nothing, even when the derivation under way ends after.
func TestCostlyKeyAsksBeforeDeriving(t *testing.T) {
	a, tt := newTestApp(t)
	data, k, n := costlyPPK(t)
	f := costlyCredForm(a, data)
	if a.SaveCredForm(en) || a.cost == nil || a.cost.backup || a.cost.memory != 768<<10 || a.cost.passes != 4 {
		t.Fatalf("saved without asking: %+v", a.cost)
	}
	if n.Load() != 0 || len(a.store.Credentials()) != 0 || f.err != "" {
		t.Fatalf("derived %d, %d credentials, err %q", n.Load(), len(a.store.Credentials()), f.err)
	}
	// Cancelled before Continue: nothing derived or saved.
	a.closeCost()
	if n.Load() != 0 || len(a.store.Credentials()) != 0 || a.credForm != f {
		t.Fatal("cancelling derived or saved")
	}
	// Cancelled while it derives: the result is dropped.
	a.SaveCredForm(en)
	a.cost.run()
	a.closeCost()
	waitFor(t, tt, "derivation", func() bool { return n.Load() == 1 })
	for range 5 {
		tt.Frame()
	}
	if len(a.store.Credentials()) != 0 || a.credForm != f || a.cost != nil {
		t.Fatal("a cancelled derivation saved the key")
	}
	// Continue: derived once, then saved.
	a.SaveCredForm(en)
	a.cost.run()
	waitFor(t, tt, "save", func() bool { return a.credForm == nil })
	crs := a.store.Credentials()
	if n.Load() != 2 || len(crs) != 1 || crs[0].Fingerprint != k.Fingerprint() {
		t.Fatalf("derived %d, saved %+v", n.Load(), crs)
	}
	if pem, err := a.kc.Get(keychain.Cred(crs[0].ID, keychain.PrivateKey)); err != nil || string(pem) != string(k.PEM) {
		t.Fatalf("stored key: %v", err)
	}
}

// TestCostlyKeyWrongPassphrase: agreed to, a wrong passphrase is told as
// such, and saves nothing.
func TestCostlyKeyWrongPassphrase(t *testing.T) {
	a, tt := newTestApp(t)
	data, _, n := costlyPPK(t)
	f := costlyCredForm(a, data)
	f.passphrase = "wrong"
	a.SaveCredForm(en)
	a.cost.run()
	waitFor(t, tt, "derivation", func() bool { return a.cost == nil })
	if n.Load() != 1 || f.err != en.ErrKeyPassphraseWrong() || len(a.store.Credentials()) != 0 {
		t.Fatalf("derived %d, err %q", n.Load(), f.err)
	}
}

// TestCostlyMigrationKey: a migrated PuTTY key past the usual cost stops
// the batch, nothing saved, until the user agrees.
func TestCostlyMigrationKey(t *testing.T) {
	a, tt := newTestApp(t)
	data, k, n := costlyPPK(t)
	e := migrationEntry("costly", "costly.example")
	e.PrivateKey, e.Passphrase = data, []byte("pw")
	f := migrationDraft(a, e)
	f.saveSecrets = true
	a.migration, a.migrationOpen = f, true
	a.startMigrationSave(en)
	waitFor(t, tt, "asked", func() bool { return a.cost != nil })
	if n.Load() != 0 || a.store.Len() != 0 {
		t.Fatalf("derived %d, saved %d hosts before agreeing", n.Load(), a.store.Len())
	}
	a.cost.run()
	waitFor(t, tt, "saved", func() bool { return a.store.Len() == 1 && !f.saving })
	h := a.store.List()[0]
	if pem, err := a.kc.Get(keychain.Host(h.ID, keychain.PrivateKey)); n.Load() != 1 || h.Auth != assets.AuthKey || err != nil || string(pem) != string(k.PEM) {
		t.Fatalf("derived %d, host %+v, key %v", n.Load(), h, err)
	}
}

// TestCostlyBackupAsksBeforeOpening: a backup past the usual cost is
// opened only once the user agrees; cancelled, nothing is opened.
func TestCostlyBackupAsksBeforeOpening(t *testing.T) {
	src, stt, sdir := backupApp(t)
	fillStore(t, src)
	path := export(t, src, stt, sdir, "correct horse", true)
	b, _ := os.ReadFile(path)

	var costly atomic.Int32
	orig := openVault
	openVault = func(ctx context.Context, b, pw []byte, allowed bool) ([]byte, error) {
		if !allowed {
			return nil, &vault.CostError{Cost: vault.Cost{Wraps: 1, Passes: 16, Memory: 1 << 20, Work: 16 << 20}}
		}
		costly.Add(1)
		return orig(ctx, b, pw, allowed)
	}
	t.Cleanup(func() { openVault = orig })

	dst, tt, _ := backupApp(t)
	dst.openRestore(en, path, b, nil)
	f := dst.backup
	f.password = "correct horse"
	dst.RestoreBackup(en)
	waitForIn(t, tt, slowWait, "asked", func() bool { return dst.cost != nil })
	if !dst.cost.backup || dst.cost.memory != 1<<20 || dst.cost.passes != 16 || costly.Load() != 0 || dst.store.Len() != 0 || f.busy {
		t.Fatalf("cost %+v, opened %d, %d hosts", dst.cost, costly.Load(), dst.store.Len())
	}
	dst.closeCost()
	if costly.Load() != 0 || dst.backup != f {
		t.Fatal("cancelling opened the backup")
	}
	dst.RestoreBackup(en)
	waitForIn(t, tt, slowWait, "asked again", func() bool { return dst.cost != nil })
	dst.cost.run()
	waitForIn(t, tt, slowWait, "restored", func() bool { return dst.backup == nil })
	if costly.Load() != 1 || dst.store.Len() == 0 {
		t.Fatalf("opened %d, %d hosts", costly.Load(), dst.store.Len())
	}
}

// TestBackupCloseCancelsOpening: closing the restore sheet cancels the
// opening under way, so no further wrap is derived (package vault stops
// before the next one), and restores nothing.
func TestBackupCloseCancelsOpening(t *testing.T) {
	src, stt, sdir := backupApp(t)
	fillStore(t, src)
	path := export(t, src, stt, sdir, "correct horse", true)
	b, _ := os.ReadFile(path)

	started, done := make(chan struct{}), make(chan error, 1)
	orig := openVault
	openVault = func(ctx context.Context, b, pw []byte, allowed bool) ([]byte, error) {
		close(started)
		<-ctx.Done()
		done <- ctx.Err()
		return nil, ctx.Err()
	}
	t.Cleanup(func() { openVault = orig })

	dst, tt, _ := backupApp(t)
	dst.openRestore(en, path, b, nil)
	dst.backup.password = "correct horse"
	dst.RestoreBackup(en)
	<-started
	dst.backingUp = false
	tt.Frame() // the sheet closes
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("opening not cancelled: %v", err)
	}
	for range 5 {
		tt.Frame()
	}
	if dst.backup != nil || dst.store.Len() != 0 {
		t.Fatal("a closed sheet restored")
	}
}
