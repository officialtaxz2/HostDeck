package restore

// Regressionstest zu einem früheren Fund, seit dem Fix ein
// Regressionstest.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/backup"
)

// H1: Auffrischen mit einem letzten Lauf (Umzugstag nach der Generalprobe).
// Die Datenbank liegt als Ordner im Projekt (Bind-Mount, etwa
// /opt/calagopus/postgres). Ihre Rohdateien kommen über die Dateien der
// Probe-Kopie, ohne dass HostDeck den Ordner vorher leert: Was die laufende
// Probe-Kopie dort geschrieben hat (WAL, pg_xact, postmaster.pid …), bleibt
// neben den Dateien vom alten Server liegen. Beim Volume leert HostDeck vorher.
func TestReview2RohdateienImOrdnerBeimAuffrischen(t *testing.T) {
	e := newJobEnv(t, "richtig", "ja")
	lauf := "/root/.hostdeck/laeufe/20261101-100000"
	shop := e.store.composeDir
	pg := filepath.Join(shop, "pg")
	var rf backup.RunFile
	if err := json.Unmarshal([]byte(e.store.files["lauf1|"+lauf+"/lauf.json"]), &rf); err != nil {
		t.Fatal(err)
	}
	rf.Final = true
	rf.Groups[0].Dumps = []backup.Item{{Key: "path:" + pg, ContainerName: "shop-db-1"}}
	var snaps []backup.RunSnapshot
	for _, s := range rf.Snapshots {
		if s.Key == "volume:shop_pg" {
			continue
		}
		snaps = append(snaps, s)
	}
	snaps = append(snaps,
		backup.RunSnapshot{Key: "path:" + pg, Group: "shop", Strategy: backup.StratDump, Snapshot: "s4", Filename: "dumps/shop/volume-shop_pg.sql", Engine: "postgres"},
		backup.RunSnapshot{Key: "path:" + pg, Group: "shop", Strategy: backup.StratRaw, Snapshot: "s5", Paths: []string{pg}})
	rf.Snapshots = snaps
	b, _ := json.Marshal(rf)
	e.store.files["lauf1|"+lauf+"/lauf.json"] = string(b)

	// Die Generalprobe hat das Projekt samt Datenbank-Ordner hier angelegt,
	// die Kopie lief und hat eigene Dateien geschrieben.
	os.MkdirAll(filepath.Join(pg, "pg_wal"), 0o755)
	stale := filepath.Join(pg, "pg_wal", "000000010000000000000099")
	os.WriteFile(stale, []byte("von der Probe-Kopie"), 0o600)
	e.docker.created = true
	e.docker.running["shop-db"] = true
	prev := Record{Run: "20261031-100000", Source: "alt",
		Groups: []GroupRecord{{Name: "shop", Compose: true, Dir: shop, Paths: []string{shop, pg}}}}
	pb, _ := json.Marshal(prev)
	os.MkdirAll(e.job.StateDir, 0o700)
	os.WriteFile(RecordPath(e.job.StateDir), pb, 0o600)

	e.job.Run(context.Background())
	if _, err := os.Stat(stale); err == nil {
		t.Errorf("Rohdateien der Datenbank über den Ordner der Probe-Kopie gespielt, ohne ihn zu leeren; %s liegt noch da.\nRestore: %q\n%s",
			stale, e.store.calls, review2Tail(e.out.String(), 25))
	}
}

func review2Tail(s string, n int) string {
	l := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, "\n")
}

// P1: Ist der Umzug hier abgeschlossen, spielt HostDeck nichts mehr darüber
// (kein Auffrischen eines echten Servers aus dem alten Stand).
func TestReview2KeinAuffrischenNachUmzug(t *testing.T) {
	e := newJobEnv(t, "richtig", "ja")
	at := time.Date(2026, 11, 5, 20, 0, 0, 0, time.UTC)
	prev := Record{Run: "20261031-100000", Source: "alt", Adopted: &at}
	pb, _ := json.Marshal(prev)
	os.MkdirAll(e.job.StateDir, 0o700)
	os.WriteFile(RecordPath(e.job.StateDir), pb, 0o600)
	if code := e.job.Run(context.Background()); code != ExitBlocked || !strings.Contains(e.out.String(), "echte Server") {
		t.Errorf("Code %d\n%s", code, review2Tail(e.out.String(), 10))
	}
	if len(e.store.calls) != 0 {
		t.Errorf("trotzdem zurückgespielt: %v", e.store.calls)
	}
}
