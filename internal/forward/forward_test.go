package forward

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/netproxy"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sshtest"
)

func server(t *testing.T) *sshtest.Server {
	t.Helper()
	s, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	s.DirectTCPIP, s.TCPIPForward = true, true
	t.Cleanup(s.Close)
	return s
}

func config(t *testing.T, s *sshtest.Server) session.Config {
	t.Helper()
	kh, err := session.OpenKnownHosts(filepath.Join(t.TempDir(), "known_hosts"))
	if err != nil {
		t.Fatal(err)
	}
	kh.Add(s.Addr, s.HostKey.PublicKey())
	return session.Config{
		Name: "srv", Addr: s.Addr, User: "demo", HostKey: kh.Callback(),
		Credentials: func(context.Context) (session.Credentials, error) {
			return session.Credentials{Password: "secret"}, nil
		},
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// web serves "hello from <path>" and, under /slow, a body a byte at a
// time until the client leaves.
func web(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/flood" {
			buf := make([]byte, 64<<10)
			for {
				if _, err := w.Write(buf); err != nil {
					return
				}
			}
		}
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
	return ln.Addr().String()
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// start runs a forward; hooks set it up before it starts.
func start(t *testing.T, r Rule, cfg session.Config, hooks ...func(*Forward)) *Forward {
	t.Helper()
	f := New(r, cfg, session.Options{Backoff: 20 * time.Millisecond, MaxBackoff: 100 * time.Millisecond})
	for _, h := range hooks {
		h(f)
	}
	if err := f.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Stop)
	waitFor(t, "running", func() bool { return f.Status().State == Running })
	return f
}

func get(t *testing.T, client *http.Client, u string) string {
	t.Helper()
	resp, err := client.Get(u)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestLocal(t *testing.T) {
	srv, target := server(t), web(t)
	listen := freePort(t)
	start(t, Rule{Kind: Local, Listen: listen, Target: target}, config(t, srv))
	if got := get(t, http.DefaultClient, "http://"+listen+"/a"); got != "hello from /a" {
		t.Fatalf("got %q", got)
	}
}

func TestPortInUse(t *testing.T) {
	srv := server(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	f := New(Rule{Kind: Local, Listen: ln.Addr().String(), Target: "127.0.0.1:1"}, config(t, srv), session.Options{})
	defer f.Stop()
	if err := f.Start(); !errors.Is(err, ErrPortInUse) {
		t.Fatalf("err = %v", err)
	}
	if st := f.Status(); st.State != Error || !errors.Is(st.Err, ErrPortInUse) {
		t.Fatalf("status %+v", st)
	}
}

func socks4(t *testing.T, proxy, host string, port int, a bool) string {
	t.Helper()
	c, err := net.Dial("tcp", proxy)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	req := []byte{4, 1, 0, 0}
	binary.BigEndian.PutUint16(req[2:], uint16(port))
	if a {
		req = append(req, 0, 0, 0, 1, 0)
		req = append(append(req, host...), 0)
	} else {
		req = append(append(req, net.ParseIP(host).To4()...), 0)
	}
	c.Write(req)
	rep := make([]byte, 8)
	if _, err := io.ReadFull(c, rep); err != nil || rep[1] != 0x5a {
		t.Fatalf("socks4 reply %v %v", rep, err)
	}
	fmt.Fprintf(c, "GET /s4 HTTP/1.0\r\nHost: x\r\n\r\n")
	b, _ := io.ReadAll(c)
	return string(b)
}

func TestDynamicAllProtocols(t *testing.T) {
	srv, target := server(t), web(t)
	listen := freePort(t)
	f := start(t, Rule{Kind: Dynamic, Listen: listen}, config(t, srv))
	host, port, _ := net.SplitHostPort(target)
	var p int
	fmt.Sscan(port, &p)
	// SOCKS5 with the name resolved remotely: "localhost" is sent as is.
	for _, scheme := range []string{"socks5h", "socks5", "http"} {
		pu, _ := url.Parse(scheme + "://" + listen)
		cl := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
		if got := get(t, cl, "http://localhost:"+port+"/"+scheme); got != "hello from /"+scheme {
			t.Fatalf("%s: %q", scheme, got)
		}
		cl.CloseIdleConnections()
	}
	// HTTP CONNECT.
	c, err := net.Dial("tcp", listen)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\nGET /tunnel HTTP/1.0\r\n\r\n", target, target)
	b, _ := io.ReadAll(c)
	c.Close()
	if !strings.Contains(string(b), "200 Connection Established") || !strings.HasSuffix(string(b), "hello from /tunnel") {
		t.Fatalf("connect: %q", b)
	}
	for _, a := range []bool{false, true} {
		if got := socks4(t, listen, host, p, a); !strings.HasSuffix(got, "hello from /s4") {
			t.Fatalf("socks4 a=%v: %q", a, got)
		}
	}
	if st := f.Status(); st.State != Running {
		t.Fatalf("status %+v", st)
	}
}

func TestDynamicRestricted(t *testing.T) {
	srv, target := server(t), web(t)
	listen := freePort(t)
	start(t, Rule{Kind: Dynamic, Listen: listen, Protocol: netproxy.OnlySOCKS5}, config(t, srv))
	pu, _ := url.Parse("http://" + listen)
	resp, err := (&http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}).Get("http://" + target + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "SOCKS5 only") {
		t.Fatalf("http on a SOCKS5 port: %d %q", resp.StatusCode, b)
	}
	pu, _ = url.Parse("socks5h://" + listen)
	if got := get(t, &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}, "http://"+target+"/ok"); got != "hello from /ok" {
		t.Fatalf("socks5: %q", got)
	}
}

func TestDynamicAuth(t *testing.T) {
	srv, target := server(t), web(t)
	listen := freePort(t)
	start(t, Rule{Kind: Dynamic, Listen: listen, User: "u", Password: "p"}, config(t, srv))
	pu, _ := url.Parse("socks5h://" + listen)
	if _, err := (&http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}).Get("http://" + target + "/"); err == nil {
		t.Fatal("no credentials: want an error")
	}
	pu, _ = url.Parse("socks5h://u:p@" + listen)
	if got := get(t, &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}, "http://"+target+"/auth"); got != "hello from /auth" {
		t.Fatalf("with credentials: %q", got)
	}
}

func TestConnectionsListedAndClosed(t *testing.T) {
	srv, target := server(t), web(t)
	listen := freePort(t)
	f := start(t, Rule{Kind: Local, Listen: listen, Target: target}, config(t, srv))
	c, err := net.Dial("tcp", listen)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "GET /slow HTTP/1.1\r\nHost: x\r\n\r\n")
	br := bufio.NewReader(c)
	if _, err := br.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a connection", func() bool { return f.Status().Conns == 1 })
	conn := f.Conns.List()[0]
	if conn.Source != c.LocalAddr().String() || conn.Target != target {
		t.Fatalf("listed %q → %q", conn.Source, conn.Target)
	}
	_, down := conn.Bytes()
	waitFor(t, "bytes growing", func() bool { _, d := conn.Bytes(); return d > down+20 })
	f.Conns.Close(conn.ID)
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	// A disconnect resets the connection, so the read ends with EOF or a
	// reset — anything but running into the deadline.
	if _, err := io.Copy(io.Discard, br); err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			t.Fatalf("after disconnect: %v", err)
		}
	}
	waitFor(t, "removed", func() bool { return f.Status().Conns == 0 })
}

func TestRemote(t *testing.T) {
	srv, target := server(t), web(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := ln.Addr().String()
	ln.Close()
	start(t, Rule{Kind: Remote, Listen: listen, Target: target}, config(t, srv))
	// The test server listens on this computer, as the remote host would.
	if got := get(t, http.DefaultClient, "http://"+listen+"/r"); got != "hello from /r" {
		t.Fatalf("got %q", got)
	}
}

func TestRemoteRefused(t *testing.T) {
	srv := server(t)
	srv.TCPIPForward = false
	f := New(Rule{Kind: Remote, Listen: "127.0.0.1:0", Target: "127.0.0.1:1"}, config(t, srv), session.Options{})
	f.Start()
	defer f.Stop()
	waitFor(t, "refused", func() bool { return f.Status().State == Error })
	var re *RemoteRefusedError
	if !errors.As(f.Status().Err, &re) {
		t.Fatalf("err = %v", f.Status().Err)
	}
}

func TestReconnects(t *testing.T) {
	srv, target := server(t), web(t)
	listen := freePort(t)
	f := start(t, Rule{Kind: Local, Listen: listen, Target: target}, config(t, srv))
	saw := make(chan struct{}, 1)
	f.SetOnChange(func() {
		if f.Status().State == Reconnecting {
			select {
			case saw <- struct{}{}:
			default:
			}
		}
	})
	srv.Drop()
	select {
	case <-saw:
	case <-time.After(10 * time.Second):
		t.Fatal("never reconnecting")
	}
	waitFor(t, "running again", func() bool { return f.Status().State == Running })
	if got := get(t, http.DefaultClient, "http://"+listen+"/again"); got != "hello from /again" {
		t.Fatalf("got %q", got)
	}
}

// When the connection to the server goes, the relays that rode it end at
// once, even one stuck writing to a client that does not read.
func TestDropEndsRelays(t *testing.T) {
	srv, target := server(t), web(t)
	listen := freePort(t)
	f := start(t, Rule{Kind: Local, Listen: listen, Target: target}, config(t, srv))
	c, err := net.Dial("tcp", listen)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.(*net.TCPConn).SetReadBuffer(4 << 10)
	fmt.Fprintf(c, "GET /flood HTTP/1.1\r\nHost: x\r\n\r\n")
	// The send buffer fills and writes block. How much it holds differs
	// by OS, so wait until accepted bytes stop growing.
	var last int64
	var still time.Time
	waitFor(t, "relay backed up", func() bool {
		ls := f.Conns.List()
		if len(ls) != 1 {
			return false
		}
		_, down := ls[0].Bytes()
		if down <= 0 || down != last {
			last, still = down, time.Time{}
			return false
		}
		if still.IsZero() {
			still = time.Now()
			return false
		}
		return time.Since(still) >= 150*time.Millisecond
	})
	srv.Drop()
	waitFor(t, "relay ended", func() bool { return f.Status().Conns == 0 })
	waitFor(t, "running again", func() bool { return f.Status().State == Running })
}

func TestSharesPool(t *testing.T) {
	srv, target := server(t), web(t)
	cfg := config(t, srv)
	cfg.Pool, cfg.Key = session.NewPool(), "k"
	term := session.New(cfg, session.Options{NoShell: true}, 80, 24)
	term.Start()
	defer term.Close()
	waitFor(t, "term", func() bool { return term.Status().State == session.Connected })
	start(t, Rule{Kind: Local, Listen: freePort(t), Target: target}, cfg)
	if srv.Logins() != 1 || cfg.Pool.Shared("k") != 2 {
		t.Fatalf("logins %d shared %d", srv.Logins(), cfg.Pool.Shared("k"))
	}
}

func TestStopClosesEverything(t *testing.T) {
	srv, target := server(t), web(t)
	listen := freePort(t)
	f := start(t, Rule{Kind: Local, Listen: listen, Target: target}, config(t, srv))
	f.Stop()
	if st := f.Status(); st.State != Stopped {
		t.Fatalf("status %+v", st)
	}
	waitFor(t, "port free", func() bool {
		ln, err := net.Listen("tcp", listen)
		if err == nil {
			ln.Close()
		}
		return err == nil
	})
}

// alive is a target that greets every connection with "alive" and tells
// when one is closed from the other end.
func alive(t *testing.T) (addr string, closed <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	ch := make(chan struct{}, 16)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.Write([]byte("alive"))
				io.Copy(io.Discard, c)
				ch <- struct{}{}
			}()
		}
	}()
	return ln.Addr().String(), ch
}

// pendingDial holds each dial until released, then makes it with next
// regardless of its context, as a dial already under way would finish.
type pendingDial struct {
	started chan context.Context
	release chan struct{}
	next    dialFunc
}

func newPendingDial(next dialFunc) *pendingDial {
	return &pendingDial{started: make(chan context.Context, 1), release: make(chan struct{}), next: next}
}

func (p *pendingDial) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	p.started <- ctx
	<-p.release
	return p.next(context.Background(), network, addr)
}

func recv[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

// sharedTerm is a terminal on the pooled connection a forward of the
// returned config shares, as another tab of the host would be.
func sharedTerm(t *testing.T, srv *sshtest.Server) (session.Config, *session.Session) {
	t.Helper()
	cfg := config(t, srv)
	cfg.Pool, cfg.Key = session.NewPool(), "k"
	term := session.New(cfg, session.Options{NoShell: true}, 80, 24)
	term.Start()
	t.Cleanup(term.Close)
	waitFor(t, "term", func() bool { return term.Status().State == session.Connected })
	return cfg, term
}

// viaTerm dials from the server on term's connection, which outlives the
// forward's own session.
func viaTerm(term *session.Session) dialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return term.Client().DialContext(ctx, network, addr)
	}
}

// watchConns records whether f ever lists a connection.
func watchConns(saw *atomic.Bool) func(*Forward) {
	return func(f *Forward) {
		f.SetOnChange(func() {
			if f.Conns.Len() > 0 {
				saw.Store(true)
			}
		})
	}
}

// stopDuringDial stops f while pd holds the dial for client, runs
// afterStop, then lets the dial through: the forward must close what it
// brings and relay nothing, and the terminal sharing its connection must
// still work.
func stopDuringDial(t *testing.T, f *Forward, pd *pendingDial, saw *atomic.Bool, term *session.Session, target string, closed <-chan struct{}, client net.Conn, afterStop func()) {
	t.Helper()
	ctx := recv(t, "the dial", pd.started)
	f.Stop()
	if afterStop != nil {
		afterStop()
	}
	recv(t, "the pending dial cancelled", ctx.Done())
	close(pd.release)
	recv(t, "the late connection closed", closed)
	client.SetReadDeadline(time.Now().Add(10 * time.Second))
	b, err := io.ReadAll(client)
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatalf("client still open after stop: read %q", b)
	}
	if strings.Contains(string(b), "alive") || (len(b) > 1 && b[1] == 0x5a) {
		t.Fatalf("relayed after stop: %q", b)
	}
	if st := f.Status(); st.State != Stopped || st.Conns != 0 || saw.Load() {
		t.Fatalf("status %+v, listed a connection: %v", st, saw.Load())
	}
	c, err := term.Client().DialContext(context.Background(), "tcp", target)
	if err != nil {
		t.Fatalf("shared connection after stop: %v", err)
	}
	defer c.Close()
	got := make([]byte, 5)
	if _, err := io.ReadFull(c, got); err != nil || string(got) != "alive" {
		t.Fatalf("shared connection read %q %v", got, err)
	}
}

func TestRemoteStopPendingDial(t *testing.T) {
	srv := server(t)
	cfg, term := sharedTerm(t, srv)
	target, closed := alive(t)
	listen := freePort(t)
	var d net.Dialer
	pd := newPendingDial(d.DialContext)
	var saw atomic.Bool
	f := start(t, Rule{Kind: Remote, Listen: listen, Target: target}, cfg,
		func(f *Forward) { f.dialOut = pd.dial }, watchConns(&saw))
	client, err := net.Dial("tcp", listen)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	stopDuringDial(t, f, pd, &saw, term, target, closed, client, nil)
}

func TestLocalStopPendingDial(t *testing.T) {
	srv := server(t)
	cfg, term := sharedTerm(t, srv)
	target, closed := alive(t)
	listen := freePort(t)
	pd := newPendingDial(viaTerm(term))
	var saw atomic.Bool
	f := start(t, Rule{Kind: Local, Listen: listen, Target: target}, cfg,
		func(f *Forward) { f.dialIn = pd.dial }, watchConns(&saw))
	client, err := net.Dial("tcp", listen)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	stopDuringDial(t, f, pd, &saw, term, target, closed, client, func() {
		// The port is free as soon as Stop returns, a dial still pending.
		ln, err := net.Listen("tcp", listen)
		if err != nil {
			t.Fatalf("port not free after stop: %v", err)
		}
		ln.Close()
	})
}

func TestDynamicStopPendingDial(t *testing.T) {
	srv := server(t)
	cfg, term := sharedTerm(t, srv)
	target, closed := alive(t)
	listen := freePort(t)
	pd := newPendingDial(viaTerm(term))
	var saw atomic.Bool
	f := start(t, Rule{Kind: Dynamic, Listen: listen}, cfg,
		func(f *Forward) { f.dialIn = pd.dial }, watchConns(&saw))
	client, err := net.Dial("tcp", listen)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	host, port, _ := net.SplitHostPort(target)
	var p int
	fmt.Sscan(port, &p)
	req := []byte{4, 1, 0, 0}
	binary.BigEndian.PutUint16(req[2:], uint16(p))
	client.Write(append(append(req, net.ParseIP(host).To4()...), 0))
	stopDuringDial(t, f, pd, &saw, term, target, closed, client, nil)
}
