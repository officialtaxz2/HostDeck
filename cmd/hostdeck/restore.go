package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/ort"
	"github.com/officialtaxz2/hostdeck/internal/pc"
	"github.com/officialtaxz2/hostdeck/internal/remote"
	"github.com/officialtaxz2/hostdeck/internal/restic"
	"github.com/officialtaxz2/hostdeck/internal/restore"
)

func restoreCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("restore", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.Usage = func() { fmt.Fprint(stderr, usage) }
	plan := fl.Bool("plan", false, "")
	server := fl.String("server", "", "")
	folder := fl.String("ordner", "", "")
	port := fl.Int("port", remote.DefaultPort, "")
	repo := fl.String("repo", "", "")
	resticPath := fl.String("restic", "", "")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if fl.NArg() > 0 {
		fmt.Fprintf(stderr, "Unerwartete Angabe %q.\n", fl.Arg(0))
		return 2
	}
	if onPC(*server) {
		return pcRestore(remote.Config{Action: remote.ActionRestore, Plan: *plan, Port: *port}, *server, *folder, stdout, stderr)
	}
	if *repo == "" {
		fmt.Fprintln(stderr, "Auf einen neuen Server zieht HostDeck im Leitstand unter Umzug (Probe-Umzug und Umzugstag).")
		fmt.Fprintln(stderr, "Direkt auf dem Server geht es nur mit einem Ziel: sudo ./hostdeck restore --repo <Ordner mit dem Backup>")
		return 2
	}
	return serverRestore(ctx, restoreRun{
		plan: *plan, repoURL: *repo, restUser: os.Getenv("RESTIC_REST_USERNAME"), restPass: os.Getenv("RESTIC_REST_PASSWORD"),
		resticPath: *resticPath, input: &termInput{out: stdout}, stdout: stdout,
	})
}

func verifyCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("verify", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.Usage = func() { fmt.Fprint(stderr, usage) }
	server := fl.String("server", "", "")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if onPC(*server) {
		return pcVerify(*server, stdout, stderr)
	}
	return serverVerify(ctx, stdout)
}

func serverVerify(ctx context.Context, w io.Writer) int {
	if os.Geteuid() != 0 {
		fmt.Fprintln(w, "hostdeck verify braucht root: sudo ./hostdeck verify")
		return 2
	}
	dir, err := stateDir()
	if err != nil {
		fmt.Fprintf(w, "Fehler: %v\n", err)
		return 1
	}
	return restore.Verify(ctx, &restore.CLI{}, dir, w)
}

// restoreRun beschreibt ein Zurückspielen auf dem Server.
type restoreRun struct {
	plan               bool
	repoURL            string
	restUser, restPass string
	resticPath         string
	input              backup.Input
	stdout             io.Writer
	hello              func(context.Context) error
	password           string // mitgebrachtes Backup-Passwort (Probe-Umzug)
	installDocker      func(context.Context) error
	run                string // Lauf, der zurückkommt; leer: fragen
}

func serverRestore(ctx context.Context, r restoreRun) int {
	w := r.stdout
	if os.Geteuid() != 0 {
		fmt.Fprintln(w, "hostdeck restore braucht root: sudo ./hostdeck restore")
		return restore.ExitBlocked
	}
	what := "Zurückspielen"
	if r.plan {
		what = "Vorschau Zurückspielen"
	}
	lock, ok := takeLauf(what, w)
	if !ok {
		return restore.ExitBlocked
	}
	defer lock.Release()
	dir, err := stateDir()
	if err != nil {
		fmt.Fprintf(w, "Fehler: %v\n", err)
		return restore.ExitBlocked
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintf(w, "Fehler: %v\n", err)
		return restore.ExitBlocked
	}
	if r.hello != nil {
		if err := r.hello(ctx); err != nil {
			fmt.Fprintf(w, "Fehler: %v\n", err)
			return restore.ExitBlocked
		}
	}
	bin := r.resticPath
	if bin == "" {
		fmt.Fprintf(w, "restic %s vorbereiten …\n", restic.Version)
		bin, err = restic.Installer{Dir: filepath.Join(dir, "bin")}.Install(ctx)
		if err != nil {
			fmt.Fprintf(w, "Fehler: %v\n", err)
			return restore.ExitBlocked
		}
	} else {
		v, err := restic.BinaryVersion(ctx, bin)
		if err == nil {
			err = restic.CheckVersion(v)
		}
		if err != nil {
			fmt.Fprintf(w, "Fehler: restic %s: %v\n", bin, err)
			return restore.ExitBlocked
		}
	}
	host, _ := os.Hostname()
	pwFile := filepath.Join(dir, "passwort")
	job := &restore.Job{
		Version: version, StateDir: dir, PasswordFile: pwFile, Input: r.input, Out: w, PlanOnly: r.plan, Hostname: host,
		Password: r.password, InstallDocker: r.installDocker, RunID: r.run,
		UserStoppedFile: filepath.Join(ort.DataDir, ort.StoppedFile),
		Host:            restore.LocalHost{},
		Caddy:           &restore.SystemCaddy{},
		Docker:          &restore.CLI{Log: &prefixWriter{w: w, prefix: "      "}},
		Repo: &restic.Repo{Bin: bin, URL: r.repoURL, PasswordFile: pwFile, RestUser: r.restUser, RestPass: r.restPass,
			CacheDir: filepath.Join(dir, "cache"), Host: host, Log: &prefixWriter{w: w, prefix: "      restic: "}},
	}
	return job.Run(ctx)
}

// probeCopy hält die Probe-Kopie auf diesem Server an oder entfernt sie
// (Agent, aus dem Leitstand des alten Servers).
func probeCopy(ctx context.Context, remove, confirmed bool, in backup.Input, w io.Writer) int {
	if os.Geteuid() != 0 {
		fmt.Fprintln(w, "  ✗ Dafür braucht HostDeck root.")
		return restore.ExitBlocked
	}
	what := "Probe-Kopie anhalten"
	if remove {
		what = "Probe-Kopie entfernen"
	}
	lock, ok := takeLauf(what, w)
	if !ok {
		return restore.ExitBlocked
	}
	defer lock.Release()
	dir, err := stateDir()
	if err != nil {
		fmt.Fprintf(w, "  ✗ %v\n", err)
		return restore.ExitBlocked
	}
	host, _ := os.Hostname()
	p := &restore.Probe{
		StateDir: dir, Docker: &restore.CLI{}, Input: in, Out: w, Hostname: host,
		UserStoppedFile: filepath.Join(ort.DataDir, ort.StoppedFile),
		OwnBackups:      filepath.Join(ort.DataDir, ort.BackupRepo),
		Caddy:           &restore.SystemCaddy{},
		Confirmed:       confirmed,
	}
	if in, err := systemInstaller(io.Discard); err == nil {
		p.Leitstand = &copyLeitstand{in: in}
	}
	if remove {
		return p.Remove(ctx)
	}
	return p.Halt(ctx)
}

// pcRestore fragt Backup und Zielserver ab und startet das Zurückspielen.
func pcRestore(cfg remote.Config, server, folder string, stdout, stderr io.Writer) int {
	pc.PrepareConsole()
	cfgPath, err := pc.ConfigPath()
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	settings, err := pc.LoadConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: Einstellungen %s: %v\n", cfgPath, err)
		return 1
	}
	if folder == "" {
		folder = settings.Folder
	}
	if folder == "" || len(pc.ListRepos(folder)) == 0 {
		if folder != "" {
			fmt.Fprintf(stdout, "In %s liegt kein Backup.\n", folder)
		}
		f, err := chooseBackupFolder(console, stdout)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return 2
		}
		folder = f
	}
	repos := pc.ListRepos(folder)
	repo := repos[0]
	if len(repos) > 1 {
		fmt.Fprintf(stdout, "In %s liegen Backups von mehreren Servern:\n", folder)
		for i, r := range repos {
			fmt.Fprintf(stdout, "  %d  %s\n", i+1, r)
		}
		fmt.Fprint(stdout, "Welches zurückspielen? Nummer: ")
		l, err := console.ReadLine()
		var n int
		if _, serr := fmt.Sscan(strings.TrimSpace(l), &n); err != nil || serr != nil || n < 1 || n > len(repos) {
			fmt.Fprintln(stderr, "Keine gültige Nummer.")
			return 2
		}
		repo = repos[n-1]
	}
	fmt.Fprintf(stdout, "Backup: %s\n", filepath.Join(folder, repo))

	target := server
	if target == "" {
		target, err = askTarget(console, stdout, settings)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return 2
		}
	}
	if err := checkServer(target); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 2
	}
	if target == settings.Server && !cfg.Plan {
		fmt.Fprintf(stdout, "Achtung: %s ist der Server, von dem das Backup kommt. Wirklich dorthin zurückspielen? (ja/nein): ", target)
		a, _ := console.ReadLine()
		if !strings.EqualFold(strings.TrimSpace(a), "ja") {
			fmt.Fprintln(stdout, "Nicht gestartet.")
			return 2
		}
	}
	settings.Target = target
	if settings.Folder == "" {
		settings.Folder = folder
	}
	if err := settings.Save(cfgPath); err != nil {
		fmt.Fprintf(stderr, "Hinweis: Einstellungen nicht gespeichert: %v\n", err)
	}
	return runSession(cfg, &pc.Session{Server: target, Folder: folder, Repo: repo}, stdout, stderr)
}

// askTarget fragt nach dem Server, auf den das Backup zurück soll.
func askTarget(con lineReader, stdout io.Writer, settings pc.Config) (string, error) {
	if settings.Target != "" {
		fmt.Fprintf(stdout, "Auf welchen Server soll das Backup zurück? Enter für %s, sonst neue Adresse: ", settings.Target)
		l, err := con.ReadLine()
		if err != nil {
			return "", errors.New("ohne Zielserver geht es nicht")
		}
		if strings.TrimSpace(l) == "" {
			return settings.Target, nil
		}
		s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "ssh "))
		if !strings.Contains(s, "@") {
			return askUser(con, stdout, s)
		}
		return s, checkServer(s)
	}
	fmt.Fprintln(stdout, "Auf welchen Server soll das Backup zurück? Das ist der neue Server, nicht der alte.")
	return askServer(con, stdout)
}

// chooseBackupFolder lässt den Ordner wählen, in dem ein Backup liegt.
func chooseBackupFolder(con lineReader, stdout io.Writer) (string, error) {
	var f string
	if pc.HasDialog() {
		fmt.Fprintln(stdout, "Wähle im Fenster den Ordner, in dem dein Backup liegt …")
		p, err := pc.PickFolder()
		switch {
		case errors.Is(err, pc.ErrCancelled):
			return "", errors.New("Ordnerwahl abgebrochen")
		case err != nil:
			fmt.Fprintf(stdout, "Das Fenster ließ sich nicht öffnen (%v).\n", err)
		default:
			f = p
		}
	}
	if f == "" {
		fmt.Fprint(stdout, "Ordner mit dem Backup, zum Beispiel D:\\hostdeck-backup: ")
		p, err := con.ReadLine()
		if err != nil || strings.TrimSpace(p) == "" {
			return "", errors.New("ohne Backup-Ordner geht es nicht")
		}
		f = strings.TrimSpace(p)
	}
	// Auch der Ordner eines einzelnen Servers ist erlaubt.
	if _, err := os.Stat(filepath.Join(f, "config")); err == nil {
		f = filepath.Dir(f)
	}
	if len(pc.ListRepos(f)) == 0 {
		return "", fmt.Errorf("in %s liegt kein Backup von HostDeck", f)
	}
	return f, nil
}

func pcVerify(server string, stdout, stderr io.Writer) int {
	pc.PrepareConsole()
	cfgPath, err := pc.ConfigPath()
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	settings, _ := pc.LoadConfig(cfgPath)
	target := server
	if target == "" {
		if target, err = askTarget(console, stdout, settings); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return 2
		}
	}
	if err := checkServer(target); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 2
	}
	return runSession(remote.Config{Action: remote.ActionVerify}, &pc.Session{Server: target}, stdout, stderr)
}

// runSession startet eine Verbindung mit den gemeinsamen Einstellungen.
func runSession(cfg remote.Config, s *pc.Session, stdout, stderr io.Writer) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	intr := make(chan os.Signal, 2)
	signal.Notify(intr, os.Interrupt)
	defer signal.Stop(intr)
	s.BinDir, s.Version, s.Out, s.Err, s.Console, s.Interrupt = filepath.Dir(exe), version, stdout, stderr, console, intr
	fmt.Fprintf(stdout, "Verbinde mit %s …\n", s.Server)
	code, err := s.Run(context.Background(), cfg)
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		if errors.Is(err, pc.ErrConnect) {
			fmt.Fprintf(stderr, "Stimmt die Adresse %s?\n", s.Server)
		}
		return 1
	}
	return code
}

// installDocker installiert Docker mit dem offiziellen Skript von
// get.docker.com. Es läuft nur nach ausdrücklicher Zustimmung im Leitstand.
func installDocker(ctx context.Context, w io.Writer) error {
	// Ein halb installiertes Docker hilft niemandem: Abbrechen wartet, bis
	// das Skript fertig ist.
	stop := context.AfterFunc(ctx, func() {
		fmt.Fprintln(w, "      Abbruch angefordert: Die Docker-Installation läuft noch zu Ende, dann hört HostDeck auf.")
	})
	defer stop()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Minute)
	defer cancel()
	dir, err := os.MkdirTemp("", "hostdeck-docker-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	script := filepath.Join(dir, "get-docker.sh")
	pw := &prefixWriter{w: w, prefix: "      "}
	// Laden ohne curl: Frisch installierte Server (etwa Debian) haben es
	// nicht immer.
	if err := fetchFile(ctx, dockerScriptURL, script); err != nil {
		return fmt.Errorf("Skript von get.docker.com laden: %w", err)
	}
	run := exec.CommandContext(ctx, "sh", script)
	run.Stdout, run.Stderr = pw, pw
	if err := run.Run(); err != nil {
		return fmt.Errorf("Skript von get.docker.com: %w", err)
	}
	return nil
}

const dockerScriptURL = "https://get.docker.com"

// fetchFile lädt url nach path (höchstens 8 MB, nur 200).
func fetchFile(ctx context.Context, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Antwort %s", resp.Status)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, 8<<20+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > 8<<20 {
		err = errors.New("Datei zu groß")
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}
