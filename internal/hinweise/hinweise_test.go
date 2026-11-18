package hinweise

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/betrieb"
	"github.com/officialtaxz2/hostdeck/internal/domains"
	"github.com/officialtaxz2/hostdeck/internal/sicherheit"
)

func keys(r Reply) string {
	var k []string
	for _, h := range r.Hints {
		k = append(k, h.Key+"="+h.Level)
	}
	return strings.Join(k, ",")
}

func TestNichtsZuMelden(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	until := now.Add(60 * 24 * time.Hour)
	s := &Service{
		Now:    func() time.Time { return now },
		Disk:   func(string) (int64, int64, error) { return 200e9, 100e9, nil },
		Memory: func() (int64, int64) { return 8e9, 3e9 },
		Containers: func(context.Context) (betrieb.Snapshot, error) {
			return betrieb.Snapshot{Containers: []betrieb.Container{{Name: "a", Running: true, State: "running"}}}, nil
		},
		Sites: func(context.Context) []domains.Site {
			return []domains.Site{{Hosts: []string{"a.de"}, Live: &domains.Live{CertUntil: &until}}}
		},
		Security: func(context.Context) sicherheit.Overview { return sicherheit.Overview{} },
	}
	if r := s.Hints(context.Background()); len(r.Hints) != 0 {
		t.Errorf("Hinweise: %+v", r.Hints)
	}
}

func TestAlleHinweise(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	soon, later, gone := now.Add(3*24*time.Hour), now.Add(10*24*time.Hour), now.Add(-time.Hour)
	recent := now.Add(-2 * time.Minute)
	restarts := 1
	s := &Service{
		Now:       func() time.Time { return now },
		MaxAge:    time.Nanosecond,
		DiskPaths: []string{"/", "/var/lib/docker", "/srv"},
		Disk: func(p string) (int64, int64, error) {
			switch p {
			case "/srv":
				return 100e9, 15e9, nil // 85 %
			}
			return 200e9, 4e9, nil // gleiche Platte, fast voll
		},
		Memory: func() (int64, int64) { return 8e9, 6e8 },
		Containers: func(context.Context) (betrieb.Snapshot, error) {
			return betrieb.Snapshot{Containers: []betrieb.Container{
				{Name: "kaputt", State: "restarting"},
				{Name: "frisch", Running: true, State: "running", Restarts: 5, Since: &recent},
				{Name: "zaehlt", Running: true, State: "running", Restarts: restarts},
				{Name: "ruhig", Running: true, State: "running", Restarts: 9},
			}}, nil
		},
		Sites: func(context.Context) []domains.Site {
			return []domains.Site{
				{Hosts: []string{"a.de"}, Live: &domains.Live{CertUntil: &later}},
				{Hosts: []string{"b.de"}, Live: &domains.Live{CertUntil: &soon}},
				{Hosts: []string{"c.de"}, Live: &domains.Live{CertUntil: &gone}},
				{Hosts: []string{"aus.de"}, Kind: domains.KindOff, Live: &domains.Live{CertUntil: &gone}},
			}
		},
		Security: func(context.Context) sicherheit.Overview {
			return sicherheit.Overview{
				Firewall: sicherheit.Firewall{OwnLost: true},
				Pending:  &sicherheit.Pending{What: "Schließen: 3552/tcp (arcane)"},
				Ports:    []sicherheit.Port{{Proto: "tcp", From: 3552, To: 3552, Owner: "arcane", Suggest: true, Risk: "hoch"}, {Proto: "tcp", From: 5432, Owner: "postgres", Suggest: true, Risk: "hoch", Closed: true}},
			}
		},
	}
	r := s.Hints(context.Background())
	want := "platz:/=err,neustarts=err,zertifikat=err,schutz-verloren=err,platz:/srv=warn,speicher=warn,schutz-wartet=warn,ports=warn"
	if got := keys(r); got != want {
		t.Fatalf("Hinweise:\n%s\nwill\n%s", got, want)
	}
	for _, h := range r.Hints {
		switch h.Key {
		case "neustarts":
			if h.Title != "2 Container starten ständig neu" || !strings.HasPrefix(h.Text, "frisch, kaputt.") {
				t.Errorf("Neustarts: %+v", h)
			}
		case "zertifikat":
			if !strings.Contains(h.Text, "c.de (abgelaufen), b.de (in 3 Tagen), a.de (in 10 Tagen)") || strings.Contains(h.Text, "aus.de") {
				t.Errorf("Zertifikate: %s", h.Text)
			}
		case "platz:/":
			if !strings.Contains(h.Text, "98 % belegt, noch 4,0 GB frei") {
				t.Errorf("Platz: %s", h.Text)
			}
		case "ports":
			if !strings.HasPrefix(h.Text, "3552/tcp (arcane) ist") {
				t.Errorf("Ports: %s", h.Text)
			}
		}
	}
	// zaehlt steigt binnen einer Stunde um 3: auch das ist eine Schleife.
	now = now.Add(20 * time.Minute)
	restarts = 4
	r = s.Hints(context.Background())
	for _, h := range r.Hints {
		if h.Key == "neustarts" && !strings.Contains(h.Text, "zaehlt") {
			t.Errorf("zaehlt fehlt: %s", h.Text)
		}
	}
	// Nach mehr als einer Stunde ohne neue Neustarts: wieder ruhig.
	now = now.Add(2 * time.Hour)
	r = s.Hints(context.Background())
	for _, h := range r.Hints {
		if h.Key == "neustarts" && strings.Contains(h.Text, "zaehlt") {
			t.Errorf("zaehlt noch da: %s", h.Text)
		}
	}
}

func TestZwischenspeicher(t *testing.T) {
	n := 0
	now := time.Now()
	s := &Service{Now: func() time.Time { return now }, Memory: func() (int64, int64) { n++; return 8e9, 1e8 }}
	s.Hints(context.Background())
	s.Hints(context.Background())
	if n != 1 {
		t.Errorf("gemessen: %d", n)
	}
	now = now.Add(31 * time.Second)
	s.Hints(context.Background())
	if n != 2 {
		t.Errorf("nicht neu gemessen: %d", n)
	}
}

// Mit Warnungen der Überwachung: Platte und Speicher kommen nur noch von
// dort; Hinweise zu den Ports bleiben, ohne doppelte Einträge.
func TestMitWarnungen(t *testing.T) {
	s := &Service{
		Disk:   func(string) (int64, int64, error) { return 100e9, 1e9, nil },
		Memory: func() (int64, int64) { return 8e9, 1e8 },
		Alerts: func() []Hint {
			return []Hint{{Key: "seite:a.de", Level: Warn, Title: "a"}, {Key: "schutz-verloren", Level: Err, Title: "b"}}
		},
		Security: func(context.Context) sicherheit.Overview {
			return sicherheit.Overview{Firewall: sicherheit.Firewall{OwnLost: true}, Pending: &sicherheit.Pending{What: "Port 8080 schließen"}}
		},
	}
	r := s.Hints(context.Background())
	var keys []string
	for _, h := range r.Hints {
		keys = append(keys, h.Key)
	}
	if got := strings.Join(keys, ","); got != "schutz-verloren,seite:a.de,schutz-wartet" {
		t.Errorf("Hinweise: %s", got)
	}
}

// Bewusst so gelassene Warnungen stehen getrennt und zählen nirgends mit.
func TestSoGelassenGetrennt(t *testing.T) {
	at := time.Now()
	s := &Service{Alerts: func() []Hint {
		return []Hint{{Key: "steal", Level: Warn, Title: "Der Hoster teilt den Prozessor knapp zu", CanKeep: true, Kept: &at},
			{Key: "aus:bot", Level: Err, Title: "bot ist unerwartet aus", CanKeep: true}}
	}}
	r := s.Hints(context.Background())
	if len(r.Hints) != 1 || r.Hints[0].Key != "aus:bot" || len(r.Kept) != 1 || r.Kept[0].Key != "steal" {
		t.Fatalf("Hinweise %+v, so gelassen %+v", r.Hints, r.Kept)
	}
	s.Forget()
	if s.last != nil {
		t.Fatal("Forget behält den Stand")
	}
}
