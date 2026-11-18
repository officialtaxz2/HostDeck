//go:build !unix

package aufraeumen

import "os"

func owner(os.FileInfo) (uid, gid int) { return 0, 0 }
