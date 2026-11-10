package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/egoist/mygo/ui"
)

func lockedTestAppIn(t *testing.T, dir string, b keychain.Backend) (*App, *ui.Tester) {
	t.Helper()
	a, err := New(Platform{DataDir: dir, Secrets: b, HomeDir: dir, CacheDir: filepath.Join(dir, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	a.Start()
	tt := ui.NewTester(a.View, 1200, 760)
	tt.SetLocale("en")
	tt.Frame()
	return a, tt
}

// TestMasterPasswordFallback: with the system keychain unusable and
// nothing stored, a local master password protects the new store, the
// pending save goes on, and the next launch unlocks only with it.
func TestMasterPasswordFallback(t *testing.T) {
	dir := t.TempDir()
	b := &linuxAuthorizationBackend{Memory: keychain.NewMemory()}
	b.fail.Store(true)
	a, tt := lockedTestAppIn(t, dir, b)
	a.openEditor(nil)
	a.form.name, a.form.address, a.form.user = "draft", "draft.example", "demo"
	a.form.auth, a.form.secret.password = en.CredKindPassword(), "transient"
	if a.SaveForm(en) {
		t.Fatal("saved while system keyring unavailable")
	}
	waitFor(t, tt, "system keyring failure", func() bool { return a.unlock.modal })
	if !masterStage(a.unlock.err) || !a.kc.CanUsePassword() {
		t.Fatalf("no fallback offered for %v", a.unlock.err)
	}
	// Drive the modal's handler: TextInputs own their frame state.
	a.unlock.modal = false
	a.openMasterPassword(masterPwSetup)
	tt.Frame()
	if a.unlock.after == nil {
		t.Fatal("switching to the password dropped the pending save")
	}
	f := a.masterPw
	f.password, f.confirm = "short", "short"
	if a.SubmitMasterPassword(en) || f.err == "" {
		t.Fatal("short password accepted")
	}
	f.password, f.confirm = "correct horse", "battery staple"
	if a.SubmitMasterPassword(en) || f.err == "" {
		t.Fatal("mismatched password accepted")
	}
	f.confirm = "correct horse"
	if !a.SubmitMasterPassword(en) {
		t.Fatal("password not submitted")
	}
	waitForIn(t, tt, slowWait, "save after setup", func() bool { return a.store.Len() == 1 })
	if !a.unlocked() || !a.kc.PasswordProtected() || a.masterPw != nil {
		t.Fatal("setup did not unlock with the password")
	}
	if b.Memory.Len() != 0 {
		t.Fatal("master key written to the system keychain", b.Memory.Keys())
	}
	if _, err := os.Stat(filepath.Join(dir, "master.enc")); err != nil {
		t.Fatal(err)
	}
	id := a.store.List()[0].ID
	a.Close()

	calls := b.calls.Load()
	a, tt = lockedTestAppIn(t, dir, b)
	ran := false
	a.withSecrets(func() { ran = true })
	f = a.masterPw
	if f == nil || f.mode != masterPwUnlock || !a.masterPwOpen {
		t.Fatal("unlock did not ask for the master password")
	}
	f.password = "wrong password"
	a.SubmitMasterPassword(en)
	waitForIn(t, tt, slowWait, "wrong password", func() bool { return !f.busy })
	if f.err != en.MasterPasswordWrong() || a.unlocked() || a.masterPw != f || ran {
		t.Fatal("wrong password unlocked or closed the sheet")
	}
	f.password = "correct horse"
	a.SubmitMasterPassword(en)
	waitForIn(t, tt, slowWait, "unlock with password", func() bool { return a.unlocked() })
	if !ran || a.masterPw != nil {
		t.Fatal("pending action not run")
	}
	if v, err := a.kc.Get(keychain.Host(id, keychain.Password)); err != nil || string(v) != "transient" {
		t.Fatalf("saved secret: %q %v", v, err)
	}
	if b.calls.Load() != calls {
		t.Fatal("password unlock asked the system keychain")
	}
}

// TestMasterPasswordCancel: cancelling the password drops the action
// waiting on the unlock and leaves the store locked.
func TestMasterPasswordCancel(t *testing.T) {
	dir := t.TempDir()
	b := &linuxAuthorizationBackend{Memory: keychain.NewMemory()}
	a, _ := lockedTestAppIn(t, dir, b)
	if err := a.kc.SetupPassword([]byte("correct horse")); err != nil {
		t.Fatal(err)
	}
	a.Close()
	a, tt := lockedTestAppIn(t, dir, b)
	a.withSecrets(func() { t.Error("cancelled action ran") })
	a.masterPwOpen = false
	tt.Frame()
	if a.masterPw != nil || a.unlock.after != nil || a.unlocked() {
		t.Fatal("cancel kept the action or unlocked")
	}
}
