//go:build linux

package sftpsvc

import (
	"io/fs"
	"syscall"
)

func partStampOf(info fs.FileInfo) partStamp {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return partStamp{}
	}
	return partStamp{sec: st.Ctim.Sec, nsec: int64(st.Ctim.Nsec), ok: true}
}
