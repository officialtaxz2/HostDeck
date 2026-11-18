// HostDeck erfasst, sichert und migriert die Docker-Projekte eines Servers.
// „hostdeck discover“ erfasst den Server (nur lesend), „hostdeck serve“ zeigt die
// Auswahl im Browser, „hostdeck backup“ sichert sie mit restic auf den PC.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/betrieb"
	"github.com/officialtaxz2/hostdeck/internal/discover"
	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/hostfs"
	"github.com/officialtaxz2/hostdeck/internal/manifest"
	"github.com/officialtaxz2/hostdeck/internal/ort"
	"github.com/officialtaxz2/hostdeck/internal/outdir"
	"github.com/officialtaxz2/hostdeck/internal/report"
)

// version wird beim Bauen per -ldflags "-X main.version=..." gesetzt.
var version = "dev"

const usage = `HostDeck – Server erfassen, sichern und umziehen

Auf dem Server als Dienst (empfohlen):
  sudo ./hostdeck einrichten   HostDeck als Dienst einrichten oder auf diese Version
                          bringen: Programm nach /usr/local/lib/hostdeck, Daten
                          nach /var/lib/hostdeck, Leitstand läuft immer
  sudo hostdeck notausgang     Leitstand über einen SSH-Tunnel öffnen
  sudo hostdeck passwort       Leitstand-Passwort festlegen oder ändern
  sudo hostdeck backup-passwort
                          Backup-Passwort festlegen oder zeigen (--zeigen);
                          der Leitstand zeigt es nie an
  sudo hostdeck freischalten ADRESSE
                          Leitstand über Caddy von außen erreichbar machen,
                          etwa hostdeck.example.de (geht auch im Leitstand)
  sudo hostdeck freischalten --zuruecknehmen
                          Zugang von außen wieder schließen
  sudo hostdeck einrichten --versionshinweis an|aus
                          dazu ohne Frage festlegen, ob HostDeck einmal am Tag
                          bei GitHub nach neuen Versionen sieht
  sudo hostdeck deinstallieren
                          HostDeck wieder vom Server nehmen; zeigt vorher, was
                          passiert, und fragt, ob die Daten bleiben sollen
  sudo hostdeck leistungstest  CPU-Leistung messen (benchmark.sh: Sysbench, mpstat);
                          --sekunden N je Sysbench-Test (Standard 120),
                          --mit-geekbench auch Geekbench 6 (lädt das
                          Ergebnis öffentlich auf browser.geekbench.com).
                          Geht auch im Leitstand unter Überwachung >
                          Leistungstest
  sudo hostdeck tuning status|vorschau|anwenden|prio|zuruecknehmen
                          Server-Tuning mit tune.sh (vps-tune): Presets
                          mixed, throughput, safe; hostdeck tuning hilfe zeigt
                          alle Schalter. Geht auch im Leitstand unter
                          Überwachung > Tuning

Auf dem Server ohne Dienst:
  sudo ./hostdeck discover     Server erfassen (ändert nichts) und Bericht schreiben
  ./hostdeck serve             Auswahl im Browser: was kommt mit auf den neuen Server
  sudo ./hostdeck backup --plan
                          Probelauf: zeigt, was ein Backup tun würde
  sudo ./hostdeck dienste-starten
                          startet Container wieder, die HostDeck gestoppt hat
  sudo ./hostdeck verify       prüft einen zurückgespielten Server

Am PC (früherer Weg; hostdeck.exe ist nicht im Release, selbst bauen mit
GOOS=windows go build -o hostdeck.exe ./cmd/hostdeck; im Ordner mit
hostdeck-linux-amd64 und hostdeck-linux-arm64). Einfacher geht alles im Leitstand:
  hostdeck.exe backup --plan   Probelauf über SSH: stoppt nichts, sendet nichts
  hostdeck.exe backup          Backup auf diesen PC; beim ersten Mal fragt HostDeck nach
                          Server und Backup-Ordner
  hostdeck.exe backup --letzter-lauf
                          am Umzugstag: Dienste bleiben nach dem Backup aus
  hostdeck.exe dienste-starten falls du den Umzug abbrichst: Dienste wieder an
  hostdeck.exe ordner          Backup-Ordner neu wählen
  hostdeck.exe server          Server-Adresse ändern
  hostdeck.exe restore --plan  Vorschau: was auf einem neuen Server zurückkäme
  hostdeck.exe restore         Backup auf einen neuen Server zurückspielen; HostDeck
                          fragt nach dem Zielserver und dem Backup-Passwort
  hostdeck.exe verify          zurückgespielten Server prüfen: Dienste, Ports,
                          Datenbanken

  ./hostdeck version           Version anzeigen
  ./hostdeck help              diese Hilfe

Optionen für discover:
  --out ORDNER            Ergebnisordner (Standard: hostdeck-ergebnis)
  --no-sizes              keine Größen berechnen (schneller)
  --scan-root ORDNER      hier nach nicht zugeordneten Ordnern suchen,
                          mehrfach angebbar (Standard: /opt und /srv)
  --caddyfile DATEI       Caddy-Konfiguration (Standard: /etc/caddy/Caddyfile)
  --docker-socket DATEI   Docker-Socket (Standard: /var/run/docker.sock)
  --size-timeout DAUER    Zeitlimit pro Ordner für Größen (Standard: 3m)

Optionen für serve:
  --out ORDNER            Ergebnisordner von discover (Standard: hostdeck-ergebnis)
  --port PORT             Port auf 127.0.0.1 (Standard: 7322)

Optionen für backup:
  --plan                  nur zeigen, was passieren würde
  --letzter-lauf          übernommene Dienste bleiben danach aus
  --server BENUTZER@HOST  Server (am PC; wird gemerkt)
  --ordner ORDNER         Backup-Ordner am PC (wird gemerkt)
  --port PORT             Tunnel-Port auf dem Server (Standard: 18800)
  --out ORDNER            Ergebnisordner auf dem Server (Standard: HostDeck sucht;
                          am PC wird er gemerkt)
  --repo ZIEL             nur direkt auf dem Server: restic-Ziel, etwa ein Ordner
  --restic DATEI          eigenes restic statt des geprüften Downloads

Optionen für restore:
  --plan                  nur zeigen, was zurückkäme, und was schon da ist
  --server BENUTZER@HOST  Zielserver (am PC)
  --ordner ORDNER         Ordner mit dem Backup am PC
  --repo ZIEL             nur direkt auf dem Server: Ordner mit dem Backup
  --restic DATEI          eigenes restic statt des geprüften Downloads

Ergebnis im Ergebnisordner:
  bericht.txt          lesbarer Bericht
  inventory.json       alles Gefundene, bei jedem Lauf neu
  manifest.yaml        deine Entscheidungen; discover schreibt nur beim ersten
                       Lauf einen Vorschlag, danach änderst du sie mit hostdeck serve
  backup-bericht.txt   Ergebnis des letzten Backups
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		if doubleClicked() {
			return menu(console, stdout, func(a []string) int { return run(ctx, a, stdout, stderr) })
		}
		fmt.Fprint(stdout, usage)
		return 2
	}
	switch args[0] {
	case "discover":
		return discoverCmd(ctx, args[1:], stdout, stderr)
	case "serve":
		return serveCmd(ctx, args[1:], stdout, stderr)
	case "backup":
		return backupCmd(ctx, args[1:], stdout, stderr)
	case "dienste-starten":
		return dienstStartenCmd(ctx, args[1:], stdout, stderr)
	case "ordner":
		return ordnerCmd(args[1:], stdout, stderr)
	case "server":
		return serverCmd(args[1:], stdout, stderr)
	case "restore":
		return restoreCmd(ctx, args[1:], stdout, stderr)
	case "verify":
		return verifyCmd(ctx, args[1:], stdout, stderr)
	case "einrichten":
		return einrichtenCmd(ctx, args[1:], stdout, stderr)
	case "deinstallieren":
		return deinstallierenCmd(ctx, args[1:], os.Stdin, stdout, stderr)
	case "passwort":
		return passwortCmd(ctx, args[1:], stdout, stderr)
	case "backup-passwort":
		return backupPasswortCmd(ctx, args[1:], stdout, stderr)
	case "notausgang":
		return notausgangCmd(ctx, args[1:], stdout, stderr)
	case "freischalten":
		return freischaltenCmd(ctx, flagsFirst(args[1:]), os.Stdin, stdout, stderr)
	case "dienst":
		return dienstCmd(ctx, args[1:], stdout, stderr)
	case "leistungstest":
		return leistungstestCmd(ctx, args[1:], stdout, stderr)
	case "tuning":
		return tuningCmd(ctx, args[1:], os.Stdin, stdout, stderr)
	case "web":
		return webCmd(ctx, args[1:], stdout, stderr)
	case "agent":
		return agentCmd(ctx, os.Stdin, stdout)
	case "intern-dump":
		return internDumpCmd(ctx, args[1:], stdout, stderr)
	case "intern-image":
		return internImageCmd(ctx, args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "hostdeck", version)
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "Unbekannter Befehl %q.\n\n%s", args[0], usage)
		return 2
	}
}

// flagsFirst stellt Optionen vor die übrigen Angaben, damit etwa
// „hostdeck freischalten hostdeck.example.de --ja“ auch geht.
func flagsFirst(args []string) []string {
	var flags, rest []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
		} else {
			rest = append(rest, a)
		}
	}
	return append(flags, rest...)
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func discoverCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("discover", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.Usage = func() { fmt.Fprint(stderr, usage) }
	out := fl.String("out", "hostdeck-ergebnis", "")
	noSizes := fl.Bool("no-sizes", false, "")
	caddyfile := fl.String("caddyfile", "/etc/caddy/Caddyfile", "")
	socket := fl.String("docker-socket", dockerapi.DefaultSocket, "")
	sizeTimeout := fl.Duration("size-timeout", 3*time.Minute, "")
	var roots stringList
	fl.Var(&roots, "scan-root", "")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if fl.NArg() > 0 {
		fmt.Fprintf(stderr, "Unerwartete Angabe %q.\n", fl.Arg(0))
		return 2
	}
	explicit := false
	fl.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "out" })
	if !explicit && serviceInstalled() {
		*out = serviceResultDir
		fmt.Fprintf(stderr, "HostDeck läuft hier als Dienst; die Bestandsaufnahme landet in %s.\n", *out)
	}
	if os.Geteuid() == 0 {
		lock, ok := takeLauf("Bestandsaufnahme", stderr)
		if !ok {
			return 1
		}
		defer lock.Release()
	}
	deps := discover.Deps{
		Docker: dockerapi.New(*socket),
		FS:     hostfs.New("/"),
		Git:    discover.ExecGit{},
		Caddy:  discover.ExecCaddy{},
	}
	opts := discover.Options{
		ToolVersion: "HostDeck " + version,
		ScanRoots:   roots,
		CaddyConfig: *caddyfile,
		NoSizes:     *noSizes,
		SizeTimeout: *sizeTimeout,
		Euid:        os.Geteuid(),
		Log:         stderr,
	}
	return runDiscover(ctx, deps, opts, *out, stdout, stderr)
}

func runDiscover(ctx context.Context, deps discover.Deps, opts discover.Options, outDir string, stdout, stderr io.Writer) int {
	if opts.UserStopped == nil {
		// Was du im Leitstand gestoppt hast, ist keine Warnung wert.
		opts.UserStopped = func() map[string]time.Time { return betrieb.LoadStopped(filepath.Join(ort.DataDir, ort.StoppedFile)) }
	}
	if opts.Euid != 0 {
		fmt.Fprintln(stderr, "Hinweis: HostDeck läuft nicht als root; manches ist dann nicht lesbar. Besser: sudo ./hostdeck discover")
	}
	inv, err := discover.Run(ctx, deps, opts)
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		switch msg := err.Error(); {
		case errors.Is(err, fs.ErrPermission) || strings.Contains(msg, "permission denied"):
			fmt.Fprintln(stderr, "Kein Zugriff auf Docker. Bitte mit sudo starten: sudo ./hostdeck discover")
		case strings.Contains(msg, "no such file") || strings.Contains(msg, "connection refused"):
			fmt.Fprintln(stderr, "Docker läuft nicht oder der Socket liegt woanders (Option --docker-socket).")
		}
		return 1
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		fmt.Fprintf(stderr, "Fehler: Ergebnisordner %s: %v\n", outDir, err)
		return 1
	}
	invJSON, err := inv.Marshal()
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	if err := outdir.WritePrivate(filepath.Join(outDir, "inventory.json"), invJSON); err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}

	// Im Datenordner des Dienstes läuft die Bestandsaufnahme für den Leitstand.
	in := report.Input{Inv: inv, OutDir: outDir, Leitstand: filepath.Clean(outDir) == ort.ResultDir}
	code := 0
	mPath := filepath.Join(outDir, "manifest.yaml")
	existing, err := os.ReadFile(mPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		m := manifest.Propose(inv)
		b, err := m.Marshal()
		if err == nil {
			err = outdir.WritePrivate(mPath, b)
		}
		if err != nil {
			fmt.Fprintf(stderr, "Fehler: manifest.yaml: %v\n", err)
			return 1
		}
		in.Manifest, in.State = m, report.ManifestCreated
	case err != nil:
		fmt.Fprintf(stderr, "Fehler: manifest.yaml: %v\n", err)
		return 1
	default:
		m, perr := manifest.Parse(bytes.NewReader(existing))
		if perr != nil {
			in.Manifest, in.State, in.ManifestError = manifest.Propose(inv), report.ManifestInvalid, perr
			code = 1
		} else {
			in.Manifest, in.State, in.Diff = m, report.ManifestKept, manifest.Compare(m, inv)
		}
	}

	var full bytes.Buffer
	report.Full(&full, in)
	if err := outdir.WritePrivate(filepath.Join(outDir, "bericht.txt"), full.Bytes()); err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	if opts.Euid == 0 {
		outdir.GiveToSudoUser(outDir, "inventory.json", "manifest.yaml", "bericht.txt")
	}
	fmt.Fprintln(stdout)
	report.Summary(stdout, in)
	fmt.Fprintf(stdout, "\nAusführlicher Bericht: %s\n", filepath.Join(outDir, "bericht.txt"))
	return code
}
