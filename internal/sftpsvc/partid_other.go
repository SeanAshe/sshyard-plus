//go:build !linux && !darwin && !windows

package sftpsvc

import "io/fs"

func partStampOf(fs.FileInfo) partStamp { return partStamp{} }
