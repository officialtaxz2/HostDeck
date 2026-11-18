package sicherheit

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// Bewusst offen lassen (Designvorgabe Grundregel 4): Der
// Vorschlag verschwindet, die Firewall bleibt unberührt, und Schließen
// eines anderen Ports behält die Wahl.
func TestBewusstOffenLassen(t *testing.T) {
	s, fr := newRig(t)
	ctx := context.Background()
	if _, err := s.KeepOpen(ctx, "tcp/22", true); !errors.Is(err, ErrNothing) {
		t.Errorf("SSH ist kein Vorschlag: %v", err)
	}
	if _, err := s.KeepOpen(ctx, "tcp/9999", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("unbekannt: %v", err)
	}
	msg, err := s.KeepOpen(ctx, "tcp/5432", true)
	if err != nil || msg == "" {
		t.Fatalf("offen lassen: %q %v", msg, err)
	}
	if _, err := os.Stat(s.RuleFile); err == nil {
		t.Error("Firewall angefasst: Regeldatei geschrieben")
	}
	for _, c := range fr.calls {
		if strings.Contains(c, " -f ") || strings.Contains(c, "systemctl enable") || strings.Contains(c, "systemctl start") {
			t.Errorf("Firewall angefasst: %s", c)
		}
	}
	p := byKey(s.Overview(ctx))["tcp/5432"]
	if p.Suggest || p.KeptOpen == nil || p.Risk != "hoch" {
		t.Fatalf("nach offen lassen: %+v", p)
	}
	// Ein anderer Port wird geschlossen: Die Wahl bleibt in schutz.json.
	pl, err := s.Plan(ctx, Change{Close: []string{"tcp/3552"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(ctx, Change{Close: []string{"tcp/3552"}}, pl.Stand); err != nil {
		t.Fatal(err)
	}
	if p := byKey(s.Overview(ctx))["tcp/5432"]; p.KeptOpen == nil || p.Suggest {
		t.Fatalf("nach Schließen eines anderen Ports: %+v", p)
	}
	if _, err := s.KeepOpen(ctx, "tcp/5432", false); err != nil {
		t.Fatal(err)
	}
	if p := byKey(s.Overview(ctx))["tcp/5432"]; !p.Suggest || p.KeptOpen != nil {
		t.Fatalf("zurückgenommen: %+v", p)
	}
	if _, err := s.KeepOpen(ctx, "tcp/5432", false); !errors.Is(err, ErrNothing) {
		t.Errorf("zweimal zurück: %v", err)
	}
}
