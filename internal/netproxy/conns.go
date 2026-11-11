package netproxy

import (
	"io"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Conn is one connection relayed by a forward or a proxy: from Source,
// to Target, since Start. It lives in memory only.
type Conn struct {
	ID       uint64
	Source   string
	Target   string
	Protocol string // "socks5", "socks4", "socks4a", "http", "connect"; "" for a fixed target
	Start    time.Time
	up, down atomic.Int64
	a, b     net.Conn
	once     sync.Once
}

// Bytes are what went from the source to the target (up) and back.
func (c *Conn) Bytes() (up, down int64) { return c.up.Load(), c.down.Load() }

// Close ends the connection both ways, at once: what the kernel still
// holds to send is dropped (a reset), so a client that reads slowly sees
// the disconnect now rather than after its buffer drains.
func (c *Conn) Close() {
	c.once.Do(func() {
		abort(c.a)
		abort(c.b)
	})
}

// closeQuiet closes after a relay ended by itself, letting queued data
// still reach the peer.
func (c *Conn) closeQuiet() {
	c.once.Do(func() {
		c.a.Close()
		c.b.Close()
	})
}

// abort closes conn with a reset when it is TCP (or wraps TCP).
func abort(conn net.Conn) {
	for {
		if l, ok := conn.(interface{ SetLinger(int) error }); ok {
			l.SetLinger(0)
			break
		}
		u, ok := conn.(interface{ NetConn() net.Conn })
		if !ok {
			break
		}
		conn = u.NetConn()
	}
	conn.Close()
}

// Registry is the connections a forward relays now.
type Registry struct {
	mu    sync.Mutex
	next  uint64
	conns map[uint64]*Conn
	shut  bool
	// OnChange, when set, is called when a connection comes or goes.
	OnChange func()
}

func (r *Registry) changed() {
	if r.OnChange != nil {
		r.OnChange()
	}
}

// Relay copies between src (the connection accepted) and dst (the one
// dialed) until either ends, listed in the registry meanwhile. It blocks.
// After Shut it closes both at once and relays nothing.
func (r *Registry) Relay(src, dst net.Conn, target, protocol string) {
	c := &Conn{Source: src.RemoteAddr().String(), Target: target, Protocol: protocol, Start: time.Now(), a: src, b: dst}
	if r != nil {
		r.mu.Lock()
		if r.shut {
			r.mu.Unlock()
			c.Close()
			return
		}
		if r.conns == nil {
			r.conns = map[uint64]*Conn{}
		}
		r.next++
		c.ID = r.next
		r.conns[c.ID] = c
		r.mu.Unlock()
		r.changed()
		defer func() {
			r.mu.Lock()
			delete(r.conns, c.ID)
			r.mu.Unlock()
			r.changed()
		}()
	}
	var wg sync.WaitGroup
	wg.Add(2)
	cp := func(to, from net.Conn, n *atomic.Int64) {
		defer wg.Done()
		io.Copy(counter{to, n}, from)
		// Half-close when the connection can, so a request-then-response
		// protocol still gets its answer; else close both.
		if cw, ok := to.(interface{ CloseWrite() error }); ok && cw.CloseWrite() == nil {
			return
		}
		c.closeQuiet()
	}
	go cp(dst, src, &c.up)
	go cp(src, dst, &c.down)
	wg.Wait()
	c.closeQuiet()
}

type counter struct {
	w io.Writer
	n *atomic.Int64
}

func (c counter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n.Add(int64(n))
	return n, err
}

// List returns the connections, oldest first.
func (r *Registry) List() []*Conn {
	r.mu.Lock()
	out := make([]*Conn, 0, len(r.conns))
	for _, c := range r.conns {
		out = append(out, c)
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Len is how many connections there are.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.conns)
}

// Close ends the connection of id; false when there is none.
func (r *Registry) Close(id uint64) bool {
	r.mu.Lock()
	c := r.conns[id]
	r.mu.Unlock()
	if c == nil {
		return false
	}
	c.Close()
	return true
}

// Shut ends every connection and refuses new ones for good: a Relay
// racing it either is listed in time to be closed here or closes itself.
func (r *Registry) Shut() {
	r.mu.Lock()
	r.shut = true
	r.mu.Unlock()
	r.CloseAll()
}

// CloseAll ends every connection.
func (r *Registry) CloseAll() {
	for _, c := range r.List() {
		c.Close()
	}
}
