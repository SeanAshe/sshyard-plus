package app

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/netproxy"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/Licoy/sshyard/locales"
	"golang.org/x/crypto/ssh"
)

func startServer(t *testing.T) *sshtest.Server {
	t.Helper()
	s, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestJumpChainTerminalAndFiles(t *testing.T) {
	bastion, db := startServer(t), startServer(t)
	bastion.DirectTCPIP = true
	db.SFTPRoot = t.TempDir()
	os.WriteFile(filepath.Join(db.SFTPRoot, "inside.txt"), []byte("x"), 0o644)
	a, tt := newTestApp(t)
	b := addHost(t, a, bastion, "bastion-1", "") // asks for its password
	h := addHost(t, a, db, "db", "secret")
	h.JumpChain = []string{b.ID}
	if _, err := a.Store().Update(h); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tip := a.hostTip(locales.In("en"), h); !strings.HasSuffix(tip, "via bastion-1") {
		t.Errorf("tooltip %q", tip)
	}
	a.Connect(h.ID, openAt{})
	c := a.focused()
	// The jump host's password is asked for under its name.
	waitFor(t, tt, "jump password prompt", func() bool { return a.prompting })
	tt.Frame()
	if a.prompt.host.ID != b.ID || !tt.HasText("Jump host bastion-1: Enter the password for bastion-1 to connect.") {
		t.Fatalf("prompt for %q: %q", a.prompt.host.Label(), tt.Texts())
	}
	a.prompt.value = "secret"
	a.SubmitPrompt()
	// Every hop's key is confirmed: the jump host's first.
	waitFor(t, tt, "jump host key prompt", func() bool { return a.hostKey != nil })
	if a.hostKey.err.Host != bastion.Addr {
		t.Fatalf("asked for %s", a.hostKey.err.Host)
	}
	mustClick(t, tt, "Trust and Connect")
	waitFor(t, tt, "target key prompt", func() bool { return a.hostKey != nil })
	if a.hostKey.err.Host != db.Addr {
		t.Fatalf("asked for %s", a.hostKey.err.Host)
	}
	mustClick(t, tt, "Trust and Connect")
	waitFor(t, tt, "connected through the chain", func() bool { return c.s.Status().State == session.Connected })
	if bastion.Tunnels() < 1 || db.Logins() != 1 {
		t.Errorf("tunnels %d, logins %d", bastion.Tunnels(), db.Logins())
	}
	// A files pane goes the same way, on the same connection.
	if _, err := a.OpenFiles(h.ID, openAt{pane: c.id, edge: layout.Right}, c); err != nil {
		t.Fatal(err)
	}
	fc := a.focused()
	waitFor(t, tt, "files through the chain", func() bool { return fc.fs.remote.client != nil })
	waitFor(t, tt, "remote listing", func() bool { return tt.HasText("inside.txt") })
	// The middle hop drops: the session comes back along the whole chain.
	bastion.Drop()
	waitFor(t, tt, "reconnected", func() bool { st := c.s.Status(); return st.State == session.Connected && st.Connects == 2 })
	if bastion.Logins() < 2 {
		t.Errorf("bastion logins %d", bastion.Logins())
	}
}

func TestJumpErrorsAndStatus(t *testing.T) {
	a, _ := newTestApp(t)
	m := locales.In("en")
	b, _ := a.Store().Add(assets.Host{Name: "bastion-1", Address: "10.0.0.1", User: "ops", Auth: assets.AuthAsk})
	h, _ := a.Store().Add(assets.Host{Name: "db", Address: "10.0.0.2", User: "demo", Auth: assets.AuthAsk, JumpChain: []string{b.ID}})
	st := session.Status{State: session.Connecting, Hop: "bastion-1"}
	if got := a.statusText(m, h, st, time.Now()); got != "Connecting to bastion-1…" {
		t.Errorf("connecting: %q", got)
	}
	err := &session.HopError{Index: 0, ID: b.ID, Name: "bastion-1", Jump: true, Err: errors.New("ssh: unable to authenticate")}
	if got := a.errText(m, h, err); !strings.HasPrefix(got, "Jump host bastion-1: Authentication failed for ops@") {
		t.Errorf("jump auth: %q", got)
	}
	err = &session.HopError{Index: 1, ID: h.ID, Name: "db", Err: errors.New("ssh: unable to authenticate")}
	if got := a.errText(m, h, err); !strings.HasPrefix(got, "Authentication failed for demo@") {
		t.Errorf("target auth: %q", got)
	}
	// A loop of jump hosts is refused on save, in the editor.
	a.openEditor(&b)
	a.form.jumps = []string{h.ID}
	if a.SaveForm(m) || a.form.err != m.ErrJumpLoop() || !a.form.netOpen {
		t.Errorf("loop saved: err %q", a.form.err)
	}
	if got, _ := a.Store().Get(b.ID); len(got.JumpChain) != 0 {
		t.Errorf("loop stored: %v", got.JumpChain)
	}
}

func TestMoveRows(t *testing.T) {
	s := []string{"a", "b", "c", "d"}
	for _, tc := range []struct {
		rows []int
		to   int
		want string
	}{{[]int{0}, 4, "bcda"}, {[]int{3}, 0, "dabc"}, {[]int{1}, 3, "acbd"}, {[]int{2}, 1, "acbd"}, {[]int{0, 2}, 4, "bdac"}} {
		if got := strings.Join(moveRows(s, tc.rows, tc.to), ""); got != tc.want {
			t.Errorf("move %v to %d: %s, want %s", tc.rows, tc.to, got, tc.want)
		}
	}
}

func TestProxySheetAndEditor(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	mustClick(t, tt, "Settings")
	tt.Frame()
	mustClick(t, tt, "Proxies")
	tt.Frame()
	if !tt.HasText(m.NoProxies()) {
		t.Fatalf("settings: %q", tt.Texts())
	}
	a.openProxyForm(nil, nil) // the "New Proxy" button, below the fold
	tt.Frame()
	f := a.proxyForm
	f.name, f.address, f.user = "office", "127.0.0.1", "pu"
	f.kind = 1
	tt.Frame()
	f.port, f.password = "3128", "proxy-secret"
	mustClick(t, tt, "Save")
	if a.proxyForm != nil {
		t.Fatalf("not saved: %q", f.err)
	}
	ps := a.Store().Proxies()
	if len(ps) != 1 || ps[0].Kind != assets.ProxyHTTP || ps[0].Port != 3128 || ps[0].User != "pu" {
		t.Fatalf("proxies %+v", ps)
	}
	px := ps[0]
	if b, err := a.kc.Get(keychain.ProxyPassword(px.ID)); err != nil || string(b) != "proxy-secret" {
		t.Errorf("keychain: %q %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(a.p.DataDir, "hosts.json")); strings.Contains(string(b), "proxy-secret") {
		t.Error("proxy password in hosts.json")
	}
	tt.Frame()
	if !tt.HasText("office") || !tt.HasText("HTTP · 127.0.0.1:3128 · pu") {
		t.Errorf("proxy row: %q", tt.Texts())
	}
	// The host editor makes one in place, and comes back with it chosen.
	mustClick(t, tt, "Close Settings")
	a.openEditor(nil)
	a.form.netOpen = true
	tt.Frame()
	a.form.name, a.form.address, a.form.user = "web", "10.0.0.5", "root"
	hf := a.form
	hf.proxy = proxyNew // "New proxy…" chosen in the select
	tt.Frame()
	if a.proxyForm == nil || a.editing || hf.proxy != "" {
		t.Fatalf("New proxy…: sheet %v, editing %v, proxy %q", a.proxyForm != nil, a.editing, hf.proxy)
	}
	tt.Frame()
	if a.form != hf {
		t.Fatal("the editor was dropped behind the proxy sheet")
	}
	a.proxyForm.name, a.proxyForm.address = "socks", "127.0.0.1"
	mustClick(t, tt, "Save")
	tt.Frame()
	if p, _ := a.Store().Proxy(hf.proxy); !a.editing || a.form != hf || p.Name != "socks" {
		t.Fatalf("back to the editor: editing %v, proxy %q", a.editing, hf.proxy)
	}
	if !a.SaveForm(m) {
		t.Fatal(hf.err)
	}
	h := a.Store().List()[0]
	socks, _ := a.Store().Proxy(h.ProxyID)
	if socks.Name != "socks" || socks.Kind != assets.ProxySOCKS5 || socks.Port != 1080 {
		t.Errorf("host proxy %+v", socks)
	}
	if !a.kc.Has(keychain.ProxyPassword(px.ID)) || a.kc.Has(keychain.ProxyPassword(socks.ID)) {
		t.Error("keychain entries")
	}
	// One in use is not deleted, and says who uses it.
	err := a.DeleteProxy(socks.ID)
	if err == nil || !strings.Contains(a.inUseText(m, err), "web") {
		t.Errorf("deleted a proxy in use: %v", err)
	}
	if err := a.DeleteProxy(px.ID); err != nil || a.kc.Has(keychain.ProxyPassword(px.ID)) {
		t.Errorf("delete: %v", err)
	}
	// The editor shows the network section open for it.
	a.openEditor(&h)
	tt.Frame()
	if !a.form.netOpen || !tt.HasText("Network") || a.form.proxy != socks.ID {
		t.Errorf("editor network: open %v, proxy %q", a.form.netOpen, a.form.proxy)
	}
}

func TestConnectThroughProxy(t *testing.T) {
	srv := startServer(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go (&netproxy.Server{User: "pu", Password: "pp", Dial: (&net.Dialer{}).DialContext}).Serve(l)
	a, tt := newTestApp(t)
	m := locales.In("en")
	host, port := splitAddr(t, l.Addr().String())
	px, err := a.Store().SaveProxy(assets.Proxy{Name: "office", Kind: assets.ProxySOCKS5, Address: host, Port: port, User: "pu"})
	if err != nil {
		t.Fatal(err)
	}
	a.kc.Set(keychain.ProxyPassword(px.ID), []byte("wrong"))
	h := addHost(t, a, srv, "box", "secret")
	h.ProxyID = px.ID
	a.Store().Update(h)
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	a.Connect(h.ID, openAt{})
	c := a.focused()
	waitFor(t, tt, "proxy failure", func() bool { return c.s.Status().State == session.Failed })
	tt.Frame()
	want := "Proxy authentication failed at office. Check the proxy’s username and password in Settings."
	if got := a.statusText(m, c.host, c.s.Status(), time.Now()); got != want || a.prompting {
		t.Fatalf("status %q, prompting %v", got, a.prompting)
	}
	a.kc.Set(keychain.ProxyPassword(px.ID), []byte("pp"))
	c.s.Reconnect()
	waitFor(t, tt, "connected through the proxy", func() bool { return c.s.Status().State == session.Connected })
}

// TestPoolKeyFollowsJumpHosts: editing a jump host (or its credential or
// proxy) gives the target a new connection, not the stale shared one.
func TestPoolKeyFollowsJumpHosts(t *testing.T) {
	old, fresh, db := startServer(t), startServer(t), startServer(t)
	old.DirectTCPIP, fresh.DirectTCPIP = true, true
	a, tt := newTestApp(t)
	b := addHost(t, a, old, "bastion", "secret")
	h := addHost(t, a, db, "db", "secret")
	h.JumpChain = []string{b.ID}
	h, _ = a.Store().Update(h)
	for _, s := range []*sshtest.Server{old, fresh, db} {
		a.known.Add(s.Addr, s.HostKey.PublicKey())
	}
	key := a.poolKey(h)
	a.Connect(h.ID, openAt{})
	c := a.focused()
	waitFor(t, tt, "connected via old", func() bool { return c.s.Status().State == session.Connected })

	// The jump host now points elsewhere: a new pane goes there.
	b.Address, b.Port = splitAddr(t, fresh.Addr)
	if b, _ = a.Store().Update(b); a.poolKey(h) == key {
		t.Fatal("pool key ignores the jump host's address")
	}
	a.Connect(h.ID, openAt{})
	c2 := a.focused()
	waitFor(t, tt, "connected via fresh", func() bool { return c2.s.Status().State == session.Connected })
	if fresh.Tunnels() != 1 || old.Tunnels() != 1 {
		t.Errorf("tunnels old %d fresh %d", old.Tunnels(), fresh.Tunnels())
	}
	// Its credential's user, and its proxy, are part of it too.
	key = a.poolKey(h)
	cr, _ := a.Store().Credential(b.CredentialID)
	cr.User = "other"
	a.Store().SaveCredential(cr)
	if a.poolKey(h) == key {
		t.Error("pool key ignores the jump host's credential user")
	}
	key = a.poolKey(h)
	p, err := a.Store().SaveProxy(assets.Proxy{Name: "p", Kind: assets.ProxySOCKS5, Address: "127.0.0.1", Port: 1080})
	if err != nil {
		t.Fatal(err)
	}
	b.ProxyID = p.ID
	b, _ = a.Store().Update(b)
	key = a.poolKey(h)
	p.Port = 1081
	a.Store().SaveProxy(p)
	if a.poolKey(h) == key {
		t.Error("pool key ignores the jump host's proxy")
	}
}

// TestHopDownMessage: a hop the jump host cannot reach is named, with the
// hop it was tried through, in plain words.
func TestHopDownMessage(t *testing.T) {
	bastion, db := startServer(t), startServer(t)
	bastion.DirectTCPIP = true
	a, tt := newTestApp(t)
	m := locales.In("en")
	b := addHost(t, a, bastion, "bastion-1", "secret")
	h := addHost(t, a, db, "db", "secret")
	h.JumpChain = []string{b.ID}
	h, _ = a.Store().Update(h)
	a.known.Add(bastion.Addr, bastion.HostKey.PublicKey())
	db.Close()
	a.Connect(h.ID, openAt{})
	c := a.focused()
	want := "Cannot reach db through bastion-1: " + m.NetRefused()
	waitFor(t, tt, "hop error", func() bool {
		st := c.s.Status()
		return st.Err != nil && a.errText(m, h, st.Err) == want
	})
	// A jump host further down the chain keeps its attribution.
	mid := addHost(t, a, db, "mid", "secret")
	mid.JumpChain = []string{b.ID}
	mid, _ = a.Store().Update(mid)
	h.JumpChain = []string{mid.ID}
	h, _ = a.Store().Update(h)
	err := &session.HopError{Index: 1, ID: mid.ID, Name: "mid", Jump: true, Err: &ssh.OpenChannelError{Reason: ssh.ConnectionFailed, Message: "dial tcp 10.0.0.9:22: connect: no route to host"}}
	if got := a.errText(m, h, err); got != "Jump host mid: Cannot reach mid through bastion-1: "+m.NetUnreachable() {
		t.Errorf("mid hop: %q", got)
	}
	zh := locales.In("zh-Hans")
	if got := a.errText(zh, h, err); !strings.Contains(got, "经由 bastion-1") || !strings.Contains(got, zh.NetUnreachable()) {
		t.Errorf("zh: %q", got)
	}
	// Windows says "actively refused", not "connection refused".
	refused := &session.HopError{Index: 1, ID: mid.ID, Name: "mid", Jump: true, Err: &ssh.OpenChannelError{Reason: ssh.ConnectionFailed, Message: "dial tcp 127.0.0.1:22: connectex: No connection could be made because the target machine actively refused it."}}
	if got := a.errText(m, h, refused); got != "Jump host mid: Cannot reach mid through bastion-1: "+m.NetRefused() {
		t.Errorf("windows refuse: %q", got)
	}
}
