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
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/discover"
	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/hostfs"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/neueversion"
	"github.com/officialtaxz2/hostdeck/internal/ort"
	"github.com/officialtaxz2/hostdeck/internal/outdir"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
)

// installer richtet HostDeck als Dienst ein. Die Pfade sind Felder, damit die
// Tests in einem leeren Ordner arbeiten können.
type installer struct {
	ProgDir, DataDir, ResultDir, UnitDir, BinLink string
	WebUser                                       string
	Exe                                           string // die laufende Programmdatei
	Out                                           io.Writer

	// Run führt systemctl und useradd aus.
	Run func(ctx context.Context, name string, args ...string) error
	// UserExists prüft, ob es den Benutzer gibt.
	UserExists func(name string) bool
	// FindOld sucht den bisherigen Ergebnisordner (vor dem Dienst).
	FindOld func() (string, error)
	Now     func() time.Time
}

func systemInstaller(out io.Writer) (*installer, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	return &installer{
		ProgDir: ort.ProgDir, DataDir: ort.DataDir, ResultDir: ort.ResultDir, UnitDir: ort.UnitDir,
		BinLink: ort.BinLink, WebUser: ort.WebUser, Exe: exe, Out: out,
		Run: func(ctx context.Context, name string, args ...string) error {
			var buf bytes.Buffer
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Stdout, cmd.Stderr = &buf, &buf
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(buf.String()))
			}
			return nil
		},
		UserExists: func(name string) bool { _, err := user.Lookup(name); return err == nil },
		FindOld:    findLegacyResultDir,
		Now:        time.Now,
	}, nil
}

func (in *installer) prog() string { return filepath.Join(in.ProgDir, "hostdeck") }

func (in *installer) printf(format string, args ...any) { fmt.Fprintf(in.Out, format, args...) }

// unitDienst ist die systemd-Unit des root-Teils. Er braucht Docker und den
// Zugriff auf die Projektordner, deshalb läuft er ohne Sandbox; dafür hat
// er keinen Netzwerk-Port.
func unitDienst(prog string) string {
	return `# Angelegt von hostdeck einrichten. Änderungen überschreibt das nächste Einrichten.
[Unit]
Description=HostDeck Dienst (Leitstand, root-Teil ohne Netzwerk-Port)
After=network.target docker.service

[Service]
Type=simple
ExecStart=` + prog + ` dienst
RuntimeDirectory=hostdeck
RuntimeDirectoryMode=0755
RuntimeDirectoryPreserve=yes
StateDirectory=hostdeck
StateDirectoryMode=0700
UMask=0077
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`
}

// unitWeb ist die systemd-Unit der Website: eigener Benutzer ohne Rechte,
// nur Unix-Sockets, kein Zugriff auf Projektordner, Docker, Backups oder die
// Verwaltung von Caddy.
func unitWeb(prog, webUser, domain string, extra ...string) string {
	exec := prog + " web"
	if domain != "" {
		exec += " --adresse " + domain
		if len(extra) > 0 {
			exec += " --auch " + strings.Join(extra, ",")
		}
	}
	return `# Angelegt von hostdeck einrichten. Änderungen überschreibt das nächste Einrichten.
[Unit]
Description=HostDeck Website (Leitstand, ohne Rechte)
After=hostdeck-dienst.service
Wants=hostdeck-dienst.service

[Service]
Type=simple
ExecStart=` + exec + `
User=` + webUser + `
Group=` + webUser + `
RuntimeDirectory=hostdeck-web
RuntimeDirectoryMode=0755
UMask=0077
Restart=on-failure
RestartSec=2

NoNewPrivileges=yes
CapabilityBoundingSet=
AmbientCapabilities=
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectClock=yes
ProtectHostname=yes
ProtectProc=invisible
ProcSubset=pid
RestrictNamespaces=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native
SystemCallFilter=@system-service
SystemCallFilter=~@privileged @resources
RestrictAddressFamilies=AF_UNIX
IPAddressDeny=any
InaccessiblePaths=-/opt -/srv -/root -/home -/var/lib/docker -/var/lib/hostdeck -/etc/caddy -/var/lib/caddy -/run/docker.sock -/var/run/docker.sock -/run/containerd
MemoryMax=256M
TasksMax=64

[Install]
WantedBy=multi-user.target
`
}

// unitSocket ist der Eingang für Caddy nach dem Freischalten. systemd legt
// den Socket an und gibt ihn nur der Gruppe von Caddy; hostdeck web bekommt ihn
// beim Start gereicht. Andere Benutzer des Servers kommen nicht heran.
func unitSocket(group string) string {
	return `# Angelegt von HostDeck beim Freischalten; zurücknehmen mit: sudo hostdeck freischalten --zuruecknehmen
[Unit]
Description=HostDeck Website, Eingang für Caddy

[Socket]
ListenStream=` + ort.CaddySocket + `
SocketUser=root
SocketGroup=` + group + `
SocketMode=0660
DirectoryMode=0755
FileDescriptorName=caddy
Service=` + ort.UnitWeb + `
RemoveOnStop=yes

[Install]
WantedBy=sockets.target
`
}

// install richtet alles ein. Läuft schon alles, bringt es die neue Version
// in Betrieb und lässt die Daten, wie sie sind.
func (in *installer) install(ctx context.Context) error {
	if err := in.copyProgram(); err != nil {
		return fmt.Errorf("Programm nach %s: %w", in.ProgDir, err)
	}
	if !in.UserExists(in.WebUser) {
		in.printf("  Lege den Benutzer %s an (ohne Anmeldung, ohne Home-Ordner) …\n", in.WebUser)
		if err := in.Run(ctx, "useradd", "--system", "--user-group", "--no-create-home", "--home-dir", "/nonexistent", "--shell", "/usr/sbin/nologin", in.WebUser); err != nil {
			return err
		}
	}
	if err := in.migrate(); err != nil {
		return fmt.Errorf("Daten nach %s: %w", in.ResultDir, err)
	}
	if err := in.link(); err != nil {
		in.printf("  Hinweis: %v\n", err)
	}
	st, err := loadAccess(in.DataDir)
	if err != nil {
		in.printf("  Hinweis: Zugang von außen nicht lesbar (%v); der Leitstand geht vorerst nur über den Notausgang.\n", err)
		st = nil
	}
	return in.applyUnits(ctx, st, true)
}

// applyUnits schreibt die Units passend zum Zugang von außen (st nil: nur
// Notausgang) und startet die Website neu, auf Wunsch auch den Dienst.
func (in *installer) applyUnits(ctx context.Context, st *accessState, restartDienst bool) error {
	sock := filepath.Join(in.UnitDir, ort.UnitSocket)
	if _, err := os.Stat(sock); err == nil && st == nil {
		// Erst stoppen, solange systemd die Unit noch kennt.
		_ = in.Run(ctx, "systemctl", "disable", "--now", ort.UnitSocket)
		if err := os.Remove(sock); err != nil {
			return err
		}
	}
	if err := in.writeUnits(st); err != nil {
		return err
	}
	units := []string{ort.UnitDienst, ort.UnitWeb}
	if st != nil {
		units = append(units, ort.UnitSocket)
	}
	steps := [][]string{{"daemon-reload"}, append([]string{"enable"}, units...)}
	if restartDienst {
		steps = append(steps, []string{"restart", ort.UnitDienst})
	}
	if st != nil {
		// Den Socket nimmt systemd nur neu, solange die Website steht.
		steps = append(steps, []string{"stop", ort.UnitWeb}, []string{"restart", ort.UnitSocket}, []string{"start", ort.UnitWeb})
	} else {
		steps = append(steps, []string{"restart", ort.UnitWeb})
	}
	for _, args := range steps {
		if err := in.Run(ctx, "systemctl", args...); err != nil {
			return err
		}
	}
	return nil
}

// copyProgram legt die laufende Datei an den festen Ort. Läuft HostDeck schon
// von dort, bleibt alles, wie es ist.
func (in *installer) copyProgram() error {
	dst := in.prog()
	if in.Exe == dst {
		return nil
	}
	if err := os.MkdirAll(in.ProgDir, 0o755); err != nil {
		return err
	}
	_ = os.Chmod(in.ProgDir, 0o755)
	if err := copyExecutable(in.Exe, dst); err != nil {
		return err
	}
	in.printf("  Programm liegt jetzt in %s\n", dst)
	return nil
}

// copyExecutable kopiert src nach dst (0755). dst ändert sich erst am Ende
// und auf einmal (rename), ein laufendes Programm stört das nicht.
func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".neu"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if _, err := io.Copy(f, in); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o755); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// migrated sind die Dateien, die aus dem alten Ergebnisordner umziehen.
var migrated = []string{"inventory.json", "manifest.yaml", "manifest.yaml.ungueltig", "bericht.txt", "backup-bericht.txt", "backup-laeufe.json"}

// movedNote liegt danach im alten Ordner.
const movedNote = "UMGEZOGEN.txt"

// migrate legt den Datenordner an und holt beim ersten Mal Auswahl,
// Bestandsaufnahme und Verlauf aus dem alten Ergebnisordner. Der alte Ordner
// bleibt, bekommt aber einen Hinweis.
func (in *installer) migrate() error {
	if err := os.MkdirAll(in.ResultDir, 0o700); err != nil {
		return err
	}
	for _, d := range []string{in.DataDir, in.ResultDir} {
		if err := os.Chmod(d, 0o700); err != nil {
			return err
		}
	}
	if _, err := os.Stat(filepath.Join(in.ResultDir, "inventory.json")); err == nil {
		return nil
	}
	old, err := in.FindOld()
	if err != nil || old == "" {
		in.printf("  Keine frühere Bestandsaufnahme gefunden; HostDeck erfasst den Server gleich neu.\n")
		return nil
	}
	if abs, _ := filepath.Abs(old); abs == in.ResultDir {
		return nil
	}
	if !trustedResultDir(old) {
		in.printf("  Den früheren Ergebnisordner %s übernimmt HostDeck nicht: Er gehört einem anderen Benutzer oder andere dürfen darin schreiben. HostDeck erfasst den Server gleich neu.\n", old)
		return nil
	}
	n := 0
	for _, name := range migrated {
		b, err := readRegular(filepath.Join(old, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			in.printf("  %s nicht übernommen: %v\n", name, err)
			continue
		}
		if err := outdir.WritePrivate(filepath.Join(in.ResultDir, name), b); err != nil {
			return err
		}
		n++
	}
	note := fmt.Sprintf(`Diese Dateien sind am %s nach %s umgezogen.
HostDeck läuft jetzt als Dienst und nutzt nur noch den neuen Ordner.
Deine Auswahl änderst du ab jetzt im Leitstand. Diesen Ordner kannst du löschen.
`, in.Now().Format("02.01.2006 15:04"), in.ResultDir)
	if f, err := openNoFollow(filepath.Join(old, movedNote), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644); err == nil {
		_, _ = f.WriteString(note)
		f.Close()
	}
	in.printf("  Auswahl, Bestandsaufnahme und Verlauf übernommen aus %s (%d Dateien)\n", old, n)
	return nil
}

// readRegular liest eine gewöhnliche Datei, ohne einem Symlink zu folgen.
func readRegular(path string) ([]byte, error) {
	f, err := openNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s ist keine gewöhnliche Datei", path)
	}
	return io.ReadAll(io.LimitReader(f, 256<<20))
}

func (in *installer) writeUnits(st *accessState) error {
	domain := ""
	var extra []string
	if st != nil {
		domain, extra = st.Domain, st.Extra
	}
	units := map[string]string{
		ort.UnitDienst: unitDienst(in.prog()),
		ort.UnitWeb:    unitWeb(in.prog(), in.WebUser, domain, extra...),
	}
	if st != nil {
		units[ort.UnitSocket] = unitSocket(st.Group)
	}
	for name, text := range units {
		p := filepath.Join(in.UnitDir, name)
		if old, err := os.ReadFile(p); err == nil && string(old) == text {
			continue
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// link legt /usr/local/bin/hostdeck an, damit „sudo hostdeck …“ überall geht. Eine
// fremde Datei dort bleibt unangetastet.
func (in *installer) link() error {
	if in.BinLink == "" {
		return nil
	}
	if fi, err := os.Lstat(in.BinLink); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s ist eine eigene Datei; ich lasse sie, wie sie ist. Aufruf dann mit sudo %s", in.BinLink, in.prog())
		}
		if t, _ := os.Readlink(in.BinLink); t == in.prog() {
			return nil
		}
		if err := os.Remove(in.BinLink); err != nil {
			return err
		}
	}
	return os.Symlink(in.prog(), in.BinLink)
}

// remove nimmt die Dienste wieder heraus. Daten und Programm bleiben.
func (in *installer) remove(ctx context.Context) error {
	units := []string{ort.UnitWeb, ort.UnitDienst}
	if _, err := os.Stat(filepath.Join(in.UnitDir, ort.UnitSocket)); err == nil {
		units = append([]string{ort.UnitSocket}, units...)
	}
	_ = in.Run(ctx, "systemctl", append([]string{"disable", "--now"}, units...)...)
	for _, u := range units {
		if err := os.Remove(filepath.Join(in.UnitDir, u)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if t, err := os.Readlink(in.BinLink); err == nil && t == in.prog() {
		_ = os.Remove(in.BinLink)
	}
	return in.Run(ctx, "systemctl", "daemon-reload")
}

func einrichtenCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("einrichten", flag.ContinueOnError)
	fl.SetOutput(stderr)
	afterUpdate := fl.Bool("nach-update", false, "")
	dockerSocket := fl.String("docker-socket", dockerapi.DefaultSocket, "")
	releaseHint := fl.String("versionshinweis", "", "")
	allowOlder := fl.Bool("aeltere-version", false, "")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if *releaseHint != "" && *releaseHint != "an" && *releaseHint != "aus" {
		fmt.Fprintln(stderr, "--versionshinweis kennt nur an oder aus.")
		return 2
	}
	if runtime.GOOS != "linux" {
		fmt.Fprintln(stderr, "hostdeck einrichten geht nur auf einem Linux-Server. Am PC brauchst du es nicht.")
		return 2
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(stderr, "hostdeck einrichten braucht root: sudo ./hostdeck einrichten")
		return 2
	}
	if fi, err := os.Stat("/run/systemd/system"); err != nil || !fi.IsDir() {
		fmt.Fprintln(stderr, "Auf diesem Server läuft kein systemd. HostDeck als Dienst braucht systemd.")
		return 1
	}
	in, err := systemInstaller(stdout)
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	lock, ok := waitForLauf(ctx, "Einrichten", stdout)
	if !ok {
		return 1
	}
	defer lock.Release()
	if *afterUpdate {
		// Läuft als eigener systemd-Dienst aus der neuen Datei, angestoßen
		// vom Leitstand. Danach ist die Datei überflüssig.
		// in.Exe ändert sich beim Umstieg von sbm (Datenordner zieht um).
		defer func() { os.Remove(in.Exe) }()
		if u := findUmstieg(in); u != nil {
			if err := u.run(ctx, systemPublisher(in), dienstVersion(dienst.NewClient(ort.DienstSocket)), time.Minute, "Server (Update)"); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			if err := markInstalled(in.Exe); err != nil {
				in.printf("  Hinweis: Stand des Updates nicht gemerkt: %v\n", err)
			}
			return 0
		}
		journal := &protokoll.Log{File: filepath.Join(ort.DataDir, ort.JournalFile)}
		gone := dropTestAddressNow(in.DataDir, stderr)
		if err := applyUpdate(ctx, in, journal, dienstVersion(dienst.NewClient(ort.DienstSocket)), time.Minute); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		refreshCaddy(ctx, systemPublisher(in), journal, "Server (Update)", stdout)
		noteTestAddress(in.DataDir, gone, journal, "Server (Update)", stdout)
		return 0
	}
	fmt.Fprintf(stdout, "Richte HostDeck %s als Dienst ein …\n", version)
	var gone []string
	if u := findUmstieg(in); u != nil {
		if err := u.run(ctx, systemPublisher(in), dienstVersion(dienst.NewClient(ort.DienstSocket)), time.Minute, dienst.AdminAddr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	} else if isInstalled(in) {
		// Schon eingerichtet: wie ein Update, mit Rückweg.
		running := dienstVersion(dienst.NewClient(ort.DienstSocket))
		if err := checkOlder(installedVersion(ctx, in.prog(), running), version, *allowOlder, newTTYPrompt(os.Stdin, stdout)); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		gone = dropTestAddressNow(in.DataDir, stderr)
		journal := &protokoll.Log{File: filepath.Join(ort.DataDir, ort.JournalFile)}
		if err := applyUpdate(ctx, in, journal, running, time.Minute); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	} else {
		gone = dropTestAddressNow(in.DataDir, stderr)
		if err := in.install(ctx); err != nil {
			fmt.Fprintf(stderr, "Fehler: %v\n", err)
			return 1
		}
	}
	if _, err := os.Stat(filepath.Join(ort.ResultDir, "inventory.json")); err != nil {
		fmt.Fprintln(stdout, "\nErste Bestandsaufnahme (ändert nichts, dauert ein paar Minuten) …")
		deps := discover.Deps{Docker: dockerapi.New(*dockerSocket), FS: hostfs.New("/"), Git: discover.ExecGit{}, Caddy: discover.ExecCaddy{}}
		opts := discover.Options{ToolVersion: "HostDeck " + version, CaddyConfig: "/etc/caddy/Caddyfile", SizeTimeout: 3 * time.Minute, Euid: 0, Log: stderr}
		if code := runDiscover(ctx, deps, opts, ort.ResultDir, stdout, stderr); code != 0 {
			fmt.Fprintln(stderr, "Die Bestandsaufnahme hatte Fehler; der Leitstand zeigt, was da ist.")
		}
	}
	c := dienst.NewClient(ort.DienstSocket)
	var st dienst.Status
	for i := 0; i < 20; i++ {
		if st, err = c.Status(ctx); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		fmt.Fprintf(stderr, "Der Dienst antwortet nicht: %v\nEinzelheiten: journalctl -u hostdeck-dienst\n", err)
		return 1
	}
	journal := &protokoll.Log{File: filepath.Join(ort.DataDir, ort.JournalFile)}
	refreshCaddy(ctx, systemPublisher(in), journal, dienst.AdminAddr, stdout)
	noteTestAddress(in.DataDir, gone, journal, dienst.AdminAddr, stdout)
	passwordHint := ""
	if ps, err := c.PasswordState(ctx); err == nil && !ps.Set {
		p := newTTYPrompt(os.Stdin, stdout)
		if p.Interactive() {
			if err := passwordDialog(ctx, c, p); err != nil {
				fmt.Fprintf(stderr, "Passwort nicht festgelegt: %v\n", err)
				passwordHint = "\nEin Leitstand-Passwort fehlt noch: sudo hostdeck passwort\n"
			}
		} else {
			passwordHint = "\nEin Leitstand-Passwort fehlt noch: sudo hostdeck passwort\n"
		}
	}
	tty := newTTYPrompt(os.Stdin, stdout)
	backupHint := ""
	if dir, err := stateDir(); err == nil && tty.Interactive() {
		if (&serverBackups{State: dir}).problem() != "" {
			if err := backupPasswordDialog(tty, dir, filepath.Join(ort.DataDir, ort.BackupRepo), false); err != nil {
				fmt.Fprintf(stderr, "Backup-Passwort nicht festgelegt: %v\n", err)
			}
		}
	}
	if dir, err := stateDir(); err == nil {
		if p := (&serverBackups{State: dir}).problem(); p != "" {
			backupHint = "\n" + p + "\n"
		}
	}
	askReleaseHint(&neueversion.Service{File: filepath.Join(ort.DataDir, ort.ReleaseFile), Version: version}, *releaseHint, tty, stdout, stderr)
	// Freischalten nimmt die Laufsperre selbst.
	lock.Release()
	if tty.Interactive() && passwordHint == "" {
		offerPublish(ctx, systemPublisher(in), tty, tty.r, stdout, stderr)
	}
	fmt.Fprintf(stdout, `
Fertig. HostDeck %s läuft jetzt als Dienst auf diesem Server.
  Programm:  %s
  Daten:     %s
  Dienste:   hostdeck-dienst und hostdeck-web, starten mit dem Server
  Nachts um 03:30 erfasst HostDeck den Server neu.

%s%s%s
`, st.Version, ort.Prog, ort.DataDir, accessHint(ctx, in), passwordHint, backupHint)
	return 0
}

// askReleaseHint fragt beim ersten Einrichten, ob HostDeck nach neuen Versionen
// sehen soll. Mit --versionshinweis an|aus ohne Frage; ohne Terminal
// und ohne Angabe bleibt es aus. Wer schon gewählt hat, wird nicht wieder
// gefragt; ändern geht im Leitstand unter Updates.
func askReleaseHint(s *neueversion.Service, flag string, p prompt, stdout, stderr io.Writer) {
	set := func(on bool) {
		if err := s.SetOn(on); err != nil {
			fmt.Fprintf(stderr, "Hinweis auf neue Versionen nicht gespeichert: %v\n", err)
			return
		}
		if on {
			fmt.Fprintf(stdout, "  Hinweis auf neue Versionen ist an: HostDeck sieht einmal am Tag bei GitHub nach (%s).\n", neueversion.Repo)
		} else {
			fmt.Fprintln(stdout, "  Hinweis auf neue Versionen ist aus. Einschalten geht im Leitstand unter Updates.")
		}
	}
	if flag != "" {
		set(flag == "an")
		return
	}
	if s.Load().On != nil || !p.Interactive() {
		return
	}
	p.Say(`
Hinweis auf neue Versionen

HostDeck kann einmal am Tag bei GitHub nachsehen, ob es eine neue Version gibt,
und zeigt das dann im Leitstand. GitHub sieht dabei nur die Adresse dieses
Servers und die Version von HostDeck. Eingespielt wird nie von selbst.

`)
	for {
		a, err := p.Line("Einschalten? [J/n]: ")
		if err != nil {
			return
		}
		switch strings.ToLower(strings.TrimSpace(a)) {
		case "", "j", "ja", "y", "yes":
			set(true)
			return
		case "n", "nein", "no":
			set(false)
			return
		}
	}
}

// offerPublish fragt beim Einrichten, unter welcher Adresse der Leitstand
// von außen erreichbar sein soll, und schaltet ihn gleich frei. So braucht
// niemand den Notausgang, nur um freizuschalten.
func offerPublish(ctx context.Context, pub *publisher, p prompt, stdin io.Reader, stdout, stderr io.Writer) {
	st := pub.State(ctx)
	if !st.Caddy || st.Domain != "" {
		return
	}
	sug := st.Suggest
	p.Say(`
Leitstand von außen erreichbar machen

Dann öffnest du ihn im Browser unter einer eigenen Adresse, mit deinem
Leitstand-Passwort, auch am Handy und ohne SSH. Dafür braucht die Adresse
beim DNS-Anbieter einen A-Eintrag auf diesen Server, wie deine anderen
Subdomains. HostDeck prüft das und zeigt vorher, was sich an Caddy ändert.

`)
	q := "Adresse eingeben"
	if sug != "" {
		q += " (Enter = " + sug + ")"
	}
	q += ", oder n für später: "
	for {
		a, err := p.Line(q)
		if err != nil {
			return
		}
		a = strings.TrimSpace(a)
		if a == "" {
			a = sug
		}
		if a == "" {
			continue
		}
		if strings.EqualFold(a, "n") {
			return
		}
		if _, err := pub.Check(ctx, a); err != nil {
			p.Say("%v\n\nNoch einmal versuchen, oder n für später (dann im Leitstand unter System > Von außen).\n\n", err)
			continue
		}
		runFreischalten(ctx, pub, a, false, false, stdin, stdout, stderr)
		return
	}
}

// refreshCaddy frischt die Caddy-Datei des Leitstands auf, falls diese
// Version eine andere schreibt. Ein Fehler hält Einrichten und Update nicht
// auf: Die alte Datei liegt dann wieder da, der Leitstand läuft weiter.
func refreshCaddy(ctx context.Context, p *publisher, journal *protokoll.Log, addr string, out io.Writer) {
	msg, err := p.Refresh(ctx)
	if err != nil {
		msg = strings.TrimSpace(msg + " Die Caddy-Datei des Leitstands ließ sich nicht auffrischen, die bisherige gilt weiter: " + err.Error())
	}
	if msg == "" {
		return
	}
	fmt.Fprintln(out, msg)
	if journal != nil {
		_ = journal.Add(protokoll.Entry{Kind: protokoll.Zugang, Text: msg, Addr: addr})
	}
}

// dropTestAddressNow räumt beim Einrichten und beim Update einmal auf, wenn
// ein schon übernommener Leitstand noch die Testadresse der Probe-Kopie
// führt (Server, die vor diesem Fix umgezogen sind). Danach
// schreibt das Einrichten die Units ohne sie, refreshCaddy die Caddy-Datei.
func dropTestAddressNow(dataDir string, stderr io.Writer) []string {
	gone, err := dropTestAddress(dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "Testadresse ließ sich nicht aus dem Zugang nehmen: %v\n", err)
	}
	return gone
}

// noteTestAddress schreibt ins Protokoll, dass die Testadresse weg ist, aber
// nur, wenn die Caddy-Datei sie wirklich nicht mehr enthält.
func noteTestAddress(dataDir string, gone []string, journal *protokoll.Log, addr string, out io.Writer) {
	if len(gone) == 0 {
		return
	}
	st, err := loadAccess(dataDir)
	if err != nil || st == nil {
		return
	}
	b, err := os.ReadFile(st.Site)
	if err != nil || !bytes.Equal(b, siteBlock(st.Domain, st.Extra...)) {
		msg := "Die Testadresse " + strings.Join(gone, ", ") + " steht noch in der Caddy-Datei des Leitstands; das nächste Update versucht es wieder. Den DNS-Eintrag bis dahin stehen lassen."
		fmt.Fprintln(out, msg)
		if journal != nil {
			_ = journal.Add(protokoll.Entry{Kind: protokoll.Zugang, Text: msg, Addr: addr})
		}
		return
	}
	msg := testAddressText(gone)
	fmt.Fprintln(out, msg)
	if journal != nil {
		_ = journal.Add(protokoll.Entry{Kind: protokoll.Zugang, Text: msg, Addr: addr})
	}
}

// accessHint sagt am Ende des Einrichtens, wie man den Leitstand erreicht.
func accessHint(ctx context.Context, in *installer) string {
	st := systemPublisher(in).State(ctx)
	// Probe-Kopie: Die Leitstand-Adresse führt noch zum alten Server; hier
	// geht es über die Testadresse.
	if c := loadCopy(in.DataDir); c != nil && c.TestDomain != "" {
		what := "Diese Probe-Kopie erreichst du"
		if c.Move {
			what = "Den neuen Leitstand erreichst du bis zur Übernahme"
		}
		return what + " unter https://" + c.TestDomain + "\n(Notausgang, falls das nicht geht: sudo hostdeck notausgang)\n"
	}
	switch {
	case st.Domain != "":
		return "Den Leitstand erreichst du unter https://" + st.Domain + "\n(Notausgang, falls das nicht geht: sudo hostdeck notausgang)\n"
	case st.Caddy && st.Suggest != "":
		return "Den Leitstand öffnest du über den Notausgang:\n\n  sudo hostdeck notausgang\n\nVon außen erreichbar machen: im Leitstand unter System > Zugang von außen\n(oder: sudo hostdeck freischalten " + st.Suggest + ")\n"
	default:
		return "Den Leitstand öffnest du über den Notausgang:\n\n  sudo hostdeck notausgang\n"
	}
}

// waitForLauf wartet, bis kein anderer Lauf mehr arbeitet, und nimmt dann
// die Sperre.
func waitForLauf(ctx context.Context, what string, w io.Writer) (*lauf.Lock, bool) {
	return waitForLaufIn(ctx, lauf.Dir(), what, w)
}

func waitForLaufIn(ctx context.Context, dir, what string, w io.Writer) (*lauf.Lock, bool) {
	said := false
	for {
		l, err := lauf.TakeIn(dir, what)
		if err == nil {
			return l, true
		}
		var busy *lauf.BusyError
		if !errors.As(err, &busy) {
			fmt.Fprintf(w, "Fehler: Laufsperre: %v\n", err)
			return nil, false
		}
		if !said {
			fmt.Fprintf(w, "%v\nIch warte, bis er fertig ist. Strg+C bricht ab.\n", err)
			said = true
		}
		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(5 * time.Second):
		}
	}
}
