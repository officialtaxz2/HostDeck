package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestTuningBefehl(t *testing.T) {
	var out, errb bytes.Buffer
	if code := tuningCmd(context.Background(), nil, nil, &out, &errb); code != 2 || !strings.Contains(errb.String(), "sudo hostdeck tuning anwenden PRESET") {
		t.Fatalf("ohne Befehl: %d %s", code, errb.String())
	}
	errb.Reset()
	if code := tuningCmd(context.Background(), []string{"turbo"}, nil, &out, &errb); code != 2 {
		t.Fatalf("unbekannt: %d", code)
	}
	errb.Reset()
	if code := tuningCmd(context.Background(), []string{"vorschau"}, nil, &out, &errb); code != 2 || !strings.Contains(errb.String(), "Preset fehlt") {
		t.Fatalf("vorschau ohne Preset: %d %s", code, errb.String())
	}
	if _, err := exec.LookPath("bash"); err == nil {
		out.Reset()
		if code := tuningCmd(context.Background(), []string{"hilfe"}, nil, &out, &errb); code != 0 || !strings.Contains(out.String(), "vps-tune 2.3 Lean – Performance-Tuning") {
			t.Fatalf("hilfe: %d %s %s", code, out.String(), errb.String())
		}
	}
	if os.Geteuid() != 0 {
		errb.Reset()
		if code := tuningCmd(context.Background(), []string{"status"}, nil, &out, &errb); code != 2 || !strings.Contains(errb.String(), "braucht root") {
			t.Fatalf("ohne root: %d %s", code, errb.String())
		}
	}
}
