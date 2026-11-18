package discover

import (
	"github.com/officialtaxz2/hostdeck/internal/ort"
	"sort"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// tasks sammelt Cronjobs und selbst angelegte systemd-Dienste und -Timer.
// Was Pakete mitbringen, bleibt draußen.
func (s *scan) tasks() {
	// Crontabs der Benutzer (crontab -e)
	for _, dir := range crontabDirs {
		for _, user := range s.crontabUsers(dir) {
			s.cronFile(dir+"/"+user, user)
		}
	}
	// /etc/crontab und /etc/cron.d: mit Benutzer-Spalte
	s.cronFile("/etc/crontab", "")
	if ents, err := s.d.FS.ReadDir("/etc/cron.d"); err == nil {
		for _, e := range ents {
			p := "/etc/cron.d/" + e.Name()
			if e.Type().IsRegular() && !strings.HasPrefix(e.Name(), ".") && !s.packageUnchanged(p) {
				s.cronFile(p, "")
			}
		}
	}
	for dir, when := range map[string]string{"/etc/cron.hourly": "stündlich", "/etc/cron.daily": "täglich", "/etc/cron.weekly": "wöchentlich", "/etc/cron.monthly": "monatlich"} {
		ents, err := s.d.FS.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			p := dir + "/" + e.Name()
			if e.Name() == ".placeholder" || !e.Type().IsRegular() || s.packageUnchanged(p) {
				continue
			}
			s.inv.Tasks = append(s.inv.Tasks, inventory.Task{Kind: "cron", Source: p, User: "root", Schedule: when, Command: p, Enabled: true})
		}
	}
	s.systemdUnits()
	for i := range s.inv.Tasks {
		s.inv.Tasks[i].When = whenText(s.inv.Tasks[i].Kind, s.inv.Tasks[i].Schedule)
	}
	sort.SliceStable(s.inv.Tasks, func(i, j int) bool {
		a, b := s.inv.Tasks[i], s.inv.Tasks[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Source < b.Source
	})
}

// packageUnchanged meldet, ob eine Datei einem Paket gehört und unverändert
// ist.
func (s *scan) packageUnchanged(p string) bool {
	if s.dpkg == nil {
		return false
	}
	if c, ok := s.dpkg.conffiles[p]; ok {
		m, ok := s.fileMD5(p)
		return ok && m == c.md5
	}
	return s.dpkg.owned[p] != ""
}

// Standardzeilen in /etc/crontab, die run-parts für cron.daily usw. starten.
func defaultCronLine(cmd string) bool {
	return strings.Contains(cmd, "run-parts") || strings.Contains(cmd, "/usr/sbin/anacron")
}

func (s *scan) cronFile(p, user string) {
	b, ok := s.readRegular(p, 1<<20)
	if !ok {
		return
	}
	system := user == ""
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		// Variablen wie MAILTO=… oder PATH=…; Werte bleiben draußen.
		if k, _, ok := strings.Cut(f[0], "="); ok && !strings.ContainsAny(k, "*/@") {
			continue
		}
		n := 5
		if strings.HasPrefix(f[0], "@") {
			n = 1
		}
		if system {
			n++
		}
		if len(f) <= n {
			continue
		}
		t := inventory.Task{Kind: "cron", Source: p, User: user, Enabled: true}
		if system {
			t.Schedule = strings.Join(f[:n-1], " ")
			t.User = f[n-1]
		} else {
			t.Schedule = strings.Join(f[:n], " ")
		}
		cmd := strings.Join(f[n:], " ")
		if p == "/etc/crontab" && defaultCronLine(cmd) {
			continue
		}
		t.Command = redact(cmd)
		s.inv.Tasks = append(s.inv.Tasks, t)
	}
}

// unitSuffixes: Arten von systemd-Units, die als eigene Aufgabe zählen.
var unitSuffixes = []string{".service", ".timer", ".path", ".socket", ".mount"}

// systemdUnits liest selbst angelegte Units in /etc/systemd/system. Pakete
// legen ihre Units unter /usr/lib ab; Symlinks dort sind nur Verweise vom
// Aktivieren. Units, die snapd erzeugt, zählen nicht.
func (s *scan) systemdUnits() {
	const dir = "/etc/systemd/system"
	ents, err := s.d.FS.ReadDir(dir)
	if err != nil {
		return
	}
	enabled := map[string]bool{}
	for _, e := range ents {
		if e.IsDir() && (strings.HasSuffix(e.Name(), ".wants") || strings.HasSuffix(e.Name(), ".requires")) {
			if sub, err := s.d.FS.ReadDir(dir + "/" + e.Name()); err == nil {
				for _, l := range sub {
					enabled[l.Name()] = true
				}
			}
		}
	}
	for _, e := range ents {
		name := e.Name()
		p := dir + "/" + name
		if !e.Type().IsRegular() || !hasUnitSuffix(name) || strings.HasPrefix(name, "snap-") || strings.HasPrefix(name, "snap.") ||
			strings.HasPrefix(name, "var-lib-snapd") || ort.IsOwnUnit(name) || s.packageUnchanged(p) {
			continue
		}
		b, ok := s.readRegular(p, 1<<20)
		if !ok {
			continue
		}
		u := parseUnit(string(b))
		kind := "dienst"
		if strings.HasSuffix(name, ".timer") {
			kind = "timer"
		}
		t := inventory.Task{Kind: kind, Source: p, Name: name, User: u["User"], Description: u["Description"], Enabled: enabled[name]}
		switch kind {
		case "timer":
			var when []string
			for _, k := range []string{"OnCalendar", "OnBootSec", "OnUnitActiveSec", "OnStartupSec"} {
				if v := u[k]; v != "" {
					when = append(when, k+"="+v)
				}
			}
			t.Schedule = strings.Join(when, ", ")
			unit := u["Unit"]
			if unit == "" {
				unit = strings.TrimSuffix(name, ".timer") + ".service"
			}
			t.Command = "startet " + unit
		default:
			if v := u["ExecStart"]; v != "" {
				t.Command = redact(strings.TrimLeft(v, "-@+!:"))
			} else if v := u["What"]; v != "" {
				t.Command = "bindet " + v + " ein"
			}
		}
		s.inv.Tasks = append(s.inv.Tasks, t)
	}
	// Ein Timer und der Dienst, den er startet, sind eine Aufgabe: Zeitplan
	// vom Timer, Befehl vom Dienst.
	merged := map[int]bool{}
	for i := range s.inv.Tasks {
		t := s.inv.Tasks[i]
		if t.Kind != "timer" || t.Timer != "" { // schon zusammengeführter Dienst
			continue
		}
		unit := strings.TrimPrefix(t.Command, "startet ")
		for j := range s.inv.Tasks {
			d := &s.inv.Tasks[j]
			if d.Kind != "dienst" || d.Name != unit || d.Timer != "" {
				continue
			}
			d.Kind, d.Schedule, d.Timer = "timer", t.Schedule, t.Name
			d.Enabled = d.Enabled || t.Enabled
			if d.Description == "" {
				d.Description = t.Description
			}
			merged[i] = true
			break
		}
	}
	out := s.inv.Tasks[:0]
	for i, t := range s.inv.Tasks {
		if !merged[i] {
			out = append(out, t)
		}
	}
	s.inv.Tasks = out
}

func hasUnitSuffix(name string) bool {
	for _, suf := range unitSuffixes {
		if strings.HasSuffix(name, suf) {
			return true
		}
	}
	return false
}

// parseUnit liest die ersten Werte einer Unit-Datei. Environment= und
// EnvironmentFile= bleiben draußen, dort stehen oft Geheimnisse.
func parseUnit(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' || line[0] == '[' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if strings.HasPrefix(k, "Environment") {
			continue
		}
		if _, seen := out[k]; !seen {
			out[k] = v
		}
	}
	return out
}
