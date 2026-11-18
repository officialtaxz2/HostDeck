package backup

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/restic"
	"github.com/officialtaxz2/hostdeck/internal/units"
)

// CheckResult ist das Ergebnis von restic check.
type CheckResult struct {
	Subset   string        `json:"subset"`
	OK       bool          `json:"ok"`
	Err      string        `json:"error,omitempty"`
	Duration time.Duration `json:"duration"`
}

// Report ist backup-bericht.txt.
type Report struct {
	Plan    *Plan
	Result  *Result
	Version string
	Where   string
	Missing []string
	Check   CheckResult
	Valid   bool
}

// Unreadable zählt die Dateien, die restic im entscheidenden Durchgang
// nicht lesen konnte. Sie fehlen im Backup.
func (r *Report) Unreadable() (n int, example string) {
	for _, s := range r.Result.Steps {
		if s.Phase == PhasePre || len(s.Unreadable) == 0 {
			continue
		}
		if n == 0 {
			example = s.Unreadable[0]
		}
		n += len(s.Unreadable)
	}
	return n, example
}

func (r *Report) verdict() string {
	n, example := r.Unreadable()
	switch {
	case r.Valid && n > 0:
		return fmt.Sprintf("GÜLTIG, aber %d Dateien waren nicht lesbar und fehlen im Backup, etwa %s.", n, example)
	case r.Valid:
		return "GÜLTIG: Alle Schritte gesichert, Prüfung bestanden."
	case r.Result.Aborted:
		return "UNGÜLTIG: Der Lauf wurde abgebrochen (" + r.Result.Err + ")."
	case r.Result.Err != "":
		return "UNGÜLTIG: " + r.Result.Err
	case len(r.Missing) > 0:
		return fmt.Sprintf("UNGÜLTIG: %d Schritte ohne Snapshot, etwa %s.", len(r.Missing), title(r.Missing[0]))
	case !r.Check.OK && r.Check.Err != "":
		return "UNGÜLTIG: Die Prüfung des Backups ist gescheitert: " + r.Check.Err
	case n > 0 && r.Result.Final:
		return fmt.Sprintf("UNGÜLTIG für den Umzug: %d Dateien waren nicht lesbar und fehlen im Backup, etwa %s.", n, example)
	}
	return "UNGÜLTIG"
}

func (r *Report) added() (added, read int64) {
	for _, s := range r.Result.Steps {
		added += s.Added
		if s.Phase == PhaseFinal {
			read += s.Bytes
		}
	}
	return added, read
}

// Text liefert den ganzen Bericht.
func (r *Report) Text() []byte {
	var b bytes.Buffer
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	res := r.Result
	kind := "normaler Lauf, Dienste wieder gestartet"
	if res.Final {
		kind = "letzter Lauf vor dem Umzug, Dienste bleiben aus"
	}
	w("hostdeck backup – Bericht\n")
	w("====================\n\n")
	w("Lauf:      %s (%s)\n", res.RunID, kind)
	w("Server:    %s\n", r.Plan.Hostname)
	if r.Where != "" {
		w("Ziel:      %s\n", r.Where)
	}
	w("Programm:  HostDeck %s, restic %s\n", r.Version, restic.Version)
	w("Beginn:    %s\n", res.Started.Local().Format("02.01.2006 15:04:05"))
	w("Ende:      %s (Dauer %s)\n", res.Finished.Local().Format("02.01.2006 15:04:05"), duration(res.Finished.Sub(res.Started)))
	added, read := r.added()
	w("Daten:     %s geprüft, %s neu übertragen (vor Kompression)\n", units.HumanSize(read), units.HumanSize(added))
	w("\nErgebnis:  %s\n", r.verdict())
	if res.Final {
		if a := r.Plan.AlwaysRestart(); len(a) > 0 {
			w("Achtung:   Startet der alte Server oder Docker neu, laufen diese Container wieder\n")
			w("           (restart: always): %s.\n", strings.Join(a, ", "))
			w("           Dann nähmen alter und neuer Server Daten an. Den alten Server bis zum Umzug\n")
			w("           nicht neu starten.\n")
		}
	}
	if r.Check.Subset != "" {
		state := "bestanden"
		if !r.Check.OK {
			state = "GESCHEITERT: " + r.Check.Err
		}
		w("Prüfung:   restic check, Struktur und Teil %s der Daten: %s (%s)\n", r.Check.Subset, state, duration(r.Check.Duration))
	}

	if len(res.Groups) > 0 {
		w("\nStillstand je Projekt\n")
		for _, g := range res.Groups {
			line := fmt.Sprintf("  %-20s %s", g.Name, duration(g.Downtime))
			if g.LeftOff {
				line += ", bleibt aus (letzter Lauf)"
			}
			if len(g.NotRunning) > 0 {
				line += ", läuft danach NICHT: " + strings.Join(g.NotRunning, ", ")
			}
			w("%s\n", line)
		}
	}

	w("\nSchritte\n")
	for _, s := range res.Steps {
		state := "ok "
		if s.Err != "" {
			state = "FEHLER"
		}
		phase := ""
		if s.Phase == PhasePre {
			phase = ", Vorlauf"
		}
		w("  [%s] %s%s: %s neu, %s, %s", state, s.Item.Title, phase, units.HumanSize(s.Added), units.HumanSize(s.Bytes), duration(s.Duration))
		if s.Snapshot != "" {
			w(", Snapshot %s", short(s.Snapshot))
		}
		w("\n")
		if s.Err != "" {
			w("         %s\n", s.Err)
		}
		if n := len(s.Unreadable); n > 0 {
			w("         %d Dateien nicht lesbar, etwa %s\n", n, s.Unreadable[0])
		}
	}
	if len(r.Missing) > 0 {
		w("\nOhne Snapshot\n")
		for _, k := range r.Missing {
			w("  %s\n", title(k))
		}
	}
	if len(r.Plan.Rebuild) > 0 {
		w("\nImages aus dem Netz (beim Restore neu geladen)\n")
		for _, img := range r.Plan.Rebuild {
			d := ""
			if len(img.Digests) > 0 {
				d = " (" + img.Digests[0] + ")"
			}
			w("  %s: %s%s\n", img.Service, img.Image, d)
		}
	}
	if len(r.Plan.Skipped) > 0 {
		w("\nBewusst weggelassen\n")
		for _, s := range r.Plan.Skipped {
			w("  %s: %s\n", title(s.Key), s.Reason)
		}
	}
	warns := append(append([]string{}, r.Plan.Warnings...), res.Warnings...)
	if len(warns) > 0 {
		w("\nHinweise\n")
		for _, s := range warns {
			w("  %s\n", s)
		}
	}
	return b.Bytes()
}

// Summary schreibt die Kurzfassung für das Ende der Ausgabe.
func (r *Report) Summary(out io.Writer) {
	res := r.Result
	fmt.Fprintf(out, "Ergebnis: %s\n", r.verdict())
	added, read := r.added()
	fmt.Fprintf(out, "Dauer %s, %s geprüft, %s neu übertragen.\n", duration(res.Finished.Sub(res.Started)), units.HumanSize(read), units.HumanSize(added))
	var longest GroupResult
	for _, g := range res.Groups {
		if g.Downtime > longest.Downtime {
			longest = g
		}
	}
	if longest.Name != "" && !res.Final {
		fmt.Fprintf(out, "Längster Stillstand: %s mit %s.\n", longest.Name, duration(longest.Downtime))
	}
	for _, g := range res.Groups {
		if len(g.NotRunning) > 0 {
			fmt.Fprintf(out, "ACHTUNG: %s läuft nach dem Start nicht: %s\n", g.Name, strings.Join(g.NotRunning, ", "))
		}
	}
	if !res.Final {
		var panel []string
		for _, g := range res.Groups {
			panel = append(panel, g.PanelStopped...)
		}
		if len(panel) > 0 {
			fmt.Fprintf(out, "Im Panel gestoppt, HostDeck startet sie nicht selbst: %s. Starte sie dort wieder, wenn sie laufen sollen.\n", strings.Join(panel, ", "))
		}
	}
	if res.Final && res.OK() {
		fmt.Fprintf(out, "Die übernommenen Dienste bleiben aus. Brichst du den Umzug ab: hostdeck dienste-starten\n")
		if a := r.Plan.AlwaysRestart(); len(a) > 0 {
			fmt.Fprintf(out, "Achtung: Starte den alten Server nicht neu. Diese Container (restart: always) liefen sonst wieder: %s\n", strings.Join(a, ", "))
		}
	}
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
