//go:build !linux

package platz

import (
	"io/fs"
	"time"
)

func lowerPriority() {}

func changedAt(fi fs.FileInfo) time.Time { return fi.ModTime() }

func statOf(fi fs.FileInfo) (size int64, dev, ino, links uint64) {
	return fi.Size(), 0, 0, 1
}

// MountPoints gibt es nur unter Linux.
func MountPoints() []string { return nil }

// SwapFiles gibt es nur unter Linux.
func SwapFiles() []string { return nil }

// OpenFile ist eine Datei, die ein Prozess offen hält.
type OpenFile struct {
	Path    string
	Proc    string
	PID     int
	Deleted bool
	Size    int64
	Dev     uint64
}

// OpenFiles gibt es nur unter Linux.
func OpenFiles() []OpenFile { return nil }
