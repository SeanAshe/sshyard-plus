package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/sftpsvc"
)

// TestSFTPSudoPassword: sudo gets the password typed for it, else the
// login password typed or saved; a key login has none to give.
func TestSFTPSudoPassword(t *testing.T) {
	a, _ := newTestApp(t)
	add := func(name string, auth assets.AuthMethod) assets.Host {
		t.Helper()
		h, err := a.store.Add(assets.Host{Name: name, Address: name + ".example", User: "ops", Auth: auth, SFTPSudo: true})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	pw := add("pw", assets.AuthPassword)
	if err := a.kc.Set(keychain.Host(pw.ID, keychain.Password), []byte("login-pw")); err != nil {
		t.Fatal(err)
	}
	if got, typed := a.sudoPassword(pw); got != "login-pw" || typed {
		t.Fatalf("saved login password: %q, %v", got, typed)
	}
	a.typed[sudoKey(pw.ID)] = "sudo-pw"
	if got, typed := a.sudoPassword(pw); got != "sudo-pw" || !typed {
		t.Fatalf("typed sudo password: %q, %v", got, typed)
	}
	ask := add("ask", assets.AuthAsk)
	if got, _ := a.sudoPassword(ask); got != "" {
		t.Fatalf("ask host before typing: %q", got)
	}
	a.typed["ask/"+ask.ID] = "asked"
	if got, typed := a.sudoPassword(ask); got != "asked" || typed {
		t.Fatalf("ask host: %q, %v", got, typed)
	}
	if got, _ := a.sudoPassword(add("agent", assets.AuthAgent)); got != "" {
		t.Fatalf("agent host: %q", got)
	}
}

// TestSFTPSudoFailed: a refused password typed for sudo is forgotten and
// asked again; the answer is kept in memory only. Other failures hold
// the pane with sudo's reason, without asking.
func TestSFTPSudoFailed(t *testing.T) {
	a, _ := newTestApp(t)
	h, err := a.store.Add(assets.Host{Name: "box", Address: "box.example", User: "ops", Auth: assets.AuthKey, SFTPSudo: true})
	if err != nil {
		t.Fatal(err)
	}
	cn := &conn{id: 42, host: h, fs: &filesState{}}
	a.typed[sudoKey(h.ID)] = "wrong"
	a.sudoFailed(cn, h, fmt.Errorf("sftp: %w", sftpsvc.ErrSudoPassword), true, true)
	if !cn.fs.remote.sudoHeld {
		t.Fatal("pane not held after sudo refused the password")
	}
	if _, ok := a.typed[sudoKey(h.ID)]; ok {
		t.Fatal("refused sudo password kept")
	}
	if a.sudo == nil || !a.sudoAsking || !a.sudo.refused || a.sudo.conn != cn.id {
		t.Fatalf("sudo sheet %+v open %v", a.sudo, a.sudoAsking)
	}
	p := a.sudo
	p.value = "right"
	a.submitSudo(p)
	if a.sudo != nil || a.sudoAsking {
		t.Fatal("sheet still open after submit")
	}
	if a.typed[sudoKey(h.ID)] != "right" {
		t.Fatalf("typed %q", a.typed[sudoKey(h.ID)])
	}
	if v, err := a.kc.Get(sudoKey(h.ID)); err == nil {
		t.Fatalf("sudo password saved to the store: %q", v)
	}

	other := &conn{id: 43, host: h, fs: &filesState{}}
	a.sudoFailed(other, h, fmt.Errorf("%w: ops is not in the sudoers file.", sftpsvc.ErrSudo), true, false)
	if !other.fs.remote.sudoHeld || a.sudo != nil {
		t.Fatalf("held %v, sheet %+v", other.fs.remote.sudoHeld, a.sudo)
	}
	if !strings.Contains(other.fs.remote.err, "ops is not in the sudoers file.") {
		t.Fatalf("err %q", other.fs.remote.err)
	}
	if a.typed[sudoKey(h.ID)] != "right" {
		t.Fatal("a failure other than the password dropped the typed one")
	}
}
