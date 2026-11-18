// Package remote ist das kleine Protokoll zwischen HostDeck auf dem PC und HostDeck
// auf dem Server. Beide sprechen über die Standardein- und -ausgabe einer
// SSH-Verbindung miteinander.
//
// Ablauf:
//
//  1. Der PC startet per ssh das Startskript (Bootstrap). Es meldet sich mit
//     „HOSTDECK-HALLO 1 <arch> <rechnername> <version>“.
//  2. Der PC antwortet „WEITER 0“ oder „BIN <bytes>“ mit der passenden
//     Linux-Datei von HostDeck direkt dahinter. Dann startet das Skript
//     „hostdeck agent“, als root.
//  3. Der Agent meldet sich mit „HOSTDECK-AGENT <version> <sha256>“, der PC
//     schickt eine Zeile JSON (Config).
//  4. Danach ist die Ausgabe des Agenten normale Ausgabe für den Benutzer,
//     außer Steuerzeilen mit Prefix. Antworten des Benutzers schickt der PC
//     als „antwort <id> <text>“; „abbruch“ bricht den Lauf ab. Alle
//     HeartbeatEvery schickt der PC „puls“. Bleibt das nach dem ersten Mal
//     HeartbeatTimeout lang aus (PC im Standby, Leitung weg), bricht der
//     Agent ab, als käme „abbruch“.
package remote

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/serverebene"
)

// Herzschlag vom PC: Ohne ihn merkt der Server einen eingeschlafenen PC
// erst, wenn TCP aufgibt, und gestoppte Container blieben so lange aus.
const Heartbeat = "puls"

var (
	HeartbeatEvery   = 10 * time.Second
	HeartbeatTimeout = 60 * time.Second
)

// DefaultPort ist der Tunnel-Port auf dem Server. 8000 wäre falsch: den
// belegt auf vps1 Calagopus.
const DefaultPort = 18800

// Prefix leitet Steuerzeilen des Agenten ein.
const Prefix = "@@hostdeck:"

// Config ist die erste Zeile, die der PC dem Agenten schickt.
type Config struct {
	V      int    `json:"v"`
	Action string `json:"action"` // siehe Action*
	Plan   bool   `json:"plan,omitempty"`
	Final  bool   `json:"final,omitempty"`
	Port   int    `json:"port,omitempty"` // Tunnel-Port auf dem Server
	User   string `json:"user,omitempty"` // Zugang zum Empfänger auf dem PC
	Key    string `json:"key,omitempty"`
	Nonce  string `json:"nonce,omitempty"` // beweist, dass am Tunnel der PC antwortet
	Out    string `json:"out,omitempty"`   // Ergebnisordner auf dem Server, leer: suchen
	Where  string `json:"where,omitempty"` // Ziel, lesbar
	// Leitstand: Der Lauf kommt aus dem Leitstand eines anderen Servers
	// (Probe-Umzug). Fragen tragen dann Knöpfe, und fehlendes Docker darf
	// HostDeck nach Rückfrage installieren.
	Leitstand bool `json:"leitstand,omitempty"`
	// Password ist das Backup-Passwort, beim Probe-Umzug vom alten Server.
	// Der Agent legt es nur in die Passwortdatei und gibt es nie aus.
	Password string `json:"passwort,omitempty"`
	// Run ist der Lauf, den der Agent zurückspielt (beim Probe-Umzug der
	// neueste gültige). Leer: Der Agent fragt.
	Run string `json:"lauf,omitempty"`
	// Für ActionTakeover: Paket ist das Datenpaket des Leitstands (tar.gz),
	// Probe sagt, dass der neue Leitstand eine Probe-Kopie ist, Testadresse
	// ist seine zweite Adresse (leer: keine), Von der alte Server.
	Package    []byte `json:"paket,omitempty"`
	Probe      bool   `json:"probe,omitempty"`
	TestDomain string `json:"testadresse,omitempty"`
	From       string `json:"von,omitempty"`
	// Move: Der Leitstand kommt am Umzugstag. Er bleibt eine
	// stille Probe-Kopie, bis die Domains hierher zeigen; dann übernimmt er.
	Move bool `json:"umzug,omitempty"`
	// Confirmed: Am Umzugstag ist das Entfernen der Probe-Kopie schon im
	// Leitstand bestätigt; ohne Kopie gibt es dann nichts zu tun.
	Confirmed bool `json:"bestaetigt,omitempty"`
	// Für ActionServer: die Punkte der Server-Ebene. Ohne
	// Apply nur prüfen; dann stehen keine Inhalte darin.
	Server *serverebene.Level `json:"server_ebene,omitempty"`
}

// Aktionen.
const (
	ActionBackup  = "backup"
	ActionStart   = "dienste-starten"
	ActionRestore = "restore"
	ActionVerify  = "verify"
	// Probe-Kopie auf dem neuen Server anhalten oder entfernen (Leitstand).
	ActionProbeHalt   = "probe-anhalten"
	ActionProbeRemove = "probe-entfernen"
	// Leitstand übernehmen: HostDeck als Dienst einrichten und die Daten des
	// alten Leitstands einspielen.
	ActionTakeover = "hostdeck-uebernahme"
	// Server-Ebene: Zeitzone, Kernel, Aufgaben und Zugänge prüfen oder
	// übernehmen.
	ActionServer = "server-ebene"
)

// bootstrap läuft auf dem Server in sh. Es legt ~/.hostdeck an, meldet Version
// und Rechnerart, nimmt bei Bedarf die neue Programmdatei entgegen und
// startet den Agenten als root. read liest aus einer Pipe Byte für Byte,
// head -c genau die angekündigte Länge; der Rest der Eingabe bleibt für den
// Agenten.
const bootstrap = `set -u
d="$HOME/.hostdeck/bin"
mkdir -p "$d" && chmod 700 "$HOME/.hostdeck" "$d" || exit 90
b="$d/hostdeck"
v=$("$b" version 2>/dev/null | head -n 1 | cut -d' ' -f2)
h=$(cat /proc/sys/kernel/hostname 2>/dev/null || hostname)
m=$(cat /etc/machine-id 2>/dev/null | tr -cd '0-9a-f')
printf 'HOSTDECK-HALLO 1 %s %s %s %s\n' "$(uname -m)" "$h" "${v:-keins}" "${m:--}"
IFS=' ' read -r cmd n || exit 91
case "$cmd" in
  BIN) head -c "$n" > "$b.neu" && chmod 700 "$b.neu" && mv -f "$b.neu" "$b" || exit 92 ;;
  WEITER) ;;
  *) exit 93 ;;
esac
if [ "$(id -u)" = 0 ]; then exec "$b" agent; fi
exec sudo -n "$b" agent
`

// BootstrapCommand ist der Befehl, den ssh auf dem Server ausführt. Das
// Skript reist base64-kodiert, damit keine Shell dazwischen Anführungszeichen
// oder Sonderzeichen verändert.
func BootstrapCommand() string {
	enc := base64.StdEncoding.EncodeToString([]byte(bootstrap))
	return `sh -c "$(echo ` + enc + ` | base64 -d)"`
}

// Hello ist die erste Zeile des Startskripts.
type Hello struct {
	Arch     string // etwa amd64
	Hostname string
	Version  string // „keins“, wenn HostDeck noch fehlt
	// MachineID ist /etc/machine-id des Servers, leer wenn unbekannt. Daran
	// erkennt HostDeck, ob das Ziel dieser Server selbst ist.
	MachineID string
}

// ParseHello liest „HOSTDECK-HALLO 1 <uname -m> <rechnername> <version>
// [<machine-id>|-]“.
func ParseHello(line string) (Hello, error) {
	f := strings.Fields(line)
	if (len(f) != 5 && len(f) != 6) || f[0] != "HOSTDECK-HALLO" || f[1] != "1" {
		return Hello{}, fmt.Errorf("unerwartete Antwort vom Server: %q", line)
	}
	h := Hello{Hostname: f[3], Version: f[4]}
	if len(f) == 6 && f[5] != "-" {
		h.MachineID = f[5]
	}
	switch f[2] {
	case "x86_64", "amd64":
		h.Arch = "amd64"
	case "aarch64", "arm64":
		h.Arch = "arm64"
	default:
		return h, fmt.Errorf("Prozessor %s wird nicht unterstützt", f[2])
	}
	return h, nil
}

// Steuerzeilen des Agenten.
const (
	ctlAsk    = "frage"      // frage <id> <0|1 geheim> <text>
	ctlAskEnd = "frage-ende" // frage-ende <id>
	ctlEnd    = "ende"       // ende <exit-code>
	// ctlAskInfo kommt direkt vor „frage“ derselben id und trägt, was nur
	// der Leitstand braucht: Knöpfe und den Satz statt „Enter = …“.
	ctlAskInfo = "frage-info" // frage-info <id> <json>
)

// Control ist eine Steuerzeile.
type Control struct {
	Kind   string
	ID     int
	Secret bool
	Text   string
	Code   int
	Info   AskInfo // nur bei frage-info
}

// AskInfo ergänzt eine Frage für den Leitstand.
type AskInfo struct {
	Choices []backup.Choice `json:"auswahl,omitempty"`
	Note    string          `json:"hinweis,omitempty"`
}

// ParseControl erkennt eine Steuerzeile. ok ist false für normale Ausgabe.
func ParseControl(line string) (Control, bool) {
	rest, ok := strings.CutPrefix(line, Prefix)
	if !ok {
		return Control{}, false
	}
	kind, rest, _ := strings.Cut(rest, " ")
	c := Control{Kind: kind}
	switch kind {
	case ctlAsk:
		idS, rest, _ := strings.Cut(rest, " ")
		sec, text, _ := strings.Cut(rest, " ")
		c.ID, _ = strconv.Atoi(idS)
		c.Secret, c.Text = sec == "1", text
	case ctlAskEnd:
		c.ID, _ = strconv.Atoi(rest)
	case ctlEnd:
		c.Code, _ = strconv.Atoi(rest)
	case ctlAskInfo:
		idS, js, _ := strings.Cut(rest, " ")
		c.ID, _ = strconv.Atoi(idS)
		if json.Unmarshal([]byte(js), &c.Info) != nil {
			return Control{}, false
		}
	default:
		return Control{}, false
	}
	return c, true
}

// Ask, AskEnd und End bauen Steuerzeilen.
func Ask(id int, secret bool, text string) string {
	s := "0"
	if secret {
		s = "1"
	}
	return fmt.Sprintf("%s%s %d %s %s", Prefix, ctlAsk, id, s, strings.ReplaceAll(text, "\n", " "))
}

func AskEnd(id int) string { return fmt.Sprintf("%s%s %d", Prefix, ctlAskEnd, id) }

// AskInfoLine baut die Zeile frage-info; leer, wenn es nichts zu ergänzen
// gibt.
func AskInfoLine(id int, q backup.Question) string {
	if len(q.Choices) == 0 && q.Note == "" {
		return ""
	}
	b, _ := json.Marshal(AskInfo{Choices: q.Choices, Note: q.Note})
	return fmt.Sprintf("%s%s %d %s", Prefix, ctlAskInfo, id, b)
}
func End(code int) string { return fmt.Sprintf("%s%s %d", Prefix, ctlEnd, code) }

// Answer ist die Zeile, mit der der PC eine Frage beantwortet.
func Answer(id int, text string) string {
	return fmt.Sprintf("antwort %d %s", id, strings.ReplaceAll(text, "\n", " "))
}

// SyncWriter schützt eine Ausgabe vor gleichzeitigen Schreibern, damit
// Steuerzeilen nie mitten in normalen Zeilen landen.
type SyncWriter struct {
	mu sync.Mutex
	W  io.Writer
}

func (s *SyncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.W.Write(p)
}

// Line schreibt eine ganze Zeile.
func (s *SyncWriter) Line(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintln(s.W, line)
}

// ServerInput ist die Eingabe des Agenten: Fragen gehen als Steuerzeilen
// zum PC, Antworten kommen über die Standardeingabe.
type ServerInput struct {
	out    *SyncWriter
	cancel context.CancelFunc

	mu        sync.Mutex
	next      int
	waiting   map[int]chan string
	last      time.Time // letzter Herzschlag, Null bis zum ersten
	timeout   time.Duration
	onTimeout func()
}

// OnTimeout setzt, was vor dem Abbruch wegen ausbleibendem Herzschlag
// passiert, etwa eine Zeile ins Protokoll.
func (si *ServerInput) OnTimeout(f func()) {
	si.mu.Lock()
	si.onTimeout = f
	si.mu.Unlock()
}

// watch bricht ab, wenn der Herzschlag nach dem ersten Mal ausbleibt.
func (si *ServerInput) watch(ctx context.Context) {
	t := time.NewTicker(si.timeout / 6)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		si.mu.Lock()
		dead := !si.last.IsZero() && time.Since(si.last) > si.timeout
		f := si.onTimeout
		si.mu.Unlock()
		if dead {
			// Erst abbrechen: Das Schreiben in die tote Verbindung darf den
			// Abbruch nicht aufhalten.
			si.cancel()
			if f != nil {
				f()
			}
			return
		}
	}
}

// ReadConfig liest die erste Zeile der Eingabe als Config und startet dann
// das Lesen der Antworten. Endet die Eingabe (der PC hat die Verbindung
// verloren) oder kommt „abbruch“, ruft ServerInput cancel auf.
func ReadConfig(in io.Reader, out *SyncWriter, cancel context.CancelFunc) (Config, *ServerInput, error) {
	br := bufio.NewReader(in)
	line, err := br.ReadString('\n')
	if err != nil {
		return Config{}, nil, fmt.Errorf("keine Anweisung vom PC: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal([]byte(line), &cfg); err != nil {
		return Config{}, nil, fmt.Errorf("Anweisung vom PC unlesbar: %w", err)
	}
	if cfg.V != 1 {
		return Config{}, nil, errors.New("PC und Server haben verschiedene Versionen von HostDeck")
	}
	si := &ServerInput{out: out, cancel: cancel, waiting: map[int]chan string{}, timeout: HeartbeatTimeout}
	done := make(chan struct{})
	go func() {
		si.read(br)
		close(done)
	}()
	wctx, stop := context.WithCancel(context.Background())
	go func() {
		<-done
		stop()
	}()
	go si.watch(wctx)
	return cfg, si, nil
}

func (si *ServerInput) read(br *bufio.Reader) {
	sc := bufio.NewScanner(br)
	for sc.Scan() {
		line := sc.Text()
		if line == Heartbeat {
			si.mu.Lock()
			si.last = time.Now()
			si.mu.Unlock()
			continue
		}
		if line == "abbruch" {
			si.cancel()
			continue
		}
		rest, ok := strings.CutPrefix(line, "antwort ")
		if !ok {
			continue
		}
		idS, text, _ := strings.Cut(rest, " ")
		id, _ := strconv.Atoi(idS)
		si.mu.Lock()
		ch := si.waiting[id]
		delete(si.waiting, id)
		si.mu.Unlock()
		if ch != nil {
			ch <- text
		}
	}
	// Eingabe zu Ende: Der PC ist weg. Der Lauf bricht ab und startet
	// gestoppte Container wieder.
	si.cancel()
}

// Ask stellt eine Frage über den PC.
func (si *ServerInput) Ask(ctx context.Context, q backup.Question) (string, error) {
	si.mu.Lock()
	si.next++
	id := si.next
	ch := make(chan string, 1)
	si.waiting[id] = ch
	si.mu.Unlock()
	if l := AskInfoLine(id, q); l != "" && !q.Secret {
		si.out.Line(l)
	}
	si.out.Line(Ask(id, q.Secret, q.Text))
	select {
	case a := <-ch:
		return a, nil
	case <-ctx.Done():
		si.mu.Lock()
		delete(si.waiting, id)
		si.mu.Unlock()
		si.out.Line(AskEnd(id))
		return "", ctx.Err()
	}
}
