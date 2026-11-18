package discover_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/fixture"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// Abnahme 13: Was außerhalb von Docker am Server liegt, wird gefunden, ohne
// Müll und ohne Geheimnisse.
func TestAbnahme13_AmServerSelbst(t *testing.T) {
	inv := fixture.Beispiel(t, 1).Run(t)
	area := map[string]inventory.Unclaimed{}
	for _, u := range inv.Unclaimed {
		area[u.Path] = u
	}
	want := map[string]string{
		"/root":                    inventory.AreaHome,
		"/home/demo":               inventory.AreaHome,
		"/var/www/alte-seite":      inventory.AreaWeb,
		"/usr/local/bin":           inventory.AreaPrograms,
		"/etc":                     inventory.AreaSettings,
		"/var/spool/cron/crontabs": inventory.AreaSettings,
		"/minecraft-alt":           inventory.AreaFound,
	}
	for p, a := range want {
		if u, ok := area[p]; !ok || u.Area != a || u.Size <= 0 {
			t.Errorf("%s: %+v, erwartet Bereich %q mit Größe", p, u, a)
		}
	}
	for _, p := range []string{"/var/www/html", "/usr/bin", "/var/log", "/swapfile", "/var/lib/dpkg", "/proc", "/vmlinuz", "/vmlinuz.old", "/initrd.img", "/initrd.img.old"} {
		if _, ok := area[p]; ok {
			t.Errorf("%s gehört nicht in die Auswahl", p)
		}
	}
	root := area["/root"]
	for _, s := range []string{"2 Login-Schlüssel", "privater SSH-Schlüssel id_ed25519", "docker login bei ghcr.io", "Skripte: backup.sh"} {
		if !strings.Contains(root.Note, s) {
			t.Errorf("/root: %q fehlt in %q", s, root.Note)
		}
	}
	if len(root.Caches) != 2 || root.Caches[0].Path != "/root/.cache" || root.Caches[1].Path != "/root/.vscode-server" || root.Caches[0].Size <= 0 {
		t.Errorf("Caches in /root: %+v", root.Caches)
	}
	if strings.Contains(root.Note, "hostdeck") {
		t.Errorf("HostDeck selbst steht bei den Skripten: %q", root.Note)
	}
	if c := area["/home/demo"].Caches; len(c) != 0 {
		t.Errorf("kleiner Cache wird abgefragt: %+v", c)
	}
	if n := area["/usr/local/bin"].Note; !strings.Contains(n, "hostdeck-sync") || strings.Contains(n, "paket-tool") || !strings.Contains(n, "per npm installiert: pm2") {
		t.Errorf("/usr/local/bin: %q", n)
	}
	if n := area["/usr/local/bin"].Note; strings.Contains(n, "HostDeck,") || strings.HasSuffix(n, " HostDeck") || strings.Contains(n, ": HostDeck") {
		t.Errorf("Verknüpfung von HostDeck selbst als eigenes Programm: %q", n)
	}

	// Aufgaben
	// Timer und Dienst sind eine Aufgabe; Zeitpläne stehen lesbar daneben.
	var tasks []string
	for _, tk := range inv.Tasks {
		tasks = append(tasks, tk.Kind+" "+tk.Source+" "+tk.Schedule+" ("+tk.When+") | "+tk.Command)
	}
	wantTasks := []string{
		"cron /etc/cron.d/caddy-reload 30 4 * * 1 (montags 04:30) | systemctl reload caddy",
		"cron /etc/cron.daily/aufraeumen täglich (täglich) | /etc/cron.daily/aufraeumen",
		"cron /var/spool/cron/crontabs/root 0 3 * * * (täglich 03:00) | PGPASSWORD=… pg_dump -h localhost app > /root/app.sql",
		"cron /var/spool/cron/crontabs/root @reboot (beim Start) | /root/backup.sh",
		"timer /etc/systemd/system/hostdeck-sync.service OnCalendar=*-*-* 02:30:00 (täglich 02:30) | /usr/local/bin/hostdeck-sync --token … /opt",
	}
	if strings.Join(tasks, "\n") != strings.Join(wantTasks, "\n") {
		t.Errorf("Aufgaben:\n%s\nerwartet:\n%s", strings.Join(tasks, "\n"), strings.Join(wantTasks, "\n"))
	}
	for _, tk := range inv.Tasks {
		if tk.Name == "hostdeck-sync.service" && (!tk.Enabled || tk.Timer != "hostdeck-sync.timer" || tk.Description != "Sync zum PC") {
			t.Errorf("Timer-Aufgabe: %+v", tk)
		}
	}
	if !hasFindingText(inv, "aufgaben", "4 Cronjobs und 1 Timer.") {
		t.Error("Hinweis zu den Aufgaben zählt falsch")
	}

	// Einstellungen: geändert laut Paketdatenbank, neu ohne erzeugte Dateien
	got := map[string]string{}
	for _, s := range inv.Settings {
		got[s.Path] = s.Change
	}
	for p, c := range map[string]string{
		"/etc/caddy/Caddyfile":                               inventory.ChangeModified,
		"/etc/sysctl.conf":                                   inventory.ChangeModified,
		"/etc/ssh/sshd_config":                               inventory.ChangeModified,
		"/etc/sysctl.d/99-eigene.conf":                       inventory.ChangeAdded,
		"/etc/docker/daemon.json":                            inventory.ChangeAdded,
		"/etc/caddy/mcserver.caddy":                          inventory.ChangeAdded,
		"/etc/systemd/system/docker.service.d/override.conf": inventory.ChangeAdded,
	} {
		if got[p] != c {
			t.Errorf("Einstellung %s: %q, erwartet %q", p, got[p], c)
		}
	}
	for _, p := range []string{"/etc/crontab", "/etc/cron.d/e2scrub_all", "/etc/os-release", "/etc/passwd", "/etc/shadow",
		"/etc/ssh/ssh_host_ed25519_key", "/etc/ssh/sshd_config.d/50-cloud-init.conf", "/etc/pam.d/common-auth",
		"/etc/cron.d/caddy-reload", "/etc/systemd/system/hostdeck-sync.service", "/etc/systemd/system/hostdeck-sync.timer", "/etc/systemd/system/snap-core22-1234.mount",
		"/etc/apt/sources.list.d/caddy-stable.list", "/etc/ufw/user.rules", "/etc/timezone", "/etc/fstab",
		// gehört zur Maschine oder entsteht von selbst, auch wenn geändert
		"/etc/ufw/ufw.conf", "/etc/crypttab", "/etc/default/grub", "/etc/mdadm/mdadm.conf", "/etc/inputrc",
		"/etc/sysctl.d/99-cloudimg-ipv6.conf", "/etc/sysctl.d/10-network-security.conf", "/etc/sysctl.d/99-sysctl.conf"} {
		if c, ok := got[p]; ok {
			t.Errorf("%s steht als %q in den Einstellungen", p, c)
		}
	}

	// Checkliste
	checks := map[string]string{}
	for _, c := range inv.Checks {
		checks[c.Title] = c.Detail
	}
	for title, detail := range map[string]string{
		"Benutzer demo anlegen":                           "Gruppen: docker, sudo",
		"SSH-Zugang einrichten":                           "PermitRootLogin prohibit-password, PasswordAuthentication no",
		"Firewall ufw ist aktiv":                          "22/tcp erlaubt, 80,443/tcp erlaubt, 19132-19133/udp erlaubt",
		"Swap-Datei /swapfile mit 4,29 GB":                "neu anlegen",
		"docker login bei ghcr.io":                        "erneut anmelden",
		"Zeitzone Europe/Berlin":                          "set-timezone",
		"4 selbst installierte Pakete":                    "apt install caddy git htop ufw; Kernel, Bootloader",
		"Kernel-Einstellungen (sysctl)":                   "net.ipv4.ip_forward=1, vm.swappiness=10, fs.inotify.max_user_watches=524288; ",
		"Zusätzliche Paketquellen":                        "dl.cloudsmith.io",
		"Eingebunden: /mnt/nas (cifs)":                    "ebenso einbinden",
		"Docker-Einstellungen in /etc/docker/daemon.json": "log-driver, log-opts",
	} {
		if d, ok := checks[title]; !ok || !strings.Contains(d, detail) {
			t.Errorf("Checkliste %q: %q, erwartet %q", title, d, detail)
		}
	}

	// Paketquellen kommen vor den Paketen; sie werden zuerst gebraucht.
	order := map[string]int{}
	for i, c := range inv.Checks {
		order[c.Title] = i
	}
	if order["Zusätzliche Paketquellen"] > order["4 selbst installierte Pakete"] {
		t.Error("Paketquellen stehen nach den Paketen")
	}
	for _, c := range inv.Checks {
		if strings.Contains(c.Detail, "grub-pc") || strings.Contains(c.Detail, "linux-generic") || strings.Contains(c.Detail, "rp_filter") ||
			strings.Contains(c.Detail, "use_tempaddr") || strings.Contains(c.Detail, "root darf") {
			t.Errorf("Checkliste %q: %q", c.Title, c.Detail)
		}
	}

	// Platz
	sp := inv.Space
	if sp == nil || sp.Used != fixture.DiskTotal-fixture.DiskFree || sp.Swap <= 0 || sp.System <= 0 || sp.Data <= 0 || sp.Volumes <= 0 ||
		sp.Docker != sp.Used-sp.Walked-sp.Volumes {
		t.Errorf("Platz: %+v", sp)
	}
	if !hasFinding(inv, "fund", "path:/minecraft-alt") || !hasFinding(inv, "ssh-schluessel", "path:/root") {
		t.Error("Hinweise zu Fund oder SSH-Schlüssel fehlen")
	}

	// Nichts davon verrät Inhalte.
	b, _ := json.Marshal(inv)
	for _, bad := range []string{"geheim", "AAAA", "Z2Vo", "credentials="} {
		if strings.Contains(string(b), bad) {
			t.Errorf("Inventar enthält %q", bad)
		}
	}
}
