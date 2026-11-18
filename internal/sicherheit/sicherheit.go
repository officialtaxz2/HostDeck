// Package sicherheit zeigt, was auf dem Server von außen erreichbar ist,
// und schließt auf deinen Wunsch einzelne Ports.
//
// HostDeck misst erst: welche Ports auf allen Adressen lauschen, wem sie gehören
// (Prozess oder Container), ob Caddy sie ohnehin über eine Domain
// weiterreicht, ob eine Firewall läuft, ob Container vollen Zugriff auf
// Docker haben und wie SSH eingestellt ist. Daraus macht es Vorschläge.
//
// Schließen heißt: HostDeck legt eine eigene nftables-Tabelle an, die nur die
// gewählten Ports von außen verwirft. Sie verträgt sich mit ufw, firewalld
// und den Regeln von Docker, weil sie nur zusätzlich verwirft und nie etwas
// erlaubt. SSH, 80 und 443 schließt HostDeck nie, Ports des Panels auch nicht.
// Nach jeder Änderung muss der Leitstand sie binnen Window bestätigen, sonst
// nimmt HostDeck sie von selbst zurück.
package sicherheit

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/altserver"
	"github.com/officialtaxz2/hostdeck/internal/betrieb"
	"github.com/officialtaxz2/hostdeck/internal/discover"
	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
)

// Fehler.
var (
	ErrChanged  = errors.New("seit der Vorschau hat sich etwas geändert; bitte die Vorschau neu öffnen")
	ErrNotFound = errors.New("diesen Port kennt HostDeck nicht")
	ErrLocked   = errors.New("diesen Port schließt HostDeck nicht")
	ErrNoNft    = errors.New("nft fehlt auf dem Server (Paket nftables)")
	ErrPending  = errors.New("eine Änderung wartet noch auf Bestätigung")
	ErrNothing  = errors.New("nichts zu ändern")
)

// Runner führt ein Programm aus und liefert seine Ausgabe.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Docker ist, was die Sicherheit von Docker liest (dockerapi.Ops).
type Docker interface {
	List(ctx context.Context) ([]dockerapi.OpsContainer, error)
}

// Service misst und schließt Ports.
type Service struct {
	Docker Docker
	Run    Runner
	// Proxied liefert je Port auf dem Server die Domains, die Caddy dorthin
	// weiterreicht; nil: unbekannt.
	Proxied func(ctx context.Context) map[int][]string
	Proc    string // Standard /proc
	Etc     string // Standard /etc; für ufw.conf
	// RuleFile ist die Datei mit der Tabelle von HostDeck, UnitFile der
	// systemd-Dienst, der sie beim Start lädt, StateFile die Liste der
	// geschlossenen Ports.
	RuleFile, UnitFile, StateFile string
	Window                        time.Duration // so lange wartet HostDeck auf die Bestätigung (Standard 90 s)
	Now                           func() time.Time
	// Old liefert, was HostDeck auf dem alten Server geschlossen hatte (nach
	// einem Umzug); nil: nichts.
	Old func() *altserver.Info
	// OnExpire bekommt den Satz fürs Protokoll, wenn HostDeck eine Änderung
	// mangels Bestätigung zurückgenommen hat.
	OnExpire func(text string)

	mu      sync.Mutex
	pending *pending
	fileMu  sync.Mutex // StateFile lesen, ändern, schreiben
}

// Table ist der Name der eigenen nftables-Tabelle.
const Table = "hostdeck_schutz"

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) proc() string {
	if s.Proc != "" {
		return s.Proc
	}
	return "/proc"
}

func (s *Service) etc() string {
	if s.Etc != "" {
		return s.Etc
	}
	return "/etc"
}

// Port ist ein Port oder Bereich, der von außen erreichbar ist (oder den
// HostDeck geschlossen hat).
type Port struct {
	Key       string   `json:"schluessel"` // etwa tcp/3552 oder udp/51000-51100
	Proto     string   `json:"proto"`
	From      int      `json:"von"`
	To        int      `json:"bis"`
	Owner     string   `json:"wer"` // Prozess oder Container
	Kind      string   `json:"art"` // ssh, web, panel, container, prozess, unbekannt
	Project   string   `json:"projekt,omitempty"`
	Domains   []string `json:"domains,omitempty"` // Caddy reicht diese Domains auf den Port weiter
	Listening bool     `json:"lauscht"`
	Closed    bool     `json:"geschlossen"`
	Locked    string   `json:"bleibt_offen,omitempty"` // warum HostDeck ihn nie schließt
	Risk      string   `json:"risiko,omitempty"`       // hoch, mittel
	Advice    string   `json:"rat,omitempty"`
	Suggest   bool     `json:"vorschlag"`            // HostDeck rät zum Schließen
	WasClosed bool     `json:"wie_vorher,omitempty"` // auf dem alten Server geschlossen (Umzug)
	// KeptOpen: Du lässt ihn bewusst offen (seit); dann kein Vorschlag.
	KeptOpen *time.Time `json:"bewusst_offen,omitempty"`
}

// DockerAccess ist ein Container mit vollem Zugriff auf Docker.
type DockerAccess struct {
	Name    string   `json:"name"`
	Project string   `json:"projekt,omitempty"`
	Running bool     `json:"laeuft"`
	Public  []string `json:"offen,omitempty"` // seine Ports von außen
	Panel   bool     `json:"panel,omitempty"` // legt selbst Container an (Panel)
	Advice  string   `json:"rat"`
}

// Firewall ist der Stand der Firewalls auf dem Server.
type Firewall struct {
	Ufw       bool   `json:"ufw"`
	Firewalld bool   `json:"firewalld"`
	Nft       bool   `json:"nft"`      // nft ist da; ohne kann HostDeck nichts schließen
	Own       bool   `json:"eigene"`   // die Tabelle von HostDeck ist geladen
	OwnLost   bool   `json:"verloren"` // HostDeck hat Ports geschlossen, aber die Tabelle fehlt
	Note      string `json:"hinweis"`
}

// SSH ist, was sshd über sich sagt.
type SSH struct {
	Ports    []int    `json:"ports"`
	Password string   `json:"passwort"` // passwordauthentication
	Root     string   `json:"root"`     // permitrootlogin
	Known    bool     `json:"bekannt"`
	Advice   []string `json:"rat,omitempty"`
}

// Pending ist eine Änderung, die auf die Bestätigung wartet.
type Pending struct {
	Until time.Time `json:"bis"`
	What  string    `json:"was"`
}

// Overview ist der Stand für die Seite Sicherheit.
type Overview struct {
	Ports    []Port         `json:"ports"`
	Docker   []DockerAccess `json:"docker_zugriff"`
	Firewall Firewall       `json:"firewall"`
	SSH      SSH            `json:"ssh"`
	Pending  *Pending       `json:"ausstehend,omitempty"`
	Problem  string         `json:"problem,omitempty"`
	At       time.Time      `json:"stand_von"`
}

// ---------- Messen ----------

type sock struct {
	proto string
	addr  string
	port  int
	inode string
}

// sockets liest offene TCP-Ports (LISTEN) und gebundene UDP-Ports ohne
// Gegenstelle aus /proc/net.
func (s *Service) sockets() []sock {
	var out []sock
	for _, f := range []string{"tcp", "tcp6", "udp", "udp6"} {
		b, err := os.ReadFile(filepath.Join(s.proc(), "net", f))
		if err != nil {
			continue
		}
		proto := strings.TrimSuffix(f, "6")
		for i, line := range strings.Split(string(b), "\n") {
			fl := strings.Fields(line)
			if i == 0 || len(fl) < 10 {
				continue
			}
			if proto == "tcp" && fl[3] != "0A" { // LISTEN
				continue
			}
			if proto == "udp" {
				if _, rport, ok := discover.ParseProcAddr(fl[2]); !ok || rport != 0 {
					continue
				}
			}
			addr, port, ok := discover.ParseProcAddr(fl[1])
			if !ok || port == 0 {
				continue
			}
			out = append(out, sock{proto, addr, port, fl[9]})
		}
	}
	return out
}

var cgroupID = regexp.MustCompile(`[0-9a-f]{64}`)

type owner struct {
	comm, container string
}

// owners ordnet Socket-Inodes Prozessen und Containern zu.
func (s *Service) owners(inodes map[string]bool) map[string]owner {
	out := map[string]owner{}
	procs, err := os.ReadDir(s.proc())
	if err != nil {
		return out
	}
	for _, p := range procs {
		if _, err := strconv.Atoi(p.Name()); err != nil {
			continue
		}
		dir := filepath.Join(s.proc(), p.Name())
		fds, err := os.ReadDir(filepath.Join(dir, "fd"))
		if err != nil {
			continue
		}
		var mine []string
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(link, "socket:[") {
				continue
			}
			in := strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")
			if inodes[in] {
				mine = append(mine, in)
			}
		}
		if len(mine) == 0 {
			continue
		}
		var o owner
		if b, err := os.ReadFile(filepath.Join(dir, "comm")); err == nil {
			o.comm = strings.TrimSpace(string(b))
		}
		if b, err := os.ReadFile(filepath.Join(dir, "cgroup")); err == nil {
			o.container = cgroupID.FindString(string(b))
		}
		for _, in := range mine {
			out[in] = o
		}
	}
	return out
}

func public(addr string) bool {
	ip := net.ParseIP(addr)
	return addr == "" || ip == nil || !ip.IsLoopback()
}

// Bekannte Datenbank-Ports: von außen offen ist fast immer ein Versehen.
var dbPorts = map[int]string{5432: "PostgreSQL", 3306: "MySQL/MariaDB", 6379: "Redis", 27017: "MongoDB", 9200: "Elasticsearch", 11211: "Memcached", 5984: "CouchDB", 8086: "InfluxDB"}

type entry struct {
	proto      string
	port       int
	owner      string
	kind       string
	project    string
	container  string
	sock       bool // Prozess auf dem Host lauscht
	pub        bool // Docker veröffentlicht ihn auf allen Adressen
	dockerSock bool
}

// Overview misst den Stand.
func (s *Service) Overview(ctx context.Context) Overview {
	ov := Overview{At: s.now(), Ports: []Port{}, Docker: []DockerAccess{}}
	ov.SSH = s.ssh(ctx)
	ov.Firewall = s.firewall(ctx)
	if p := s.pendingInfo(); p != nil {
		ov.Pending = p
	}
	st := s.state()
	closed := st.Closed
	if len(closed) > 0 && ov.Firewall.Nft && !ov.Firewall.Own && ov.Pending == nil {
		ov.Firewall.OwnLost = true
	}

	var list []dockerapi.OpsContainer
	if s.Docker != nil {
		var err error
		list, err = s.Docker.List(ctx)
		if err != nil && !dockerapi.Missing(err) {
			ov.Problem = "Docker antwortet nicht: " + err.Error()
		}
	}
	managers := betrieb.ManagedBy(list)
	isManager := map[string]bool{}
	for _, m := range managers {
		isManager[m] = true
	}
	byID := map[string]dockerapi.OpsContainer{}
	for _, c := range list {
		byID[c.ID] = c
	}
	var proxied map[int][]string
	if s.Proxied != nil {
		proxied = s.Proxied(ctx)
	}

	entries := map[string]*entry{}
	get := func(proto string, port int) *entry {
		k := proto + "/" + strconv.Itoa(port)
		if e := entries[k]; e != nil {
			return e
		}
		e := &entry{proto: proto, port: port}
		entries[k] = e
		return e
	}
	containerKind := func(c dockerapi.OpsContainer) string {
		switch {
		case managers[c.Name()] != "" || isManager[c.Name()]:
			return "panel"
		}
		return "container"
	}
	hasSock := func(c dockerapi.OpsContainer) bool {
		for _, m := range c.Mounts {
			if m.Type == "bind" && discover.IsDockerSocket(m.Source) {
				return true
			}
		}
		return false
	}

	// Veröffentlichte Ports der Container.
	for _, c := range list {
		for _, p := range c.Ports {
			if p.PublicPort == 0 || !public(p.IP) {
				continue
			}
			proto := p.Type
			if proto == "" {
				proto = "tcp"
			}
			e := get(proto, p.PublicPort)
			e.pub = true
			e.owner, e.container, e.project, e.kind = c.Name(), c.Name(), c.Labels["com.docker.compose.project"], containerKind(c)
			e.dockerSock = hasSock(c)
		}
	}
	// Sockets auf dem Host.
	socks := s.sockets()
	inodes := map[string]bool{}
	for _, k := range socks {
		if public(k.addr) {
			inodes[k.inode] = true
		}
	}
	own := s.owners(inodes)
	for _, k := range socks {
		if !public(k.addr) {
			continue
		}
		e := get(k.proto, k.port)
		e.sock = true
		if e.owner != "" {
			continue // schon über Docker bekannt (docker-proxy)
		}
		o := own[k.inode]
		e.owner, e.kind = o.comm, "prozess"
		if o.container != "" {
			if c, ok := byID[o.container]; ok {
				e.owner, e.container, e.project, e.kind = c.Name(), c.Name(), c.Labels["com.docker.compose.project"], containerKind(c)
				e.dockerSock = hasSock(c)
			}
		}
		if e.owner == "" {
			e.kind = "unbekannt"
		}
	}

	sshPorts := map[int]bool{}
	for _, p := range ov.SSH.Ports {
		sshPorts[p] = true
	}
	for _, e := range entries {
		switch {
		case e.proto == "tcp" && (sshPorts[e.port] || e.owner == "sshd"):
			e.kind = "ssh"
		case e.port == 80 || e.port == 443:
			if e.kind == "prozess" || e.kind == "unbekannt" {
				e.kind = "web"
			}
		}
	}

	// Zu Bereichen zusammenfassen: gleicher Besitzer, gleiches Protokoll,
	// lückenlos.
	var keys []*entry
	for _, e := range entries {
		keys = append(keys, e)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].proto != keys[b].proto {
			return keys[a].proto > keys[b].proto // tcp vor udp
		}
		return keys[a].port < keys[b].port
	})
	ports := []Port{}
	for _, e := range keys {
		if n := len(ports); n > 0 {
			last := &ports[n-1]
			if last.Proto == e.proto && last.To == e.port-1 && last.Owner == e.owner && e.owner != "" && last.Kind == e.kind {
				last.To = e.port
				continue
			}
		}
		p := Port{Proto: e.proto, From: e.port, To: e.port, Owner: e.owner, Kind: e.kind, Project: e.project, Listening: true}
		if p.Owner == "" {
			p.Owner = "unbekannt"
		}
		if d := proxied[e.port]; len(d) > 0 && e.proto == "tcp" {
			p.Domains = d
		}
		classify(&p, e)
		ports = append(ports, p)
	}
	// Geschlossene Ports, die gerade nicht lauschen, bleiben sichtbar, damit
	// du sie wieder öffnen kannst.
	for _, c := range closed {
		found := false
		for i := range ports {
			if ports[i].Proto == c.Proto && ports[i].From <= c.From && ports[i].To >= c.To {
				found = true
			}
		}
		if !found {
			ports = append(ports, Port{Proto: c.Proto, From: c.From, To: c.To, Owner: c.Owner, Kind: "geschlossen", Advice: "Lauscht gerade nicht. HostDeck hält ihn trotzdem von außen zu."})
		}
	}
	var old *altserver.Info
	if s.Old != nil {
		old = s.Old()
	}
	for i := range ports {
		p := &ports[i]
		p.Key = key(p.Proto, p.From, p.To)
		for _, c := range closed {
			if c.Proto == p.Proto && c.From <= p.From && c.To >= p.To {
				p.Closed = true
				p.Suggest = false
			}
		}
		// Umzug: Was HostDeck auf dem alten Server geschlossen hatte,
		// schlägt es hier wieder vor, sobald der Port hier offen ist.
		if !p.Closed && p.Locked == "" && p.Listening && old.Has(p.Proto, p.From, p.To) {
			p.WasClosed, p.Suggest = true, true
			if p.Risk == "" {
				p.Risk = "mittel"
			}
			from := "auf dem alten Server"
			if old.From != "" {
				from += " (" + old.From + ")"
			}
			p.Advice = "Hattest du " + from + " geschlossen. " + p.Advice
		}
		// Bewusst offen gelassen: kein Vorschlag mehr, nur noch der Vermerk.
		if t, ok := st.KeptOpen[p.Key]; ok && !p.Closed && p.Locked == "" {
			t := t
			p.KeptOpen, p.Suggest = &t, false
		}
	}
	ov.Ports = ports

	for _, c := range list {
		if !hasSock(c) {
			continue
		}
		da := DockerAccess{Name: c.Name(), Project: c.Labels["com.docker.compose.project"], Running: c.State == "running", Panel: isManager[c.Name()]}
		for _, p := range ports {
			if p.Owner == c.Name() && !p.Closed {
				da.Public = append(da.Public, portText(p))
			}
		}
		switch {
		case da.Panel:
			da.Advice = "Legt die Spielserver des Panels an und braucht dafür Docker. Gehört so."
		case len(da.Public) > 0:
			da.Advice = "Von außen erreichbar und mit vollem Zugriff auf Docker: Wer hier hineinkommt, beherrscht den ganzen Server. Brauchst du es nicht, stoppe das Projekt unter Projekte (seine Daten bleiben, auch im Backup); sonst schließe den Port und nutze es über eine Domain mit Anmeldung."
		default:
			da.Advice = "Nur auf dem Server erreichbar. Voller Zugriff auf Docker heißt trotzdem: Ein Fehler darin trifft den ganzen Server. Brauchst du es nicht, stoppe das Projekt unter Projekte; seine Daten bleiben, auch im Backup."
		}
		ov.Docker = append(ov.Docker, da)
	}
	return ov
}

func key(proto string, from, to int) string {
	if from == to {
		return proto + "/" + strconv.Itoa(from)
	}
	return proto + "/" + strconv.Itoa(from) + "-" + strconv.Itoa(to)
}

func portText(p Port) string {
	if p.From == p.To {
		return strconv.Itoa(p.From) + "/" + p.Proto
	}
	return strconv.Itoa(p.From) + "–" + strconv.Itoa(p.To) + "/" + p.Proto
}

func classify(p *Port, e *entry) {
	switch e.kind {
	case "ssh":
		p.Locked = "SSH: dein Zugang zum Server. HostDeck schließt ihn nie."
		return
	case "web":
		p.Locked = "Caddy: deine Webseiten und der Leitstand. HostDeck schließt ihn nie."
		return
	case "panel":
		p.Locked = "Gehört zum Panel (Spielserver, Verbindung zum Panel). Was offen sein muss, entscheidest du im Panel."
		return
	}
	if p.From == 80 || p.From == 443 {
		p.Locked = "Port für Webseiten. HostDeck schließt ihn nie."
		return
	}
	db, isDB := dbPorts[e.port]
	switch {
	case isDB:
		p.Suggest, p.Risk = true, "hoch"
		p.Advice = db + " ist direkt aus dem Netz erreichbar. Projekte und Spielserver auf dem Server erreichen die Datenbank auch ohne diesen Weg, ebenso Geräte in einem VPN."
	case e.dockerSock:
		p.Suggest, p.Risk = true, "hoch"
		p.Advice = "Dieser Container hat vollen Zugriff auf Docker und ist von außen erreichbar."
	case len(p.Domains) > 0:
		p.Suggest, p.Risk = true, "mittel"
		p.Advice = "Caddy reicht " + strings.Join(p.Domains, ", ") + " schon an diesen Port weiter. Von außen braucht es ihn nicht; direkt erreichbar umgeht er HTTPS."
	case p.Proto == "udp":
		p.Advice = "UDP nutzen oft Spiele, Sprache oder Streaming. Nur schließen, wenn niemand von außen direkt darauf zugreift."
	default:
		p.Advice = "Nur schließen, wenn niemand von außen direkt darauf zugreift."
	}
}

// ssh liest die Einstellungen von sshd (nur lesen).
func (s *Service) ssh(ctx context.Context) SSH {
	out := SSH{Ports: []int{}}
	if s.Run == nil {
		return out
	}
	b, err := s.Run(ctx, "sshd", "-T")
	if err != nil {
		return out
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, _ := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		switch strings.ToLower(k) {
		case "port":
			if n, err := strconv.Atoi(v); err == nil {
				out.Ports = append(out.Ports, n)
			}
		case "passwordauthentication":
			out.Password = v
		case "permitrootlogin":
			out.Root = v
		}
	}
	out.Known = len(out.Ports) > 0
	if out.Password == "yes" {
		out.Advice = append(out.Advice, "Anmelden mit Passwort ist an. Mit einem SSH-Schlüssel und Passwort aus wäre Raten aussichtslos. Das stellst du am besten selbst um, wenn dein Schlüssel funktioniert; HostDeck ändert SSH nicht, damit du dich nie aussperrst.")
	}
	if out.Root == "yes" {
		out.Advice = append(out.Advice, "root darf sich mit Passwort anmelden. Sicherer ist „prohibit-password“ (root nur mit Schlüssel).")
	}
	return out
}

// firewall erkennt ufw, firewalld und die Tabelle von HostDeck.
func (s *Service) firewall(ctx context.Context) Firewall {
	var f Firewall
	if b, err := os.ReadFile(filepath.Join(s.etc(), "ufw", "ufw.conf")); err == nil && strings.Contains(string(b), "ENABLED=yes") {
		f.Ufw = true
	}
	if s.Run != nil {
		if b, err := s.Run(ctx, "systemctl", "is-active", "firewalld"); err == nil && strings.TrimSpace(string(b)) == "active" {
			f.Firewalld = true
		}
		if _, err := s.Run(ctx, "nft", "--version"); err == nil {
			f.Nft = true
			if _, err := s.Run(ctx, "nft", "list", "table", "inet", Table); err == nil {
				f.Own = true
			}
		}
	}
	switch {
	case f.Ufw || f.Firewalld:
		f.Note = "Eine Firewall läuft. Ports, die Docker veröffentlicht, gehen trotzdem an ihr vorbei; die zeigt HostDeck hier mit."
	default:
		f.Note = "Keine Firewall aktiv: Alles, was auf allen Adressen lauscht, ist von außen erreichbar, sofern dein Anbieter nichts davor filtert."
	}
	return f
}
