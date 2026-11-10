package updater

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeTree creates files (directories end in "/") under root, all last
// modified at mod.
func makeTree(t *testing.T, root string, mod time.Time, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if f[len(f)-1] == '/' {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := filepath.WalkDir(root, func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(p, mod, mod)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCleanLeftovers(t *testing.T) {
	now := time.Now()
	old := now.Add(-2 * leftoverAge)
	parent := t.TempDir()
	appDir := filepath.Join(parent, "sshyard")
	makeTree(t, appDir, old, "SSHYard.exe")

	removed := map[string][]string{
		".sshyard.update-123":  {"app/", "update.tar.gz"},
		".sshyard.update-456":  {"app/SSHYard.exe", "app/resources/icon.png", "update.delta"},
		".sshyard.update-789":  {},
		".sshyard.update-1011": {"update.tar.gz"},
	}
	kept := map[string][]string{
		".sshyard.update-abc":   {"app/", "update.tar.gz"},
		".sshyard.update-":      {"app/"},
		".other.update-123":     {"app/", "update.tar.gz"},
		".sshyard.update-12":    {"app/", "notes.txt"},
		".sshyard.update-13":    {"update.tar.gz/"},
		".sshyard.update-14":    {"app"},
		"sshyard.update-15":     {"app/"},
		".sshyard.update-16.bk": {"app/"},
	}
	for name, files := range removed {
		makeTree(t, filepath.Join(parent, name), old, append(files, "./")...)
	}
	for name, files := range kept {
		makeTree(t, filepath.Join(parent, name), old, append(files, "./")...)
	}
	// Still downloading: the directory is old, its archive is not.
	makeTree(t, filepath.Join(parent, ".sshyard.update-20"), old, "app/", "update.tar.gz")
	if err := os.Chtimes(filepath.Join(parent, ".sshyard.update-20", "update.tar.gz"), now, now); err != nil {
		t.Fatal(err)
	}
	kept[".sshyard.update-20"] = nil
	// Made just now by another instance.
	makeTree(t, filepath.Join(parent, ".sshyard.update-21"), now, "app/")
	kept[".sshyard.update-21"] = nil
	// A file, not a directory.
	if err := os.WriteFile(filepath.Join(parent, ".sshyard.update-22"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	kept[".sshyard.update-22"] = nil
	// A symlink to a directory that looks like a leftover.
	outside := filepath.Join(t.TempDir(), "data")
	makeTree(t, outside, old, "app/", "update.tar.gz")
	if err := os.Symlink(outside, filepath.Join(parent, ".sshyard.update-23")); err == nil {
		kept[".sshyard.update-23"] = nil
	}

	cleanLeftovers(appDir, now)

	for name := range removed {
		if _, err := os.Lstat(filepath.Join(parent, name)); !os.IsNotExist(err) {
			t.Errorf("%s was not removed: %v", name, err)
		}
	}
	for name := range kept {
		if _, err := os.Lstat(filepath.Join(parent, name)); err != nil {
			t.Errorf("%s was removed: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "update.tar.gz")); err != nil {
		t.Errorf("the symlink's target was touched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(appDir, "SSHYard.exe")); err != nil {
		t.Errorf("the app directory was touched: %v", err)
	}
}
