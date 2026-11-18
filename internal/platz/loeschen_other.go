//go:build !linux

package platz

import (
	"context"
	"errors"
)

// Remove gibt es nur unter Linux.
func Remove(ctx context.Context, root string, s Sel) (Result, error) {
	return Result{}, errors.New("Löschen geht nur unter Linux")
}
