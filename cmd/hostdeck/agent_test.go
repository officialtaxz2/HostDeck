// Regressionstests zu früheren Funden.
package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type dyingWriter struct{ n int }

func (d *dyingWriter) Write(p []byte) (int, error) {
	if d.n <= 0 {
		return 0, errors.New("broken pipe")
	}
	d.n--
	return len(p), nil
}

// Reißt die Verbindung ab, landet im Protokoll auf dem Server nichts mehr.
func TestProtokollNachVerbindungsabbruch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOSTDECK_STATE_DIR", dir)
	in := strings.NewReader(`{"v":1,"action":"dienste-starten"}` + "\n")
	agentCmd(context.Background(), in, &dyingWriter{n: 1})
	logs, _ := filepath.Glob(filepath.Join(dir, "protokolle", "*.log"))
	if len(logs) != 1 {
		t.Fatalf("Protokolle: %v", logs)
	}
	b, _ := os.ReadFile(logs[0])
	// Als root findet dienste-starten nichts zu tun, sonst lehnt es ab.
	// Beides geschieht nach dem Abbruch und muss im Protokoll stehen.
	want := "keine Container vermerkt"
	if os.Geteuid() != 0 {
		want = "dienste-starten braucht root"
	}
	if !strings.Contains(string(b), want) {
		t.Errorf("Ausgabe nach dem Abbruch fehlt im Protokoll (erwartet %q): %q", want, b)
	}
}
