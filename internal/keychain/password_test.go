package keychain

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Licoy/sshyard/internal/vault"
)

var cheapKDF = vault.Params{Time: 1, Memory: 8, Threads: 1}

func passwordOptions(t *testing.T) OpenOptions {
	t.Helper()
	o := options(t)
	o.KDF = cheapKDF
	return o
}

func masterPath(o OpenOptions) string { return filepath.Join(filepath.Dir(o.Path), masterFile) }

func setupPassword(t *testing.T, o OpenOptions, password string) {
	t.Helper()
	k, err := OpenLocked(o)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	if !k.CanUsePassword() {
		t.Fatal("empty store cannot use a password")
	}
	if err := k.SetupPassword([]byte(password)); err != nil {
		t.Fatal(err)
	}
	if !k.Unlocked() || !k.PasswordProtected() {
		t.Fatal("setup left the store locked or unprotected")
	}
	if err := k.Set("secret", []byte("value")); err != nil {
		t.Fatal(err)
	}
}

func TestPasswordSetupAndUnlock(t *testing.T) {
	o := passwordOptions(t)
	setupPassword(t, o, "correct horse")
	mem := o.System.(*Memory)
	if mem.Len() != 0 {
		t.Fatal("system keychain written", mem.Keys())
	}
	k := openLockedTest(t, o)
	if !k.PasswordProtected() || k.CanUsePassword() {
		t.Fatal("protection not detected")
	}
	if err := k.TryUnlock(); !errors.Is(err, ErrInteractionRequired) {
		t.Fatal("silent unlock", err)
	}
	if err := k.Unlock(); !errors.Is(err, ErrPasswordRequired) {
		t.Fatal("system unlock", err)
	}
	if err := k.UnlockPassword([]byte("wrong")); !errors.Is(err, ErrWrongPassword) || k.Unlocked() {
		t.Fatal("wrong password", err)
	}
	if err := k.UnlockPassword([]byte("correct horse")); err != nil {
		t.Fatal(err)
	}
	mustGet(t, k, "secret", []byte("value"))
	if mem.Len() != 0 {
		t.Fatal("system keychain written", mem.Keys())
	}
}

func TestPasswordSetupNeedsEmptyStore(t *testing.T) {
	o := passwordOptions(t)
	k := openTest(t, o)
	if err := k.Set("secret", []byte("value")); err != nil {
		t.Fatal(err)
	}
	k.Close()
	k = openLockedTest(t, o)
	if k.CanUsePassword() {
		t.Fatal("store with secrets offers a password")
	}
	if err := k.SetupPassword([]byte("password")); !errors.Is(err, ErrPasswordSetup) {
		t.Fatal(err)
	}
	if _, err := os.Stat(masterPath(o)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("master file written", err)
	}
	if err := k.Unlock(); err != nil {
		t.Fatal(err)
	}
	mustGet(t, k, "secret", []byte("value"))
}

func TestPasswordDamagedMaster(t *testing.T) {
	o := passwordOptions(t)
	setupPassword(t, o, "password")
	if err := os.WriteFile(masterPath(o), []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	k := openLockedTest(t, o)
	if err := k.UnlockPassword([]byte("password")); !errors.Is(err, ErrMasterInvalid) || k.Unlocked() {
		t.Fatal(err)
	}
}

func TestPasswordChange(t *testing.T) {
	o := passwordOptions(t)
	setupPassword(t, o, "old password")
	k := openLockedTest(t, o)
	if err := k.ChangePassword([]byte("old password"), []byte("new password")); err == nil {
		t.Fatal("changed while locked")
	}
	if err := k.UnlockPassword([]byte("old password")); err != nil {
		t.Fatal(err)
	}
	if err := k.ChangePassword([]byte("wrong"), []byte("new password")); !errors.Is(err, ErrWrongPassword) {
		t.Fatal(err)
	}
	if err := k.ChangePassword([]byte("old password"), []byte("new password")); err != nil {
		t.Fatal(err)
	}
	k.Close()
	k = openLockedTest(t, o)
	if err := k.UnlockPassword([]byte("old password")); !errors.Is(err, ErrWrongPassword) {
		t.Fatal("old password still opens", err)
	}
	if err := k.UnlockPassword([]byte("new password")); err != nil {
		t.Fatal(err)
	}
	mustGet(t, k, "secret", []byte("value"))
}

func TestPasswordUseSystem(t *testing.T) {
	o := passwordOptions(t)
	setupPassword(t, o, "password")
	mem := o.System.(*Memory)
	k := openLockedTest(t, o)
	if err := k.UnlockPassword([]byte("password")); err != nil {
		t.Fatal(err)
	}
	if err := k.UseSystem([]byte("wrong")); !errors.Is(err, ErrWrongPassword) || mem.Len() != 0 {
		t.Fatal("wrong password moved the key", err)
	}
	mem.Fail = errors.New("no secret service")
	if err := k.UseSystem([]byte("password")); !errors.Is(err, mem.Fail) || !k.PasswordProtected() {
		t.Fatal("failed move dropped the password", err)
	}
	if _, err := os.Stat(masterPath(o)); err != nil {
		t.Fatal("master file removed", err)
	}
	mem.Fail = nil
	if err := k.UseSystem([]byte("password")); err != nil || k.PasswordProtected() {
		t.Fatal(err)
	}
	if _, err := os.Stat(masterPath(o)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("master file kept", err)
	}
	k.Close()
	k = openLockedTest(t, o)
	if k.PasswordProtected() {
		t.Fatal("still protected")
	}
	if err := k.Unlock(); err != nil {
		t.Fatal(err)
	}
	mustGet(t, k, "secret", []byte("value"))
}
