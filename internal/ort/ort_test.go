package ort

import (
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/outdir"
)

func TestDatenordnerUeberall(t *testing.T) {
	if outdir.ServiceDataDir != DataDir {
		t.Fatalf("outdir kennt %s, ort sagt %s", outdir.ServiceDataDir, DataDir)
	}
}

func TestEigeneUnits(t *testing.T) {
	if !IsOwnUnit("hostdeck-dienst.service") || !IsOwnUnit("hostdeck-web.service") || IsOwnUnit("hostdeck-backup.service") {
		t.Fatal("IsOwnUnit")
	}
}
