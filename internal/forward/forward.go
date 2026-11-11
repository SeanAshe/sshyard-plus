// Package forward runs port forwards over SSH: local (ssh -L), remote
// (ssh -R) and dynamic (ssh -D, a local HTTP/SOCKS proxy whose targets
// are dialed, and their names resolved, on the server). A forward rides
// a session without a shell, so it shares the host's pooled connection
// with the host's terminals and reconnects with the session's backoff.
package forward

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Licoy/sshyard/internal/netproxy"
	"github.com/Licoy/sshyard/internal/session"
	"golang.org/x/crypto/ssh"
)

// Kind is the kind of forward.
type Kind int

const (
	Local Kind = iota
	Remote
	Dynamic
)

// Rule is what a forward does.
type Rule struct {
	Kind Kind
	// Listen is host:port: on this computer for Local and Dynamic, on the
	// server for Remote.
	Listen string
	// Target is host:port: seen from the server for Local, from this
	// computer for Remote; unused for Dynamic.
	Target string
	// Protocol limits a dynamic forward's proxy; netproxy.Auto takes all.
	Protocol netproxy.Protocol
	// User and Password, when User is set, are asked of a dynamic
	// forward's HTTP and SOCKS5 clients.
	User, Password string
}

// State is a forward's state.
type State int

const (
	Stopped State = iota
	Connecting
	Running
	Reconnecting
	Error
)

func (s State) String() string {
	return [...]string{"stopped", "connecting", "running", "reconnecting", "error"}[s]
}

// Status is a forward's state for the UI: why it is in Error, and how
// many connections it relays.
type Status struct {
	State State
	Err   error
	Conns int
	// Session is the underlying session's status (the hop being
	// dialed, the reconnect attempt).
	Session session.Status
}

// ErrPortInUse is a local port another program listens on.
var ErrPortInUse = errors.New("forward: port already in use")

// RemoteRefusedError is a server that would not listen for a remote
// forward.
type RemoteRefusedError struct {
	Addr string
	Err  error
}

func (e *RemoteRefusedError) Error() string {
	return fmt.Sprintf("forward: server refused to listen on %s: %v", e.Addr, e.Err)
}
func (e *RemoteRefusedError) Unwrap() error { return e.Err }

// Forward is one running rule. It is safe for concurrent use.
type Forward struct {
	rule Rule
	sess *session.Session
	// Conns are the connections relayed now.
	Conns    *netproxy.Registry
	onChange atomic.Pointer[func()]

	mu           sync.Mutex
	started      bool
	stopped      bool
	ln           net.Listener // local listener (Local, Dynamic)
	remote       net.Listener // server listener (Remote), on client
	client       *ssh.Client  // the connection the relays (and remote) are on
	err          error        // the forward's own failure (Remote refused)
	proxyLimitAt time.Time    // last resource-limit log; protected by mu
	// life ends when the forward stops: every dial for it gives up then.
	life     context.Context
	end      context.CancelFunc
	listenOn func(network, addr string) (net.Listener, error)
	dialIn   dialFunc // from the server (Local, Dynamic)
	dialOut  dialFunc // from this computer (Remote)
}

type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// New returns a forward of r that connects as cfg says; Start runs it.
// opts.NoShell is set, and reconnecting is endless.
func New(r Rule, cfg session.Config, opts session.Options) *Forward {
	opts.NoShell = true
	opts.AutoReconnect = true
	if opts.MaxAttempts == 0 {
		opts.MaxAttempts = 1 << 30
	}
	f := &Forward{rule: r, Conns: &netproxy.Registry{}, listenOn: net.Listen}
	var d net.Dialer
	f.dialOut = d.DialContext
	f.dialIn = f.dialRemote
	f.Conns.OnChange = f.changed
	f.sess = session.New(cfg, opts, 1, 1)
	f.sess.OnChange = func() {
		f.sync()
		f.changed()
	}
	return f
}

// Rule is the forward's rule.
func (f *Forward) Rule() Rule { return f.rule }

// Session is the session the forward rides, for prompts and Reconnect.
func (f *Forward) Session() *session.Session { return f.sess }

// SetOnChange sets fn to be called, from any goroutine, when the status
// or the connections change.
func (f *Forward) SetOnChange(fn func()) { f.onChange.Store(&fn) }

func (f *Forward) changed() {
	if fn := f.onChange.Load(); fn != nil && *fn != nil {
		(*fn)()
	}
}

// Start listens (Local, Dynamic) and connects. A local port in use is
// returned at once, as ErrPortInUse, and the forward is in Error.
func (f *Forward) Start() error {
	f.mu.Lock()
	if f.started || f.stopped {
		f.mu.Unlock()
		return nil
	}
	f.started = true
	f.life, f.end = context.WithCancel(context.Background())
	life := f.life
	f.mu.Unlock()
	if f.rule.Kind != Remote {
		ln, err := f.listenOn("tcp", f.rule.Listen)
		if err != nil {
			if errors.Is(err, errAddrInUse) {
				err = fmt.Errorf("%w: %s", ErrPortInUse, f.rule.Listen)
			}
			f.mu.Lock()
			f.err = err
			f.mu.Unlock()
			f.changed()
			return err
		}
		f.mu.Lock()
		if f.stopped {
			f.mu.Unlock()
			ln.Close()
			return nil
		}
		f.ln = ln
		f.mu.Unlock()
		if f.rule.Kind == Dynamic {
			dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialFor(life, ctx, f.dialIn, network, addr)
			}
			srv := &netproxy.Server{Protocol: f.rule.Protocol, User: f.rule.User, Password: f.rule.Password, Dial: dial, Conns: f.Conns, OnError: f.proxyError}
			go srv.Serve(ln)
		} else {
			go f.acceptLocal(life, ln)
		}
	}
	f.sess.Start()
	return nil
}

// proxyError reports resource rejection without logging peer-supplied data.
// Coalesce flood traffic to one log per forward per minute.
func (f *Forward) proxyError(err error) {
	var limit error
	switch {
	case errors.Is(err, netproxy.ErrClientLimit):
		limit = netproxy.ErrClientLimit
	case errors.Is(err, netproxy.ErrHandshakeTooLarge):
		limit = netproxy.ErrHandshakeTooLarge
	default:
		return
	}
	now := time.Now()
	f.mu.Lock()
	if !f.proxyLimitAt.IsZero() && now.Sub(f.proxyLimitAt) < time.Minute {
		f.mu.Unlock()
		return
	}
	f.proxyLimitAt = now
	f.mu.Unlock()
	log.Printf("forward: dynamic proxy rejected client: %v", limit)
}

// Stop ends the forward and every connection it relays. It does not
// wait on the network: the local port is free when it returns. Dials
// still pending give up, and a connection that arrives anyway is closed,
// never relayed. The pooled SSH connection stays for whoever else shares
// it.
func (f *Forward) Stop() {
	f.mu.Lock()
	if f.stopped {
		f.mu.Unlock()
		return
	}
	f.stopped = true
	ln, remote, end := f.ln, f.remote, f.end
	f.ln, f.remote, f.client = nil, nil, nil
	f.mu.Unlock()
	if end != nil {
		end()
	}
	if ln != nil {
		ln.Close()
	}
	f.Conns.Shut()
	// The local port is free now; the server's listener and the session
	// end in the background, as they wait on the network.
	go func() {
		if remote != nil {
			remote.Close()
		}
		f.sess.Close()
	}()
	f.changed()
}

// Status is the forward's status.
func (f *Forward) Status() Status {
	st := Status{Conns: f.Conns.Len(), Session: f.sess.Status()}
	f.mu.Lock()
	ownErr, stopped, started, remoteUp := f.err, f.stopped, f.started, f.remote != nil
	f.mu.Unlock()
	switch {
	case stopped || !started:
		st.State = Stopped
	case ownErr != nil:
		st.State, st.Err = Error, ownErr
	default:
		switch st.Session.State {
		case session.Idle, session.Connecting:
			st.State = Connecting
		case session.Connected:
			st.State = Running
			if f.rule.Kind == Remote && !remoteUp {
				st.State = Connecting
			}
		case session.Reconnecting:
			st.State, st.Err = Reconnecting, st.Session.Err
		case session.Closed:
			st.State = Stopped
		default:
			st.State, st.Err = Error, st.Session.Err
		}
	}
	return st
}

// dialRemote opens a connection from the server, on the connection now.
func (f *Forward) dialRemote(ctx context.Context, network, addr string) (net.Conn, error) {
	c := f.sess.Client()
	if c == nil {
		return nil, errors.New("forward: not connected")
	}
	return c.DialContext(ctx, network, addr)
}

// dialFor dials with fn until ctx ends or the forward's life does. A
// connection made as the forward stopped is closed rather than returned.
func dialFor(life, ctx context.Context, fn dialFunc, network, addr string) (net.Conn, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(life, cancel)()
	c, err := fn(ctx, network, addr)
	if err == nil && life.Err() != nil {
		c.Close()
		return nil, life.Err()
	}
	return c, err
}

func (f *Forward) acceptLocal(life context.Context, ln net.Listener) {
	for {
		src, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			dst, err := dialFor(life, life, f.dialIn, "tcp", f.rule.Target)
			if err != nil {
				src.Close()
				return
			}
			f.Conns.Relay(src, dst, f.rule.Target, "")
		}()
	}
}

// sync follows the session. When the connection changes, the relays on
// the old one end at once: one blocked writing to a slow client would not
// notice the server is gone until its buffer drained. A remote forward
// then asks the new connection for its listener.
func (f *Forward) sync() {
	c := f.sess.Client()
	f.mu.Lock()
	if f.stopped || c == f.client {
		f.mu.Unlock()
		return
	}
	prev, old := f.client, f.remote
	f.client, f.remote = c, nil
	if f.rule.Kind == Remote {
		f.err = nil
	}
	f.mu.Unlock()
	if prev != nil {
		f.Conns.CloseAll()
	}
	if old != nil {
		old.Close()
	}
	if c == nil || f.rule.Kind != Remote {
		return
	}
	go func() {
		ln, err := c.Listen("tcp", f.rule.Listen)
		f.mu.Lock()
		life := f.life
		if f.stopped || f.client != c {
			f.mu.Unlock()
			if ln != nil {
				ln.Close()
			}
			return
		}
		if err != nil {
			f.err = &RemoteRefusedError{f.rule.Listen, err}
		} else {
			f.remote = ln
		}
		f.mu.Unlock()
		f.changed()
		if err == nil {
			f.acceptRemote(life, ln)
		}
	}()
}

func (f *Forward) acceptRemote(life context.Context, ln net.Listener) {
	for {
		src, err := ln.Accept()
		if err != nil {
			f.mu.Lock()
			if f.remote == ln {
				f.remote = nil
			}
			f.mu.Unlock()
			f.changed()
			return
		}
		go func() {
			dst, err := dialFor(life, life, f.dialOut, "tcp", f.rule.Target)
			if err != nil {
				src.Close()
				return
			}
			f.Conns.Relay(src, dst, f.rule.Target, "")
		}()
	}
}
