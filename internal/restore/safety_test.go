package restore

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/backup"
)

// Teile dieser Tests sichern frühere Funde ab.

// finalRun macht aus dem Testlauf einen letzten Lauf mit Rohdateien der
// Datenbank und lässt das Projekt auf dem Ziel schon laufen.
func finalRun(t *testing.T, e *jobEnv, pgContent bool) string {
	t.Helper()
	lauf := "/root/.hostdeck/laeufe/20261101-100000"
	var rf backup.RunFile
	if err := json.Unmarshal([]byte(e.store.files["lauf1|"+lauf+"/lauf.json"]), &rf); err != nil {
		t.Fatal(err)
	}
	rf.Final = true
	rf.Snapshots = append(rf.Snapshots, backup.RunSnapshot{Key: "volume:shop_pg", Group: "shop", Strategy: backup.StratRaw,
		Snapshot: "s5", Paths: []string{"/var/lib/docker/volumes/shop_pg/_data"}})
	b, _ := json.Marshal(rf)
	e.store.files["lauf1|"+lauf+"/lauf.json"] = string(b)
	pgdir := filepath.Join(e.root, "volumes", "shop_pg")
	os.MkdirAll(pgdir, 0o755)
	if pgContent {
		os.WriteFile(filepath.Join(pgdir, "PG_VERSION"), []byte("16"), 0o644)
	}
	e.docker.volumes["shop_pg"] = pgdir
	e.docker.created = true
	e.docker.running["shop-db"] = true
	e.docker.running["shop-web"] = true
	return pgdir
}

func TestRestoreRohdateienNichtInVolumeMitDatenbank(t *testing.T) {
	e := newJobEnv(t, "richtig", "überschreiben")
	finalRun(t, e, true)
	if code := e.job.Run(context.Background()); code != ExitFailed {
		t.Fatalf("Exit %d:\n%s", code, e.out)
	}
	for _, c := range e.store.calls {
		if strings.HasPrefix(c, "restore s5 ") {
			t.Errorf("Rohdateien in ein Volume mit Datenbank geschrieben: %v", e.store.calls)
		}
	}
	if !strings.Contains(e.out.String(), "Volume shop_pg hat schon eine Datenbank") || !strings.Contains(e.out.String(), "stoppt sie vor dem Zurückspielen") {
		t.Errorf("Vorschau sagt nicht, was passiert:\n%s", e.out)
	}
	rec, err := LoadRecord(e.job.StateDir)
	if err != nil || len(rec.Groups[0].DBs) != 1 || !rec.Groups[0].DBs[0].Failed {
		t.Fatalf("Protokoll: %+v %v", rec, err)
	}
}

func TestRestoreStopptLaufendesProjektVorher(t *testing.T) {
	e := newJobEnv(t, "richtig", "überschreiben")
	finalRun(t, e, false)
	os.MkdirAll(e.store.composeDir, 0o755)
	os.WriteFile(filepath.Join(e.store.composeDir, "compose.yaml"), []byte("alt"), 0o644)
	e.job.Run(context.Background())
	stop, create := slices.Index(e.docker.calls, "stop shop-db shop-web"), slices.Index(e.docker.calls, "create shop")
	if stop < 0 || create < stop {
		t.Fatalf("Projekt nicht vor allem anderen gestoppt: %q", e.docker.calls)
	}
	if !slices.Contains(e.store.calls, "restore s5 /var/lib/docker/volumes/shop_pg/_data → "+filepath.Join(e.root, "volumes", "shop_pg")) {
		t.Errorf("Rohdateien ins leere Volume fehlen: %q", e.store.calls)
	}
}

func TestVerifyNachUebersprungenemDump(t *testing.T) {
	e := newJobEnv(t, "richtig", "ja")
	e.docker.preData = Counts{"shop/public.kunden": 7}
	if code := e.job.Run(context.Background()); code != ExitFailed {
		t.Fatalf("restore Exit %d", code)
	}
	var out bytes.Buffer
	if code := Verify(context.Background(), e.docker, e.job.StateDir, &out); code != ExitFailed ||
		!strings.Contains(out.String(), "✗ beim Zurückspielen: Volume shop_pg: die Datenbank enthält schon Daten") {
		t.Errorf("verify, Exit %d:\n%s", code, out.String())
	}
}

func TestRestoreFehlerBeimEinspielenZaehlen(t *testing.T) {
	e := newJobEnv(t, "richtig", "ja")
	e.docker.replayStderr = "psql:<stdin>:12: ERROR:  role \"shop\" already exists\n" +
		"psql:<stdin>:80: ERROR:  could not create unique index \"kunden_pkey\"\nDETAIL:  Key (id)=(1) is duplicated.\n"
	if code := e.job.Run(context.Background()); code != ExitFailed {
		t.Fatalf("Exit %d:\n%s", code, e.out)
	}
	rec, _ := LoadRecord(e.job.StateDir)
	g := rec.Groups[0]
	if len(g.DBs[0].Errors) != 1 || !strings.Contains(g.DBs[0].Errors[0], "kunden_pkey") || len(g.Errors) != 1 ||
		!strings.Contains(g.Errors[0], "1 Fehler beim Einspielen") {
		t.Errorf("Protokoll: %+v", g)
	}
}

func TestVerifyPrueftNichtLaufendeNeu(t *testing.T) {
	e := newJobEnv(t, "richtig", "ja")
	if code := e.job.Run(context.Background()); code != ExitOK {
		t.Fatalf("Exit %d:\n%s", code, e.out)
	}
	rec, _ := LoadRecord(e.job.StateDir)
	rec.Groups[0].NotRunning = []string{"web"} // lief kurz nach dem Zurückspielen nicht
	if err := e.job.saveRecord(rec); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := Verify(context.Background(), e.docker, e.job.StateDir, &out); code != ExitOK {
		t.Errorf("verify prüft den Dienst nicht neu, Exit %d:\n%s", code, out.String())
	}
}
