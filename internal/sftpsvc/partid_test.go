package sftpsvc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSamePartRejectsReusedInode covers a removed part whose inode number
// is given to the replacement. Filesystems that do not recycle it skip.
func TestSamePartRejectsReusedInode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "part")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := f.Stat()
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		// A fast recreate can keep the previous change time. Wait until
		// a reused inode has a stamp of its own.
		time.Sleep(20 * time.Millisecond)
		if err := os.WriteFile(path, []byte("new-file"), 0o600); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		a, b := partStampOf(saved), partStampOf(fi)
		if os.SameFile(saved, fi) && a.ok && b.ok && (a.sec != b.sec || a.nsec != b.nsec) {
			if samePart(saved, fi) {
				t.Fatal("replacement with a reused inode was accepted")
			}
			return
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	t.Skip("filesystem did not reuse the inode with a new change stamp")
}
