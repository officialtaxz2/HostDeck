//go:build !linux

package discover

import "errors"

func statfs(string) (total, free int64, err error) {
	return 0, 0, errors.New("nur unter Linux")
}
