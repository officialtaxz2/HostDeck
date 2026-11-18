package serverebene

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

func put(t *testing.T, root, p, s string, mode os.FileMode) {
	t.Helper()
	full := filepath.Join(root, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(s), mode); err != nil {
		t.Fatal(err)
	}
	os.Chmod(full, mode)
}

func read(t *testing.T, root, p string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, p))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Beide Server kennen root mit der ID, unter der der Test läuft; so geht
// chown auch ohne root.
func fakeIDs(t *testing.T, root string, more ...string) {
	me := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	put(t, root, "/etc/passwd", "root:x:"+me+"::/root:/bin/bash\n"+strings.Join(more, ""), 0o644)
	put(t, root, "/etc/group", fmt.Sprintf("root:x:%d:\ncrontab:x:%d:\n", os.Getgid(), os.Getgid()), 0o644)
}

func oldServer(t *testing.T) Source {
	root := t.TempDir()
	fakeIDs(t, root)
	put(t, root, "/etc/timezone", "Europe/Berlin\n", 0o644)
	put(t, root, "/etc/sysctl.conf", "# Vorgabe\nnet.ipv4.ip_forward=1\n", 0o644)
	put(t, root, "/etc/sysctl.d/60-vpn.conf", "vm.max_map_count = 262144\n", 0o644)
	put(t, root, "/etc/crontab", "SHELL=/bin/sh\n17 * * * * root cd / && run-parts --report /etc/cron.hourly\n0 4 * * * root /opt/tools/aufraeumen.sh\n", 0o644)
	put(t, root, "/var/spool/cron/crontabs/root", "MAILTO=\"\"\n30 3 * * * /opt/docker/pensum/backup.sh --geheim\n", 0o600)
	put(t, root, "/var/spool/cron/crontabs/demo", "@reboot /home/demo/start.sh\n", 0o600)
	put(t, root, "/etc/cron.daily/logs-leeren", "#!/bin/bash\necho\n", 0o755)
	put(t, root, "/etc/systemd/system/sync.service", "[Service]\nExecStart=/usr/local/bin/sync-it --fest\nEnvironmentFile=/etc/sync.env\nEnvironmentFile=-/etc/sync-optional.env\n", 0o644)
	put(t, root, "/etc/systemd/system/sync.timer", "[Timer]\nOnCalendar=daily\n", 0o644)
	put(t, root, "/etc/systemd/system/sync.service.d/override.conf", "[Service]\nNice=10\n", 0o644)
	put(t, root, "/etc/systemd/system/mnt-daten.mount", "[Mount]\nWhat=/dev/sdb1\n", 0o644)
	put(t, root, "/root/.ssh/id_ed25519", "PRIVAT\n", 0o600)
	put(t, root, "/root/.ssh/id_ed25519.pub", "ssh-ed25519 AAAA root@alt\n", 0o644)
	put(t, root, "/root/.ssh/known_hosts", "github.com ssh-ed25519 AAAAgh\n", 0o644)
	put(t, root, "/root/.ssh/authorized_keys", "ssh-ed25519 AAAAdemo demo@pc\n# alt\nssh-ed25519 AAAAhandy handy\n", 0o600)
	put(t, root, "/root/.docker/config.json", `{"auths":{"ghcr.io":{"auth":"Z2VoZWlt"}}}`, 0o600)
	inv := &inventory.Inventory{
		Tasks: []inventory.Task{
			{Kind: "cron", Source: "/etc/crontab", User: "root", Schedule: "0 4 * * *", When: "täglich 04:00", Command: "/opt/tools/aufraeumen.sh"},
			{Kind: "cron", Source: "/etc/cron.daily/logs-leeren", User: "root", Schedule: "täglich", When: "täglich", Command: "/etc/cron.daily/logs-leeren"},
			{Kind: "cron", Source: "/var/spool/cron/crontabs/demo", User: "demo", Schedule: "@reboot", Command: "/home/demo/start.sh"},
			{Kind: "cron", Source: "/var/spool/cron/crontabs/root", User: "root", Schedule: "30 3 * * *", When: "täglich 03:30", Command: "/opt/docker/pensum/backup.sh --geheim"},
			{Kind: "dienst", Source: "/etc/systemd/system/mnt-daten.mount", Name: "mnt-daten.mount", Enabled: true},
			{Kind: "timer", Source: "/etc/systemd/system/sync.service", Name: "sync.service", Timer: "sync.timer", When: "täglich", Enabled: true},
		},
		Settings: []inventory.Setting{{Path: "/etc/sysctl.conf", Change: inventory.ChangeModified}, {Path: "/etc/sysctl.d/60-vpn.conf", Change: inventory.ChangeAdded},
			{Path: "/etc/ssh/sshd_config", Change: inventory.ChangeModified}, {Path: "/etc/caddy/Caddyfile", Change: inventory.ChangeModified}, {Path: "/etc/cron.d/x", Change: inventory.ChangeAdded}},
	}
	return Source{Root: root, Inv: inv, Host: "alt"}
}

func byKey(items []Item) map[string]Item {
	m := map[string]Item{}
	for _, it := range items {
		m[it.Key] = it
	}
	return m
}

func TestQuelle(t *testing.T) {
	s := oldServer(t)
	m := byKey(s.Items())
	if it := m["zeitzone"]; it.TimeZone != "Europe/Berlin" || it.Selectable() {
		t.Errorf("Zeitzone: %+v", it)
	}
	k := m["kernel"]
	if len(k.Files) != 1 || k.Files[0].Path != SysctlFile || !strings.Contains(string(k.Files[0].Data), "net.ipv4.ip_forward = 1\nvm.max_map_count = 262144\n") || k.Selectable() {
		t.Errorf("Kernel: %+v", k)
	}
	ct := m["aufgabe:/etc/crontab"]
	if f := ct.Files[0]; f.Path != CrontabFile || strings.Contains(string(f.Data), "run-parts") || !strings.Contains(string(f.Data), "SHELL=/bin/sh\n0 4 * * * root /opt/tools/aufraeumen.sh\n") {
		t.Errorf("/etc/crontab: %q", f.Data)
	}
	if strings.Join(ct.Needs, " ") != "/opt/tools/aufraeumen.sh" || strings.Join(ct.Users, " ") != "root" {
		t.Errorf("/etc/crontab braucht %v, Benutzer %v", ct.Needs, ct.Users)
	}
	rc := m["aufgabe:/var/spool/cron/crontabs/root"]
	if f := rc.Files[0]; f.How != HowCrontab || f.Owner != "root" || f.Group != "crontab" || f.Mode != 0o600 || rc.Title != "Cronjobs von root (1)" {
		t.Errorf("Crontab root: %+v %+v", rc, f)
	}
	if strings.Join(rc.Needs, " ") != "/opt/docker/pensum/backup.sh" {
		t.Errorf("braucht %v", rc.Needs)
	}
	if mz := m["aufgabe:/var/spool/cron/crontabs/demo"]; strings.Join(mz.Users, " ") != "demo" {
		t.Errorf("Crontab demo: %+v", mz)
	}
	if sk := m["aufgabe:/etc/cron.daily/logs-leeren"]; sk.Files[0].Mode != 0o755 || strings.Join(sk.Needs, " ") != "/bin/bash" {
		t.Errorf("Skript: %+v", sk)
	}
	sy := m["aufgabe:/etc/systemd/system/sync.service"]
	var paths []string
	for _, f := range sy.Files {
		paths = append(paths, f.Path)
	}
	if strings.Join(paths, " ") != "/etc/systemd/system/sync.service /etc/systemd/system/sync.timer /etc/systemd/system/sync.service.d/override.conf" {
		t.Errorf("Timer-Dateien: %v", paths)
	}
	if strings.Join(sy.Units, " ") != "sync.timer" || strings.Join(sy.Needs, " ") != "/etc/sync.env /usr/local/bin/sync-it" || sy.Title != "Timer sync (täglich)" {
		t.Errorf("Timer: %+v", sy)
	}
	if mt := m["aufgabe:/etc/systemd/system/mnt-daten.mount"]; mt.Manual == "" || mt.Selectable() || len(mt.Files) != 0 {
		t.Errorf("Laufwerk: %+v", mt)
	}
	ssh := m["zugang:ssh:/root"]
	paths = nil
	for _, f := range ssh.Files {
		paths = append(paths, f.Path+":"+f.How)
	}
	if strings.Join(paths, " ") != "/root/.ssh/id_ed25519:datei /root/.ssh/id_ed25519.pub:datei /root/.ssh/known_hosts:zeilen" || ssh.Files[0].Mode != 0o600 || ssh.Files[0].DirMode != 0o700 {
		t.Errorf("SSH: %v %+v", paths, ssh.Files[0])
	}
	if lg := m["zugang:login:/root"]; len(lg.Files[0].Lines) != 2 || lg.Title != "Login-Schlüssel für root (2)" {
		t.Errorf("Login: %+v", lg)
	}
	if !dockerHelper([]byte(`{"auths":{"ghcr.io":{}},"credsStore":"desktop"}`)) || !dockerHelper([]byte(`{"auths":{"ghcr.io":{}}}`)) || dockerHelper([]byte(`{"auths":{"ghcr.io":{"auth":"eA=="}}}`)) {
		t.Error("Schlüsselspeicher nicht erkannt")
	}
	if dk := m["zugang:docker:/root"]; strings.Join(dk.Files[0].Names, " ") != "ghcr.io" || registryNames([]string{"https://index.docker.io/v1/", "ghcr.io"}) != "Docker Hub, ghcr.io" {
		t.Errorf("Docker: %+v", dk)
	}
	if r := strings.Join(s.Rest(), " "); r != "/etc/ssh/sshd_config" {
		t.Errorf("Rest: %s", r)
	}

	// Ohne Haken nur das Automatische und das zum Nachsehen, ohne Inhalte.
	lv := Choose(s.Items(), map[string]bool{"zugang:ssh:/root": true}, false)
	var keys []string
	for _, it := range lv.Items {
		keys = append(keys, it.Key)
		for _, f := range it.Files {
			if f.Data != nil {
				t.Errorf("Vorschau mit Inhalt: %s", f.Path)
			}
		}
	}
	if strings.Join(keys, " ") != "zeitzone kernel aufgabe:/etc/systemd/system/mnt-daten.mount zugang:ssh:/root" {
		t.Errorf("gewählt: %v", keys)
	}
	b, _ := json.Marshal(lv)
	if bytes.Contains(b, []byte("PRIVAT")) || bytes.Contains(b, []byte("Z2VoZWlt")) {
		t.Errorf("Geheimnis in der Vorschau: %s", b)
	}
}

type fakeRun struct {
	root  string
	calls []string
}

func (f *fakeRun) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if name == "apt-get" {
		os.MkdirAll(filepath.Join(f.root, "/var/spool/cron/crontabs"), 0o755)
		os.MkdirAll(filepath.Join(f.root, "/usr/sbin"), 0o755)
		return nil, os.WriteFile(filepath.Join(f.root, "/usr/sbin/cron"), nil, 0o755)
	}
	return nil, nil
}

func newServer(t *testing.T) (*Target, *fakeRun, string) {
	root := t.TempDir()
	fakeIDs(t, root)
	put(t, root, "/etc/timezone", "Etc/UTC\n", 0o644)
	put(t, root, "/root/.ssh/authorized_keys", "ssh-ed25519 AAAAdemo demo@pc\n", 0o600)
	put(t, root, "/root/.docker/config.json", `{"auths":{"docker.io":{"auth":"aGllcg=="}}}`, 0o600)
	put(t, root, "/etc/systemd/system/sync.service.d/override.conf", "[Service]\nNice=5\n", 0o644)
	put(t, root, "/usr/local/bin/sync-it", "", 0o755)
	put(t, root, "/bin/bash", "", 0o755)
	fr := &fakeRun{root: root}
	return &Target{Root: root, Run: fr.run}, fr, root
}

func TestVorschauAendertNichts(t *testing.T) {
	s := oldServer(t)
	all := map[string]bool{}
	for _, it := range s.Items() {
		all[it.Key] = true
	}
	tg, fr, root := newServer(t)
	var out bytes.Buffer
	res := tg.Do(context.Background(), Choose(s.Items(), all, false), &out)
	got := out.String()
	for _, want := range []string{
		"· Zeitzone: hier Etc/UTC, am Umzugstag stellt HostDeck Europe/Berlin ein",
		"· Kernel-Einstellungen (2): käme am Umzugstag dazu",
		"· Cronjobs von root (1): cron ist hier nicht installiert",
		"! Cronjobs von demo (1): Benutzer demo gibt es hier nicht",
		"! Laufwerk mnt-daten.mount: Laufwerke sind",
		"· Timer sync (täglich): käme am Umzugstag dazu und würde eingeschaltet",
		"! Timer sync (täglich): /etc/systemd/system/sync.service.d/override.conf liegt hier schon anders",
		"! Timer sync (täglich) braucht /etc/sync.env",
		"· SSH-Schlüssel von root: id_ed25519: käme am Umzugstag dazu",
		"· Login-Schlüssel für root (2): käme am Umzugstag dazu (1 Zeile)",
		"· Docker-Anmeldung von root: ghcr.io: käme am Umzugstag dazu (1 Registry)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fehlt %q in\n%s", want, got)
		}
	}
	if len(fr.calls) != 0 {
		t.Errorf("Vorschau ruft auf: %v", fr.calls)
	}
	if _, err := os.Stat(filepath.Join(root, SysctlFile)); err == nil {
		t.Error("Vorschau hat geschrieben")
	}
	if res.Warn == 0 || res.Add == 0 {
		t.Errorf("Zählung %+v", res)
	}
}

func TestUmzugstagUebernimmt(t *testing.T) {
	s := oldServer(t)
	all := map[string]bool{}
	for _, it := range s.Items() {
		all[it.Key] = true
	}
	tg, fr, root := newServer(t)
	put(t, root, "/etc/sync.env", "X=1\n", 0o600)
	var out bytes.Buffer
	tg.Do(context.Background(), Choose(s.Items(), all, true), &out)
	got := out.String()
	for _, want := range []string{
		"✓ Zeitzone auf Europe/Berlin gestellt (vorher Etc/UTC)",
		"✓ Kernel-Einstellungen (2): übernommen",
		"✓ Cronjobs von root (1): übernommen",
		"✓ Timer sync (täglich): übernommen und eingeschaltet",
		"! Timer sync (täglich): /etc/systemd/system/sync.service.d/override.conf liegt hier schon anders",
		"✓ Login-Schlüssel für root (2): übernommen",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fehlt %q in\n%s", want, got)
		}
	}
	calls := strings.Join(fr.calls, "\n")
	for _, want := range []string{"timedatectl set-timezone Europe/Berlin", "sysctl -p " + SysctlFile, "apt-get install -y -q cron", "systemctl daemon-reload", "systemctl enable --now sync.timer"} {
		if !strings.Contains(calls, want) {
			t.Errorf("Aufruf fehlt %q:\n%s", want, calls)
		}
	}
	if b := read(t, root, "/var/spool/cron/crontabs/root"); !strings.Contains(b, "/opt/docker/pensum/backup.sh --geheim") {
		t.Errorf("Crontab: %q", b)
	}
	if fi, _ := os.Stat(filepath.Join(root, "/var/spool/cron/crontabs/root")); fi.Mode().Perm() != 0o600 {
		t.Errorf("Crontab-Rechte %v", fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Join(root, "/root/.ssh/id_ed25519")); fi.Mode().Perm() != 0o600 {
		t.Errorf("Schlüssel-Rechte %v", fi.Mode())
	}
	if b := read(t, root, "/root/.ssh/authorized_keys"); b != "ssh-ed25519 AAAAdemo demo@pc\nssh-ed25519 AAAAhandy handy\n" {
		t.Errorf("authorized_keys: %q", b)
	}
	if b := read(t, root, "/etc/systemd/system/sync.service.d/override.conf"); b != "[Service]\nNice=5\n" {
		t.Errorf("Vorhandenes überschrieben: %q", b)
	}
	var cfg struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}
	if err := json.Unmarshal([]byte(read(t, root, "/root/.docker/config.json")), &cfg); err != nil || len(cfg.Auths) != 2 {
		t.Errorf("Docker: %v %v", cfg.Auths, err)
	}

	// Ein zweites Mal ist alles schon da.
	out.Reset()
	fr.calls = nil
	put(t, root, "/etc/timezone", "Europe/Berlin\n", 0o644)
	tg.Do(context.Background(), Choose(s.Items(), all, true), &out)
	for _, want := range []string{"✓ Kernel-Einstellungen (2): schon so da", "✓ Login-Schlüssel für root (2): schon so da", "✓ Docker-Anmeldung von root: ghcr.io: schon so da"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("zweites Mal fehlt %q in\n%s", want, out.String())
		}
	}
}

func TestUmzugstagLehntSymlinkAb(t *testing.T) {
	s := oldServer(t)
	tg, _, root := newServer(t)
	os.RemoveAll(filepath.Join(root, "/root/.ssh"))
	os.Symlink(filepath.Join(root, "/etc"), filepath.Join(root, "/root/.ssh"))
	var out bytes.Buffer
	tg.Do(context.Background(), Choose(s.Items(), map[string]bool{"zugang:ssh:/root": true}, true), &out)
	if !strings.Contains(out.String(), "✗ SSH-Schlüssel von root") {
		t.Errorf("Symlink: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(root, "/etc/id_ed25519")); err == nil {
		t.Error("durch den Symlink geschrieben")
	}
}

func TestAlteAdresse(t *testing.T) {
	root := t.TempDir()
	put(t, root, "/opt/app/.env", "API=http://203.0.113.180:8080\nNICHT=1203.0.113.180\nAUCH_NICHT=203.0.113.1801\n", 0o600)
	put(t, root, "/opt/app/sub/conf.yml", "a: 1\nhost: 203.0.113.180\n", 0o644)
	put(t, root, "/opt/app/node_modules/x.js", "203.0.113.180", 0o644)
	put(t, root, "/opt/app/db/pg.conf", "203.0.113.180", 0o644)
	put(t, root, "/opt/app/bild.bin", "\x00\x01203.0.113.180", 0o644)
	put(t, root, "/etc/hosts", "203.0.113.180 vps1\n", 0o644)
	put(t, root, "/opt/app/server/logs/latest.log.txt", "203.0.113.180", 0o644)
	put(t, root, "/opt/app/app.log", "203.0.113.180", 0o644)
	put(t, root, "/opt/app/app.log.2.gz", "203.0.113.180", 0o644)
	put(t, root, "/etc/v6", "addr 2a01:4f8::1;\nnicht 2a01:4f8::10\n", 0o644)
	hits, cut := FindAddress(context.Background(), root, []string{"/opt/app", "/opt/app/sub", "/etc"}, []string{"/opt/app/db"}, []string{"203.0.113.180", "2a01:4f8::1"}, DefaultLimits)
	var got []string
	for _, h := range hits {
		got = append(got, fmt.Sprintf("%s:%d", h.Path, h.Line))
	}
	if strings.Join(got, " ") != "/etc/hosts:1 /etc/v6:1 /opt/app/.env:1 /opt/app/sub/conf.yml:2" || cut {
		t.Errorf("Funde %v (abgebrochen %v)", got, cut)
	}
	if _, cut := FindAddress(context.Background(), root, []string{"/opt/app"}, nil, []string{"203.0.113.180"}, Limits{Files: 1, Bytes: 1 << 20, Hits: 10}); !cut {
		t.Error("Grenze greift nicht")
	}
}
