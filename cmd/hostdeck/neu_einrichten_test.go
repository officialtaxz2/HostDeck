package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// noTTY ist ein Prompt ohne Terminal (install.sh --ja, Skripte).
type noTTY struct{ scriptPrompt }

func (noTTY) Interactive() bool { return false }

func TestCheckOlder(t *testing.T) {
	// Neuer oder gleich: keine Frage.
	for _, c := range [][2]string{{"1.0.0", "1.1.0"}, {"1.0.0-rc.1", "1.0.0"}, {"1.0.0", "1.0.0"}, {"", "1.0.0"}, {"e0-258afb6", "1.0.0"}} {
		p := &scriptPrompt{}
		if err := checkOlder(c[0], c[1], false, p); err != nil || p.out.Len() != 0 {
			t.Fatalf("%s -> %s: %v %q", c[0], c[1], err, p.out.String())
		}
	}
	// Älter ohne Terminal: abbrechen, außer ausdrücklich erlaubt.
	err := checkOlder("1.1.0", "1.0.0", false, &noTTY{})
	if err == nil || !strings.Contains(err.Error(), "ältere Version 1.0.0") || !strings.Contains(err.Error(), "--version 1.0.0") {
		t.Fatalf("ohne Terminal: %v", err)
	}
	if err := checkOlder("1.0.0", "1.0.0-rc.1", true, &noTTY{}); err != nil {
		t.Fatalf("erlaubt: %v", err)
	}
	// Älter am Terminal: Vorgabe ist nein.
	for answer, ok := range map[string]bool{"": false, "n": false, "nein": false, "j": true, " Ja": true} {
		p := &scriptPrompt{answers: []string{answer}}
		err := checkOlder("1.1.0", "1.0.0", false, p)
		if (err == nil) != ok {
			t.Fatalf("Antwort %q: %v", answer, err)
		}
		if !strings.Contains(p.out.String(), "Eingerichtet ist Version 1.1.0") {
			t.Fatalf("Frage fehlt: %q", p.out.String())
		}
	}
}

func TestInstalledVersion(t *testing.T) {
	ctx := context.Background()
	if v := installedVersion(ctx, "/gibt/es/nicht", func(context.Context) (string, error) { return "1.2.3", nil }); v != "1.2.3" {
		t.Fatalf("vom Dienst: %q", v)
	}
	// Dienst antwortet nicht: das Programm selbst fragen.
	prog := filepath.Join(t.TempDir(), "hostdeck")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\necho 'hostdeck 1.0.0-rc.1'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	down := func(context.Context) (string, error) { return "", errors.New("aus") }
	if v := installedVersion(ctx, prog, down); v != "1.0.0-rc.1" {
		t.Fatalf("vom Programm: %q", v)
	}
	if v := installedVersion(ctx, "/gibt/es/nicht", down); v != "" {
		t.Fatalf("unbekannt: %q", v)
	}
}
