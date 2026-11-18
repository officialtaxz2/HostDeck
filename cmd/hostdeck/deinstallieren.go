package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/leistung"
	"github.com/officialtaxz2/hostdeck/internal/ort"
	"github.com/officialtaxz2/hostdeck/internal/sicherheit"
	"github.com/officialtaxz2/hostdeck/internal/tuning"
)

// uninstaller nimmt HostDeck wieder vom Server. Felder für Tests.
type uninstaller struct {
	In       *installer
	StateDir string // /root/.hostdeck
	RunDirs  []string
	// Domain ist die freigeschaltete Adresse (leer: keine); Unpublish
	// nimmt sie zurück.
	Domain    string
	Unpublish func(ctx context.Context) error
	Shield    interface {
		ClosedText() string
		OpenAll(ctx context.Context) error
	}
	// Tuned: tune.sh ist angewendet; Restore nimmt es zurück.
	Tuned   bool
	Restore func(ctx context.Context, out io.Writer) (reboot bool, err error)
	Remains []string // was unter Leistung geändert ist und bleibt
	Size    func(dir string) int64
	Out     io.Writer

	// Antworten
	RestoreTuning bool
	DeleteData    bool
}

func systemUninstaller(ctx context.Context, in *installer, out io.Writer) *uninstaller {
	u := &uninstaller{
		In:      in,
		RunDirs: []string{ort.RunDir, ort.WebRunDir, ort.CaddyRunDir},
		Shield: &sicherheit.Service{
			Run:       runCombined,
			RuleFile:  filepath.Join(in.DataDir, ort.ShieldRules),
			StateFile: filepath.Join(in.DataDir, ort.ShieldFile),
			UnitFile:  filepath.Join(in.UnitDir, ort.ShieldUnit),
		},
		Tuned: tuning.ReadApplied("") != nil,
		Restore: func(ctx context.Context, out io.Writer) (bool, error) {
			o, err := tuning.FromEnv(ort.RunDir).Restore(ctx, out)
			if err == nil && o.Code != 0 {
				err = fmt.Errorf("tune.sh restore endete mit Code %d", o.Code)
			}
			return o.Reboot, err
		},
		Remains: (&leistung.Service{Dir: filepath.Join(in.DataDir, ort.PerfDir)}).Remains(),
		Size:    func(dir string) int64 { n, _ := dirSize(dir); return n },
		Out:     out,
	}
	if dir, err := stateDir(); err == nil {
		u.StateDir = dir
	}
	if st, _ := loadAccess(in.DataDir); st != nil {
		u.Domain = st.Domain
		pub := systemPublisher(in)
		u.Unpublish = func(ctx context.Context) error { return pub.Unpublish(ctx, printStep(out)) }
	}
	return u
}

func (u *uninstaller) printf(format string, args ...any) { fmt.Fprintf(u.Out, format, args...) }

// plan beschreibt, was passiert und was bleibt.
func (u *uninstaller) plan() string {
	var b strings.Builder
	n := 0
	step := func(s string) { n++; fmt.Fprintf(&b, "  %d. %s\n", n, s) }
	b.WriteString("HostDeck entfernen\n\nDas passiert:\n")
	if u.Domain != "" {
		step("Zugang von außen schließen: Caddy bedient https://" + u.Domain + " nicht mehr (mit derselben Prüfung wie beim Freischalten).")
	}
	if p := u.Shield.ClosedText(); p != "" {
		step("Port-Schutz aufheben: " + p + " ist danach wieder von außen erreichbar.")
	}
	step("Dienste " + strings.TrimSuffix(ort.UnitDienst, ".service") + " und " + strings.TrimSuffix(ort.UnitWeb, ".service") + " anhalten und entfernen.")
	step("Programm (" + u.In.ProgDir + "), Befehl hostdeck und Benutzer " + u.In.WebUser + " entfernen.")
	b.WriteString("\nBleibt, wie es ist:\n")
	b.WriteString("  - deine Projekte, Container, Caddy und alle anderen Einstellungen des Servers\n")
	for _, r := range u.Remains {
		b.WriteString("  - " + r + " (zurücknehmen vorher im Leitstand unter Leistung)\n")
	}
	if !u.DeleteData {
		b.WriteString("  - die Daten von HostDeck: " + u.dataText() + " (gleich fragt HostDeck, ob sie weg sollen)\n")
	}
	return b.String()
}

func (u *uninstaller) dataText() string {
	t := u.In.DataDir
	if u.Size != nil {
		if n := u.Size(u.In.DataDir); n > 0 {
			t += " (" + humanBytes(n) + ")"
		}
	}
	if u.StateDir != "" && exists(u.StateDir) {
		t += " und " + u.StateDir
	}
	return t
}

// ask stellt die Fragen vor dem ersten Schritt. ok false: abgebrochen.
func (u *uninstaller) ask(p prompt, yes, deleteData bool) (bool, error) {
	u.DeleteData = deleteData
	u.printf("%s\n", u.plan())
	if yes {
		u.RestoreTuning = u.Tuned
		return true, nil
	}
	a, err := p.Line("HostDeck entfernen? [j/N] ")
	if err != nil {
		return false, err
	}
	if !isYes(a, false) {
		return false, nil
	}
	if u.Tuned {
		a, err := p.Line("\nServer-Tuning (tune.sh) ist angewendet. Zurücknehmen, wie es vorher war? [J/n] ")
		if err != nil {
			return false, err
		}
		u.RestoreTuning = isYes(a, true)
	}
	if !deleteData && exists(u.In.DataDir) {
		u.printf("\nDie Daten von HostDeck: %s.\n", u.dataText())
		u.printf("Darin liegen Backups auf dem Server, Protokoll, Einstellungen und das Backup-Passwort.\n")
		u.printf("Ohne dieses Passwort lassen sich Backups, die du heruntergeladen hast, nicht mehr öffnen.\n")
		u.printf("Notiere es vorher: sudo hostdeck backup-passwort --zeigen\n")
		a, err := p.Line("Daten auch löschen? Dann „löschen“ eingeben, sonst Enter (Daten bleiben): ")
		if err != nil {
			return false, err
		}
		u.DeleteData = strings.EqualFold(strings.TrimSpace(a), "löschen")
	}
	return true, nil
}

func isYes(a string, def bool) bool {
	a = strings.ToLower(strings.TrimSpace(a))
	if a == "" {
		return def
	}
	return strings.HasPrefix(a, "j") || strings.HasPrefix(a, "y")
}

// run entfernt HostDeck. Schritte, die etwas offen ließen (Caddy, Ports),
// kommen zuerst; schlägt einer fehl, bleibt der Rest eingerichtet.
func (u *uninstaller) run(ctx context.Context) error {
	in := u.In
	if u.RestoreTuning {
		u.printf("\nNehme das Server-Tuning zurück …\n")
		reboot, err := u.Restore(ctx, u.Out)
		if err != nil {
			return fmt.Errorf("Tuning ließ sich nicht zurücknehmen: %v. HostDeck bleibt eingerichtet", err)
		}
		if reboot {
			u.printf("  Ein Neustart des Servers macht das Zurücknehmen vollständig.\n")
		}
	}
	if u.Unpublish != nil {
		u.printf("\nSchließe den Zugang von außen (https://%s) …\n", u.Domain)
		if err := u.Unpublish(ctx); err != nil {
			return fmt.Errorf("Zugang von außen: %v. HostDeck bleibt eingerichtet, bis das geklärt ist", err)
		}
	}
	if p := u.Shield.ClosedText(); p != "" {
		u.printf("\nHebe den Port-Schutz auf (%s) …\n", p)
	}
	if err := u.Shield.OpenAll(ctx); err != nil {
		return fmt.Errorf("Port-Schutz ließ sich nicht aufheben: %v. HostDeck bleibt eingerichtet", err)
	}
	u.printf("\nEntferne Dienste, Programm und Benutzer …\n")
	if err := in.remove(ctx); err != nil {
		return err
	}
	if err := os.RemoveAll(in.ProgDir); err != nil {
		return err
	}
	if in.UserExists(in.WebUser) {
		if err := in.Run(ctx, "userdel", in.WebUser); err != nil {
			u.printf("  Hinweis: %v\n", err)
		}
	}
	if _, err := user.LookupGroup(in.WebUser); err == nil {
		_ = in.Run(ctx, "groupdel", in.WebUser)
	}
	for _, d := range u.RunDirs {
		_ = os.RemoveAll(d)
	}
	if u.DeleteData {
		u.printf("Lösche die Daten (%s) …\n", u.dataText())
		for _, d := range []string{in.DataDir, u.StateDir} {
			if d == "" {
				continue
			}
			if err := os.RemoveAll(d); err != nil {
				return err
			}
		}
	}
	u.printf("\nHostDeck ist entfernt.\n")
	if !u.DeleteData && exists(in.DataDir) {
		u.printf("Die Daten liegen weiter in %s. Später löschen: sudo rm -rf %s", in.DataDir, in.DataDir)
		if u.StateDir != "" && exists(u.StateDir) {
			u.printf(" %s", u.StateDir)
		}
		u.printf("\nWer HostDeck neu einrichtet, bekommt sie zurück.\n")
	}
	for _, r := range u.Remains {
		u.printf("Bleibt: %s.\n", r)
	}
	return nil
}

func deinstallierenCmd(ctx context.Context, args []string, stdin *os.File, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("deinstallieren", flag.ContinueOnError)
	fl.SetOutput(stderr)
	yes := fl.Bool("ja", false, "")
	deleteData := fl.Bool("daten-loeschen", false, "")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if runtime.GOOS != "linux" {
		fmt.Fprintln(stderr, "hostdeck deinstallieren geht nur auf einem Linux-Server.")
		return 2
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(stderr, "hostdeck deinstallieren braucht root: sudo hostdeck deinstallieren")
		return 2
	}
	in, err := systemInstaller(stdout)
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	if !exists(in.ProgDir) && !exists(filepath.Join(in.UnitDir, ort.UnitDienst)) && !exists(in.DataDir) {
		fmt.Fprintln(stdout, "HostDeck ist auf diesem Server nicht eingerichtet. Nichts zu tun.")
		return 0
	}
	tty := newTTYPrompt(stdin, stdout)
	if !*yes && !tty.Interactive() {
		fmt.Fprintln(stderr, "Ohne Terminal nur mit --ja (Daten bleiben; mit --daten-loeschen auch die Daten).")
		return 2
	}
	lock, ok := waitForLauf(ctx, "Deinstallieren", stdout)
	if !ok {
		return 1
	}
	defer lock.Release()
	u := systemUninstaller(ctx, in, stdout)
	go_, err := u.ask(tty, *yes, *deleteData)
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	if !go_ {
		fmt.Fprintln(stdout, "Abgebrochen; nichts geändert.")
		return 0
	}
	if err := u.run(ctx); err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	return 0
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return strings.Replace(fmt.Sprintf("%.1f GB", float64(n)/(1<<30)), ".", ",", 1)
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n>>20)
	default:
		return fmt.Sprintf("%d KB", (n+1023)>>10)
	}
}
