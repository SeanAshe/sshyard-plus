package app

import (
	"context"
	"net"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// The sidebar shows how far each host is: how long a TCP connection to
// its SSH or Telnet port takes, the name looked up first. The hosts are
// measured only when the user asks, never on their own. A host reached
// through jump hosts or a proxy, or on a serial port, is not.

const (
	pingTimeout = 3 * time.Second
	// pingWorkers is how many hosts are measured at once.
	pingWorkers = 8
	// pingWidth is the room a host's latency takes at the end of its row.
	pingWidth = 52
)

// pingResult is a host's latency as last measured at addr: d once it
// connected (ok), busy while it is measured again.
type pingResult struct {
	addr string
	d    time.Duration
	ok   bool
	busy bool
}

// pingState is the latency of the hosts, by ID; sem holds the measures
// under way, which read it off the UI thread.
type pingState struct {
	res map[string]pingResult
	sem chan struct{}
}

// pingAddr is where h is measured, false for a host not reached straight
// from here.
func (a *App) pingAddr(h assets.Host) (string, bool) {
	if h.Protocol == assets.ProtoSerial || h.Address == "" || len(h.JumpChain) > 0 {
		return "", false
	}
	if _, ok := a.proxyOf(h); ok {
		return "", false
	}
	return h.Addr(), true
}

// PingAll measures every host that can be, and forgets the hosts gone.
func (a *App) PingAll() {
	ids := map[string]bool{}
	for _, h := range a.store.List() {
		ids[h.ID] = true
		a.pingHost(h)
	}
	for id := range a.ping.res {
		if !ids[id] {
			delete(a.ping.res, id)
		}
	}
}

// pinging reports whether any host is being measured.
func (a *App) pinging() bool {
	for _, r := range a.ping.res {
		if r.busy {
			return true
		}
	}
	return false
}

// pingHost measures h, unless it is already being measured; the result
// it had stays meanwhile, should its address be the same.
func (a *App) pingHost(h assets.Host) {
	addr, ok := a.pingAddr(h)
	r := a.ping.res[h.ID]
	if !ok || r.busy {
		return
	}
	if r.addr != addr {
		r = pingResult{addr: addr}
	}
	r.busy = true
	if a.ping.res == nil {
		a.ping.res = map[string]pingResult{}
	}
	if a.ping.sem == nil {
		a.ping.sem = make(chan struct{}, pingWorkers)
	}
	a.ping.res[h.ID] = r
	ctx, sem, dial := a.cloudCtx, a.ping.sem, a.p.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	go func() {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		d, err := measurePing(ctx, dial, addr)
		<-sem
		a.post(func() {
			if _, ok := a.ping.res[h.ID]; ok {
				a.ping.res[h.ID] = pingResult{addr: addr, d: d, ok: err == nil}
			}
		})
	}()
}

// measurePing is how long a TCP connection to addr takes, its host name
// looked up first, within pingTimeout.
func measurePing(ctx context.Context, dial func(ctx context.Context, network, addr string) (net.Conn, error), addr string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, err
	}
	ips, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	conn, err := dial(ctx, "tcp", net.JoinHostPort(ips[0], port))
	if err != nil {
		return 0, err
	}
	d := time.Since(start)
	conn.Close()
	return d, nil
}

// pingOf is the latency of h to show, false for a host not measured at
// its address: never, or not since it was given another.
func (a *App) pingOf(h assets.Host) (pingResult, bool) {
	addr, ok := a.pingAddr(h)
	r, seen := a.ping.res[h.ID]
	if !ok || !seen || r.addr != addr {
		return pingResult{}, false
	}
	return r, true
}

// pingText is a host's latency at the end of its sidebar row: a spinner
// while it is measured, then colored by how far it is, or unreachable
// when it could not be connected to.
func pingText(c *ui.Context, m locales.Messages, tok theme.Tokens, r pingResult) {
	if r.busy {
		ui.Spinner(c).Size(12, 12).Label(m.TestingLatency())
		return
	}
	text, color := m.HostUnreachable(), tok.Danger
	if r.ok {
		text, color = latencyValue(m, r.d, true), latencyColor(tok, r.d, true)
	}
	ui.Text(c, text).FontSize(theme.Caption).TextColor(color).SingleLine().MaxWidth(pingWidth).Shrink(0)
}
