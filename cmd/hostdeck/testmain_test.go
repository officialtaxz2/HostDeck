package main

import (
	"os"
	"testing"
)

// Tests nehmen die Laufsperre in einem eigenen Ordner und sehen nie einen
// echten Dienst auf dem Rechner, auf dem sie laufen.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sbm-lauf")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOSTDECK_RUN_DIR", dir)
	serviceResultDir = dir + "/kein-dienst"
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
