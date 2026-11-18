package fixture

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/discover"
	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/hostfs"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// GameServer ist der Name des Spielserver-Containers, den Wings anlegt.
const GameServer = "c324292c-dd72-4c73-b255-1302b7d71909"

// IdleServer ist ein zweiter Spielserver, der aus ist: Es gibt nur seinen
// Ordner in wings/volumes, keinen Container.
const IdleServer = "78586c9d-db17-49e8-8950-614ca9dd0755"

// AnonRedis ist das anonyme Volume des Redis-Containers von urls.
var AnonRedis = id("volume:anon-redis")

// Server bündelt alles, was ein Test braucht.
type Server struct {
	Root   string
	Docker *Docker
	Git    *Git
	Caddy  *Caddy
}

func (s *Server) Deps() discover.Deps {
	return discover.Deps{Docker: s.Docker, FS: hostfs.New(s.Root), Git: s.Git, Caddy: s.Caddy, Statfs: Statfs}
}

func (s *Server) Options() discover.Options {
	return discover.Options{
		ToolVersion: "HostDeck test",
		Euid:        0,
		Now:         func() time.Time { return time.Date(2026, 10, 30, 22, 0, 0, 0, time.UTC) },
		SizeTimeout: time.Minute,
	}
}

// Run führt discover gegen den nachgebauten Server aus.
func (s *Server) Run(t testing.TB) *inventory.Inventory {
	t.Helper()
	inv, err := discover.Run(context.Background(), s.Deps(), s.Options())
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

// Beispiel baut den Server nach. gen verändert alle Container-IDs, so wie
// ein Neustart der Container oder des Spielservers.
func Beispiel(t testing.TB, gen int) *Server {
	t.Helper()
	root := t.TempDir()
	s := &Server{Root: root, Docker: buildDocker(gen), Git: buildGit(), Caddy: &Caddy{JSON: caddyJSON}}
	buildTree(t, root, s.Docker)
	buildHost(t, root)
	return s
}

func buildDocker(gen int) *Docker {
	d := &Docker{VolSizes: map[string]int64{}}
	const (
		mc   = "/opt/docker/mcserver"
		cala = "/opt/calagopus"
	)
	// mcserver: Calagopus-Panel mit eingebautem Wings, DB, Cache, Router, Packs.
	d.add("calagopus-web", "ghcr.io/calagopus/panel:latest", compose("mcserver", "web", mc), true, gen).
		Bind(cala+"/wings", cala+"/wings", true).
		Bind(cala+"/wings-config.yml", cala+"/wings-config.yml", true).
		Bind(cala+"/data", "/var/lib/calagopus", true).
		Bind(cala+"/logs", "/var/log/calagopus", true).
		Bind("/var/run/docker.sock", "/var/run/docker.sock", true).
		Bind("/var/lib/docker/containers", "/var/lib/docker/containers", false).
		Port("127.0.0.1", 8000, 8000, "tcp").Port("0.0.0.0", 2022, 2022, "tcp").
		Env("DATABASE_URL=postgres://calagopus:geheim-db-passwort@calagopus-db/panel", "APP_KEY=geheim-app-key").Net("mcserver_default")
	d.add("calagopus-db", "pgautoupgrade/pgautoupgrade:18-alpine", compose("mcserver", "db", mc), true, gen).
		Bind(cala+"/postgres", "/var/lib/postgresql", true).
		Env("POSTGRES_PASSWORD=geheim-calagopus-pg", "POSTGRES_USER=calagopus").Net("mcserver_default")
	d.add("calagopus-cache", "valkey/valkey:8-alpine", compose("mcserver", "cache", mc), true, gen).
		Bind(cala+"/cache", "/data", true).Net("mcserver_default")
	d.add("mc-router", "itzg/mc-router:latest", compose("mcserver", "mc-router", mc), true, gen).HostNet().
		Env("MAPPING=geheim-mapping")
	d.add("mc-packs", "mcserver-mc-packs", compose("mcserver", "mc-packs", mc), true, gen).
		ImageID("sha256:eb4d35b7452d"+strings.Repeat("0", 52)).
		Bind(mc+"/resourcepacks/dist", "/srv", false).Port("0.0.0.0", 8095, 80, "tcp")
	d.add(GameServer, "ghcr.io/pterodactyl/yolks:java_21", map[string]string{"Service": "Calagopus"}, true, gen).
		Bind(cala+"/wings/volumes/"+GameServer, "/home/container", true).
		Bind(cala+"/wings/vmounts/"+GameServer+"/hosts", "/etc/hosts", false).
		Bind(cala+"/wings/vmounts/"+GameServer+"/machine-id", "/etc/machine-id", false).
		Port("0.0.0.0", 25567, 25567, "tcp").Port("0.0.0.0", 19144, 19144, "udp").Port("0.0.0.0", 19145, 19145, "udp").
		Env("SERVER_MEMORY=8192", "RCON_PASSWORD=geheim-rcon")

	// pensum (Nachfolger von Arbeitszeit), Compose in einem Unterordner des Repos.
	const pen = "/opt/docker/pensum/pensum"
	d.add("pensum-app-1", "pensum-app:latest", compose("pensum", "app", pen), true, gen).
		Port("127.0.0.1", 3005, 3000, "tcp").Env("SESSION_SECRET=geheim-session")
	d.add("pensum-db-1", "postgres:17-alpine", compose("pensum", "db", pen), true, gen).
		Vol("pensum_pensum_db", "/var/lib/postgresql/data").Env("POSTGRES_PASSWORD=geheim-pensum")

	// work: Ordner fehlt, keine Domain.
	const work = "/opt/docker/work"
	d.add("work-frontend-1", "work-frontend:latest", compose("work", "frontend", work), true, gen).Port("127.0.0.1", 8085, 80, "tcp")
	d.add("work-backend-1", "work-backend:latest", compose("work", "backend", work), true, gen).Port("127.0.0.1", 8081, 8080, "tcp").
		Env("JWT_SECRET=geheim-jwt")
	d.add("work-db-1", "postgres:16-alpine", compose("work", "db", work), true, gen).Vol("work_pgdata_prod", "/var/lib/postgresql/data").
		Env("POSTGRES_PASSWORD=geheim-work")

	// urls: PHP, Redis mit anonymem Volume, MySQL 8.4.
	const urls = "/opt/docker/URLS"
	d.add("urls-web-1", "php:8.3-apache", compose("urls", "web", urls), true, gen).Bind(urls+"/site", "/var/www/html", true).Port("127.0.0.1", 82, 80, "tcp")
	d.add("urls-redis-1", "redis:7-alpine", compose("urls", "redis", urls), true, gen).Vol(AnonRedis, "/data")
	d.add("urls-db-1", "mysql:8.4", compose("urls", "db", urls), true, gen).Vol("urls_db_data", "/var/lib/mysql").
		Env("MYSQL_ROOT_PASSWORD=geheim-mysql")

	// arbeitszeit (alt): App gestoppt, DB läuft, Daten als Bind im Projektordner.
	const az = "/opt/docker/arbeitszeit"
	d.add("zeiterfassung-app-prod", "ghcr.io/officialtaxz2/arbeitszeit:v0.2.4-18-g83b7ab8", compose("arbeitszeit", "app", az), false, gen).
		Exit(143).Bind(az+"/data", "/app/data", true).Port("127.0.0.1", 3004, 3000, "tcp")
	d.add("zeiterfassung-database-prod", "postgres:15-alpine", compose("arbeitszeit", "database", az), true, gen).
		Bind(az+"/postgres_data", "/var/lib/postgresql/data", true).Port("127.0.0.1", 5432, 5432, "tcp").Env("POSTGRES_PASSWORD=geheim-az")

	// cloudgamingvpn mit Headscale (SQLite) und eigener Caddy-Datei im Repo.
	const cg = "/opt/docker/cloudgamingvpn"
	d.add("cloudgamingvpn-gaming-hub", "ghcr.io/officialtaxz2/gaming-hub:1.4.0", compose("cloudgamingvpn", "gaming-hub", cg), true, gen).
		Bind(cg+"/deploy/data/gaming-hub", "/data", true).Port("127.0.0.1", 18081, 8080, "tcp")
	d.add("cloudgamingvpn-headscale", "headscale/headscale:0.26.1", compose("cloudgamingvpn", "headscale", cg), true, gen).
		Bind(cg+"/deploy/headscale", "/etc/headscale", false).
		Bind(cg+"/deploy/data/headscale", "/var/lib/headscale", true).Port("127.0.0.1", 18080, 8080, "tcp")

	// Arcane: Docker-Oberfläche, bindet ganz /opt/docker ein.
	d.add("arcane", "ghcr.io/getarcaneapp/arcane:latest", compose("arcane", "arcane", "/opt/arcane"), true, gen).
		Vol("arcane_arcane-data", "/app/data").Bind("/opt/docker", "/app/data/projects", true).
		Bind("/var/run/docker.sock", "/var/run/docker.sock", true).Port("0.0.0.0", 3552, 3552, "tcp").
		Env("ENCRYPTION_KEY=geheim-arcane", "JWT_SECRET=geheim-arcane-jwt")

	// tippspiel: Release-Ordner im Repo plus Volume mit SQLite.
	const tipp = "/opt/docker/tipptipp2-git"
	d.add("tippspiel-pro-v2", "tippspiel-pro-v2:latest", compose("tipptipp2", "app", tipp), true, gen).
		Bind(tipp+"/.releases/20261029T190221Z-857d86660ed5/runtime", "/app", false).
		Vol("tippspiel-pro-v3-data", "/data").Port("127.0.0.1", 8083, 3000, "tcp")

	d.add("saftbar-app-1", "saftbar-app:latest", compose("saftbar", "app", "/opt/docker/drink"), true, gen).
		Vol("saftbar_saftbar-data", "/app/data").Port("127.0.0.1", 3001, 3000, "tcp")
	d.add("mcguides-guide-1", "mcguides-guide:latest", compose("mcguides", "guide", "/opt/docker/mcguides"), true, gen).
		Vol("mcguides_guide-data", "/app/data").Port("127.0.0.1", 3080, 3000, "tcp")

	// neko: großer Container-Layer, Cleanup-Job gestoppt.
	const neko = "/opt/docker/nekoNew/neko"
	d.add("neko-neko-1", "my-neko/helium:latest", compose("neko", "neko", neko), true, gen).
		Bind(neko+"/deploy/adaptive-quality.yaml", "/etc/neko/adaptive-quality.yaml", false).
		Bind(neko+"/downloads", "/home/neko/Downloads", true).
		Bind(neko+"/files", "/home/neko/files", true).
		Port("127.0.0.1", 8082, 8080, "tcp").Port("0.0.0.0", 51000, 51000, "udp").
		Env("NEKO_PASSWORD=geheim-neko", "NEKO_PASSWORD_ADMIN=geheim-neko-admin").Size(1_160_000_000)
	d.add("neko-cleanup-1", "alpine:3.20", compose("neko", "cleanup", neko), false, gen).Exit(0).
		Bind(neko+"/files", "/cleanup-dir", true)

	// kula: Monitoring im Host-Netz mit /proc.
	d.add("kula-monitor", "ghcr.io/c0m4r/kula:latest", compose("kula", "kula-monitor", "/opt/docker/kula/deploy"), true, gen).HostNet().
		Bind("/proc", "/proc", false).Vol("kula-monitor-data", "/var/lib/kula")

	// Images: acht laufende lokal gebaute ohne Registry-Digest.
	local := map[string]bool{
		"pensum-app:latest": true, "work-frontend:latest": true, "work-backend:latest": true,
		"my-neko/helium:latest": true, "tippspiel-pro-v2:latest": true, "saftbar-app:latest": true,
		"mcguides-guide:latest": true,
	}
	// So kamen die Images im containerd-Modus auf den Server.
	how := map[string]string{
		"pensum-app:latest": "compose", "work-frontend:latest": "compose", "work-backend:latest": "compose",
		"saftbar-app:latest": "build", "mcguides-guide:latest": "build", "tippspiel-pro-v2:latest": "build",
		"my-neko/helium:latest": "load",
	}
	d.How = map[string]string{}
	seen := map[string]bool{}
	for _, c := range d.Ctrs {
		if seen[c.C.Image] {
			continue
		}
		seen[c.C.Image] = true
		img := dockerapi.Image{ID: c.C.Image, Size: 250_000_000}
		ref := c.C.Config.Image
		d.How[img.ID] = "pull"
		if h := how[ref]; h != "" {
			d.How[img.ID] = h
		}
		switch {
		case strings.HasPrefix(c.C.Image, "sha256:eb4d35b7452d"):
			// mc-packs: altes Image, dessen Name inzwischen auf ein neueres
			// zeigt.
			img.RepoTags = nil
		case local[ref]:
			img.RepoTags = []string{ref}
		default:
			name, _, _ := strings.Cut(ref, ":")
			img.RepoTags = []string{ref}
			img.RepoDigests = []string{name + "@sha256:" + id("digest:"+ref)}
		}
		d.Imgs = append(d.Imgs, img)
	}
	// Ein ungenutztes Image taucht im Inventar nicht auf.
	d.Imgs = append(d.Imgs, dockerapi.Image{ID: ImageID("alt:1"), RepoTags: []string{"alt:1"}})

	vol := func(name string, size int64, labels map[string]string) {
		d.Vols = append(d.Vols, dockerapi.Volume{Name: name, Driver: "local", Mountpoint: "/var/lib/docker/volumes/" + name + "/_data", Labels: labels})
		d.VolSizes[name] = size
	}
	proj := func(p string) map[string]string { return map[string]string{"com.docker.compose.project": p} }
	vol("pensum_pensum_db", 48_000_000, proj("pensum"))
	vol("work_pgdata_prod", 61_000_000, proj("work"))
	vol("urls_db_data", 210_000_000, proj("urls"))
	vol(AnonRedis, 1_200_000, map[string]string{"com.docker.volume.anonymous": ""})
	vol("arcane_arcane-data", 3_000_000, proj("arcane"))
	vol("tippspiel-pro-v3-data", 9_000_000, nil)
	vol("saftbar_saftbar-data", 2_000_000, proj("saftbar"))
	vol("mcguides_guide-data", 4_000_000, proj("mcguides"))
	vol("kula-monitor-data", 120_000_000, nil)
	// Elf verwaiste Volumes.
	vol("streamingroom_postgres-data", 900_000_000, proj("streamingroom"))
	vol("streamingroom_uploads", 1_100_000_000, proj("streamingroom"))
	vol("lol-build-explorer_postgres-data", 300_000_000, proj("lol-build-explorer"))
	vol("work_db_data", 80_000_000, proj("work"))
	vol("work_go_mod_cache", 250_000_000, proj("work"))
	vol("work_go-mod-cache", 240_000_000, proj("work"))
	vol("tippspiel-pro-v2-data", 7_000_000, nil)
	vol("tippspiel-setup-data-7f3a", 1_000_000, nil)
	vol("neko_profile", 90_000_000, proj("neko"))
	vol("saftbar_old-data", 1_000_000, nil)
	vol(id("volume:anon-orphan"), 30_000_000, map[string]string{"com.docker.volume.anonymous": ""})

	for _, n := range []string{"bridge", "host", "none"} {
		d.Nets = append(d.Nets, dockerapi.Network{Name: n, Driver: n, Scope: "local"})
	}
	net := dockerapi.Network{Name: "mcserver_default", Driver: "bridge", Scope: "local", Labels: proj("mcserver")}
	net.IPAM.Config = append(net.IPAM.Config, struct {
		Subnet string `json:"Subnet"`
	}{Subnet: "172.18.0.0/16"})
	d.Nets = append(d.Nets, net)
	return d
}

// Git ist ein nachgebautes git.
type Git struct {
	Repos map[string]*inventory.Git // Ordner -> Stand
}

func (g *Git) Inspect(_ context.Context, dir string) (*inventory.Git, error) {
	for d, r := range g.Repos {
		if hostfs.Under(dir, d) && hostfs.Under(dir, r.Root) {
			cp := *r
			return &cp, nil
		}
	}
	return nil, nil
}

func repo(root, name string, dirty int) *inventory.Git {
	return &inventory.Git{Root: root, Remote: "https://github.com/officialtaxz2/" + name + ".git", Branch: "main", Commit: id("commit:" + name)[:40], Dirty: dirty}
}

func buildGit() *Git {
	return &Git{Repos: map[string]*inventory.Git{
		"/opt/docker/mcserver":       repo("/opt/docker/mcserver", "mcserver", 0),
		"/opt/docker/pensum":         repo("/opt/docker/pensum", "arbeitszeit", 1),
		"/opt/docker/arbeitszeit":    repo("/opt/docker/arbeitszeit", "arbeitszeit", 3),
		"/opt/docker/nekoNew/neko":   repo("/opt/docker/nekoNew/neko", "neko", 3),
		"/opt/docker/kula":           repo("/opt/docker/kula", "kula", 0),
		"/opt/docker/cloudgamingvpn": repo("/opt/docker/cloudgamingvpn", "cloudgamingvpn", 0),
		"/opt/docker/URLS":           repo("/opt/docker/URLS", "urls", 0),
		"/opt/docker/tipptipp2-git":  repo("/opt/docker/tipptipp2-git", "tippspiel", 0),
		"/opt/docker/drink":          repo("/opt/docker/drink", "saftbar", 0),
		"/opt/docker/mcguides":       repo("/opt/docker/mcguides", "mcguides", 0),
		"/opt/docker/neko":           repo("/opt/docker/neko", "neko-alt", 0),
	}}
}

// Caddy liefert eine feste Ausgabe von caddy adapt.
type Caddy struct {
	JSON string
	Err  error
}

func (c *Caddy) Adapt(context.Context, string) ([]byte, error) {
	if c.Err != nil {
		return nil, c.Err
	}
	return []byte(c.JSON), nil
}

const sqliteHeader = "SQLite format 3\x00"

func buildTree(t testing.TB, root string, d *Docker) {
	t.Helper()
	write := func(p, content string) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkdir := func(p string) {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sqlite := func(p string) { write(p, sqliteHeader+strings.Repeat("\x00", 1024)) }
	big := strings.Repeat("x", 8192)

	write("/proc/sys/kernel/hostname", "vps1\n")
	write("/proc/sys/kernel/osrelease", "6.8.0-87-generic\n")
	write("/etc/os-release", "PRETTY_NAME=\"Ubuntu 24.04.5 LTS\"\nID=ubuntu\nVERSION_ID=\"24.04\"\n")

	// /opt/docker
	write("/opt/docker/mcserver/docker-compose.yml", "services: {}\n")
	write("/opt/docker/mcserver/resourcepacks/dist/pack.zip", big)
	write("/opt/docker/pensum/pensum/docker-compose.yml", "services: {}\n")
	write("/opt/docker/pensum/README.md", "pensum\n")
	write("/opt/docker/arbeitszeit/docker-compose.yml", "services: {}\n")
	write("/opt/docker/arbeitszeit/data/uploads/a.pdf", big)
	write("/opt/docker/arbeitszeit/postgres_data/PG_VERSION", "15\n")
	write("/opt/docker/arbeitszeit/postgres_data/base/1/PG_VERSION", "15\n")
	write("/opt/docker/cloudgamingvpn/docker-compose.yml", "services: {}\n")
	write("/opt/docker/cloudgamingvpn/deploy/caddy/cloudgamingvpn.caddy", "play.example.net {\n\treverse_proxy localhost:18081\n}\n")
	write("/opt/docker/cloudgamingvpn/deploy/headscale/config.yaml", "server_url: https://play.example.net\n")
	write("/opt/docker/cloudgamingvpn/deploy/data/gaming-hub/state.json", "{}\n")
	sqlite("/opt/docker/cloudgamingvpn/deploy/data/headscale/db.sqlite")
	write("/opt/docker/URLS/docker-compose.yml", "services: {}\n")
	write("/opt/docker/URLS/site/index.php", "<?php\n")
	write("/opt/docker/tipptipp2-git/docker-compose.yml", "services: {}\n")
	write("/opt/docker/tipptipp2-git/.releases/20261029T190221Z-857d86660ed5/runtime/server.js", big)
	write("/opt/docker/drink/docker-compose.yml", "services: {}\n")
	write("/opt/docker/mcguides/docker-compose.yml", "services: {}\n")
	write("/opt/docker/nekoNew/neko/docker-compose.yml", "services: {}\n")
	write("/opt/docker/nekoNew/neko/deploy/adaptive-quality.yaml", "quality: auto\n")
	mkdir("/opt/docker/nekoNew/neko/downloads")
	write("/opt/docker/nekoNew/neko/files/notiz.txt", "hallo\n")
	write("/opt/docker/kula/deploy/docker-compose.yml", "services: {}\n")
	write("/opt/docker/kula/README.md", "kula\n")
	// alte Projekte ohne Container
	write("/opt/docker/neko/docker-compose.yml", "services: {}\n")
	mkdir("/opt/docker/neko/.git")
	write("/opt/docker/streamingroom/docker-compose.yml", "services: {}\n")
	// /opt/arcane
	write("/opt/arcane/docker-compose.yml", "services: {}\n")
	// containerd legt Docker selbst an; wird ignoriert.
	mkdir("/opt/containerd/bin")

	// /opt/calagopus
	write("/opt/calagopus/wings/volumes/"+GameServer+"/server.properties", "#Minecraft server properties\nrcon.password=geheim-rcon\nmotd=\\u00A7aHallo\nlevel-name=world\n")
	write("/opt/calagopus/wings/volumes/"+GameServer+"/world/level.dat", big)
	write("/opt/calagopus/wings/backups/backup-1.tar.gz", big+big)
	// Ein zweiter Server, der aus ist: Wings hat seinen Container gelöscht,
	// die Daten liegen noch da. Ordner ohne UUID-Namen sind keine Server.
	write("/opt/calagopus/wings/volumes/"+IdleServer+"/server.properties", "motd=Alt\nlevel-name=survival\nrcon.password=geheim-rcon2\n")
	write("/opt/calagopus/wings/volumes/"+IdleServer+"/world/level.dat", big)
	mkdir("/opt/calagopus/wings/volumes/.sftp-tmp")
	write("/opt/calagopus/wings/vmounts/"+GameServer+"/hosts", "127.0.0.1 localhost\n")
	write("/opt/calagopus/wings/vmounts/"+GameServer+"/machine-id", "0123456789abcdef\n")
	write("/opt/calagopus/wings-config.yml", "token: geheim-wings-token\n")
	write("/opt/calagopus/data/settings.json", "{}\n")
	write("/opt/calagopus/logs/panel.log", "start\n")
	write("/opt/calagopus/postgres/18/docker/PG_VERSION", "18\n")
	write("/opt/calagopus/cache/dump.rdb", "REDIS0011")

	// Docker-Volumes
	vols := "/var/lib/docker/volumes/"
	write(vols+"pensum_pensum_db/_data/PG_VERSION", "17\n")
	write(vols+"work_pgdata_prod/_data/PG_VERSION", "16\n")
	write(vols+"urls_db_data/_data/mysql.ibd", "x")
	write(vols+"urls_db_data/_data/ibdata1", "x")
	write(vols+AnonRedis+"/_data/dump.rdb", "REDIS0011")
	sqlite(vols + "arcane_arcane-data/_data/arcane.db")
	sqlite(vols + "tippspiel-pro-v3-data/_data/tipp.db")
	sqlite(vols + "saftbar_saftbar-data/_data/data.sqlite")
	sqlite(vols + "mcguides_guide-data/_data/guide.db")
	write(vols+"kula-monitor-data/_data/metrics.bin", big)
	write(vols+"streamingroom_postgres-data/_data/PG_VERSION", "14\n")
	write(vols+"streamingroom_uploads/_data/video.mp4", big)
	write(vols+"lol-build-explorer_postgres-data/_data/PG_VERSION", "16\n")
	write(vols+"work_db_data/_data/PG_VERSION", "16\n")
	mkdir(vols + "work_go_mod_cache/_data/cache")
	mkdir(vols + "work_go-mod-cache/_data/cache")
	sqlite(vols + "tippspiel-pro-v2-data/_data/tipp.db")
	mkdir(vols + "tippspiel-setup-data-7f3a/_data")
	mkdir(vols + "neko_profile/_data/.config")
	mkdir(vols + "saftbar_old-data/_data")
	mkdir(vols + id("volume:anon-orphan") + "/_data")

	// Caddy
	write("/etc/caddy/Caddyfile", caddyfile)
	write("/etc/caddy/mcserver.caddy", "panel.example.net {\n\treverse_proxy localhost:8000\n}\n")
	write("/etc/caddy/kula-monitor.caddy", "monitor.example.net {\n\treverse_proxy 127.0.0.1:27960\n}\n")
	for _, f := range []string{"Caddyfile.bak", "Caddyfile.bak2", "Caddyfile.kula-backup", "Caddyfile.old", "Caddyfile.2026-09-01"} {
		write("/etc/caddy/"+f, "# alt\n")
	}

	// /proc: offene Ports. Docker-Weiterleitungen (docker-proxy) für
	// veröffentlichte Ports, dazu sshd, caddy und die Container im Host-Netz.
	type sock struct {
		addr  string
		port  int
		pid   int
		inode int
	}
	kula := d.Find("kula-monitor").C.ID
	router := d.Find("mc-router").C.ID
	procs := map[int][2]string{ // pid -> comm, cgroup
		100: {"sshd", "0::/system.slice/ssh.service\n"},
		200: {"caddy", "0::/system.slice/caddy.service\n"},
		300: {"docker-proxy", "0::/system.slice/docker.service\n"},
		400: {"kula", "0::/system.slice/docker-" + kula + ".scope\n"},
		500: {"mc-router", "0::/system.slice/docker-" + router + ".scope\n"},
	}
	socks := []sock{
		{"0.0.0.0", 22, 100, 1001}, {"0.0.0.0", 80, 200, 1002}, {"0.0.0.0", 443, 200, 1003},
		{"127.0.0.1", 27960, 400, 1004}, {"0.0.0.0", 25565, 500, 1005},
	}
	inode := 2000
	for _, c := range d.Ctrs {
		if !c.C.State.Running {
			continue
		}
		for spec, binds := range c.C.HostConfig.PortBindings {
			if !strings.HasSuffix(spec, "/tcp") {
				continue
			}
			for _, b := range binds {
				var p int
				fmt.Sscan(b.HostPort, &p)
				inode++
				socks = append(socks, sock{b.HostIP, p, 300, inode})
			}
		}
	}
	var tcp strings.Builder
	tcp.WriteString("  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n")
	for i, s := range socks {
		fmt.Fprintf(&tcp, "%4d: %s:%04X 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 %d 1 0000000000000000 100 0 0 10 0\n", i, procHex(s.addr), s.port, s.inode)
	}
	// Eine aufgebaute Verbindung (01) zählt nicht als offener Port.
	fmt.Fprintf(&tcp, "%4d: %s:%04X 0200000A:D431 01 00000000:00000000 00:00000000 00000000     0        0 9999 1 0000000000000000 100 0 0 10 0\n", len(socks), procHex("127.0.0.1"), 3005)
	write("/proc/net/tcp", tcp.String())
	write("/proc/net/tcp6", "  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"+
		"   0: 00000000000000000000000000000000:0016 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1006 1 0000000000000000 100 0 0 10 0\n")
	for pid, pc := range procs {
		write(fmt.Sprintf("/proc/%d/comm", pid), pc[0]+"\n")
		write(fmt.Sprintf("/proc/%d/cgroup", pid), pc[1])
		mkdir(fmt.Sprintf("/proc/%d/fd", pid))
	}
	fd := 3
	for _, s := range append(socks, sock{"::", 22, 100, 1006}) {
		link := filepath.Join(root, fmt.Sprintf("/proc/%d/fd/%d", s.pid, fd))
		fd++
		if err := os.Symlink(fmt.Sprintf("socket:[%d]", s.inode), link); err != nil {
			t.Fatal(err)
		}
	}
}

// procHex schreibt eine IPv4-Adresse wie /proc/net/tcp (Little Endian).
func procHex(addr string) string {
	var a, b, c, d int
	fmt.Sscanf(addr, "%d.%d.%d.%d", &a, &b, &c, &d)
	return fmt.Sprintf("%02X%02X%02X%02X", d, c, b, a)
}

const caddyfile = `{
	email admin@example.net
}

(logging) {
	log {
		output file /var/log/caddy/access.log
	}
}

import /etc/caddy/mcserver.caddy
import /opt/docker/cloudgamingvpn/deploy/caddy/cloudgamingvpn.caddy
import kula-monitor.caddy # relativ zu /etc/caddy

example.com {
	import logging
	reverse_proxy localhost:3001
}
work.example.net {
	reverse_proxy localhost:3005
}
neko.example.net {
	reverse_proxy localhost:8082
}
tipp.example.net {
	reverse_proxy localhost:8083
}
urls.example.net {
	reverse_proxy localhost:82
}
pensum.example.net {
	reverse_proxy localhost:3004
}
webmc.example.net {
	reverse_proxy localhost:8084
}
crafting.example.net {
	reverse_proxy localhost:3080
}
`

// caddyJSON entspricht der Ausgabe von caddy adapt für das Caddyfile oben,
// gekürzt auf das, was discover liest, plus ein Zugangsdaten-Feld, das nie
// im Inventar landen darf.
const caddyJSON = `{
  "apps": {
    "http": {
      "servers": {
        "srv0": {
          "listen": [":443"],
          "routes": [
            {"match":[{"host":["panel.example.net"]}],"handle":[{"handler":"subroute","routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"localhost:8000"}]}]}]}],"terminal":true},
            {"match":[{"host":["play.example.net"]}],"handle":[{"handler":"subroute","routes":[
              {"match":[{"path":["/headscale/*"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"localhost:18080"}]}]},
              {"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"localhost:18081"}]}]}
            ]}],"terminal":true},
            {"match":[{"host":["monitor.example.net"]}],"handle":[{"handler":"subroute","routes":[{"handle":[
              {"handler":"authentication","providers":{"http_basic":{"accounts":[{"username":"demo","password":"geheim-bcrypt-hash"}]}}},
              {"handler":"reverse_proxy","upstreams":[{"dial":"127.0.0.1:27960"}]}]}]}],"terminal":true},
            {"match":[{"host":["example.com"]}],"handle":[{"handler":"subroute","routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"localhost:3001"}]}]}]}],"terminal":true},
            {"match":[{"host":["work.example.net"]}],"handle":[{"handler":"subroute","routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"localhost:3005"}]}]}]}],"terminal":true},
            {"match":[{"host":["neko.example.net"]}],"handle":[{"handler":"subroute","routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"localhost:8082"}]}]}]}],"terminal":true},
            {"match":[{"host":["tipp.example.net"]}],"handle":[{"handler":"subroute","routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"localhost:8083"}]}]}]}],"terminal":true},
            {"match":[{"host":["urls.example.net"]}],"handle":[{"handler":"subroute","routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"localhost:82"}]}]}]}],"terminal":true},
            {"match":[{"host":["pensum.example.net"]}],"handle":[{"handler":"subroute","routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"localhost:3004"}]}]}]}],"terminal":true},
            {"match":[{"host":["webmc.example.net"]}],"handle":[{"handler":"subroute","routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"localhost:8084"}]}]}]}],"terminal":true},
            {"match":[{"host":["crafting.example.net"]}],"handle":[{"handler":"subroute","routes":[{"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"localhost:3080"}]}]}]}],"terminal":true}
          ]
        }
      }
    },
    "tls": {"automation": {"policies": [{"issuers": [{"module": "acme", "email": "admin@example.net"}]}]}}
  }
}`
