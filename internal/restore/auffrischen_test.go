package restore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRestoreAuffrischen(t *testing.T) {
	e := newJobEnv(t, "richtig", "ja")
	// Die Kopie vom letzten Mal: Projektordner, Datenbank mit eigenen
	// Zeilen, das Image unter seiner ID hier.
	os.MkdirAll(e.store.composeDir, 0o755)
	os.WriteFile(filepath.Join(e.store.composeDir, "alt.txt"), []byte("x"), 0o644)
	pgdir := filepath.Join(e.root, "volumes", "shop_pg")
	os.MkdirAll(filepath.Join(pgdir, "base"), 0o755)
	os.WriteFile(filepath.Join(pgdir, "PG_VERSION"), []byte("17"), 0o644)
	e.docker.volumes["shop_pg"] = pgdir
	e.docker.created = true
	e.docker.running["shop-db"] = true
	e.docker.counts["shop-db"] = Counts{"shop/public.kunden": 7}
	e.docker.preData = Counts{"shop/public.kunden": 7}
	e.docker.images["sha256:hier"] = true
	prev := Record{Run: "20261101-100000", Source: "alt", ImageIDs: map[string][]string{"sha256:bbb": {"sha256:hier"}},
		Groups: []GroupRecord{{Name: "shop", Compose: true, Dir: e.store.composeDir, Paths: []string{e.store.composeDir}, Volumes: []string{"shop_pg"},
			DBs: []DBRecord{{Title: "Volume shop_pg", Service: "db", Replayed: true, From: "alt/20261101-100000"}}},
			{Name: "weg", Compose: true, Services: []string{"app"}, Ports: []int{9}, Paths: []string{"/opt/weg"}}}}
	b, _ := json.Marshal(prev)
	os.MkdirAll(e.job.StateDir, 0o700)
	os.WriteFile(RecordPath(e.job.StateDir), b, 0o600)

	if code := e.job.Run(context.Background()); code != ExitOK {
		t.Fatalf("Exit %d:\n%s", code, e.out)
	}
	out := e.out.String()
	for _, want := range []string{"Auffrischen\n", "Image bbb: schon hier, gleich wie im Backup",
		"Datenbank vom letzten Lauf (7 Zeilen) wird aus dem frischen Dump ersetzt", "✓ 2 Tabellen, 2 Zeilen, wie im Backup"} {
		if !strings.Contains(out, want) {
			t.Errorf("Ausgabe ohne %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Auf diesem Server liegt schon etwas") || strings.Contains(e.input.questions[len(e.input.questions)-1].Text, "überschreiben") {
		t.Errorf("Kopie von HostDeck als Konflikt gemeldet:\n%s", out)
	}
	if slices.Contains(e.docker.calls, "load") || !slices.Contains(e.docker.calls, "tag sha256:hier shop-web:lokal") || !slices.Contains(e.docker.calls, "einspielen shop-db") {
		t.Errorf("Docker: %q", e.docker.calls)
	}
	if _, err := os.Stat(filepath.Join(pgdir, "PG_VERSION")); err == nil {
		t.Error("alte Datenbank-Dateien liegen noch da")
	}
	if _, err := os.Stat(pgdir); err != nil {
		t.Error("Volume-Ordner selbst gelöscht")
	}
	rec, err := LoadRecord(e.job.StateDir)
	if err != nil || !slices.Equal(rec.ImageIDs["sha256:bbb"], []string{"sha256:hier"}) || !rec.Groups[0].DBs[0].Replayed {
		t.Fatalf("Protokoll: %+v %v", rec, err)
	}
	// Was diesmal nicht mitkam, kennt Entfernen weiter.
	if len(rec.Groups) != 2 || rec.Groups[1].Name != "weg" || len(rec.Groups[1].Services) > 0 || len(rec.Groups[1].Ports) > 0 || rec.Groups[1].Paths[0] != "/opt/weg" {
		t.Errorf("übernommen: %+v", rec.Groups)
	}
}

func TestAuffrischenFremdeDatenbankBleibt(t *testing.T) {
	// Das Volume steht nicht im letzten Lauf: Fremdes ersetzt HostDeck nie.
	e := newJobEnv(t, "richtig", "überschreiben")
	e.docker.preData = Counts{"shop/public.kunden": 7}
	prev := Record{Run: "x", Source: "alt", Groups: []GroupRecord{{Name: "anderes", Compose: true, Volumes: []string{"anderes_db"}}}}
	b, _ := json.Marshal(prev)
	os.MkdirAll(e.job.StateDir, 0o700)
	os.WriteFile(RecordPath(e.job.StateDir), b, 0o600)
	pgdir := filepath.Join(e.root, "volumes", "shop_pg")
	os.MkdirAll(pgdir, 0o755)
	os.WriteFile(filepath.Join(pgdir, "PG_VERSION"), []byte("17"), 0o644)
	e.docker.volumes["shop_pg"] = pgdir
	e.job.Run(context.Background())
	if !strings.Contains(e.out.String(), "Volume shop_pg hat schon eine Datenbank") || slices.Contains(e.docker.calls, "einspielen shop-db") {
		t.Errorf("Ausgabe:\n%s\n%q", e.out, e.docker.calls)
	}
	if _, err := os.Stat(filepath.Join(pgdir, "PG_VERSION")); err != nil {
		t.Error("fremde Datenbank geleert")
	}
}

func TestUnsafeDir(t *testing.T) {
	for d, ok := range map[string]bool{
		"/var/lib/docker/volumes/shop_pg/_data": true,
		"/srv/docker/volumes/x/_data":           true,
		"/opt/calagopus/postgres":               true,
		"/var/lib/docker":                       false,
		"/etc/postgres":                         false,
		"/opt":                                  false,
		"/home/demo":                            false,
		"relativ/pfad":                          false,
		"/var/lib/docker/overlay2/x/_data":      false,
	} {
		if got := unsafeDir(d) == ""; got != ok {
			t.Errorf("%s: %v", d, got)
		}
	}
}
