//go:build !linux

// Package ohnelink öffnet Ordner ohne Verknüpfungen; das geht nur unter
// Linux.
package ohnelink

import "errors"

// ErrOld: geht nur unter Linux.
var ErrOld = errors.New("geht nur unter Linux")

// OpenDir gibt es nur unter Linux.
func OpenDir(dirfd int, name string, xdev bool) (int, error) { return -1, ErrOld }

// OpenParent gibt es nur unter Linux.
func OpenParent(root, host string, xdev bool) (int, string, string, error) {
	return -1, "", "/", ErrOld
}
