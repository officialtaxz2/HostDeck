//go:build !linux

package aufraeumen

import (
	"errors"
	"os"
)

func placeBack(src, dst string, merge bool) (int, error) {
	return 0, errors.New("Zurückholen geht nur unter Linux")
}

func createNew(dst string, mode os.FileMode) (*os.File, func(bool), error) {
	return nil, nil, errors.New("Zurückholen geht nur unter Linux")
}
