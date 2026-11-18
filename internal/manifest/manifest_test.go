package manifest_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/fixture"
	"github.com/officialtaxz2/hostdeck/internal/manifest"
)

const valid = `version: 1
source: {hostname: vps1}
projects:
  mcserver:
    decision: take
    managed:
      c324292c-dd72-4c73-b255-1302b7d71909: {manager: web, stop: manager}
    data:
      path:/opt/calagopus/wings: {strategy: stop-copy}
      path:/opt/calagopus/wings/backups: {strategy: skip, reason: nur aktueller Stand}
      path:/opt/calagopus/postgres: {strategy: dump, engine: postgres}
  arbeitszeit:
    decision: archive
    reason: pensum ist der Nachfolger
host:
  caddy: {decision: take}
`

func TestGueltigesManifest(t *testing.T) {
	m, err := manifest.Parse(strings.NewReader(valid))
	if err != nil {
		t.Fatal(err)
	}
	if m.Projects["arbeitszeit"].Decision != "archive" {
		t.Errorf("arbeitszeit: %+v", m.Projects["arbeitszeit"])
	}
}

func TestStrengesEinlesen(t *testing.T) {
	cases := map[string]struct{ yaml, want string }{
		"unbekanntes Feld":      {"version: 1\nsource: {hostname: x}\nfarbe: blau\n", "field farbe not found"},
		"Tippfehler im Projekt": {"version: 1\nsource: {hostname: x}\nprojects:\n  a: {decison: take}\n", "field decison not found"},
		"falsche Version":       {"version: 2\nsource: {hostname: x}\n", "version: 2 wird nicht unterstützt"},
		"ungültige Entscheidung": {"version: 1\nsource: {hostname: x}\nprojects:\n  a: {decision: vielleicht}\n",
			`decision "vielleicht" ist ungültig`},
		"skip ohne Grund": {"version: 1\nsource: {hostname: x}\nprojects:\n  a: {decision: skip}\n", "skip braucht eine Begründung"},
		"ungültige Strategie": {"version: 1\nsource: {hostname: x}\nprojects:\n  a:\n    decision: take\n    data:\n      volume:x: {strategy: kopieren}\n",
			`strategy "kopieren" ist ungültig`},
		"dump ohne Engine": {"version: 1\nsource: {hostname: x}\nunassigned:\n  volume:x: {strategy: dump}\n", "dump braucht eine engine"},
		"relativer Pfad":   {"version: 1\nsource: {hostname: x}\nunassigned:\n  path:opt/x: {strategy: live-copy}\n", "muss absolut"},
		"Pfad mit ..":      {"version: 1\nsource: {hostname: x}\nunassigned:\n  path:/opt/../etc: {strategy: live-copy}\n", "muss absolut"},
		"Container-ID als Schlüssel": {"version: 1\nsource: {hostname: x}\nunassigned:\n  4f2a9c: {strategy: live-copy}\n",
			"muss mit volume:, anon: oder path: beginnen"},
		"ungültiges Image": {"version: 1\nsource: {hostname: x}\nprojects:\n  a:\n    decision: take\n    services:\n      app: {image: bauen}\n",
			`image "bauen" ist ungültig`},
		"archive im Projekt": {"version: 1\nsource: {hostname: x}\nprojects:\n  a:\n    decision: take\n    data:\n      volume:x: {strategy: archive}\n",
			"archive gibt es nur unter unassigned"},
		"doppelter Schlüssel": {"version: 1\nsource: {hostname: x}\nprojects:\n  a: {decision: take}\n  a: {decision: skip, reason: x}\n", "already defined"},
		"zwei Dokumente":      {"version: 1\nsource: {hostname: x}\n---\nversion: 1\n", "nur ein YAML-Dokument"},
		"leer":                {"", "leer"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := manifest.Parse(strings.NewReader(c.yaml))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Fehler %v, erwartet %q", err, c.want)
			}
		})
	}
}

func TestVorschlagIstGueltigUndStabil(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	m := manifest.Propose(inv)
	b, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := manifest.Parse(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("Vorschlag lässt sich nicht einlesen: %v\n%s", err, b)
	}
	if d := manifest.Compare(back, inv); !d.Empty() {
		t.Errorf("eingelesener Vorschlag weicht ab: %+v", d)
	}
	if strings.Contains(string(b), "sha256:") {
		t.Error("Vorschlag enthält eine Image- oder Container-ID")
	}
	for _, c := range inv.Containers {
		if strings.Contains(string(b), c.ID[:12]) {
			t.Errorf("Vorschlag enthält die Container-ID von %s", c.Name)
		}
	}
}

func TestVorschlagRegeln(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	m := manifest.Propose(inv)
	check := func(what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s: %q, erwartet %q", what, got, want)
		}
	}
	check("pensum", m.Projects["pensum"].Decision, "take")
	check("work (Ordner fehlt)", m.Projects["work"].Decision, "open")
	check("pensum DB", m.Projects["pensum"].Data["volume:pensum_pensum_db"].Strategy, "dump")
	check("pensum App-Image", m.Projects["pensum"].Services["app"].Image, "image-save")
	check("pensum DB-Image", m.Projects["pensum"].Services["db"].Image, "rebuild")
	check("Calagopus-DB", m.Projects["mcserver"].Data["path:/opt/calagopus/postgres"].Strategy, "dump")
	check("Wings", m.Projects["mcserver"].Data["path:/opt/calagopus/wings"].Strategy, "stop-copy")
	check("Spielserver", m.Projects["mcserver"].Managed[fixture.GameServer].Manager, "web")
	if idle := m.Projects["mcserver"].Managed[fixture.IdleServer]; idle == nil || idle.Manager != "web" || !strings.HasPrefix(idle.Note, "„Alt“, Welt survival; gerade aus") {
		t.Errorf("Spielserver ohne Container: %+v", idle)
	}
	check("Headscale-SQLite", m.Projects["cloudgamingvpn"].Data["path:/opt/docker/cloudgamingvpn/deploy/data/headscale"].Engine, "sqlite")
	check("verwaistes Volume", m.Unassigned["volume:work_db_data"].Strategy, "archive")
	check("verwaister Cache", m.Unassigned["volume:work_go-mod-cache"].Strategy, "skip")
	check("alter Ordner", m.Unassigned["path:/opt/docker/neko"].Strategy, "archive")
	check("Caddy", m.Host.Caddy.Decision, "take")
	if _, ok := m.Projects["arbeitszeit"].Data["path:/opt/docker/arbeitszeit"]; ok {
		t.Error("Projektordner selbst soll kein Daten-Eintrag sein")
	}
	if _, ok := m.Projects["cloudgamingvpn"].Data["path:/opt/docker/cloudgamingvpn/deploy/headscale"]; ok {
		t.Error("nur lesend eingebundene Konfiguration im Projektordner soll keinen eigenen Eintrag haben")
	}
}

func TestEigeneUnterpfadeSindNichtWeggefallen(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	m := manifest.Propose(inv)
	m.Projects["mcserver"].Data["path:/opt/calagopus/wings/backups"] = &manifest.Data{Strategy: "skip", Reason: "nur aktueller Stand"}
	m.Projects["neko"].Data["path:/opt/docker/nekoNew/neko/cache"] = &manifest.Data{Strategy: "skip", Reason: "Cache"}
	m.Unassigned["path:/opt/gibtsnicht"] = &manifest.Data{Strategy: "skip", Reason: "x"}
	d := manifest.Compare(m, inv)
	if len(d.New) != 0 || len(d.Gone) != 1 || d.Gone[0] != "path:/opt/gibtsnicht" {
		t.Errorf("Diff: %+v", d)
	}
}

func TestNeueEintraegeErgaenzen(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	proposal := manifest.Propose(inv)
	m := manifest.Propose(inv)
	m.Projects["pensum"].Decision = "archive"
	delete(m.Projects, "urls")
	delete(m.Projects["mcserver"].Services, "web")
	delete(m.Unassigned, "volume:work_db_data")
	m.Host = nil

	added := manifest.AddNew(m, proposal)
	want := "compose:mcserver/web,host:caddy,project:urls,volume:work_db_data"
	if strings.Join(added, ",") != want {
		t.Errorf("ergänzt: %v, erwartet %s", added, want)
	}
	if m.Projects["pensum"].Decision != "archive" {
		t.Error("bestehende Entscheidung überschrieben")
	}
	if m.Projects["urls"].Data["volume:urls_db_data"].Strategy != "dump" {
		t.Error("neues Projekt ohne seine Daten")
	}
	if d := manifest.Compare(m, inv); !d.Empty() {
		t.Errorf("nach dem Ergänzen bleibt ein Unterschied: %+v", d)
	}
	// Ergänzte Einträge sind Kopien, keine Verweise in den Vorschlag.
	m.Projects["urls"].Decision = "skip"
	if proposal.Projects["urls"].Decision == "skip" {
		t.Error("Vorschlag wurde mitgeändert")
	}
	if again := manifest.AddNew(m, proposal); len(again) != 0 {
		t.Errorf("zweites Ergänzen: %v", again)
	}
}

func TestEntfernen(t *testing.T) {
	m := manifest.Propose(fixture.Beispiel(t, 1).Run(t))
	for _, k := range []string{"project:urls", "compose:mcserver/web", "managed:" + fixture.GameServer, "volume:work_db_data", "path:/opt/calagopus/wings", "host:caddy"} {
		if !m.Remove(k) {
			t.Errorf("%s nicht entfernt", k)
		}
		if m.Remove(k) {
			t.Errorf("%s zweimal entfernt", k)
		}
	}
	if p, s, ok := manifest.SplitServiceKey("compose:mcserver/web#2"); !ok || p != "mcserver" || s != "web" {
		t.Errorf("SplitServiceKey: %q %q %v", p, s, ok)
	}
}

// Ist ein Spielserver beim nächsten Lauf aus, hat Wings seinen Container
// gelöscht. Er darf dann nicht als weggefallen gelten.
func TestSpielserverAusIstNichtWeggefallen(t *testing.T) {
	m := manifest.Propose(fixture.Beispiel(t, 1).Run(t))
	s := fixture.Beispiel(t, 2)
	var keep []*fixture.Ctr
	for _, c := range s.Docker.Ctrs {
		if c.C.Name != "/"+fixture.GameServer {
			keep = append(keep, c)
		}
	}
	s.Docker.Ctrs = keep
	d := manifest.Compare(m, s.Run(t))
	if !d.Empty() {
		t.Errorf("weg: %v, neu: %v", d.Gone, d.New)
	}
}

// Vorgeschlagene Caches in /root kommen nur mit /root zusammen. Wer den
// Haken entfernt, bekommt ihn beim nächsten Lauf nicht wieder.
func TestCacheVorschlagBleibtEntfernt(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	m := manifest.Propose(inv)
	if d := m.Unassigned["path:/root/.cache"]; d == nil || d.Strategy != "skip" {
		t.Fatalf("Cache in /root nicht vorgeschlagen: %+v", d)
	}
	if !m.Remove("path:/root/.cache") {
		t.Fatal("Cache nicht entfernt")
	}
	if d := manifest.Compare(m, inv); !d.Empty() {
		t.Errorf("entfernter Cache gilt als neu: %+v", d)
	}
	if added := manifest.AddNew(m, manifest.Propose(inv)); len(added) != 0 {
		t.Errorf("wieder hinzugefügt: %v", added)
	}
	// Ohne /root im Manifest kommen /root und seine Caches zusammen dazu.
	m.Remove("path:/root")
	m.Remove("path:/root/.vscode-server")
	added := manifest.AddNew(m, manifest.Propose(inv))
	if strings.Join(added, ",") != "path:/root,path:/root/.cache,path:/root/.vscode-server" {
		t.Errorf("hinzugefügt: %v", added)
	}
	if m.Unassigned["path:/etc"].Strategy != "archive" || m.Unassigned["path:/minecraft-alt"].Strategy != "open" {
		t.Errorf("/etc oder Fund: %+v %+v", m.Unassigned["path:/etc"], m.Unassigned["path:/minecraft-alt"])
	}
}

// Hinweise schreibt HostDeck selbst. Nach einer neuen Bestandsaufnahme zeigen sie
// den aktuellen Stand; Entscheidungen und eigene Einträge bleiben.
func TestHinweiseWerdenAktualisiert(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	proposal := manifest.Propose(inv)
	m := manifest.Propose(inv)

	etc := m.Unassigned["path:/etc"]
	etc.Strategy, etc.Reason, etc.Note = "skip", "brauche ich nicht", "4 geänderte Dateien, 25 eigene Dateien"
	var idle *manifest.Managed
	for _, p := range m.Projects {
		if mg := p.Managed[fixture.IdleServer]; mg != nil {
			idle = mg
		}
	}
	if idle == nil {
		t.Fatal("ausgeschalteter Server fehlt im Vorschlag")
	}
	idle.Note = "veraltet"
	m.Projects["pensum"].Decision, m.Projects["pensum"].Note = "archive", "veraltet"
	m.Unassigned["path:/opt/eigen"] = &manifest.Data{Strategy: "skip", Reason: "eigener Eintrag", Note: "bleibt"}

	manifest.RefreshNotes(m, proposal)

	if etc.Note != proposal.Unassigned["path:/etc"].Note || etc.Strategy != "skip" || etc.Reason != "brauche ich nicht" {
		t.Errorf("/etc: %+v", etc)
	}
	if !strings.HasPrefix(idle.Note, "„Alt“, Welt survival; gerade aus") {
		t.Errorf("Hinweis zum Server nicht aktualisiert: %q", idle.Note)
	}
	if p := m.Projects["pensum"]; p.Note != proposal.Projects["pensum"].Note || p.Decision != "archive" {
		t.Errorf("pensum: %+v", p)
	}
	if d := m.Unassigned["path:/opt/eigen"]; d.Note != "bleibt" || d.Reason != "eigener Eintrag" {
		t.Errorf("eigener Eintrag verändert: %+v", d)
	}
	// Ohne Änderung am Server bleibt das Manifest gleich.
	a, _ := proposal.Marshal()
	again := manifest.Propose(inv)
	manifest.RefreshNotes(again, proposal)
	b, _ := again.Marshal()
	if !bytes.Equal(a, b) {
		t.Error("Auffrischen ohne Änderung verändert das Manifest")
	}
}
