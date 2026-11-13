//go:build windows

package sftpsvc

import (
	"io/fs"
	"syscall"
)

func partStampOf(info fs.FileInfo) partStamp {
	st, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok || st == nil {
		return partStamp{}
	}
	return partStamp{sec: st.CreationTime.Nanoseconds(), ok: true}
}
