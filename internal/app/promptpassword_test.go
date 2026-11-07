package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/locales"
)

func passwordPromptFor(h assets.Host) *credPrompt {
	return &credPrompt{conn: 1, host: h, key: "ask/" + h.ID, value: "pw"}
}

func TestPromptPasswordCheckbox(t *testing.T) {
	a, tt := newTestApp(t)
	h, err := a.store.Add(assets.Host{Address: "example.com", User: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	m := locales.In("en")
	a.prompt, a.prompting = passwordPromptFor(h), true
	tt.Frame()
	if a.prompt.savePassword || !a.canSavePromptPassword(a.prompt) {
		t.Fatal("saved host prompt: save on by default or not offered")
	}
	mustClick(t, tt, m.SavePasswordCredential())
	tt.Frame()
	if !a.prompt.savePassword {
		t.Fatal("checkbox did not turn on")
	}
	mustClick(t, tt, m.SavePasswordCredential())
	tt.Frame()
	if a.prompt.savePassword {
		t.Fatal("checkbox did not turn off")
	}
	mustClick(t, tt, m.SavePasswordCredential())
	tt.Frame()
	mustClick(t, tt, m.Cancel())
	if a.kc.Has(keychain.Host(h.ID, keychain.Password)) {
		t.Fatal("cancel saved a password")
	}
	a.prompt, a.prompting = passwordPromptFor(h), true
	tt.Frame()
	if a.prompt.savePassword {
		t.Fatal("save checkbox persisted across prompts")
	}
	a.prompt.passphrase = true
	if a.canSavePromptPassword(a.prompt) {
		t.Fatal("passphrase offered persistence")
	}
	a.prompt, a.prompting = passwordPromptFor(assets.Host{ID: tempPrefix + "test", Address: "example.com", User: "demo"}), true
	if a.canSavePromptPassword(a.prompt) {
		t.Fatal("temporary host offered persistence")
	}
}

func TestPromptPasswordPersistsHostAndReconnects(t *testing.T) {
	srv, _ := keyServer(t)
	a, tt := newTestApp(t)
	cr, err := a.store.SaveCredential(assets.Credential{Name: "shared", Kind: assets.CredPassword, User: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.kc.Set(keychain.Cred(cr.ID, keychain.Password), []byte("old-shared-password")); err != nil {
		t.Fatal(err)
	}
	h := addSrvHost(t, a, srv, "saved", assets.AuthCredential, cr.ID)
	h.User = "wrong-host-user"
	h, err = a.store.Update(h)
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.Connect(h.ID, openAt{})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "password prompt", func() bool { return a.prompting })
	a.prompt.value, a.prompt.savePassword = "pw", true
	a.submitPrompt(en)
	waitFor(t, tt, "password retry connected", func() bool { return a.conns[id].s.Status().State == session.Connected })
	saved, _ := a.store.Get(h.ID)
	if saved.Auth != assets.AuthPassword || saved.CredentialID != "" || saved.User != "demo" {
		t.Fatal(saved)
	}
	if value, err := a.kc.Get(keychain.Cred(cr.ID, keychain.Password)); err != nil || string(value) != "old-shared-password" {
		t.Fatal("shared credential changed", err)
	}
	a.CloseSession(id)
	a.mu.Lock()
	clear(a.typed)
	a.mu.Unlock()
	id, err = a.Connect(h.ID, openAt{})
	if err != nil {
		t.Fatal(err)
	}
	waitConnected(t, a, tt, id)
	if a.prompting {
		t.Fatal("asked for persisted password")
	}
	noSecrets(t, a.p.DataDir, "old-shared-password", "\"pw\"")
}

func TestPromptPasswordSaveFailures(t *testing.T) {
	a, _, kc := newFlakyApp(t)
	h, err := a.store.Add(assets.Host{Address: "example.com", User: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	p := passwordPromptFor(h)
	p.savePassword = true
	a.prompt, a.prompting = p, true
	a.conns[1] = &conn{id: 1, host: h}
	kc.failOn(suffix("/password"), nil)
	a.submitPrompt(en)
	if a.prompt != p || !a.prompting || p.value != "pw" || !strings.Contains(p.err, "keychain locked") {
		t.Fatal("failed write closed prompt", p)
	}
	if a.typed[p.key] != "" {
		t.Fatal("continued with unsaved password")
	}
	kc.failOn(nil, nil)
	path := filepath.Join(a.p.DataDir, "hosts.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	a.submitPrompt(en)
	if a.prompt != p || !a.prompting || p.value != "pw" || p.err == "" {
		t.Fatal("metadata failure closed prompt", p)
	}
	if a.kc.Has(keychain.Host(h.ID, keychain.Password)) {
		t.Fatal("metadata failure retained secret")
	}
	got, _ := a.store.Get(h.ID)
	if got.Auth != assets.AuthAsk {
		t.Fatal("metadata failure changed auth", got)
	}
	os.Remove(path)
	os.WriteFile(path, data, 0600)
	p.value = ""
	a.submitPrompt(en)
	if a.prompt != p || !strings.Contains(p.err, en.CredPasswordRequired()) {
		t.Fatal("empty saved password accepted")
	}
}

func TestPromptPasswordSavesJumpOwner(t *testing.T) {
	bastion, _ := keyServer(t)
	target, _ := keyServer(t)
	bastion.DirectTCPIP = true
	a, tt := newTestApp(t)
	b := addSrvHost(t, a, bastion, "jump", assets.AuthAsk, "")
	h := addSrvHost(t, a, target, "target", assets.AuthPassword, "")
	if err := a.kc.Set(keychain.Host(h.ID, keychain.Password), []byte("pw")); err != nil {
		t.Fatal(err)
	}
	h.JumpChain = []string{b.ID}
	h, err := a.store.Update(h)
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.Connect(h.ID, openAt{})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "jump password prompt", func() bool { return a.prompting })
	if a.prompt.host.ID != b.ID {
		t.Fatal("prompted wrong owner", a.prompt.host.ID)
	}
	a.prompt.value, a.prompt.savePassword = "pw", true
	a.submitPrompt(en)
	waitFor(t, tt, "saved jump retry connected", func() bool { return a.conns[id].s.Status().State == session.Connected })
	saved, _ := a.store.Get(b.ID)
	if saved.Auth != assets.AuthPassword {
		t.Fatal("jump auth not saved", saved)
	}
	if value, err := a.kc.Get(keychain.Host(b.ID, keychain.Password)); err != nil || string(value) != "pw" {
		t.Fatal("jump password not saved", err)
	}
	a.CloseSession(id)
	a.mu.Lock()
	clear(a.typed)
	a.mu.Unlock()
	id, err = a.Connect(h.ID, openAt{})
	if err != nil {
		t.Fatal(err)
	}
	waitConnected(t, a, tt, id)
	if a.prompting {
		t.Fatal("saved jump prompted again")
	}
}

func TestPromptPasswordRejectsChangedOrUnsupportedOwner(t *testing.T) {
	a, _ := newTestApp(t)
	h, err := a.store.Add(assets.Host{Address: "example.com", User: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	p := passwordPromptFor(h)
	p.passphrase = true
	if err := a.savePromptPassword(en, p); err == nil {
		t.Fatal("saved passphrase")
	}
	p.passphrase = false
	h.Auth = assets.AuthAgent
	if _, err := a.store.Update(h); err != nil {
		t.Fatal(err)
	}
	if err := a.savePromptPassword(en, p); err == nil {
		t.Fatal("overwrote changed host authentication")
	}
	p.host.ID = tempPrefix + "1"
	if err := a.savePromptPassword(en, p); err == nil {
		t.Fatal("persisted a temporary host")
	}
	if a.kc.Has(keychain.Host(h.ID, keychain.Password)) {
		t.Fatal("rejected owner retained a secret")
	}
}

func TestPromptPasswordCorrectsPreviouslySavedWrongPassword(t *testing.T) {
	srv, _ := keyServer(t)
	a, tt := newTestApp(t)
	h := addSrvHost(t, a, srv, "correct-password", assets.AuthAsk, "")
	id, err := a.Connect(h.ID, openAt{})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "first password prompt", func() bool { return a.prompting })
	a.prompt.value, a.prompt.savePassword = "wrong-password", true
	a.submitPrompt(en)
	waitFor(t, tt, "wrong saved password prompt", func() bool { return a.prompting && a.prompt.err != "" })
	if a.prompt.savePassword {
		t.Fatal("save choice carried over to retry")
	}
	a.prompt.value, a.prompt.savePassword = "pw", true
	a.submitPrompt(en)
	waitFor(t, tt, "correct saved password retry connected", func() bool { return a.conns[id].s.Status().State == session.Connected })
	if value, err := a.kc.Get(keychain.Host(h.ID, keychain.Password)); err != nil || string(value) != "pw" {
		t.Fatal("correct password not persisted", err)
	}
	a.CloseSession(id)
	a.mu.Lock()
	clear(a.typed)
	a.mu.Unlock()
	id, err = a.Connect(h.ID, openAt{})
	if err != nil {
		t.Fatal(err)
	}
	waitConnected(t, a, tt, id)
}
