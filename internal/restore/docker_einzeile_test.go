//go:build !windows

package restore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Labor S4: Docker schreibt den Grund in eine eigene Zeile. Die Fehlermeldung
// steht trotzdem auf einer Zeile, damit der Lotse sie ganz zeigt.
func TestDockerFehlerAufEinerZeile(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "docker")
	os.WriteFile(bin, []byte("#!/bin/sh\necho ' Image busybox Pulling '\necho 'error from registry: Data limit exceeded' >&2\nexit 1\n"), 0o755)
	err := (&CLI{Bin: bin}).runLog(context.Background(), "compose", "create")
	if err == nil || strings.Contains(err.Error(), "\n") || !strings.Contains(err.Error(), "Pulling · error from registry: Data limit exceeded") {
		t.Fatalf("%q", err)
	}
}
