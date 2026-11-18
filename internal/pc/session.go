package pc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/remote"
)

// Console fragt den Benutzer am PC.
type Console interface {
	// Lines liefert jede eingegebene Zeile. Der Kanal wird beim ersten
	// Aufruf angelegt und liest ab dann dauerhaft.
	Lines() <-chan string
	// Secret liest eine Eingabe, ohne sie anzuzeigen. Es geht nur, solange
	// Lines noch nicht läuft.
	Secret() (string, error)
}

// Session ist eine Verbindung vom PC zum Server.
type Session struct {
	Server string // etwa root@203.0.113.10
	Port   int    // Tunnel-Port auf dem Server
	Folder string // Backup-Ordner auf dem PC; leer bei --plan
	// Repo ist beim Zurückspielen der Unterordner mit dem Backup; beim
	// Backup nimmt HostDeck den Rechnernamen des Servers.
	Repo    string
	SSH     string // Pfad zu ssh, Standard „ssh“
	BinDir  string // Ordner mit hostdeck-linux-amd64 und hostdeck-linux-arm64
	Version string
	Out     io.Writer
	Err     io.Writer
	Console Console
	// Interrupt liefert Strg+C. Beim ersten bittet HostDeck den Server um einen
	// geordneten Abbruch, beim zweiten trennt es die Verbindung.
	Interrupt <-chan os.Signal
	// ExtraSSH sind zusätzliche Optionen für ssh, etwa für Tests.
	ExtraSSH []string
}

// RepoDir ist das Repository für einen Server im Backup-Ordner.
func RepoDir(folder, hostname string) string { return filepath.Join(folder, safeName(hostname)) }

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "server"
	}
	return b.String()
}

// lazyHandler leitet an den Empfänger weiter, sobald er feststeht.
type lazyHandler struct{ h atomic.Value }

func (l *lazyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h, _ := l.h.Load().(http.Handler)
	if h == nil {
		http.Error(w, "noch nicht bereit", http.StatusServiceUnavailable)
		return
	}
	h.ServeHTTP(w, r)
}

// ErrConnect heißt: ssh kam nicht bis zum Server durch.
var ErrConnect = errors.New("Verbindung zum Server fehlgeschlagen")

// Run baut die Verbindung auf, startet den Agenten auf dem Server und gibt
// seinen Exit-Code zurück.
func (s *Session) Run(ctx context.Context, cfg remote.Config) (int, error) {
	cfg.V = 1
	restoring := cfg.Action == remote.ActionRestore
	needTunnel := (cfg.Action == remote.ActionBackup && !cfg.Plan) || restoring
	var lh lazyHandler
	var srv *http.Server
	var localPort int
	if needTunnel {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return 1, fmt.Errorf("Empfänger auf dem PC: %w", err)
		}
		localPort = ln.Addr().(*net.TCPAddr).Port
		if s.Err != nil {
			log.SetFlags(0)
			log.SetOutput(quietLog{s.Err})
		}
		srv = &http.Server{Handler: &lh, ReadHeaderTimeout: 30 * time.Second}
		go srv.Serve(ln)
		defer srv.Close()
		key, err := NewSecret(24)
		if err != nil {
			return 1, err
		}
		nonce, err := NewSecret(16)
		if err != nil {
			return 1, err
		}
		cfg.User, cfg.Key, cfg.Nonce = "hostdeck", key, nonce
		if cfg.Port == 0 {
			cfg.Port = remote.DefaultPort
		}
	}

	sshBin := s.SSH
	if sshBin == "" {
		sshBin = "ssh"
	}
	args := []string{"-T", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=4"}
	if needTunnel {
		args = append(args, "-o", "ExitOnForwardFailure=yes",
			"-R", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", cfg.Port, localPort))
	}
	args = append(args, s.ExtraSSH...)
	args = append(args, s.Server, remote.BootstrapCommand())
	cmd := exec.Command(sshBin, args...)
	setProcessGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 1, err
	}
	// Eigene Pipe statt StdoutPipe: cmd.Wait schließt jene, sobald ssh
	// endet, auch wenn die letzten Zeilen („@@hostdeck:ende“) noch ungelesen
	// darin liegen. Dann hieß es „Verbindung getrennt“ statt des echten
	// Ergebnisses. Diese Pipe liest HostDeck bis zum Ende leer.
	stdout, pw, err := os.Pipe()
	if err != nil {
		return 1, err
	}
	defer stdout.Close()
	cmd.Stdout = pw
	cmd.Stderr = s.Err
	err = cmd.Start()
	pw.Close()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return 1, errors.New("ssh fehlt auf diesem PC. Unter Windows: Einstellungen > Apps > Optionale Features > OpenSSH-Client hinzufügen")
		}
		return 1, fmt.Errorf("ssh starten: %w", err)
	}
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	fail := func(err error) (int, error) {
		stdin.Close()
		_ = cmd.Process.Kill()
		<-waitErr
		return 1, err
	}

	br := bufio.NewReader(stdout)
	line, err := br.ReadString('\n')
	if err != nil {
		stdin.Close()
		werr := <-waitErr
		return 1, fmt.Errorf("%w (%v); Adresse, Benutzer und Passwort oder Schlüssel prüfen", ErrConnect, firstErr(werr, err))
	}
	hello, err := remote.ParseHello(strings.TrimSpace(line))
	if err != nil {
		return fail(err)
	}
	var uploaded []byte
	if hello.Version != s.Version {
		bin, err := s.linuxBinary(hello.Arch)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(s.Out, "Kopiere HostDeck %s auf den Server (dort bisher: %s) …\n", s.Version, hello.Version)
		if _, err := fmt.Fprintf(stdin, "BIN %d\n", len(bin)); err != nil {
			return fail(err)
		}
		if _, err := stdin.Write(bin); err != nil {
			return fail(err)
		}
		uploaded = bin
	} else if _, err := io.WriteString(stdin, "WEITER 0\n"); err != nil {
		return fail(err)
	}

	line, err = br.ReadString('\n')
	if err != nil {
		stdin.Close()
		werr := <-waitErr
		if strings.Contains(fmt.Sprint(werr), "exit status 1") {
			return 1, errors.New("HostDeck auf dem Server ließ sich nicht als root starten. Melde dich als root an oder richte sudo ohne Passwort ein")
		}
		return 1, fmt.Errorf("HostDeck auf dem Server startet nicht (%v)", firstErr(werr, err))
	}
	f := strings.Fields(line)
	if len(f) != 3 || f[0] != "HOSTDECK-AGENT" {
		return fail(fmt.Errorf("unerwartete Antwort von HostDeck auf dem Server: %q", strings.TrimSpace(line)))
	}
	if f[1] != s.Version {
		return fail(fmt.Errorf("auf dem Server läuft HostDeck %s statt %s", f[1], s.Version))
	}
	if uploaded != nil {
		sum := sha256.Sum256(uploaded)
		if f[2] != hex.EncodeToString(sum[:]) {
			return fail(errors.New("die Programmdatei kam auf dem Server verändert an"))
		}
	}

	if needTunnel {
		if err := os.MkdirAll(s.Folder, 0o700); err != nil {
			return fail(fmt.Errorf("Backup-Ordner %s: %w", s.Folder, err))
		}
		dir := RepoDir(s.Folder, hello.Hostname)
		if restoring {
			if s.Repo == "" {
				return fail(errors.New("kein Backup zum Zurückspielen gewählt"))
			}
			dir = filepath.Join(s.Folder, s.Repo)
		}
		rc, err := NewReceiver(dir, filepath.Join(s.Folder, "berichte"), cfg.User, cfg.Key, cfg.Nonce)
		if err != nil {
			return fail(err)
		}
		rc.ReadOnly = restoring
		lh.h.Store(http.Handler(rc))
		if cfg.Where == "" {
			cfg.Where = dir + " auf deinem PC"
		}
	}
	b, _ := json.Marshal(cfg)
	if _, err := stdin.Write(append(b, '\n')); err != nil {
		return fail(err)
	}
	return s.converse(ctx, br, stdin, cmd, waitErr)
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// converse zeigt die Ausgabe des Servers an und beantwortet Fragen.
func (s *Session) converse(ctx context.Context, br *bufio.Reader, stdin io.WriteCloser, cmd *exec.Cmd, waitErr <-chan error) (int, error) {
	var wmu sync.Mutex
	send := func(line string) {
		wmu.Lock()
		defer wmu.Unlock()
		io.WriteString(stdin, line+"\n")
	}
	// Herzschlag: Der Server bricht ab, wenn er ausbleibt, und der PC
	// bleibt wach, solange der Lauf geht.
	beat := time.NewTicker(remote.HeartbeatEvery)
	beatDone := make(chan struct{})
	defer func() {
		beat.Stop()
		close(beatDone)
	}()
	go func() {
		for {
			send(remote.Heartbeat)
			keepAwake()
			select {
			case <-beat.C:
			case <-beatDone:
				return
			}
		}
	}()
	code := -1
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

	var current int // offene Frage
	var answers <-chan string
	interrupts := 0
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				stdin.Close()
				err := <-waitErr
				if code >= 0 {
					return code, nil
				}
				return 1, fmt.Errorf("Verbindung zum Server getrennt (%v)", err)
			}
			c, isCtl := remote.ParseControl(l)
			if !isCtl {
				fmt.Fprintln(s.Out, l)
				continue
			}
			switch c.Kind {
			case "frage":
				current = c.ID
				fmt.Fprintf(s.Out, "%s ", c.Text)
				if c.Secret && answers == nil {
					pw, err := s.Console.Secret()
					fmt.Fprintln(s.Out)
					if err == nil {
						send(remote.Answer(c.ID, pw))
					}
					current = 0
					continue
				}
				if answers == nil {
					answers = s.Console.Lines()
				}
			case "frage-info":
				// Nur für den Leitstand; im Terminal reicht der Text.
			case "frage-ende":
				if current == c.ID {
					current = 0
					fmt.Fprintln(s.Out)
				}
			case "ende":
				code = c.Code
			}
		case a, ok := <-answers:
			if !ok {
				answers = nil
				continue
			}
			if current != 0 {
				send(remote.Answer(current, a))
				current = 0
			}
		case <-s.Interrupt:
			interrupts++
			if interrupts == 1 {
				fmt.Fprintln(s.Out, "\nAbbruch angefordert. Der Server startet gestoppte Container wieder, bitte warten …")
				fmt.Fprintln(s.Out, "(Noch einmal Strg+C trennt sofort; dann startet der Server sie trotzdem wieder, sieht aber niemand zu.)")
				send("abbruch")
			} else {
				_ = cmd.Process.Kill()
			}
		case <-ctx.Done():
			send("abbruch")
			select {
			case <-waitErr:
			case <-time.After(5 * time.Minute):
				_ = cmd.Process.Kill()
			}
			return 1, ctx.Err()
		}
	}
}

// linuxBinary liest hostdeck-linux-<arch> aus dem Programmordner und prüft es
// gegen SHA256SUMS, falls die Datei daneben liegt.
func (s *Session) linuxBinary(arch string) ([]byte, error) {
	name := "hostdeck-linux-" + arch
	path := filepath.Join(s.BinDir, name)
	bin, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s fehlt neben hostdeck.exe (%v); lege die Datei aus dem Download daneben", name, err)
	}
	if sums, err := os.ReadFile(filepath.Join(s.BinDir, "SHA256SUMS")); err == nil {
		sum := sha256.Sum256(bin)
		want := ""
		for _, l := range strings.Split(string(sums), "\n") {
			f := strings.Fields(l)
			if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
				want = f[0]
			}
		}
		if want != "" && want != hex.EncodeToString(sum[:]) {
			return nil, fmt.Errorf("%s passt nicht zu SHA256SUMS; bitte neu herunterladen", name)
		}
	}
	if !bytes.HasPrefix(bin, []byte("\x7fELF")) {
		return nil, fmt.Errorf("%s ist kein Linux-Programm", name)
	}
	return bin, nil
}
