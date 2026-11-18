package discover_test

import (
	"bytes"
	"context"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/discover"
	"github.com/officialtaxz2/hostdeck/internal/fixture"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/manifest"
)

// Die Tests folgen der Abnahme-Checkliste für M1a aus dem Konzept
// („Startbedingungen und Abnahme für M1“), angewendet auf einen
// nachgebauten vps1.

func findings(inv *inventory.Inventory, code string) []inventory.Finding {
	var out []inventory.Finding
	for _, f := range inv.Findings {
		if f.Code == code {
			out = append(out, f)
		}
	}
	return out
}

func hasFinding(inv *inventory.Inventory, code, subject string) bool {
	for _, f := range findings(inv, code) {
		if f.Subject == subject {
			return true
		}
	}
	return false
}

func TestAbnahme01_ContainerUndProjekte(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	stopped := 0
	for _, c := range inv.Containers {
		if !c.Running {
			stopped++
		}
	}
	if len(inv.Containers) != 25 || stopped != 2 {
		t.Errorf("Container: %d, gestoppt %d; erwartet 25 und 2", len(inv.Containers), stopped)
	}
	if len(inv.Projects) != 12 {
		var names []string
		for _, p := range inv.Projects {
			names = append(names, p.Name)
		}
		t.Errorf("Compose-Projekte: %d %v; erwartet 12", len(inv.Projects), names)
	}
	if !hasFinding(inv, "projektordner-fehlt", "project:work") {
		t.Errorf("fehlender Ordner von work nicht gemeldet")
	}
	if p := inv.Project("project:pensum"); p == nil || p.Path != "/opt/docker/pensum" || p.WorkingDir != "/opt/docker/pensum/pensum" {
		t.Errorf("pensum: Projektordner soll das Git-Repo /opt/docker/pensum sein, ist %+v", p)
	}
}

func TestAbnahme02_SpielserverVerwaltet(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	c := inv.Container("managed:" + fixture.GameServer)
	if c == nil {
		t.Fatal("Spielserver nicht als verwaltet erkannt")
	}
	if c.ManagedBy != "compose:mcserver/web" || c.Project != "project:mcserver" || c.ManagerPath != "/opt/calagopus/wings" {
		t.Errorf("Spielserver: ManagedBy=%q Project=%q ManagerPath=%q", c.ManagedBy, c.Project, c.ManagerPath)
	}
	for _, m := range c.Mounts {
		if m.Type == "bind" && m.DataKey != "path:/opt/calagopus/wings" {
			t.Errorf("Mount %s gehört zu %q, erwartet path:/opt/calagopus/wings", m.Source, m.DataKey)
		}
	}
	for _, e := range inv.Paths {
		if strings.HasPrefix(e.Path, "/opt/calagopus/wings/") {
			t.Errorf("eigener Eintrag für %s, gehört aber zum Ordner des Verwalters", e.Path)
		}
		if e.Owner == "project:arcane" && strings.HasPrefix(e.Path, "/opt/calagopus") {
			t.Errorf("%s ist Arcane zugeordnet", e.Path)
		}
	}
	if !hasFinding(inv, "verwaltet", c.Key) {
		t.Error("kein Hinweis, den Spielserver über den Verwalter zu stoppen")
	}
	m := inv.ManagedServer("managed:" + fixture.GameServer)
	if m == nil || !m.Container || !m.Running || m.Path != "/opt/calagopus/wings/volumes/"+fixture.GameServer || m.Size <= 0 {
		t.Errorf("Spielserver in der Server-Liste: %+v", m)
	}
}

// Wings löscht den Container eines Servers, der aus ist. Der Server muss
// trotzdem in der Liste stehen, sonst fiele er beim nächsten Lauf aus der
// Auswahl.
func TestAbnahme02b_SpielserverAus(t *testing.T) {
	check := func(t *testing.T, inv *inventory.Inventory, name string) {
		t.Helper()
		m := inv.ManagedServer("managed:" + name)
		if m == nil {
			t.Fatalf("Server %s ohne Container fehlt; Liste: %+v", name, inv.Managed)
		}
		if m.Container || m.Running || m.Manager != "compose:mcserver/web" || m.Project != "project:mcserver" ||
			m.Root != "/opt/calagopus/wings" || m.Path != "/opt/calagopus/wings/volumes/"+name || m.Size <= 0 {
			t.Errorf("Server %s: %+v", name, m)
		}
		p := inv.Project("project:mcserver")
		if !slices.Contains(p.Managed, m.Key) {
			t.Errorf("mcserver listet %s nicht: %v", m.Key, p.Managed)
		}
		if !hasFinding(inv, "verwaltet-ohne-container", m.Key) {
			t.Errorf("kein Hinweis zu %s", name)
		}
	}
	t.Run("ein Server läuft, einer ist aus", func(t *testing.T) {
		inv := fixture.Beispiel(t, 1).Run(t)
		check(t, inv, fixture.IdleServer)
		if len(inv.Managed) != 2 {
			t.Errorf("erwartet 2 Server, gefunden %+v", inv.Managed)
		}
		// Namen aus server.properties helfen beim Auseinanderhalten.
		if l := inv.ManagedServer("managed:" + fixture.IdleServer).Label; l != "„Alt“, Welt survival" {
			t.Errorf("Label des Servers, der aus ist: %q", l)
		}
		if l := inv.ManagedServer("managed:" + fixture.GameServer).Label; l != "„Hallo“" {
			t.Errorf("Label des laufenden Servers: %q", l)
		}
		if !hasFindingText(inv, "verwaltet-ohne-container", "(„Alt“, Welt survival)") {
			t.Error("Hinweis nennt den Namen des Servers nicht")
		}
	})
	t.Run("alle Server aus", func(t *testing.T) {
		s := fixture.Beispiel(t, 1)
		var keep []*fixture.Ctr
		for _, c := range s.Docker.Ctrs {
			if c.C.Name != "/"+fixture.GameServer {
				keep = append(keep, c)
			}
		}
		s.Docker.Ctrs = keep
		inv := s.Run(t)
		check(t, inv, fixture.GameServer)
		check(t, inv, fixture.IdleServer)
		if len(inv.Managed) != 2 {
			t.Errorf("erwartet 2 Server, gefunden %+v", inv.Managed)
		}
	})
}

func TestAbnahme03_HostNetz(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	var host []string
	for _, c := range inv.Containers {
		if c.HostNetwork {
			host = append(host, c.Name)
		}
	}
	sort.Strings(host)
	if !reflect.DeepEqual(host, []string{"kula-monitor", "mc-router"}) {
		t.Errorf("Host-Netz: %v", host)
	}
}

func TestAbnahme04_Datenbanken(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	var dbs, caches []string
	for _, c := range inv.Containers {
		if c.DB == nil {
			continue
		}
		s := c.DB.Engine + " " + c.DB.Version
		if c.DB.Engine == "redis" || c.DB.Engine == "valkey" {
			caches = append(caches, c.DB.Engine)
		} else {
			dbs = append(dbs, s)
		}
		if c.DB.DataKey == "" {
			t.Errorf("%s: Datenbank ohne zugeordneten Datenordner", c.Name)
		}
	}
	sort.Strings(dbs)
	sort.Strings(caches)
	want := []string{"mysql 8.4", "postgres 15", "postgres 16", "postgres 17", "postgres 18"}
	if !reflect.DeepEqual(dbs, want) {
		t.Errorf("Datenbanken: %v, erwartet %v", dbs, want)
	}
	if !reflect.DeepEqual(caches, []string{"redis", "valkey"}) {
		t.Errorf("Redis/Valkey: %v", caches)
	}
	// Die Daten der DB hängen an ihrem Eintrag, damit das Manifest dump vorschlägt.
	if v := inv.Volume("volume:pensum_pensum_db"); v == nil || v.DB == nil || v.DB.Engine != "postgres" {
		t.Errorf("pensum_pensum_db ohne Datenbank: %+v", v)
	}
	if e := inv.Path("path:/opt/calagopus/postgres"); e == nil || e.DB == nil || e.DB.Version != "18" {
		t.Errorf("Calagopus-DB (PG18-Layout) nicht erkannt: %+v", e)
	}
	if v := inv.Volume("anon:compose:urls/redis:/data"); v == nil || v.DB == nil || v.DB.Engine != "redis" {
		t.Errorf("anonymes Redis-Volume: %+v", v)
	}
}

func TestAbnahme05_VerwaisteVolumes(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	var orphans []string
	for _, v := range inv.Volumes {
		if !v.Orphan {
			continue
		}
		orphans = append(orphans, v.Name)
		if v.Size < 0 {
			t.Errorf("%s ohne Größe", v.Name)
		}
	}
	if len(orphans) != 11 {
		t.Errorf("verwaiste Volumes: %d %v; erwartet 11", len(orphans), orphans)
	}
	for _, name := range []string{"lol-build-explorer_postgres-data", "work_db_data"} {
		v := inv.Volume("volume:" + name)
		if v == nil || v.DB == nil || v.DB.Engine != "postgres" {
			t.Errorf("%s nicht als PostgreSQL erkannt: %+v", name, v)
		}
	}
	if v := inv.Volume("volume:work_db_data"); v != nil && v.FormerProj != "project:work" {
		t.Errorf("work_db_data: früheres Projekt %q", v.FormerProj)
	}
}

func TestAbnahme06_Systempfade(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	got := map[string]bool{}
	for _, s := range inv.System {
		got[s.Container+" "+s.Source] = true
	}
	for _, want := range []string{
		"compose:kula/kula-monitor /proc",
		"compose:mcserver/web /var/run/docker.sock",
		"compose:mcserver/web /var/lib/docker/containers",
		"compose:arcane/arcane /var/run/docker.sock",
	} {
		if !got[want] {
			t.Errorf("Systempfad fehlt: %s (gefunden: %v)", want, got)
		}
	}
	for _, e := range inv.Paths {
		if e.Path == "/proc" || strings.HasPrefix(e.Path, "/var/lib/docker") || strings.HasSuffix(e.Path, "docker.sock") {
			t.Errorf("Systempfad %s als Daten erfasst", e.Path)
		}
	}
	for _, c := range inv.Containers {
		for _, m := range c.Mounts {
			if m.System && m.DataKey != "" {
				t.Errorf("%s: Systempfad %s hat Daten-Schlüssel %s", c.Name, m.Source, m.DataKey)
			}
		}
	}
}

func TestAbnahme07_ElternordnerUndDatenInProjekten(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	e := inv.Path("path:/opt/docker")
	if e == nil || e.Kind != inventory.KindParent || e.Owner != "" {
		t.Fatalf("/opt/docker soll Elternordner ohne Eigentümer sein: %+v", e)
	}
	if len(e.Contains) < 10 {
		t.Errorf("/opt/docker enthält %d Projekte", len(e.Contains))
	}
	arcane := inv.Container("compose:arcane/arcane")
	for _, m := range arcane.Mounts {
		if m.Source == "/opt/docker" && m.DataKey != "" {
			t.Errorf("Arcanes Mount von /opt/docker zählt als Daten: %s", m.DataKey)
		}
	}

	pg := inv.Path("path:/opt/docker/arbeitszeit/postgres_data")
	if pg == nil || pg.Kind != inventory.KindData || pg.CoveredBy != "" || pg.DB == nil || pg.DB.Engine != "postgres" || pg.DB.Version != "15" {
		t.Errorf("arbeitszeit/postgres_data: %+v", pg)
	}
	az := inv.Path("path:/opt/docker/arbeitszeit")
	if az == nil || !contains(az.Excludes, "path:/opt/docker/arbeitszeit/postgres_data") || !contains(az.Excludes, "path:/opt/docker/arbeitszeit/data") {
		t.Errorf("Datenordner nicht aus dem Projektordner geschnitten: %+v", az)
	}
	hs := inv.Path("path:/opt/docker/cloudgamingvpn/deploy/data/headscale")
	if hs == nil || hs.CoveredBy != "" || len(hs.SQLite) != 1 {
		t.Errorf("Headscale-SQLite nicht als eigener Eintrag: %+v", hs)
	}
	cfg := inv.Path("path:/opt/docker/cloudgamingvpn/deploy/headscale")
	if cfg == nil || cfg.CoveredBy != "path:/opt/docker/cloudgamingvpn" {
		t.Errorf("Headscale-Konfiguration soll mit dem Projektordner gesichert werden: %+v", cfg)
	}
	file := inv.Path("path:/opt/docker/nekoNew/neko/deploy/adaptive-quality.yaml")
	if file == nil || file.Kind != inventory.KindConfig || file.CoveredBy != "path:/opt/docker/nekoNew/neko" {
		t.Errorf("einzelne Datei im Projektordner: %+v", file)
	}
	// Ein Ordner, den zwei Container nutzen, hat trotzdem genau einen Eintrag.
	files := inv.Path("path:/opt/docker/nekoNew/neko/files")
	if files == nil || len(files.Mounts) != 2 || files.Owner != "project:neko" {
		t.Errorf("neko/files: %+v", files)
	}
	// Genau ein Eigentümer pro Pfad: Kein eigener Eintrag liegt in einem
	// anderen, ohne dort ausgeschnitten zu sein.
	own := map[string]inventory.Path{}
	for _, p := range inv.Paths {
		if p.Kind != inventory.KindParent && p.CoveredBy == "" {
			own[p.Path] = p
		}
	}
	for _, p := range own {
		var parent *inventory.Path
		for _, q := range own {
			if q.Path != p.Path && strings.HasPrefix(p.Path, q.Path+"/") && (parent == nil || len(q.Path) > len(parent.Path)) {
				q := q
				parent = &q
			}
		}
		if parent != nil && !contains(parent.Excludes, p.Key) {
			t.Errorf("%s liegt in %s, ist dort aber nicht ausgeschnitten", p.Path, parent.Path)
		}
	}
}

func TestAbnahme08_LokaleImagesUndGit(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	local := 0
	for _, img := range inv.Images {
		if !img.Local || !img.Running {
			continue
		}
		local++
		for _, ck := range img.UsedBy {
			c := inv.Container(ck)
			p := inv.Project(c.Project)
			if p == nil || p.Path == "" {
				t.Errorf("%s: lokales Image ohne Projektordner", ck)
				continue
			}
			if p.DirExists && p.Git == nil {
				t.Errorf("%s: lokales Image ohne Git-Stand", ck)
			}
		}
	}
	if local != 8 {
		t.Errorf("lokal gebaute laufende Images: %d, erwartet 8", local)
	}
	dirty := 0
	for _, p := range inv.Projects {
		if p.Git != nil {
			dirty += p.Git.Dirty
		}
	}
	if dirty != 7 {
		t.Errorf("nicht committete Änderungen: %d, erwartet 7", dirty)
	}
	if n := len(findings(inv, "git-aenderungen")); n != 3 {
		t.Errorf("%d Hinweise zu Git-Änderungen, erwartet 3", n)
	}
	if !hasFinding(inv, "image-unbenannt", "image:eb4d35b7452d") {
		t.Error("unbenanntes Image von mc-packs nicht gemeldet")
	}
}

func TestAbnahme09_Caddy(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	c := inv.Caddy
	if c == nil || !c.Found || c.Error != "" {
		t.Fatalf("Caddy: %+v", c)
	}
	if c.DataDir != "/var/lib/caddy/.local/share/caddy" || c.UID != 998 || c.GID != 997 {
		t.Errorf("Datenordner und Benutzer von Caddy: %q %d:%d", c.DataDir, c.UID, c.GID)
	}
	var imports []string
	for _, imp := range c.Imports {
		imports = append(imports, imp.Path)
		if !imp.Exists {
			t.Errorf("Import %s nicht gefunden", imp.Path)
		}
	}
	wantImports := []string{"/etc/caddy/mcserver.caddy", "/opt/docker/cloudgamingvpn/deploy/caddy/cloudgamingvpn.caddy", "/etc/caddy/kula-monitor.caddy"}
	if !reflect.DeepEqual(imports, wantImports) {
		t.Errorf("Imports: %v", imports)
	}
	if c.Imports[1].Project != "project:cloudgamingvpn" || !c.Imports[1].Outside {
		t.Errorf("Import aus dem Projektordner nicht erkannt: %+v", c.Imports[1])
	}
	dead := map[string]string{}
	for _, r := range c.Routes {
		if r.Status == inventory.RouteStopped || r.Status == inventory.RouteNothing {
			dead[r.Hosts[0]] = r.Status
		}
	}
	want := map[string]string{"pensum.example.net": inventory.RouteStopped, "webmc.example.net": inventory.RouteNothing}
	if !reflect.DeepEqual(dead, want) {
		t.Errorf("tote Routen: %v", dead)
	}
	if len(findings(inv, "caddy-tote-route")) != 2 {
		t.Errorf("tote Routen nicht als Warnung gemeldet")
	}
	for _, k := range []string{"compose:work/frontend", "compose:work/backend"} {
		if !hasFinding(inv, "ohne-domain", k) {
			t.Errorf("%s ohne Domain nicht gemeldet", k)
		}
	}
	if n := len(findings(inv, "ohne-domain")); n != 2 {
		t.Errorf("%d Container ohne Domain gemeldet, erwartet 2: %v", n, findings(inv, "ohne-domain"))
	}
	// Domains landen beim Projekt, auch über Host-Netz (kula) und Unterrouten.
	domains := map[string][]string{}
	for _, p := range inv.Projects {
		domains[p.Name] = p.Domains
	}
	if !reflect.DeepEqual(domains["pensum"], []string{"work.example.net"}) ||
		!reflect.DeepEqual(domains["kula"], []string{"monitor.example.net"}) ||
		!reflect.DeepEqual(domains["cloudgamingvpn"], []string{"play.example.net"}) ||
		!reflect.DeepEqual(domains["mcserver"], []string{"panel.example.net"}) {
		t.Errorf("Domains: %v", domains)
	}
}

func TestAbnahme10_NekoContainerSchicht(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	f := findings(inv, "container-schicht")
	if len(f) != 1 || f[0].Subject != "compose:neko/neko" || !strings.Contains(f[0].Message, "1,16 GB") {
		t.Errorf("Warnung zum neko-Container: %+v", f)
	}
}

func TestAbnahme11_KeineUmgebungswerte(t *testing.T) {
	s := fixture.Beispiel(t, 1)
	var log bytes.Buffer
	opts := s.Options()
	opts.Log = &log
	inv, err := discover.Run(context.Background(), s.Deps(), opts)
	if err != nil {
		t.Fatal(err)
	}
	invJSON, err := inv.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Propose(inv).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	all := string(invJSON) + string(m) + log.String()
	if strings.Contains(all, "geheim") {
		i := strings.Index(all, "geheim")
		t.Fatalf("Geheimnis in der Ausgabe: …%s…", all[max(0, i-80):min(len(all), i+40)])
	}
	c := inv.Container("compose:pensum/db")
	if !contains(c.EnvNames, "POSTGRES_PASSWORD") {
		t.Errorf("Namen der Umgebungsvariablen fehlen: %v", c.EnvNames)
	}
	if !contains(c.EnvSet, "POSTGRES_PASSWORD") {
		t.Errorf("Namen mit Wert fehlen: %v", c.EnvSet)
	}
	if len(s.Docker.RawEnv()) == 0 {
		t.Fatal("Testaufbau ohne Umgebungsvariablen")
	}
}

func TestAbnahme12_ZweiterLaufGleicheSchluessel(t *testing.T) {
	a := fixture.Beispiel(t, 1).Run(t)
	b := fixture.Beispiel(t, 2).Run(t) // alle Container-IDs neu, wie nach Neustarts
	if a.Containers[0].ID == b.Containers[0].ID {
		t.Fatal("Testaufbau: IDs sollten sich unterscheiden")
	}
	if !reflect.DeepEqual(keys(a), keys(b)) {
		t.Errorf("Schlüssel unterscheiden sich:\n%v\n%v", keys(a), keys(b))
	}
	ma, mb := manifest.Propose(a), manifest.Propose(b)
	if !reflect.DeepEqual(ma.Keys(), mb.Keys()) {
		t.Error("Manifest-Schlüssel unterscheiden sich")
	}
	if d := manifest.Compare(ma, b); !d.Empty() {
		t.Errorf("zweiter Lauf meldet Änderungen: neu %v, weg %v", d.New, d.Gone)
	}
	ya, _ := ma.Marshal()
	yb, _ := mb.Marshal()
	if !bytes.Equal(ya, yb) {
		t.Error("Manifest-Vorschlag ist nicht deterministisch")
	}
}

func keys(inv *inventory.Inventory) []string {
	var out []string
	for _, c := range inv.Containers {
		out = append(out, c.Key)
	}
	for _, v := range inv.Volumes {
		out = append(out, v.Key)
	}
	for _, p := range inv.Paths {
		out = append(out, p.Key)
	}
	for _, p := range inv.Projects {
		out = append(out, p.Key)
	}
	for _, u := range inv.Unclaimed {
		out = append(out, u.Key)
	}
	for _, c := range inv.Containers {
		for _, m := range c.Mounts {
			out = append(out, c.Key+">"+m.DataKey)
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestNichtZugeordneteOrdner(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	var got []string
	var neko *inventory.Unclaimed
	for i, u := range inv.Unclaimed {
		if u.Area == inventory.AreaScan {
			got = append(got, u.Path)
		}
		if u.Path == "/opt/docker/neko" {
			neko = &inv.Unclaimed[i]
		}
	}
	want := []string{"/opt/docker/neko", "/opt/docker/streamingroom"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("nicht zugeordnet: %v, erwartet %v", got, want)
	}
	if neko == nil || neko.Git == nil {
		t.Error("Git-Stand des alten neko-Ordners fehlt")
	}
}

func TestOffenePorts(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	byPort := map[int]inventory.Listener{}
	for _, l := range inv.Listeners {
		byPort[l.Port] = l
	}
	if l := byPort[27960]; l.Container != "compose:kula/kula-monitor" || l.Addr != "127.0.0.1" {
		t.Errorf("kula: %+v", l)
	}
	if l := byPort[22]; l.Process != "sshd" {
		t.Errorf("sshd: %+v", l)
	}
	if _, ok := byPort[0]; ok {
		t.Error("Eintrag mit Port 0")
	}
	n22 := 0
	for _, l := range inv.Listeners {
		if l.Port == 22 {
			n22++
			if l.Addr != "0.0.0.0" && l.Addr != "::" {
				t.Errorf("Adresse für 22: %q", l.Addr)
			}
		}
	}
	if n22 != 2 {
		t.Errorf("Port 22 soll für IPv4 und IPv6 erscheinen, gefunden %d", n22)
	}
}

func TestOhneRootUndOhneGroessen(t *testing.T) {
	s := fixture.Beispiel(t, 1)
	opts := s.Options()
	opts.Euid = 1000
	opts.NoSizes = true
	inv, err := discover.Run(context.Background(), s.Deps(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(inv, "kein-root", "") || inv.Findings[0].Code != "kein-root" {
		t.Errorf("Warnung „kein root“ fehlt oder steht nicht oben: %+v", inv.Findings[0])
	}
	for _, e := range inv.Paths {
		if e.Size != inventory.Unknown {
			t.Errorf("%s: Größe %d trotz --no-sizes", e.Path, e.Size)
		}
	}
}

func TestCaddyAdaptFehler(t *testing.T) {
	s := fixture.Beispiel(t, 1)
	s.Caddy.Err = errorString("caddy ist nicht im PATH")
	inv := s.Run(t)
	if inv.Caddy.Error == "" || !hasFinding(inv, "caddy-adapt", "host:caddy") {
		t.Errorf("Fehler von caddy adapt nicht gemeldet: %+v", inv.Caddy)
	}
	if len(inv.Caddy.Imports) != 3 {
		t.Errorf("Imports sollen auch ohne caddy adapt gelesen werden: %v", inv.Caddy.Imports)
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

func hasFindingText(inv *inventory.Inventory, code, text string) bool {
	for _, f := range findings(inv, code) {
		if strings.Contains(f.Message, text) {
			return true
		}
	}
	return false
}

// K6: Eine Route auf einen Container, den du im Leitstand gestoppt hast, ist
// pausiert und keine Warnung.
func TestRoutePausiertBeiVonDirGestopptem(t *testing.T) {
	s := fixture.Beispiel(t, 1)
	inv, err := discover.Run(context.Background(), s.Deps(), func() discover.Options {
		o := s.Options()
		o.UserStopped = func() map[string]time.Time {
			return map[string]time.Time{"zeiterfassung-app-prod": time.Date(2026, 10, 30, 20, 0, 0, 0, time.UTC)}
		}
		return o
	}())
	if err != nil {
		t.Fatal(err)
	}
	if n := len(findings(inv, "caddy-tote-route")); n != 1 {
		t.Errorf("Warnungen „Tote Route“: %d, erwartet 1 (webmc)", n)
	}
	p := findings(inv, "caddy-route-pausiert")
	if len(p) != 1 || p[0].Level != inventory.LevelInfo || !strings.Contains(p[0].Message, "zeiterfassung-app-prod") {
		t.Errorf("Hinweis „pausiert“: %+v", p)
	}
}
