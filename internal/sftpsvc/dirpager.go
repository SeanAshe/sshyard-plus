package sftpsvc

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

// pkg/sftp reads a whole folder before it returns any of it, so a folder
// of a million names holds them all in memory at once. A download plan
// lists folders with a dirPager instead: its own small SFTP session on
// the same connection (a second channel, not a second login), which
// reads a folder one READDIR reply (about a hundred names) at a time and
// can stop after any of them.

// SFTP v3 packet types and status codes the pager uses.
const (
	fxpInit      = 1
	fxpVersion   = 2
	fxpClose     = 4
	fxpOpendir   = 11
	fxpReaddir   = 12
	fxpStatus    = 101
	fxpHandle    = 102
	fxpName      = 104
	fxEOF        = 1
	fxNoSuch     = 2
	fxPermDenied = 3

	attrSize     = 0x1
	attrUIDGID   = 0x2
	attrPerms    = 0x4
	attrTimes    = 0x8
	attrExtended = 0x80000000
)

// pagerMaxPacket is the largest reply the pager takes (OpenSSH sends at
// most 256 KB); a larger length is a broken or hostile server.
const pagerMaxPacket = 1 << 20

// errNoPager: the server or connection gives no second SFTP session.
var errNoPager = errors.New("sftp: no session for paged listing")

// errPagerBroken: the pager's session broke off or a reply made no
// sense (a protocol error), not that the server refused the listing.
// The session is of no more use; the folder can still be read whole.
var errPagerBroken = errors.New("sftp: paged listing broke")

// broken marks err as the pager's (errPagerBroken).
func broken(err error) error { return fmt.Errorf("%w: %w", errPagerBroken, err) }

// dirPager lists remote folders a batch at a time.
type dirPager struct {
	s    *ssh.Session
	w    io.WriteCloser
	r    *bufio.Reader
	id   uint32
	stop func() bool
}

// pagerReplyTimeout is how long the pager waits for any one reply: a
// server silent that long has stopped answering, and the pager's
// session is closed (a protocol error: the folder is read whole).
var pagerReplyTimeout = 30 * time.Second

// errPagerTimeout: no reply came within pagerReplyTimeout.
var errPagerTimeout = errors.New("sftp: the server stopped answering the paged listing")

// exchange sends a request and reads its reply, closing the session
// should the reply not come within pagerReplyTimeout.
func (p *dirPager) exchange(typ byte, payload []byte) (byte, []byte, error) {
	var late atomic.Bool
	t := time.AfterFunc(pagerReplyTimeout, func() { late.Store(true); p.s.Close() })
	defer t.Stop()
	err := p.send(typ, payload)
	var rt byte
	var b []byte
	if err == nil {
		rt, b, err = p.recv()
	}
	if err != nil && late.Load() {
		err = errPagerTimeout
	}
	return rt, b, err
}

// openPager starts a pager on c's connection; it ends with ctx.
func (c *Client) openPager(ctx context.Context) (*dirPager, error) {
	if c.conn == nil || noPager || c.scp || c.sudo != nil {
		return nil, errNoPager
	}
	s, err := c.conn.NewSession()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNoPager, err)
	}
	w, err := s.StdinPipe()
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("%w: %v", errNoPager, err)
	}
	r, err := s.StdoutPipe()
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("%w: %v", errNoPager, err)
	}
	if err := s.RequestSubsystem("sftp"); err != nil {
		s.Close()
		return nil, fmt.Errorf("%w: %v", errNoPager, err)
	}
	p := &dirPager{s: s, w: w, r: bufio.NewReaderSize(r, 64<<10)}
	// A read blocked on a server that stopped answering ends with ctx.
	p.stop = context.AfterFunc(ctx, func() { s.Close() })
	typ, _, err := p.exchange(fxpInit, binary.BigEndian.AppendUint32(nil, 3))
	if err != nil || typ != fxpVersion {
		p.Close()
		return nil, fmt.Errorf("%w: no version (%v)", errNoPager, err)
	}
	return p, nil
}

// Close ends the pager's session.
func (p *dirPager) Close() error {
	p.stop()
	return p.s.Close()
}

func (p *dirPager) send(typ byte, payload []byte) error {
	b := make([]byte, 5, 5+len(payload))
	binary.BigEndian.PutUint32(b, uint32(1+len(payload)))
	b[4] = typ
	_, err := p.w.Write(append(b, payload...))
	return err
}

func (p *dirPager) recv() (byte, []byte, error) {
	var h [5]byte
	if _, err := io.ReadFull(p.r, h[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(h[:4])
	if n < 1 || n > pagerMaxPacket {
		return 0, nil, fmt.Errorf("sftp: a reply of %d bytes", n)
	}
	b := make([]byte, n-1)
	if _, err := io.ReadFull(p.r, b); err != nil {
		return 0, nil, err
	}
	if testPagerRecv != nil {
		return testPagerRecv(h[4], b)
	}
	return h[4], b, nil
}

// call sends a request with a new id before payload and returns the
// reply's type and what follows its id.
func (p *dirPager) call(typ byte, payload []byte) (byte, []byte, error) {
	p.id++
	id := p.id
	rt, b, err := p.exchange(typ, append(binary.BigEndian.AppendUint32(nil, id), payload...))
	if err != nil {
		return 0, nil, err
	}
	got, b, ok := takeUint32(b)
	if !ok || got != id {
		return 0, nil, errors.New("sftp: a reply out of order")
	}
	return rt, b, nil
}

// statusErr is the error a STATUS reply says (nil for OK), io.EOF for
// the end of a listing.
func statusErr(p string, b []byte) error {
	code, b, ok := takeUint32(b)
	if !ok {
		return errors.New("sftp: a short status")
	}
	msg, _, _ := takeString(b)
	switch code {
	case 0:
		return nil
	case fxEOF:
		return io.EOF
	case fxNoSuch:
		return &fs.PathError{Op: "readdir", Path: p, Err: fs.ErrNotExist}
	case fxPermDenied:
		return &fs.PathError{Op: "readdir", Path: p, Err: fs.ErrPermission}
	}
	return &fs.PathError{Op: "readdir", Path: p, Err: fmt.Errorf("sftp status %d: %s", code, msg)}
}

// each lists the folder dir, calling fn with each reply's entries (as
// the server named them, "." and ".." left out) until the end, an error,
// or fn's error, which it returns. An error of the pager's own (a
// protocol error) is an errPagerBroken; the pager is not to be used
// again after one.
func (p *dirPager) each(dir string, fn func([]fs.FileInfo) error) error {
	typ, b, err := p.call(fxpOpendir, appendString(nil, dir))
	if err != nil {
		return broken(err)
	}
	switch typ {
	case fxpHandle:
	case fxpStatus:
		if err := statusErr(dir, b); err != nil && err != io.EOF {
			return err
		}
		return broken(errors.New("sftp: opendir gave no handle"))
	default:
		return broken(fmt.Errorf("sftp: opendir: reply type %d", typ))
	}
	handle, _, ok := takeString(b)
	if !ok {
		return broken(errors.New("sftp: a short handle"))
	}
	for {
		err := p.page(dir, handle, fn)
		if err == errNextPage {
			continue
		}
		if err == io.EOF {
			err = nil
		}
		if !errors.Is(err, errPagerBroken) {
			// Not after a protocol error: no reply that makes sense may
			// come, and the plan's ctx may never end.
			p.call(fxpClose, appendString(nil, handle))
		}
		return err
	}
}

// errNextPage: page read a reply; there are more.
var errNextPage = errors.New("next page")

// page reads one READDIR reply of the folder open at handle and gives
// its entries to fn: errNextPage after them, io.EOF at the end.
func (p *dirPager) page(dir, handle string, fn func([]fs.FileInfo) error) error {
	typ, b, err := p.call(fxpReaddir, appendString(nil, handle))
	if err != nil {
		return broken(err)
	}
	switch typ {
	case fxpStatus:
		err := statusErr(dir, b)
		switch {
		case err == nil:
			return broken(errors.New("sftp: readdir gave no names"))
		case err == io.EOF:
			return io.EOF
		case errors.As(err, new(*fs.PathError)):
			return err
		}
		return broken(err)
	case fxpName:
	default:
		return broken(fmt.Errorf("sftp: readdir: reply type %d", typ))
	}
	ents, err := parseNames(b)
	if err != nil {
		return broken(err)
	}
	if err := fn(ents); err != nil {
		return err
	}
	return errNextPage
}

// parseNames reads a NAME reply after its id.
func parseNames(b []byte) ([]fs.FileInfo, error) {
	count, b, ok := takeUint32(b)
	// Each entry takes at least 12 bytes: a count past that is a lie.
	if !ok || uint64(count)*12 > uint64(len(b)) {
		return nil, errors.New("sftp: a bad name list")
	}
	out := make([]fs.FileInfo, 0, count)
	for range count {
		var name string
		if name, b, ok = takeString(b); !ok {
			return nil, errors.New("sftp: a short name")
		}
		if _, b, ok = takeString(b); !ok { // the long name
			return nil, errors.New("sftp: a short long name")
		}
		var fi pagedInfo
		if fi, b, ok = takeAttrs(b); !ok {
			return nil, errors.New("sftp: short attributes")
		}
		if name == "." || name == ".." {
			continue
		}
		fi.name = name
		out = append(out, fi)
	}
	return out, nil
}

func takeUint32(b []byte) (uint32, []byte, bool) {
	if len(b) < 4 {
		return 0, b, false
	}
	return binary.BigEndian.Uint32(b), b[4:], true
}

func takeString(b []byte) (string, []byte, bool) {
	n, b, ok := takeUint32(b)
	if !ok || uint64(n) > uint64(len(b)) {
		return "", b, false
	}
	return string(b[:n]), b[n:], true
}

func appendString(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint32(b, uint32(len(s)))
	return append(b, s...)
}

// takeAttrs reads SFTP v3 attributes.
func takeAttrs(b []byte) (pagedInfo, []byte, bool) {
	var fi pagedInfo
	flags, b, ok := takeUint32(b)
	if !ok {
		return fi, b, false
	}
	if flags&attrSize != 0 {
		if len(b) < 8 {
			return fi, b, false
		}
		fi.size = int64(binary.BigEndian.Uint64(b))
		b = b[8:]
	}
	if flags&attrUIDGID != 0 {
		if len(b) < 8 {
			return fi, b, false
		}
		b = b[8:]
	}
	if flags&attrPerms != 0 {
		var m uint32
		if m, b, ok = takeUint32(b); !ok {
			return fi, b, false
		}
		fi.mode = posixMode(m)
	}
	if flags&attrTimes != 0 {
		if len(b) < 8 {
			return fi, b, false
		}
		fi.mtime = time.Unix(int64(binary.BigEndian.Uint32(b[4:])), 0)
		b = b[8:]
	}
	if flags&attrExtended != 0 {
		var n uint32
		if n, b, ok = takeUint32(b); !ok {
			return fi, b, false
		}
		for range n {
			if _, b, ok = takeString(b); !ok {
				return fi, b, false
			}
			if _, b, ok = takeString(b); !ok {
				return fi, b, false
			}
		}
	}
	return fi, b, true
}

// posixMode turns POSIX st_mode bits into a FileMode.
func posixMode(m uint32) fs.FileMode {
	mode := fs.FileMode(m & 0o777)
	switch m & 0o170000 {
	case 0o040000:
		mode |= fs.ModeDir
	case 0o120000:
		mode |= fs.ModeSymlink
	case 0o010000:
		mode |= fs.ModeNamedPipe
	case 0o140000:
		mode |= fs.ModeSocket
	case 0o020000:
		mode |= fs.ModeDevice | fs.ModeCharDevice
	case 0o060000:
		mode |= fs.ModeDevice
	}
	if m&0o4000 != 0 {
		mode |= fs.ModeSetuid
	}
	if m&0o2000 != 0 {
		mode |= fs.ModeSetgid
	}
	if m&0o1000 != 0 {
		mode |= fs.ModeSticky
	}
	return mode
}

// pagedInfo is a listed entry: only what a plan needs.
type pagedInfo struct {
	name  string
	size  int64
	mode  fs.FileMode
	mtime time.Time
}

func (i pagedInfo) Name() string       { return i.name }
func (i pagedInfo) Size() int64        { return i.size }
func (i pagedInfo) Mode() fs.FileMode  { return i.mode }
func (i pagedInfo) ModTime() time.Time { return i.mtime }
func (i pagedInfo) IsDir() bool        { return i.mode.IsDir() }
func (i pagedInfo) Sys() any           { return nil }

var _ fs.FileInfo = pagedInfo{}

// readDir reads the folder whole, giving up when ctx is done even if
// the server has stopped answering.
func (c *Client) readDir(ctx context.Context, dir string) ([]fs.FileInfo, error) {
	return withCtx(ctx, func() ([]fs.FileInfo, error) { return c.c.ReadDirContext(ctx, dir) })
}

// withCtx is f's result, or ctx's error as soon as ctx is done: pkg/sftp
// waits on a silent server for good in calls without a context, and in
// ReadDirContext's closing of the folder. f is left to end with the
// connection.
func withCtx[T any](ctx context.Context, f func() (T, error)) (T, error) {
	type res struct {
		v   T
		err error
	}
	ch := make(chan res, 1)
	go func() {
		v, err := f()
		ch <- res{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// noPager, for tests, acts as if the server gave no second session.
var noPager bool

// testPagerRecv, for tests, stands for the server in each reply the
// pager reads: given what came, it says what did.
var testPagerRecv func(typ byte, b []byte) (byte, []byte, error)
