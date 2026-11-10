package sftpsvc

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"golang.org/x/crypto/ssh"

	"github.com/Licoy/sshyard/internal/scpfs"
	"github.com/pkg/sftp"
)

var ErrOperationsBusy = errors.New("sftp: too many pending operations")

var operationPools = struct {
	sync.Mutex
	slots map[*ssh.Client]*operationPool
}{slots: make(map[*ssh.Client]*operationPool)}

func operationSlots(conn *ssh.Client) *operationPool {
	operationPools.Lock()
	defer operationPools.Unlock()
	if slots := operationPools.slots[conn]; slots != nil {
		return slots
	}
	slots := &operationPool{changed: make(chan struct{})}
	operationPools.slots[conn] = slots
	go func() {
		conn.Wait()
		operationPools.Lock()
		delete(operationPools.slots, conn)
		operationPools.Unlock()
	}()
	return slots
}

// operationClient bounds pending SSH channel opens, which SSH cannot cancel.
// Once a channel exists, cancellation closes it before subsystem negotiation.
func (c *Client) operationClient(ctx context.Context) (*Client, error) {
	reservation, err := c.operations.reserve(ctx)
	if err != nil {
		return nil, err
	}
	type result struct {
		c   *Client
		err error
	}
	ready := make(chan result)
	go func() {
		sub, err := c.negotiateOperation(ctx)
		// Negotiation no longer occupies a pending-open slot once it finishes.
		// Release before handing the client to a caller that may open again.
		// A cancelled NewSession keeps its slot until negotiation really ends.
		reservation.release()
		select {
		case ready <- result{sub, err}:
		case <-ctx.Done():
			if sub != nil {
				sub.Close()
			}
		}
	}()
	select {
	case got := <-ready:
		if err := ctx.Err(); err != nil {
			if got.c != nil {
				got.c.Close()
			}
			return nil, err
		}
		return got.c, got.err
	case <-ctx.Done():
		reservation.abandon()
		return nil, ctx.Err()
	}
}

func (c *Client) negotiateOperation(ctx context.Context) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.sudo != nil {
		return c.sudoOperation(ctx)
	}
	if withoutSFTP(c.conn) {
		return c.scpOperation()
	}
	session, err := c.conn.NewSession()
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { session.Close() })
	defer stop()
	complete := false
	defer func() {
		if !complete {
			session.Close()
		}
	}()
	rd, err := session.StdoutPipe()
	if err != nil {
		return nil, err
	}
	wr, err := session.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := session.RequestSubsystem("sftp"); err != nil {
		if err.Error() != errSubsystem {
			return nil, err
		}
		// The server has no SFTP: its shell and scp stand in.
		session.Close()
		markWithoutSFTP(c.conn)
		return c.scpOperation()
	}
	sc, err := sftp.NewClientPipe(rd, wr)
	if err != nil {
		return nil, err
	}
	complete = true
	return &Client{c: sc, conn: c.conn, operation: session, operations: c.operations}, nil
}

// errSubsystem is what x/crypto/ssh says when the server refuses a
// subsystem.
const errSubsystem = "ssh: subsystem request failed"

// noSFTP are the connections whose server refused the sftp subsystem.
var noSFTP = struct {
	sync.Mutex
	m map[*ssh.Client]bool
}{m: map[*ssh.Client]bool{}}

func withoutSFTP(conn *ssh.Client) bool {
	noSFTP.Lock()
	defer noSFTP.Unlock()
	return noSFTP.m[conn]
}

func markWithoutSFTP(conn *ssh.Client) {
	noSFTP.Lock()
	defer noSFTP.Unlock()
	if noSFTP.m[conn] {
		return
	}
	noSFTP.m[conn] = true
	go func() {
		conn.Wait()
		noSFTP.Lock()
		delete(noSFTP.m, conn)
		noSFTP.Unlock()
	}()
}

// scpOperation is an SFTP client of a server without SFTP (scpfs).
func (c *Client) scpOperation() (*Client, error) {
	sc, err := scpfs.NewClient(c.conn)
	if err != nil {
		return nil, fmt.Errorf("sftp: no SFTP, and %w", err)
	}
	return &Client{c: sc, conn: c.conn, operations: c.operations, scp: true}, nil
}
