package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/aktualisieren"
	"github.com/officialtaxz2/hostdeck/internal/altserver"
	"github.com/officialtaxz2/hostdeck/internal/anmeldung"
	"github.com/officialtaxz2/hostdeck/internal/aufraeumen"
	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/betrieb"
	"github.com/officialtaxz2/hostdeck/internal/caddy"
	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/discover"
	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/domains"
	"github.com/officialtaxz2/hostdeck/internal/hinweise"
	"github.com/officialtaxz2/hostdeck/internal/hostfs"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/leistung"
	"github.com/officialtaxz2/hostdeck/internal/leistungstest"
	"github.com/officialtaxz2/hostdeck/internal/melden"
	"github.com/officialtaxz2/hostdeck/internal/neueversion"
	"github.com/officialtaxz2/hostdeck/internal/ort"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/sicherheit"
	"github.com/officialtaxz2/hostdeck/internal/sitzung"
	"github.com/officialtaxz2/hostdeck/internal/spiele"
	"github.com/officialtaxz2/hostdeck/internal/tuning"
	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
	"github.com/officialtaxz2/hostdeck/internal/umzugstag"
	"github.com/officialtaxz2/hostdeck/internal/update"
	"github.com/officialtaxz2/hostdeck/internal/web"
	"github.com/officialtaxz2/hostdeck/internal/zustand"
)

// nightlyAt ist die Uhrzeit der nächtlichen Bestandsaufnahme.
const nightlyAt = 3*time.Hour + 30*time.Minute

// dienstCmd ist der root-Teil des Leitstands. systemd startet ihn
// (hostdeck-dienst.service).
func dienstCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("dienst", flag.ContinueOnError)
	fl.SetOutput(stderr)
	socket := fl.String("socket", ort.DienstSocket, "")
	result := fl.String("ergebnis", ort.ResultDir, "")
	data := fl.String("daten", ort.DataDir, "")
	webUser := fl.String("web-user", ort.WebUser, "")
	dockerSocket := fl.String("docker-socket", dockerapi.DefaultSocket, "")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(stderr, "hostdeck dienst läuft als root und wird von systemd gestartet. Einrichten: sudo hostdeck einrichten")
		return 2
	}
	u, err := user.Lookup(*webUser)
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: Benutzer %s fehlt (%v). Einrichten: sudo hostdeck einrichten\n", *webUser, err)
		return 1
	}
	webUID, _ := strconv.Atoi(u.Uid)
	if err := os.MkdirAll(*result, 0o700); err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	ln, err := listenUnix(*socket, 0o666)
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	log := &lineLog{w: stdout}
	d := newDienst(*result, *dockerSocket, webUID, log)
	withDoor(d, *data)
	if key, err := update.ParsePublicKey(update.PublicKey); err == nil {
		u := &updateReceiver{Dir: filepath.Join(*data, "update"), RunDir: lauf.Dir(), Key: key, Version: version, Start: startUpdate}
		d.Update = u.receive
	}
	if dir, err := stateDir(); err == nil {
		host, _ := os.Hostname()
		sb := &serverBackups{
			Repo: filepath.Join(*data, ort.BackupRepo), State: dir, Result: *result, Socket: *dockerSocket,
			LogDir: filepath.Join(*data, ort.RunLogs), Host: host,
		}
		sb.Downloading = d.Downloading
		d.Backups = sb
		d.Moves = &serverMoves{Stored: sb.Stored, Repo: sb.Repo, PasswordFile: backupPasswordFile(dir), Dir: filepath.Join(*data, ort.MoveDir),
			Sites: func(ctx context.Context) ([]string, error) {
				c, err := caddy.Find(ctx, caddy.Exec)
				if err != nil {
					return nil, err
				}
				return c.Hosts(ctx)
			},
			Own: func() string {
				if d.Access != nil {
					return d.Access.Domain()
				}
				return ""
			},
			Pack:  func() ([]byte, []string, error) { return packTakeover(*data) },
			Final: sb.RunFinal,
			LeftOff: func() int {
				st, err := backup.LoadStopState(backup.StopStatePath(dir))
				if err != nil {
					return 0
				}
				n := 0
				for _, e := range st.Entries() {
					if e.Final {
						n++
					}
				}
				return n
			},
			Restart: func(ctx context.Context, w io.Writer) []string {
				st, err := backup.LoadStopState(backup.StopStatePath(dir))
				if err != nil {
					fmt.Fprintf(w, "  ✗ Liste der gestoppten Container nicht lesbar: %v\n", err)
					return []string{"(Liste unlesbar)"}
				}
				return backup.StartRecorded(ctx, dockerapi.NewControl(*dockerSocket), st, w, 10*time.Second)
			},
			Inventory: func() (*inventory.Inventory, error) {
				b, err := os.ReadFile(filepath.Join(*result, "inventory.json"))
				if err != nil {
					return nil, err
				}
				var inv inventory.Inventory
				if err := json.Unmarshal(b, &inv); err != nil {
					return nil, err
				}
				return &inv, nil
			},
		}
	}
	ops := dockerapi.NewOps(*dockerSocket)
	cont := &betrieb.Service{Docker: ops, StoppedFile: filepath.Join(*data, ort.StoppedFile), MoveStopped: moveStopped}
	d.Containers = cont
	registry := &aktualisieren.Registry{DockerConfig: "/root/.docker/config.json"}
	d.Updates = &aktualisieren.Updater{
		Docker: ops, CLI: aktualisieren.Exec{}, StateDir: filepath.Join(*data, ort.UpdatesDir),
		Registry: registry,
		Domains:  projectDomains(*result),
		Probe:    caddy.LocalProber(443, nil),
	}
	dms := &domains.Service{
		Find:       func(ctx context.Context) (*caddy.Caddy, error) { return caddy.Find(ctx, caddy.Exec) },
		Probe:      caddy.LocalProber(443, nil),
		Cert:       domains.LocalCert,
		Listening:  domains.LocalListening,
		Resolve:    resolveIPs,
		Local:      localIPs,
		ProjectOf:  projectOfPath(*result),
		Ready:      domainReady,
		StateFile:  filepath.Join(*data, ort.DomainsFile),
		Journal:    filepath.Join(*data, ort.CaddyJournal),
		Retries:    3,
		RetryDelay: 3 * time.Second,
	}
	d.Domains = dms
	d.DomainStates = func(ctx context.Context, sites []domains.Site) []zustand.Domain {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var conts []betrieb.Container
		if snap, err := cont.States(ctx); err == nil {
			conts = snap.Containers
		}
		return zustand.Domains(sites, conts, loadInventory(*result), time.Now())
	}
	trashDir := filepath.Join(*data, ort.TrashDir)
	host, _ := os.Hostname()
	cleanup := &aufraeumen.Service{
		Docker: dockerapi.NewCleaner(*dockerSocket),
		Trash: &aufraeumen.ResticTrash{
			Dir: trashDir, Host: host, Log: log,
			Bin: func(ctx context.Context) (string, error) {
				dir, err := stateDir()
				if err != nil {
					return "", err
				}
				return resticBinary(ctx, "", dir, io.Discard)
			},
		},
		Caddyfile: func(ctx context.Context) (string, error) {
			c, err := caddy.Find(ctx, caddy.Exec)
			if err != nil {
				return "", err
			}
			return c.Config, nil
		},
		Disk:        diskDF,
		RootReserve: diskReserve,
		Dir:         trashDir,
		ProjectDir:  projectDirOf(*result),
		Pullable:    pullableFrom(registry),
		Unselect:    func(keys []string) (int, error) { return web.Unselect(*result, keys) },
	}
	d.Cleanup = cleanup
	space := spaceService(*dockerSocket, *data, *result, cleanup)
	cleanup.RestoreCheck = space.CheckRestore
	cleanup.Restored = space.Changed
	d.Space = space
	d.Security = &sicherheit.Service{
		Docker:    ops,
		Run:       runCombined,
		Proxied:   proxiedPorts(*result),
		RuleFile:  filepath.Join(*data, ort.ShieldRules),
		StateFile: filepath.Join(*data, ort.ShieldFile),
		UnitFile:  filepath.Join(ort.UnitDir, ort.ShieldUnit),
		OnExpire:  d.SecurityExpired,
		Old:       func() *altserver.Info { return altserver.Load(*data) },
	}
	perf := &leistung.Service{
		Docker: ops,
		Run:    runCombined,
		Dir:    filepath.Join(*data, ort.PerfDir),
		Disk:   diskSize,
		Old:    func() *altserver.Info { return altserver.Load(*data) },
	}
	d.Performance = perf
	d.Benchmarks = &leistungstest.Service{Dir: filepath.Join(*data, ort.BenchDir), Host: host}
	d.Tuning = tuning.FromEnv(d.RunDir)
	d.OldTuning = func() (string, *tuning.Applied, []tuning.PrioSet) {
		if old := altserver.Load(*data); old != nil {
			return old.From, old.Tuning, old.Prio
		}
		return "", nil, nil
	}
	d.DropOldTuning = func() error { return altserver.DropTuning(*data) }
	isCopy := func() *dienst.CopyInfo { return copyInfo(*data) }
	d.Copy = isCopy
	d.Replaced = func() *umzugstag.Done { return (umzugstag.Store{Dir: filepath.Join(*data, ort.MoveDir)}).Done() }
	d.ModeFile = filepath.Join(*data, ort.MoveDir, modeFile)
	d.FinishFile = filepath.Join(*data, finishFile)
	d.TestAddresses = func() []string {
		if st, err := loadAccess(*data); err == nil && st != nil {
			return st.Extra
		}
		return nil
	}
	d.ProbeFile = filepath.Join(*data, ort.MoveDir, "probe-ziel.json")
	d.LookFile = filepath.Join(*data, ort.LookFile)
	d.Release = &neueversion.Service{File: filepath.Join(*data, ort.ReleaseFile), Version: version}
	go d.Release.Run(ctx, log)
	d.Arrived = func() bool {
		_, err := os.Stat(filepath.Join(*data, arrivedFile))
		return err == nil
	}
	notify := &melden.Service{
		File: filepath.Join(*data, ort.MonitorDir, "nachrichten.json"),
		Quiet: func() bool {
			return isCopy() != nil || (umzugstag.Store{Dir: filepath.Join(*data, ort.MoveDir)}).Done() != nil
		},
		Base: func() string {
			if d.Access != nil {
				if dom := d.Access.Domain(); dom != "" {
					return "https://" + dom
				}
			}
			return ""
		},
		Note: func(text string, details []string) {
			if d.Journal != nil {
				_ = d.Journal.Add(protokoll.Entry{Kind: protokoll.Ueberwachung, Text: text, Details: details, Addr: "Server (Nachrichten)"})
			}
		},
	}
	d.Notify = notify
	rules := monitorRules(d, cont, dms, notify, *result, *data)
	dms.Changed = rules.RecheckSites
	d.Events = rules.Journal
	if err := rules.Journal.Started(version); err != nil {
		fmt.Fprintf(log, "Ereignisse: %v\n", err)
	}
	d.Limits = rules
	d.Alerts = rules
	hints := hintsFor(d, perf)
	hints.Alerts = alertHints(rules)
	hints.Limits = rules.CurrentLimits
	hints.Extra = dienst.NeueVersionHints(d.Release)
	perf.Limits = rules.CurrentLimits
	d.Hints = hints
	mon := &ueberwachung.Service{
		Collector: &ueberwachung.Collector{Docker: ops, Disk: diskDF},
		Store:     &ueberwachung.Store{Dir: filepath.Join(*data, ort.MonitorDir)},
		Log:       func(format string, args ...any) { fmt.Fprintf(log, format+"\n", args...) },
		Rules:     rules,
		// Jede Sekunde nur, solange jemand die Live-Ansicht offen hat (E21).
		Live: &ueberwachung.Live{Collector: &ueberwachung.Collector{Docker: ops, ListEvery: ueberwachung.LiveListEvery}},
	}
	// Spielserver erkennen, nur zur Einordnung; HostDeck fragt Spiele nicht ab.
	mon.Games = &spiele.Service{Docker: ops, FS: hostfs.New("/")}
	d.Monitor = mon
	// Prozessor je Container als Anteil am Server, mit denselben Kernen wie
	// die Überwachung (E1).
	cont.Cores = mon.Cores
	// Prozessor und Speicher je Container aus der Messung der Überwachung,
	// wo sie misst; sonst docker stats (eine Quelle für alle Seiten).
	cont.Measured = func() map[string]betrieb.Measured {
		now := mon.ContainersNow()
		if len(now) == 0 {
			return nil
		}
		out := make(map[string]betrieb.Measured, len(now))
		for k, v := range now {
			out[k] = betrieb.Measured{CPU: v.CPU, Mem: v.RAM, MemLimit: v.MemLimit}
		}
		return out
	}
	if in, err := systemInstaller(io.Discard); err == nil {
		in.DataDir = *data
		p := systemPublisher(in)
		d.Access = p
		recoverCaddy(ctx, p, d, stdout)
	}
	watchMove(ctx, d, *data, log)
	fmt.Fprintf(stdout, "hostdeck dienst %s läuft (%s, Daten in %s)\n", version, *socket, *result)
	if err := d.Serve(ctx, ln); err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	return 0
}

// recoverCaddy räumt beim Start auf, falls ein Absturz oder Neustart eine
// Änderung an Caddy unterbrochen hat. Hält gerade jemand die Laufsperre
// (etwa sudo hostdeck freischalten), räumt der selbst auf.
func recoverCaddy(ctx context.Context, p *publisher, d *dienst.Dienst, out io.Writer) {
	l, err := lauf.TakeIn(d.RunDir, "Aufräumen nach einer unterbrochenen Änderung an Caddy")
	if err != nil {
		return
	}
	defer l.Release()
	msg, err := p.recoverChange(ctx)
	if err != nil {
		msg = "Aufräumen nach einer unterbrochenen Änderung an Caddy ging nicht: " + err.Error()
	}
	if msg == "" {
		return
	}
	fmt.Fprintln(out, msg)
	if d.Journal != nil {
		_ = d.Journal.Add(protokoll.Entry{Kind: protokoll.Zugang, Text: msg, Addr: dienst.AdminAddr})
	}
}

// newDienst baut den Dienst mit echter Bestandsaufnahme und Rettung
// abgebrochener Läufe.
func newDienst(result, dockerSocket string, webUID int, log io.Writer) *dienst.Dienst {
	return &dienst.Dienst{
		Data:    (&web.Server{Dir: result, Log: log}).DataHandler(),
		RunDir:  lauf.Dir(),
		Version: version,
		WebUID:  webUID,
		Discover: func(ctx context.Context) error {
			deps := discover.Deps{Docker: dockerapi.New(dockerSocket), FS: hostfs.New("/"), Git: discover.ExecGit{}, Caddy: discover.ExecCaddy{}}
			opts := discover.Options{ToolVersion: "HostDeck " + version, CaddyConfig: "/etc/caddy/Caddyfile", SizeTimeout: 3 * time.Minute, Euid: os.Geteuid(), Log: log}
			if code := runDiscover(ctx, deps, opts, result, log, log); code != 0 {
				return fmt.Errorf("die Bestandsaufnahme endete mit Fehler (Code %d); Einzelheiten im Protokoll: journalctl -u hostdeck-dienst", code)
			}
			return nil
		},
		Recover: func(ctx context.Context) { rescueStopped(ctx, dockerSocket, log) },
		Nightly: nightlyAt,
		Log:     log,
	}
}

// withDoor gibt dem Dienst Passwort, Sperre, Anmeldungen und Protokoll in
// dataDir.
func withDoor(d *dienst.Dienst, dataDir string) {
	file := func(name string) string { return filepath.Join(dataDir, name) }
	d.Password = &anmeldung.Passwort{File: file(ort.PasswordFile)}
	d.Lockout = &anmeldung.Sperre{File: file(ort.LockoutFile)}
	d.Sessions = sitzung.New(file(ort.SessionsFile))
	d.Journal = &protokoll.Log{File: file(ort.JournalFile)}
	d.EmergencyGate = ort.GateNotausgang
}

// rescueStopped startet Container, die ein abgebrochener Lauf gestoppt hat
// (Absturz, Neustart des Servers mitten im Backup).
func rescueStopped(ctx context.Context, socket string, w io.Writer) {
	dir, err := stateDir()
	if err != nil {
		return
	}
	st, err := backup.LoadStopState(backup.StopStatePath(dir))
	if err != nil {
		fmt.Fprintf(w, "Gestoppte Container nicht lesbar: %v\n", err)
		return
	}
	n := len(st.Interrupted())
	if n == 0 {
		return
	}
	fmt.Fprintf(w, "Ein Lauf wurde abgebrochen. Starte %d Container wieder, die HostDeck gestoppt hatte:\n", n)
	bad := backup.StartInterrupted(ctx, dockerapi.NewControl(socket), st, w, 10*time.Second)
	if len(bad) > 0 {
		fmt.Fprintf(w, "Läuft danach nicht: %s\n", strings.Join(bad, ", "))
	}
}

// webCmd ist die Website des Leitstands. Sie läuft als eigener Benutzer
// ohne Rechte (hostdeck-web.service) und kommt nur über den Dienst an Daten.
// Nach dem Freischalten reicht systemd ihr zusätzlich den Eingang für
// Caddy (hostdeck-web.socket); dort gilt nur die freigeschaltete Adresse.
func webCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("web", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dienstSock := fl.String("dienst", ort.DienstSocket, "")
	notSock := fl.String("notausgang", ort.NotausgangSocket, "")
	domain := fl.String("adresse", "", "")
	also := fl.String("auch", "", "") // weitere Adressen, durch Komma getrennt (Testadresse)
	if err := fl.Parse(args); err != nil {
		return 2
	}
	var extra []string
	for _, a := range strings.Split(*also, ",") {
		if a = normDomain(a); a != "" && validDomain(a) == nil {
			extra = append(extra, a)
		}
	}
	if os.Geteuid() == 0 {
		fmt.Fprintf(stderr, "hostdeck web läuft nie als root, sondern als Benutzer %s (systemd: hostdeck-web).\n", ort.WebUser)
		return 2
	}
	got, err := activated()
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	ln, err := listenUnix(*notSock, 0o600)
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	log := &lineLog{w: stdout}
	c := dienst.NewClient(*dienstSock)
	gates := []served{{ln, newWeb(c, log)}}
	where := "Notausgang " + *notSock
	if pub := got["caddy"]; pub != nil {
		if *domain == "" {
			fmt.Fprintln(stderr, "Eingang für Caddy ohne freigeschaltete Adresse; er bleibt zu. Neu einrichten: sudo hostdeck einrichten")
			pub.Close()
		} else {
			gates = append(gates, served{pub, newPublicWeb(c, log, *domain, extra...)})
			where += ", von außen über Caddy: https://" + *domain
			for _, e := range extra {
				where += ", https://" + e
			}
		}
	}
	for name, l := range got {
		if name != "caddy" {
			l.Close()
		}
	}
	fmt.Fprintf(stdout, "hostdeck web %s läuft (%s)\n", version, where)
	return serveAll(ctx, gates, stderr)
}

// newWeb baut die Website vor dem Dienst für den Notausgang; dort geht der
// Einmal-Link oder das Leitstand-Passwort.
func newWeb(c *dienst.Client, log io.Writer) http.Handler {
	f := &web.Front{API: c.Proxy(), Door: c.Door(), Log: log}
	return f.Handler(web.Gate{
		Name: ort.GateNotausgang, Hosts: web.LoopbackHost, Cookie: "hostdeck", Password: true,
		Hint: "Bitte melde dich mit deinem Leitstand-Passwort an. Ohne Passwort: auf dem Server sudo hostdeck notausgang eingeben und den Link öffnen.",
	})
}

// newPublicWeb baut die Website für den Weg über Caddy: nur die
// freigeschaltete Adresse, nur HTTPS-Cookies, nur Passwort, keine
// Einmal-Links. Die Adresse des Besuchers kommt von Caddy.
func newPublicWeb(c *dienst.Client, log io.Writer, domain string, extra ...string) http.Handler {
	f := &web.Front{API: c.Proxy(), Door: c.Door(), Log: log}
	return f.Handler(web.Gate{
		Name: domain, Hosts: web.ExactHosts(append([]string{domain}, extra...)...), Cookie: "__Host-hostdeck", Secure: true, Password: true, NoLinks: true,
		Hint: "Bitte melde dich mit deinem Leitstand-Passwort an.",
		Addr: web.ForwardedFor,
	})
}

type served struct {
	ln net.Listener
	h  http.Handler
}

// serveAll bedient mehrere Eingänge, bis ctx endet oder einer ausfällt.
func serveAll(ctx context.Context, gates []served, stderr io.Writer) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	codes := make(chan int, len(gates))
	for _, g := range gates {
		go func() {
			code := serveHTTP(ctx, g.ln, g.h, stderr)
			cancel()
			codes <- code
		}()
	}
	worst := 0
	for range gates {
		if c := <-codes; c > worst {
			worst = c
		}
	}
	return worst
}

func serveHTTP(ctx context.Context, ln net.Listener, h http.Handler, stderr io.Writer) int {
	hs := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
		return 0
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return 0
		}
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
}

// listenUnix lauscht auf einem Unix-Socket. Ein alter Socket von einem
// früheren Start wird ersetzt, eine andere Datei nicht.
func listenUnix(path string, mode os.FileMode) (net.Listener, error) {
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s ist kein Socket; bitte prüfen", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, mode); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// notausgangCmd öffnet den Leitstand über einen SSH-Tunnel, auch wenn Caddy
// oder DNS nicht gehen oder die Anmeldung gesperrt ist.
func notausgangCmd(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("notausgang", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dienstSock := fl.String("dienst", ort.DienstSocket, "")
	notSock := fl.String("notausgang", ort.NotausgangSocket, "")
	port := fl.Int("port", defaultPort, "")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(stderr, "Der Notausgang braucht root: sudo hostdeck notausgang")
		return 2
	}
	return runNotausgang(ctx, dienst.NewClient(*dienstSock), *notSock, *port, stdout, stderr)
}

func runNotausgang(ctx context.Context, c *dienst.Client, notSock string, port int, stdout, stderr io.Writer) int {
	link, err := c.NewLink(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\nLäuft der Dienst? systemctl status hostdeck-dienst\n", err)
		return 1
	}
	if conn, err := net.DialTimeout("unix", notSock, 3*time.Second); err != nil {
		fmt.Fprintf(stderr, "Fehler: die Website antwortet nicht (%v).\nLäuft sie? systemctl status hostdeck-web\n", err)
		return 1
	} else {
		conn.Close()
	}
	var ln net.Listener
	for p := port; p < port+10; p++ {
		if ln, err = net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p))); err == nil {
			break
		}
	}
	if ln == nil {
		fmt.Fprintf(stderr, "Fehler: kein freier Port ab %d (%v)\n", port, err)
		return 1
	}
	actual := ln.Addr().(*net.TCPAddr).Port
	fmt.Fprint(stdout, notausgangHelp(actual, link, os.Getenv("SSH_CONNECTION"), sshUser()))
	var wg sync.WaitGroup
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	allowed := allowedPeers()
	for {
		conn, err := ln.Accept()
		if err != nil {
			break
		}
		// Den Port auf 127.0.0.1 erreicht jeder Benutzer am Server. Durch
		// darf nur root und wer sudo hostdeck notausgang aufrief, also dessen
		// SSH-Tunnel.
		if uid, err := tcpPeerUID(conn); err == nil && !allowed[uid] || err != nil && !errors.Is(err, errNoPeerInfo) {
			fmt.Fprintf(stderr, "Verbindung abgewiesen: Sie kommt von einem anderen Benutzer am Server (%v).\n", peerText(uid, err))
			conn.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			forward(ctx, conn, notSock)
		}()
	}
	wg.Wait()
	fmt.Fprintln(stdout, "\nNotausgang geschlossen.")
	return 0
}

// allowedPeers sind root und der Benutzer, der sudo aufrief.
func allowedPeers() map[int]bool {
	ok := map[int]bool{0: true, os.Getuid(): true}
	if id, err := strconv.Atoi(os.Getenv("SUDO_UID")); err == nil {
		ok[id] = true
	}
	return ok
}

func peerText(uid int, err error) string {
	if err != nil {
		return err.Error()
	}
	return "Benutzer-ID " + strconv.Itoa(uid)
}

// forward reicht eine TCP-Verbindung an den Socket der Website weiter.
func forward(ctx context.Context, conn net.Conn, sock string) {
	defer conn.Close()
	up, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		return
	}
	defer up.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, conn); closeWrite(up); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, up); closeWrite(conn); done <- struct{}{} }()
	select {
	case <-done:
		<-done
	case <-ctx.Done():
	}
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
}

func notausgangHelp(port int, l dienst.Link, sshConn, user string) string {
	target := "<server>"
	sshPort := ""
	if f := strings.Fields(sshConn); len(f) == 4 {
		target = f[2]
		if f[3] != "22" {
			sshPort = " -p " + f[3]
		}
	}
	if user == "" {
		user = "<benutzer>"
	}
	p := strconv.Itoa(port)
	return fmt.Sprintf(`
Notausgang offen. Der Leitstand ist jetzt über 127.0.0.1:%[1]s auf diesem Server erreichbar.

So öffnest du ihn auf deinem PC:

  1. Auf dem PC ein neues Terminal öffnen (Windows: PowerShell) und eingeben:

       ssh -N -L %[1]s:127.0.0.1:%[1]s%[2]s %[3]s@%[4]s

     Das Fenster bleibt danach leer, das ist richtig. Offen lassen.

  2. Im Browser diese Adresse öffnen:

       http://127.0.0.1:%[1]s/#%[5]s

Der Link gilt bis %[6]s und nur einmal. Danach bleibt die Seite offen,
solange dieses Fenster läuft. Beenden mit Strg+C.

`, p, sshPort, user, target, l.Token, l.Until.Local().Format("15:04"))
}

// lineLog schreibt jede Meldung mit Uhrzeit; systemd legt sie ins Journal.
type lineLog struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lineLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// projectDomains liest die Domains eines Projekts aus der letzten
// Bestandsaufnahme.
func projectDomains(result string) func(string) []string {
	return func(project string) []string {
		b, err := os.ReadFile(filepath.Join(result, "inventory.json"))
		if err != nil {
			return nil
		}
		var inv inventory.Inventory
		if json.Unmarshal(b, &inv) != nil {
			return nil
		}
		for _, p := range inv.Projects {
			if p.Name == project {
				return p.Domains
			}
		}
		return nil
	}
}

// hintsFor sammelt die Hinweise aus den Teilen des Dienstes.
func hintsFor(d *dienst.Dienst, perf *leistung.Service) *hinweise.Service {
	h := &hinweise.Service{Disk: diskDF, Memory: perf.Mem}
	if c := d.Containers; c != nil {
		h.Containers = c.List
	}
	if dm := d.Domains; dm != nil {
		h.Sites = func(ctx context.Context) []domains.Site { return dm.Overview(ctx).Sites }
	}
	if sec := d.Security; sec != nil {
		h.Security = sec.Overview
	}
	return h
}

// runCombined startet ein Programm und liefert Ausgabe und Fehlerausgabe.
func runCombined(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// proxiedPorts liefert je Port die Domains, die Caddy laut letzter
// Bestandsaufnahme dorthin weiterreicht.
func proxiedPorts(result string) func(context.Context) map[int][]string {
	return func(context.Context) map[int][]string {
		b, err := os.ReadFile(filepath.Join(result, "inventory.json"))
		if err != nil {
			return nil
		}
		var inv inventory.Inventory
		if json.Unmarshal(b, &inv) != nil || inv.Caddy == nil {
			return nil
		}
		m := map[int][]string{}
		for _, r := range inv.Caddy.Routes {
			if r.Port > 0 && r.Handler == "reverse_proxy" {
				m[r.Port] = append(m[r.Port], r.Hosts...)
			}
		}
		return m
	}
}

// projectDirOf liefert den Ordner eines Compose-Projekts laut letzter
// Bestandsaufnahme, wenn es ihn noch gibt.
func projectDirOf(result string) func(string) string {
	return func(name string) string {
		b, err := os.ReadFile(filepath.Join(result, "inventory.json"))
		if err != nil {
			return ""
		}
		var inv inventory.Inventory
		if json.Unmarshal(b, &inv) != nil {
			return ""
		}
		for _, p := range inv.Projects {
			if p.Name != name || p.WorkingDir == "" {
				continue
			}
			if fi, err := os.Stat(p.WorkingDir); err == nil && fi.IsDir() {
				return p.WorkingDir
			}
		}
		return ""
	}
}

// projectOfPath sagt, zu welchem Projekt eine Datei gehört: das Projekt,
// in dessen Ordner sie liegt (laut letzter Bestandsaufnahme).
func projectOfPath(result string) func(string) string {
	return func(p string) string {
		b, err := os.ReadFile(filepath.Join(result, "inventory.json"))
		if err != nil {
			return ""
		}
		var inv inventory.Inventory
		if json.Unmarshal(b, &inv) != nil {
			return ""
		}
		best, name := "", ""
		for _, pr := range inv.Projects {
			dir := filepath.Clean(pr.WorkingDir)
			if pr.WorkingDir != "" && len(dir) > len(best) && strings.HasPrefix(p, dir+string(filepath.Separator)) {
				best, name = dir, pr.Name
			}
		}
		return name
	}
}

// domainReady wartet nach dem Anlegen oder Anschalten einer Domain, bis
// Caddy sie mit gültigem Zertifikat ausliefert (beim ersten Mal holt Caddy
// es eben).
func domainReady(ctx context.Context, host string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	last := ""
	for {
		ci := domains.LocalCert(ctx, host)
		if ci.Err == "" && !ci.Until.IsZero() {
			return nil
		}
		last = ci.Err
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s bekam in zwei Minuten kein gültiges Zertifikat (%s)", host, last)
		case <-time.After(3 * time.Second):
		}
	}
}

// pullableFrom fragt die Registry, ob sie ein Image herausgibt. Unbekannt
// oder nur mit Anmeldung (ohne passende Anmeldung in der Docker-Konfiguration
// von root) heißt für das Aufräumen: nicht neu ladbar.
func pullableFrom(reg *aktualisieren.Registry) func(context.Context, string) error {
	return func(ctx context.Context, ref string) error {
		r, err := aktualisieren.ParseRef(ref)
		if err != nil {
			return err
		}
		if _, err := reg.Digest(ctx, r); err != nil {
			if errors.Is(err, aktualisieren.ErrNoAccess) || errors.Is(err, aktualisieren.ErrNoSuchImage) {
				return aufraeumen.ErrNotPullable
			}
			return err
		}
		return nil
	}
}

// moveStopped liest, welche Container der letzte Lauf eines Umzugs aus
// gelassen hat (Einträge mit final in der Stopp-Liste des Backups).
func moveStopped() map[string]bool {
	dir, err := stateDir()
	if err != nil {
		return nil
	}
	st, err := backup.LoadStopState(backup.StopStatePath(dir))
	if err != nil {
		return nil
	}
	var out map[string]bool
	for _, e := range st.Entries() {
		if e.Final {
			if out == nil {
				out = map[string]bool{}
			}
			out[e.Name] = true
		}
	}
	return out
}
