package dienst

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Lotse: Probelauf merken, „Passt so“ mit Haken, Neustart bei neuem Umzug.
func TestLotse(t *testing.T) {
	r, sid := modeRig(t, nil)
	d := r.d
	d.lotseRun(MovePlan, MoveTarget{Host: "198.51.100.20", Port: 22, User: "root", Password: "geheim"})
	code, b := r.api(t, "GET", "/api/umzug/lotse", sid, "")
	if code != 200 || !strings.Contains(b, `"ziel":"root@198.51.100.20"`) || strings.Contains(b, "geheim") {
		t.Fatalf("%d %s", code, b)
	}
	code, b = r.api(t, "POST", "/api/umzug/lotse", sid, `{"passt_so":true,"haken":["cron:b","cron:a"]}`)
	if code != 200 || !strings.Contains(b, `"passt_so_haken":["cron:a","cron:b"]`) || !strings.Contains(b, `"probelauf"`) {
		t.Fatalf("%d %s", code, b)
	}
	if code, _ := r.api(t, "POST", "/api/umzug/lotse", sid, `{"pc_kopie":"irgendwas"}`); code != 400 {
		t.Fatalf("unbekannter Stand: %d", code)
	}
	// Alltag, dann neuer Umzug: Der Lotse beginnt von vorn.
	if code, b := r.api(t, "POST", "/api/umzug/modus", sid, `{"geplant":false}`); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	if code, b := r.api(t, "POST", "/api/umzug/modus", sid, `{"geplant":true}`); code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	if _, b := r.api(t, "GET", "/api/umzug/lotse", sid, ""); b != "{}\n" && b != "{}" {
		t.Fatalf("nach neuem Umzug: %s", b)
	}
}

// Schritt 10: Abschluss auf dem neuen Server.
func TestUmzugAbschluss(t *testing.T) {
	test := []string{"hostdeck-neu.beispiel.de"}
	r, sid := modeRig(t, func(d *Dienst) {
		d.FinishFile = filepath.Join(t.TempDir(), "umzug-abschluss.json")
		d.TestAddresses = func() []string { return test }
	})
	d := r.d
	if d.Status().AbschlussOffen {
		t.Fatal("ohne Datei offen")
	}
	if code, _ := r.api(t, "POST", "/api/umzug/abschluss", sid, `{"punkt":"tabs","erledigt":true}`); code != 404 {
		t.Fatalf("ohne Datei: %d", code)
	}
	f := NewFinish("root@203.0.113.1", "hostdeck-neu.beispiel.de", d.now())
	bs, _ := json.Marshal(f)
	os.WriteFile(d.FinishFile, bs, 0o600)
	if !d.Status().AbschlussOffen {
		t.Fatal("nicht offen")
	}
	// Testadresse erst abhaken, wenn HostDeck sie aus Caddy genommen hat.
	if code, b := r.api(t, "POST", "/api/umzug/abschluss", sid, `{"punkt":"testadresse","erledigt":true}`); code != 409 || !strings.Contains(b, "noch nicht aus Caddy") {
		t.Fatalf("%d %s", code, b)
	}
	test = nil
	for _, k := range []string{"tabs", "backup", "testadresse", "ttl"} {
		if code, b := r.api(t, "POST", "/api/umzug/abschluss", sid, `{"punkt":"`+k+`","erledigt":true}`); code != 200 {
			t.Fatalf("%s: %d %s", k, code, b)
		}
	}
	if !d.Status().AbschlussOffen {
		t.Fatal("Reserve fehlt noch, aber nicht mehr offen")
	}
	if code, b := r.api(t, "POST", "/api/umzug/abschluss", sid, `{"punkt":"reserve","erledigt":true}`); code != 200 || !strings.Contains(b, `"offen":false`) {
		t.Fatalf("%d %s", code, b)
	}
	if d.Status().AbschlussOffen {
		t.Fatal("alles erledigt, trotzdem offen")
	}
	// Ohne Testadresse gibt es den Punkt nicht; Wegklicken schließt ab.
	f = NewFinish("", "", d.now())
	bs, _ = json.Marshal(f)
	os.WriteFile(d.FinishFile, bs, 0o600)
	_, b := r.api(t, "GET", "/api/umzug/abschluss", sid, "")
	if strings.Contains(b, `"testadresse"`) || !strings.Contains(b, `"offen":true`) {
		t.Fatalf("%s", b)
	}
	if code, b := r.api(t, "POST", "/api/umzug/abschluss", sid, `{"alle_weg":true}`); code != 200 || !strings.Contains(b, `"offen":false`) {
		t.Fatalf("%d %s", code, b)
	}
	// Nach der Reserve-Woche ist von selbst Ruhe.
	f = NewFinish("", "", d.now().AddDate(0, 0, -8))
	bs, _ = json.Marshal(f)
	os.WriteFile(d.FinishFile, bs, 0o600)
	if d.Status().AbschlussOffen {
		t.Fatal("nach der Reserve-Woche noch offen")
	}
}

// Labor S3: Nach einem vollständigen Download ist „Erstes Backup hier
// erstellen und herunterladen“ erledigt, ohne dass du es abhakst.
func TestAbschlussBackupNachDownload(t *testing.T) {
	r, sid := modeRig(t, func(d *Dienst) {
		d.FinishFile = filepath.Join(t.TempDir(), "umzug-abschluss.json")
	})
	d := r.d
	bs, _ := json.Marshal(NewFinish("root@203.0.113.1", "", d.now()))
	os.WriteFile(d.FinishFile, bs, 0o600)
	d.fetchedComplete("etag-1", "hostdeck-backup-neu.tar", "203.0.113.9", "")
	_, b := r.api(t, "GET", "/api/umzug/abschluss", sid, "")
	if !strings.Contains(b, `"backup":"`) {
		t.Fatalf("Backup-Punkt nach dem Download nicht erledigt: %s", b)
	}
	// Ohne Abschluss (Alltag) bleibt alles, wie es ist.
	os.Remove(d.FinishFile)
	d.fetchedComplete("etag-2", "hostdeck-backup-neu.tar", "203.0.113.9", "")
	if _, err := os.Stat(d.FinishFile); err == nil {
		t.Error("Abschluss-Datei ohne Umzug angelegt")
	}
}
