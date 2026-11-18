package discover

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/units"
)

// checks stellt die Checkliste für den neuen Server zusammen: Dinge, die
// nicht als Datei mitkommen, sondern neu eingerichtet werden.
func (s *scan) checks() {
	s.checkUsers()
	s.checkSSH()
	s.checkFirewall()
	s.checkSwap()
	s.checkDocker()
	s.checkTimezone()
	s.checkSysctl()
	s.checkPackages()
	s.checkMounts()
}

func (s *scan) addCheck(area, title, detail string) {
	s.inv.Checks = append(s.inv.Checks, inventory.Check{Area: area, Title: title, Detail: detail})
}

// checkUsers nennt Benutzerkonten von Menschen (UID ab 1000) und ihre
// wichtigen Gruppen.
func (s *scan) checkUsers() {
	b, ok := s.readRegular("/etc/passwd", 4<<20)
	if !ok {
		return
	}
	groups := map[string][]string{}
	if g, ok := s.readRegular("/etc/group", 4<<20); ok {
		for _, line := range strings.Split(string(g), "\n") {
			f := strings.Split(line, ":")
			if len(f) < 4 || f[3] == "" {
				continue
			}
			for _, u := range strings.Split(f[3], ",") {
				groups[u] = append(groups[u], f[0])
			}
		}
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 7 {
			continue
		}
		uid, err := strconv.Atoi(f[2])
		if err != nil || uid < 1000 || uid >= 60000 || strings.HasSuffix(f[6], "nologin") || strings.HasSuffix(f[6], "/false") {
			continue
		}
		detail := "Home " + f[5]
		if gs := groups[f[0]]; len(gs) > 0 {
			sort.Strings(gs)
			detail += ", Gruppen: " + strings.Join(gs, ", ")
		}
		s.addCheck("benutzer", "Benutzer "+f[0]+" anlegen", detail)
	}
}

// sshKeys sind Einstellungen von sshd, die für den Zugang wichtig sind.
var sshKeys = []string{"Port", "ListenAddress", "PermitRootLogin", "PasswordAuthentication", "PubkeyAuthentication",
	"KbdInteractiveAuthentication", "AuthenticationMethods", "AllowUsers", "AllowGroups"}

// checkSSH liest die gesetzten Werte aus sshd_config und den Dateien in
// sshd_config.d. Wie bei sshd gilt der erste Wert.
func (s *scan) checkSSH() {
	files := []string{}
	if ents, err := s.d.FS.ReadDir("/etc/ssh/sshd_config.d"); err == nil {
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".conf") {
				files = append(files, "/etc/ssh/sshd_config.d/"+e.Name())
			}
		}
	}
	sort.Strings(files)
	files = append([]string{"/etc/ssh/sshd_config"}, files...)
	set := map[string]string{}
	for _, p := range files {
		b, ok := s.readRegular(p, 1<<20)
		if !ok {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 || strings.HasPrefix(f[0], "#") {
				continue
			}
			if strings.EqualFold(f[0], "Match") {
				break // ab hier gelten Werte nur für bestimmte Verbindungen
			}
			for _, k := range sshKeys {
				if strings.EqualFold(f[0], k) {
					if _, seen := set[k]; !seen {
						set[k] = strings.Join(f[1:], " ")
					}
				}
			}
		}
	}
	var parts []string
	for _, k := range sshKeys {
		if v, ok := set[k]; ok {
			parts = append(parts, k+" "+v)
		}
	}
	keys := 0
	for _, h := range s.homeDirs() {
		keys += s.authorizedKeys(h)
	}
	if len(parts) == 0 && keys == 0 {
		return
	}
	detail := strings.Join(parts, ", ")
	if strings.EqualFold(set["PermitRootLogin"], "yes") && strings.EqualFold(set["PasswordAuthentication"], "yes") {
		detail += "; root darf sich mit Passwort anmelden, sicherer ist nur mit Schlüssel"
	}
	if keys > 0 {
		detail = strings.TrimPrefix(detail+"; "+plural(keys, "Login-Schlüssel", "Login-Schlüssel")+" in authorized_keys", "; ")
	}
	s.addCheck("ssh", "SSH-Zugang einrichten", detail)
}

// checkFirewall erkennt ufw, nftables und gespeicherte iptables-Regeln.
func (s *scan) checkFirewall() {
	if b, ok := s.readRegular("/etc/ufw/ufw.conf", 64<<10); ok && strings.Contains(string(b), "ENABLED=yes") {
		var rules []string
		seen := map[string]bool{}
		for _, f := range []string{"/etc/ufw/user.rules", "/etc/ufw/user6.rules"} {
			r, ok := s.readRegular(f, 4<<20)
			if !ok {
				continue
			}
			for _, line := range strings.Split(string(r), "\n") {
				t, ok := strings.CutPrefix(line, "### tuple ### ")
				if !ok {
					continue
				}
				if rule := ufwRule(t); rule != "" && !seen[rule] {
					seen[rule] = true
					rules = append(rules, rule)
				}
			}
		}
		detail := "keine eigenen Regeln"
		if len(rules) > 0 {
			detail = strings.Join(rules, ", ")
		}
		s.addCheck("firewall", "Firewall ufw ist aktiv", detail)
		return
	}
	if s.dpkg != nil {
		if c, ok := s.dpkg.conffiles["/etc/nftables.conf"]; ok {
			if m, ok := s.fileMD5("/etc/nftables.conf"); ok && m != c.md5 {
				s.addCheck("firewall", "Eigene nftables-Regeln", "in /etc/nftables.conf; wird mit /etc archiviert")
				return
			}
		}
	}
	for _, f := range []string{"/etc/iptables/rules.v4", "/etc/iptables/rules.v6"} {
		if s.isRegular(f) {
			s.addCheck("firewall", "Gespeicherte iptables-Regeln", "in /etc/iptables; wird mit /etc archiviert")
			return
		}
	}
	s.addCheck("firewall", "Keine Firewall auf dem Server gefunden", "ufw ist aus, keine eigenen nftables- oder iptables-Regeln. Docker öffnet veröffentlichte Ports ohnehin an der Firewall vorbei.")
}

// ufwRule macht aus „allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in“ den Text
// „22/tcp erlaubt“.
func ufwRule(t string) string {
	f := strings.Fields(t)
	if len(f) < 7 {
		return ""
	}
	action, proto, dport, src := f[0], f[1], f[2], f[5]
	verb := map[string]string{"allow": "erlaubt", "deny": "gesperrt", "reject": "abgewiesen", "limit": "begrenzt"}[action]
	if verb == "" {
		verb = action
	}
	port := strings.ReplaceAll(dport, ":", "-")
	if proto != "any" {
		port += "/" + proto
	}
	if src != "0.0.0.0/0" && src != "::/0" {
		port += " von " + src
	}
	return port + " " + verb
}

func (s *scan) checkSwap() {
	b, ok := s.readRegular("/proc/swaps", 64<<10)
	if !ok {
		b, ok = s.readProc("/proc/swaps")
	}
	if !ok {
		return
	}
	for _, line := range strings.Split(string(b), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		kb, _ := strconv.ParseInt(f[2], 10, 64)
		size := units.HumanSize(kb * 1024)
		switch f[1] {
		case "file":
			s.addCheck("swap", "Swap-Datei "+f[0]+" mit "+size, "auf dem neuen Server neu anlegen (fallocate, mkswap, Eintrag in /etc/fstab)")
		case "partition":
			s.addCheck("swap", "Swap-Partition mit "+size, "kommt meist mit dem neuen System; sonst eine Swap-Datei anlegen")
		}
	}
}

// readProc liest Dateien unter /proc, die Lstat als leer meldet.
func (s *scan) readProc(p string) ([]byte, bool) {
	b, err := s.d.FS.ReadFile(p, 1<<20)
	return b, err == nil
}

func (s *scan) checkDocker() {
	if b, ok := s.readRegular("/etc/docker/daemon.json", 1<<20); ok {
		var cfg map[string]json.RawMessage
		if json.Unmarshal(b, &cfg) == nil && len(cfg) > 0 {
			keys := make([]string, 0, len(cfg))
			for k := range cfg {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			s.addCheck("docker", "Docker-Einstellungen in /etc/docker/daemon.json", "setzt "+strings.Join(keys, ", ")+"; Datei liegt im Archiv von /etc")
		}
	}
	if ents, err := s.d.FS.ReadDir("/etc/systemd/system/docker.service.d"); err == nil && len(ents) > 0 {
		s.addCheck("docker", "Docker-Dienst angepasst", "eigene Einstellungen in /etc/systemd/system/docker.service.d")
	}
	if rd := s.inv.Docker.RootDir; rd != "" && rd != "/var/lib/docker" {
		s.addCheck("docker", "Docker speichert in "+rd, "auf dem neuen Server ebenso einstellen (data-root)")
	}
	logins := map[string][]string{}
	for _, h := range s.homeDirs() {
		for _, r := range s.dockerLogins(h) {
			logins[r] = append(logins[r], h)
		}
	}
	if len(logins) > 0 {
		regs := make([]string, 0, len(logins))
		for r := range logins {
			regs = append(regs, r)
		}
		sort.Strings(regs)
		s.addCheck("docker", "docker login bei "+strings.Join(regs, ", "), "auf dem neuen Server erneut anmelden, sonst lassen sich private Images nicht laden")
	}
}

func (s *scan) checkTimezone() {
	tz := ""
	if b, ok := s.readRegular("/etc/timezone", 4<<10); ok {
		tz = strings.TrimSpace(string(b))
	}
	if tz == "" {
		if t, err := s.d.FS.Readlink("/etc/localtime"); err == nil {
			if _, after, ok := strings.Cut(t, "zoneinfo/"); ok {
				tz = after
			}
		}
	}
	if tz != "" && tz != "Etc/UTC" && tz != "UTC" {
		s.addCheck("zeitzone", "Zeitzone "+tz, "timedatectl set-timezone "+tz)
	}
}

// checkPackages nennt Pakete, die jemand mit apt install installiert hat
// (laut /var/log/apt/history.log) und die noch installiert sind, sowie
// zusätzliche Paketquellen.
func (s *scan) checkPackages() {
	if s.dpkg != nil {
		var logs []string
		if ents, err := s.d.FS.ReadDir("/var/log/apt"); err == nil {
			for _, e := range ents {
				if strings.HasPrefix(e.Name(), "history.log") {
					logs = append(logs, "/var/log/apt/"+e.Name())
				}
			}
		}
		set := map[string]bool{}
		for _, p := range logs {
			b, ok := s.readRegular(p, 64<<20)
			if !ok {
				continue
			}
			if strings.HasSuffix(p, ".gz") {
				zr, err := gzip.NewReader(bytes.NewReader(b))
				if err != nil {
					continue
				}
				b, err = io.ReadAll(io.LimitReader(zr, 256<<20))
				if err != nil {
					continue
				}
			}
			for _, line := range strings.Split(string(b), "\n") {
				for _, pkg := range installedByHand(line) {
					if s.dpkg.installed[pkg] {
						set[pkg] = true
					}
				}
			}
		}
		// Paketquellen zuerst: ohne sie lassen sich manche Pakete nicht installieren.
		if hosts := s.extraSources(); len(hosts) > 0 {
			s.addCheck("pakete", "Zusätzliche Paketquellen", strings.Join(hosts, ", ")+"; vor der Installation der Pakete einrichten")
		}
		var pkgs []string
		machine := 0
		for p := range set {
			if machinePackage(p) {
				machine++
				continue
			}
			pkgs = append(pkgs, p)
		}
		sort.Strings(pkgs)
		if len(pkgs) > 0 {
			detail := "apt install " + strings.Join(pkgs, " ")
			if machine > 0 {
				detail += "; Kernel, Bootloader und Gast-Werkzeuge bringt der neue Server selbst mit"
			}
			s.addCheck("pakete", plural(len(pkgs), "selbst installiertes Paket", "selbst installierte Pakete"), detail)
		}
		return
	}
	if hosts := s.extraSources(); len(hosts) > 0 {
		s.addCheck("pakete", "Zusätzliche Paketquellen", strings.Join(hosts, ", ")+"; vor der Installation der Pakete einrichten")
	}
}

// machinePackage: Pakete für Kernel, Start und die Virtualisierung des
// Anbieters. Der neue Server hat seine eigenen; sie gehören nicht in die
// Liste zum Nachinstallieren.
func machinePackage(p string) bool {
	for _, pre := range []string{"linux-", "grub-", "grub2-", "shim-", "efibootmgr", "qemu-guest-agent", "open-vm-tools",
		"cloud-init", "cloud-guest-utils", "cloud-initramfs-", "hyperv-daemons", "xe-guest-utilities", "amd64-microcode", "intel-microcode"} {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return strings.HasSuffix(p, "-firmware") || p == "firmware-linux"
}

// checkSysctl nennt Kernel-Einstellungen, die jemand selbst gesetzt hat,
// etwa ip_forward für ein VPN. Werte von sysctl sind keine Geheimnisse.
func (s *scan) checkSysctl() {
	files := []string{"/etc/sysctl.conf"}
	if ents, err := s.d.FS.ReadDir("/etc/sysctl.d"); err == nil {
		for _, e := range ents {
			if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".conf") {
				files = append(files, "/etc/sysctl.d/"+e.Name())
			}
		}
	}
	var keys []string
	val := map[string]string{}
	for _, p := range files {
		if s.packageUnchanged(p) || generatedEtcFile(relEtc(p)) {
			continue
		}
		b, ok := s.readRegular(p, 1<<20)
		if !ok {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || line[0] == '#' || line[0] == ';' {
				continue
			}
			k, v, ok := strings.Cut(strings.TrimPrefix(line, "-"), "=")
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if !ok || k == "" || strings.ContainsAny(k, " \t") {
				continue
			}
			if _, seen := val[k]; !seen {
				keys = append(keys, k)
			}
			val[k] = v
		}
	}
	if len(keys) == 0 {
		return
	}
	var parts []string
	for _, k := range keys {
		parts = append(parts, redact(k+"="+val[k]))
	}
	detail := shortList(parts, 10)
	s.addCheck("kernel", "Kernel-Einstellungen (sysctl)", detail+"; auf dem neuen Server in /etc/sysctl.d/ eintragen")
}

// installedByHand liest aus „Commandline: apt install -y git htop“ die
// Paketnamen. Neuinstallationen (--reinstall) und lokale .deb-Dateien
// zählen nicht.
func installedByHand(line string) []string {
	cmd, ok := strings.CutPrefix(line, "Commandline: ")
	if !ok {
		return nil
	}
	f := strings.Fields(cmd)
	i := -1
	for j, w := range f {
		if w == "install" {
			i = j
			break
		}
		if w == "--reinstall" {
			return nil
		}
	}
	if i < 0 || len(f) == 0 || !(strings.HasSuffix(f[0], "apt") || strings.HasSuffix(f[0], "apt-get") || strings.HasSuffix(f[0], "aptitude")) {
		return nil
	}
	var out []string
	for _, w := range f[i+1:] {
		if strings.HasPrefix(w, "-") {
			if w == "--reinstall" {
				return nil
			}
			continue
		}
		if strings.Contains(w, "/") || strings.HasSuffix(w, ".deb") {
			continue
		}
		w, _, _ = strings.Cut(w, "=")
		w, _, _ = strings.Cut(w, ":")
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

// distroHosts sind die Paketquellen der Distribution selbst.
var distroHosts = []string{"ubuntu.com", "debian.org", "canonical.com"}

// extraSources nennt die Server zusätzlicher Paketquellen, ohne Zugangsdaten.
func (s *scan) extraSources() []string {
	ents, err := s.d.FS.ReadDir("/etc/apt/sources.list.d")
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".list") && !strings.HasSuffix(e.Name(), ".sources") {
			continue
		}
		b, ok := s.readRegular("/etc/apt/sources.list.d/"+e.Name(), 1<<20)
		if !ok {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#") {
				continue
			}
			for _, w := range strings.Fields(line) {
				if !strings.Contains(w, "://") {
					continue
				}
				u, err := url.Parse(w)
				if err != nil || u.Host == "" {
					continue
				}
				h := u.Hostname()
				distro := false
				for _, d := range distroHosts {
					if h == d || strings.HasSuffix(h, "."+d) {
						distro = true
					}
				}
				if !distro {
					if h == "ppa.launchpadcontent.net" || h == "ppa.launchpad.net" {
						h = "ppa:" + strings.Join(strings.Split(strings.Trim(u.Path, "/"), "/")[:min(2, len(strings.Split(strings.Trim(u.Path, "/"), "/")))], "/")
					}
					set[h] = true
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// checkMounts nennt zusätzliche Platten und Netzlaufwerke aus /etc/fstab.
// Optionen bleiben draußen, dort stehen manchmal Zugangsdaten.
func (s *scan) checkMounts() {
	b, ok := s.readRegular("/etc/fstab", 1<<20)
	if !ok {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || strings.HasPrefix(f[0], "#") {
			continue
		}
		mp, typ := f[1], f[2]
		switch {
		case mp == "/" || mp == "/boot" || mp == "/boot/efi" || mp == "none" || typ == "swap":
			continue
		case typ == "proc" || typ == "tmpfs" || typ == "sysfs" || typ == "devpts":
			continue
		}
		s.addCheck("platten", "Eingebunden: "+mp+" ("+typ+")", "auf dem neuen Server ebenso einbinden; Daten dort sind nicht Teil dieses Servers")
	}
}
