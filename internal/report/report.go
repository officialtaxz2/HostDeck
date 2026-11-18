// Package report schreibt den deutschen Textbericht von hostdeck discover.
package report

import (
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/manifest"
	"github.com/officialtaxz2/hostdeck/internal/units"
)

// ManifestState beschreibt, was mit manifest.yaml passiert ist.
type ManifestState int

const (
	ManifestCreated ManifestState = iota // neu als Vorschlag geschrieben
	ManifestKept                         // vorhanden, unverändert gelassen
	ManifestInvalid                      // vorhanden, aber ungültig
)

type Input struct {
	Inv           *inventory.Inventory
	Manifest      *manifest.Manifest // aktives Manifest; bei ManifestInvalid der Vorschlag
	State         ManifestState
	Diff          manifest.Diff
	ManifestError error
	OutDir        string
	// Leitstand: Die Bestandsaufnahme gehört zu einer Installation als
	// Dienst; ausgewählt wird dann im Leitstand, nicht mit „hostdeck serve“.
	Leitstand bool
}

// Summary ist die Kurzfassung für das Terminal.
func Summary(w io.Writer, in Input) {
	overview(w, in)
	fmt.Fprintln(w)
	findings(w, in.Inv, 5)
	fmt.Fprintln(w)
	manifestSection(w, in)
}

// Full ist der vollständige Bericht für bericht.txt.
func Full(w io.Writer, in Input) {
	inv := in.Inv
	title(w, "hostdeck discover – Bericht")
	fmt.Fprintf(w, "Server:   %s (%s, %s", or(inv.Host.Hostname, "?"), or(inv.Host.OS, "unbekanntes System"), inv.Host.Arch)
	if inv.Host.Kernel != "" {
		fmt.Fprintf(w, ", Kernel %s", inv.Host.Kernel)
	}
	fmt.Fprintln(w, ")")
	fmt.Fprintf(w, "Docker:   %s (API %s, Speichertreiber %s, Datenordner %s)\n", inv.Docker.Version, inv.Docker.APIVersion, inv.Docker.StorageDriver, inv.Docker.RootDir)
	fmt.Fprintf(w, "Erstellt: %s von %s\n", inv.CreatedAt.In(time.UTC).Format("02.01.2006 15:04 UTC"), inv.Tool)
	fmt.Fprintln(w, "Dieser Lauf hat nichts am Server geändert.")
	fmt.Fprintln(w)
	overview(w, in)
	fmt.Fprintln(w)
	findings(w, inv, 0)
	fmt.Fprintln(w)
	projects(w, in)
	unassigned(w, in)
	atHost(w, in)
	checklist(w, inv)
	system(w, inv)
	caddy(w, inv)
	hostPorts(w, inv)
	manifestSection(w, in)
}

func title(w io.Writer, t string) {
	fmt.Fprintln(w, t)
	fmt.Fprintln(w, strings.Repeat("=", len([]rune(t))))
}

func section(w io.Writer, t string) {
	fmt.Fprintln(w, t)
	fmt.Fprintln(w, strings.Repeat("-", len([]rune(t))))
}

func overview(w io.Writer, in Input) {
	inv := in.Inv
	section(w, "Übersicht")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	running, managed := 0, []string{}
	var hostNet []string
	for _, m := range inv.Managed {
		state := "läuft"
		switch {
		case !m.Container:
			state = "aus, kein Container"
		case !m.Running:
			state = "gestoppt"
		}
		name := shortName(m.Name)
		if m.Label != "" {
			name += " " + m.Label
		}
		managed = append(managed, fmt.Sprintf("%s (von %s, %s)", name, ctrName(inv, m.Manager), state))
	}
	for _, c := range inv.Containers {
		if c.Running {
			running++
		}
		if c.HostNetwork {
			hostNet = append(hostNet, c.Name)
		}
	}
	fmt.Fprintf(tw, "Container:\t%d (%d laufen, %d gestoppt)\n", len(inv.Containers), running, len(inv.Containers)-running)
	fmt.Fprintf(tw, "Compose-Projekte:\t%d\n", len(inv.Projects))
	if len(managed) > 0 {
		fmt.Fprintf(tw, "Verwaltete Server:\t%s\n", strings.Join(managed, ", "))
	}
	if len(hostNet) > 0 {
		fmt.Fprintf(tw, "Im Host-Netz:\t%s\n", strings.Join(hostNet, ", "))
	}
	var dbs, caches []string
	for _, c := range inv.Containers {
		if c.DB == nil {
			continue
		}
		label := engineName(c.DB.Engine)
		if c.DB.Version != "" {
			label += " " + c.DB.Version
		}
		label += " (" + c.Name + ")"
		if c.DB.Engine == "redis" || c.DB.Engine == "valkey" {
			caches = append(caches, label)
		} else {
			dbs = append(dbs, label)
		}
	}
	sort.Strings(dbs)
	sort.Strings(caches)
	if len(dbs) > 0 {
		fmt.Fprintf(tw, "Datenbanken:\t%d: %s\n", len(dbs), strings.Join(dbs, ", "))
	}
	if len(caches) > 0 {
		fmt.Fprintf(tw, "Redis/Valkey:\t%s\n", strings.Join(caches, ", "))
	}
	orphans, orphanSize := 0, int64(0)
	for _, v := range inv.Volumes {
		if v.Orphan {
			orphans++
			if v.Size > 0 {
				orphanSize += v.Size
			}
		}
	}
	fmt.Fprintf(tw, "Volumes:\t%d (%d genutzt, %d verwaist", len(inv.Volumes), len(inv.Volumes)-orphans, orphans)
	if orphanSize > 0 {
		fmt.Fprintf(tw, ", verwaist zusammen %s", units.HumanSize(orphanSize))
	}
	fmt.Fprintln(tw, ")")
	saved, unclear := 0, 0
	for _, img := range inv.Images {
		if img.Local && img.Running {
			saved++
			if img.Origin == inventory.OriginUnknown {
				unclear++
			}
		}
	}
	fmt.Fprintf(tw, "Images zum Mitsichern:\t%d in laufenden Containern (lokal gebaut oder ohne Namen", saved)
	if unclear > 0 {
		fmt.Fprintf(tw, ", davon %d mit unklarer Herkunft", unclear)
	}
	fmt.Fprintln(tw, ")")
	if c := inv.Caddy; c != nil && c.Found {
		dead := 0
		for _, r := range c.Routes {
			if r.Status == inventory.RouteStopped || r.Status == inventory.RouteNothing {
				dead++
			}
		}
		fmt.Fprintf(tw, "Caddy:\t%d Routen, davon %d tot; %d Imports\n", len(c.Routes), dead, len(c.Imports))
	}
	if total, ok := dataTotal(inv); ok {
		fmt.Fprintf(tw, "Daten der Projekte:\t%s (ohne Images, verwaiste Volumes und nicht zugeordnete Ordner)\n", units.HumanSize(total))
	}
	if sp := inv.Space; sp != nil {
		fmt.Fprintf(tw, "Platte:\t%s von %s belegt: Daten %s, Docker-Images und Build-Cache %s, System %s, Swap %s, Kleinteiliges %s\n",
			units.HumanSize(sp.Used), units.HumanSize(sp.Total), units.HumanSize(sp.Data+sp.Volumes), units.HumanSize(sp.Docker),
			units.HumanSize(sp.System), units.HumanSize(sp.Swap), units.HumanSize(sp.Other))
	}
	if len(inv.Tasks) > 0 || len(inv.Checks) > 0 {
		var parts []string
		if len(inv.Tasks) > 0 {
			parts = append(parts, fmt.Sprintf("%d Aufgabe%s (Cronjobs, Timer, Dienste)", len(inv.Tasks), plural(len(inv.Tasks), "n")))
		}
		parts = append(parts, fmt.Sprintf("%d Punkt%s auf der Checkliste", len(inv.Checks), plural(len(inv.Checks), "e")))
		if len(inv.Settings) > 0 {
			parts = append(parts, fmt.Sprintf("%d geänderte oder eigene Datei%s in /etc", len(inv.Settings), plural(len(inv.Settings), "en")))
		}
		fmt.Fprintf(tw, "Am Server selbst:\t%s\n", strings.Join(parts, ", "))
	}
	tw.Flush()
}

// dataTotal summiert, was mit den Projekten gesichert würde.
func dataTotal(inv *inventory.Inventory) (int64, bool) {
	var total int64
	known := false
	for _, e := range inv.Paths {
		if e.Kind == inventory.KindParent || e.CoveredBy != "" || e.Size < 0 {
			continue
		}
		total += e.Size
		known = true
	}
	for _, v := range inv.Volumes {
		if !v.Orphan && v.Size >= 0 {
			total += v.Size
			known = true
		}
	}
	return total, known
}

// findings listet alle Warnungen und höchstens maxInfo Hinweise (0 = alle).
func findings(w io.Writer, inv *inventory.Inventory, maxInfo int) {
	warn, info := 0, 0
	for _, f := range inv.Findings {
		if f.Level == inventory.LevelWarn {
			warn++
		} else {
			info++
		}
	}
	section(w, fmt.Sprintf("Hinweise (%d Warnungen, %d Hinweise)", warn, info))
	if len(inv.Findings) == 0 {
		fmt.Fprintln(w, "Keine.")
		return
	}
	shown := 0
	for _, f := range inv.Findings {
		if f.Level == inventory.LevelWarn {
			fmt.Fprintf(w, "! %s\n", f.Message)
			continue
		}
		if maxInfo > 0 && shown == maxInfo {
			fmt.Fprintf(w, "… und %d weitere Hinweise in bericht.txt\n", info-shown)
			break
		}
		shown++
		fmt.Fprintf(w, "- %s\n", f.Message)
	}
}

func projects(w io.Writer, in Input) {
	inv := in.Inv
	section(w, "Projekte")
	for _, p := range inv.Projects {
		mp := (*manifest.Project)(nil)
		if in.Manifest != nil {
			mp = in.Manifest.Projects[p.Name]
		}
		head := p.Name
		if mp != nil {
			label := decisionLabel(mp.Decision)
			if in.State != ManifestKept {
				label = "Vorschlag: " + label
			}
			head += "  [" + label + "]"
		}
		fmt.Fprintln(w, head)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		dir := p.Path
		if p.WorkingDir != "" && p.WorkingDir != p.Path {
			dir += " (Compose-Datei in " + p.WorkingDir + ")"
		}
		if !p.DirExists {
			dir += "  FEHLT"
		} else if e := inv.Path("path:" + p.Path); e != nil && e.Size >= 0 {
			dir += "  " + units.HumanSize(e.Size)
		}
		fmt.Fprintf(tw, "  Ordner:\t%s\n", dir)
		if g := p.Git; g != nil {
			s := or(g.Remote, "ohne Remote")
			if g.Branch != "" || g.Commit != "" {
				s += ", " + g.Branch + " @ " + shortHash(g.Commit)
			}
			if g.Dirty > 0 {
				s += fmt.Sprintf(", %d nicht committete Änderung(en)", g.Dirty)
			}
			fmt.Fprintf(tw, "  Git:\t%s\n", s)
		} else if p.DirExists {
			fmt.Fprintf(tw, "  Git:\tkein Git-Repo\n")
		}
		if len(p.Domains) > 0 {
			fmt.Fprintf(tw, "  Domains:\t%s\n", strings.Join(p.Domains, ", "))
		}
		var ctrs []*inventory.Container
		for _, ck := range p.Containers {
			if c := inv.Container(ck); c != nil {
				ctrs = append(ctrs, c)
			}
		}
		sort.Slice(ctrs, func(i, j int) bool { return ctrs[i].Name < ctrs[j].Name })
		first := true
		for _, c := range ctrs {
			label := "  Container:"
			if !first {
				label = ""
			}
			first = false
			state := "läuft"
			if !c.Running {
				state = fmt.Sprintf("%s (Exit %d)", c.State, c.ExitCode)
			}
			origin := originText(c.ImageOrigin, c.ImageLocal)
			extra := ""
			if mp != nil && mp.Services[c.Service] != nil {
				extra = " → " + mp.Services[c.Service].Image
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s (%s%s)\n", label, c.Name, state, imageName(inv, c), origin, extra)
		}
		for _, mk := range p.Managed {
			m := inv.ManagedServer(mk)
			if m == nil {
				continue
			}
			state := "läuft"
			switch {
			case !m.Container:
				state = "aus, kein Container"
			case !m.Running:
				state = m.State
			}
			where := m.Path
			if where == "" {
				where = m.Root
			}
			if m.Size >= 0 {
				where += " (" + units.HumanSize(m.Size) + ")"
			}
			label := ""
			if m.Label != "" {
				label = m.Label + ", "
			}
			fmt.Fprintf(tw, "  Verwaltet:\t%s\t%s\t%svon %s, Daten in %s\n", m.Name, state, label, ctrName(inv, m.Manager), where)
		}
		tw.Flush()
		dataLines(w, inv, p, mp)
		fmt.Fprintln(w)
	}
}

func dataLines(w io.Writer, inv *inventory.Inventory, p inventory.Project, mp *manifest.Project) {
	type row struct{ key, what, size, strategy string }
	var rows []row
	strategyOf := func(key string) string {
		if mp == nil || mp.Data[key] == nil {
			return ""
		}
		return "→ " + mp.Data[key].Strategy
	}
	for _, e := range inv.Paths {
		if e.Owner != p.Key || e.Kind == inventory.KindProject || e.Kind == inventory.KindParent {
			continue
		}
		if e.CoveredBy != "" {
			continue
		}
		rows = append(rows, row{e.Key, describe(e.DB, e.SQLite, e.Kind), sizeOf(e.Size, e.Exists), strategyOf(e.Key)})
		for _, l := range e.Largest {
			rows = append(rows, row{"    " + l.Path, "Unterordner", units.HumanSize(l.Size), ""})
		}
	}
	for _, v := range inv.Volumes {
		if v.Project != p.Key || v.Orphan {
			continue
		}
		rows = append(rows, row{v.Key, describe(v.DB, v.SQLite, "volume"), sizeOf(v.Size, true), strategyOf(v.Key)})
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, "  Daten:      keine eigenen Datenordner (nur der Projektordner)")
		return
	}
	fmt.Fprintln(w, "  Daten:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		fmt.Fprintf(tw, "    %s\t%s\t%s\t%s\n", r.key, r.what, r.size, r.strategy)
	}
	tw.Flush()
}

func describe(db *inventory.DB, sqlite []string, kind string) string {
	switch {
	case db != nil:
		s := engineName(db.Engine)
		if db.Version != "" {
			s += " " + db.Version
		}
		return s
	case len(sqlite) > 0:
		return fmt.Sprintf("SQLite (%d Datei%s)", len(sqlite), plural(len(sqlite), "en"))
	case kind == inventory.KindConfig:
		return "Datei"
	case kind == "volume":
		return "Volume"
	}
	return "Ordner"
}

func unassigned(w io.Writer, in Input) {
	inv := in.Inv
	var orphans []inventory.Volume
	for _, v := range inv.Volumes {
		if v.Orphan {
			orphans = append(orphans, v)
		}
	}
	var loose []inventory.Unclaimed
	for _, u := range inv.Unclaimed {
		if u.Area == inventory.AreaScan {
			loose = append(loose, u)
		}
	}
	if len(orphans) == 0 && len(loose) == 0 {
		return
	}
	section(w, "Ohne Projekt")
	strategyOf := func(key string) string {
		if in.Manifest == nil || in.Manifest.Unassigned[key] == nil {
			return ""
		}
		return "→ " + in.Manifest.Unassigned[key].Strategy
	}
	if len(orphans) > 0 {
		fmt.Fprintln(w, "Verwaiste Volumes (kein Container nutzt sie):")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, v := range orphans {
			from := ""
			if v.FormerProj != "" {
				from = "früher " + short(v.FormerProj)
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", volumeName(v), describe(v.DB, v.SQLite, "volume"), sizeOf(v.Size, true), from, strategyOf(v.Key))
		}
		tw.Flush()
	}
	if len(loose) > 0 {
		fmt.Fprintln(w, "Nicht zugeordnete Ordner:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, u := range loose {
			git := ""
			if u.Git != nil {
				git = "Git: " + or(u.Git.Remote, "ohne Remote")
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", u.Path, sizeOf(u.Size, true), git, strategyOf(u.Key))
		}
		tw.Flush()
	}
	fmt.Fprintln(w)
}

var areaLabels = map[string]string{
	inventory.AreaHome:     "Home-Ordner",
	inventory.AreaWeb:      "Webseite",
	inventory.AreaPrograms: "Programme",
	inventory.AreaSettings: "Einstellungen",
	inventory.AreaFound:    "Fund",
}

// atHost zeigt, was außerhalb von Docker am Server liegt.
func atHost(w io.Writer, in Input) {
	inv := in.Inv
	var host []inventory.Unclaimed
	for _, u := range inv.Unclaimed {
		if u.Area != inventory.AreaScan {
			host = append(host, u)
		}
	}
	if len(host) == 0 && len(inv.Tasks) == 0 && len(inv.Settings) == 0 {
		return
	}
	section(w, "Am Server selbst")
	strategyOf := func(key string) string {
		if in.Manifest == nil || in.Manifest.Unassigned[key] == nil {
			return ""
		}
		return "→ " + in.Manifest.Unassigned[key].Strategy
	}
	if len(host) > 0 {
		fmt.Fprintln(w, "Ordner (Vorschlag: archivieren, also sichern, aber nicht automatisch zurückspielen):")
		pw := 0
		for _, u := range host {
			pw = max(pw, len([]rune(u.Path)))
		}
		for _, u := range host {
			fmt.Fprintf(w, "  %s  %-13s  %8s  %s\n", padRight(u.Path, pw), areaLabels[u.Area], sizeOf(u.Size, true), strategyOf(u.Key))
			var extra []string
			if u.Note != "" {
				extra = append(extra, u.Note)
			}
			for _, c := range u.Caches {
				extra = append(extra, "Cache "+strings.TrimPrefix(c.Path, u.Path+"/")+" ("+sizeOf(c.Size, true)+") "+strategyOf("path:"+c.Path))
			}
			for _, e := range extra {
				fmt.Fprintf(w, "  %s  %s\n", strings.Repeat(" ", pw), e)
			}
		}
	}
	if len(inv.Tasks) > 0 {
		fmt.Fprintln(w, "Zeitgesteuerte Aufgaben und eigene Dienste:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, t := range inv.Tasks {
			what := t.Command
			if t.Description != "" {
				what = t.Description + ": " + what
			}
			state := ""
			if t.Kind != "cron" && !t.Enabled {
				state = " (nicht aktiviert)"
			}
			kind := map[string]string{"cron": "Cron", "timer": "Timer", "dienst": "Dienst"}[t.Kind]
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s%s\n", kind, or(t.When, or(t.Schedule, "–")), or(t.User, "root"), what, state)
		}
		tw.Flush()
	}
	if len(inv.Settings) > 0 {
		fmt.Fprintln(w, "Geänderte oder eigene Dateien in /etc (liegen im Archiv von /etc):")
		for _, line := range settingLines(inv.Settings) {
			fmt.Fprintln(w, "  "+line)
		}
	}
	fmt.Fprintln(w)
}

// settingLines fasst die Dateien nach Ordnern zusammen.
func settingLines(list []inventory.Setting) []string {
	type group struct{ mod, add []string }
	groups := map[string]*group{}
	var dirs []string
	for _, st := range list {
		dir := path.Dir(st.Path)
		g := groups[dir]
		if g == nil {
			g = &group{}
			groups[dir] = g
			dirs = append(dirs, dir)
		}
		name := path.Base(st.Path)
		if st.Change == inventory.ChangeModified {
			g.mod = append(g.mod, name)
		} else {
			g.add = append(g.add, name)
		}
	}
	sort.Strings(dirs)
	var out []string
	for _, d := range dirs {
		g := groups[d]
		var parts []string
		if len(g.mod) > 0 {
			parts = append(parts, "geändert: "+listShort(g.mod, 6))
		}
		if len(g.add) > 0 {
			parts = append(parts, "eigene: "+listShort(g.add, 6))
		}
		out = append(out, d+"/  "+strings.Join(parts, "; "))
	}
	return out
}

func padRight(s string, n int) string {
	if d := n - len([]rune(s)); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func listShort(items []string, max int) string {
	if len(items) <= max {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:max], ", ") + fmt.Sprintf(" und %d weitere", len(items)-max)
}

// checklist zeigt, was auf dem neuen Server von Hand eingerichtet wird.
func checklist(w io.Writer, inv *inventory.Inventory) {
	if len(inv.Checks) == 0 {
		return
	}
	section(w, "Checkliste für den neuen Server")
	for _, c := range inv.Checks {
		fmt.Fprintf(w, "[ ] %s\n", c.Title)
		if c.Detail != "" {
			fmt.Fprintf(w, "    %s\n", c.Detail)
		}
	}
	fmt.Fprintln(w)
}

func system(w io.Writer, inv *inventory.Inventory) {
	if len(inv.System) == 0 {
		return
	}
	section(w, "Systempfade (werden nie gesichert)")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, s := range inv.System {
		fmt.Fprintf(tw, "  %s\t%s\t→ %s\n", ctrName(inv, s.Container), s.Source, s.Destination)
	}
	tw.Flush()
	for _, e := range inv.Paths {
		if e.Kind == inventory.KindParent {
			var users []string
			for _, m := range e.Mounts {
				users = append(users, ctrName(inv, m.Container))
			}
			fmt.Fprintf(w, "  %s bindet %s ein: Elternordner von %d Projekten, keine Daten von %s.\n", strings.Join(users, ", "), e.Path, len(e.Contains), strings.Join(users, ", "))
		}
	}
	fmt.Fprintln(w)
}

func caddy(w io.Writer, inv *inventory.Inventory) {
	c := inv.Caddy
	if c == nil {
		return
	}
	section(w, "Caddy")
	if !c.Found {
		fmt.Fprintf(w, "Keine Caddy-Konfiguration unter %s gefunden.\n\n", c.Config)
		return
	}
	fmt.Fprintf(w, "Konfiguration: %s\n", c.Config)
	if len(c.Imports) > 0 {
		fmt.Fprintln(w, "Imports:")
		for _, imp := range c.Imports {
			note := ""
			switch {
			case !imp.Exists:
				note = "  FEHLT"
			case imp.Project != "":
				note = "  (im Projekt " + short(imp.Project) + ")"
			case imp.Outside:
				note = "  (außerhalb von " + c.Config + ")"
			}
			fmt.Fprintf(w, "  %s%s\n", imp.Path, note)
		}
	}
	if c.Error != "" {
		fmt.Fprintf(w, "Routen nicht lesbar: %s\n\n", c.Error)
		return
	}
	fmt.Fprintln(w, "Routen:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range c.Routes {
		target := r.Upstream
		if r.Handler != "reverse_proxy" {
			target = r.Handler
		}
		who := ctrName(inv, r.Target)
		if strings.HasPrefix(r.Target, "host") {
			who = "Host-Dienst " + strings.TrimPrefix(strings.TrimPrefix(r.Target, "host"), ":")
		}
		fmt.Fprintf(tw, "  %s\t→ %s\t%s\t%s\n", strings.Join(r.Hosts, ", "), target, who, routeStatus(r.Status))
	}
	tw.Flush()
	var other []string
	for _, f := range c.Files {
		if f == c.Config {
			continue
		}
		used := false
		for _, imp := range c.Imports {
			if imp.Path == f {
				used = true
			}
		}
		if !used {
			other = append(other, f)
		}
	}
	if len(other) > 0 {
		fmt.Fprintf(w, "Weitere Dateien, die Caddy nicht lädt: %s\n", strings.Join(other, ", "))
	}
	fmt.Fprintln(w)
}

func hostPorts(w io.Writer, inv *inventory.Inventory) {
	var rows []inventory.Listener
	for _, l := range inv.Listeners {
		if l.Process == "docker-proxy" {
			continue
		}
		rows = append(rows, l)
	}
	if len(rows) == 0 {
		return
	}
	section(w, "Offene Ports auf dem Host (ohne Docker-Weiterleitungen)")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, l := range rows {
		who := or(l.Process, "?")
		if l.Container != "" {
			who += " (Container " + ctrName(inv, l.Container) + ")"
		}
		fmt.Fprintf(tw, "  %d/%s\t%s\t%s\n", l.Port, l.Proto, l.Addr, who)
	}
	tw.Flush()
	fmt.Fprintln(w)
}

func manifestSection(w io.Writer, in Input) {
	section(w, "Manifest")
	dir := or(in.OutDir, ".")
	switch in.State {
	case ManifestCreated:
		open := countOpen(in.Manifest)
		fmt.Fprintf(w, "%s/manifest.yaml wurde als Vorschlag neu geschrieben.\n", dir)
		if open > 0 {
			fmt.Fprintf(w, "%s noch offen (open) und %s deine Entscheidung.\n", openText(open), pick(open, "braucht", "brauchen"))
		}
		fmt.Fprintln(w, "Ein neuer Lauf überschreibt ihn nicht.")
		if in.Leitstand {
			fmt.Fprintln(w, "Was bei einem Umzug mitkommt, legst du im Leitstand unter „Umzug“ fest, wenn einer ansteht.")
		} else {
			fmt.Fprintln(w, "Nächster Schritt: im Browser auswählen, was mitkommt, mit ./hostdeck serve")
		}
	case ManifestKept:
		fmt.Fprintf(w, "%s/manifest.yaml ist vorhanden und wurde nicht verändert.\n", dir)
		if in.Diff.Empty() {
			fmt.Fprintln(w, "Keine neuen und keine weggefallenen Einträge.")
		}
		if len(in.Diff.New) > 0 {
			fmt.Fprintf(w, "Neu auf dem Server, noch ohne Entscheidung (%d):\n", len(in.Diff.New))
			for _, k := range in.Diff.New {
				fmt.Fprintf(w, "  + %s\n", k)
			}
		}
		if len(in.Diff.Gone) > 0 {
			fmt.Fprintf(w, "Im Manifest, aber nicht mehr auf dem Server (%d):\n", len(in.Diff.Gone))
			for _, k := range in.Diff.Gone {
				fmt.Fprintf(w, "  - %s\n", k)
			}
		}
		if open := countOpen(in.Manifest); open > 0 {
			fmt.Fprintf(w, "%s noch offen (open).\n", openText(open))
		}
		wrong := builtButPulled(in.Inv, in.Manifest)
		if len(wrong) > 0 {
			fmt.Fprintf(w, "Bei %d %s steht image: rebuild, das Image ist aber auf diesem Server gebaut: %s.\n", len(wrong), pick(len(wrong), "Dienst", "Diensten"), strings.Join(wrong, ", "))
			fmt.Fprintln(w, "Aus einer Registry ließe es sich vermutlich nicht laden; besser image-save.")
		}
		if len(in.Diff.New) > 0 || len(in.Diff.Gone) > 0 || countOpen(in.Manifest) > 0 || len(wrong) > 0 {
			if in.Leitstand {
				fmt.Fprintln(w, "Entscheiden kannst du das im Leitstand unter „Umzug“.")
			} else {
				fmt.Fprintln(w, "Entscheiden kannst du das im Browser mit ./hostdeck serve")
			}
		}
	case ManifestInvalid:
		fmt.Fprintf(w, "%s/manifest.yaml ist vorhanden, aber ungültig und wurde nicht verändert:\n", dir)
		fmt.Fprintf(w, "%v\n", in.ManifestError)
		if in.Leitstand {
			fmt.Fprintln(w, "Korrigiere die Datei oder speichere die Auswahl im Leitstand unter „Umzug“ neu; das legt ein gültiges Manifest an.")
		} else {
			fmt.Fprintln(w, "Korrigiere die Datei oder öffne ./hostdeck serve; dort legt Speichern ein gültiges Manifest an.")
		}
	}
}

func countOpen(m *manifest.Manifest) int {
	if m == nil {
		return 0
	}
	n := 0
	for _, p := range m.Projects {
		if p.Decision == "open" {
			n++
		}
		for _, d := range p.Data {
			if d.Strategy == "open" {
				n++
			}
		}
	}
	for _, d := range m.Unassigned {
		if d.Strategy == "open" {
			n++
		}
	}
	for _, c := range m.Containers {
		if c.Decision == "open" {
			n++
		}
	}
	return n
}

func decisionLabel(d string) string {
	switch d {
	case "take":
		return "übernehmen"
	case "archive":
		return "archivieren"
	case "skip":
		return "weglassen"
	case "open":
		return "offen"
	}
	return d
}

func routeStatus(s string) string {
	switch s {
	case inventory.RouteOK:
		return "ok"
	case inventory.RouteStopped:
		return "TOT: Container gestoppt"
	case inventory.RouteNothing:
		return "TOT: nichts lauscht"
	case inventory.RouteExternal:
		return "externes Ziel"
	case inventory.RouteNoProxy:
		return ""
	}
	return s
}

func engineName(e string) string {
	switch e {
	case "postgres":
		return "PostgreSQL"
	case "mysql":
		return "MySQL"
	case "mariadb":
		return "MariaDB"
	case "mongo":
		return "MongoDB"
	case "redis":
		return "Redis"
	case "valkey":
		return "Valkey"
	case "sqlite":
		return "SQLite"
	}
	return e
}

// builtButPulled listet Dienste, die laut Manifest aus der Registry kommen
// sollen, deren Image discover aber als lokal gebaut erkannt hat. Das passiert,
// wenn das Manifest mit einer älteren HostDeck-Version entstanden ist.
func builtButPulled(inv *inventory.Inventory, m *manifest.Manifest) []string {
	if inv == nil || m == nil {
		return nil
	}
	var out []string
	for _, c := range inv.Containers {
		if c.ImageOrigin != inventory.OriginBuilt || c.Project == "" {
			continue
		}
		p := inv.Project(c.Project)
		if p == nil {
			continue
		}
		pr := m.Projects[p.Name]
		if pr == nil || (pr.Decision != "take" && pr.Decision != "archive") {
			continue
		}
		if s := pr.Services[c.Service]; s != nil && s.Image == "rebuild" {
			out = appendOnce(out, p.Name+"/"+c.Service)
		}
	}
	sort.Strings(out)
	return out
}

func appendOnce(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// openText: „1 Eintrag ist“ oder „3 Einträge sind“.
func openText(n int) string {
	if n == 1 {
		return "1 Eintrag ist"
	}
	return fmt.Sprintf("%d Einträge sind", n)
}

func pick(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// originText beschreibt die Herkunft eines Images. Ältere Inventare kennen
// nur ImageLocal.
func originText(origin string, local bool) string {
	switch {
	case origin == inventory.OriginUnknown:
		return "Herkunft unklar"
	case origin == inventory.OriginBuilt, origin == "" && local:
		return "lokal gebaut"
	}
	return "Registry"
}

func imageName(inv *inventory.Inventory, c *inventory.Container) string {
	for _, img := range inv.Images {
		if img.ID == c.ImageID && len(img.Tags) == 0 {
			return "Image ohne Namen " + shortHash(strings.TrimPrefix(img.ID, "sha256:"))
		}
	}
	return c.Image
}

func sizeOf(sz int64, exists bool) string {
	if !exists {
		return "fehlt"
	}
	if sz < 0 {
		return "?"
	}
	return units.HumanSize(sz)
}

// ctrName gibt den Containernamen zu einem Schlüssel zurück, wie ihn docker ps
// zeigt; für andere Schlüssel die gekürzte Form.
func ctrName(inv *inventory.Inventory, key string) string {
	if c := inv.Container(key); c != nil {
		return c.Name
	}
	return short(key)
}

// volumeName kürzt die Zufallsnamen anonymer Volumes.
func volumeName(v inventory.Volume) string {
	if v.Anonymous && len(v.Name) == 64 {
		return v.Name[:12] + "… (anonym)"
	}
	return v.Name
}

func short(key string) string {
	for _, p := range []string{"project:", "compose:", "container:", "managed:"} {
		if strings.HasPrefix(key, p) {
			return shortName(strings.TrimPrefix(key, p))
		}
	}
	return key
}

// shortName kürzt lange UUID-Namen wie c324292c-dd72-… für die Anzeige.
func shortName(n string) string {
	if len(n) == 36 && strings.Count(n, "-") == 4 {
		return n[:8] + "…"
	}
	return n
}

func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func plural(n int, suffix string) string {
	if n == 1 {
		return ""
	}
	return suffix
}
