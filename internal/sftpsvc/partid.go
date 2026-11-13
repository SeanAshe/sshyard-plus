package sftpsvc

import (
	"io/fs"
	"os"
)

// partStamp is a file timestamp that a reused inode does not keep.
// Zero means the platform did not report one.
type partStamp struct {
	sec, nsec int64
	ok        bool
}

// samePart reports whether saved and now are the same part file.
// os.SameFile is dev and inode, which a new file can reuse after the
// part is removed; the change or creation stamp distinguishes that file.
func samePart(saved, now fs.FileInfo) bool {
	if saved == nil || now == nil || !os.SameFile(saved, now) {
		return false
	}
	a, b := partStampOf(saved), partStampOf(now)
	if !a.ok || !b.ok {
		return true
	}
	return a.sec == b.sec && a.nsec == b.nsec
}
