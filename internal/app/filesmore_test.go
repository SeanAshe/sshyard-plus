package app

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/sftpsvc"
	"github.com/Licoy/sshyard/internal/sshtest"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

// localPane is a files pane whose local side lists a new folder with
// files, returned with it.
func localPane(t *testing.T, files ...string) (*App, *ui.Tester, *conn, string) {
	t.Helper()
	a, tt, fc, _ := filesPane(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		write(t, filepath.Join(dir, f), f)
	}
	a.listLocal(&fc.fs.local, dir)
	tt.Frame()
	tt.Frame()
	return a, tt, fc, dir
}

func chosenNames(sd *sideList) []string {
	var out []string
	for _, e := range sd.picked() {
		out = append(out, e.Name)
	}
	return out
}

// Rows are chosen with clicks (Shift extends, Cmd/Ctrl toggles), the
// keys and a drag over them; the delete key deletes all those chosen.
func TestFilesMultiSelect(t *testing.T) {
	a, tt, fc, dir := localPane(t, "a.txt", "b.txt", "c.txt", "d.txt")
	l := &fc.fs.local
	click := func(mods ui.Modifiers, name string) {
		t.Helper()
		x, y := nameAt(t, tt, name)
		tt.ClickAtWith(mods, x, y)
		tt.Advance(time.Second) // the next is not a double click
		tt.Frame()
	}
	click(0, "a.txt")
	click(ui.Shift, "c.txt")
	if got := chosenNames(&l.sideList); !slices.Equal(got, []string{"a.txt", "b.txt", "c.txt"}) {
		t.Fatalf("shift click chose %q", got)
	}
	click(toggleMods, "b.txt")
	if got := chosenNames(&l.sideList); !slices.Equal(got, []string{"a.txt", "c.txt"}) {
		t.Fatalf("toggle chose %q", got)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if l.chosen.Len() != 0 {
		t.Fatalf("escape left %q", chosenNames(&l.sideList))
	}

	// The keys: Shift+Down extends from the row chosen.
	click(0, "b.txt")
	tt.Key(ui.Shift, ui.KeyDown)
	tt.Frame()
	if got := chosenNames(&l.sideList); !slices.Equal(got, []string{"b.txt", "c.txt"}) {
		t.Fatalf("shift+down chose %q", got)
	}

	// A drag off the names chooses the rows it goes over.
	ra, _ := tt.Find("a.txt")
	rc, _ := tt.Find("c.txt")
	x := ra.X + ra.W - 12
	tt.Press(x, ra.Y+ra.H/2)
	tt.Move(x, ra.Y+ra.H/2+8)
	tt.Move(x, rc.Y+rc.H/2)
	tt.Release(x, rc.Y+rc.H/2)
	tt.Frame()
	if got := chosenNames(&l.sideList); !slices.Equal(got, []string{"a.txt", "b.txt", "c.txt"}) {
		t.Fatalf("drag chose %q", got)
	}

	// A right click on a row chosen keeps the others chosen.
	if err := tt.RightClick("b.txt"); err != nil {
		t.Fatal(err)
	}
	if l.chosen.Len() != 3 || !slices.Contains(menuOf(a), "Compress…") || slices.Contains(menuOf(a), "Rename") {
		t.Fatalf("chose %q, menu %q", chosenNames(&l.sideList), menuOf(a))
	}
	tt.Key(0, ui.KeyEscape) // closes the menu
	tt.Frame()

	tt.Key(deleteMods, deleteKey) // the right click left the focus on the table
	tt.Frame()
	if fc.fs.del == nil || len(fc.fs.del.paths) != 3 {
		t.Fatalf("no confirmation for 3 items: %+v", fc.fs.del)
	}
	mustClick(t, tt, "Delete")
	waitFor(t, tt, "deleted", func() bool {
		for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
			if _, err := os.Stat(filepath.Join(dir, n)); !os.IsNotExist(err) {
				return false
			}
		}
		return true
	})
	if read(filepath.Join(dir, "d.txt")) != "d.txt" {
		t.Error("d.txt deleted")
	}
}

// The menu is drawn by the app: a click on its entry chooses it. Get Info
// has a file's size, and counts a folder's.
func TestFilesMenuClickAndInfo(t *testing.T) {
	_, tt, fc, _ := localPane(t, "a.txt", "d/one.txt", "d/sub/two.txt")
	if err := tt.RightClick("a.txt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	mustClick(t, tt, "Get Info")
	tt.Frame()
	if info := fc.fs.info; info == nil || len(info.es) != 1 || info.es[0].Name != "a.txt" || info.size != 5 || info.files != 1 {
		t.Fatalf("info %+v", fc.fs.info)
	}
	mustClick(t, tt, "Close")
	tt.Frame()
	if fc.fs.info != nil {
		t.Fatal("info stays")
	}

	// A folder's size is counted.
	x, y := nameAt(t, tt, "d")
	tt.ClickAt(x, y)
	tt.Frame()
	tt.Key(toggleMods, ui.KeyI)
	tt.Frame()
	waitFor(t, tt, "folder size", func() bool {
		info := fc.fs.info
		return info != nil && !info.counting && len(info.es) == 1 && info.es[0].IsDir
	})
	if info := fc.fs.info; info.size != 22 || info.files != 2 {
		t.Errorf("folder of %d B in %d files, want 22 B in 2", info.size, info.files)
	}
}

// Folders and files compress into a new archive, which extracts into a
// new folder by it.
func TestFilesCompressExtractLocal(t *testing.T) {
	a, tt, fc, dir := localPane(t, "d/one.txt", "d/sub/two.txt")
	m := locales.In("en")
	l := &fc.fs.local
	x, y := nameAt(t, tt, "d")
	tt.ClickAt(x, y)
	tt.Frame()
	if err := tt.RightClick("d"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(a, tt, "Compress…"); err != nil {
		t.Fatal(err)
	}
	ask := fc.fs.compress
	if ask == nil || ask.name != "d.zip" {
		t.Fatalf("ask %+v", ask)
	}
	mustClick(t, tt, "tar.gz")
	tt.Frame()
	if ask.name != "d.tar.gz" {
		t.Fatalf("name %q after tar.gz", ask.name)
	}
	if err := a.compress(fc, ask, m); err != nil {
		t.Fatal(err)
	}
	fc.fs.compressOpen = false
	waitFor(t, tt, "archive", func() bool { return fc.fs.working == nil && slices.Contains(chosenNames(&l.sideList), "d.tar.gz") })

	// The same name again is refused.
	ask = &compressAsk{dir: dir, names: []string{"d"}, name: "d.tar.gz", format: int(sftpsvc.TarGz)}
	if err := a.compress(fc, ask, m); err == nil || err.Error() != "“d.tar.gz” already exists." {
		t.Fatalf("taken name: %v", err)
	}

	if err := tt.RightClick("d.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if err := chooseMenu(a, tt, "Extract Here"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, tt, "extracted", func() bool {
		return fc.fs.working == nil && read(filepath.Join(dir, "d (2)", "d", "sub", "two.txt")) != ""
	})
	if got := read(filepath.Join(dir, "d (2)", "d", "sub", "two.txt")); got != "d/sub/two.txt" {
		t.Fatalf("extracted %q", got)
	}
	if l.err != "" {
		t.Fatal(l.err)
	}
}

// On the server, the archive is made by a command; its error shows.
func TestFilesCompressRemote(t *testing.T) {
	skipWindowsSFTP(t)
	cmds := make(chan string, 4)
	a, tt, fc, root := filesPaneOn(t, func(srv *sshtest.Server) {
		srv.Exec = func(ch ssh.Channel, cmd string) int {
			cmds <- cmd
			ch.Stderr().Write([]byte("tar: something\ntar: no space left\n"))
			return 2
		}
	})
	write(t, filepath.Join(root, "logs", "a.log"), "a")
	a.listAgain(fc, true)
	waitFor(t, tt, "listing", func() bool { return tt.HasText("logs") })
	a.askCompress(fc, true, []sftpsvc.Entry{{Name: "logs", IsDir: true}}, locales.In("en"))
	ask := fc.fs.compress
	if ask.name != "logs.tar.gz" {
		t.Fatalf("name %q", ask.name)
	}
	if err := a.compress(fc, ask, locales.In("en")); err != nil {
		t.Fatal(err)
	}
	fc.fs.compressOpen = false
	tt.Frame()
	if fc.fs.working == nil && fc.fs.remote.err == "" {
		t.Error("neither compressing nor failed")
	}
	var cmd string
	select {
	case cmd = <-cmds:
	case <-time.After(5 * time.Second):
		t.Fatal("no command")
	}
	if want := sftpsvc.CompressCommand(root, []string{"logs"}, filepath.Join(root, "logs.tar.gz"), sftpsvc.TarGz); cmd != want {
		t.Errorf("command %q, want %q", cmd, want)
	}
	waitFor(t, tt, "error", func() bool { return fc.fs.working == nil && fc.fs.remote.err != "" })
	if fc.fs.remote.err != "tar: no space left" {
		t.Errorf("error %q", fc.fs.remote.err)
	}
}

func writePNG(t *testing.T, p string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{0x19, 0x84, 0xfc, 0xff})
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// An image opens in a preview, which steps through the folder's images.
func TestFilesImagePreview(t *testing.T) {
	_, tt, fc, dir := localPane(t, "notes.txt")
	writePNG(t, filepath.Join(dir, "a.png"), 3, 2)
	writePNG(t, filepath.Join(dir, "b.png"), 4, 5)
	write(t, filepath.Join(dir, "broken.jpg"), "not a jpeg")
	tt.Frame()
	mustClick(t, tt, "Refresh Local Files")
	tt.Frame()
	x, y := nameAt(t, tt, "a.png")
	tt.ClickAt(x, y)
	tt.Frame()
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	p := fc.fs.preview
	if p == nil {
		t.Fatal("no preview")
	}
	waitFor(t, tt, "image", func() bool { return p.bmp != nil })
	if p.at != 0 || len(p.images) != 3 {
		t.Fatalf("image %d of %d, want 0 of 3", p.at, len(p.images))
	}
	tt.Key(0, ui.KeyRight)
	waitFor(t, tt, "next image", func() bool { return p.at == 1 && p.bmp != nil })
	mustClick(t, tt, "Next Image")
	waitFor(t, tt, "broken image", func() bool { return !p.loading && p.err != "" })
	if p.at != 2 || p.err != "image: unknown format" {
		t.Errorf("image %d, error %q", p.at, p.err)
	}
	mustClick(t, tt, "Close Preview")
	tt.Frame()
	if fc.fs.preview != nil {
		t.Fatal("preview stays")
	}
}

func TestCrumbsOf(t *testing.T) {
	got := crumbsOf("/srv/www/site", true)
	want := []crumb{{"/", "/"}, {"srv", "/srv"}, {"www", "/srv/www"}, {"site", "/srv/www/site"}}
	if !slices.Equal(got, want) {
		t.Errorf("remote %q", got)
	}
	if got := crumbsOf("C:/Users", true); !slices.Equal(got, []crumb{{"C:", "C:/"}, {"Users", "C:/Users"}}) {
		t.Errorf("remote drive %q", got)
	}
	if got := crumbsOf("", true); got != nil {
		t.Errorf("none %q", got)
	}
	local := filepath.Join(string(filepath.Separator), "home", "me")
	got = crumbsOf(local, false)
	if len(got) != 3 || got[1].name != "home" || got[2].path != local {
		t.Errorf("local %q", got)
	}
}

// The breadcrumbs go to a folder above; resting on one lists its folder,
// to go into a folder or to a file there.
func TestFilesBreadcrumbs(t *testing.T) {
	skipWindowsSFTP(t)
	a, tt, fc, root := filesPane(t)
	write(t, filepath.Join(root, "sub", "deep", "x.txt"), "x")
	write(t, filepath.Join(root, "sub", "other.txt"), "o")
	s := &fc.fs.remote
	a.listRemote(s, filepath.Join(root, "sub", "deep"))
	waitFor(t, tt, "deep", func() bool { return tt.HasText("x.txt") })
	tt.Frame()

	r, ok := tt.Find("sub")
	if !ok {
		t.Fatalf("no crumb in %q", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if strings.HasPrefix(a.menu.key, "crumb:") {
		t.Fatal("menu at once")
	}
	tt.Advance(crumbRest + 50*time.Millisecond)
	tt.Frame()
	if !strings.HasPrefix(a.menu.key, "crumb:") {
		t.Fatalf("no menu, key %q", a.menu.key)
	}
	waitFor(t, tt, "listed", func() bool { return tt.HasText("other.txt") })
	mustClick(t, tt, "other.txt")
	waitFor(t, tt, "revealed", func() bool {
		return s.dir == filepath.Join(root, "sub") && slices.Contains(chosenNames(&s.sideList), "other.txt")
	})

	// A click goes there.
	a.listRemote(s, filepath.Join(root, "sub", "deep"))
	waitFor(t, tt, "deep again", func() bool { return tt.HasText("x.txt") })
	tt.Frame()
	mustClick(t, tt, "sub")
	waitFor(t, tt, "sub", func() bool { return s.dir == filepath.Join(root, "sub") })
}

func TestTypedPath(t *testing.T) {
	for _, tc := range []struct{ typed, want string }{
		{"/var/log", "/var/log"},
		{"  '/var/log/'  ", "/var/log"},
		{`"/srv/a b"`, "/srv/a b"},
		{"logs", "/home/me/www/logs"},
		{"../x", "/home/me/x"},
		{"~", "/home/me"},
		{"~/tmp", "/home/me/tmp"},
		{`C:\Users\me`, "C:/Users/me"},
		{"  ", ""},
	} {
		if got := typedPath(tc.typed, "/home/me/www", "/home/me", true); got != tc.want {
			t.Errorf("remote %q: %q, want %q", tc.typed, got, tc.want)
		}
	}
	home := filepath.Join(string(filepath.Separator), "Users", "me")
	if got := typedPath("~/Desktop", filepath.Join(home, "x"), home, false); got != filepath.Join(home, "Desktop") {
		t.Errorf("local home: %q", got)
	}
	if got := typedPath("docs", home, home, false); got != filepath.Join(home, "docs") {
		t.Errorf("local relative: %q", got)
	}
}

// The breadcrumbs' button gives a field on the folder shown, selected,
// so that a path typed or pasted replaces it: Enter goes there, Escape
// gives the breadcrumbs back where they were.
func TestFilesTypePath(t *testing.T) {
	skipWindowsSFTP(t)
	a, tt, fc, root := filesPaneAs(t, false, nil)
	write(t, filepath.Join(root, "sub", "deep", "x.txt"), "x")
	s := &fc.fs.remote
	a.listRemote(s, root)
	waitFor(t, tt, "the root", func() bool { return s.dir == root && tt.HasText("sub") })
	tt.Frame()

	mustClick(t, tt, "Type a Path")
	tt.Frame()
	tt.Frame()
	if !s.typing || s.typed != root {
		t.Fatalf("field: typing %v on %q", s.typing, s.typed)
	}
	tt.Type(filepath.Join(root, "sub", "deep"))
	tt.Key(0, ui.KeyEnter)
	waitFor(t, tt, "deep", func() bool { return tt.HasText("x.txt") })
	if s.dir != filepath.Join(root, "sub", "deep") || s.typing {
		t.Fatalf("went to %q, typing %v", s.dir, s.typing)
	}

	mustClick(t, tt, "Type a Path")
	tt.Frame()
	tt.Frame()
	tt.Type("/nowhere")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if s.typing || s.dir != filepath.Join(root, "sub", "deep") {
		t.Errorf("Escape: typing %v in %q", s.typing, s.dir)
	}
}
