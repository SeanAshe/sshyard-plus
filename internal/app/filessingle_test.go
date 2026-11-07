package app

import (
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/sftpsvc"
	"github.com/Licoy/sshyard/locales"
)

// A files pane shows the server's files alone, with their uploads and
// downloads in one row with the breadcrumbs; its header's toggle shows the
// local side beside them, for every pane, and remembers it.
func TestFilesSingleByDefault(t *testing.T) {
	skipWindowsSFTP(t)
	a, tt, fc, root := filesPaneAs(t, false, nil)
	write(t, filepath.Join(root, "a.txt"), "a")
	a.refreshSides(fc)
	waitFor(t, tt, "listing", func() bool { return tt.HasText("a.txt") })
	if fc.fs.local.loaded {
		t.Error("the local side listed")
	}
	for _, s := range []string{"Show Local Files", "Upload Files…", "Upload Folder…", "Download To…"} {
		if _, ok := tt.Find(s); !ok {
			t.Errorf("no %q in %q", s, tt.Texts())
		}
	}
	if _, ok := tt.Find("Download Selected File"); ok {
		t.Error("downloads beside a local side")
	}

	mustClick(t, tt, "Show Local Files")
	waitFor(t, tt, "local side", func() bool { return fc.fs.local.loaded && tt.HasText("Download Selected File") })
	if _, ok := tt.Find("Upload Files…"); ok {
		t.Error("uploads from a dialog beside the local side")
	}
	if saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json")); err != nil || !saved.FilesDual {
		t.Errorf("saved %+v, %v", saved, err)
	}
	mustClick(t, tt, "Show Local Files")
	tt.Frame()
	if a.settings.FilesDual {
		t.Error("still dual")
	}
	if _, ok := tt.Find("Download To…"); !ok {
		t.Errorf("no Download To… in %q", tt.Texts())
	}
}

// With the server's files alone, uploads come from files or a folder
// chosen, and downloads go to a folder chosen, the Downloads folder first.
func TestFilesUploadAndDownloadTo(t *testing.T) {
	skipWindowsSFTP(t)
	a, tt, fc, root := filesPaneAs(t, false, nil)
	write(t, filepath.Join(root, "a.txt"), "remote a")
	src, dest := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "up.txt"), "up")
	write(t, filepath.Join(src, "dir", "in.txt"), "in")
	var mu sync.Mutex
	var asked []string
	a.p.PickFiles = func(title string) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, title)
		return []string{filepath.Join(src, "up.txt")}, nil
	}
	a.p.PickFolder = func(title, start string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, title+" in "+start)
		if title == "Choose Where to Download" {
			return dest, nil
		}
		return filepath.Join(src, "dir"), nil
	}
	a.refreshSides(fc)
	waitFor(t, tt, "listing", func() bool { return tt.HasText("a.txt") })

	mustClick(t, tt, "Upload Files…")
	waitFor(t, tt, "files uploaded", func() bool { return read(filepath.Join(root, "up.txt")) == "up" })
	mustClick(t, tt, "Upload Folder…")
	waitFor(t, tt, "folder uploaded", func() bool { return read(filepath.Join(root, "dir", "in.txt")) == "in" })
	mustClick(t, tt, "a.txt")
	tt.Frame()
	mustClick(t, tt, "Download To…")
	waitFor(t, tt, "downloaded", func() bool { return read(filepath.Join(dest, "a.txt")) == "remote a" })

	mu.Lock()
	defer mu.Unlock()
	want := []string{"Choose Files to Upload", "Choose a Folder to Upload in ", "Choose Where to Download in " + a.downloadsDir()}
	if !slices.Equal(asked, want) {
		t.Errorf("asked %q, want %q", asked, want)
	}
}

// A table shows more about its entries as it widens.
func TestTableColumns(t *testing.T) {
	perm := " perm"
	if runtime.GOOS == "windows" {
		perm = ""
	}
	for _, tc := range []struct {
		w      float32
		remote bool
		want   string
	}{
		{0, true, "name size"},
		{200, true, "name"},
		{300, true, "name size"},
		{500, true, "name size modified"},
		{700, true, "name size modified perm"},
		{700, false, "name size modified" + perm},
	} {
		if got := strings.Join(tableColumns(tc.w, tc.remote), " "); got != tc.want {
			t.Errorf("%v (remote %v): %q, want %q", tc.w, tc.remote, got, tc.want)
		}
	}
}

// Permissions show as the owner's and in octal, with the owner's name,
// the server's if it has one; the tooltip adds the group, and nothing a
// server names breaks the line.
func TestPermText(t *testing.T) {
	a, _ := newTestApp(t)
	m := locales.In("en")
	fs := &filesState{names: &idNames{
		users:  map[uint32]string{1000: "ubuntu"},
		groups: map[uint32]string{1000: "ubuntu", 27: "su\ndo"},
	}}
	for _, tc := range []struct {
		e            sftpsvc.Entry
		text, detail string
	}{
		{sftpsvc.Entry{Mode: 0o755, IDs: true, UID: 1000, GID: 1000}, "rwx (755/ubuntu)", "-rwxr-xr-x · Owner: ubuntu · Group: ubuntu"},
		{sftpsvc.Entry{Mode: 0o640, IDs: true, UID: 1001, GID: 27}, "rw- (640/1001)", "-rw-r----- · Owner: 1001 · Group: su\\ndo"},
		{sftpsvc.Entry{Mode: 0o600}, "rw- (600)", "-rw-------"},
	} {
		text, detail := a.permText(fs, true, tc.e, m)
		if text != tc.text || detail != tc.detail {
			t.Errorf("%v: %q, %q; want %q, %q", tc.e.Mode, text, detail, tc.text, tc.detail)
		}
	}
}

// A wide table shows when entries were modified and their permissions; a
// folder's size is calculated when asked, and asked again once the table
// has left the folder.
func TestFilesWideTableAndFolderSize(t *testing.T) {
	skipWindowsSFTP(t)
	a, tt, fc, root := filesPaneAs(t, false, nil)
	write(t, filepath.Join(root, "a.txt"), "a")
	write(t, filepath.Join(root, "sub", "one.bin"), strings.Repeat("x", 1000))
	write(t, filepath.Join(root, "sub", "two.bin"), strings.Repeat("y", 2000))
	a.refreshSides(fc)
	waitFor(t, tt, "listing", func() bool { return tt.HasText("a.txt") && tt.HasText("sub") })
	tt.Frame()
	for _, s := range []string{"Size", "Modified", "Permissions"} {
		if _, ok := tt.Find(s); !ok {
			t.Errorf("no %q column at %v in %q", s, fc.fs.remote.width, tt.Texts())
		}
	}
	if !tt.HasText("rw- (644/") {
		t.Errorf("no permissions in %q", tt.Texts())
	}

	mustClick(t, tt, "Calculate")
	waitFor(t, tt, "folder size", func() bool { return tt.HasText(humanSize(3000)) })
	if tt.HasText("Calculate") {
		t.Error("still asks")
	}
	dir := fc.fs.remote.dir
	a.listRemote(&fc.fs.remote, path.Join(dir, "sub"))
	waitFor(t, tt, "in the folder", func() bool { return tt.HasText("one.bin") })
	a.listRemote(&fc.fs.remote, dir)
	waitFor(t, tt, "back", func() bool { return tt.HasText("sub") && tt.HasText("Calculate") })
}
