package backup

import (
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/units"
)

func size(n int64) string {
	if n < 0 {
		return "Größe unbekannt"
	}
	return units.HumanSize(n)
}

func decisionText(d string) string {
	if d == "archive" {
		return "archivieren"
	}
	return "übernehmen"
}

func itemLine(it Item) string {
	s := it.Title
	switch it.Strategy {
	case StratDump:
		s = fmt.Sprintf("%s: %s-Dump aus %s", it.Title, it.Engine, it.ContainerName)
	case StratRaw:
		s = it.Title
	}
	if n := len(it.Excludes); n == 1 {
		s += " (ohne 1 Unterordner mit eigenem Eintrag)"
	} else if n > 1 {
		s += fmt.Sprintf(" (ohne %d Unterordner mit eigenem Eintrag)", n)
	}
	if it.Strategy != StratDump && (it.Strategy != StratHost || it.Size >= 0) {
		s += ", " + size(it.Size)
	}
	return s
}

func managedLine(m ManagedRef) string {
	name := m.Name
	if m.Label != "" {
		name = m.Label
	}
	state := "aus"
	if m.Running {
		state = "läuft, muss im Panel gestoppt werden"
	}
	return name + " " + state
}

// Write schreibt den Plan lesbar, so wie hostdeck backup --plan ihn zeigt.
func (p *Plan) Write(w io.Writer) {
	stops, dumps, live := 0, 0, 0
	var managers []string
	for _, g := range p.Groups {
		if g.NeedsStop(p.Final) {
			stops++
		}
		if len(g.Managed) > 0 {
			managers = append(managers, g.Name)
		}
		dumps += len(g.Dumps)
		live += len(g.Live) + len(g.Stopped)
	}
	live += len(p.Archive)

	fmt.Fprintf(w, "Ablauf\n")
	fmt.Fprintf(w, "  1. Vorprüfung: Auswahl, Server unverändert, Verbindung zum PC\n")
	fmt.Fprintf(w, "  2. Vorlauf, alles läuft: %d Schritte\n", live)
	if p.Final {
		fmt.Fprintf(w, "  3. Datenbanken: %d Dumps, jeweils nachdem die übrigen Dienste des Projekts gestoppt sind\n", dumps)
		fmt.Fprintf(w, "  4. Projekt für Projekt stoppen: %d Projekte; sie bleiben danach aus (letzter Lauf)\n", stops)
	} else {
		fmt.Fprintf(w, "  3. Datenbanken, laufen weiter: %d Dumps\n", dumps)
		fmt.Fprintf(w, "  4. Projekt für Projekt kurz stoppen: %d Projekte\n", stops)
	}
	if len(managers) > 0 {
		fmt.Fprintf(w, "     Zuletzt %s: verwaltete Server müssen vorher im Panel aus sein\n", strings.Join(managers, ", "))
	}
	fmt.Fprintf(w, "  5. Images: %d per docker save, %d nur mit Name und Digest\n", len(p.Images), len(p.Rebuild))
	fmt.Fprintf(w, "  6. Abschluss: Prüfung des Backups und Bericht\n")

	fmt.Fprintf(w, "\nProjekte\n")
	for _, g := range p.Groups {
		fmt.Fprintf(w, "  %s (%s)\n", g.Name, decisionText(g.Decision))
		if len(g.Managed) > 0 {
			var ms []string
			for _, m := range g.Managed {
				ms = append(ms, managedLine(m))
			}
			fmt.Fprintf(w, "    verwaltete Server: %s\n", strings.Join(ms, "; "))
		}
		if g.NeedsStop(p.Final) {
			var running, idle []string
			for _, c := range g.Containers {
				if c.Running {
					running = append(running, c.Name)
				} else {
					idle = append(idle, c.Name)
				}
			}
			verb := "stoppt kurz"
			if p.Final {
				verb = "stoppt und lässt aus"
			}
			line := fmt.Sprintf("    %s: %s", verb, strings.Join(running, ", "))
			if len(idle) > 0 {
				line += fmt.Sprintf(" (läuft schon nicht: %s)", strings.Join(idle, ", "))
			}
			fmt.Fprintln(w, line)
		} else {
			fmt.Fprintf(w, "    stoppt nichts\n")
		}
		for _, it := range g.Live {
			fmt.Fprintf(w, "    im Betrieb: %s\n", itemLine(it))
		}
		for _, it := range g.Dumps {
			fmt.Fprintf(w, "    Dump:       %s\n", itemLine(it))
		}
		for _, it := range g.Stopped {
			fmt.Fprintf(w, "    gestoppt:   %s\n", itemLine(it))
		}
	}

	if len(p.Archive) > 0 {
		fmt.Fprintf(w, "\nOhne Projekt und am Server selbst (im Betrieb)\n")
		for _, l := range archiveLines(p.Archive) {
			fmt.Fprintf(w, "  %s\n", l)
		}
	}
	if len(p.Images) > 0 {
		fmt.Fprintf(w, "\nImages per docker save\n")
		for _, it := range p.Images {
			fmt.Fprintf(w, "  %s\n", itemLine(it))
		}
	}
	if len(p.Rebuild) > 0 {
		fmt.Fprintf(w, "\nImages aus dem Netz (nur Name und Digest werden notiert)\n")
		for _, r := range p.Rebuild {
			fmt.Fprintf(w, "  %s: %s\n", r.Service, r.Image)
		}
	}
	if len(p.Skipped) > 0 {
		fmt.Fprintf(w, "\nBewusst weggelassen\n")
		for _, s := range p.Skipped {
			reason := s.Reason
			if reason == "" {
				reason = "ohne Begründung"
			}
			fmt.Fprintf(w, "  %s: %s\n", title(s.Key), reason)
		}
	}
	if len(p.Warnings) > 0 {
		fmt.Fprintf(w, "\nHinweise\n")
		for _, s := range p.Warnings {
			fmt.Fprintf(w, "  %s\n", s)
		}
	}
	total, unknown := p.TotalSize()
	steps := len(p.Items())
	fmt.Fprintf(w, "\nSumme: etwa %s in %d Schritten", units.HumanSize(total), steps)
	if unknown > 0 {
		fmt.Fprintf(w, " (%d davon ohne Größenangabe)", unknown)
	}
	fmt.Fprintln(w)
	if len(p.Problems) > 0 {
		fmt.Fprintf(w, "\nSo kann der Lauf nicht starten:\n")
		for _, s := range p.Problems {
			fmt.Fprintf(w, "  - %s\n", s)
		}
	}
}

// archiveLines fasst viele Archiv-Ordner im selben Elternordner zu einer
// Zeile zusammen, damit 30 Testergebnisse nicht den Plan füllen.
func archiveLines(items []Item) []string {
	const minGroup = 5
	parent := func(it Item) string {
		if !it.Archive || len(it.Paths) != 1 || len(it.Excludes) > 0 || !strings.HasPrefix(it.Key, "path:") {
			return ""
		}
		return path.Dir(it.Paths[0])
	}
	count := map[string]int{}
	for _, it := range items {
		if d := parent(it); d != "" {
			count[d]++
		}
	}
	var out []string
	done := map[string]bool{}
	for _, it := range items {
		d := parent(it)
		if d == "" || count[d] < minGroup {
			kind := ""
			if it.Archive {
				kind = " [Archiv]"
			}
			out = append(out, itemLine(it)+kind)
			continue
		}
		if done[d] {
			continue
		}
		done[d] = true
		var sum int64
		unknown := false
		for _, x := range items {
			if parent(x) == d {
				if x.Size < 0 {
					unknown = true
				} else {
					sum += x.Size
				}
			}
		}
		total := units.HumanSize(sum)
		if unknown {
			total += " und mehr"
		}
		out = append(out, fmt.Sprintf("%d Ordner in %s, zusammen %s [Archiv]", count[d], d, total))
	}
	return out
}
