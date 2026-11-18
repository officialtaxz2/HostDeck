//go:build unix

package leistungstest

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

// runGroup startet cmd in einer eigenen Prozessgruppe. Endet ctx, bekommt
// die ganze Gruppe SIGTERM (wie Strg+C im Terminal): Das Skript stoppt
// seine Messungen, schreibt die Übersicht und packt die ZIP. Nach grace
// folgt SIGKILL.
func runGroup(ctx context.Context, cmd *exec.Cmd, grace time.Duration) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case err := <-done:
		return err
	case <-t.C:
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		return <-done
	}
}
