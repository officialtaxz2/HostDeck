//go:build !unix

package serverebene

import "os"

func fileIDs(os.FileInfo) (uid, gid uint32, ok bool) { return 0, 0, false }

func chown(string, int, int) error { return nil }
