package discover

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/units"
)

// largeLayer: ab dieser Größe der beschreibbaren Container-Schicht warnt der
// Bericht, weil die Daten beim Neuanlegen des Containers verloren gehen.
const largeLayer = 500_000_000

func (s *scan) addFinding(level, code, subject, msg string) {
	for _, f := range s.inv.Findings {
		if f.Code == code && f.Subject == subject && f.Message == msg {
			return
		}
	}
	s.inv.Findings = append(s.inv.Findings, inventory.Finding{Level: level, Code: code, Subject: subject, Message: msg})
}

// findings leitet aus dem Inventar die Hinweise für den Bericht ab.
func (s *scan) findings() {
	inv := s.inv
	if !inv.Host.Root {
		s.addFinding(inventory.LevelWarn, "kein-root", "", "HostDeck läuft nicht als root. Volume-Inhalte, Prozesse und manche Ordner sind nicht lesbar; Datenbanken in Volumes und Größen können fehlen. Bitte mit sudo starten.")
	}

	for _, p := range inv.Projects {
		switch {
		case p.WorkingDir == "":
			s.addFinding(inventory.LevelWarn, "projektordner-unbekannt", p.Key, "Für das Projekt "+p.Name+" ist kein Compose-Ordner bekannt.")
		case !p.DirExists:
			s.addFinding(inventory.LevelWarn, "projektordner-fehlt", p.Key, "Der Compose-Ordner von "+p.Name+" ("+p.WorkingDir+") fehlt. Die Container laufen noch, lassen sich aber ohne Compose-Datei nicht neu aufsetzen.")
		case p.Git == nil:
			s.addFinding(inventory.LevelInfo, "kein-git", p.Key, "Der Ordner von "+p.Name+" ist kein Git-Repo; er wird komplett gesichert.")
		case p.Git.Dirty > 0:
			s.addFinding(inventory.LevelWarn, "git-aenderungen", p.Key, fmt.Sprintf("%s hat %d nicht committete Änderung(en) in %s. Sie werden mitgesichert; besser vorher committen und pushen.", p.Name, p.Git.Dirty, p.Git.Root))
		}
		var stopped []string
		for _, ck := range p.Containers {
			if c := inv.Container(ck); c != nil && !c.Running {
				stopped = append(stopped, fmt.Sprintf("%s (%s, Exit %d)", c.Name, c.State, c.ExitCode))
			}
		}
		if len(stopped) > 0 {
			s.addFinding(inventory.LevelInfo, "gestoppt", p.Key, p.Name+": gestoppt ist "+strings.Join(stopped, ", ")+".")
		}
	}

	for _, e := range inv.Paths {
		if e.Kind == inventory.KindProject && len(e.SQLite) > 0 {
			s.addFinding(inventory.LevelInfo, "sqlite-im-projekt", e.Key, "Im Projektordner "+e.Path+" liegen SQLite-Dateien ("+shortList(e.SQLite, 3)+"). Sie werden mit dem Projektordner gesichert; dafür muss der Container gestoppt sein.")
		}
		if e.Kind == inventory.KindParent {
			var users []string
			for _, m := range e.Mounts {
				users = appendUnique(users, s.name(m.Container))
			}
			s.addFinding(inventory.LevelInfo, "elternordner", e.Key, fmt.Sprintf("%s bindet %s ein. Der Ordner enthält %d Projekte und zählt deshalb nicht als Daten von %s.", strings.Join(users, ", "), e.Path, len(e.Contains), strings.Join(users, ", ")))
		}
	}

	for _, m := range inv.Managed {
		if m.Container {
			continue
		}
		sz := ""
		if m.Size >= 0 {
			sz = ", " + units.HumanSize(m.Size)
		}
		label := ""
		if m.Label != "" {
			label = " (" + m.Label + ")"
		}
		s.addFinding(inventory.LevelInfo, "verwaltet-ohne-container", m.Key, "Für den Server "+m.Name+label+" von "+s.name(m.Manager)+" gibt es gerade keinen Container; er ist aus oder wurde im Panel gelöscht. Seine Daten ("+m.Path+sz+") werden mit "+m.Root+" gesichert. Wird er nicht mehr gebraucht, lässt er sich in der Auswahl weglassen.")
	}

	var hostNet []string
	for _, c := range inv.Containers {
		if c.ManagedBy != "" {
			s.addFinding(inventory.LevelInfo, "verwaltet", c.Key, "Container "+c.Name+" wird von "+s.name(c.ManagedBy)+" verwaltet (Ordner "+c.ManagerPath+"). Er muss vor dem Backup über den Verwalter gestoppt werden, etwa im Panel, nicht mit docker stop.")
		} else if strings.HasPrefix(c.Key, "container:") {
			s.addFinding(inventory.LevelInfo, "ohne-compose", c.Key, "Container "+c.Name+" gehört zu keinem Compose-Projekt; beim Restore muss er von Hand angelegt werden.")
		}
		if c.HostNetwork {
			hostNet = append(hostNet, c.Name)
		}
		if c.SizeRw >= largeLayer {
			s.addFinding(inventory.LevelWarn, "container-schicht", c.Key, "Im Container "+c.Name+" liegen "+units.HumanSize(c.SizeRw)+" außerhalb von Volumes. Diese Daten gehen beim Neuanlegen verloren; prüfen, ob sie gebraucht werden (oft ein Cache).")
		}
	}
	if len(hostNet) > 0 {
		sort.Strings(hostNet)
		s.addFinding(inventory.LevelInfo, "host-netz", "", "Im Host-Netz laufen "+strings.Join(hostNet, ", ")+"; ihre Ports belegen sie direkt auf dem Server.")
	}

	var unclear []string
	for _, img := range inv.Images {
		if !img.Local {
			continue
		}
		if len(img.Tags) == 0 {
			s.addFinding(inventory.LevelInfo, "image-unbenannt", "image:"+shortID(img.ID), "Das Image "+shortID(img.ID)+" (genutzt von "+s.names(img.UsedBy)+") hat keinen Namen mehr; vermutlich wurde es neu gebaut und der Name zeigt jetzt auf ein anderes Image. Es wird per ID gesichert.")
		}
		if img.Origin == inventory.OriginUnknown {
			unclear = append(unclear, imageLabel(img))
		}
		if img.Origin != inventory.OriginBuilt {
			continue
		}
		for _, ck := range img.UsedBy {
			c := inv.Container(ck)
			if c == nil || c.Project == "" {
				continue
			}
			if p := inv.Project(c.Project); p != nil && p.DirExists && p.Git == nil {
				s.addFinding(inventory.LevelWarn, "image-ohne-git", c.Key, "Das lokal gebaute Image von "+c.Name+" stammt aus einem Ordner ohne Git; ein Neubau ist nur aus der Sicherung möglich.")
			}
		}
	}

	if len(unclear) > 0 {
		sort.Strings(unclear)
		msg := "Bei einem Image sagt Docker nicht, ob es aus einer Registry stammt: %s. Zur Sicherheit wird es per docker save mitgesichert; kommt es aus einer Registry, kannst du in der Auswahl auf „Aus der Registry laden“ umstellen."
		if len(unclear) > 1 {
			msg = fmt.Sprintf("Bei %d Images sagt Docker nicht, ob sie aus einer Registry stammen: %%s. Zur Sicherheit werden sie per docker save mitgesichert; kommen sie aus einer Registry, kannst du in der Auswahl auf „Aus der Registry laden“ umstellen.", len(unclear))
		}
		s.addFinding(inventory.LevelInfo, "image-herkunft-unklar", "", fmt.Sprintf(msg, shortList(unclear, 8)))
	}

	var orphans []string
	var orphanSize int64
	for _, v := range inv.Volumes {
		if v.Orphan {
			orphans = append(orphans, displayVolume(v.Name))
			if v.Size > 0 {
				orphanSize += v.Size
			}
		}
	}
	if len(orphans) > 0 {
		s.addFinding(inventory.LevelInfo, "verwaiste-volumes", "", fmt.Sprintf("%d Volumes werden von keinem Container genutzt (zusammen %s): %s.", len(orphans), units.HumanSize(orphanSize), shortList(orphans, 12)))
	}

	if c := inv.Caddy; c != nil && c.Found {
		for _, imp := range c.Imports {
			switch {
			case !imp.Exists:
				s.addFinding(inventory.LevelWarn, "caddy-import-fehlt", "host:caddy", "Caddy importiert "+imp.Path+", die Datei fehlt. Caddy startet so nicht.")
			case imp.Outside && imp.Project != "":
				s.addFinding(inventory.LevelInfo, "caddy-import-projekt", "host:caddy", "Caddy importiert "+imp.Path+" aus dem Projekt "+short(imp.Project)+". Beim Restore muss Caddy deshalb nach den Projekten kommen.")
			case imp.Outside:
				s.addFinding(inventory.LevelInfo, "caddy-import-extern", "host:caddy", "Caddy importiert "+imp.Path+" außerhalb von "+c.Config+"; die Datei muss mitgesichert werden.")
			}
		}
		routed := map[string]bool{}
		for _, r := range c.Routes {
			if r.Target != "" {
				routed[r.Target] = true
			}
			switch r.Status {
			case inventory.RouteStopped:
				if s.userStopped()[s.name(r.Target)] {
					s.addFinding(inventory.LevelInfo, "caddy-route-pausiert", "host:caddy", fmt.Sprintf("Pausiert: %s zeigt auf Port %d; du hast %s gestoppt. Die Seite antwortet wieder, wenn du ihn startest.", strings.Join(r.Hosts, ", "), r.Port, s.name(r.Target)))
					break
				}
				s.addFinding(inventory.LevelWarn, "caddy-tote-route", "host:caddy", fmt.Sprintf("Tote Route: %s zeigt auf Port %d, der Container %s ist gestoppt.", strings.Join(r.Hosts, ", "), r.Port, s.name(r.Target)))
			case inventory.RouteNothing:
				s.addFinding(inventory.LevelWarn, "caddy-tote-route", "host:caddy", fmt.Sprintf("Tote Route: %s zeigt auf Port %d, dort lauscht nichts.", strings.Join(r.Hosts, ", "), r.Port))
			}
		}
		for _, ct := range inv.Containers {
			if !ct.Running || routed[ct.Key] || (ct.DB != nil) {
				continue
			}
			var local []string
			for _, p := range ct.Ports {
				if p.Proto == "tcp" && isLoopback(p.HostIP) {
					local = append(local, fmt.Sprint(p.HostPort))
				}
			}
			if len(local) > 0 {
				s.addFinding(inventory.LevelInfo, "ohne-domain", ct.Key, "Container "+ct.Name+" lauscht lokal auf Port "+strings.Join(local, ", ")+", aber keine Caddy-Route zeigt darauf.")
			}
		}
	}

	var public []string
	for _, ct := range inv.Containers {
		if !ct.Running {
			continue
		}
		var ports []inventory.Port
		for _, p := range ct.Ports {
			if isWildcard(p.HostIP) {
				ports = append(ports, p)
			}
		}
		if len(ports) > 0 {
			public = append(public, ct.Name+" ("+portRanges(ports)+")")
		}
	}
	if len(public) > 0 {
		sort.Strings(public)
		s.addFinding(inventory.LevelInfo, "offen", "", "Direkt aus dem Internet erreichbar, ohne Caddy: "+strings.Join(public, "; ")+".")
	}

	var ps, host []string
	for _, u := range inv.Unclaimed {
		switch u.Area {
		case inventory.AreaScan:
			ps = append(ps, u.Path)
		case inventory.AreaFound:
			s.addFinding(inventory.LevelWarn, "fund", u.Key, "Ordner außerhalb der üblichen Orte: "+u.Path+" ("+units.HumanSize(u.Size)+"). In der Auswahl entscheiden, ob er mitkommt.")
		default:
			host = append(host, u.Path)
		}
	}
	if len(ps) > 0 {
		s.addFinding(inventory.LevelInfo, "nicht-zugeordnet", "", fmt.Sprintf("%d Ordner gehören zu keinem laufenden Projekt: %s. In der Auswahl (./hostdeck serve) entscheiden, ob sie mitkommen.", len(ps), shortList(groupSiblings(ps, 5), 12)))
	}
	if len(host) > 0 {
		s.addFinding(inventory.LevelInfo, "am-server", "", "Am Server selbst gefunden: "+shortList(host, 12)+". Vorschlag: archivieren, also sichern, aber nicht automatisch zurückspielen.")
	}
	for _, u := range inv.Unclaimed {
		if u.Area == inventory.AreaHome && strings.Contains(u.Note, "privater SSH-Schlüssel") {
			s.addFinding(inventory.LevelInfo, "ssh-schluessel", u.Key, "In "+u.Path+"/.ssh liegt ein privater Schlüssel. Damit meldet sich der Server woanders an, etwa bei GitHub für git pull. Er wird mit "+u.Path+" archiviert; auf dem neuen Server zurücklegen oder neu anlegen.")
		}
	}
	if len(inv.Tasks) > 0 {
		s.addFinding(inventory.LevelInfo, "aufgaben", "", taskCount(inv.Tasks)+". Sie werden mit /etc und den Crontabs archiviert; auf dem neuen Server von Hand einrichten, wenn sie noch gebraucht werden.")
	}
	if inv.Space != nil {
		s.addFinding(inventory.LevelInfo, "platz", "", spaceText(inv.Space))
	}

	sort.SliceStable(inv.Findings, func(i, j int) bool {
		return inv.Findings[i].Level == inventory.LevelWarn && inv.Findings[j].Level != inventory.LevelWarn
	})
}

// name gibt für einen Container-Schlüssel den Containernamen zurück, den man
// aus docker ps kennt, sonst den gekürzten Schlüssel.
// displayVolume kürzt die Zufallsnamen anonymer Volumes für die Anzeige.
func displayVolume(name string) string {
	if hex64.MatchString(name) {
		return name[:12] + "… (anonym)"
	}
	return name
}

// userStopped liest einmal, was du im Leitstand gestoppt hast.
func (s *scan) userStopped() map[string]bool {
	if s.stoppedByYou == nil {
		s.stoppedByYou = map[string]bool{}
		if s.o.UserStopped != nil {
			for n := range s.o.UserStopped() {
				s.stoppedByYou[n] = true
			}
		}
	}
	return s.stoppedByYou
}

func (s *scan) name(key string) string {
	if c := s.inv.Container(key); c != nil {
		return c.Name
	}
	return short(key)
}

func (s *scan) names(keys []string) string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = s.name(k)
	}
	return strings.Join(out, ", ")
}

// shortList zählt höchstens max Einträge auf und fasst den Rest zusammen,
// damit ein Hinweis lesbar bleibt.
func shortList(items []string, max int) string {
	if len(items) <= max {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:max], ", ") + fmt.Sprintf(" und %d weitere", len(items)-max)
}

// portRanges schreibt Ports sortiert und fasst lückenlose Folgen zusammen:
// aus 51000/udp bis 51100/udp wird „51000–51100/udp (101 Ports)“.
func portRanges(ports []inventory.Port) string {
	byProto := map[string][]int{}
	for _, p := range ports {
		byProto[p.Proto] = append(byProto[p.Proto], p.HostPort)
	}
	protos := make([]string, 0, len(byProto))
	for proto := range byProto {
		protos = append(protos, proto)
	}
	sort.Strings(protos)
	var out []string
	for _, proto := range protos {
		nums := byProto[proto]
		sort.Ints(nums)
		for i := 0; i < len(nums); {
			j := i
			for j+1 < len(nums) && nums[j+1] <= nums[j]+1 {
				j++
			}
			switch {
			case nums[j]-nums[i] >= 2:
				out = append(out, fmt.Sprintf("%d–%d/%s (%d Ports)", nums[i], nums[j], proto, nums[j]-nums[i]+1))
			case nums[j] != nums[i]:
				out = append(out, fmt.Sprintf("%d/%s", nums[i], proto), fmt.Sprintf("%d/%s", nums[j], proto))
			default:
				out = append(out, fmt.Sprintf("%d/%s", nums[i], proto))
			}
			i = j + 1
		}
	}
	return strings.Join(out, ", ")
}

// groupSiblings fasst gleichartige Ordner zusammen, sobald es mindestens min
// davon gibt: gleicher Elternordner und gleicher Namensanfang bis zum ersten
// -, _ oder . wie bei /opt/docker/nekoNew/neko-hls-results-1 und
// neko-webcodecs-results-2. Daraus wird „/opt/docker/nekoNew/neko-… (40 Ordner)“.
func groupSiblings(paths []string, min int) []string {
	count := map[string]int{}
	for _, p := range paths {
		if k := siblingKey(p); k != "" {
			count[k]++
		}
	}
	var out []string
	done := map[string]bool{}
	for _, p := range paths {
		k := siblingKey(p)
		if k == "" || count[k] < min {
			out = append(out, p)
			continue
		}
		if !done[k] {
			done[k] = true
			out = append(out, fmt.Sprintf("%s… (%d Ordner)", k, count[k]))
		}
	}
	return out
}

// siblingKey ist Elternordner plus Namensanfang, "" ohne Trennzeichen im Namen.
func siblingKey(p string) string {
	dir, name := path.Split(p)
	i := strings.IndexAny(name, "-_.")
	if i <= 0 {
		return ""
	}
	return dir + name[:i+1]
}

// taskCount zählt Aufgaben nach Art, etwa „1 Cronjob und 2 Timer“.
func taskCount(tasks []inventory.Task) string {
	var cron, timer, other int
	for _, t := range tasks {
		switch t.Kind {
		case "cron":
			cron++
		case "timer":
			timer++
		default:
			other++
		}
	}
	var parts []string
	if cron > 0 {
		parts = append(parts, plural(cron, "Cronjob", "Cronjobs"))
	}
	if timer > 0 {
		parts = append(parts, plural(timer, "Timer", "Timer"))
	}
	if other > 0 {
		parts = append(parts, plural(other, "eigener Dienst", "eigene Dienste"))
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " und " + parts[len(parts)-1]
}
