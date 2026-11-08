package app

import (
	"net"
	"strconv"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
)

// TestPingHosts: the hosts are measured only when asked, those reached
// straight from here by a TCP connection to their port, one refusing it
// unreachable; a host through a proxy or jump hosts, or on a serial port,
// is not measured. A host given another address shows no latency until
// measured again.
func TestPingHosts(t *testing.T) {
	a, tt := newTestApp(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedPort := closed.Addr().(*net.TCPAddr).Port
	closed.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	add := func(h assets.Host) assets.Host {
		t.Helper()
		h.User = "u"
		h, err := a.store.Add(h)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	up := add(assets.Host{Name: "up", Address: "127.0.0.1", Port: port})
	down := add(assets.Host{Name: "down", Address: "127.0.0.1", Port: closedPort})
	px, err := a.store.AddProxy(assets.Proxy{ID: assets.NewID(), Name: "px", Kind: assets.ProxySOCKS5, Address: "127.0.0.1", Port: port})
	if err != nil {
		t.Fatal(err)
	}
	proxied := add(assets.Host{Name: "proxied", Address: "127.0.0.1", Port: port, ProxyID: px.ID})
	jumped := add(assets.Host{Name: "jumped", Address: "127.0.0.1", Port: port, JumpChain: []string{up.ID}})

	// Nothing is measured on its own, even as the hosts show.
	tt.Frame()
	if _, ok := a.pingOf(up); ok || a.pinging() || len(a.ping.res) != 0 {
		t.Fatal("measured without being asked")
	}

	a.PingAll()
	if !a.pinging() {
		t.Fatal("not measuring")
	}
	waitFor(t, tt, "hosts measured", func() bool { return !a.pinging() })
	if r, ok := a.pingOf(up); !ok || !r.ok || r.addr != up.Addr() {
		t.Fatalf("reachable host: %+v %v", r, ok)
	}
	if r, ok := a.pingOf(down); !ok || r.ok || r.busy {
		t.Fatalf("refusing host: %+v %v", r, ok)
	}
	for _, h := range []assets.Host{proxied, jumped, {Name: "tty", Protocol: assets.ProtoSerial}} {
		if _, ok := a.pingOf(h); ok {
			t.Fatalf("%s measured", h.Name)
		}
		if _, ok := a.ping.res[h.ID]; ok && h.ID != "" {
			t.Fatalf("%s has a result", h.Name)
		}
	}

	// Moved to the listening port, the refusing host shows nothing until
	// measured again.
	down.Port = port
	if _, err := a.store.Update(down); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := a.pingOf(down); ok || a.pinging() {
		t.Fatal("moved host kept its old result, or was measured on its own")
	}
	a.PingAll()
	waitFor(t, tt, "moved host measured", func() bool { return !a.pinging() })
	if r, ok := a.pingOf(down); !ok || !r.ok || r.addr != "127.0.0.1:"+strconv.Itoa(port) {
		t.Fatalf("moved host: %+v", r)
	}

	// A deleted host's result goes.
	if err := a.store.Delete(down.ID); err != nil {
		t.Fatal(err)
	}
	a.PingAll()
	if _, ok := a.ping.res[down.ID]; ok {
		t.Fatal("deleted host kept its result")
	}
	waitFor(t, tt, "hosts measured", func() bool { return !a.pinging() })
}
