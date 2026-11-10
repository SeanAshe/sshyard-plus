package sftpsvc

import (
	"context"
	"errors"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/sshtest"
)

func TestDownloadDoesNotFollowPlantedPartOrAncestor(t *testing.T) {
	r, root := queueServer(t)
	data := bigFile(t, filepath.Join(root, "source"), 128)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "target")
	if err := os.Symlink(outside, PartName(dst, false)); err != nil {
		t.Fatal(err)
	}
	ts := newQueue(r)
	ts.Add(Download, filepath.Join(root, "source"), dst, "target")
	ts.Wait()
	if tr := ts.List()[0]; tr.State != Done {
		t.Fatalf("%v", tr.Err)
	}
	downloaded(t, dst, data)
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "original" {
		t.Fatalf("outside changed: %q %v", got, err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Dir(outside), link); err != nil {
		t.Fatal(err)
	}
	ts = newQueue(r)
	ts.Add(Download, filepath.Join(root, "source"), filepath.Join(link, "outside.txt"), "target")
	ts.Wait()
	if tr := ts.List()[0]; tr.State != Failed || !errors.Is(tr.Err, ErrUnsafePath) || tr.Attempt != 0 {
		t.Fatalf("linked ancestor accepted or retried: %+v", tr)
	}
	got, _ = os.ReadFile(outside)
	if string(got) != "original" {
		t.Fatalf("outside changed: %q", got)
	}
}

func TestResumeRefusesReplacedPartialIdentity(t *testing.T) {
	ts, id, _, _, release := heldDownload(t)
	ts.Pause(id)
	release()
	ts.Wait()
	tr := ts.List()[0]
	if err := os.Remove(tr.part); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, make([]byte, tr.Bytes), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, tr.part); err != nil {
		t.Fatal(err)
	}
	ts.Resume(id)
	ts.Wait()
	if got := ts.List()[0]; got.State != Failed || !errors.Is(got.Err, ErrPartChanged) || got.Attempt != 0 {
		t.Fatalf("replaced part accepted or retried: %+v", got)
	}
	info, err := os.Stat(outside)
	if err != nil || info.Size() != tr.Bytes {
		t.Fatalf("outside modified: %v %v", info, err)
	}
}

func TestReadBoundAndCancelledSubsystemKeepSharedSSH(t *testing.T) {
	r, _ := queueServer(t)
	fake := &sshtest.FakeFS{Files: map[string][]byte{"/file": []byte("hello")}}
	r.srv.SFTPHandlers = fake.Handlers
	r.srv.SFTPConn = fake.Conn
	c, err := r.get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	p := "/file"
	if _, _, err := c.ReadFileContext(t.Context(), p, 4); !errors.Is(err, ErrReadLimit) {
		t.Fatalf("limit: %v", err)
	}
	if data, _, err := c.ReadFileContext(t.Context(), p, 5); err != nil || string(data) != "hello" {
		t.Fatalf("exact: %q %v", data, err)
	}
	fake.Stop()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err = c.ReadFileContext(ctx, p, 5)
	fake.Resume()
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("cancel: %v elapsed %s", err, time.Since(start))
	}
	if _, err := c.Home(); err != nil {
		t.Fatalf("shared SFTP closed: %v", err)
	}
}

func TestResolvePathRejectsFileAndAncestorLinks(t *testing.T) {
	r, root := queueServer(t)
	c, err := r.get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "report"), []byte("ordinary fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("report", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ResolvePath(t.Context(), filepath.Join(root, "alias")); err == nil {
		t.Fatal("file link allowed")
	}
	if err := os.Symlink(root, filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ResolvePath(t.Context(), filepath.Join(root, "dir", "report")); err == nil {
		t.Fatal("ancestor link allowed")
	}
}

type lyingFileList struct{ base sftp.FileLister }

func (l lyingFileList) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	base, err := l.base.Filelist(r)
	if err != nil {
		return nil, err
	}
	return lyingLister{base}, nil
}

type lyingLister struct{ base sftp.ListerAt }

func (l lyingLister) ListAt(dst []os.FileInfo, off int64) (int, error) {
	n, err := l.base.ListAt(dst, off)
	for i := 0; i < n; i++ {
		if !dst[i].IsDir() {
			dst[i] = lyingInfo{dst[i]}
		}
	}
	return n, err
}

type lyingInfo struct{ os.FileInfo }

func (lyingInfo) Size() int64 { return 0 }

func TestReadRejectsLyingSizeBeforeReturningData(t *testing.T) {
	r, _ := queueServer(t)
	fake := &sshtest.FakeFS{Files: map[string][]byte{"/file": []byte("hello")}}
	r.srv.SFTPHandlers = func() sftp.Handlers { h := fake.Handlers(); h.FileList = lyingFileList{h.FileList}; return h }
	c, err := r.get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data, _, err := c.ReadFileContext(t.Context(), "/file", 4)
	if !errors.Is(err, ErrReadLimit) || data != nil {
		t.Fatalf("unbounded lying read: %q %v", data, err)
	}
	data, _, err = c.ReadFileContext(t.Context(), "/file", 8)
	if !errors.Is(err, ErrChanged) || data != nil {
		t.Fatalf("changed size accepted: %q %v", data, err)
	}
}

type refuseRename struct{ base sftp.FileCmder }

func (r refuseRename) Filecmd(req *sftp.Request) error {
	if req.Method == "PosixRename" || req.Method == "Rename" {
		return os.ErrPermission
	}
	return r.base.Filecmd(req)
}

func TestTransferFailedRemoteRenamePreservesOriginal(t *testing.T) {
	r, _ := queueServer(t)
	fake := &sshtest.FakeFS{Files: map[string][]byte{"/original": []byte("original"), "/part": []byte("new")}}
	r.srv.SFTPHandlers = func() sftp.Handlers { h := fake.Handlers(); h.FileCmd = refuseRename{h.FileCmd}; return h }
	c, err := r.get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := (remoteFS{c}).rename("/part", "/original"); err == nil {
		t.Fatal("rename unexpectedly succeeded")
	}
	if got := fake.File("/original"); string(got) != "original" {
		t.Fatalf("original lost: %q", got)
	}
}

func TestQueueSelectedSourceLinkStillDownloadsTarget(t *testing.T) {
	r, root := queueServer(t)
	data := bigFile(t, filepath.Join(root, "source"), 128)
	link := filepath.Join(root, "link")
	if err := os.Symlink("source", link); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "copy")
	ts := newQueue(r)
	ts.Add(Download, link, dst, "copy")
	ts.Wait()
	if got := ts.List()[0]; got.State != Done {
		t.Fatalf("source link failed: %+v", got)
	}
	downloaded(t, dst, data)
}

// blockedChannelConn exercises ssh.Client.NewSession's uncancellable channel
// open without authentication or transport fixtures.
type blockedChannelConn struct {
	ssh.Conn
	opened chan struct{}
	closed chan struct{}
}

func (c *blockedChannelConn) OpenChannel(string, []byte) (ssh.Channel, <-chan *ssh.Request, error) {
	c.opened <- struct{}{}
	<-c.closed
	return nil, nil, os.ErrClosed
}
func (c *blockedChannelConn) Wait() error { <-c.closed; return os.ErrClosed }
func TestPendingChannelQuotaSharedAcrossRecreatedClients(t *testing.T) {
	raw := &blockedChannelConn{opened: make(chan struct{}, 4), closed: make(chan struct{})}
	channels := make(chan ssh.NewChannel)
	close(channels)
	requests := make(chan *ssh.Request)
	close(requests)
	conn := ssh.NewClient(raw, channels, requests)
	defer close(raw.closed)
	for range 4 {
		ctx, cancel := context.WithCancel(t.Context())
		finished := make(chan error, 1)
		go func() { _, err := NewContext(ctx, conn); finished <- err }()
		select {
		case <-raw.opened:
		case <-time.After(time.Second):
			t.Fatal("channel did not open")
		}
		cancel()
		if err := <-finished; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	}
	// A fresh SFTP Client must share the four pending opens, even with a
	// background context, rather than starting another goroutine or waiting.
	started := time.Now()
	if _, err := NewContext(context.Background(), conn); !errors.Is(err, ErrOperationsBusy) {
		t.Fatalf("fresh client bypassed quota: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("busy operation waited")
	}
}

func TestPlanDirectoryCreationConfinedAndNested(t *testing.T) {
	local := t.TempDir()
	nested := filepath.Join(local, "a", "b")
	if err := MkdirAllLocal(nested); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	link := filepath.Join(local, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := MkdirAllLocal(filepath.Join(link, "new")); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("linked plan directory accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside directory created: %v", err)
	}
	r, root := queueServer(t)
	c, err := r.get(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.MkdirAll(filepath.Join(root, "a", "b", "c")); err != nil {
		t.Fatalf("nested remote folder: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := c.MkdirAll(filepath.Join(root, "link", "new")); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("remote linked parent accepted: %v", err)
	}
}
