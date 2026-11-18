package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/neueversion"
)

// Einrichten über eine laufende Installation (etwa install.sh mit einer
// neuen Version) geht denselben Weg wie das Update im Leitstand: alte
// Version beiseitelegen, einspielen, warten, sonst zurückholen.

// installedVersion nennt die eingerichtete Version: zuerst fragt es den
// Dienst, sonst das Programm selbst. Leer: unbekannt.
func installedVersion(ctx context.Context, prog string, running func(ctx context.Context) (string, error)) string {
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if v, err := running(c); err == nil && v != "" {
		return v
	}
	out, err := exec.CommandContext(c, prog, "version").Output()
	if err != nil {
		return ""
	}
	// „hostdeck 1.2.3“
	if f := strings.Fields(string(out)); len(f) >= 2 {
		return f[1]
	}
	return ""
}

// checkOlder hält ein stilles Zurückgehen auf eine ältere Version auf.
// Ist this älter als installed, fragt es am Terminal nach; ohne Terminal
// geht es nur mit allow (--aeltere-version).
func checkOlder(installed, this string, allow bool, p prompt) error {
	if !neueversion.Newer(this, installed) || allow {
		return nil
	}
	if !p.Interactive() {
		return fmt.Errorf("Eingerichtet ist Version %s, diese Datei ist die ältere Version %s. Nichts geändert.\nWer wirklich zurück will: mit --version %s ausdrücklich angeben (install.sh) oder einrichten --aeltere-version", installed, this, this)
	}
	p.Say("Eingerichtet ist Version %s, diese Datei ist die ältere Version %s.\n", installed, this)
	a, err := p.Line("Wirklich auf die ältere Version zurückgehen? [j/N] ")
	if err != nil {
		return fmt.Errorf("Abgebrochen; nichts geändert.")
	}
	if a = strings.TrimSpace(a); a == "" || !strings.ContainsAny(a[:1], "jJyY") {
		return fmt.Errorf("Abgebrochen; nichts geändert.")
	}
	return nil
}

// isInstalled sagt, ob HostDeck hier schon als Programm liegt.
func isInstalled(in *installer) bool {
	fi, err := os.Stat(in.prog())
	return err == nil && fi.Mode().IsRegular()
}
