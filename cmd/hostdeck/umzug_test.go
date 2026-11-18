package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/remote"
	"github.com/officialtaxz2/hostdeck/internal/restore"
)

// fakeTarget ist ein kleiner SSH-Server, der den neuen Server spielt: Er
// nimmt ein Passwort an, erlaubt eine Weiterleitung zurück und spielt
// Startskript und Agent. Der Agent prüft den Weg zum Backup und stellt
// eine Frage.
type fakeTarget struct {
	t         *testing.T
	addr      string
	key       ssh.Signer
	password  string
	version   string // HostDeck-Version, die der Server schon hat
	machineID string
	gateway   string // GatewayPorts laut sshd -T
	record    string // wiederherstellung.json drüben (leer: keine)

	mu       sync.Mutex
	cfg      remote.Config
	takeover *remote.Config // Anweisung der Übernahme, falls eine kam
	server   *remote.Config // Server-Ebene, falls sie kam
	actions  []string       // Aktionen in der Reihenfolge, in der sie kamen
	restored string         // zurückgespielter Lauf
	failOn   string         // diese Aktion endet mit Code 1
	upload   int
	answer   string
	readOK   bool
	writeErr int // Status beim Versuch zu schreiben
	forwards int // erbetene Weiterleitungen
	err      error
}

func newFakeTarget(t *testing.T) *fakeTarget {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f := &fakeTarget{t: t, addr: ln.Addr().String(), key: signer, password: "richtig", version: "keins"}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeTarget) fail(err error) {
	f.mu.Lock()
	if f.err == nil {
		f.err = err
	}
	f.mu.Unlock()
}

func (f *fakeTarget) serve(c net.Conn) {
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(m ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if m.User() == "root" && string(p) == f.password {
				return nil, nil
			}
			return nil, fmt.Errorf("falsch")
		},
	}
	cfg.AddHostKey(f.key)
	sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	defer sc.Close()
	var fwd net.Listener
	go func() {
		for r := range reqs {
			if r.Type != "tcpip-forward" {
				r.Reply(false, nil)
				continue
			}
			f.mu.Lock()
			f.forwards++
			f.mu.Unlock()
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				r.Reply(false, nil)
				continue
			}
			fwd = ln
			port := uint32(ln.Addr().(*net.TCPAddr).Port)
			b := make([]byte, 4)
			binary.BigEndian.PutUint32(b, port)
			r.Reply(true, b)
			go func() {
				for {
					lc, err := ln.Accept()
					if err != nil {
						return
					}
					payload := ssh.Marshal(struct {
						Addr       string
						Port       uint32
						OriginAddr string
						OriginPort uint32
					}{"127.0.0.1", port, "127.0.0.1", 1})
					ch, creqs, err := sc.OpenChannel("forwarded-tcpip", payload)
					if err != nil {
						lc.Close()
						continue
					}
					go ssh.DiscardRequests(creqs)
					go func() { io.Copy(ch, lc); ch.CloseWrite() }()
					go func() { io.Copy(lc, ch); lc.Close() }()
				}
			}()
		}
	}()
	defer func() {
		if fwd != nil {
			fwd.Close()
		}
	}()
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "nein")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			return
		}
		go func() {
			for r := range creqs {
				r.Reply(r.Type == "exec", nil)
				if r.Type != "exec" {
					continue
				}
				var cmd struct{ Command string }
				_ = ssh.Unmarshal(r.Payload, &cmd)
				if strings.Contains(cmd.Command, "wiederherstellung.json") {
					f.mu.Lock()
					fmt.Fprint(ch, f.record)
					f.mu.Unlock()
					ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
					ch.Close()
					continue
				}
				if strings.HasPrefix(cmd.Command, "sshd -T") {
					gp := f.gateway
					if gp == "" {
						gp = "no"
					}
					fmt.Fprintf(ch, "port 22\ngatewayports %s\n", gp)
					ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
					ch.Close()
					continue
				}
				go f.agent(ch)
			}
		}()
	}
}

// agent spielt Startskript und Agent auf dem neuen Server.
func (f *fakeTarget) agent(ch ssh.Channel) {
	defer ch.Close()
	br := bufio.NewReader(ch)
	mid := f.machineID
	if mid == "" {
		mid = "-"
	}
	fmt.Fprintf(ch, "HOSTDECK-HALLO 1 x86_64 neuer-server %s %s\n", f.version, mid)
	line, _ := br.ReadString('\n')
	var bin []byte
	if n := 0; strings.HasPrefix(line, "BIN ") {
		fmt.Sscan(strings.TrimPrefix(line, "BIN "), &n)
		bin = make([]byte, n)
		if _, err := io.ReadFull(br, bin); err != nil {
			f.fail(err)
			return
		}
	}
	sum := sha256.Sum256(bin)
	f.mu.Lock()
	f.upload = len(bin)
	f.mu.Unlock()
	fmt.Fprintf(ch, "HOSTDECK-AGENT %s %s\n", version, hex.EncodeToString(sum[:]))
	line, err := br.ReadString('\n')
	if err != nil {
		f.fail(err)
		return
	}
	var cfg remote.Config
	if err := json.Unmarshal([]byte(line), &cfg); err != nil {
		f.fail(err)
		return
	}
	f.mu.Lock()
	f.actions = append(f.actions, cfg.Action)
	failOn := f.failOn
	if cfg.Action == remote.ActionRestore {
		f.restored = cfg.Run
	}
	f.mu.Unlock()
	if cfg.Action == failOn {
		fmt.Fprintln(ch, "  ✗ ging schief")
		fmt.Fprintln(ch, remote.End(1))
		return
	}
	if cfg.Action == remote.ActionProbeRemove && cfg.Confirmed {
		fmt.Fprintln(ch, "  ✓ Probe-Kopie entfernt")
		fmt.Fprintln(ch, remote.End(0))
		return
	}
	if cfg.Action == remote.ActionServer {
		f.mu.Lock()
		f.server = &cfg
		f.mu.Unlock()
		fmt.Fprintln(ch, "  · Zeitzone: hier Etc/UTC, am Umzugstag stellt HostDeck Europe/Berlin ein")
		fmt.Fprintln(ch, remote.End(0))
		return
	}
	if cfg.Action == remote.ActionTakeover {
		f.mu.Lock()
		f.takeover = &cfg
		f.mu.Unlock()
		fmt.Fprintln(ch, "Leitstand")
		fmt.Fprintln(ch, "  ✓ HostDeck läuft hier als Dienst")
		fmt.Fprintln(ch, remote.End(0))
		return
	}
	f.mu.Lock()
	f.cfg = cfg
	f.mu.Unlock()
	if cfg.Action != remote.ActionRestore {
		q := backup.Question{Text: "Tippe anhalten:", Choices: []backup.Choice{{Label: "Anhalten", Answer: "anhalten"}}}
		fmt.Fprintln(ch, remote.AskInfoLine(3, q))
		fmt.Fprintln(ch, remote.Ask(3, false, q.Text))
		for {
			l, err := br.ReadString('\n')
			if err != nil {
				f.fail(err)
				return
			}
			if a, ok := strings.CutPrefix(strings.TrimSpace(l), "antwort 3 "); ok {
				f.mu.Lock()
				f.answer = a
				f.mu.Unlock()
				break
			}
		}
		fmt.Fprintln(ch, "  ✓ app angehalten")
		fmt.Fprintln(ch, remote.End(0))
		return
	}
	// Der Weg zurück zum Backup: lesen geht, schreiben nicht.
	get := func(method, path string) (int, string) {
		req, _ := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", cfg.Port, path), strings.NewReader("x"))
		req.SetBasicAuth(cfg.User, cfg.Key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			f.fail(err)
			return 0, ""
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	code, body := get(http.MethodGet, "/.hostdeck/hallo")
	cfgCode, _ := get(http.MethodGet, "/config")
	putCode, _ := get(http.MethodPost, "/locks/abc")
	f.mu.Lock()
	f.readOK = code == 200 && body == cfg.Nonce && cfgCode == 200
	f.writeErr = putCode
	f.mu.Unlock()
	fmt.Fprintln(ch, "Vorprüfung")
	fmt.Fprintln(ch, "  ✓ Docker 29 mit Compose 2")
	q := backup.Question{Text: "Jetzt zurückspielen? Tippe ja:", Note: "Jetzt zurückspielen?",
		Choices: []backup.Choice{{Label: "Jetzt zurückspielen", Answer: "ja"}, {Label: "Nicht starten", Answer: "nein"}}}
	fmt.Fprintln(ch, remote.AskInfoLine(7, q))
	fmt.Fprintln(ch, remote.Ask(7, false, q.Text))
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			f.fail(fmt.Errorf("keine Antwort: %v", err))
			return
		}
		l = strings.TrimSpace(l)
		if l == "abbruch" {
			fmt.Fprintln(ch, "Abgebrochen, gestoppte Container laufen wieder.")
			fmt.Fprintln(ch, remote.End(1))
			return
		}
		if a, ok := strings.CutPrefix(l, "antwort 7 "); ok {
			f.mu.Lock()
			f.answer = a
			f.mu.Unlock()
			break
		}
	}
	fmt.Fprintln(ch, "Fertig: alles zurückgespielt.")
	fmt.Fprintln(ch, remote.End(0))
}

// autoInput beantwortet jede Frage mit dem ersten Knopf.
type autoInput struct {
	mu    sync.Mutex
	asked []backup.Question
	pick  func(q backup.Question) string
}

func (a *autoInput) Ask(_ context.Context, q backup.Question) (string, error) {
	a.mu.Lock()
	a.asked = append(a.asked, q)
	a.mu.Unlock()
	if a.pick != nil {
		return a.pick(q), nil
	}
	if len(q.Choices) == 0 {
		return "", nil
	}
	return q.Choices[0].Answer, nil
}

func moveRig(t *testing.T) (*serverMoves, *fakeTarget, dienst.MoveTarget) {
	t.Helper()
	dir := t.TempDir()
	repo := filepath.Join(dir, "backup")
	os.MkdirAll(filepath.Join(repo, "keys"), 0o700)
	os.WriteFile(filepath.Join(repo, "config"), []byte("restic-config"), 0o600)
	pw := filepath.Join(dir, "passwort")
	os.WriteFile(pw, []byte("backup-geheim\n"), 0o600)
	exe := filepath.Join(dir, "hostdeck")
	os.WriteFile(exe, []byte("\x7fELF-hostdeck-programm"), 0o700)
	last := time.Date(2026, 11, 1, 20, 56, 0, 0, time.UTC)
	m := &serverMoves{
		Stored: func(context.Context) dienst.StoredBackups {
			return dienst.StoredBackups{Ready: true, Runs: 1, Last: &last, LastRun: "20261101-205600"}
		},
		Repo:         repo,
		PasswordFile: pw,
		Dir:          filepath.Join(dir, "umzug"),
		Exe:          func() (string, error) { return exe, nil },
		Heartbeat:    20 * time.Millisecond,
	}
	f := newFakeTarget(t)
	host, port, _ := net.SplitHostPort(f.addr)
	var p int
	fmt.Sscan(port, &p)
	return m, f, dienst.MoveTarget{Host: host, Port: p, User: "root", Password: "richtig"}
}

func TestProbeUmzugVerbindung(t *testing.T) {
	m, f, target := moveRig(t)
	in := &autoInput{}
	var out strings.Builder
	code := m.Run(context.Background(), target, dienst.MoveRun, in, &out)
	if f.err != nil {
		t.Fatalf("neuer Server: %v\n%s", f.err, out.String())
	}
	if code != 0 {
		t.Fatalf("Code %d\n%s", code, out.String())
	}
	if !f.readOK || f.writeErr != http.StatusForbidden {
		t.Fatalf("Weg zum Backup: lesen %v, schreiben %d", f.readOK, f.writeErr)
	}
	if f.upload != len("\x7fELF-hostdeck-programm") {
		t.Fatalf("Programm nicht übertragen: %d Byte", f.upload)
	}
	if f.cfg.Password != "backup-geheim" || !f.cfg.Leitstand || f.cfg.Action != remote.ActionRestore || f.cfg.Plan || f.cfg.Run != "20261101-205600" {
		t.Fatalf("Anweisung: %+v", f.cfg)
	}
	if f.answer != "ja" {
		t.Fatalf("Antwort %q", f.answer)
	}
	if len(in.asked) != 2 || !strings.Contains(in.asked[0].Note, "Erste Verbindung") || in.asked[1].Note != "Jetzt zurückspielen?" || len(in.asked[1].Choices) != 2 {
		t.Fatalf("Fragen: %+v", in.asked)
	}
	for _, secret := range []string{"richtig", "backup-geheim"} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("Ausgabe enthält ein Passwort:\n%s", out.String())
		}
	}
	for _, want := range []string{"zum ersten Mal", "✓ angemeldet als root", "neuer-server", "Kopiere HostDeck", "Fertig: alles zurückgespielt."} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("Ausgabe ohne %q:\n%s", want, out.String())
		}
	}
	// Letztes Ziel ohne Passwort gemerkt.
	if l := m.last(); l == nil || l.Host != target.Host || l.Password != "" {
		t.Fatalf("zuletzt: %+v", l)
	}
	b, _ := os.ReadFile(filepath.Join(m.Dir, lastFile))
	if strings.Contains(string(b), "richtig") {
		t.Fatal("Passwort des neuen Servers gespeichert")
	}

	// Zweites Mal: Schlüssel bekannt, keine Frage dazu.
	in2 := &autoInput{}
	out.Reset()
	if code := m.Run(context.Background(), target, dienst.MoveRun, in2, &out); code != 0 {
		t.Fatalf("zweites Mal: %d\n%s", code, out.String())
	}
	if len(in2.asked) != 1 || !strings.Contains(out.String(), "Schlüssel unverändert") {
		t.Fatalf("zweites Mal fragte %d-mal:\n%s", len(in2.asked), out.String())
	}
	addr := fmt.Sprintf("%s:%d", target.Host, target.Port)
	if _, typ := m.knownKey(addr); typ != ssh.KeyAlgoED25519 {
		t.Fatalf("Schlüsselart nicht gemerkt: %q", typ)
	}
	if strings.Contains(out.String(), "GatewayPorts") {
		t.Fatalf("Hinweis ohne Grund:\n%s", out.String())
	}
	// N4: Mit GatewayPorts yes sagt HostDeck, dass der Zugang von außen erreichbar ist.
	f.gateway = "yes"
	out.Reset()
	if code := m.Run(context.Background(), target, dienst.MoveRun, &autoInput{}, &out); code != 0 {
		t.Fatalf("mit GatewayPorts: %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "„GatewayPorts yes“") {
		t.Fatalf("kein Hinweis auf GatewayPorts:\n%s", out.String())
	}
}

// Ältere Zeilen ohne Schlüsselart bekommen sie beim nächsten Lauf dazu.
func TestProbeUmzugSchluesselartNachgetragen(t *testing.T) {
	m, f, target := moveRig(t)
	addr := fmt.Sprintf("%s:%d", target.Host, target.Port)
	if err := m.rememberKey(addr, ssh.FingerprintSHA256(f.key.PublicKey()), ""); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if code := m.Run(context.Background(), target, dienst.MovePlan, &autoInput{}, &out); code != 0 {
		t.Fatalf("Code %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Schlüssel unverändert") {
		t.Fatalf("Ausgabe:\n%s", out.String())
	}
	if _, typ := m.knownKey(addr); typ != ssh.KeyAlgoED25519 {
		t.Fatalf("Schlüsselart nicht nachgetragen: %q", typ)
	}
}

// Ist dieselbe Rechner-Kennung am anderen Ende, ist das dieser Server.
func TestProbeUmzugAufSichSelbst(t *testing.T) {
	m, f, target := moveRig(t)
	f.machineID = "0123456789abcdef0123456789abcdef"
	m.MachineID = func() string { return "0123456789abcdef0123456789abcdef" }
	var out strings.Builder
	if code := m.Run(context.Background(), target, dienst.MovePlan, &autoInput{}, &out); code != restore.ExitBlocked {
		t.Fatalf("Code %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "ist dieser Server selbst") {
		t.Fatalf("Ausgabe:\n%s", out.String())
	}
}

// Schweigt der neue Server nach der Anmeldung, trennt die Frist.
func TestProbeUmzugStartFrist(t *testing.T) {
	m, _, _ := moveRig(t)
	m.StartWait = 300 * time.Millisecond
	host, port := stummerServer(t)
	var out strings.Builder
	done := make(chan int, 1)
	go func() {
		done <- m.Run(context.Background(), dienst.MoveTarget{Host: host, Port: port, User: "root", Password: "x"}, dienst.MovePlan, &autoInput{}, &out)
	}()
	select {
	case code := <-done:
		if code != restore.ExitBlocked || !strings.Contains(out.String(), "nicht innerhalb von 300ms") {
			t.Fatalf("Code %d\n%s", code, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Frist greift nicht")
	}
}

func TestProbeUmzugAndererSchluessel(t *testing.T) {
	m, _, target := moveRig(t)
	addr := fmt.Sprintf("%s:%d", target.Host, target.Port)
	if err := m.rememberKey(addr, "SHA256:alterSchluessel", ""); err != nil {
		t.Fatal(err)
	}
	in := &autoInput{pick: func(q backup.Question) string { return "nein" }}
	var out strings.Builder
	if code := m.Run(context.Background(), target, dienst.MoveRun, in, &out); code != restore.ExitBlocked {
		t.Fatalf("Code %d\n%s", code, out.String())
	}
	if len(in.asked) != 1 || !strings.Contains(in.asked[0].Note, "anderen Schlüssel") || !strings.Contains(out.String(), "nicht bestätigt") {
		t.Fatalf("Fragen %+v\n%s", in.asked, out.String())
	}
	if fp, _ := m.knownKey(addr); fp != "SHA256:alterSchluessel" {
		t.Fatal("abgelehnter Schlüssel trotzdem gemerkt")
	}
}

func TestProbeUmzugFalschesPasswort(t *testing.T) {
	m, _, target := moveRig(t)
	target.Password = "falsch"
	var out strings.Builder
	if code := m.Run(context.Background(), target, dienst.MovePlan, &autoInput{}, &out); code != restore.ExitBlocked {
		t.Fatalf("Code %d\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Anmeldung als root abgelehnt") || strings.Contains(out.String(), "falsch") && !strings.Contains(out.String(), "abgelehnt") {
		t.Fatalf("Ausgabe:\n%s", out.String())
	}
}

func TestProbeUmzugNichtErreichbar(t *testing.T) {
	m, _, target := moveRig(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	target.Port = port
	var out strings.Builder
	if code := m.Run(context.Background(), target, dienst.MovePlan, &autoInput{}, &out); code != restore.ExitBlocked || !strings.Contains(out.String(), "nicht erreichbar") {
		t.Fatalf("Code %d\n%s", code, out.String())
	}
}

func TestProbeUmzugOhneBackup(t *testing.T) {
	m, _, target := moveRig(t)
	m.Stored = func(context.Context) dienst.StoredBackups { return dienst.StoredBackups{Ready: true} }
	var out strings.Builder
	if code := m.Run(context.Background(), target, dienst.MovePlan, &autoInput{}, &out); code != restore.ExitBlocked || !strings.Contains(out.String(), "noch kein gültiges Backup") {
		t.Fatalf("Code %d\n%s", code, out.String())
	}
	if st := m.State(context.Background()); st.Ready {
		t.Fatalf("Stand: %+v", st)
	}
}

func TestProbeUmzugAbbrechen(t *testing.T) {
	m, f, target := moveRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := &blockInput{asked: make(chan backup.Question, 4)}
	var out syncBuilder
	done := make(chan int)
	go func() { done <- m.Run(ctx, target, dienst.MoveRun, in, &out) }()
	// Erste Frage: Schlüssel; dann die Frage des neuen Servers.
	<-in.asked
	in.reply <- "ja"
	<-in.asked
	cancel()
	select {
	case code := <-done:
		if code != 1 || !strings.Contains(out.String(), "Abbruch angefordert") || !strings.Contains(out.String(), "Container laufen wieder") {
			t.Fatalf("Code %d\n%s", code, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("Abbrechen hängt\n%s", out.String())
	}
	if f.answer != "" {
		t.Fatalf("Antwort trotz Abbruch: %q", f.answer)
	}
}

// blockInput wartet auf Antworten aus dem Test.
type blockInput struct {
	asked chan backup.Question
	reply chan string
}

func (b *blockInput) Ask(ctx context.Context, q backup.Question) (string, error) {
	if b.reply == nil {
		b.reply = make(chan string)
	}
	b.asked <- q
	select {
	case a := <-b.reply:
		return a, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

type syncBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuilder) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuilder) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestProbeUmzugBackupPasswortVergleich(t *testing.T) {
	m, _, _ := moveRig(t)
	for pw, want := range map[string]bool{"backup-geheim": true, " backup-geheim\n": true, "backup-geheim2": false, "": false} {
		if ok, err := m.BackupPasswordOK(pw); err != nil || ok != want {
			t.Errorf("%q: %v %v", pw, ok, err)
		}
	}
}

// Anhalten und Entfernen der Probe-Kopie brauchen weder ein Backup hier
// noch das Backup-Passwort, und sie öffnen keinen Weg zum Backup.
func TestProbeKopieAnhaltenUeberSSH(t *testing.T) {
	for _, k := range []dienst.MoveKind{dienst.MoveHalt, dienst.MoveRemove} {
		m, f, target := moveRig(t)
		m.Stored = func(context.Context) dienst.StoredBackups { return dienst.StoredBackups{} }
		m.PasswordFile = filepath.Join(t.TempDir(), "fehlt")
		var out strings.Builder
		code := m.Run(context.Background(), target, k, &autoInput{}, &out)
		if f.err != nil || code != 0 {
			t.Fatalf("%s: Code %d, %v\n%s", k, code, f.err, out.String())
		}
		f.mu.Lock()
		cfg, fwd, ans := f.cfg, f.forwards, f.answer
		f.mu.Unlock()
		if cfg.Action != string(k) || cfg.Password != "" || cfg.Port != 0 || cfg.Key != "" || !cfg.Leitstand {
			t.Errorf("%s: Konfiguration %+v", k, cfg)
		}
		if fwd != 0 {
			t.Errorf("%s: %d Weiterleitungen zum Backup", k, fwd)
		}
		if ans != "anhalten" || !strings.Contains(out.String(), "app angehalten") {
			t.Errorf("%s: Antwort %q\n%s", k, ans, out.String())
		}
	}
}
