package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/caddy"
	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/dnscheck"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/outdir"
	"github.com/officialtaxz2/hostdeck/internal/pc"
	"github.com/officialtaxz2/hostdeck/internal/remote"
	"github.com/officialtaxz2/hostdeck/internal/restore"
)

// serverMoves ist der Probe-Umzug aus dem Leitstand: Dieser Server
// verbindet sich per SSH mit dem neuen, bringt HostDeck dorthin und lässt es
// das Backup direkt aus /var/lib/hostdeck/backup lesen. Dafür öffnet die
// SSH-Verbindung auf dem neuen Server einen Port nur für 127.0.0.1, hinter
// dem dieser Server das Backup nur zum Lesen anbietet, mit einem Schlüssel
// nur für diesen Lauf. Das Passwort des neuen Servers lebt nur im Speicher.
type serverMoves struct {
	Stored       func(ctx context.Context) dienst.StoredBackups
	Repo         string // Backup-Ordner, den der neue Server liest
	PasswordFile string // Backup-Passwort
	Dir          string // eigener Ordner: bekannte Server, letztes Ziel, Ausgaben
	// Exe liefert die eigene Programmdatei; nil: os.Executable.
	Exe func() (string, error)
	// Dial baut die TCP-Verbindung; nil: net.Dialer. Für Tests.
	Dial func(ctx context.Context, addr string) (net.Conn, error)
	// Heartbeat: so oft geht ein Lebenszeichen an den Agenten.
	Heartbeat time.Duration
	// MachineID liefert die Rechner-Kennung dieses Servers; nil: aus
	// /etc/machine-id.
	MachineID func() string
	// StartWait: so lange darf HostDeck auf dem neuen Server bis zur ersten
	// Antwort und nach dem Hochladen bis zum Start brauchen; 0: 2 Minuten.
	StartWait time.Duration
	// KeepAlive: so oft fragt HostDeck, ob die SSH-Verbindung noch steht; 0: 30 s.
	KeepAlive time.Duration
	// Sites liefert die Domains, die Caddy hier bedient; nach dem
	// Probe-Umzug prüft HostDeck jede über die neue Adresse. nil: keine Prüfung.
	Sites func(ctx context.Context) ([]string, error)
	// Own ist die Domain des Leitstands (leer: keine).
	Own func() string
	// SiteHere und SiteThere fragen eine Seite hier und auf dem neuen
	// Server ab; nil: über Caddy. Für Tests.
	SiteHere, SiteThere caddy.Prober
	SiteRetry           time.Duration
	// DNSResolver und LocalIPs für die DNS-Liste; nil: das System.
	DNSResolver *dnscheck.Resolver
	LocalIPs    func() []net.IP
	// DNSNames liefert Ziele der MX- und SRV-Einträge einer Zone für die
	// Vorschläge der DNS-Liste; nil: über das System.
	DNSNames func(ctx context.Context, zone string) []string
	// Pack packt die Daten des Leitstands; nach dem Probe-Umzug richtet
	// HostDeck damit den Leitstand auf dem neuen Server ein (2c). nil: nicht.
	Pack func() ([]byte, []string, error)
	// Inventory liefert die Bestandsaufnahme dieses Servers; für die
	// Server-Ebene (2d). nil: keine Server-Ebene.
	Inventory func() (*inventory.Inventory, error)
	// Root ist die Wurzel für Tests; leer: /.
	Root string
	// Final ist der letzte Lauf am Umzugstag (Projekte bleiben aus); nil:
	// kein Umzugstag.
	Final func(ctx context.Context, in backup.Input, out io.Writer) int
	// LeftOff zählt die Container, die ein letzter Lauf hier aus gelassen
	// hat; Restart startet sie wieder (Rückweg) und nennt, was danach
	// nicht läuft.
	LeftOff func() int
	Restart func(ctx context.Context, w io.Writer) []string
	Now     func() time.Time

	sent atomic.Int64 // Bytes aus dem Backup an den neuen Server, je Lauf
}

// counted zählt, was eine Antwort an Bytes schreibt.
type counted struct {
	http.ResponseWriter
	n *atomic.Int64
}

func (c counted) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	c.n.Add(int64(n))
	return n, err
}

// moveTakeover ist der zweite Lauf nach dem Probe-Umzug: den Leitstand
// übernehmen. Nur intern; der Leitstand startet ihn nicht einzeln.
const moveTakeover dienst.MoveKind = "hostdeck-uebernahme"

const (
	knownFile = "bekannte-server" // „host:port SHA256:… schlüsselart“ je Zeile
	lastFile  = "zuletzt.json"
)

func (m *serverMoves) State(ctx context.Context) dienst.MoveState {
	st := m.Stored(ctx)
	ms := dienst.MoveState{Ready: st.Ready && st.Runs > 0, Backup: st.Last, Last: m.last()}
	switch {
	case !st.Ready:
		ms.Problem = st.Problem
	case st.Runs == 0:
		ms.Problem = "Auf diesem Server liegt noch kein gültiges Backup. Erstelle zuerst eins auf der Seite Backup."
	}
	return ms
}

// BackupPasswordOK vergleicht pw in fester Zeit mit dem Backup-Passwort.
func (m *serverMoves) BackupPasswordOK(pw string) (bool, error) {
	b, err := os.ReadFile(m.PasswordFile)
	if err != nil {
		return false, fmt.Errorf("Backup-Passwort nicht lesbar: %v", err)
	}
	want := sha256.Sum256([]byte(strings.TrimSpace(string(b))))
	got := sha256.Sum256([]byte(strings.TrimSpace(pw)))
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1, nil
}

func (m *serverMoves) last() *dienst.MoveTarget {
	b, err := os.ReadFile(filepath.Join(m.Dir, lastFile))
	if err != nil {
		return nil
	}
	var t dienst.MoveTarget
	if json.Unmarshal(b, &t) != nil || t.Host == "" {
		return nil
	}
	t.Password = ""
	return &t
}

func (m *serverMoves) saveLast(t dienst.MoveTarget) {
	t.Password = ""
	b, _ := json.Marshal(t)
	_ = os.MkdirAll(m.Dir, 0o700)
	_ = outdir.WritePrivate(filepath.Join(m.Dir, lastFile), b)
}

// logTo schreibt die Ausgabe zusätzlich in eine Datei, die letzten 30.
func (m *serverMoves) logTo(out io.Writer, kind dienst.MoveKind) (io.Writer, func()) {
	dir := filepath.Join(m.Dir, "laeufe")
	if os.MkdirAll(dir, 0o700) != nil {
		return out, func() {}
	}
	name := "umzug-" + string(kind)
	if kind == dienst.MoveRun {
		name = "umzug"
	}
	f, err := os.OpenFile(filepath.Join(dir, time.Now().Format("20060102-150405")+"-"+name+".log"), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return out, func() {}
	}
	pruneLogs(dir, 30)
	return io.MultiWriter(out, f), func() { f.Close() }
}

func (m *serverMoves) Run(ctx context.Context, t dienst.MoveTarget, kind dienst.MoveKind, in backup.Input, out io.Writer) int {
	w, closeLog := m.logTo(out, kind)
	defer closeLog()
	switch kind {
	case dienst.MoveDay:
		return m.runDay(ctx, t, in, w)
	case dienst.MoveBack:
		return m.runBack(ctx, in, w)
	}
	title := map[dienst.MoveKind]string{dienst.MovePlan: "Probelauf Umzug nach", dienst.MoveRun: "Probe-Umzug nach",
		dienst.MoveHalt: "Probe-Kopie anhalten auf", dienst.MoveRemove: "Probe-Kopie entfernen auf"}[kind]
	if title == "" {
		fmt.Fprintf(w, "Unbekannte Art %q\n", kind)
		return restore.ExitBlocked
	}
	fmt.Fprintf(w, "HostDeck %s: %s %s\n\nVerbindung\n", version, title, t)
	var pw []byte
	var run string
	if kind == dienst.MovePlan || kind == dienst.MoveRun {
		if st := m.State(ctx); !st.Ready {
			fmt.Fprintf(w, "  ✗ %s\n", st.Problem)
			return restore.ExitBlocked
		}
		var err error
		pw, err = os.ReadFile(m.PasswordFile)
		if err != nil {
			fmt.Fprintf(w, "  ✗ Backup-Passwort nicht lesbar: %v\n", err)
			return restore.ExitBlocked
		}
		run = m.Stored(ctx).LastRun
	}
	client, err := m.connect(ctx, t, in, w)
	if err != nil {
		if ctx.Err() == nil {
			fmt.Fprintf(w, "  ✗ %v\n", err)
		} else {
			fmt.Fprintln(w, "  Abgebrochen.")
		}
		return restore.ExitBlocked
	}
	defer client.Close()
	// Steht die Leitung nicht mehr, merkt das sonst niemand: TCP meldet eine
	// tote Verbindung erst nach vielen Minuten.
	alive, stopAlive := context.WithCancel(ctx)
	defer stopAlive()
	go m.keepAlive(alive, client)
	m.saveLast(t)
	watch := &lineWatch{w: w, needle: []byte(restore.CaddyRunningLine)}
	code, err := m.agent(ctx, client, kind, strings.TrimSpace(string(pw)), run, nil, in, watch)
	switch {
	case err != nil && ctx.Err() != nil:
		fmt.Fprintln(w, "  Abgebrochen.")
		return code
	case err != nil:
		fmt.Fprintf(w, "  ✗ %v\n", err)
		return code
	case kind != dienst.MoveRun || code == restore.ExitBlocked:
		return code
	}
	fmt.Fprintf(w, "\nÜbertragen aus dem Backup: %s\n", sizeText(m.sent.Load()))
	tested, copied := m.takeover(ctx, client, t, false, in, w)
	if ctx.Err() != nil {
		return code
	}
	sitesGood, sitesTotal := 0, -1
	if watch.seen {
		sitesGood, sitesTotal = m.checkSites(ctx, t, nil, w)
	}
	if tested != "" {
		m.checkTestDomain(ctx, t, tested, w)
	}
	if ctx.Err() == nil {
		m.serverLevel(ctx, client, false, in, w)
	}
	ip, _ := m.targetIP(ctx, t)
	if ctx.Err() == nil {
		m.oldAddress(ctx, ip, w)
	}
	if ctx.Err() == nil {
		m.recordProbe(ctx, t, ip, run, code, watch.errs, copied, sitesGood, sitesTotal, w)
	}
	return code
}

// takeover richtet nach dem Probe-Umzug den Leitstand auf dem neuen
// Server ein und nennt seine Testadresse (leer: keine).
// ok: Der Leitstand ist drüben eingerichtet (oder es gibt hier keinen).
// move: am Umzugstag.
func (m *serverMoves) takeover(ctx context.Context, client *ssh.Client, t dienst.MoveTarget, move bool, in backup.Input, w io.Writer) (test string, ok bool) {
	if m.Pack == nil {
		return "", true
	}
	fmt.Fprintf(w, "\nLeitstand auf den neuen Server\n")
	pkg, names, err := m.Pack()
	if err != nil {
		fmt.Fprintf(w, "  ✗ Daten des Leitstands packen: %v\n", err)
		return "", false
	}
	fmt.Fprintf(w, "  Datenpaket: %d Dateien, %s\n", len(names), sizeText(int64(len(pkg))))
	host, _ := os.Hostname()
	cfg := &remote.Config{Action: remote.ActionTakeover, Package: pkg, Probe: true, From: host, Move: move}
	if own := m.ownDomain(); own != "" {
		cfg.TestDomain = m.testDomainFor(ctx, own, t, w)
	} else {
		fmt.Fprintf(w, "  Der Leitstand hier ist nicht von außen freigeschaltet; die Kopie öffnest du dort über den Notausgang.\n")
	}
	fmt.Fprintln(w)
	code, err := m.agent(ctx, client, moveTakeover, "", "", cfg, in, w)
	if err != nil {
		if ctx.Err() != nil {
			fmt.Fprintln(w, "  Abgebrochen.")
		} else {
			fmt.Fprintf(w, "  ✗ %v\n", err)
		}
		return "", false
	}
	return cfg.TestDomain, code == restore.ExitOK
}

func (m *serverMoves) ownDomain() string {
	if m.Own == nil {
		return ""
	}
	return m.Own()
}

// testDomainFor nimmt die Testadresse (hostdeck-neu.…), wenn sie beim
// zuständigen Nameserver auf den neuen Server zeigt.
func (m *serverMoves) testDomainFor(ctx context.Context, own string, t dienst.MoveTarget, w io.Writer) string {
	test := testDomain(own)
	if test == "" {
		return ""
	}
	ip, err := m.targetIP(ctx, t)
	if err != nil {
		fmt.Fprintf(w, "  ! %v\n", err)
		return ""
	}
	res := m.DNSResolver
	if res == nil {
		res = &dnscheck.Resolver{}
	}
	a := res.Lookup(ctx, test)
	var seen []string
	for _, r := range a.Records {
		if r.IP.String() == ip {
			fmt.Fprintf(w, "  ✓ Testadresse %s zeigt auf den neuen Server (%s)\n", test, ip)
			return test
		}
		seen = append(seen, r.IP.String())
	}
	switch {
	case len(seen) > 0:
		fmt.Fprintf(w, "  ! Testadresse %s zeigt auf %s, nicht auf den neuen Server (%s).\n", test, strings.Join(seen, ", "), ip)
	case a.Err != "":
		fmt.Fprintf(w, "  ! Testadresse %s: %s.\n", test, a.Err)
	default:
		fmt.Fprintf(w, "  ! Testadresse %s hat keinen Eintrag.\n", test)
	}
	fmt.Fprintf(w, "    Lege beim DNS-Anbieter einen A-Eintrag %s → %s an; bis dahin öffnest du die Kopie dort über den Notausgang.\n", test, ip)
	return ""
}

// targetIP ist die Adresse des neuen Servers.
func (m *serverMoves) targetIP(ctx context.Context, t dienst.MoveTarget) (string, error) {
	if ip := net.ParseIP(t.Host); ip != nil {
		return ip.String(), nil
	}
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	addrs, err := net.DefaultResolver.LookupIPAddr(rctx, t.Host)
	cancel()
	if err != nil || len(addrs) == 0 {
		return "", fmt.Errorf("die Adresse von %s ist nicht auflösbar (%v)", t.Host, err)
	}
	return addrs[0].IP.String(), nil
}

// keepAlive fragt regelmäßig über SSH, ob der neue Server noch antwortet.
// Bleibt die Antwort aus, trennt sie die Verbindung; dann enden alle Lese-
// und Schreibversuche mit einem Fehler statt zu hängen.
func (m *serverMoves) keepAlive(ctx context.Context, client *ssh.Client) {
	every := m.KeepAlive
	if every == 0 {
		every = 30 * time.Second
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		reply := make(chan error, 1)
		go func() {
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			reply <- err
		}()
		select {
		case <-ctx.Done():
			return
		case err := <-reply:
			if err != nil {
				return
			}
		case <-time.After(3 * every):
			client.Close()
			return
		}
	}
}

// errKeySeen beendet die erste Verbindung, sobald der Schlüssel des
// Servers da ist.
var errKeySeen = errors.New("schlüssel gesehen")

func (m *serverMoves) dial(ctx context.Context, addr string) (net.Conn, error) {
	if m.Dial != nil {
		return m.Dial(ctx, addr)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var d net.Dialer
	return d.DialContext(ctx, "tcp", addr)
}

// handshake baut eine SSH-Verbindung auf. Die Frist gilt nur bis zur
// Anmeldung.
func (m *serverMoves) handshake(ctx context.Context, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	conn, err := m.dial(ctx, addr)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	stop()
	if err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(c, chans, reqs), nil
}

// connect meldet sich am neuen Server an. Den Schlüssel des Servers lässt
// sie beim ersten Mal bestätigen und merkt ihn sich; meldet sich der
// Server später mit einem anderen, warnt sie deutlich.
func (m *serverMoves) connect(ctx context.Context, t dienst.MoveTarget, in backup.Input, w io.Writer) (*ssh.Client, error) {
	addr := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	old, oldType := m.knownKey(addr)
	var key ssh.PublicKey
	// Ist die Schlüsselart bekannt, fragt HostDeck genau nach ihr. Sonst könnte
	// ein Update von HostDeck eine andere Art bevorzugen, und derselbe Server
	// erschiene als „anderer Schlüssel“.
	_, err := m.handshake(ctx, addr, &ssh.ClientConfig{
		User:              t.User,
		HostKeyAlgorithms: hostKeyAlgos(oldType),
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			key = k
			return errKeySeen
		},
	})
	if key == nil && oldType != "" && ctx.Err() == nil {
		// Der Server bietet die gemerkte Art nicht mehr an: dann zeigt der
		// Vergleich unten den neuen Schlüssel.
		_, err = m.handshake(ctx, addr, &ssh.ClientConfig{
			User: t.User,
			HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
				key = k
				return errKeySeen
			},
		})
	}
	if key == nil {
		return nil, fmt.Errorf("%s ist nicht erreichbar (%v). Stimmen Adresse und Port, und läuft dort SSH?", addr, plainNetErr(err))
	}
	fp, typ := ssh.FingerprintSHA256(key), key.Type()
	switch {
	case old == fp:
		fmt.Fprintf(w, "  ✓ %s ist bekannt, Schlüssel unverändert (%s)\n", addr, fp)
		if oldType == "" {
			if err := m.rememberKey(addr, fp, typ); err != nil {
				return nil, err
			}
		}
	case old != "":
		fmt.Fprintf(w, "  ! %s meldet sich mit einem anderen Schlüssel als beim letzten Mal.\n    vorher: %s\n    jetzt:  %s\n", addr, old, fp)
		ans, err := in.Ask(ctx, backup.Question{
			Text: "Neuen Schlüssel annehmen? Tippe neu installiert:",
			Note: "Achtung: " + addr + " meldet sich mit einem anderen Schlüssel als beim letzten Mal (jetzt " + fp + "). Das ist normal, wenn du den Server neu installiert hast. Sonst könnte sich ein anderer Rechner als dein Server ausgeben: dann abbrechen.",
			Choices: []backup.Choice{
				{Label: "Server wurde neu installiert", Answer: "neu installiert"},
				{Label: "Abbrechen", Answer: "nein"},
			},
		})
		if err != nil || strings.TrimSpace(ans) != "neu installiert" {
			return nil, errors.New("abgebrochen: der Schlüssel des Servers ist nicht bestätigt")
		}
		if err := m.rememberKey(addr, fp, typ); err != nil {
			return nil, err
		}
	default:
		fmt.Fprintf(w, "  %s meldet sich zum ersten Mal, Schlüssel %s\n", addr, fp)
		ans, err := in.Ask(ctx, backup.Question{
			Text: "Ist das dein neuer Server? Tippe ja:",
			Note: "Erste Verbindung zu " + addr + ". Der Server meldet sich mit dem Schlüssel " + fp + ". Zeigt dein Anbieter den Fingerabdruck an, vergleiche ihn; sonst reicht es, dass Adresse und Server stimmen. HostDeck merkt sich den Schlüssel und warnt, falls er sich später ändert.",
			Choices: []backup.Choice{
				{Label: "Ja, das ist mein neuer Server", Answer: "ja"},
				{Label: "Abbrechen", Answer: "nein"},
			},
		})
		if err != nil || !strings.EqualFold(strings.TrimSpace(ans), "ja") {
			return nil, errors.New("abgebrochen: der Schlüssel des Servers ist nicht bestätigt")
		}
		if err := m.rememberKey(addr, fp, typ); err != nil {
			return nil, err
		}
	}
	pass := t.Password
	client, err := m.handshake(ctx, addr, &ssh.ClientConfig{
		User:              t.User,
		HostKeyAlgorithms: hostKeyAlgos(typ),
		Auth: []ssh.AuthMethod{
			ssh.Password(pass),
			ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
				a := make([]string, len(qs))
				for i := range a {
					a[i] = pass
				}
				return a, nil
			}),
		},
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			if ssh.FingerprintSHA256(k) != fp {
				return errors.New("der Server meldet sich jetzt mit einem anderen Schlüssel")
			}
			return nil
		},
	})
	if err != nil {
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, fmt.Errorf("Anmeldung als %s abgelehnt. Stimmt das Passwort? Manche Anbieter erlauben root nur mit Schlüssel; dann in /etc/ssh/sshd_config „PermitRootLogin yes“ und „PasswordAuthentication yes“ setzen und SSH neu starten", t.User)
		}
		return nil, fmt.Errorf("Anmeldung an %s: %v", addr, plainNetErr(err))
	}
	fmt.Fprintf(w, "  ✓ angemeldet als %s\n", t.User)
	return client, nil
}

func plainNetErr(err error) error {
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		return op.Err
	}
	return err
}

// hostKeyAlgos sind die Signaturverfahren zu einer Schlüsselart; nil:
// alle, die x/crypto/ssh kennt.
func hostKeyAlgos(typ string) []string {
	switch typ {
	case "":
		return nil
	case ssh.KeyAlgoRSA:
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
	default:
		return []string{typ}
	}
}

// knownKey liefert Fingerabdruck und Schlüsselart, die HostDeck sich für addr
// gemerkt hat. Ältere Zeilen haben keine Art.
func (m *serverMoves) knownKey(addr string) (fp, typ string) {
	b, err := os.ReadFile(filepath.Join(m.Dir, knownFile))
	if err != nil {
		return "", ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || f[0] != addr {
			continue
		}
		fp, typ = f[1], ""
		if len(f) > 2 {
			typ = f[2]
		}
	}
	return fp, typ
}

func (m *serverMoves) rememberKey(addr, fp, typ string) error {
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(m.Dir, knownFile)
	b, _ := os.ReadFile(path)
	var keep []string
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] != addr {
			keep = append(keep, l)
		}
	}
	keep = append(keep, strings.TrimSpace(addr+" "+fp+" "+typ))
	return outdir.WritePrivate(path, []byte(strings.Join(keep, "\n")+"\n"))
}

// gatewayPorts fragt sshd auf dem neuen Server nach GatewayPorts (no,
// yes, clientspecified); "" heißt unbekannt. Schweigt der Server, trennt
// die Startfrist des Aufrufers die Verbindung.
func gatewayPorts(client *ssh.Client) string {
	sess, err := client.NewSession()
	if err != nil {
		return ""
	}
	defer sess.Close()
	out, _ := sess.Output("sshd -T 2>/dev/null || /usr/sbin/sshd -T 2>/dev/null")
	for _, l := range strings.Split(string(out), "\n") {
		if f := strings.Fields(l); len(f) == 2 && strings.EqualFold(f[0], "gatewayports") {
			return strings.ToLower(f[1])
		}
	}
	return ""
}

func (m *serverMoves) exe() ([]byte, error) {
	get := m.Exe
	if get == nil {
		get = os.Executable
	}
	p, err := get()
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// agent startet HostDeck auf dem neuen Server und führt den Lauf. run ist der
// Lauf, den der neue Server zurückspielt (der neueste gültige).
// Ist job gesetzt, schickt agent diese Anweisung statt der zur Art.
func (m *serverMoves) agent(ctx context.Context, client *ssh.Client, kind dienst.MoveKind, password, run string, job *remote.Config, in backup.Input, w io.Writer) (int, error) {
	// Bis das Gespräch steht, beendet Abbrechen die Verbindung; danach
	// bittet converse den neuen Server um einen geordneten Abbruch.
	cancelled := context.AfterFunc(ctx, func() { client.Close() })
	defer cancelled()
	// Schweigt der neue Server beim Start, trennt eine Frist die Verbindung.
	wait := m.StartWait
	if wait == 0 {
		wait = 2 * time.Minute
	}
	var late sync.Mutex
	lateErr := error(nil)
	deadline := func(what string) *time.Timer {
		return time.AfterFunc(wait, func() {
			late.Lock()
			lateErr = fmt.Errorf("HostDeck auf dem neuen Server %s nicht innerhalb von %s; die Verbindung ist getrennt", what, wait)
			late.Unlock()
			client.Close()
		})
	}
	why := func(err error) error {
		late.Lock()
		defer late.Unlock()
		if lateErr != nil {
			return lateErr
		}
		return err
	}

	// Nur das Zurückspielen liest das Backup; Anhalten und Entfernen
	// brauchen keinen Zugang dazu.
	var port int
	var key, nonce string
	if kind == dienst.MovePlan || kind == dienst.MoveRun {
		var stop func()
		var err error
		port, key, nonce, stop, err = m.serveBackup(client, deadline, why, w)
		if err != nil {
			return restore.ExitBlocked, err
		}
		defer stop()
	}

	sess, err := client.NewSession()
	if err != nil {
		return restore.ExitBlocked, err
	}
	defer sess.Close()
	stdin, err := sess.StdinPipe()
	if err != nil {
		return restore.ExitBlocked, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return restore.ExitBlocked, err
	}
	sess.Stderr = &prefixWriter{w: w, prefix: "  neuer Server: "}
	timer := deadline("antwortet")
	if err := sess.Start(remote.BootstrapCommand()); err != nil {
		timer.Stop()
		return restore.ExitBlocked, why(err)
	}
	br := bufio.NewReader(stdout)
	line, err := br.ReadString('\n')
	timer.Stop()
	if err != nil {
		return restore.ExitBlocked, why(fmt.Errorf("HostDeck auf dem neuen Server meldet sich nicht (%v)", err))
	}
	hello, err := remote.ParseHello(strings.TrimSpace(line))
	if err != nil {
		return restore.ExitBlocked, err
	}
	ip := client.RemoteAddr().String()
	if h, _, err := net.SplitHostPort(ip); err == nil {
		ip = h
	}
	if job == nil { // weitere Gespräche im selben Lauf nennen ihn nicht noch einmal
		fmt.Fprintf(w, "  ✓ neuer Server: %s, Adresse %s (%s)\n", hello.Hostname, ip, hello.Arch)
	}
	if hello.MachineID != "" && hello.MachineID == m.machineID() {
		return restore.ExitBlocked, fmt.Errorf("%s ist dieser Server selbst (gleiche Rechner-Kennung in /etc/machine-id). Der Probe-Umzug geht auf einen anderen Server", ip)
	}
	var uploaded []byte
	if hello.Version != version {
		if hello.Arch != runtime.GOARCH {
			return restore.ExitBlocked, fmt.Errorf("der neue Server hat einen %s-Prozessor, dieser einen %s-Prozessor; HostDeck kann sich nur auf gleiche Prozessoren selbst bringen", hello.Arch, runtime.GOARCH)
		}
		bin, err := m.exe()
		if err != nil {
			return restore.ExitBlocked, fmt.Errorf("eigene Programmdatei: %v", err)
		}
		if !bytes.HasPrefix(bin, []byte("\x7fELF")) {
			return restore.ExitBlocked, errors.New("die eigene Programmdatei ist kein Linux-Programm")
		}
		fmt.Fprintf(w, "  Kopiere HostDeck %s auf den neuen Server (dort bisher: %s) …\n", version, hello.Version)
		if _, err := fmt.Fprintf(stdin, "BIN %d\n", len(bin)); err != nil {
			return restore.ExitBlocked, why(err)
		}
		if _, err := stdin.Write(bin); err != nil {
			return restore.ExitBlocked, why(err)
		}
		uploaded = bin
	} else if _, err := io.WriteString(stdin, "WEITER 0\n"); err != nil {
		return restore.ExitBlocked, why(err)
	}
	timer = deadline("startet")
	line, err = br.ReadString('\n')
	timer.Stop()
	if err != nil {
		if ctx.Err() != nil {
			return restore.ExitBlocked, ctx.Err()
		}
		return restore.ExitBlocked, why(errors.New("HostDeck auf dem neuen Server ließ sich nicht als root starten. Melde dich als root an oder richte sudo ohne Passwort ein"))
	}
	f := strings.Fields(line)
	if len(f) != 3 || f[0] != "HOSTDECK-AGENT" || f[1] != version {
		return restore.ExitBlocked, fmt.Errorf("unerwartete Antwort von HostDeck auf dem neuen Server: %q", strings.TrimSpace(line))
	}
	if uploaded != nil {
		sum := sha256.Sum256(uploaded)
		if f[2] != hex.EncodeToString(sum[:]) {
			return restore.ExitBlocked, errors.New("die Programmdatei kam auf dem neuen Server verändert an")
		}
	}
	host, _ := os.Hostname()
	cfg := remote.Config{V: 1, Leitstand: true}
	switch {
	case job != nil:
		cfg = *job
		cfg.V, cfg.Leitstand = 1, true
	case kind == dienst.MoveHalt:
		cfg.Action = remote.ActionProbeHalt
	case kind == dienst.MoveRemove:
		cfg.Action = remote.ActionProbeRemove
	default:
		cfg = remote.Config{
			V: 1, Action: remote.ActionRestore, Plan: kind == dienst.MovePlan, Port: port, User: "hostdeck", Key: key, Nonce: nonce,
			Where: host + " (" + m.Repo + ")", Leitstand: true, Password: password, Run: run,
		}
	}
	b, _ := json.Marshal(cfg)
	if _, err := stdin.Write(append(b, '\n')); err != nil {
		return restore.ExitBlocked, why(err)
	}
	if job == nil {
		fmt.Fprintln(w)
	}
	if !cancelled() {
		return restore.ExitBlocked, ctx.Err()
	}
	return m.converse(ctx, br, stdin, in, w)
}

// serveBackup bietet das Backup über die SSH-Verbindung nur zum Lesen an,
// auf dem neuen Server an 127.0.0.1 und einem Port, den der Server wählt.
func (m *serverMoves) serveBackup(client *ssh.Client, deadline func(string) *time.Timer, why func(error) error, w io.Writer) (port int, key, nonce string, stop func(), err error) {
	// Mit „GatewayPorts yes“ legt sshd die Weiterleitung auf alle
	// Adressen, egal was HostDeck erbittet (N4). Das sagt HostDeck vor dem Start.
	gpTimer := deadline("antwortet")
	gp := gatewayPorts(client)
	gpTimer.Stop()
	if gp == "yes" {
		fmt.Fprintln(w, "  ! Der neue Server hat in sshd „GatewayPorts yes“. Der Lese-Zugang zum Backup ist während dieses Laufs darum auch von außen erreichbar: geschützt durch einen Zufallsschlüssel, nur lesend, verschlüsselt. Sicherer ist „GatewayPorts no“ in /etc/ssh/sshd_config des neuen Servers.")
	}
	// Auf dem neuen Server nur für 127.0.0.1; Port wählt der Server.
	ln, err := client.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if late := why(nil); late != nil {
			return 0, "", "", nil, late
		}
		return 0, "", "", nil, fmt.Errorf("der neue Server erlaubt keine Weiterleitung über SSH (%v); in /etc/ssh/sshd_config „AllowTcpForwarding yes“ setzen", err)
	}
	port = ln.Addr().(*net.TCPAddr).Port
	key, err = pc.NewSecret(24)
	if err != nil {
		ln.Close()
		return 0, "", "", nil, err
	}
	nonce, err = pc.NewSecret(16)
	if err != nil {
		ln.Close()
		return 0, "", "", nil, err
	}
	rc, err := pc.NewReceiver(m.Repo, "", "hostdeck", key, nonce)
	if err != nil {
		ln.Close()
		return 0, "", "", nil, err
	}
	rc.ReadOnly = true
	m.sent.Store(0)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { rc.ServeHTTP(counted{w, &m.sent}, r) })
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	go srv.Serve(ln)
	return port, key, nonce, func() { srv.Close(); ln.Close() }, nil
}

// machineID ist die Rechner-Kennung dieses Servers, leer wenn unbekannt.
func (m *serverMoves) machineID() string {
	if m.MachineID != nil {
		return m.MachineID()
	}
	b, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// converse zeigt die Ausgabe des neuen Servers, stellt seine Fragen im
// Leitstand und hält die Verbindung mit Lebenszeichen am Leben. Endet ctx
// (Abbrechen), bittet sie den neuen Server um einen geordneten Abbruch und
// wartet darauf.
func (m *serverMoves) converse(ctx context.Context, br *bufio.Reader, stdin io.WriteCloser, in backup.Input, w io.Writer) (int, error) {
	var wmu sync.Mutex
	send := func(line string) {
		wmu.Lock()
		defer wmu.Unlock()
		_, _ = io.WriteString(stdin, line+"\n")
	}
	every := m.Heartbeat
	if every == 0 {
		every = remote.HeartbeatEvery
	}
	beat := time.NewTicker(every)
	defer beat.Stop()
	lines := make(chan string)
	go func() {
		defer close(lines)
		for {
			l, err := br.ReadString('\n')
			if l != "" {
				lines <- strings.TrimRight(l, "\r\n")
			}
			if err != nil {
				return
			}
		}
	}()
	infos := map[int]remote.AskInfo{}
	asks := map[int]context.CancelFunc{}
	defer func() {
		for _, c := range asks {
			c()
		}
	}()
	code := -1
	done := ctx.Done()
	var grace <-chan time.Time
	send(remote.Heartbeat)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				stdin.Close()
				if code >= 0 {
					return code, nil
				}
				if ctx.Err() != nil {
					return restore.ExitFailed, errors.New("abgebrochen")
				}
				return restore.ExitFailed, errors.New("die Verbindung zum neuen Server ist abgerissen")
			}
			c, isCtl := remote.ParseControl(l)
			if !isCtl {
				fmt.Fprintln(w, l)
				continue
			}
			switch c.Kind {
			case "frage-info":
				infos[c.ID] = c.Info
			case "frage":
				if c.Secret {
					// Geheimes geht nicht in den Browser.
					send(remote.Answer(c.ID, ""))
					continue
				}
				info := infos[c.ID]
				delete(infos, c.ID)
				qctx, cancel := context.WithCancel(ctx)
				asks[c.ID] = cancel
				id := c.ID
				q := backup.Question{Text: c.Text, Choices: info.Choices, Note: info.Note}
				go func() {
					a, err := in.Ask(qctx, q)
					if err == nil {
						send(remote.Answer(id, a))
					}
				}()
			case "frage-ende":
				if cancel := asks[c.ID]; cancel != nil {
					cancel()
					delete(asks, c.ID)
				}
			case "ende":
				code = c.Code
			}
		case <-beat.C:
			send(remote.Heartbeat)
		case <-done:
			done = nil
			fmt.Fprintln(w, "\nAbbruch angefordert. Der neue Server hört auf und startet, was er gestoppt hat, wieder …")
			send("abbruch")
			grace = time.After(5 * time.Minute)
		case <-grace:
			stdin.Close()
			return restore.ExitFailed, errors.New("der neue Server hat nach dem Abbruch nicht geantwortet; die Verbindung ist getrennt")
		}
	}
}
