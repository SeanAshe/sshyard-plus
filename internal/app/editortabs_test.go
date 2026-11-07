package app

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/egoist/mygo/ui"
)

// The files of a host open in tabs of one window, those of this computer
// in another: a click or Ctrl+Tab shows a tab, closing an edited one
// asks, and the window stays open, empty, once its last tab closes.
func TestEditorTabs(t *testing.T) {
	skipWindowsSFTP(t)
	a, tt, fc, root := filesPane(t)
	wins := fakeWindows(a)
	write(t, filepath.Join(root, "a.txt"), "a\n")
	write(t, filepath.Join(root, "b.txt"), "b\n")
	a.refreshSides(fc)
	waitFor(t, tt, "listing", func() bool { return tt.HasText("a.txt") && tt.HasText("b.txt") })
	editorOn(t, a, tt, "a.txt")
	w, et, ec := openedWindow(t, a, tt, wins, 1)
	editorOn(t, a, tt, "b.txt")
	waitFor(t, et, "second tab", func() bool { return len(ec.ed.tabs) == 2 && shown(ec).name == "b.txt" })
	if n := len(wins.list()); n != 1 {
		t.Fatalf("%d windows for one host", n)
	}
	waitFor(t, et, "title", func() bool { title, _, _, _ := w.state(); return title == "b.txt — box" })

	// The tab bar comes first: the tab, not the row of the folders.
	mustClick(t, et, "a.txt")
	et.Frame()
	if shown(ec).name != "a.txt" {
		t.Fatalf("shows %q after a click on its tab", shown(ec).name)
	}
	et.Key(ui.Ctrl, ui.KeyTab)
	et.Frame()
	if shown(ec).name != "b.txt" {
		t.Fatalf("shows %q after Ctrl+Tab", shown(ec).name)
	}
	et.Key(ui.Ctrl|ui.Shift, ui.KeyTab)
	et.Frame()
	if shown(ec).name != "a.txt" {
		t.Fatalf("shows %q after Ctrl+Shift+Tab", shown(ec).name)
	}
	mustClick(t, et, "b.txt")
	et.Frame()

	b := shown(ec)
	b.st.Buffer.Set("changed\n")
	et.Frame()
	mustClick(t, et, "Close “b.txt”")
	waitFor(t, et, "question", func() bool { return et.HasText("Save changes to “b.txt”?") })
	mustClick(t, et, "Cancel")
	et.Frame()
	if len(ec.ed.tabs) != 2 {
		t.Fatal("canceling closed the tab")
	}
	mustClick(t, et, "Close “b.txt”")
	waitFor(t, et, "question", func() bool { return et.HasText("Don’t Save") })
	mustClick(t, et, "Don’t Save")
	et.Frame()
	if len(ec.ed.tabs) != 1 || shown(ec).name != "a.txt" || read(filepath.Join(root, "b.txt")) != "b\n" {
		t.Fatalf("after Don't Save: %d tabs, %q", len(ec.ed.tabs), read(filepath.Join(root, "b.txt")))
	}
	mustClick(t, et, "Close “a.txt”")
	et.Frame()
	if len(ec.ed.tabs) != 0 || a.conns[ec.id] != ec {
		t.Fatalf("the last tab closed: %d tabs", len(ec.ed.tabs))
	}
	waitFor(t, et, "title", func() bool { title, _, _, _ := w.state(); return title == "box" })

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "here.txt"), "here")
	write(t, filepath.Join(dir, "near.txt"), "near")
	a.editFile(fc, false, filepath.Join(dir, "here.txt"), 4, false)
	_, lt, lc := openedWindow(t, a, tt, wins, 2)
	if !lc.ed.local || lc.s != nil || shown(lc).name != "here.txt" {
		t.Fatalf("local window %+v", lc.ed)
	}
	waitFor(t, lt, "local folders", func() bool { return lt.HasText("near.txt") })
	a.editFile(ec, true, path.Join(root, "a.txt"), 2, false)
	waitFor(t, et, "back in the host's window", func() bool { return len(ec.ed.tabs) == 1 })
	if len(wins.list()) != 2 || len(lc.ed.tabs) != 1 {
		t.Fatalf("%d windows, %d local tabs", len(wins.list()), len(lc.ed.tabs))
	}
}

// The folders start at the file's, chosen; Up goes to the folder above,
// with the one left open. A double click opens: a folder, a text file in
// a tab, an image in a tab of its own, another file asks how.
func TestEditorFolders(t *testing.T) {
	skipWindowsSFTP(t)
	a, tt, fc, root := filesPane(t)
	wins := fakeWindows(a)
	write(t, filepath.Join(root, "top.txt"), "top\n")
	write(t, filepath.Join(root, "sub", "in.txt"), "in\n")
	write(t, filepath.Join(root, "sub", "deep", "x.txt"), "x\n")
	writePNG(t, filepath.Join(root, "sub", "pic.png"), 3, 2)
	write(t, filepath.Join(root, "sub", "doc.pdf"), "%PDF-1.4")
	sub := path.Join(root, "sub")
	a.editFile(fc, true, path.Join(sub, "in.txt"), 3, false)
	_, et, ec := openedWindow(t, a, tt, wins, 1)
	tr := &ec.ed.tree
	waitFor(t, et, "folders", func() bool { return et.HasText("deep") && et.HasText("pic.png") })
	waitFor(t, et, "file chosen", func() bool {
		return tr.root == sub && tr.sel >= 0 && tr.sel < tr.out.Rows() && tr.out.Item(tr.sel) == path.Join(sub, "in.txt")
	})

	mustClick(t, et, "Up One Folder")
	waitFor(t, et, "up", func() bool { return et.HasText("top.txt") && et.HasText("deep") })
	if tr.root != root || !tr.out.Open.Has(sub) {
		t.Fatalf("root %q, open %v", tr.root, slices.Collect(tr.out.Open.All()))
	}

	open := func(name string) {
		t.Helper()
		et.Advance(time.Second) // not a double click with the one before
		mustClick(t, et, name)
		mustClick(t, et, name)
		et.Frame()
	}
	open("deep")
	waitFor(t, et, "deep open", func() bool { return et.HasText("x.txt") })
	open("top.txt")
	waitFor(t, et, "top.txt in a tab", func() bool {
		ed := shown(ec)
		return ed != nil && ed.name == "top.txt" && ed.st.Buffer.String() == "top\n"
	})
	open("pic.png")
	waitFor(t, et, "image in a tab", func() bool { ed := shown(ec); return ed != nil && ed.img != nil && ed.img.bmp != nil })
	if len(ec.ed.tabs) != 3 {
		t.Errorf("image tab: %d tabs", len(ec.ed.tabs))
	}
	open("doc.pdf")
	waitFor(t, et, "open with", func() bool { return et.HasText("Open “doc.pdf”") })
	mustClick(t, et, "Cancel")
	et.Frame()
	if len(wins.list()) != 1 || len(ec.ed.tabs) != 3 {
		t.Fatalf("%d windows, %d tabs", len(wins.list()), len(ec.ed.tabs))
	}
}

// The folders make files and folders, and rename, delete, upload and
// download from their rows' menu, which shows in their window alone; the
// tabs follow what is renamed and close with what is deleted.
func TestEditorFolderOps(t *testing.T) {
	skipWindowsSFTP(t)
	a, tt, fc, root := filesPane(t)
	wins := fakeWindows(a)
	for _, n := range []string{"a.txt", "old.txt", "gone.txt", "dl.txt"} {
		write(t, filepath.Join(root, n), n)
	}
	src, dest := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "up.txt"), "up")
	a.p.PickFiles = func(string) ([]string, error) { return []string{filepath.Join(src, "up.txt")}, nil }
	a.p.PickFolder = func(string, string) (string, error) { return dest, nil }
	a.editFile(fc, true, path.Join(root, "a.txt"), 5, false)
	_, et, ec := openedWindow(t, a, tt, wins, 1)
	waitFor(t, et, "folders", func() bool { return et.HasText("old.txt") && et.HasText("dl.txt") })

	answer := func(kind askKind, dir, value string) {
		t.Helper()
		ask := ec.fs.ask
		if ask == nil || ask.kind != kind || dir != "" && ask.dir != dir {
			t.Fatalf("asked %+v", ask)
		}
		ask.value = value
		if err := a.doAsk(ec, ask); err != nil {
			t.Fatal(err)
		}
		ec.fs.askOpen = false
		et.Frame()
	}
	mustClick(t, et, "New File…")
	et.Frame()
	answer(askNewFile, root, "new.txt")
	waitFor(t, et, "new file in a tab", func() bool { ed := shown(ec); return ed != nil && ed.name == "new.txt" && !ed.loading })
	if fi, err := os.Stat(filepath.Join(root, "new.txt")); err != nil || fi.Size() != 0 {
		t.Fatalf("new file %v, %v", fi, err)
	}
	mustClick(t, et, "New Folder…")
	et.Frame()
	answer(askMkdir, root, "made")
	waitFor(t, et, "new folder", func() bool { return et.HasText("made") })
	if fi, err := os.Stat(filepath.Join(root, "made")); err != nil || !fi.IsDir() {
		t.Fatalf("new folder %v, %v", fi, err)
	}

	// A row's menu belongs to the editor's window, not the main one.
	if err := et.RightClick("old.txt"); err != nil {
		t.Fatal(err)
	}
	et.Frame()
	tt.Frame()
	if a.ctx.win != ec.id {
		t.Fatalf("menu %q in window %d", menuOf(a), a.ctx.win)
	}
	if got := menuOf(a); !slices.Contains(got, "Rename") || !slices.Contains(got, "Download To…") || !slices.Contains(got, "Upload Files…") {
		t.Fatalf("file menu %q", got)
	}
	if err := chooseMenu(a, et, "Rename"); err != nil {
		t.Fatal(err)
	}
	ask := ec.fs.ask
	a.editFile(ec, true, path.Join(root, "old.txt"), 7, false)
	waitFor(t, et, "old.txt in a tab", func() bool { ed := shown(ec); return ed != nil && ed.name == "old.txt" })
	old := shown(ec)
	ec.fs.ask = ask
	answer(askRename, "", "renamed.txt")
	if old.path != path.Join(root, "renamed.txt") || old.name != "renamed.txt" || read(filepath.Join(root, "renamed.txt")) != "old.txt" {
		t.Fatalf("renamed tab %q", old.path)
	}
	old.st.Buffer.Set("saved after the rename")
	a.saveTab(ec, old, false)
	waitFor(t, et, "saved to the new name", func() bool { return read(filepath.Join(root, "renamed.txt")) == "saved after the rename" })

	a.editFile(ec, true, path.Join(root, "gone.txt"), 8, false)
	waitFor(t, et, "gone.txt in a tab", func() bool { ed := shown(ec); return ed != nil && ed.name == "gone.txt" })
	n := len(ec.ed.tabs)
	a.treeDelete(ec, path.Join(root, "gone.txt"))
	et.Frame()
	mustClick(t, et, "Delete")
	waitFor(t, et, "deleted", func() bool { return len(ec.ed.tabs) == n-1 })
	if _, err := os.Stat(filepath.Join(root, "gone.txt")); !os.IsNotExist(err) {
		t.Fatalf("still there: %v", err)
	}

	// Uploads go into the folder of the row; downloads to a folder chosen.
	if err := et.RightClick("made"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(a, et, "Upload Files…"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, et, "uploaded", func() bool { return read(filepath.Join(root, "made", "up.txt")) == "up" })
	if err := et.RightClick("dl.txt"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(a, et, "Download To…"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, et, "downloaded", func() bool { return read(filepath.Join(dest, "dl.txt")) == "dl.txt" })

	// Off the rows, the menu is the top folder's.
	r := mustFind(t, et, "Folders")
	et.RightClickAt(r.X+r.W/2, r.Y+r.H-8)
	et.Frame()
	if got := menuOf(a); !slices.Contains(got, "New File…") || slices.Contains(got, "Rename") {
		t.Fatalf("folder menu %q", got)
	}
}

// Closing a window with several edited tabs asks once; Save All saves
// them all, then closes it.
func TestEditorWindowSaveAll(t *testing.T) {
	skipWindowsSFTP(t)
	a, tt, fc, root := filesPane(t)
	wins := fakeWindows(a)
	write(t, filepath.Join(root, "a.txt"), "a")
	write(t, filepath.Join(root, "b.txt"), "b")
	a.editFile(fc, true, path.Join(root, "a.txt"), 1, false)
	w, et, ec := openedWindow(t, a, tt, wins, 1)
	a.editFile(fc, true, path.Join(root, "b.txt"), 1, false)
	waitFor(t, et, "two tabs", func() bool { return len(ec.ed.tabs) == 2 })
	for _, ed := range ec.ed.tabs {
		ed.st.Buffer.Set("new " + ed.name)
	}
	et.Frame()
	waitFor(t, et, "window edited", func() bool { _, edited, _, _ := w.state(); return edited })
	if w.userClose() {
		t.Fatal("closed with unsaved changes")
	}
	waitFor(t, et, "close question", func() bool { return et.HasText("Save changes to 2 files?") })
	et.Key(0, ui.KeyEnter) // Save All, the default
	waitFor(t, tt, "saved and closed", func() bool { _, _, closed, _ := w.state(); return closed && a.conns[ec.id] == nil })
	if read(filepath.Join(root, "a.txt")) != "new a.txt" || read(filepath.Join(root, "b.txt")) != "new b.txt" {
		t.Fatalf("saved %q, %q", read(filepath.Join(root, "a.txt")), read(filepath.Join(root, "b.txt")))
	}
}

// The folders' width is saved once it rests, for the windows that open
// next; their button hides them, for every window.
func TestEditorFoldersLayout(t *testing.T) {
	a, tt := newTestApp(t)
	wins := fakeWindows(a)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "f.txt"), "f")
	a.editFile(&conn{host: a.localHost(localID), fs: newFilesState(nil)}, false, filepath.Join(dir, "f.txt"), 1, false)
	_, et, ec := openedWindow(t, a, tt, wins, 1)
	if ec.ed.treeW != 240 {
		t.Fatalf("starts at %v", ec.ed.treeW)
	}
	ec.ed.treeW = 300
	et.Frame()
	et.Advance(time.Second)
	et.Frame()
	if saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json")); err != nil || saved.EditorTreeWidth != 300 {
		t.Fatalf("saved %v, %v", saved.EditorTreeWidth, err)
	}
	mustClick(t, et, "Show Folders")
	et.Frame()
	if !a.settings.EditorTreeHidden {
		t.Fatal("the folders' button did not hide them")
	}
}
