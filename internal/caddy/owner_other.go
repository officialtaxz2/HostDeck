//go:build windows

package caddy

import "io/fs"

func owner(fs.FileInfo) (uid, gid int) { return -1, -1 }
