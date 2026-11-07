package app

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/forward"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func fwdServer(t *testing.T) *sshtest.Server {
	t.Helper()
	s := startServer(t)
	s.DirectTCPIP, s.TCPIPForward = true, true
	return s
}

// webServer answers "hello from <path>", and /slow a byte at a time.
func webServer(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			for {
				if _, err := w.Write([]byte("x")); err != nil {
					return
				}
				w.(http.Flusher).Flush()
				time.Sleep(10 * time.Millisecond)
			}
		}
		fmt.Fprintf(w, "hello from %s", r.URL.Path)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return "127.0.0.1", ln.Addr().(*net.TCPAddr).Port
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

var noKeep = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

func httpGet(cl *http.Client, u string) (string, error) {
	resp, err := cl.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

func waitRunning(t *testing.T, a *App, tt *ui.Tester, id string) {
	t.Helper()
	waitFor(t, tt, "forward running", func() bool { st, _ := a.ForwardStatus(id); return st.State == forward.Running })
}

func TestForwardSheetLocal(t *testing.T) {
	srv := fwdServer(t)
	a, tt := newTestApp(t)
	m := locales.In("en")
	h := addHost(t, a, srv, "box", "secret")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	tt.Frame()
	// The host's context menu opens the sheet with the host chosen.
	a.openFwdForm(m, nil, h.ID)
	tt.Frame()
	if f := a.fwdForm; f.host != "box" || f.listenAddr != "127.0.0.1" {
		t.Fatalf("defaults: host %q listen %q", f.host, f.listenAddr)
	}
	// A port is required.
	if a.SaveFwdForm(m) || a.fwdForm.err != m.InvalidPort() {
		t.Fatalf("saved without a port: %q", a.fwdForm.err)
	}
	webHost, webPort := webServer(t)
	listen := freeTCPPort(t)
	f := a.fwdForm
	f.name, f.listenPort, f.targetAddr, f.targetPort = "web", strconv.Itoa(listen), webHost, strconv.Itoa(webPort)
	if !a.SaveFwdForm(m) {
		t.Fatalf("not saved: %q", f.err)
	}
	fw := a.Store().Forwards()[0]
	if fw.Kind != assets.ForwardLocal || fw.HostID != h.ID || fw.ListenAddr != "127.0.0.1" || fw.TargetPort != webPort {
		t.Fatalf("saved %+v", fw)
	}
	tt.Frame()
	// The sidebar lists it, stopped; its switch starts it.
	if _, on := a.ForwardStatus(fw.ID); on {
		t.Fatal("started on saving")
	}
	mustClick(t, tt, "Start")
	waitRunning(t, a, tt, fw.ID)
	if a.fwdOpen[fw.ID] {
		t.Error("the switch also opened the row")
	}
	got, err := httpGet(noKeep, "http://127.0.0.1:"+strconv.Itoa(listen)+"/a")
	if err != nil || got != "hello from /a" {
		t.Fatalf("through the forward: %q %v", got, err)
	}
	// A terminal on the host shares the forward's connection, and the
	// host counts the forward.
	a.Connect(h.ID, openAt{})
	c := a.focused()
	waitFor(t, tt, "terminal", func() bool { return c.s.Status().State == session.Connected })
	if srv.Logins() != 1 {
		t.Errorf("logins %d: the terminal did not share the forward's connection", srv.Logins())
	}
	if a.runningForwards(h.ID) != 1 {
		t.Errorf("header count %d", a.runningForwards(h.ID))
	}
	// A click on the row lists its connections.
	mustClick(t, tt, "Port forward web")
	tt.Frame()
	if !a.fwdOpen[fw.ID] || len(a.forwardConns(fw.ID)) != 0 {
		t.Fatalf("expanded %v, %d connections", a.fwdOpen[fw.ID], len(a.forwardConns(fw.ID)))
	}
	mustClick(t, tt, "Stop")
	tt.Frame()
	if _, on := a.ForwardStatus(fw.ID); on {
		t.Fatal("not stopped")
	}
	if _, err := httpGet(noKeep, "http://127.0.0.1:"+strconv.Itoa(listen)+"/a"); err == nil {
		t.Error("the port still answers after stop")
	}
	if c.s.Status().State != session.Connected {
		t.Error("stopping the forward closed the terminal's connection")
	}
}

func TestForwardPortInUse(t *testing.T) {
	srv := fwdServer(t)
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "box", "secret")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	fw, err := a.Store().SaveForward(assets.Forward{Name: "busy", Kind: assets.ForwardLocal, HostID: h.ID,
		ListenPort: ln.Addr().(*net.TCPAddr).Port, TargetAddr: "127.0.0.1", TargetPort: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.StartForward(fw.ID); !errors.Is(err, forward.ErrPortInUse) {
		t.Fatalf("started on a port in use: %v", err)
	}
	tt.Frame()
	if st, _ := a.ForwardStatus(fw.ID); st.State != forward.Error || !errors.Is(st.Err, forward.ErrPortInUse) {
		t.Fatalf("status %v %v", st.State, st.Err)
	}
	// A failed rule has no connections.
	if cs := a.forwardConns(fw.ID); len(cs) != 0 {
		t.Fatalf("%d connections on a failed rule", len(cs))
	}
}

func TestForwardDynamicAuthKeychain(t *testing.T) {
	srv := fwdServer(t)
	a, tt := newTestApp(t)
	m := locales.In("en")
	h := addHost(t, a, srv, "box", "secret")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	a.openFwdForm(m, nil, h.ID)
	f := a.fwdForm
	f.kind, f.listenAddr = 2, "0.0.0.0"
	port := freeTCPPort(t)
	f.listenPort = strconv.Itoa(port)
	tt.Frame()
	f = a.fwdForm
	f.kind, f.listenAddr, f.listenPort, f.user = 2, "0.0.0.0", strconv.Itoa(port), "u"
	// A user needs a password.
	if a.SaveFwdForm(m) {
		t.Fatal("saved a user without a password")
	}
	f.password = "p4ss-forward"
	if !a.SaveFwdForm(m) {
		t.Fatalf("not saved: %q", f.err)
	}
	fw := a.Store().Forwards()[0]
	if pw, err := a.kc.Get(keychain.ForwardPassword(fw.ID)); err != nil || string(pw) != "p4ss-forward" {
		t.Fatalf("keychain: %q %v", pw, err)
	}
	entries, _ := os.ReadDir(a.p.DataDir)
	for _, e := range entries {
		if b, _ := os.ReadFile(filepath.Join(a.p.DataDir, e.Name())); strings.Contains(string(b), "p4ss-forward") {
			t.Errorf("password in %s", e.Name())
		}
	}
	if err := a.StartForward(fw.ID); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, a, tt, fw.ID)
	webHost, webPort := webServer(t)
	target := "http://" + webHost + ":" + strconv.Itoa(webPort) + "/d"
	proxy := func(u string) *http.Client {
		pu, _ := url.Parse(u)
		return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
	}
	if _, err := httpGet(proxy("socks5h://127.0.0.1:"+strconv.Itoa(port)), target); err == nil {
		t.Error("no credentials: want refused")
	}
	if got, err := httpGet(proxy("socks5h://u:p4ss-forward@127.0.0.1:"+strconv.Itoa(port)), target); err != nil || got != "hello from /d" {
		t.Errorf("with credentials: %q %v", got, err)
	}
	// Deleting it, from its sheet, asks first; Cancel keeps everything.
	ff := fw
	a.openFwdForm(m, &ff, "")
	tt.Frame()
	mustClick(t, tt, "Delete")
	tt.Frame()
	if !a.fwdDeleting {
		t.Fatal("no confirmation")
	}
	mustClick(t, tt, "Cancel")
	tt.Frame()
	if a.fwdDeleting || !a.kc.Has(keychain.ForwardPassword(fw.ID)) || len(a.Store().Forwards()) != 1 {
		t.Fatal("cancel deleted")
	}
	// Confirmed, it stops and takes its password.
	ff = fw
	a.openFwdForm(m, &ff, "")
	tt.Frame()
	mustClick(t, tt, "Delete")
	tt.Frame()
	mustClick(t, tt, "Delete")
	tt.Frame()
	if a.kc.Has(keychain.ForwardPassword(fw.ID)) || len(a.Store().Forwards()) != 0 || a.fwds[fw.ID] != nil {
		t.Error("delete left something")
	}
}

func TestForwardAsksPasswordAndListsConns(t *testing.T) {
	srv := fwdServer(t)
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "ask", "")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	webHost, webPort := webServer(t)
	listen := freeTCPPort(t)
	fw, err := a.Store().SaveForward(assets.Forward{Kind: assets.ForwardLocal, HostID: h.ID, ListenPort: listen, TargetAddr: webHost, TargetPort: webPort})
	if err != nil {
		t.Fatal(err)
	}
	a.StartForward(fw.ID)
	// No pane is open: the forward's own connection asks.
	waitFor(t, tt, "password prompt", func() bool { return a.prompting })
	if st, _ := a.ForwardStatus(fw.ID); st.State != forward.Connecting {
		t.Errorf("while asking: %v %v", st.State, st.Err)
	}
	a.prompt.value = "secret"
	a.SubmitPrompt()
	waitRunning(t, a, tt, fw.ID)
	c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(listen))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "GET /slow HTTP/1.1\r\nHost: x\r\n\r\n")
	buf := make([]byte, 64)
	if _, err := c.Read(buf); err != nil {
		t.Fatal(err)
	}
	a.fwdOpen[fw.ID] = true
	waitFor(t, tt, "connection", func() bool { return len(a.forwardConns(fw.ID)) == 1 })
	if cs := a.forwardConns(fw.ID); cs[0].Source != c.LocalAddr().String() {
		t.Fatalf("connection from %q, want %q", cs[0].Source, c.LocalAddr())
	}
	tt.Frame()
	mustClick(t, tt, "Disconnect")
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.Copy(io.Discard, c); err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			t.Errorf("client not cut: %v", err)
		}
	}
	waitFor(t, tt, "connection gone", func() bool { return len(a.forwardConns(fw.ID)) == 0 })
}

func TestForwardAutoStart(t *testing.T) {
	srv := fwdServer(t)
	dir := t.TempDir()
	kc := keychain.NewMemory()
	a, _ := appIn(t, dir, kc)
	h := addHost(t, a, srv, "box", "secret")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	webHost, webPort := webServer(t)
	listen := freeTCPPort(t)
	fw, err := a.Store().SaveForward(assets.Forward{Kind: assets.ForwardLocal, HostID: h.ID, ListenPort: listen, TargetAddr: webHost, TargetPort: webPort, AutoStart: true})
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	// The next launch waits for an explicit secrets action before auto-starting.
	b, err := New(Platform{DataDir: dir, Secrets: kc, HomeDir: dir, CacheDir: filepath.Join(dir, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	b.Start()
	tt := ui.NewTester(b.View, 1200, 760)
	tt.Frame()
	if _, on := b.ForwardStatus(fw.ID); on || b.unlocked() {
		t.Fatal("auto-forward started before explicit unlock")
	}
	b.withSecrets(nil)
	waitRunning(t, b, tt, fw.ID)
	if got, err := httpGet(noKeep, "http://127.0.0.1:"+strconv.Itoa(listen)+"/auto"); err != nil || got != "hello from /auto" {
		t.Errorf("%q %v", got, err)
	}
}

func TestForwardReconnectsAndHostDeleteBlocked(t *testing.T) {
	srv := fwdServer(t)
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "box", "secret")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	webHost, webPort := webServer(t)
	listen := freeTCPPort(t)
	fw, _ := a.Store().SaveForward(assets.Forward{Name: "r", Kind: assets.ForwardRemote, HostID: h.ID, ListenPort: listen, TargetAddr: webHost, TargetPort: webPort})
	a.StartForward(fw.ID)
	waitRunning(t, a, tt, fw.ID)
	// The test server listens on this computer, as the remote host would.
	u := "http://127.0.0.1:" + strconv.Itoa(listen) + "/r"
	if got, err := httpGet(noKeep, u); err != nil || got != "hello from /r" {
		t.Fatalf("-R: %q %v", got, err)
	}
	srv.Drop()
	waitFor(t, tt, "reconnected", func() bool {
		st, _ := a.ForwardStatus(fw.ID)
		return st.State == forward.Running && st.Session.Connects >= 2
	})
	if got, err := httpGet(&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}, u); err != nil || got != "hello from /r" {
		t.Fatalf("-R after reconnect: %q %v", got, err)
	}
	if err := a.store.Delete(h.ID); err == nil {
		t.Fatal("deleted a host a forward uses")
	} else if txt := a.inUseText(locales.In("en"), err); !strings.Contains(txt, "r") {
		t.Errorf("in use: %q", txt)
	}
}
