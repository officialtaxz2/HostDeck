package sitzung

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUeberstehtNeustartOhneKennungImKlartext(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sitzungen.json")
	now := time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	a := New(file)
	a.Now = clock
	id, err := a.NewSession(ViaPassword, "Notausgang", "Notausgang")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(file)
	if strings.Contains(string(b), id) {
		t.Fatal("Kennung steht im Klartext in der Datei")
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
		t.Errorf("Rechte: %v", fi.Mode().Perm())
	}
	c := New(file)
	c.Now = clock
	i, err := c.Check(id, true)
	if err != nil || i.Via != ViaPassword || i.Gate != "Notausgang" {
		t.Fatalf("nach Neustart: %+v %v", i, err)
	}
}

func TestBestaetigungGiltFuenfMinuten(t *testing.T) {
	now := time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC)
	s := New("")
	s.Now = func() time.Time { return now }
	pw, _ := s.NewSession(ViaPassword, "g", "a")
	link, _ := s.NewSession(ViaLink, "g", "a")
	i, _ := s.Check(pw, true)
	if s.Confirmed(i) {
		t.Fatal("ohne Bestätigung bestätigt")
	}
	if j, _ := s.Check(link, true); !s.Confirmed(j) {
		t.Fatal("Link-Anmeldung gilt als bestätigt")
	}
	if _, err := s.MarkConfirmed(pw); err != nil {
		t.Fatal(err)
	}
	now = now.Add(4 * time.Minute)
	if i, _ = s.Check(pw, true); !s.Confirmed(i) {
		t.Fatal("nach 4 Minuten nicht mehr bestätigt")
	}
	now = now.Add(2 * time.Minute)
	if i, _ = s.Check(pw, true); s.Confirmed(i) {
		t.Fatal("nach 6 Minuten noch bestätigt")
	}
}

func TestAblaufUndHoechstzahl(t *testing.T) {
	now := time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC)
	s := New("")
	s.Now = func() time.Time { return now }
	first, _ := s.NewSession(ViaLink, "g", "a")
	for i := 0; i < Max; i++ {
		now = now.Add(time.Second)
		if _, err := s.NewSession(ViaLink, "g", "a"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Check(first, false); err == nil {
		t.Fatal("älteste Anmeldung nicht verdrängt")
	}
	if s.Count() != Max {
		t.Fatalf("Anzahl %d", s.Count())
	}
	now = now.Add(31 * time.Minute)
	if s.Count() != 0 {
		t.Fatal("nach 31 Minuten ohne Aktivität noch angemeldet")
	}
	if n := s.DropAll(); n != 0 {
		t.Fatalf("DropAll: %d", n)
	}
}
