//go:build !unix

package leistungstest

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

// Den Leistungstest gibt es nur auf Linux-Servern.
func runGroup(context.Context, *exec.Cmd, time.Duration) error {
	return errors.New("der Leistungstest läuft nur auf einem Linux-Server")
}
