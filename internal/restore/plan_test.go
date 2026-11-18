package restore

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// testRun ist ein letzter Lauf mit Panel (verwaltet Spielserver), einem
// Compose-Projekt, einem Container ohne Compose und Dingen ohne Projekt.
func testRun() (*backup.RunFile, *inventory.Inventory) {
	rf := &backup.RunFile{
		Run: "20261101-100000", Valid: true, Final: true, Hostname: "vps1",
		Groups: []backup.Group{
			{Name: "shop", Containers: []backup.Ctr{{Name: "shop-web-1", Running: true}, {Name: "shop-db-1", Running: true}, {Name: "shop-cron-1"}},
				Dumps: []backup.Item{{Key: "volume:shop_pg", ContainerName: "shop-db-1"}}},
			{Name: "panel", Containers: []backup.Ctr{{Name: "panel-1", Running: true}, {Name: "panel-db-1", Running: true}},
				Dumps:   []backup.Item{{Key: "volume:panel_db", ContainerName: "panel-db-1"}},
				Managed: []backup.ManagedRef{{Key: "managed:mc", Name: "abc"}}},
			{Name: "solo", Containers: []backup.Ctr{{Name: "solo", Running: true}}},
		},
		Snapshots: []backup.RunSnapshot{
			{Key: "path:/opt/shop/data", Group: "shop", Strategy: backup.StratStop, Snapshot: "s2", Paths: []string{"/opt/shop/data"}},
			{Key: "project:shop", Group: "shop", Strategy: backup.StratFolder, Snapshot: "s1", Paths: []string{"/opt/shop"}},
			{Key: "anon:compose:shop/web:/cache", Group: "shop", Strategy: backup.StratStop, Snapshot: "s3", Paths: []string{"/var/lib/docker/volumes/f00/_data"}},
			{Key: "volume:shop_pg", Group: "shop", Strategy: backup.StratDump, Snapshot: "s4", Filename: "dumps/shop/volume-shop_pg.sql", Engine: "postgres"},
			{Key: "volume:shop_pg", Group: "shop", Strategy: backup.StratRaw, Snapshot: "s5", Paths: []string{"/var/lib/docker/volumes/shop_pg/_data"}},
			{Key: "project:panel", Group: "panel", Strategy: backup.StratFolder, Snapshot: "p1", Paths: []string{"/opt/panel"}},
			{Key: "managed:mc", Group: "panel", Strategy: backup.StratStop, Snapshot: "p2", Paths: []string{"/var/lib/pterodactyl/volumes/abc"}},
			{Key: "volume:panel_db", Group: "panel", Strategy: backup.StratDump, Snapshot: "p3", Filename: "dumps/panel/volume-panel_db.sql", Engine: "mariadb"},
			{Key: "volume:solo_data", Group: "solo", Strategy: backup.StratStop, Snapshot: "o1", Paths: []string{"/var/lib/docker/volumes/solo_data/_data"}},
			{Key: "image:sha256:aaa", Strategy: backup.StratImage, Snapshot: "i1", Filename: "images/aaa.tar", ImageID: "sha256:aaa", Refs: []string{"shop-web:lokal"}},
			{Key: "image:sha256:bbb", Strategy: backup.StratImage, Snapshot: "i2", Filename: "images/bbb.tar", ImageID: "sha256:bbb"},
			{Key: "path:/srv/files", Strategy: backup.StratStop, Snapshot: "l1", Paths: []string{"/srv/files"}},
			{Key: "volume:lose", Strategy: backup.StratLive, Snapshot: "l2", Paths: []string{"/var/lib/docker/volumes/lose/_data"}},
			{Key: "path:/home/demo/alt", Strategy: backup.StratArchive, Snapshot: "a1", Paths: []string{"/home/demo/alt"}, Archive: true},
			{Key: "caddy", Strategy: backup.StratHost, Snapshot: "c1", Paths: []string{"/etc/caddy"}},
		},
	}
	inv := &inventory.Inventory{
		Projects: []inventory.Project{
			{Key: "project:shop", WorkingDir: "/opt/shop", ConfigFiles: []string{"/opt/shop/compose.yaml"}},
			{Key: "project:panel", WorkingDir: "/opt/panel", ConfigFiles: []string{"/opt/panel/docker-compose.yml"}},
		},
		Containers: []inventory.Container{
			{Name: "shop-web-1", Service: "web", ImageID: "sha256:aaa", Image: "shop-web:lokal", Networks: []string{"shop_default", "proxy"},
				Ports: []inventory.Port{{HostPort: 8080, Proto: "tcp"}, {HostPort: 8080, Proto: "tcp"}, {HostPort: 53, Proto: "udp"}, {ContainerPort: 9000, Proto: "tcp"}}},
			{Name: "shop-db-1", Service: "db", Networks: []string{"shop_default"}},
			{Name: "shop-cron-1", Service: "cron", Ports: []inventory.Port{{HostPort: 9999, Proto: "tcp"}}},
			{Name: "panel-1", Service: "panel", Networks: []string{"bridge"}, Ports: []inventory.Port{{HostIP: "203.0.113.5", HostPort: 443, Proto: "tcp"}, {HostIP: "203.0.113.5", HostPort: 443, Proto: "udp"}}},
			{Name: "panel-db-1", Service: "database"},
			{Name: "solo", ImageID: "sha256:bbb", Image: "solo-bild:1", HostNetwork: true, Ports: []inventory.Port{{HostPort: 25565, Proto: "tcp"}}},
		},
		Volumes: []inventory.Volume{
			{Key: "volume:lose", Name: "lose"},
			{Key: "volume:solo_data", Name: "solo_data"},
			{Key: "volume:shop_pg", Name: "shop_pg", FormerProj: "shop"},
		},
		Networks: []inventory.Network{
			{Name: "bridge", Builtin: true},
			{Name: "shop_default", Project: "shop"},
			{Name: "proxy", Driver: "bridge"},
			{Name: "unbenutzt", Driver: "bridge"},
		},
	}
	return rf, inv
}

func TestBuild(t *testing.T) {
	p := Build(testRun())
	if len(p.Problems) > 0 {
		t.Fatalf("Probleme: %v", p.Problems)
	}
	var names []string
	for _, g := range p.Groups {
		names = append(names, g.Name)
	}
	if !reflect.DeepEqual(names, []string{"panel", "shop", "solo"}) {
		t.Errorf("Reihenfolge %v: das Panel muss zuerst kommen", names)
	}
	panel, shop, solo := p.Groups[0], p.Groups[1], p.Groups[2]

	if !shop.Compose || shop.Dir != "/opt/shop" || !reflect.DeepEqual(shop.Files, []string{"/opt/shop/compose.yaml"}) {
		t.Errorf("shop Compose: %+v", shop)
	}
	if !reflect.DeepEqual(shop.Services, []string{"db", "web"}) {
		t.Errorf("shop Dienste %v: nur die, die liefen", shop.Services)
	}
	if !reflect.DeepEqual(shop.Ports, []int{8080}) {
		t.Errorf("shop Ports %v: nur TCP am Host, ohne Doppelte", shop.Ports)
	}
	var paths []string
	for _, f := range shop.Paths {
		paths = append(paths, f.Paths...)
	}
	if !reflect.DeepEqual(paths, []string{"/opt/shop", "/opt/shop/data"}) {
		t.Errorf("shop Ordner %v: Projektordner zuerst", paths)
	}
	wantVols := []Volume{
		{Key: "anon:compose:shop/web:/cache", Title: backup.Title("anon:compose:shop/web:/cache"), Snapshot: "s3", OldPath: "/var/lib/docker/volumes/f00/_data", Service: "web", Dest: "/cache"},
		{Key: "volume:shop_pg", Title: backup.Title("volume:shop_pg"), Snapshot: "s5", OldPath: "/var/lib/docker/volumes/shop_pg/_data", Name: "shop_pg"},
	}
	if !reflect.DeepEqual(shop.Volumes, wantVols) {
		t.Errorf("shop Volumes:\n%+v\nerwartet\n%+v", shop.Volumes, wantVols)
	}
	wantDump := []Dump{{Key: "volume:shop_pg", Title: backup.Title("volume:shop_pg"), Snapshot: "s4", File: "/dumps/shop/volume-shop_pg.sql", Engine: "postgres", Service: "db", Raw: true}}
	if !reflect.DeepEqual(shop.Dumps, wantDump) {
		t.Errorf("shop Dumps: %+v", shop.Dumps)
	}

	if len(panel.Ports) != 0 {
		t.Errorf("panel Ports %v: an die alte Adresse gebunden, also nicht prüfbar", panel.Ports)
	}
	if !panel.Manager || len(panel.Paths) != 2 || panel.Paths[1].Paths[0] != "/var/lib/pterodactyl/volumes/abc" {
		t.Errorf("panel: %+v", panel)
	}
	if len(panel.Dumps) != 1 || panel.Dumps[0].Raw || panel.Dumps[0].Service != "database" || panel.Dumps[0].Engine != "mariadb" {
		t.Errorf("panel Dumps: %+v", panel.Dumps)
	}

	if solo.Compose || len(solo.Ports) != 0 || len(solo.Volumes) != 1 || !solo.Volumes[0].External {
		t.Errorf("solo: %+v", solo)
	}

	if len(p.Images) != 2 || len(p.Images[0].Tags) != 0 || len(p.Images[1].Tags) != 1 || p.Images[1].Tags[0] != "solo-bild:1" || p.Images[1].File != "/images/bbb.tar" {
		t.Errorf("Images: %+v", p.Images)
	}
	if !reflect.DeepEqual(p.Networks, []Network{{Name: "proxy", Driver: "bridge"}}) {
		t.Errorf("Netze: %+v", p.Networks)
	}
	if len(p.Loose) != 1 || p.Loose[0].Paths[0] != "/srv/files" {
		t.Errorf("ohne Projekt: %+v", p.Loose)
	}
	if len(p.Volumes) != 1 || p.Volumes[0].Name != "lose" || !p.Volumes[0].External {
		t.Errorf("Volumes ohne Projekt: %+v", p.Volumes)
	}
	if len(p.Kept) != 2 {
		t.Errorf("Archiv und Caddy kommen nicht automatisch zurück: %v", p.Kept)
	}
	if !reflect.DeepEqual(p.Targets(), []string{"/opt/panel", "/opt/shop", "/opt/shop/data", "/srv/files", "/var/lib/pterodactyl/volumes/abc"}) {
		t.Errorf("Ziele: %v", p.Targets())
	}
	if !reflect.DeepEqual(p.VolumeNames(), []string{"lose", "shop_pg", "solo_data"}) {
		t.Errorf("Volumes: %v", p.VolumeNames())
	}
	if !reflect.DeepEqual(p.DumpVolumes(), []string{"panel_db", "shop_pg"}) {
		t.Errorf("Datenbank-Volumes %v: Dumps und Rohkopien", p.DumpVolumes())
	}
	if len(p.Warnings) != 2 || !strings.Contains(p.Warnings[0], "panel-1 hört nur auf die Adresse 203.0.113.5") ||
		!strings.Contains(p.Warnings[1], "solo ist kein Compose-Projekt") {
		t.Errorf("Hinweise: %v", p.Warnings)
	}

	var out bytes.Buffer
	p.Write(&out)
	for _, s := range []string{"letzter Lauf, Dienste waren aus", "Images laden: shop-web:lokal; als solo-bild:1", "Netze anlegen: proxy",
		"Dateien aus dem letzten Lauf, Dump nur zum Vergleich", "Gesichert, kommt aber nicht automatisch zurück"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("Plan ohne %q:\n%s", s, out.String())
		}
	}
}

func TestBuildProbleme(t *testing.T) {
	rf, inv := testRun()
	rf.Valid = false
	inv.Containers[1].Service = "" // Dienst der Shop-Datenbank unbekannt
	p := Build(rf, inv)
	if len(p.Problems) != 1 || !strings.Contains(p.Problems[0], "shop-db-1") {
		t.Errorf("Probleme: %v", p.Problems)
	}
	if !strings.Contains(strings.Join(p.Warnings, "\n"), "nicht vollständig gültig") {
		t.Errorf("Hinweise: %v", p.Warnings)
	}
}

// Ein Volume, das ein früheres Projekt angelegt hat und das jetzt ein
// anderes als external nutzt, legt HostDeck vor Compose an (sonst: „external
// volume not found“).
func TestBuildVolumeVonAnderemProjekt(t *testing.T) {
	rf, inv := testRun()
	rf.Snapshots = append(rf.Snapshots, backup.RunSnapshot{Key: "volume:shop-v3-data", Group: "shop", Strategy: backup.StratStop, Snapshot: "s9", Paths: []string{"/var/lib/docker/volumes/shop-v3-data/_data"}})
	inv.Volumes = append(inv.Volumes, inventory.Volume{Key: "volume:shop-v3-data", Name: "shop-v3-data", FormerProj: "project:shop-v3"})
	p := Build(rf, inv)
	var shop Group
	for _, g := range p.Groups {
		if g.Name == "shop" {
			shop = g
		}
	}
	ext := map[string]bool{}
	for _, v := range shop.Volumes {
		ext[v.Name] = v.External
	}
	if !ext["shop-v3-data"] || ext["shop_pg"] {
		t.Fatalf("extern: %v", ext)
	}
}

// Ein Image, das nach dem Anlegen des Containers umbenannt wurde, bekommt
// beim Zurückspielen auch den Namen aus der Compose-Datei. Sonst sucht
// Compose ihn im Internet (Fall kula-monitor beim Probe-Umzug 03.11.).
func TestImageMitAnderemNamenImContainer(t *testing.T) {
	rf, inv := testRun()
	for i := range rf.Snapshots {
		if rf.Snapshots[i].ImageID == "sha256:aaa" {
			rf.Snapshots[i].Refs = []string{"shop-web:2a219d7c237b"}
		}
	}
	p := Build(rf, inv)
	var im *Image
	for i := range p.Images {
		if p.Images[i].ID == "sha256:aaa" {
			im = &p.Images[i]
		}
	}
	if im == nil {
		t.Fatalf("Image fehlt: %+v", p.Images)
	}
	if len(im.Tags) != 1 || im.Tags[0] != "shop-web:lokal" {
		t.Errorf("Tags: %+v, erwartet shop-web:lokal", im.Tags)
	}
	var out bytes.Buffer
	p.Write(&out)
	if !strings.Contains(out.String(), "auch als shop-web:lokal") {
		t.Errorf("Vorschau nennt den zweiten Namen nicht:\n%s", out.String())
	}
}

func TestNormRef(t *testing.T) {
	for in, want := range map[string]string{
		"nginx":                        "nginx:latest",
		"nginx:latest":                 "nginx:latest",
		"docker.io/library/nginx:1.27": "nginx:1.27",
		"ghcr.io/x/kula:local":         "ghcr.io/x/kula:local",
		"localhost:5000/app":           "localhost:5000/app:latest",
		"kula-monitor:2a219d7c237b":    "kula-monitor:2a219d7c237b",
		"app@sha256:abc":               "app@sha256:abc",
	} {
		if got := normRef(in); got != want {
			t.Errorf("normRef(%q) = %q, erwartet %q", in, got, want)
		}
	}
}

// Was du im Leitstand gestoppt hattest, bleibt auf dem neuen Server aus
// und steht in der Vorschau (Probe-Umzug 03.11.: pensum-app).
func TestVonDirGestopptBleibtAus(t *testing.T) {
	rf, inv := testRun()
	at := time.Date(2026, 11, 2, 21, 0, 0, 0, time.UTC)
	rf.UserStopped = map[string]time.Time{"shop-cron-1": at, "shop-web-1": at}
	p := Build(rf, inv)
	var shop *Group
	for i := range p.Groups {
		if p.Groups[i].Name == "shop" {
			shop = &p.Groups[i]
		}
	}
	if shop == nil {
		t.Fatal("shop fehlt")
	}
	if !reflect.DeepEqual(shop.KeptOff, []string{"shop-web-1", "shop-cron-1"}) {
		t.Errorf("KeptOff: %v", shop.KeptOff)
	}
	for _, s := range shop.Services {
		if s == "web" {
			t.Errorf("web startet trotzdem: %v", shop.Services)
		}
	}
	var out bytes.Buffer
	p.Write(&out)
	if !strings.Contains(out.String(), "bleibt aus, wie von dir im Leitstand gestoppt: shop-web-1, shop-cron-1") {
		t.Errorf("Vorschau:\n%s", out.String())
	}
	if !p.UserStopped["shop-cron-1"].Equal(at) {
		t.Errorf("UserStopped: %v", p.UserStopped)
	}
}
