package sicherheit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Closed ist ein Port oder Bereich, den HostDeck von außen schließt.
type Closed struct {
	Proto string    `json:"proto"`
	From  int       `json:"von"`
	To    int       `json:"bis"`
	Owner string    `json:"wer,omitempty"`
	Since time.Time `json:"seit"`
}

type stateFile struct {
	Closed []Closed `json:"geschlossen"`
	// KeptOpen: Ports, die du bewusst offen lässt (Schlüssel wie tcp/3306),
	// mit Zeitpunkt. HostDeck schlägt sie nicht mehr zum Schließen vor; sie
	// zählen in keinem Hinweis mit. Das ändert nichts an der Firewall.
	KeptOpen map[string]time.Time `json:"bewusst_offen,omitempty"`
}

func (s *Service) state() stateFile {
	var st stateFile
	if b, err := os.ReadFile(s.StateFile); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func (s *Service) closed() []Closed { return s.state().Closed }

func (s *Service) saveState(st stateFile) error {
	if err := os.MkdirAll(filepath.Dir(s.StateFile), 0o700); err != nil {
		return err
	}
	if st.Closed == nil {
		st.Closed = []Closed{}
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	tmp := s.StateFile + ".neu"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.StateFile)
}

func (s *Service) saveClosed(c []Closed) error {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	st := s.state()
	st.Closed = c
	return s.saveState(st)
}

// KeepOpen lässt einen Port bewusst offen (on) oder nimmt das zurück: HostDeck
// schlägt ihn dann nicht mehr zum Schließen vor (Designvorgabe
// Grundregel 4). Die Firewall bleibt, wie sie ist. Der Text
// ist der Satz fürs Protokoll.
func (s *Service) KeepOpen(ctx context.Context, key string, on bool) (string, error) {
	if s.StateFile == "" {
		return "", ErrNothing
	}
	var port *Port
	ov := s.Overview(ctx)
	for i := range ov.Ports {
		if ov.Ports[i].Key == key {
			port = &ov.Ports[i]
		}
	}
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	st := s.state()
	_, kept := st.KeptOpen[key]
	if on {
		// Nur, was HostDeck gerade zum Schließen vorschlägt: Ports, die HostDeck nie
		// schließt oder die schon zu sind, brauchen das nicht.
		if port == nil {
			return "", fmt.Errorf("%s: %w", key, ErrNotFound)
		}
		if !kept && !port.Suggest {
			return "", ErrNothing
		}
		if !kept {
			if st.KeptOpen == nil {
				st.KeptOpen = map[string]time.Time{}
			}
			st.KeptOpen[key] = s.now()
			if err := s.saveState(st); err != nil {
				return "", err
			}
		}
		return "Bleibt bewusst offen: " + portLabel(key, port) + ". HostDeck schlägt ihn nicht mehr zum Schließen vor; die Firewall bleibt, wie sie ist.", nil
	}
	if !kept {
		return "", ErrNothing
	}
	delete(st.KeptOpen, key)
	if err := s.saveState(st); err != nil {
		return "", err
	}
	return "Wieder als Vorschlag: " + portLabel(key, port) + ".", nil
}

func portLabel(key string, p *Port) string {
	if p == nil {
		return key
	}
	t := portText(*p)
	if p.Owner != "" && p.Owner != "unbekannt" {
		t += " (" + p.Owner + ")"
	}
	return t
}

// Change ist eine gewünschte Änderung: Ports schließen und öffnen, je über
// ihren Schlüssel aus der Übersicht.
type Change struct {
	Close []string `json:"schliessen"`
	Open  []string `json:"oeffnen"`
}

// Plan ist die Vorschau einer Änderung.
type Plan struct {
	Close   []Port   `json:"schliessen"`
	Open    []Port   `json:"oeffnen"`
	Steps   []string `json:"schritte"`
	Rules   string   `json:"regeln,omitempty"` // die Tabelle von HostDeck danach
	Back    string   `json:"rueckweg"`
	Notes   []string `json:"hinweise,omitempty"`
	Problem string   `json:"problem,omitempty"`
	Stand   string   `json:"stand"`

	next []Closed
}

func (s *Service) window() time.Duration {
	if s.Window > 0 {
		return s.Window
	}
	return 90 * time.Second
}

// Plan zeigt, was eine Änderung täte.
func (s *Service) Plan(ctx context.Context, ch Change) (Plan, error) {
	ov := s.Overview(ctx)
	return s.plan(ov, ch)
}

func (s *Service) plan(ov Overview, ch Change) (Plan, error) {
	if len(ch.Close) == 0 && len(ch.Open) == 0 {
		return Plan{}, ErrNothing
	}
	byKey := map[string]Port{}
	for _, p := range ov.Ports {
		byKey[p.Key] = p
	}
	p := Plan{Close: []Port{}, Open: []Port{}}
	cur := s.closed()
	next := append([]Closed(nil), cur...)
	now := s.now()
	for _, k := range ch.Close {
		pt, ok := byKey[k]
		if !ok {
			return Plan{}, fmt.Errorf("%s: %w", k, ErrNotFound)
		}
		if pt.Locked != "" {
			return Plan{}, fmt.Errorf("%s: %w: %s", k, ErrLocked, pt.Locked)
		}
		if pt.Closed {
			continue
		}
		p.Close = append(p.Close, pt)
		next = append(next, Closed{Proto: pt.Proto, From: pt.From, To: pt.To, Owner: pt.Owner, Since: now})
	}
	for _, k := range ch.Open {
		pt, ok := byKey[k]
		if !ok || !pt.Closed {
			return Plan{}, fmt.Errorf("%s: %w", k, ErrNotFound)
		}
		p.Open = append(p.Open, pt)
		var keep []Closed
		for _, c := range next {
			if !(c.Proto == pt.Proto && c.From >= pt.From && c.To <= pt.To) && !(c.Proto == pt.Proto && c.From <= pt.From && c.To >= pt.To) {
				keep = append(keep, c)
			}
		}
		next = keep
	}
	if len(p.Close) == 0 && len(p.Open) == 0 {
		return Plan{}, ErrNothing
	}
	sort.Slice(next, func(a, b int) bool {
		if next[a].Proto != next[b].Proto {
			return next[a].Proto > next[b].Proto
		}
		return next[a].From < next[b].From
	})
	p.next = next
	if !ov.Firewall.Nft {
		p.Problem = "Auf dem Server fehlt nft (Paket nftables). Damit schließt HostDeck Ports. Installieren: sudo apt install nftables"
	}
	if ov.Pending != nil {
		p.Problem = "Eine Änderung wartet noch auf Bestätigung. Erst danach geht die nächste."
	}
	texts := func(list []Port) string {
		var t []string
		for _, x := range list {
			t = append(t, portText(x)+" ("+x.Owner+")")
		}
		return strings.Join(t, ", ")
	}
	if len(p.Close) > 0 {
		p.Steps = append(p.Steps, "Von außen schließen: "+texts(p.Close)+". Auf dem Server selbst, für Caddy, für Container (auch Spielserver im Panel-Netz) und aus privaten Netzen wie einem VPN bleiben sie erreichbar.")
	}
	if len(p.Open) > 0 {
		p.Steps = append(p.Steps, "Wieder öffnen: "+texts(p.Open)+".")
	}
	if len(next) > 0 {
		p.Rules = rules(next)
		p.Steps = append(p.Steps,
			"nft prüft die Regeln, dann lädt HostDeck sie in die eigene Tabelle „"+Table+"“. Andere Firewalls und die Regeln von Docker bleiben unberührt.",
			fmt.Sprintf("Der Leitstand bestätigt danach von selbst, dass er dich noch erreicht. Kommt binnen %d Sekunden keine Bestätigung, nimmt HostDeck die Änderung zurück.", int(s.window().Seconds())),
			"Ein kleiner systemd-Dienst (hostdeck-schutz) lädt die Regeln nach jedem Neustart des Servers wieder.")
	} else {
		p.Steps = append(p.Steps, "HostDeck entfernt seine Tabelle und den Dienst hostdeck-schutz; danach schließt HostDeck keinen Port mehr.")
	}
	p.Back = "Jeden Port kannst du hier jederzeit wieder öffnen. Notfalls auf dem Server: sudo nft delete table inet " + Table
	for _, x := range p.Close {
		if len(x.Domains) > 0 {
			p.Notes = append(p.Notes, portText(x)+": Über "+strings.Join(x.Domains, ", ")+" bleibt es erreichbar.")
		}
	}
	h := sha256.New()
	for _, c := range next {
		fmt.Fprintf(h, "%s|%d|%d|%s\n", c.Proto, c.From, c.To, c.Owner)
	}
	for _, x := range append(append([]Port{}, p.Close...), p.Open...) {
		fmt.Fprintf(h, "%s|%s|%s\n", x.Key, x.Owner, x.Locked)
	}
	p.Stand = hex.EncodeToString(h.Sum(nil))[:16]
	return p, nil
}

// rules schreibt die Tabelle von HostDeck. Die ersten beiden Zeilen ersetzen
// eine vorhandene Tabelle in einem Zug.
// Private Netze: Absender von dort lässt die Tabelle immer durch.
var (
	PrivateV4 = []string{"10.0.0.0/8", "100.64.0.0/10", "172.16.0.0/12", "192.168.0.0/16"}
	PrivateV6 = []string{"fc00::/7", "fe80::/10"}
)

func rules(list []Closed) string {
	var tcp, udp []string
	for _, c := range list {
		v := strconv.Itoa(c.From)
		if c.To > c.From {
			v += "-" + strconv.Itoa(c.To)
		}
		if c.Proto == "udp" {
			udp = append(udp, v)
		} else {
			tcp = append(tcp, v)
		}
	}
	var b strings.Builder
	b.WriteString("# Von HostDeck verwaltet (Leitstand, Seite Sicherheit). Bitte nicht von Hand ändern.\n")
	b.WriteString("# Verwirft die gewählten Ports von außen; erlaubt selbst nichts.\n")
	fmt.Fprintf(&b, "table inet %s\ndelete table inet %s\n", Table, Table)
	fmt.Fprintf(&b, "table inet %s {\n", Table)
	chain := func(name, hook string, match func(proto, set string) string) {
		fmt.Fprintf(&b, "\tchain %s {\n\t\ttype filter hook %s priority filter - 5; policy accept;\n", name, hook)
		b.WriteString("\t\tiif \"lo\" accept\n\t\tiifname \"docker0\" accept\n\t\tiifname \"br-*\" accept\n")
		// Absender aus privaten Netzen: Docker-Netze mit eigener Brücke
		// (Wings: pterodactyl0), VPN (wg0, Tailscale), internes Netz des
		// Anbieters. Aus dem Internet kommt so etwas nicht an.
		fmt.Fprintf(&b, "\t\tip saddr { %s } accept\n", strings.Join(PrivateV4, ", "))
		fmt.Fprintf(&b, "\t\tip6 saddr { %s } accept\n", strings.Join(PrivateV6, ", "))
		if len(tcp) > 0 {
			fmt.Fprintf(&b, "\t\t%s drop\n", match("tcp", strings.Join(tcp, ", ")))
		}
		if len(udp) > 0 {
			fmt.Fprintf(&b, "\t\t%s drop\n", match("udp", strings.Join(udp, ", ")))
		}
		b.WriteString("\t}\n")
	}
	// Eingang: Prozesse auf dem Host und der docker-proxy.
	chain("eingang", "input", func(proto, set string) string { return proto + " dport { " + set + " }" })
	// Weiterleitung: veröffentlichte Ports von Containern; Docker hat das
	// Ziel schon umgeschrieben, darum zählt der ursprüngliche Port.
	chain("weiter", "forward", func(proto, set string) string {
		return "meta l4proto " + proto + " ct original proto-dst { " + set + " }"
	})
	b.WriteString("}\n")
	return b.String()
}

func (s *Service) nftBin() string {
	for _, p := range []string{"/usr/sbin/nft", "/sbin/nft", "/usr/bin/nft"} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return "/usr/sbin/nft"
}

func (s *Service) unit() string {
	nft := s.nftBin()
	return "# Von HostDeck angelegt: lädt die Regeln der Seite Sicherheit nach jedem Start.\n" +
		"[Unit]\nDescription=HostDeck: gewählte Ports von außen schließen\n" +
		"After=nftables.service network-pre.target\nWants=network-pre.target\n\n" +
		"[Service]\nType=oneshot\nRemainAfterExit=yes\n" +
		"ExecStart=" + nft + " -f " + s.RuleFile + "\n" +
		"ExecStop=-" + nft + " delete table inet " + Table + "\n\n" +
		"[Install]\nWantedBy=multi-user.target\n"
}

type pending struct {
	until    time.Time
	what     string
	prevRule []byte // nil: keine Datei
	prevOn   bool   // Tabelle war geladen
	prevList []Closed
	timer    *time.Timer
}

func (s *Service) pendingInfo() *Pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return nil
	}
	return &Pending{Until: s.pending.until, What: s.pending.what}
}

func writeAtomic(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".neu"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Apply führt eine Änderung aus, wie die Vorschau mit stand sie gezeigt
// hat. Schließt sie etwas, wartet HostDeck danach auf Keep; ohne Bestätigung
// nimmt es sie nach Window zurück.
func (s *Service) Apply(ctx context.Context, ch Change, stand string) (string, error) {
	ov := s.Overview(ctx)
	p, err := s.plan(ov, ch)
	if err != nil {
		return "", err
	}
	if p.Stand != stand {
		return "", ErrChanged
	}
	if s.pendingInfo() != nil {
		return "", ErrPending
	}
	if !ov.Firewall.Nft {
		return "", ErrNoNft
	}
	prevRule, rerr := os.ReadFile(s.RuleFile)
	if rerr != nil {
		prevRule = nil
	}
	prev := &pending{prevRule: prevRule, prevOn: ov.Firewall.Own, prevList: s.closed()}

	if len(p.next) == 0 {
		if err := s.removeAll(ctx); err != nil {
			return "", err
		}
		if err := s.saveClosed(nil); err != nil {
			return "", err
		}
		return "Alle Ports sind wieder offen; HostDeck schließt keinen mehr.", nil
	}
	text := []byte(p.Rules)
	tmp := s.RuleFile + ".pruefen"
	if err := writeAtomic(tmp, text, 0o600); err != nil {
		return "", err
	}
	defer os.Remove(tmp)
	if out, err := s.Run(ctx, "nft", "-c", "-f", tmp); err != nil {
		return "", fmt.Errorf("nft lehnt die Regeln ab: %v %s", err, strings.TrimSpace(string(out)))
	}
	if err := writeAtomic(s.RuleFile, text, 0o600); err != nil {
		return "", err
	}
	if out, err := s.Run(ctx, "nft", "-f", s.RuleFile); err != nil {
		s.restore(context.WithoutCancel(ctx), prev)
		return "", fmt.Errorf("nft: %v %s; alles ist wie vorher", err, strings.TrimSpace(string(out)))
	}
	if err := writeAtomic(s.UnitFile, []byte(s.unit()), 0o644); err == nil {
		_, _ = s.Run(ctx, "systemctl", "daemon-reload")
	}
	if err := s.saveClosed(p.next); err != nil {
		s.restore(context.WithoutCancel(ctx), prev)
		return "", err
	}
	if len(p.Close) == 0 {
		// Nur geöffnet: Aussperren geht damit nicht, keine Wartezeit.
		_, _ = s.Run(ctx, "systemctl", "enable", filepath.Base(s.UnitFile))
		return "Wieder offen: " + portList(p.Open) + ".", nil
	}
	s.mu.Lock()
	prev.until = s.now().Add(s.window())
	prev.what = "Schließen: " + portList(p.Close)
	prev.timer = time.AfterFunc(s.window(), func() { s.expire(prev) })
	s.pending = prev
	s.mu.Unlock()
	return "Geschlossen: " + portList(p.Close) + ". Warte auf die Bestätigung des Leitstands.", nil
}

func portList(list []Port) string {
	var t []string
	for _, x := range list {
		if x.Owner != "" {
			t = append(t, portText(x)+" ("+x.Owner+")")
		} else {
			t = append(t, portText(x))
		}
	}
	return strings.Join(t, ", ")
}

// Keep bestätigt die wartende Änderung: Der Leitstand erreicht dich noch.
func (s *Service) Keep(ctx context.Context) (string, error) {
	s.mu.Lock()
	p := s.pending
	if p == nil {
		s.mu.Unlock()
		return "", errors.New("es wartet keine Änderung auf Bestätigung")
	}
	p.timer.Stop()
	s.pending = nil
	s.mu.Unlock()
	if out, err := s.Run(ctx, "systemctl", "enable", filepath.Base(s.UnitFile)); err != nil {
		return p.what + " ist aktiv, aber der Dienst für den Neustart ließ sich nicht einschalten: " + strings.TrimSpace(string(out)), nil
	}
	return p.what + " ist aktiv und bleibt es auch nach einem Neustart.", nil
}

func (s *Service) expire(p *pending) {
	s.mu.Lock()
	if s.pending != p {
		s.mu.Unlock()
		return
	}
	s.pending = nil
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s.restore(ctx, p)
	if s.OnExpire != nil {
		s.OnExpire(p.what + ": keine Bestätigung vom Leitstand, darum zurückgenommen.")
	}
}

func (s *Service) restore(ctx context.Context, p *pending) {
	if p.prevRule == nil {
		_ = s.removeAll(ctx)
	} else {
		_ = writeAtomic(s.RuleFile, p.prevRule, 0o600)
		if p.prevOn {
			_, _ = s.Run(ctx, "nft", "-f", s.RuleFile)
		} else {
			_, _ = s.Run(ctx, "nft", "delete", "table", "inet", Table)
		}
	}
	_ = s.saveClosed(p.prevList)
}

// removeAll entfernt Tabelle, Regeldatei und Dienst.
func (s *Service) removeAll(ctx context.Context) error {
	if _, err := s.Run(ctx, "nft", "list", "table", "inet", Table); err == nil {
		if out, err := s.Run(ctx, "nft", "delete", "table", "inet", Table); err != nil {
			return fmt.Errorf("nft: %v %s", err, strings.TrimSpace(string(out)))
		}
	}
	if _, err := os.Stat(s.UnitFile); err == nil {
		_, _ = s.Run(ctx, "systemctl", "disable", filepath.Base(s.UnitFile))
		_ = os.Remove(s.UnitFile)
		_, _ = s.Run(ctx, "systemctl", "daemon-reload")
	}
	if err := os.Remove(s.RuleFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Reload lädt die Regeln neu, wenn HostDeck Ports geschlossen hat, die Tabelle
// aber fehlt (etwa nach „systemctl restart nftables“, das alles leert).
func (s *Service) Reload(ctx context.Context) (string, error) {
	list := s.closed()
	if len(list) == 0 {
		return "", ErrNothing
	}
	if err := writeAtomic(s.RuleFile, []byte(rules(list)), 0o600); err != nil {
		return "", err
	}
	if out, err := s.Run(ctx, "nft", "-f", s.RuleFile); err != nil {
		return "", fmt.Errorf("nft: %v %s", err, strings.TrimSpace(string(out)))
	}
	return "Die Regeln von HostDeck sind wieder geladen.", nil
}

// Adopt lädt beim Umstieg von sbm auf HostDeck die eigene Tabelle und
// schaltet den eigenen Dienst ein, solange die alte Tabelle noch steht. So
// ist kein geschlossener Port zwischendurch offen, und die Überwachung
// sieht die Regeln von Anfang an. msg ist leer, wenn kein Port zu ist.
func (s *Service) Adopt(ctx context.Context) (string, error) {
	list := s.closed()
	if len(list) == 0 {
		return "", nil
	}
	if err := writeAtomic(s.RuleFile, []byte(rules(list)), 0o600); err != nil {
		return "", err
	}
	if out, err := s.Run(ctx, "nft", "-c", "-f", s.RuleFile); err != nil {
		return "", fmt.Errorf("nft lehnt die Regeln ab: %v %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := s.Run(ctx, "nft", "-f", s.RuleFile); err != nil {
		return "", fmt.Errorf("nft: %v %s", err, strings.TrimSpace(string(out)))
	}
	if err := writeAtomic(s.UnitFile, []byte(s.unit()), 0o644); err != nil {
		return "", err
	}
	_, _ = s.Run(ctx, "systemctl", "daemon-reload")
	_, _ = s.Run(ctx, "systemctl", "enable", filepath.Base(s.UnitFile))
	return fmt.Sprintf("Port-Schutz übernommen: %s geschlossen wie vorher, jetzt in der Tabelle %s.", portNames(list), Table), nil
}

// Drop nimmt Tabelle, Regeldatei und Dienst heraus (Rückweg von Adopt).
func (s *Service) Drop(ctx context.Context) error { return s.removeAll(ctx) }

// DropOld entfernt Tabelle und Dienst der früheren Fassung sbm, nachdem
// Adopt die eigenen geladen hat.
func (s *Service) DropOld(ctx context.Context, oldTable, oldUnit string) error {
	if _, err := s.Run(ctx, "nft", "list", "table", "inet", oldTable); err == nil {
		if out, err := s.Run(ctx, "nft", "delete", "table", "inet", oldTable); err != nil {
			return fmt.Errorf("alte Tabelle %s: %v %s", oldTable, err, strings.TrimSpace(string(out)))
		}
	}
	if _, err := os.Stat(oldUnit); err == nil {
		_, _ = s.Run(ctx, "systemctl", "disable", filepath.Base(oldUnit))
		if err := os.Remove(oldUnit); err != nil {
			return err
		}
		_, _ = s.Run(ctx, "systemctl", "daemon-reload")
	}
	return nil
}

func portNames(list []Closed) string {
	var t []string
	for _, c := range list {
		p := strconv.Itoa(c.From)
		if c.To > c.From {
			p += "–" + strconv.Itoa(c.To)
		}
		t = append(t, p+"/"+c.Proto)
	}
	if len(t) == 1 {
		return "Port " + t[0]
	}
	return "Ports " + strings.Join(t, ", ")
}

// ClosedText nennt die Ports, die HostDeck gerade schließt; leer: keine.
func (s *Service) ClosedText() string {
	list := s.closed()
	if len(list) == 0 {
		return ""
	}
	return portNames(list)
}

// OpenAll öffnet alle Ports wieder und entfernt Tabelle, Regeldatei und
// Dienst (beim Deinstallieren).
func (s *Service) OpenAll(ctx context.Context) error {
	if err := s.removeAll(ctx); err != nil {
		return err
	}
	return s.saveClosed(nil)
}
