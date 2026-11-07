package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/sftpsvc"
	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// filesPane opens a files pane on a test server serving a temporary
// folder, returning the folder and the pane.
func filesPane(t *testing.T) (*App, *ui.Tester, *conn, string) {
	t.Helper()
	return filesPaneOn(t, nil)
}

// filesPaneOn is filesPane with setup run on the server first.
func filesPaneOn(t *testing.T, setup func(*sshtest.Server)) (*App, *ui.Tester, *conn, string) {
	t.Helper()
	return filesPaneAs(t, true, setup)
}

// filesPaneAs is filesPaneOn showing the local side beside the server's,
// or the server's files alone as a pane starts.
func filesPaneAs(t *testing.T, dual bool, setup func(*sshtest.Server)) (*App, *ui.Tester, *conn, string) {
	t.Helper()
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv.SFTPRoot = root
	if setup != nil {
		setup(srv)
	}
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "box", "secret")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	a.selected = h.ID
	if dual {
		showLocal(a)
	}
	tt.Frame()
	mustClick(t, tt, "Open Files")
	fc := a.focused()
	waitFor(t, tt, "files connected", func() bool { return fc.fs.remote.client != nil })
	return a, tt, fc, root
}

// showLocal shows this computer's files beside the server's in the files
// panes, as their header's toggle does. The tests of what the local side
// does start with it.
func showLocal(a *App) {
	if !a.settings.FilesDual {
		a.toggleDual()
	}
}

// skipWindowsSFTP skips a test that moves files through the test server's
// remote side: pkg/sftp serving this machine's disk, which on Windows
// mangles absolute paths ("/C:/...") and reports every folder as writable
// by others. Real remotes are POSIX servers; Linux and macOS cover these.
func skipWindowsSFTP(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sftp test server does not support Windows paths")
	}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

func center(t *testing.T, tt *ui.Tester, s string) (float32, float32) {
	t.Helper()
	r, ok := tt.Find(s)
	if !ok {
		t.Fatalf("no %q in %q", s, tt.Texts())
	}
	return r.X + r.W/2, r.Y + r.H/2
}

// nameAt is the middle of the icon of the row of name.
func nameAt(t *testing.T, tt *ui.Tester, name string) (float32, float32) {
	t.Helper()
	r, ok := tt.Find(name)
	if !ok {
		t.Fatalf("no %q in %q", name, tt.Texts())
	}
	return r.X + 18, r.Y + r.H/2
}

func TestFilesContextMenu(t *testing.T) {
	skipWindowsSFTP(t)
	a, tt, fc, root := filesPane(t)
	write(t, filepath.Join(root, "a.txt"), "hello")
	a.refreshSides(fc)
	waitFor(t, tt, "a.txt", func() bool { return tt.HasText("a.txt") })
	remote := sftpsvc.RemoteJoin(fc.fs.remote.dir, "a.txt")

	menu := func() []string {
		t.Helper()
		if err := tt.RightClick("a.txt"); err != nil {
			t.Fatal(err)
		}
		return menuOf(a)
	}
	choose := func(item string) {
		t.Helper()
		menu()
		if err := chooseMenu(a, tt, item); err != nil {
			t.Fatalf("%v: menu %q", err, menuOf(a))
		}
		tt.Frame()
	}
	got := menu()
	for _, want := range []string{"Edit", "Open With Default App", "Download", "Rename", "Permissions…", "Copy Path", "Delete…", "New Folder…", "New File…", "Refresh"} {
		if !slices.Contains(got, want) {
			t.Errorf("menu %q has no %q", got, want)
		}
	}
	chooseMenu(a, tt, "Copy Path")
	tt.Frame()
	if tt.Clipboard() != remote {
		t.Errorf("clipboard %q, want %q", tt.Clipboard(), remote)
	}

	// Permissions. Windows reports a writable file as 0666 and Chmod
	// cannot store 0600, so both the dialog and the result stay 0666.
	perm, mode := "644", os.FileMode(0o600)
	if runtime.GOOS == "windows" {
		perm, mode = "666", 0o666
	}
	choose("Permissions…")
	if fc.fs.ask == nil || fc.fs.ask.kind != askChmod || fc.fs.ask.value != perm {
		t.Fatalf("permissions dialog %+v", fc.fs.ask)
	}
	fc.fs.ask.value = "600"
	tt.Frame()
	mustClick(t, tt, "Save")
	if fi, err := os.Stat(filepath.Join(root, "a.txt")); err != nil || fi.Mode().Perm() != mode {
		t.Errorf("mode %v %v", fi.Mode(), err)
	}
	if fc.fs.ask != nil {
		t.Error("permissions dialog still open")
	}
	// A bad mode is said in the dialog.
	choose("Permissions…")
	fc.fs.ask.value = "999"
	tt.Frame()
	mustClick(t, tt, "Save")
	if fc.fs.ask == nil || fc.fs.ask.err == "" {
		t.Error("bad mode taken")
	}
	mustClick(t, tt, "Cancel")

	// Rename
	choose("Rename")
	if fc.fs.ask == nil || fc.fs.ask.kind != askRename || fc.fs.ask.value != "a.txt" {
		t.Fatalf("rename dialog %+v", fc.fs.ask)
	}
	fc.fs.ask.value = "b.txt"
	tt.Frame()
	mustClick(t, tt, "Rename")
	waitFor(t, tt, "rename", func() bool { return read(filepath.Join(root, "b.txt")) == "hello" && tt.HasText("b.txt") })

	// New file, from the menu of the folder
	if err := tt.RightClick("b.txt"); err != nil {
		t.Fatal(err)
	}
	chooseMenu(a, tt, "New File…")
	tt.Frame()
	tt.Type("new.txt")
	tt.Frame()
	mustClick(t, tt, "Create")
	waitFor(t, tt, "new file", func() bool { _, err := os.Stat(filepath.Join(root, "new.txt")); return err == nil })

	// Delete, after confirming
	if err := tt.RightClick("b.txt"); err != nil {
		t.Fatal(err)
	}
	chooseMenu(a, tt, "Delete…")
	tt.Frame()
	if _, err := os.Stat(filepath.Join(root, "b.txt")); err != nil {
		t.Fatalf("deleted before confirming: %v", err)
	}
	mustClick(t, tt, "Delete")
	waitFor(t, tt, "delete", func() bool {
		_, err := os.Stat(filepath.Join(root, "b.txt"))
		return os.IsNotExist(err)
	})

	// A local entry's menu uploads.
	write(t, filepath.Join(fc.fs.local.dir, "up.txt"), "up")
	a.refreshSides(fc)
	waitFor(t, tt, "up.txt", func() bool { return tt.HasText("up.txt") })
	tt.RightClick("up.txt")
	if got := menuOf(a); !slices.Contains(got, "Upload") || slices.Contains(got, "Permissions…") {
		t.Errorf("local menu %q", got)
	}
	chooseMenu(a, tt, "Upload")
	waitFor(t, tt, "menu upload", func() bool { return read(filepath.Join(root, "up.txt")) == "up" })
}

func TestFilesConflicts(t *testing.T) {
	skipWindowsSFTP(t)
	a, tt, fc, root := filesPane(t)
	local := fc.fs.local.dir
	for _, n := range []string{"a.txt", "c.txt", "d.txt"} {
		write(t, filepath.Join(local, n), "new "+n)
		write(t, filepath.Join(root, n), "old "+n)
	}
	paths := []string{filepath.Join(local, "a.txt"), filepath.Join(local, "c.txt"), filepath.Join(local, "d.txt")}
	// asked is whether the conflict asked about is name's.
	asked := func(name string) bool {
		k := fc.fs.conflict
		return k != nil && k.at < len(k.items) && k.items[k.at].Rel == name
	}
	a.Upload(fc, paths, fc.fs.remote.dir)
	waitFor(t, tt, "conflict", func() bool { return asked("a.txt") })
	if n := fc.fs.conflict.left(); n != 3 {
		t.Errorf("%d conflicts left, want 3", n)
	}
	mustClick(t, tt, "Keep Both")
	waitFor(t, tt, "second conflict", func() bool { return asked("c.txt") })
	mustClick(t, tt, "Skip")
	waitFor(t, tt, "third conflict", func() bool { return asked("d.txt") })
	if n := fc.fs.conflict.left(); n != 1 {
		t.Errorf("%d conflicts left on the last one, want 1", n)
	}
	mustClick(t, tt, "Replace")
	waitFor(t, tt, "uploads", func() bool {
		return read(filepath.Join(root, "a (2).txt")) == "new a.txt" && read(filepath.Join(root, "d.txt")) == "new d.txt"
	})
	fc.fs.transfers.Wait()
	if read(filepath.Join(root, "a.txt")) != "old a.txt" || read(filepath.Join(root, "c.txt")) != "old c.txt" {
		t.Error("kept or skipped files changed")
	}

	// Once for all
	a.Upload(fc, paths, fc.fs.remote.dir)
	waitFor(t, tt, "conflict", func() bool { return asked("a.txt") })
	mustClick(t, tt, "Do this for the other 2 too")
	mustClick(t, tt, "Replace")
	waitFor(t, tt, "replaced", func() bool {
		return read(filepath.Join(root, "a.txt")) == "new a.txt" && read(filepath.Join(root, "c.txt")) == "new c.txt"
	})
	if fc.fs.conflict != nil {
		t.Error("asked again")
	}

	// Cancel queues nothing.
	write(t, filepath.Join(local, "a.txt"), "newer")
	n := len(fc.fs.transfers.List())
	a.Upload(fc, paths[:1], fc.fs.remote.dir)
	waitFor(t, tt, "conflict", func() bool { return asked("a.txt") })
	mustClick(t, tt, "Cancel")
	tt.Frame()
	if len(fc.fs.transfers.List()) != n || fc.fs.conflict != nil {
		t.Error("canceled transfer queued")
	}
}

func TestFilesDragDropAndQueue(t *testing.T) {
	skipWindowsSFTP(t)
	a, tt, fc, root := filesPane(t)
	local := fc.fs.local.dir
	write(t, filepath.Join(local, "drag.txt"), "dragged")
	write(t, filepath.Join(root, "down.txt"), "down")
	a.refreshSides(fc)
	waitFor(t, tt, "listings", func() bool { return tt.HasText("drag.txt") && tt.HasText("down.txt") })

	// A local row dragged onto the remote side uploads.
	drag := func(from, to string) {
		x, y := nameAt(t, tt, from)
		tx, ty := center(t, tt, to)
		tt.ClickAt(x, y)        // a row chosen drags; elsewhere, a drag chooses
		tt.Advance(time.Second) // not a double click
		tt.Frame()
		tt.Press(x, y)
		tt.Move(x+10, y+5)
		tt.Move(tx, ty+60)
		tt.Release(tx, ty+60)
		tt.Frame()
	}
	drag("drag.txt", "Remote")
	waitFor(t, tt, "drag upload", func() bool { return read(filepath.Join(root, "drag.txt")) == "dragged" })
	// The remote side lists it, not its partial file, before the next
	// drag picks a row there.
	waitFor(t, tt, "listed", func() bool {
		es := fc.fs.remote.entries
		return len(es) == 2 && es[0].Name == "down.txt" && es[1].Name == "drag.txt"
	})
	tt.Frame()
	// and a remote one onto the local side downloads.
	drag("down.txt", "Local")
	waitFor(t, tt, "drag download", func() bool { return read(filepath.Join(local, "down.txt")) == "down" })

	// A folder dropped from the file manager uploads with what is in it.
	tree := filepath.Join(t.TempDir(), "tree")
	write(t, filepath.Join(tree, "x", "y", "z.txt"), "deep")
	write(t, filepath.Join(tree, "top.txt"), "top")
	x, y := center(t, tt, "Remote")
	if !tt.DropFiles(x, y+60, tree) {
		t.Fatal("drop not taken")
	}
	waitFor(t, tt, "folder upload", func() bool {
		return read(filepath.Join(root, "tree", "x", "y", "z.txt")) == "deep" && read(filepath.Join(root, "tree", "top.txt")) == "top"
	})
	fc.fs.transfers.Wait()
	tt.Frame()

	// The queue is in a sheet the header's button opens: its transfers
	// are done, and clear.
	mustClick(t, tt, "Transfers")
	if !fc.fs.queueOpen {
		t.Fatal("the transfers sheet did not open")
	}
	trs := fc.fs.transfers.List()
	if len(trs) != 4 {
		t.Fatalf("%d transfers, want 4", len(trs))
	}
	for _, tr := range trs {
		if tr.State != sftpsvc.Done {
			t.Errorf("%s: state %v, err %v", tr.Name, tr.State, tr.Err)
		}
	}
	mustClick(t, tt, "Clear finished")
	if len(fc.fs.transfers.List()) != 0 {
		t.Error("not cleared")
	}
	mustClick(t, tt, "Close")
	tt.Frame()
	if fc.fs.queueOpen {
		t.Error("sheet not closed")
	}
}

// TestFilesSplit resizes the sides by dragging the line between them;
// the files panes keep where it was left, and so do the next launches.
func TestFilesSplit(t *testing.T) {
	a, tt, _, _ := filesPane(t)
	if a.filesSplit != 50 {
		t.Fatalf("split %v", a.filesSplit)
	}
	// The sidebar's own divider would be found first.
	a.setSidebarHidden(true)
	tt.Frame()
	_, ly := center(t, tt, "Local")
	r := mustFind(t, tt, "Divider")
	x := r.X + r.W/2
	tt.Press(x, ly+100)
	tt.Move(x-10, ly+100)
	tt.Move(x-120, ly+100)
	tt.Release(x-120, ly+100)
	tt.Frame()
	got := a.filesSplit
	if got >= 50 {
		t.Errorf("split %v after dragging left", got)
	}
	if a.settings.FilesSplit != 50 {
		t.Error("saved while it moves")
	}
	tt.Advance(time.Second)
	tt.Frame()
	if a.settings.FilesSplit != got {
		t.Errorf("saved %v, want %v", a.settings.FilesSplit, got)
	}
	if saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json")); err != nil || saved.FilesSplit != got || !saved.FilesDual {
		t.Errorf("on disk %v %v %v", saved.FilesSplit, saved.FilesDual, err)
	}
	// A new pane shows both sides, split as the others are.
	other, err := a.OpenFiles(a.focused().host.ID, openAt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if oc := a.conns[other]; !oc.fs.local.loaded {
		t.Error("new pane without the local side")
	}
}

// A narrow pane stacks the local side over the server's, and the divider
// between them drags up and down.
func TestFilesSplitStacked(t *testing.T) {
	a, tt, _, _ := filesPane(t)
	a.setSidebarHidden(true)
	tt.SetSize(520, 900)
	tt.Frame()
	tt.Frame()
	lx, _ := center(t, tt, "Local")
	r := mustFind(t, tt, "Divider")
	y := r.Y + r.H/2
	tt.Press(lx, y)
	tt.Move(lx, y+10)
	tt.Move(lx, y+120)
	tt.Release(lx, y+120)
	tt.Frame()
	if a.filesSplit <= 50 {
		t.Errorf("split %v after dragging down", a.filesSplit)
	}
}

func TestFilesQueueControls(t *testing.T) {
	_, tt, fc, root := filesPane(t)
	trs := fc.fs.transfers
	// A big file and one byte at a time keeps them going.
	write(t, filepath.Join(root, "big.bin"), string(make([]byte, 4<<20)))
	trs.SetConcurrency(1)
	fc.fs.queueOpen = true
	dst := filepath.Join(fc.fs.local.dir, "big.bin")
	id := trs.Add(sftpsvc.Download, sftpsvc.RemoteJoin(fc.fs.remote.dir, "big.bin"), dst, "big.bin")
	id2 := trs.Add(sftpsvc.Download, sftpsvc.RemoteJoin(fc.fs.remote.dir, "big.bin"), dst+"2", "big2.bin")
	trs.Pause(id)
	trs.Pause(id2)
	tt.Frame()
	for _, tr := range trs.List() {
		if tr.State != sftpsvc.Paused {
			t.Fatalf("%s: state %v, want paused", tr.Name, tr.State)
		}
	}
	mustClick(t, tt, "Resume all")
	waitFor(t, tt, "done", func() bool {
		ts := trs.List()
		return len(ts) == 2 && ts[0].State == sftpsvc.Done && ts[1].State == sftpsvc.Done
	})
	tt.Frame()
	// Cancel and retry, one row at a time.
	id3 := trs.Add(sftpsvc.Download, sftpsvc.RemoteJoin(fc.fs.remote.dir, "big.bin"), dst+"3", "big3.bin")
	trs.Pause(id3)
	tt.Frame()
	mustClick(t, tt, "Cancel big3.bin")
	tt.Frame()
	if i := slices.IndexFunc(trs.List(), func(tr sftpsvc.Transfer) bool { return tr.ID == id3 }); i < 0 || trs.List()[i].State != sftpsvc.Canceled {
		t.Fatalf("big3.bin not canceled: %+v", trs.List())
	}
	mustClick(t, tt, "Retry big3.bin")
	waitFor(t, tt, "retried", func() bool { return read(dst+"3") == read(filepath.Join(root, "big.bin")) })
}

// TestFilesTenThousand lists 10,000 remote files and scrolls through
// them: the table builds only the rows it shows, so a frame costs about
// what it does with 200 files (compared rather than timed, as -race is
// slow).
func TestFilesTenThousand(t *testing.T) {
	a, tt, fc, root := filesPane(t)
	// Kept from before either listing: listRemote replaces remote.dir
	// with the folder it opened. The server wants this SFTP path; on
	// Windows the local path is not one, and joining it on doubles it.
	home := fc.fs.remote.dir
	scroll := func(n int) time.Duration {
		name := fmt.Sprintf("many%d", n)
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := range n {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%05d.txt", i)), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		a.listRemote(&fc.fs.remote, sftpsvc.RemoteJoin(home, name))
		waitFor(t, tt, "listed", func() bool { return len(fc.fs.remote.entries) == n })
		tt.Frame()
		r, ok := tt.Find("f00000.txt")
		if !ok {
			t.Fatalf("first row missing: %q", tt.Texts())
		}
		const frames = 40
		start := time.Now()
		for i := range frames {
			dy := float32(60)
			if i%2 == 1 && n < 1000 { // stay inside the short list
				dy = -60
			}
			tt.Wheel(r.X+r.W/2, r.Y+r.H/2, 0, dy)
			tt.Advance(16 * time.Millisecond)
		}
		if n := len(tt.Texts()); n > 400 {
			t.Errorf("%d texts in a frame: rows are not virtualized", n)
		}
		return time.Since(start) / frames
	}
	small := scroll(200)
	big := scroll(10000)
	t.Logf("%v per frame with 200 files, %v with 10,000", small, big)
	if big > 3*small+20*time.Millisecond {
		t.Errorf("10,000 files: %v per frame, 200 files: %v", big, small)
	}
}

// TestOneFilesPanePerTab: a tab has one files pane. Opening another on
// its host shows that one, the rail's Files button is lit while it is
// there and closes it, and a files pane does not drag into a tab that
// has one.
func TestOneFilesPanePerTab(t *testing.T) {
	a, tt, fc, _ := filesPane(t)
	term, err := a.Connect(fc.host.ID, openAt{pane: a.paneNode(fc.id), edge: layout.Right})
	if err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tab := a.ws.ActiveTab()
	id, err := a.OpenFiles(fc.host.ID, openAt{pane: a.paneNode(term), edge: layout.Bottom}, nil)
	if err != nil || id != fc.id || tab.Count(layout.Files) != 1 || a.focused() != fc {
		t.Fatalf("second files pane %d (first %d), %d in the tab, err %v", id, fc.id, tab.Count(layout.Files), err)
	}
	tt.Key(appMods, ui.KeyO) // toggles: the tab has one, so it closes
	tt.Frame()
	if tab.Count(layout.Files) != 0 || a.conns[fc.id] != nil {
		t.Fatal("the Files shortcut did not close the tab's files pane")
	}
	mustClick(t, tt, "Open Files on the Right")
	tt.Frame()
	if tab.Count(layout.Files) != 1 {
		t.Fatal("rail did not open a files pane")
	}
	mustClick(t, tt, "Close Files")
	tt.Frame()
	if tab.Count(layout.Files) != 0 {
		t.Fatal("rail did not close the files pane")
	}

	// A files pane of another tab does not drop into this one at an edge;
	// a swap with this tab's own files pane keeps one each.
	mustClick(t, tt, "Open Files on the Right")
	here := a.tabPane(tab, layout.Files)
	other, err := a.OpenFiles(fc.host.ID, openAt{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.fits(other, tab.Pane(a.paneNode(term)), layout.Right) {
		t.Fatal("a second files pane fits into the tab")
	}
	if !a.fits(other, tab.Pane(a.paneNode(here.id)), layout.Center) {
		t.Fatal("swapping two files panes does not fit")
	}
	if a.fits(other, tab.Pane(a.paneNode(term)), layout.Center) {
		t.Fatal("swapping a terminal for a second files pane fits")
	}
}

// TestFilesClickName: a row's name drags it, and still chooses the row
// when clicked and opens it when double-clicked.
func TestFilesClickName(t *testing.T) {
	a, tt, fc, root := filesPane(t)
	write(t, filepath.Join(root, "a.txt"), "hello")
	write(t, filepath.Join(root, "dir", "inner.txt"), "x")
	a.refreshSides(fc)
	waitFor(t, tt, "a.txt", func() bool { return tt.HasText("a.txt") })
	s := &fc.fs.remote

	mustClick(t, tt, "a.txt")
	tt.Frame()
	if e, ok := selected(s.sel, s.entries); !ok || e.Name != "a.txt" {
		t.Fatalf("clicking the name chose %d of %v", s.sel, s.entries)
	}
	mustClick(t, tt, "dir")
	mustClick(t, tt, "dir")
	waitFor(t, tt, "into dir", func() bool { return tt.HasText("inner.txt") })
	tt.Frame()
	if s.sel != -1 {
		t.Errorf("a new folder starts with row %d chosen", s.sel)
	}
	mustClick(t, tt, "inner.txt")
	tt.Frame()
	if e, ok := selected(s.sel, s.entries); !ok || e.Name != "inner.txt" {
		t.Fatalf("a click after opening a folder chose %d of %v", s.sel, s.entries)
	}
	tt.Advance(time.Second) // not a double click with the one before
	wins := fakeWindows(a)
	mustClick(t, tt, "inner.txt")
	mustClick(t, tt, "inner.txt")
	waitFor(t, tt, "editor", func() bool { return len(wins.list()) == 1 })
	if ec := editorConn(a); ec == nil || shown(ec) == nil || shown(ec).name != "inner.txt" || len(fc.fs.transfers.List()) != 0 {
		t.Fatalf("double click did not open the editor alone: %+v", fc.fs.transfers.List())
	}
}

// TestFilesEdgeSetting: a files pane opens on the terminal's right by
// default, and under it when the files settings say below; the
// terminal's menu offers the other place too.
func TestFilesEdgeSetting(t *testing.T) {
	srv, err := sshtest.Start("127.0.0.1:0", "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	a, tt := newTestApp(t)
	h := addHost(t, a, srv, "box", "secret")
	a.known.Add(srv.Addr, srv.HostKey.PublicKey())
	a.Connect(h.ID, openAt{})
	tab := a.ws.ActiveTab()
	m := locales.In("en")
	labels := func() []string {
		cn := a.focused()
		var out []string
		for _, e := range a.termEntries(tab, tab.Pane(tab.Focus), cn, cn.tv, ui.Services{}, m) {
			out = append(out, e.Label)
		}
		return out
	}
	if l := labels(); !slices.Contains(l, "Open Files on the Right") || !slices.Contains(l, "Open Files Below") {
		t.Errorf("terminal menu %q", l)
	}
	a.OpenFilesBeside()
	if tab.Root.Axis != layout.Row {
		t.Errorf("by default the files pane is not on the right: axis %v", tab.Root.Axis)
	}
	a.ToggleFiles()

	a.openSettings(prefFiles)
	tt.Frame()
	mustClick(t, tt, "Below")
	tt.Frame()
	if a.settings.FilesEdge != settings.FilesBelow {
		t.Fatalf("chose %q, want below", a.settings.FilesEdge)
	}
	a.page = pageWorkspace
	tt.Frame()
	a.OpenFilesBeside()
	if tab.Count(layout.Files) != 1 || tab.Root.Axis != layout.Column {
		t.Errorf("the files pane is not under the terminal: %d, axis %v", tab.Count(layout.Files), tab.Root.Axis)
	}
}

// TestFilesSortByHeader: a click on a column's header sorts the rows by
// it, a second reverses them and a third shows them as listed, folders
// first throughout; the row chosen stays chosen.
func TestFilesSortByHeader(t *testing.T) {
	a, tt, fc, root := filesPaneAs(t, false, nil)
	write(t, filepath.Join(root, "a.txt"), "12345678")
	write(t, filepath.Join(root, "B.txt"), "1")
	write(t, filepath.Join(root, "c.txt"), "1234")
	write(t, filepath.Join(root, "dir", "inner.txt"), "x")
	a.refreshSides(fc)
	waitFor(t, tt, "c.txt", func() bool { return tt.HasText("c.txt") })
	s := &fc.fs.remote
	names := func() []string {
		var out []string
		for _, e := range s.entries {
			out = append(out, e.Name)
		}
		return out
	}
	click := func(title string, want ...string) {
		t.Helper()
		tt.Advance(time.Second) // not a double click with the one before
		mustClick(t, tt, title)
		tt.Frame()
		if got := names(); !slices.Equal(got, want) {
			t.Fatalf("after clicking %s: %q, want %q", title, got, want)
		}
	}
	listed := []string{"dir", "a.txt", "B.txt", "c.txt"}
	if got := names(); !slices.Equal(got, listed) {
		t.Fatalf("listed %q", got)
	}
	mustClick(t, tt, "B.txt")
	click("Size", "dir", "B.txt", "c.txt", "a.txt")
	click("Size", "dir", "a.txt", "c.txt", "B.txt")
	click("Size", listed...)
	if s.sort != (ui.SortOrder{}) {
		t.Errorf("the third click left the sort %+v", s.sort)
	}
	click("Name", listed...)
	click("Name", "dir", "c.txt", "B.txt", "a.txt")
	if e, ok := selected(s.sel, s.entries); !ok || e.Name != "B.txt" || !s.chosen.Has("B.txt") {
		t.Errorf("sorting chose %d of %q", s.sel, names())
	}
	// A folder listed again keeps the order.
	a.refreshSides(fc)
	waitFor(t, tt, "listed again", func() bool { return s.sel == -1 })
	if got := names(); !slices.Equal(got, []string{"dir", "c.txt", "B.txt", "a.txt"}) {
		t.Errorf("listed again %q", got)
	}
	click("Name", listed...)
}
