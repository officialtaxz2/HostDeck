//go:build !unix

package aktualisieren

import (
	"os"
	"syscall"
)

func ownerOf(os.FileInfo) *Owner { return nil }

func setOwner(**syscall.SysProcAttr, *Owner) {}
