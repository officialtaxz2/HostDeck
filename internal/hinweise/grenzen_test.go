package hinweise

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/domains"
	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
)

// T10: Hinweise bewerten Platte
// und Arbeitsspeicher mit denselben Grenzen wie die Regeln der Überwachung,
// auch nach „Grenzen ändern“; keine eigenen Zahlen.
func TestT10HinweiseNutzenGrenzen(t *testing.T) {
	s := &Service{
		Disk:   func(string) (int64, int64, error) { return 100e9, 40e9, nil }, // 60 % belegt
		Memory: func() (int64, int64) { return 16e9, 4e9 },                     // 25 % frei
	}
	if h := s.Hints(context.Background()).Hints; len(h) != 0 {
		t.Fatalf("mit Voreinstellungen: %+v", h)
	}
	lim := ueberwachung.DefaultLimits
	lim.DiskWarn, lim.MemWarn = 0.5, 0.3
	s = &Service{Disk: s.Disk, Memory: s.Memory, Limits: func() ueberwachung.Limits { return lim }}
	got := map[string]string{}
	for _, h := range s.Hints(context.Background()).Hints {
		got[h.Key] = h.Level
	}
	if got["platz:/"] != Warn || got["speicher"] != Warn {
		t.Errorf("geänderte Grenzen: %v", got)
	}
}

// V3: Stehen zwei Domains in einem Caddy-Block, zählt das Zertifikat jeder
// Domain, nicht nur das der ersten.
func TestZertifikatJeDomain(t *testing.T) {
	now := time.Now()
	gut, bald := now.Add(60*24*time.Hour), now.Add(3*24*time.Hour)
	sites := []domains.Site{{Hosts: []string{"a.example", "b.example"},
		Live: &domains.Live{OK: true, CertUntil: &gut},
		Each: map[string]domains.Live{"a.example": {OK: true, CertUntil: &gut}, "b.example": {OK: true, CertUntil: &bald}}}}
	h := certs(sites, now, ueberwachung.DefaultLimits)
	if len(h) != 1 || h[0].Level != Err || !strings.Contains(h[0].Text, "b.example") || strings.Contains(h[0].Text, "a.example") {
		t.Errorf("Zertifikat je Domain: %+v", h)
	}
}
