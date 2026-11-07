package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A save a cut left undone shows at the top of the Files pane, its name
// escaped and quoted; nothing is put back until Restore, and Discard
// removes the backup.
func TestUnfinishedSaveNotice(t *testing.T) {
	a, tt, fc, root := filesPane(t)
	name := "a\u202eb\nc.conf"
	notice := "“a\\u202eb\\nc.conf”’s last save didn’t finish; it can be restored"
	if runtime.GOOS == "windows" {
		// Windows forbids a newline in a file name. The bidi control is
		// allowed, and the notice still escapes it.
		name = "a\u202eb.conf"
		notice = "“a\\u202eb.conf”’s last save didn’t finish; it can be restored"
	}
	backup := ".sshyard-old-0123456789ab-" + name
	write(t, filepath.Join(root, backup), "old")
	a.refreshSides(fc)
	waitFor(t, tt, "notice", func() bool { return tt.HasText(notice) })
	if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
		t.Fatal("the listing put the file back")
	}
	mustClick(t, tt, "Restore")
	waitFor(t, tt, "restored", func() bool { return !tt.HasText(notice) && read(filepath.Join(root, name)) == "old" })
	if _, err := os.Lstat(filepath.Join(root, backup)); err == nil {
		t.Fatal("the backup is left")
	}

	write(t, filepath.Join(root, ".sshyard-old-0123456789ab-d.conf"), "old d")
	a.refreshSides(fc)
	waitFor(t, tt, "notice", func() bool { return tt.HasText("“d.conf”’s last save didn’t finish; it can be restored") })
	mustClick(t, tt, "Discard")
	waitFor(t, tt, "discarded", func() bool { return !tt.HasText("“d.conf”’s last save didn’t finish; it can be restored") })
	for _, p := range []string{".sshyard-old-0123456789ab-d.conf", "d.conf"} {
		if _, err := os.Lstat(filepath.Join(root, p)); err == nil {
			t.Fatalf("%s left", p)
		}
	}

	// Uploaded again since the listing: Restore replaces nothing.
	write(t, filepath.Join(root, ".sshyard-old-0123456789ab-e.conf"), "old e")
	a.refreshSides(fc)
	waitFor(t, tt, "notice", func() bool { return tt.HasText("“e.conf”’s last save didn’t finish; it can be restored") })
	write(t, filepath.Join(root, "e.conf"), "new e")
	mustClick(t, tt, "Restore")
	waitFor(t, tt, "error", func() bool { return strings.Contains(fc.fs.remote.err, "is there again") })
	if read(filepath.Join(root, "e.conf")) != "new e" || read(filepath.Join(root, ".sshyard-old-0123456789ab-e.conf")) != "old e" {
		t.Fatal("replaced")
	}
}
