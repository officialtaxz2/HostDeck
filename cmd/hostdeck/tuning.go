package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/tuning"
)

const tuningUsage = `hostdeck tuning – tune.sh (vps-tune ` + tuning.Version + `) über HostDeck

  sudo hostdeck tuning status                       Ist-Zustand, ändert nichts
  sudo hostdeck tuning vorschau PRESET [Schalter]   apply mit --dry-run
  sudo hostdeck tuning anwenden PRESET [Schalter]   apply; fragt bei mitigations=off
                                               und Co. nach (--yes überspringt)
  sudo hostdeck tuning prio DIENST high|normal|low  CPU-Vorrang eines systemd-Dienstes
  sudo hostdeck tuning zuruecknehmen [--dry-run]    restore: alles wie vor vps-tune
  hostdeck tuning hilfe                             Hilfe von tune.sh (Presets, Schalter)

Presets: mixed, throughput, safe. Geht auch im Leitstand unter
Überwachung > Tuning.
`

// tuningCmd reicht an das eingebettete tune.sh durch, etwa gleich nach
// einer Neuinstallation. Ändernde Befehle nehmen die Laufsperre wie im
// Leitstand.
func tuningCmd(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, tuningUsage)
		return 2
	}
	var script []string
	change := true
	switch args[0] {
	case "status":
		script, change = []string{"status"}, false
	case "vorschau":
		script, change = append(append([]string{"apply"}, args[1:]...), "--dry-run"), false
	case "anwenden":
		script = append([]string{"apply"}, args[1:]...)
	case "prio":
		script = append([]string{"prio"}, args[1:]...)
	case "zuruecknehmen":
		script = append([]string{"restore"}, args[1:]...)
		change = !(len(args) > 1 && args[1] == "--dry-run")
	case "hilfe", "help", "--help", "-h":
		script, change = []string{"--help"}, false
	default:
		fmt.Fprintf(stderr, "Unbekannt: %s\n\n%s", args[0], tuningUsage)
		return 2
	}
	if script[0] == "apply" && len(script) > 1 && script[1] == "--dry-run" {
		fmt.Fprintln(stderr, "Preset fehlt: mixed, throughput oder safe.")
		return 2
	}
	if script[0] != "--help" && os.Geteuid() != 0 {
		fmt.Fprintln(stderr, "Tuning braucht root: sudo hostdeck tuning "+args[0])
		return 2
	}
	s := tuning.FromEnv("")
	if os.Geteuid() != 0 {
		s.RunDir = os.TempDir() // nur die Hilfe; /run/hostdeck gehört root
	}
	if change {
		l, err := lauf.Take(lauf.Tuning)
		var busy *lauf.BusyError
		if errors.As(err, &busy) {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err != nil {
			fmt.Fprintf(stderr, "Fehler: %v\n", err)
			return 1
		}
		defer l.Release()
	}
	// Strg+C erreicht das Skript direkt über das Terminal; HostDeck wartet, bis
	// es fertig ist, statt es abzuwürgen.
	o, err := s.Exec(context.WithoutCancel(ctx), script, stdin, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	if change && o.Reboot && o.Code == 0 {
		fmt.Fprintln(stdout, "Neu starten: sudo systemctl reboot, oder im Leitstand unter Überwachung > Tuning.")
	}
	return o.Code
}
