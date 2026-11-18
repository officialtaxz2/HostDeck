package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/betrieb"
	"github.com/officialtaxz2/hostdeck/internal/discover"
	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/hostfs"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/ort"
	"github.com/officialtaxz2/hostdeck/internal/pc"
	"github.com/officialtaxz2/hostdeck/internal/remote"
	"github.com/officialtaxz2/hostdeck/internal/restic"
)

// onPC meldet, ob HostDeck als PC-Seite laufen soll: unter Windows immer, sonst,
// wenn ein Server angegeben ist.
func onPC(server string) bool {
	return runtime.GOOS == "windows" || server != ""
}

func backupCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("backup", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.Usage = func() { fmt.Fprint(stderr, usage) }
	plan := fl.Bool("plan", false, "")
	final := fl.Bool("letzter-lauf", false, "")
	server := fl.String("server", "", "")
	folder := fl.String("ordner", "", "")
	port := fl.Int("port", remote.DefaultPort, "")
	out := fl.String("out", "", "")
	repo := fl.String("repo", "", "")
	resticPath := fl.String("restic", "", "")
	socket := fl.String("docker-socket", dockerapi.DefaultSocket, "")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if fl.NArg() > 0 {
		fmt.Fprintf(stderr, "Unerwartete Angabe %q.\n", fl.Arg(0))
		return 2
	}
	if *plan && *final {
		fmt.Fprintln(stderr, "--plan und --letzter-lauf zusammen: Der Probelauf zeigt auch den letzten Lauf, mit --plan --letzter-lauf ist das erlaubt.")
	}
	if onPC(*server) {
		return pcSession(remote.Config{Action: remote.ActionBackup, Plan: *plan, Final: *final, Port: *port, Out: *out},
			*server, *folder, stdout, stderr)
	}
	if !*plan && *repo == "" {
		fmt.Fprintln(stderr, "Als Dienst eingerichtet startest du das Backup im Leitstand unter Backup.")
		fmt.Fprintln(stderr, "Direkt auf dem Server geht es nur mit einem Ziel: sudo ./hostdeck backup --repo <Ziel>,")
		fmt.Fprintln(stderr, "etwa einem Ordner auf einer zweiten Platte. Den Plan zeigt sudo ./hostdeck backup --plan.")
		return 2
	}
	input := &termInput{out: stdout}
	return serverBackup(ctx, serverRun{
		plan: *plan, final: *final, out: *out, repoURL: *repo,
		restUser: os.Getenv("RESTIC_REST_USERNAME"), restPass: os.Getenv("RESTIC_REST_PASSWORD"),
		resticPath: *resticPath, socket: *socket, where: *repo, input: input, stdout: stdout,
	})
}

func dienstStartenCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("dienste-starten", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.Usage = func() { fmt.Fprint(stderr, usage) }
	server := fl.String("server", "", "")
	socket := fl.String("docker-socket", dockerapi.DefaultSocket, "")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if onPC(*server) {
		return pcSession(remote.Config{Action: remote.ActionStart}, *server, "", stdout, stderr)
	}
	return startServices(ctx, *socket, stdout)
}

func ordnerCmd(args []string, stdout, stderr io.Writer) int {
	pc.PrepareConsole()
	cfgPath, err := pc.ConfigPath()
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	cfg, _ := pc.LoadConfig(cfgPath)
	con := console
	f, err := chooseFolder(con, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 1
	}
	cfg.Folder = f
	if err := cfg.Save(cfgPath); err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Backup-Ordner ist jetzt %s\n", f)
	return 0
}

func serverCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "Unerwartete Angabe %q.\n", args[0])
		return 2
	}
	pc.PrepareConsole()
	cfgPath, err := pc.ConfigPath()
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	cfg, _ := pc.LoadConfig(cfgPath)
	if cfg.Server != "" {
		fmt.Fprintf(stdout, "Bisher: %s\n", cfg.Server)
	}
	s, err := askServer(console, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return 2
	}
	cfg.Server = s
	if err := cfg.Save(cfgPath); err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Server ist jetzt %s\n", s)
	return 0
}

// askServer fragt die Adresse des Servers ab. Fehlt der Benutzer, fragt
// HostDeck nach, statt still den Windows-Benutzernamen zu nehmen.
func askServer(con lineReader, stdout io.Writer) (string, error) {
	fmt.Fprint(stdout, "Adresse des Servers, zum Beispiel root@203.0.113.10: ")
	s, err := con.ReadLine()
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "ssh "))
	if err != nil || s == "" {
		return "", errors.New("ohne Server-Adresse geht es nicht")
	}
	if !strings.Contains(s, "@") {
		if s, err = askUser(con, stdout, s); err != nil {
			return "", err
		}
	}
	if err := checkServer(s); err != nil {
		return "", err
	}
	return s, nil
}

// askUser ergänzt den Benutzer zu einer Adresse ohne „benutzer@“.
func askUser(con lineReader, stdout io.Writer, host string) (string, error) {
	if err := checkServer("root@" + host); err != nil {
		return "", err
	}
	fmt.Fprintf(stdout, "Mit welchem Benutzer auf %s anmelden? (Enter für root): ", host)
	u, err := con.ReadLine()
	if err != nil {
		return "", errors.New("ohne Benutzer geht es nicht")
	}
	u = strings.TrimSpace(u)
	if u == "" {
		u = "root"
	}
	s := u + "@" + host
	if err := checkServer(s); err != nil {
		return "", err
	}
	return s, nil
}

// checkServer prüft eine Adresse der Form benutzer@rechner. Ein führendes
// Minus würde ssh als Option lesen.
func checkServer(s string) error {
	u, host, ok := strings.Cut(s, "@")
	if !ok || u == "" || host == "" || strings.HasPrefix(s, "-") || strings.ContainsAny(s, " \t\"'`;|&$<>") || strings.Contains(host, "@") {
		return fmt.Errorf("%q ist keine gültige Adresse; gemeint ist etwa root@203.0.113.10", s)
	}
	return nil
}

// pcSession ist die PC-Seite: Einstellungen, Ordner, Verbindung.
func pcSession(cfg remote.Config, server, folder string, stdout, stderr io.Writer) int {
	pc.PrepareConsole()
	con := console
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
	changed := false
	if server != "" && server != settings.Server {
		settings.Server, changed = server, true
	}
	if settings.Server == "" {
		s, err := askServer(con, stdout)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return 2
		}
		settings.Server, changed = s, true
	}
	if !strings.Contains(settings.Server, "@") {
		// Ohne Benutzer nähme ssh den Windows-Benutzernamen.
		s, err := askUser(con, stdout, settings.Server)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return 2
		}
		settings.Server, changed = s, true
	}
	if err := checkServer(settings.Server); err != nil {
		fmt.Fprintf(stderr, "%v\nÄndern: im Menü Punkt 6 oder hostdeck.exe server\n", err)
		return 2
	}
	if cfg.Out != "" && cfg.Out != settings.Out {
		settings.Out, changed = cfg.Out, true
	}
	if cfg.Out == "" {
		cfg.Out = settings.Out
	}
	needFolder := cfg.Action == remote.ActionBackup && !cfg.Plan
	if folder != "" {
		settings.Folder, changed = folder, true
	}
	if needFolder && settings.Folder == "" {
		f, err := chooseFolder(con, stdout)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return 2
		}
		settings.Folder, changed = f, true
	}
	if changed {
		if err := settings.Save(cfgPath); err != nil {
			fmt.Fprintf(stderr, "Hinweis: Einstellungen nicht gespeichert: %v\n", err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	intr := make(chan os.Signal, 2)
	signal.Notify(intr, os.Interrupt)
	defer signal.Stop(intr)

	if needFolder {
		fmt.Fprintf(stdout, "Backup-Ordner: %s (ändern mit: hostdeck.exe ordner)\n", settings.Folder)
	}
	fmt.Fprintf(stdout, "Verbinde mit %s … (Adresse ändern: Menüpunkt 6 oder hostdeck.exe server)\n", settings.Server)
	s := &pc.Session{
		Server: settings.Server, Folder: settings.Folder, BinDir: filepath.Dir(exe), Version: version,
		Out: stdout, Err: stderr, Console: con, Interrupt: intr,
	}
	code, err := s.Run(context.Background(), cfg)
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		if errors.Is(err, pc.ErrConnect) {
			fmt.Fprintf(stderr, "Stimmt die Adresse %s? Ändern: im Menü Punkt 6 oder hostdeck.exe server\n", settings.Server)
		}
		return 1
	}
	if needFolder && code == 0 {
		if err := pc.WriteNotfall(settings.Folder, filepath.Dir(exe), exe, restic.Version); err != nil {
			fmt.Fprintf(stdout, "Hinweis zum Notfall-Ordner: %v\n", err)
		} else {
			fmt.Fprintf(stdout, "Notfall-Ordner mit Programmen und Anleitung: %s\n", filepath.Join(settings.Folder, "notfall"))
		}
	}
	return code
}

// chooseFolder lässt den Backup-Ordner wählen und prüft den freien Platz.
func chooseFolder(con *pc.StdConsole, stdout io.Writer) (string, error) {
	var f string
	if pc.HasDialog() {
		fmt.Fprintln(stdout, "Wähle im Fenster den Ordner für das Backup (etwa 60 GB frei) …")
		p, err := pc.PickFolder()
		switch {
		case errors.Is(err, pc.ErrCancelled):
			return "", errors.New("Ordnerwahl abgebrochen; ohne Backup-Ordner geht es nicht")
		case err != nil:
			fmt.Fprintf(stdout, "Das Fenster ließ sich nicht öffnen (%v).\n", err)
		default:
			f = p
		}
	}
	if f == "" {
		fmt.Fprint(stdout, "Ordner für das Backup, zum Beispiel D:\\hostdeck-backup: ")
		p, err := con.ReadLine()
		if err != nil || strings.TrimSpace(p) == "" {
			return "", errors.New("ohne Backup-Ordner geht es nicht")
		}
		f = strings.TrimSpace(p)
	}
	abs, err := filepath.Abs(f)
	if err == nil {
		f = abs
	}
	if err := os.MkdirAll(f, 0o700); err != nil {
		return "", fmt.Errorf("Ordner %s lässt sich nicht anlegen: %v", f, err)
	}
	if free, err := pc.FreeSpace(f); err == nil && free < pc.MinFree {
		fmt.Fprintf(stdout, "Achtung: Dort sind nur %d GB frei, gebraucht werden etwa 60 GB. Trotzdem nehmen? (ja/nein): ", free>>30)
		a, _ := con.ReadLine()
		if !strings.EqualFold(strings.TrimSpace(a), "ja") {
			return "", errors.New("anderen Ordner wählen: hostdeck.exe ordner")
		}
	}
	return f, nil
}

// serverRun beschreibt einen Lauf auf dem Server.
type serverRun struct {
	plan, final        bool
	out                string
	repoURL            string
	restUser, restPass string
	resticPath         string
	socket             string
	where              string
	input              backup.Input
	stdout             io.Writer
	screen             io.Writer // nur Bildschirm, nicht Protokoll; Standard stdout
	hello              func(context.Context) error
	sendReport         func(context.Context, string, []byte) error
	target             string                  // „server“: Backup in einen Ordner auf dem Server
	planHint           string                  // Satz am Ende eines Probelaufs
	checkSpace         func(total int64) error // Platz vor dem Lauf prüfen
	exclude            []string                // nie mitsichern, etwa der Backup-Ordner
}

// stateDir ist der eigene Ordner von HostDeck auf dem Server, normalerweise
// /root/.hostdeck. HOSTDECK_STATE_DIR ersetzt ihn, etwa für Tests.
func stateDir() (string, error) {
	if d := os.Getenv("HOSTDECK_STATE_DIR"); d != "" {
		return d, nil
	}
	u, err := user.Current()
	if err != nil || u.HomeDir == "" {
		home, err2 := os.UserHomeDir()
		if err2 != nil {
			return "", fmt.Errorf("Home-Ordner unbekannt: %v", err)
		}
		return filepath.Join(home, ".hostdeck"), nil
	}
	return filepath.Join(u.HomeDir, ".hostdeck"), nil
}

// serviceResultDir ist der Ergebnisordner von HostDeck als Dienst.
var serviceResultDir = ort.ResultDir

// serviceInstalled sagt, ob HostDeck hier als Dienst eingerichtet ist und seine
// Daten im festen Ordner liegen.
func serviceInstalled() bool {
	_, err := os.Stat(filepath.Join(serviceResultDir, "manifest.yaml"))
	return err == nil
}

// findResultDir sucht den Ergebnisordner von hostdeck discover. Ist HostDeck als
// Dienst eingerichtet, gilt nur noch dessen Ordner, auch wenn hostdeck.exe sich
// einen alten gemerkt hat: Sonst sicherte es eine veraltete Auswahl.
func findResultDir(out string) (string, error) {
	if serviceInstalled() {
		return serviceResultDir, nil
	}
	if out != "" {
		return out, nil
	}
	return findLegacyResultDir()
}

// findLegacyResultDir sucht den Ergebnisordner an den Orten, die HostDeck vor
// dem Dienst nutzte.
func findLegacyResultDir() (string, error) {
	var cands []string
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, filepath.Join(wd, "hostdeck-ergebnis"))
	}
	if su := os.Getenv("SUDO_USER"); su != "" {
		if u, err := user.Lookup(su); err == nil {
			cands = append(cands, filepath.Join(u.HomeDir, "hostdeck-ergebnis"))
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		cands = append(cands, filepath.Join(home, "hostdeck-ergebnis"))
	}
	cands = append(cands, "/root/hostdeck-ergebnis")
	for _, c := range cands {
		if trustedResultDir(c) {
			return c, nil
		}
	}
	// Liegt HostDeck in einem eigenen Ordner, etwa /root/hostdeck, liegt das Ergebnis
	// eine Ebene tiefer. Bei mehreren gewinnt das zuletzt geänderte Manifest.
	var best string
	var bestTime time.Time
	for _, pat := range resultGlobs {
		ms, _ := filepath.Glob(filepath.Join(pat, "manifest.yaml"))
		for _, m := range ms {
			if !trustedResultDir(filepath.Dir(m)) {
				continue
			}
			if fi, err := os.Lstat(m); err == nil && fi.ModTime().After(bestTime) {
				best, bestTime = filepath.Dir(m), fi.ModTime()
			}
		}
	}
	if best != "" {
		return best, nil
	}
	return "", fmt.Errorf("kein Ergebnisordner mit manifest.yaml gefunden (gesucht: %s und eine Ebene darunter); mit --out angeben, etwa hostdeck.exe backup --out /root/hostdeck/hostdeck-ergebnis", strings.Join(cands, ", "))
}

// trustedResultDir sagt, ob dir ein Ergebnisordner ist, dem HostDeck als root
// trauen darf: Ordner und manifest.yaml sind keine Symlinks, gehören root,
// dem aufrufenden Benutzer oder dem, der sudo aufrief, und nur der darf
// darin schreiben. Sonst könnte ein anderer Benutzer am Server in seinem
// Home einen Ordner anlegen, dessen Auswahl HostDeck übernimmt.
func trustedResultDir(dir string) bool {
	ok := map[int]bool{0: true, os.Geteuid(): true}
	if id, err := strconv.Atoi(os.Getenv("SUDO_UID")); err == nil {
		ok[id] = true
	}
	for _, p := range []string{dir, filepath.Join(dir, "manifest.yaml")} {
		fi, err := os.Lstat(p)
		if err != nil || fi.Mode()&os.ModeSymlink != 0 || fi.Mode().Perm()&0o022 != 0 {
			return false
		}
		if uid, known := fileOwner(fi); known && !ok[uid] {
			return false
		}
	}
	return true
}

// resultGlobs sind die Orte eine Ebene tiefer, an denen findResultDir sucht.
var resultGlobs = []string{"/root/*/hostdeck-ergebnis", "/home/*/hostdeck-ergebnis", "/home/*/*/hostdeck-ergebnis", "/opt/*/hostdeck-ergebnis"}

func serverBackup(ctx context.Context, r serverRun) int {
	w := r.stdout
	if os.Geteuid() != 0 {
		fmt.Fprintln(w, "hostdeck backup braucht root: sudo ./hostdeck backup")
		return backup.ExitBlocked
	}
	dir, err := stateDir()
	if err != nil {
		fmt.Fprintf(w, "Fehler: %v\n", err)
		return backup.ExitBlocked
	}
	what := "Backup"
	if r.plan {
		what = "Probelauf"
	}
	lock, ok := takeLauf(what, w)
	if !ok {
		return backup.ExitBlocked
	}
	defer lock.Release()
	resultDir, err := findResultDir(r.out)
	if err != nil {
		fmt.Fprintf(w, "Fehler: %v\n", err)
		return backup.ExitBlocked
	}
	return runBackupJob(ctx, r, dir, resultDir)
}

// runBackupJob führt einen Lauf aus; die Laufsperre hält der Aufrufer.
func runBackupJob(ctx context.Context, r serverRun, dir, resultDir string) int {
	w := r.stdout
	fmt.Fprintf(w, "Auswahl aus %s\n", resultDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintf(w, "Fehler: %v\n", err)
		return backup.ExitBlocked
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(w, "Fehler: %v\n", err)
		return backup.ExitBlocked
	}
	// Dumps und Images laufen als „HostDeck intern-…“ unter restic; sie finden
	// den Docker-Socket über die Umgebung.
	os.Setenv("HOSTDECK_DOCKER_SOCKET", r.socket)
	ctl := dockerapi.NewControl(r.socket)
	host, _ := os.Hostname()

	job := &backup.Job{
		Version: version, ResultDir: resultDir, StateDir: dir,
		Discover: func(ctx context.Context) (*inventory.Inventory, error) {
			deps := discover.Deps{Docker: dockerapi.New(r.socket), FS: hostfs.New("/"), Git: discover.ExecGit{}, Caddy: discover.ExecCaddy{}}
			return discover.Run(ctx, deps, discover.Options{ToolVersion: "HostDeck " + version, NoSizes: true, Euid: os.Geteuid(), Log: io.Discard})
		},
		Docker: ctl, PasswordFile: backupPasswordFile(dir), Input: r.input, Out: w, Screen: r.screen,
		Self: []string{exe}, Platform: fmt.Sprintf(`{"os":"linux","architecture":"%s"}`, runtime.GOARCH),
		Final: r.final, PlanOnly: r.plan, Hello: r.hello, SendReport: r.sendReport, Where: r.where,
		Target: r.target, PlanHint: r.planHint, Leitstand: r.target == targetServer, CheckSpace: r.checkSpace, Exclude: r.exclude,
		UserStopped: func() map[string]time.Time { return betrieb.LoadStopped(filepath.Join(ort.DataDir, ort.StoppedFile)) },
	}
	if !r.plan {
		bin, err := resticBinary(ctx, r.resticPath, dir, w)
		if err != nil {
			fmt.Fprintf(w, "Fehler: %v\n", err)
			return backup.ExitBlocked
		}
		job.Repo = &restic.Repo{
			Bin: bin, URL: r.repoURL, PasswordFile: job.PasswordFile, RestUser: r.restUser, RestPass: r.restPass,
			CacheDir: filepath.Join(dir, "cache"), Host: host, Log: &prefixWriter{w: w, prefix: "      restic: "},
		}
	}
	return job.Run(ctx)
}

// backupPasswordFile ist die Datei mit dem Backup-Passwort im Ordner von HostDeck.
func backupPasswordFile(dir string) string { return filepath.Join(dir, "passwort") }

// resticBinary liefert restic: das angegebene, geprüft, oder das von HostDeck
// verwaltete, das es bei Bedarf herunterlädt.
func resticBinary(ctx context.Context, path, dir string, w io.Writer) (string, error) {
	if path == "" {
		fmt.Fprintf(w, "restic %s vorbereiten …\n", restic.Version)
		return restic.Installer{Dir: filepath.Join(dir, "bin")}.Install(ctx)
	}
	v, err := restic.BinaryVersion(ctx, path)
	if err == nil {
		err = restic.CheckVersion(v)
	}
	if err != nil {
		return "", fmt.Errorf("restic %s: %v", path, err)
	}
	return path, nil
}

func startServices(ctx context.Context, socket string, w io.Writer) int {
	if os.Geteuid() != 0 {
		fmt.Fprintln(w, "hostdeck dienste-starten braucht root: sudo ./hostdeck dienste-starten")
		return 2
	}
	dir, err := stateDir()
	if err != nil {
		fmt.Fprintf(w, "Fehler: %v\n", err)
		return 1
	}
	lock, ok := takeLauf("Dienste starten", w)
	if !ok {
		return 1
	}
	defer lock.Release()
	st, err := backup.LoadStopState(backup.StopStatePath(dir))
	if err != nil {
		fmt.Fprintf(w, "Fehler: %v\n", err)
		return 1
	}
	if len(st.Entries()) == 0 {
		fmt.Fprintln(w, "Es sind keine Container vermerkt, die HostDeck gestoppt hat. Nichts zu tun.")
		return 0
	}
	fmt.Fprintf(w, "Starte %d Container, die HostDeck gestoppt hat:\n", len(st.Entries()))
	bad := backup.StartRecorded(ctx, dockerapi.NewControl(socket), st, w, 10*time.Second)
	if len(bad) > 0 {
		fmt.Fprintf(w, "Läuft danach nicht: %s. Bitte mit docker ps und docker logs nachsehen.\n", strings.Join(bad, ", "))
		return 1
	}
	fmt.Fprintln(w, "Alle laufen wieder.")
	return 0
}

// agentCmd ist die Server-Seite einer Verbindung vom PC.
func agentCmd(ctx context.Context, stdin io.Reader, stdout io.Writer) int {
	// Bricht die Verbindung ab, darf ein Schreibversuch HostDeck nicht beenden:
	// Es muss gestoppte Container noch wieder starten.
	signal.Ignore(syscall.SIGPIPE, syscall.SIGHUP)
	out := &remote.SyncWriter{W: stdout}
	sum := "unbekannt"
	if exe, err := os.Executable(); err == nil {
		if b, err := os.ReadFile(exe); err == nil {
			s := sha256.Sum256(b)
			sum = hex.EncodeToString(s[:])
		}
	}
	out.Line("HOSTDECK-AGENT " + version + " " + sum)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cfg, in, err := remote.ReadConfig(stdin, out, cancel)
	if err != nil {
		out.Line("Fehler: " + err.Error())
		out.Line(remote.End(2))
		return 2
	}
	w := io.Writer(out)
	if dir, err := stateDir(); err == nil {
		logDir := filepath.Join(dir, "protokolle")
		if os.MkdirAll(logDir, 0o700) == nil {
			if f, err := os.OpenFile(filepath.Join(logDir, time.Now().Format("20060102-150405")+"-"+cfg.Action+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
				defer f.Close()
				w = &logWriter{file: f, conn: out}
			}
		}
	}
	in.OnTimeout(func() {
		fmt.Fprintf(w, "\nSeit %d Sekunden kein Lebenszeichen vom PC (Standby oder Leitung weg). Breche ab.\n", int(remote.HeartbeatTimeout.Seconds()))
	})
	socket := envOr("HOSTDECK_DOCKER_SOCKET", dockerapi.DefaultSocket)
	base := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	repoURL := tunnelRepo(cfg.Port)
	client := &http.Client{Timeout: time.Minute}
	req := func(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
		r, err := http.NewRequestWithContext(ctx, method, base+path, body)
		if err != nil {
			return nil, err
		}
		r.SetBasicAuth(cfg.User, cfg.Key)
		return client.Do(r)
	}
	hello := func(ctx context.Context) error {
		resp, err := req(ctx, http.MethodGet, "/.hostdeck/hallo", nil)
		if err != nil {
			return fmt.Errorf("der Tunnel zum PC antwortet nicht (%v)", err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		if resp.StatusCode != http.StatusOK || string(b) != cfg.Nonce {
			return fmt.Errorf("am Tunnel-Port %d antwortet nicht dein PC (%s); läuft dort ein anderes Programm?", cfg.Port, resp.Status)
		}
		return nil
	}
	var code int
	switch cfg.Action {
	case remote.ActionRestore:
		rr := restoreRun{
			plan: cfg.Plan, repoURL: repoURL, restUser: cfg.User, restPass: cfg.Key,
			input: in, stdout: w, hello: hello, password: cfg.Password, run: cfg.Run,
		}
		if cfg.Leitstand {
			rr.installDocker = func(ctx context.Context) error { return installDocker(ctx, w) }
		}
		code = serverRestore(ctx, rr)
	case remote.ActionVerify:
		code = serverVerify(ctx, w)
	case remote.ActionBackup:
		code = serverBackup(ctx, serverRun{
			plan: cfg.Plan, final: cfg.Final, out: cfg.Out, repoURL: repoURL,
			restUser: cfg.User, restPass: cfg.Key, socket: socket, where: cfg.Where,
			input: in, stdout: w, screen: out,
			hello: hello,
			sendReport: func(ctx context.Context, run string, body []byte) error {
				resp, err := req(ctx, http.MethodPut, "/.hostdeck/bericht?lauf="+run, strings.NewReader(string(body)))
				if err != nil {
					return err
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusNoContent {
					return errors.New(resp.Status)
				}
				return nil
			},
		})
		if code == backup.ExitBlocked || cfg.Plan {
			backup.RemoveDanglingPassword(filepath.Join(mustStateDir(), "passwort"))
		}
	case remote.ActionStart:
		code = startServices(ctx, socket, w)
	case remote.ActionProbeHalt, remote.ActionProbeRemove:
		code = probeCopy(ctx, cfg.Action == remote.ActionProbeRemove, cfg.Confirmed, in, w)
	case remote.ActionTakeover:
		code = takeoverCmd(ctx, cfg, w)
	case remote.ActionServer:
		code = serverLevelCmd(ctx, cfg, w)
	default:
		fmt.Fprintf(w, "Unbekannte Aktion %q\n", cfg.Action)
		code = 2
	}
	out.Line(remote.End(code))
	return code
}

// logWriter schreibt zuerst ins Protokoll auf dem Server und dann zum PC.
// Ist die Verbindung weg, geht das Protokoll weiter: Gerade dann steht
// darin, welche Container HostDeck wieder gestartet hat.
type logWriter struct {
	file io.Writer
	conn io.Writer
}

func (l *logWriter) Write(p []byte) (int, error) {
	l.file.Write(p)
	l.conn.Write(p)
	return len(p), nil
}

// tunnelRepo ist das restic-Ziel am Tunnel zum PC. Ohne „rest:“ hielte
// restic die Adresse für einen Ordner auf dem Server.
// takeLauf nimmt die Laufsperre. Als root ist sie Pflicht; ohne root (etwa
// in Tests) geht es ohne weiter, wenn der Ordner nicht beschreibbar ist.
func takeLauf(what string, w io.Writer) (*lauf.Lock, bool) {
	l, err := lauf.Take(what)
	if err == nil {
		return l, true
	}
	var busy *lauf.BusyError
	if errors.As(err, &busy) {
		fmt.Fprintf(w, "%v\n", err)
		return nil, false
	}
	if os.Geteuid() != 0 {
		return nil, true
	}
	fmt.Fprintf(w, "Fehler: Laufsperre: %v\n", err)
	return nil, false
}

func tunnelRepo(port int) string { return fmt.Sprintf("rest:http://127.0.0.1:%d/", port) }

func mustStateDir() string {
	d, _ := stateDir()
	return d
}

// termInput fragt direkt im Terminal des Servers.
type termInput struct {
	out   io.Writer
	lines chan string
}

func (t *termInput) Ask(ctx context.Context, q backup.Question) (string, error) {
	if t.lines == nil {
		t.lines = make(chan string)
		go func() {
			sc := bufio.NewScanner(os.Stdin)
			for sc.Scan() {
				t.lines <- sc.Text()
			}
			close(t.lines)
		}()
	}
	if q.Secret {
		fmt.Fprintf(t.out, "%s (Eingabe ist sichtbar) ", q.Text)
	} else {
		fmt.Fprintf(t.out, "%s ", q.Text)
	}
	select {
	case l, ok := <-t.lines:
		if !ok {
			return "", io.EOF
		}
		return l, nil
	case <-ctx.Done():
		fmt.Fprintln(t.out)
		return "", ctx.Err()
	}
}

// prefixWriter rückt jede Zeile ein.
type prefixWriter struct {
	w      io.Writer
	prefix string
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		fmt.Fprintln(p.w, p.prefix+l)
	}
	return len(b), nil
}

// internDumpCmd läuft unter restic --stdin-from-command und schreibt einen
// Datenbank-Dump auf die Standardausgabe.
func internDumpCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("intern-dump", flag.ContinueOnError)
	fl.SetOutput(stderr)
	ctr := fl.String("container", "", "")
	engine := fl.String("engine", "", "")
	socket := fl.String("docker-socket", envOr("HOSTDECK_DOCKER_SOCKET", dockerapi.DefaultSocket), "")
	if err := fl.Parse(args); err != nil || *ctr == "" || *engine == "" {
		fmt.Fprintln(stderr, "intern-dump braucht --container und --engine")
		return 2
	}
	if err := backup.Dump(ctx, dockerapi.NewControl(*socket), *ctr, *engine, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "HostDeck: %v\n", err)
		return 1
	}
	return 0
}

// internImageCmd läuft unter restic --stdin-from-command und schreibt ein
// Image als Tar-Archiv auf die Standardausgabe.
func internImageCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("intern-image", flag.ContinueOnError)
	fl.SetOutput(stderr)
	id := fl.String("image", "", "")
	platform := fl.String("platform", "", "")
	var refs stringList
	fl.Var(&refs, "ref", "")
	socket := fl.String("docker-socket", envOr("HOSTDECK_DOCKER_SOCKET", dockerapi.DefaultSocket), "")
	if err := fl.Parse(args); err != nil || *id == "" {
		fmt.Fprintln(stderr, "intern-image braucht --image")
		return 2
	}
	if err := backup.SaveImage(ctx, dockerapi.NewControl(*socket), *id, refs, *platform, stdout); err != nil {
		fmt.Fprintf(stderr, "HostDeck: %v\n", err)
		return 1
	}
	return 0
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
