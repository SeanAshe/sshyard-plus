package sftpsvc

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sshtest"
)

// redialer serves a queue's Client: one connection, dialed again once
// it is dropped.
type redialer struct {
	t   *testing.T
	srv *sshtest.Server
	kh  *session.KnownHosts
	mu  sync.Mutex
	c   *Client
	n   atomic.Int32
}

func (r *redialer) get(ctx context.Context) (*Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.c != nil {
		if _, err := r.c.Home(); err == nil {
			return r.c, nil
		}
		r.c.Close()
		r.c = nil
	}
	conn, err := session.Dial(ctx, session.Config{Addr: r.srv.Addr, User: "demo", HostKey: r.kh.Callback(),
		Credentials: func(context.Context) (session.Credentials, error) {
			return session.Credentials{Password: "secret"}, nil
		}})
	if err != nil {
		return nil, err
	}
	r.t.Cleanup(func() { conn.Close() })
	c, err := New(conn)
	if err != nil {
		return nil, err
	}
	r.n.Add(1)
	r.c = c
	return c, nil
}

func queueServer(t *testing.T) (*redialer, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sftp test server does not support Windows paths")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	srv.SFTPRoot = root
	t.Cleanup(srv.Close)
	kh, _ := session.OpenKnownHosts(filepath.Join(t.TempDir(), "kh"))
	kh.Add(srv.Addr, srv.HostKey.PublicKey())
	return &redialer{t: t, srv: srv, kh: kh}, root
}

func newQueue(r *redialer) *Transfers {
	return &Transfers{Client: r.get, Logf: func(string, ...any) {}, Backoff: func(int) time.Duration { return 10 * time.Millisecond }}
}

// tree makes 200 files in 3 levels of folders under dir/name, and an
// empty folder.
func tree(t *testing.T, dir, name string) {
	n := 0
	for a := range 4 {
		for b := range 5 {
			p := filepath.Join(dir, name, fmt.Sprintf("a%d", a), fmt.Sprintf("b%d", b), "c")
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			for f := range 10 {
				body := strings.Repeat(fmt.Sprintf("file %d %d %d\n", a, b, f), n%50+1)
				os.WriteFile(filepath.Join(p, fmt.Sprintf("f%d.log", f)), []byte(body), 0o640)
				n++
			}
		}
	}
	os.MkdirAll(filepath.Join(dir, name, "empty"), 0o755)
}

// snapshot is every path under root with its content's hash.
func snapshot(t *testing.T, root string) map[string]string {
	out := map[string]string{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			out[rel] = "dir"
			return nil
		}
		b, _ := os.ReadFile(p)
		out[rel] = fmt.Sprintf("%x", sha256.Sum256(b))
		return nil
	})
	return out
}

// enqueue makes the plan's folders and queues its files, as the app does.
func enqueue(t *testing.T, c *Client, ts *Transfers, items []Item, dir Direction) {
	for _, it := range items {
		switch {
		case it.IsDir && dir == Upload:
			if err := c.MkdirAll(it.Dst); err != nil {
				t.Fatal(err)
			}
		case it.IsDir:
			os.MkdirAll(it.Dst, 0o755)
		default:
			ts.Add(dir, it.Src, it.Dst, it.Rel)
		}
	}
}

// A folder dropped from Finder comes with a trailing separator; it still
// uploads as itself, not just what is in it.
func TestPlanUploadFolderWithTrailingSeparator(t *testing.T) {
	r, root := queueServer(t)
	local := t.TempDir()
	tree(t, local, "logs")
	ts := newQueue(r)
	c, _ := r.get(context.Background())
	items, err := c.PlanUpload(context.Background(), []string{filepath.Join(local, "logs") + string(filepath.Separator)}, root)
	if err != nil {
		t.Fatal(err)
	}
	if it := items[0]; !it.IsDir || it.Rel != "logs" || it.Dst != root+"/logs" {
		t.Fatalf("first item %+v, want the folder logs", it)
	}
	enqueue(t, c, ts, items, Upload)
	ts.Wait()
	want := snapshot(t, filepath.Join(local, "logs"))
	if got := snapshot(t, filepath.Join(root, "logs")); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("uploaded tree differs: %d vs %d entries", len(got), len(want))
	}
}

func TestQueueUploadsAndDownloadsTrees(t *testing.T) {
	for _, n := range []int{1, 5} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			r, root := queueServer(t)
			local := t.TempDir()
			tree(t, local, "logs")
			ts := newQueue(r)
			ts.SetConcurrency(n)
			var mu sync.Mutex
			most := 0
			ts.OnChange = func() {
				running := 0
				for _, tr := range ts.List() {
					if tr.State == Running {
						running++
					}
				}
				mu.Lock()
				most = max(most, running)
				mu.Unlock()
			}
			ts.onWrite = func(*Transfer, int64) { time.Sleep(time.Millisecond) }
			c, _ := r.get(context.Background())
			items, err := c.PlanUpload(context.Background(), []string{filepath.Join(local, "logs")}, root)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 200+1+4+20+20+1 {
				t.Fatalf("%d items", len(items))
			}
			enqueue(t, c, ts, items, Upload)
			ts.Wait()
			for _, tr := range ts.List() {
				if tr.State != Done {
					t.Fatalf("%s: %v %v", tr.Name, tr.State, tr.Err)
				}
			}
			want := snapshot(t, filepath.Join(local, "logs"))
			if got := snapshot(t, filepath.Join(root, "logs")); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("uploaded tree differs: %d vs %d entries", len(got), len(want))
			}
			mu.Lock()
			if most > n {
				t.Errorf("%d ran at once, limit %d", most, n)
			}
			mu.Unlock()

			// and back down
			back := t.TempDir()
			items, _, err = c.PlanDownload(context.Background(), []string{root + "/logs"}, back)
			if err != nil {
				t.Fatal(err)
			}
			enqueue(t, c, ts, items, Download)
			ts.Wait()
			if got := snapshot(t, filepath.Join(back, "logs")); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("downloaded tree differs: %d vs %d entries", len(got), len(want))
			}
			// planned again, everything is there already
			items, _ = c.PlanUpload(context.Background(), []string{filepath.Join(local, "logs")}, root)
			for _, it := range items {
				if !it.IsDir && !it.Exists {
					t.Fatalf("%s not seen as existing", it.Rel)
				}
			}
		})
	}
}

// An uploaded file keeps its mode and modification time, not the part
// file's 0600 and the time it was written.
func TestQueueUploadKeepsModeAndTime(t *testing.T) {
	r, root := queueServer(t)
	local := t.TempDir()
	mod := time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)
	for name, mode := range map[string]fs.FileMode{"index.html": 0o644, "run.sh": 0o755} {
		p := filepath.Join(local, name)
		os.WriteFile(p, []byte(name), mode)
		os.Chmod(p, mode)
		os.Chtimes(p, mod, mod)
	}
	ts := newQueue(r)
	ts.Add(Upload, filepath.Join(local, "index.html"), root+"/index.html", "index.html")
	ts.Add(Upload, filepath.Join(local, "run.sh"), root+"/run.sh", "run.sh")
	ts.Wait()
	for name, want := range map[string]fs.FileMode{"index.html": 0o644, "run.sh": 0o755} {
		fi, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s: mode %v, want %v", name, fi.Mode().Perm(), want)
		}
		if !fi.ModTime().Equal(mod) {
			t.Errorf("%s: time %v, want %v", name, fi.ModTime(), mod)
		}
	}
}

func bigFile(t *testing.T, p string, n int) []byte {
	data := make([]byte, n)
	rand.Read(data)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return data
}

func waitState(t *testing.T, ts *Transfers, id int, st TransferState) Transfer {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, tr := range ts.List() {
			if tr.ID == id && tr.State == st {
				return tr
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("transfer %d never %v: %+v", id, st, ts.List())
	return Transfer{}
}

func TestQueuePauseResumesAtOffset(t *testing.T) {
	r, root := queueServer(t)
	data := bigFile(t, filepath.Join(root, "big.bin"), 8<<20)
	local := t.TempDir()
	ts := newQueue(r)
	var logs []string
	var mu sync.Mutex
	ts.Logf = func(f string, a ...any) { mu.Lock(); logs = append(logs, fmt.Sprintf(f, a...)); mu.Unlock() }
	var once sync.Once
	var id int
	ts.onWrite = func(tr *Transfer, done int64) {
		if done >= int64(len(data))*4/10 {
			once.Do(func() { go ts.Pause(id) })
			time.Sleep(time.Millisecond)
		}
	}
	dst := filepath.Join(local, "big.bin")
	id = ts.Add(Download, root+"/big.bin", dst, "big.bin")
	waitState(t, ts, id, Paused)
	ts.Wait() // stopped: what it copied is counted
	tr := ts.List()[0]
	if tr.State != Paused {
		t.Fatalf("settled %v", tr.State)
	}
	if tr.Bytes < int64(len(data))*4/10 || tr.Bytes >= int64(len(data)) {
		t.Fatalf("paused at %d", tr.Bytes)
	}
	if LocalExists(dst) {
		t.Fatal("a paused download is at its destination")
	}
	if fi, err := os.Stat(tr.part); err != nil || fi.Size() == 0 {
		t.Fatalf("part file: %v", err)
	}
	ts.onWrite = nil
	ts.Resume(id)
	ts.Wait()
	tr = ts.List()[0]
	if tr.State != Done || tr.Offset <= 0 {
		t.Fatalf("resumed: %+v", tr)
	}
	got, _ := os.ReadFile(dst)
	if sha256.Sum256(got) != sha256.Sum256(data) {
		t.Fatal("sha256 differs after resume")
	}
	if LocalExists(tr.part) {
		t.Error("part file left")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logs) != 1 || !strings.Contains(logs[0], "at offset ") || strings.Contains(logs[0], "at offset 0 ") {
		t.Errorf("logs %q", logs)
	}
}

func TestQueueCancelLeavesNothing(t *testing.T) {
	r, root := queueServer(t)
	local := t.TempDir()
	bigFile(t, filepath.Join(local, "up.bin"), 4<<20)
	bigFile(t, filepath.Join(root, "down.bin"), 4<<20)
	ts := newQueue(r)
	ts.onWrite = func(*Transfer, int64) { time.Sleep(time.Millisecond) }
	up := ts.Add(Upload, filepath.Join(local, "up.bin"), root+"/sub/up.bin", "up.bin")
	down := ts.Add(Download, root+"/down.bin", filepath.Join(local, "down.bin"), "down.bin")
	paused := ts.Add(Download, root+"/down.bin", filepath.Join(local, "p.bin"), "p.bin")
	ts.Pause(paused)
	for _, tr := range ts.List() {
		if tr.State != Running && tr.ID != paused {
			t.Fatalf("%+v", tr)
		}
	}
	time.Sleep(50 * time.Millisecond)
	ts.CancelAll()
	ts.Wait()
	time.Sleep(100 * time.Millisecond) // the upload's part goes after its goroutine ends
	for _, p := range []string{filepath.Join(root, "sub", "up.bin"), ts.List()[0].part, filepath.Join(local, "down.bin"), ts.List()[1].part} {
		if LocalExists(p) {
			t.Errorf("%s left", p)
		}
	}
	for _, tr := range ts.List() {
		if tr.State != Canceled {
			t.Errorf("%s %v", tr.Name, tr.State)
		}
	}
	// A canceled transfer can be retried, from the start.
	ts.onWrite = nil
	ts.Retry(up)
	ts.Retry(down)
	ts.Wait()
	if l := ts.List(); l[0].State != Done || l[1].State != Done || l[0].Offset != 0 {
		t.Fatalf("%+v", l)
	}
}

func TestQueueRetriesAfterDrop(t *testing.T) {
	r, root := queueServer(t)
	local := t.TempDir()
	data := bigFile(t, filepath.Join(local, "up.bin"), 6<<20)
	ts := newQueue(r)
	var once sync.Once
	ts.onWrite = func(tr *Transfer, done int64) {
		if done > 2<<20 {
			once.Do(r.srv.Drop)
		}
	}
	id := ts.Add(Upload, filepath.Join(local, "up.bin"), root+"/up.bin", "up.bin")
	ts.Wait()
	tr := ts.List()[0]
	if tr.ID != id || tr.State != Done || tr.Attempt < 1 || tr.Offset <= 0 {
		t.Fatalf("%+v", tr)
	}
	got, _ := os.ReadFile(filepath.Join(root, "up.bin"))
	if !bytes.Equal(got, data) {
		t.Fatal("content differs after the retry")
	}
	if r.n.Load() < 2 {
		t.Error("never dialed again")
	}
}

func TestQueueGivesUpAfterRetries(t *testing.T) {
	r, root := queueServer(t)
	ts := newQueue(r)
	ts.Client = func(ctx context.Context) (*Client, error) { return nil, errors.New("down") }
	id := ts.Add(Download, root+"/x", filepath.Join(t.TempDir(), "x"), "x")
	ts.Wait()
	tr := ts.List()[0]
	if tr.State != Failed || tr.Attempt != MaxRetries {
		t.Fatalf("%+v", tr)
	}
	// missing files fail at once
	ts.Client = r.get
	ts.Retry(id)
	ts.Wait()
	if tr := ts.List()[0]; tr.State != Failed || tr.Attempt != 0 || !errors.Is(tr.Err, fs.ErrNotExist) {
		t.Fatalf("%+v", tr)
	}
	ts.ClearFinished()
	if len(ts.List()) != 0 {
		t.Error("clear")
	}
}

func TestQueueSourceChangedStartsOver(t *testing.T) {
	r, root := queueServer(t)
	src := filepath.Join(root, "f.bin")
	bigFile(t, src, 2<<20)
	local := t.TempDir()
	ts := newQueue(r)
	var once sync.Once
	var id int
	ts.onWrite = func(tr *Transfer, done int64) {
		if done > 1<<20 {
			once.Do(func() { go ts.Pause(id) })
			time.Sleep(time.Millisecond)
		}
	}
	dst := filepath.Join(local, "f.bin")
	id = ts.Add(Download, src, dst, "f.bin")
	waitState(t, ts, id, Paused)
	ts.Wait()
	data := bigFile(t, src, 3<<20) // the source changed
	ts.onWrite = nil
	ts.Resume(id)
	ts.Wait()
	tr := ts.List()[0]
	got, _ := os.ReadFile(dst)
	if tr.State != Done || tr.Offset != 0 || !bytes.Equal(got, data) {
		t.Fatalf("%+v", tr)
	}
}

func TestOps(t *testing.T) {
	c, root := connect(t)
	p, err := c.CreateFile(root, "a.conf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateFile(root, "a.conf"); err == nil {
		t.Error("created twice")
	}
	v, err := c.WriteFile(p, []byte("one\n"), Version{})
	if err != nil {
		t.Fatal(err)
	}
	b, v2, err := c.ReadFile(p)
	if err != nil || string(b) != "one\n" || v2 != v {
		t.Fatalf("read %q %v %v/%v", b, err, v, v2)
	}
	// someone else writes it
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(filepath.Join(root, "a.conf"), []byte("theirs\n"), 0o644)
	os.Chtimes(filepath.Join(root, "a.conf"), time.Now().Add(time.Minute), time.Now().Add(time.Minute))
	if _, err := c.WriteFile(p, []byte("mine\n"), v); !errors.Is(err, ErrChanged) {
		t.Fatalf("conflict not seen: %v", err)
	}
	if _, err := c.WriteFile(p, []byte("mine\n"), Version{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a.conf")); string(b) != "mine\n" {
		t.Fatalf("%q", b)
	}
	if err := c.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(root, "a.conf")); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	q, err := c.Rename(p, "b.conf")
	if err != nil || q != root+"/b.conf" || c.Exists(p) {
		t.Fatalf("rename %q %v", q, err)
	}
	c.CreateFile(root, "c.conf")
	if _, err := c.Rename(q, "c.conf"); !errors.Is(err, fs.ErrExist) {
		t.Errorf("renamed over a file: %v", err)
	}
	os.MkdirAll(filepath.Join(root, "d", "e"), 0o755)
	os.WriteFile(filepath.Join(root, "d", "e", "f"), []byte("x"), 0o644)
	if err := c.Remove(root + "/d"); err != nil || LocalExists(filepath.Join(root, "d")) {
		t.Fatalf("remove dir: %v", err)
	}
	if err := c.Remove(q); err != nil || c.Exists(q) {
		t.Fatalf("remove file: %v", err)
	}

	local := t.TempDir()
	lp, err := CreateLocalFile(local, "x.txt")
	if err != nil {
		t.Fatal(err)
	}
	if lq, err := RenameLocal(lp, "y.txt"); err != nil || LocalExists(lp) || !LocalExists(lq) {
		t.Fatalf("local rename %v", err)
	}
	os.MkdirAll(filepath.Join(local, "z", "w"), 0o755)
	if err := RemoveLocal(filepath.Join(local, "z")); err != nil || LocalExists(filepath.Join(local, "z")) {
		t.Fatal(err)
	}
}

func TestUniqueNameModeAndText(t *testing.T) {
	taken := map[string]bool{"/r/a.txt": true, "/r/a (2).txt": true, "/r/Makefile": true}
	has := func(p string) bool { return taken[p] }
	if got := UniqueName("/r/a.txt", true, has); got != "/r/a (3).txt" {
		t.Error(got)
	}
	if got := UniqueName("/r/Makefile", true, has); got != "/r/Makefile (2)" {
		t.Error(got)
	}
	if got := UniqueName("/r/new.txt", true, has); got != "/r/new.txt" {
		t.Error(got)
	}
	if got := UniqueName("/r/.bashrc", true, func(p string) bool { return p == "/r/.bashrc" }); got != "/r/.bashrc (2)" {
		t.Error(got)
	}
	once := func(p string) func(string) bool { return func(q string) bool { return q == p } }
	for in, want := range map[string]string{
		"/r/report.tar.gz": "/r/report (2).tar.gz",
		"/r/b.TAR.XZ":      "/r/b (2).TAR.XZ",
		"/r/x.gz":          "/r/x (2).gz",
		"/r/.tar.gz":       "/r/.tar (2).gz",
		"/r/v1.2.tar":      "/r/v1.2 (2).tar",
		"/r/notes.star.gz": "/r/notes.star (2).gz",
	} {
		if got := UniqueName(in, true, once(in)); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
	if got := UniqueName(filepath.Join("l", "r.tar.gz"), false, once(filepath.Join("l", "r.tar.gz"))); got != filepath.Join("l", "r (2).tar.gz") {
		t.Error(got)
	}
	for in, want := range map[string]fs.FileMode{"644": 0o644, "0755": 0o755, " 600 ": 0o600} {
		if m, err := ParseMode(in); err != nil || m != want {
			t.Errorf("%q: %v %v", in, m, err)
		}
	}
	for _, bad := range []string{"", "8", "1777", "rwx", "-1"} {
		if _, err := ParseMode(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	if !IsText([]byte("server {\n  listen 80;\n}\n")) || !IsText([]byte("中文")) || IsText([]byte{0x7f, 'E', 'L', 'F', 0, 1}) || IsText([]byte{0xff, 0xfe, 0x41}) {
		t.Error("IsText")
	}
}
