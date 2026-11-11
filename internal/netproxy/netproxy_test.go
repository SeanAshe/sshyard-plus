package netproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/sshtest"
	"golang.org/x/crypto/ssh"
)

// echo serves one line back, upper-cased, per connection.
func echo(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, _ := bufio.NewReader(c).ReadString('\n')
				io.WriteString(c, strings.ToUpper(line))
			}()
		}
	}()
	return l.Addr().String()
}

// serve starts s on a loopback port; names "echo.test" go to echoAddr,
// as a far side resolving them would.
func serve(t *testing.T, s *Server, echoAddr string) string {
	t.Helper()
	if s.Conns == nil {
		s.Conns = &Registry{}
	}
	s.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if h, p, _ := net.SplitHostPort(addr); h == "echo.test" {
			_, ep, _ := net.SplitHostPort(echoAddr)
			if p != ep {
				return nil, errors.New("connection refused")
			}
			addr = echoAddr
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go s.Serve(l)
	return l.Addr().String()
}

func roundTrip(t *testing.T, c net.Conn) string {
	t.Helper()
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(c, "hello\n")
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return line
}

func TestDialerThroughServer(t *testing.T) {
	e := echo(t)
	_, port, _ := net.SplitHostPort(e)
	for _, kind := range []Kind{HTTP, SOCKS5} {
		for _, auth := range []bool{false, true} {
			t.Run(fmt.Sprint(kind, auth), func(t *testing.T) {
				s := &Server{}
				if auth {
					s.User, s.Password = "u", "p@ss"
				}
				addr := serve(t, s, e)
				d := &Dialer{Kind: kind, Addr: addr}
				if auth {
					d.User = "u"
					d.Password = func(context.Context) (string, error) { return "p@ss", nil }
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				c, err := d.DialContext(ctx, "tcp", "echo.test:"+port)
				if err != nil {
					t.Fatal(err)
				}
				if got := roundTrip(t, c); got != "HELLO\n" {
					t.Fatalf("got %q", got)
				}
				if !auth {
					return
				}
				d.Password = func(context.Context) (string, error) { return "wrong", nil }
				_, err = d.DialContext(ctx, "tcp", "echo.test:"+port)
				if !errors.Is(err, ErrAuth) {
					t.Fatalf("wrong password: %v, want ErrAuth", err)
				}
				var pe *Error
				if !errors.As(err, &pe) {
					t.Fatalf("not a proxy error: %v", err)
				}
			})
		}
	}
}

func TestDialerRefused(t *testing.T) {
	e := echo(t)
	for _, kind := range []Kind{HTTP, SOCKS5} {
		addr := serve(t, &Server{}, e)
		d := &Dialer{Kind: kind, Addr: addr}
		_, err := d.DialContext(context.Background(), "tcp", "echo.test:1")
		var re *RefusedError
		if !errors.As(err, &re) {
			t.Fatalf("%s: %v, want RefusedError", kind, err)
		}
	}
	// No proxy there at all.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	_, err := (&Dialer{Kind: SOCKS5, Addr: addr}).DialContext(context.Background(), "tcp", "x:1")
	var pe *Error
	if !errors.As(err, &pe) {
		t.Fatalf("%v, want proxy Error", err)
	}
}

func socks4(t *testing.T, addr string, req []byte) (byte, net.Conn) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(5 * time.Second))
	c.Write(req)
	var rep [8]byte
	if _, err := io.ReadFull(c, rep[:]); err != nil {
		t.Fatal(err)
	}
	return rep[1], c
}

func TestSOCKS4(t *testing.T) {
	e := echo(t)
	host, port, _ := net.SplitHostPort(e)
	var pb [2]byte
	p := 0
	fmt.Sscan(port, &p)
	pb[0], pb[1] = byte(p>>8), byte(p)
	reg := &Registry{}
	addr := serve(t, &Server{Conns: reg}, e)
	ip := net.ParseIP(host).To4()
	code, c := socks4(t, addr, append(append([]byte{4, 1, pb[0], pb[1]}, ip...), 'u', 0))
	if code != 0x5a {
		t.Fatalf("socks4 reply %#x", code)
	}
	if got := roundTrip(t, c); got != "HELLO\n" {
		t.Fatalf("socks4: %q", got)
	}
	code, c = socks4(t, addr, append([]byte{4, 1, pb[0], pb[1], 0, 0, 0, 1, 0}, "echo.test\x00"...))
	if code != 0x5a {
		t.Fatalf("socks4a reply %#x", code)
	}
	if got := roundTrip(t, c); got != "HELLO\n" {
		t.Fatalf("socks4a: %q", got)
	}
	// With a password set, SOCKS4 cannot log in.
	addr = serve(t, &Server{User: "u", Password: "p"}, e)
	if code, c := socks4(t, addr, append(append([]byte{4, 1, pb[0], pb[1]}, ip...), 0)); code != 0x5b {
		t.Fatalf("socks4 with auth required: %#x", code)
	} else {
		c.Close()
	}
}

func TestPlainHTTPAndRestriction(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "path=%s proxyauth=%q", r.URL.RequestURI(), r.Header.Get("Proxy-Authorization"))
	}))
	defer web.Close()
	var errs = make(chan error, 4)
	addr := serve(t, &Server{User: "u", Password: "p"}, "")
	get := func(proxy string) (int, string) {
		cl := &http.Client{Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) { return url.Parse(proxy) }}, Timeout: 5 * time.Second}
		resp, err := cl.Get(web.URL + "/a?b=1")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get("http://u:p@" + addr); code != 200 || body != `path=/a?b=1 proxyauth=""` {
		t.Fatalf("plain http: %d %q", code, body)
	}
	if code, _ := get("http://" + addr); code != http.StatusProxyAuthRequired {
		t.Fatalf("no auth: %d", code)
	}
	only := serve(t, &Server{Protocol: OnlySOCKS5, OnError: func(err error) { errs <- err }}, "")
	code, body := get("http://" + only)
	if code != 400 || !strings.Contains(body, "SOCKS5 only") {
		t.Fatalf("http on socks5-only: %d %q", code, body)
	}
	if err := <-errs; !errors.Is(err, ErrProtocol) {
		t.Fatalf("OnError %v", err)
	}
}

func TestRegistryListsAndCloses(t *testing.T) {
	e := echo(t)
	_, port, _ := net.SplitHostPort(e)
	reg := &Registry{}
	addr := serve(t, &Server{Conns: reg}, e)
	c, err := (&Dialer{Kind: SOCKS5, Addr: addr}).DialContext(context.Background(), "tcp", "echo.test:"+port)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	io.WriteString(c, "x")
	deadline := time.Now().Add(5 * time.Second)
	for reg.Len() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	ls := reg.List()
	if len(ls) != 1 || ls[0].Target != "echo.test:"+port || ls[0].Protocol != "socks5" || ls[0].Source != c.LocalAddr().String() {
		t.Fatalf("list %+v", ls)
	}
	for up, _ := ls[0].Bytes(); up != 1 && time.Now().Before(deadline); up, _ = ls[0].Bytes() {
		time.Sleep(10 * time.Millisecond)
	}
	if !reg.Close(ls[0].ID) {
		t.Fatal("close")
	}
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadAll(c); isTimeout(err) {
		t.Fatalf("read after close: %v", err)
	}
	for reg.Len() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if reg.Len() != 0 {
		t.Fatal("still listed")
	}
}

// After Shut a relay is refused: both ends close and nothing is listed.
func TestRegistryShutRefusesRelay(t *testing.T) {
	reg := &Registry{}
	listed := false
	reg.OnChange = func() { listed = true }
	reg.Shut()
	srcA, srcB := net.Pipe()
	dstA, dstB := net.Pipe()
	defer srcB.Close()
	defer dstB.Close()
	reg.Relay(srcA, dstA, "t", "")
	if listed || reg.Len() != 0 {
		t.Fatalf("listed %v len %d", listed, reg.Len())
	}
	for _, c := range []net.Conn{srcB, dstB} {
		if _, err := c.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("write to a refused relay: %v", err)
		}
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// isReset reports a TCP reset. Unix surfaces ECONNRESET. Windows surfaces
// WSAECONNRESET (10054); syscall.ECONNRESET there is an invented value
// and is not what the socket returns.
func isReset(err error) bool {
	var n syscall.Errno
	return errors.As(err, &n) && (n == syscall.ECONNRESET || n == 10054)
}

// Registry.Close aborts the relay (a TCP reset), so the client's read
// ends with a connection reset rather than io.EOF. Bytes already in the
// client's own receive buffer may still be returned before that error.
// A graceful close delivers what was queued and then io.EOF.
//
// The client has not read while the relay filled its buffer, so the reset
// can reach it with its receive window shut, and its TCP may drop it: it
// then learns of the abort only when it next sends. So when the data
// stops, it sends a byte, which the closed socket answers with a reset;
// a graceful close would have ended in io.EOF before the data stopped.
func TestRegistryCloseResetsSlowReader(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 64<<10)
		for {
			if _, err := c.Write(buf); err != nil {
				return
			}
		}
	}()
	reg := &Registry{}
	addr := serve(t, &Server{Conns: reg}, l.Addr().String())
	c, err := (&Dialer{Kind: SOCKS5, Addr: addr}).DialContext(context.Background(), "tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// The relay is up once it has copied something. No pause: how full
	// the buffers are is not the property under test.
	deadline := time.Now().Add(5 * time.Second)
	var id uint64
	for {
		ls := reg.List()
		if len(ls) == 1 {
			if _, down := ls[0].Bytes(); down > 0 {
				id = ls[0].ID
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("relay never got going")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !reg.Close(id) {
		t.Fatal("close")
	}
	deadline = time.Now().Add(5 * time.Second)
	buf := make([]byte, 32<<10)
	for {
		next := time.Now().Add(200 * time.Millisecond)
		if next.After(deadline) {
			next = deadline
		}
		c.SetReadDeadline(next)
		_, err = c.Read(buf)
		if err == nil {
			continue
		}
		if !isTimeout(err) || time.Now().After(deadline) {
			break
		}
		c.Write([]byte{0})
	}
	if isTimeout(err) {
		t.Fatalf("still open after disconnect: %v", err)
	}
	if !isReset(err) {
		t.Fatalf("read ended with %v, want a connection reset", err)
	}
}

// eagerProxy answers CONNECT with the 200 and the target's first bytes
// (an SSH banner) in a single write, as a proxy forwarding at once can.
func eagerProxy(t *testing.T, target string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				if _, err := http.ReadRequest(br); err != nil {
					return
				}
				up, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer up.Close()
				ur := bufio.NewReader(up)
				banner, err := ur.ReadString('\n')
				if err != nil {
					return
				}
				if _, err := io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n"+banner); err != nil {
					return
				}
				go io.Copy(up, br)
				io.Copy(c, ur)
			}()
		}
	}()
	return l.Addr().String()
}

func TestHTTPConnectKeepsEarlyBytes(t *testing.T) {
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	d := &Dialer{Kind: HTTP, Addr: eagerProxy(t, srv.Addr)}
	cfg := &ssh.ClientConfig{
		User:            "demo",
		Auth:            []ssh.AuthMethod{ssh.Password("secret")},
		HostKeyCallback: ssh.FixedHostKey(srv.HostKey.PublicKey()),
		Timeout:         5 * time.Second,
	}
	for i := range 30 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		conn, err := d.DialContext(ctx, "tcp", srv.Addr)
		cancel()
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		sc, chans, reqs, err := ssh.NewClientConn(conn, srv.Addr, cfg)
		if err != nil {
			conn.Close()
			t.Fatalf("handshake %d: %v", i, err)
		}
		ssh.NewClient(sc, chans, reqs).Close()
	}
}
