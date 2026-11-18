package backup_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/fixture"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/manifest"
)

// vps1 liefert Inventar und eine fertige Auswahl wie im Alltag: work
// und arcane weglassen, Spielserver-Backups weglassen, nichts offen.
func vps1(t *testing.T) (*inventory.Inventory, *manifest.Manifest) {
	t.Helper()
	inv := fixture.Beispiel(t, 2).Run(t)
	m := manifest.Propose(inv)
	m.Projects["work"].Decision, m.Projects["work"].Reason = "skip", "nicht gebraucht"
	m.Projects["arcane"].Decision, m.Projects["arcane"].Reason = "skip", "nicht gebraucht"
	m.Projects["mcserver"].Data["path:/opt/calagopus/wings/backups"] = &manifest.Data{Strategy: "skip", Reason: "nur aktueller Stand"}
	m.Unassigned["path:/minecraft-alt"].Strategy = "archive"
	if open := backup.OpenEntries(m); len(open) > 0 {
		t.Fatalf("noch offen: %v", open)
	}
	return inv, m
}

func group(p *backup.Plan, name string) *backup.Group {
	for i := range p.Groups {
		if p.Groups[i].Name == name {
			return &p.Groups[i]
		}
	}
	return nil
}

func TestPlanJederPfadGenauEinmal(t *testing.T) {
	inv, m := vps1(t)
	p := backup.Build(m, inv, backup.Options{Excludes: []string{"/root/.hostdeck"}})
	if len(p.Problems) > 0 {
		t.Fatalf("Probleme: %v", p.Problems)
	}
	seen := map[string]string{}
	for _, it := range p.Items() {
		for _, path := range it.Paths {
			if prev, ok := seen[path]; ok {
				t.Errorf("%s doppelt: %s und %s", path, prev, it.Key)
			}
			seen[path] = it.Key
		}
		// Kein Schritt sichert einen Pfad, den ein genauerer Eintrag besitzt.
		for _, other := range p.Items() {
			if other.Key == it.Key {
				continue
			}
			for _, op := range other.Paths {
				for _, path := range it.Paths {
					if strings.HasPrefix(op, path+"/") && !slices.Contains(it.Excludes, op) {
						t.Errorf("%s sichert %s mit, das %s gehört", it.Key, op, other.Key)
					}
				}
			}
		}
	}
	for _, skipped := range []string{"/opt/arcane", "/opt/docker/work", "/opt/calagopus/wings/backups"} {
		if _, ok := seen[skipped]; ok {
			t.Errorf("%s ist weggelassen, steht aber im Plan", skipped)
		}
	}
}

func TestPlanReihenfolgeUndStrategien(t *testing.T) {
	inv, m := vps1(t)
	p := backup.Build(m, inv, backup.Options{Excludes: []string{"/root/.hostdeck"}})
	if last := p.Groups[len(p.Groups)-1]; last.Name != "mcserver" || len(last.Managed) == 0 {
		t.Errorf("Panel nicht zuletzt: %s", last.Name)
	}
	pensum := group(p, "pensum")
	if pensum == nil || len(pensum.Dumps) != 1 || pensum.Dumps[0].Engine != "postgres" || pensum.Dumps[0].Container == "" {
		t.Fatalf("pensum: Dump fehlt: %+v", pensum)
	}
	if pensum.NeedsStop(false) {
		t.Error("pensum hat nur einen Dump und muss im normalen Lauf nicht stoppen")
	}
	if !pensum.NeedsStop(true) {
		t.Error("pensum muss im letzten Lauf stoppen")
	}
	if group(p, "work") != nil || group(p, "arcane") != nil {
		t.Error("weggelassene Projekte stehen im Plan")
	}
	mc := group(p, "mcserver")
	var wings *backup.Item
	for i := range mc.Stopped {
		if mc.Stopped[i].Key == "path:/opt/calagopus/wings" {
			wings = &mc.Stopped[i]
		}
	}
	if wings == nil || !slices.Contains(wings.Excludes, "/opt/calagopus/wings/backups") {
		t.Errorf("wings ohne Ausschluss der Backups: %+v", wings)
	}
	for _, it := range p.Archive {
		if it.Key == "path:/root" && !slices.Contains(it.Excludes, "/root/.hostdeck") {
			t.Errorf("/root ohne den Ordner von HostDeck: %v", it.Excludes)
		}
	}
	var caddy *backup.Item
	for i := range p.Archive {
		if p.Archive[i].Key == "host:caddy" {
			caddy = &p.Archive[i]
		}
	}
	// Caddy zieht samt Zertifikaten um.
	if caddy == nil || caddy.Archive || !slices.Contains(caddy.Paths, "/etc/caddy") || !slices.Contains(caddy.Paths, "/var/lib/caddy/.local/share/caddy") {
		t.Errorf("Caddy: %+v", caddy)
	}
	if len(p.Images) == 0 {
		t.Error("keine Images zum Sichern")
	}
	for _, img := range p.Images {
		if img.ImageID == "" || !strings.HasPrefix(img.Filename, "images/") {
			t.Errorf("Image unvollständig: %+v", img)
		}
	}
	var buf bytes.Buffer
	p.Write(&buf)
	for _, want := range []string{"Projekt für Projekt kurz stoppen", "Dump:", "Bewusst weggelassen", "Projekt work: nicht gebraucht", "Summe: etwa"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("Plan-Ausgabe ohne %q:\n%s", want, buf.String())
		}
	}
}

func TestLetzterLaufKopiertDatenbankDateienZusaetzlich(t *testing.T) {
	inv, m := vps1(t)
	p := backup.Build(m, inv, backup.Options{Final: true})
	pensum := group(p, "pensum")
	var raw bool
	for _, it := range pensum.Stopped {
		if it.Strategy == backup.StratRaw && it.Key == pensum.Dumps[0].Key {
			raw = true
		}
	}
	if !raw {
		t.Errorf("letzter Lauf ohne Rohkopie der Datenbank: %+v", pensum.Stopped)
	}
	if pensum.Stopped[0].Strategy != backup.StratFolder {
		t.Error("im letzten Lauf gehört der Projektordner in die Stopp-Phase")
	}
}

func TestOffeneEintraegeUndUnbekannteEngine(t *testing.T) {
	inv := fixture.Beispiel(t, 2).Run(t)
	m := manifest.Propose(inv)
	open := backup.OpenEntries(m)
	if !slices.Contains(open, "project:work") || !slices.Contains(open, "path:/minecraft-alt") {
		t.Errorf("offene Einträge: %v", open)
	}
	for k, d := range m.Projects["urls"].Data {
		if d.Engine == "redis" {
			m.Projects["urls"].Data[k].Strategy = "dump"
		}
	}
	p := backup.Build(m, inv, backup.Options{})
	found := false
	for _, pr := range p.Problems {
		if strings.Contains(pr, "keinen Dump") {
			found = true
		}
	}
	if !found {
		t.Errorf("Dump für Redis nicht abgelehnt: %v", p.Problems)
	}
}
