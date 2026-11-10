package updater

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// leftoverAge is how long a work directory of MyGo's updater must have
// been left alone before CleanLeftovers removes it: a younger one may be
// another instance's, installing an update right now.
const leftoverAge = time.Hour

// leftoverEntries are what a work directory may hold: the unpacked update
// and the archive or delta it came from.
var leftoverEntries = map[string]bool{"app": true, "update.tar.gz": true, "update.delta": true}

// CleanLeftovers removes the work directories that MyGo's updater makes
// next to the app directory, ".<app dir>.update-<digits>", and leaves
// behind when it cannot remove them: Windows keeps a file just written
// open while it scans it, and an install cut short by the app exiting
// keeps its directory too. MyGo only cleans what it leaves inside the app
// directory. macOS is left out: there a helper removes the directory
// once the app has exited, and its staged update waits in it until then.
func CleanLeftovers() {
	if runtime.GOOS == "darwin" || !mygo.Updater.Enabled() {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	// As MyGo finds the app directory it updates.
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	cleanLeftovers(filepath.Dir(exe), time.Now())
}

// cleanLeftovers removes the work directories of the app directory
// appDir that are leftovers at now.
func cleanLeftovers(appDir string, now time.Time) {
	parent := filepath.Dir(appDir)
	prefix := "." + filepath.Base(appDir) + ".update-"
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		// Not a symlink: DirEntry.IsDir does not follow one.
		if !e.IsDir() || !strings.HasPrefix(name, prefix) || !isDigits(name[len(prefix):]) {
			continue
		}
		dir := filepath.Join(parent, name)
		if isLeftover(dir, now) {
			_ = os.RemoveAll(dir)
		}
	}
}

// isLeftover reports whether dir holds only what the updater puts in a
// work directory and nothing in it changed for leftoverAge: a download
// still running keeps writing its archive.
func isLeftover(dir string, now time.Time) bool {
	top, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range top {
		if !leftoverEntries[e.Name()] || e.IsDir() != (e.Name() == "app") || e.Type()&fs.ModeSymlink != 0 {
			return false
		}
	}
	old := true
	err = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if now.Sub(info.ModTime()) < leftoverAge {
			old = false
			return filepath.SkipAll
		}
		return nil
	})
	return err == nil && old
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
