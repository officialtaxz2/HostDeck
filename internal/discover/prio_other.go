//go:build !linux

package discover

import (
	"io/fs"
	"syscall"
)

func lowerPriority() {}

func diskUsage(fi fs.FileInfo) (size int64, dev, ino uint64, links uint64) {
	return fi.Size(), 0, 0, 1
}

func runAsOwner(string) *syscall.SysProcAttr { return nil }
