//go:build !unix

package main

import (
	"io/fs"
	"os"
)

func fileOwner(fs.FileInfo) (int, bool) { return 0, false }

func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag, perm)
}
