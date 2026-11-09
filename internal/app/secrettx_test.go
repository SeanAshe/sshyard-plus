package app

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// flakyKC is a keychain backend whose writes and deletes fail for the
// keys a test names.
type flakyKC struct {
	*keychain.Memory
	mu                sync.Mutex
	failSet, failDele func(key string) bool
	failGet           func(key string) bool
}

func (k *flakyKC) Get(key string) ([]byte, error) {
	k.mu.Lock()
	f := k.failGet
	k.mu.Unlock()
	if f != nil && f(key) {
		return nil, errFlaky
	}
	return k.Memory.Get(key)
}

var errFlaky = errors.New("keychain locked")

func (k *flakyKC) fail(set bool, key string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	f := k.failDele
	if set {
		f = k.failSet
	}
	return f != nil && f(key)
}

func (k *flakyKC) Set(key string, v []byte) error {
	if k.fail(true, key) {
		return errFlaky
	}
	return k.Memory.Set(key, v)
}

func (k *flakyKC) Delete(key string) error {
	if k.fail(false, key) {
		return errFlaky
	}
	return k.Memory.Delete(key)
}

func (k *flakyKC) failOn(set, del func(string) bool) {
	k.mu.Lock()
	k.failSet, k.failDele = set, del
	k.mu.Unlock()
}

func newFlakyApp(t *testing.T) (*App, *ui.Tester, *flakyKC) {
	t.Helper()
	a, tt := newTestApp(t)
	kc := &flakyKC{Memory: keychain.NewMemory()}
	if err := a.kc.Close(); err != nil {
		t.Fatal(err)
	}
	a.kc = keychain.New(kc)
	return a, tt, kc
}

func suffix(s string) func(string) bool {
	return func(k string) bool { return strings.HasSuffix(k, s) }
}

// TestCredentialSaveKeychainFailure: when the keychain refuses a write,
// a save changes nothing, neither the entry nor the secrets.
func TestCredentialSaveKeychainFailure(t *testing.T) {
	a, _, kc := newFlakyApp(t)
	// A new encrypted key with its passphrase: the key is written, the
	// passphrase fails, and the key goes again.
	priv, _ := newKey(t)
	kc.failOn(suffix("/passphrase"), nil)
	a.openCredForm(nil, nil)
	a.credForm.name, a.credForm.kind = "deploy", 1
	a.credForm.setKey(openSSHKey(t, priv, "pp"), "k")
	a.credForm.passphrase = "pp"
	if a.SaveCredForm(en) {
		t.Fatal("saved with a failing keychain")
	}
	if !strings.Contains(a.credForm.err, "keychain locked") || !strings.Contains(a.credForm.err, "Nothing was changed") {
		t.Errorf("error %q", a.credForm.err)
	}
	if n := len(a.Store().Credentials()); n != 0 || kc.Len() != 0 {
		t.Fatalf("%d credentials, keychain %q", n, kc.Keys())
	}
	a.closeCredForm()

	// A saved password credential.
	kc.failOn(nil, nil)
	a.openCredForm(nil, nil)
	a.credForm.name, a.credForm.password = "ops", "old-secret"
	if !a.SaveCredForm(en) {
		t.Fatal(a.credForm.err)
	}
	cr := a.Store().Credentials()[0]
	pwKey := keychain.Cred(cr.ID, keychain.Password)
	same := func(what string) {
		t.Helper()
		got, _ := a.Store().Credential(cr.ID)
		if got.Kind != assets.CredPassword || got.Name != "ops" || !got.Updated.Equal(cr.Updated) {
			t.Errorf("%s: credential changed to %+v", what, got)
		}
		if b, err := a.kc.Get(pwKey); err != nil || string(b) != "old-secret" {
			t.Errorf("%s: password %q %v", what, b, err)
		}
		if len(kc.Keys()) != 1 {
			t.Errorf("%s: keychain %q", what, kc.Keys())
		}
	}

	// Rotating the password fails: the old one stays.
	kc.failOn(suffix("/password"), nil)
	a.openCredForm(&cr, nil)
	a.credForm.password = "new-secret"
	if a.SaveCredForm(en) {
		t.Fatal("rotated with a failing keychain")
	}
	same("rotate")
	a.closeCredForm()

	// To a key: the key is written, dropping the password fails, the key
	// goes again and the entry stays a password.
	kc.failOn(nil, suffix("/password"))
	a.openCredForm(&cr, nil)
	a.credForm.kind = 1
	a.credForm.setKey(openSSHKey(t, priv, ""), "k")
	if a.SaveCredForm(en) {
		t.Fatal("switched to a key with a failing keychain")
	}
	same("to key")
	a.closeCredForm()

	// The keychain works but the entry does not save (a name in use):
	// the new password is taken back.
	kc.failOn(nil, nil)
	a.openCredForm(nil, nil)
	a.credForm.name, a.credForm.password = "other", "x"
	if !a.SaveCredForm(en) {
		t.Fatal(a.credForm.err)
	}
	a.openCredForm(&cr, nil)
	a.credForm.name, a.credForm.password = "other", "new-secret"
	if a.SaveCredForm(en) || a.credForm.err != "That name is already in use." {
		t.Fatalf("saved a taken name, err %q", a.credForm.err)
	}
	got, _ := a.Store().Credential(cr.ID)
	if b, _ := a.kc.Get(pwKey); string(b) != "old-secret" || got.Name != "ops" {
		t.Errorf("after a refused save: %q, %+v", b, got)
	}
	a.closeCredForm()

	// Neither the write nor the rollback works: both are reported.
	a.openCredForm(&cr, nil)
	a.credForm.name, a.credForm.password = "other", "new-secret"
	calls := 0
	kc.failOn(func(k string) bool { calls++; return calls > 1 && strings.HasSuffix(k, "/password") }, nil)
	if a.SaveCredForm(en) || !strings.Contains(a.credForm.err, "could not be restored") {
		t.Errorf("err %q", a.credForm.err)
	}
}

// TestProxySaveKeychainFailure: a proxy whose password cannot be stored
// is not saved, and an edit keeps the old password.
func TestProxySaveKeychainFailure(t *testing.T) {
	a, _, kc := newFlakyApp(t)
	kc.failOn(suffix("/password"), nil)
	a.openProxyForm(nil, nil)
	f := a.proxyForm
	f.name, f.address, f.user, f.port, f.password = "office", "127.0.0.1", "pu", "3128", "pw1"
	if a.SaveProxyForm(en) || !strings.Contains(f.err, "keychain locked") {
		t.Fatalf("saved, err %q", f.err)
	}
	if len(a.Store().Proxies()) != 0 || kc.Len() != 0 {
		t.Fatalf("proxies %+v, keychain %q", a.Store().Proxies(), kc.Keys())
	}
	kc.failOn(nil, nil)
	if !a.SaveProxyForm(en) {
		t.Fatal(f.err)
	}
	px := a.Store().Proxies()[0]
	// Dropping the user name deletes the password; that failing keeps
	// both.
	kc.failOn(nil, suffix("/password"))
	a.openProxyForm(&px, nil)
	a.proxyForm.user = ""
	if a.SaveProxyForm(en) {
		t.Fatal("saved with a failing keychain")
	}
	got, _ := a.Store().Proxy(px.ID)
	if b, _ := a.kc.Get(keychain.ProxyPassword(px.ID)); string(b) != "pw1" || got.User != "pu" {
		t.Errorf("after a failed edit: %q, %+v", b, got)
	}
}

// TestDeleteKeychainFailure: a delete the keychain refuses keeps the
// entry, says why, and a retry finishes it.
func TestDeleteKeychainFailure(t *testing.T) {
	a, tt, kc := newFlakyApp(t)
	want := en.ErrKeychainDelete(errFlaky.Error())
	cr := addCred(t, a, "ops", "pw")
	kc.failOn(nil, suffix("/password"))
	if err := a.DeleteCredential(cr.ID, nil); err == nil || a.inUseText(en, err) != want {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := a.Store().Credential(cr.ID); !ok || !a.kc.Has(keychain.Cred(cr.ID, keychain.Password)) {
		t.Fatal("credential or its password gone")
	}
	// From the confirmation, the error is a toast.
	a.askDeleteCred(en, cr.ID)
	tt.Frame()
	mustClick(t, tt, "Delete")
	tt.Frame()
	if !tt.HasText(want) {
		t.Errorf("no error shown: %q", tt.Texts())
	}
	kc.failOn(nil, nil)
	if err := a.DeleteCredential(cr.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Store().Credential(cr.ID); ok || kc.Len() != 0 {
		t.Errorf("not deleted; keychain %q", kc.Keys())
	}

	// A proxy, from its sheet.
	p, err := a.Store().SaveProxy(assets.Proxy{Name: "corp", Kind: assets.ProxySOCKS5, Address: "10.0.0.1", User: "u"})
	if err != nil {
		t.Fatal(err)
	}
	a.kc.Set(keychain.ProxyPassword(p.ID), []byte("proxy-pw"))
	kc.failOn(nil, suffix("/password"))
	a.openProxyForm(&p, nil) // over the host list: its Delete is the only one
	tt.Frame()
	mustClick(t, tt, "Delete")
	tt.Frame()
	if a.proxyForm == nil || a.proxyForm.err != want || !tt.HasText(want) {
		t.Fatalf("proxy sheet: %+v", a.proxyForm)
	}
	if _, ok := a.Store().Proxy(p.ID); !ok || !a.kc.Has(keychain.ProxyPassword(p.ID)) {
		t.Fatal("proxy or its password gone")
	}
	a.closeProxyForm("")
	// From its row in Settings, the error is a toast.
	a.openSettings(prefProxies)
	tt.SetSize(1200, 2400)
	tt.Advance(time.Minute) // the earlier toast goes
	tt.Frame()
	if tt.HasText(want) {
		t.Fatal("old toast still up")
	}
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(want) {
		t.Errorf("no toast: %q", tt.Texts())
	}
	if _, ok := a.Store().Proxy(p.ID); !ok {
		t.Fatal("proxy gone")
	}

	// A forward.
	h, err := a.Store().Add(assets.Host{Name: "h", Address: "10.0.0.2", User: "u"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := a.Store().SaveForward(assets.Forward{Name: "d", Kind: assets.ForwardDynamic, HostID: h.ID, ListenAddr: "127.0.0.1", ListenPort: 1080, User: "c"})
	if err != nil {
		t.Fatal(err)
	}
	a.kc.Set(keychain.ForwardPassword(f.ID), []byte("fwd-pw"))
	if err := a.DeleteForward(f.ID); a.inUseText(en, err) != want {
		t.Fatalf("forward delete: %v", err)
	}
	if _, ok := a.Store().Forward(f.ID); !ok {
		t.Fatal("forward gone")
	}
	kc.failOn(nil, nil)
	if err := a.DeleteProxy(p.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteForward(f.ID); err != nil {
		t.Fatal(err)
	}
	if kc.Len() != 0 {
		t.Errorf("keychain %q", kc.Keys())
	}
}

// TestSecretDeleteReadError: a keychain that cannot be read is an error
// for a delete, not "nothing to delete".
func TestSecretDeleteReadError(t *testing.T) {
	a, _, kc := newFlakyApp(t)
	cr := addCred(t, a, "pw", "secret")
	key := keychain.Cred(cr.ID, keychain.Password)
	if ok, err := a.kc.Exists(key); !ok || err != nil {
		t.Fatalf("exists %v %v", ok, err)
	}
	if ok, err := a.kc.Exists("cred/none/password"); ok || err != nil {
		t.Fatalf("missing: %v %v", ok, err)
	}
	kc.mu.Lock()
	kc.failGet = func(k string) bool { return k == key }
	kc.mu.Unlock()
	if _, err := a.kc.Exists(key); !errors.Is(err, errFlaky) {
		t.Fatalf("read error lost: %v", err)
	}
	tx := &secretTx{kc: a.kc}
	if err := tx.delete(key); !errors.Is(err, errFlaky) {
		t.Fatalf("delete skipped on a read error: %v", err)
	}
	if err := tx.rollback(); err != nil {
		t.Fatal(err)
	}
	kc.mu.Lock()
	kc.failGet = nil
	kc.mu.Unlock()
	if _, ok := a.Store().Credential(cr.ID); !ok || !a.kc.Has(key) {
		t.Error("credential or its secret lost")
	}
	// A proxy losing its user drops its password: with the keychain
	// unreadable, the save fails and nothing changes.
	p, err := a.Store().SaveProxy(assets.Proxy{Name: "corp", Kind: assets.ProxySOCKS5, Address: "10.0.0.1", Port: 1080, User: "u"})
	if err != nil {
		t.Fatal(err)
	}
	pk := keychain.ProxyPassword(p.ID)
	a.kc.Set(pk, []byte("proxy-pw"))
	kc.mu.Lock()
	kc.failGet = func(k string) bool { return k == pk }
	kc.mu.Unlock()
	a.openProxyForm(&p, nil)
	a.proxyForm.user = ""
	if a.SaveProxyForm(locales.In("en")) || !strings.Contains(a.proxyForm.err, errFlaky.Error()) {
		t.Fatalf("saved with the keychain unreadable: %q", a.proxyForm.err)
	}
	kc.mu.Lock()
	kc.failGet = nil
	kc.mu.Unlock()
	if got, _ := a.Store().Proxy(p.ID); got.User != "u" || !a.kc.Has(pk) {
		t.Errorf("proxy %+v, password kept %v", got, a.kc.Has(pk))
	}
}

// TestDeleteCredentialRebindAfterSecrets: deleting a credential in use
// rebinds its hosts only once its secrets are gone, so a keychain failure
// leaves the hosts on it.
func TestDeleteCredentialRebindAfterSecrets(t *testing.T) {
	a, _, kc := newFlakyApp(t)
	cr := addCred(t, a, "ops", "pw")
	h, err := a.Store().Add(assets.Host{Name: "web", Address: "10.0.0.2", User: "u", Auth: assets.AuthCredential, CredentialID: cr.ID})
	if err != nil {
		t.Fatal(err)
	}
	agent := &authChoice{method: assets.AuthAgent}
	kc.failOn(nil, suffix("/password"))
	var kerr *keychainDeleteError
	if err := a.DeleteCredential(cr.ID, agent); !errors.As(err, &kerr) {
		t.Fatalf("err %v", err)
	}
	got, _ := a.Store().Get(h.ID)
	if got.Auth != assets.AuthCredential || got.CredentialID != cr.ID {
		t.Fatalf("host rebound before the secrets went: %+v", got)
	}
	if _, ok := a.Store().Credential(cr.ID); !ok || !a.kc.Has(keychain.Cred(cr.ID, keychain.Password)) {
		t.Fatal("credential or its password gone")
	}
	// A replacement that does not exist changes nothing either.
	kc.failOn(nil, nil)
	if err := a.DeleteCredential(cr.ID, &authChoice{method: assets.AuthCredential, cred: "nope"}); !errors.Is(err, assets.ErrNotFound) {
		t.Fatalf("missing replacement: %v", err)
	}
	if !a.kc.Has(keychain.Cred(cr.ID, keychain.Password)) {
		t.Fatal("secrets deleted for a failed rebind")
	}
	if err := a.DeleteCredential(cr.ID, agent); err != nil {
		t.Fatal(err)
	}
	got, _ = a.Store().Get(h.ID)
	if _, ok := a.Store().Credential(cr.ID); ok || got.Auth != assets.AuthAgent || got.CredentialID != "" || kc.Len() != 0 {
		t.Errorf("host %+v, keychain %q", got, kc.Keys())
	}
}
