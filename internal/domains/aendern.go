package domains

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
)

// Aktionen für eine Domain.
const (
	ActNew    = "neu"
	ActTarget = "ziel"
	ActOff    = "aus"
	ActOn     = "an"
)

// ChangeReq ist eine Änderung an einer Domain.
type ChangeReq struct {
	Action string `json:"aktion"`
	Host   string `json:"domain"`
	Target string `json:"ziel,omitempty"`
}

// ChangePlan zeigt, was die Änderung tut.
type ChangePlan struct {
	Action  string   `json:"aktion"`
	Host    string   `json:"domain"`
	Target  string   `json:"ziel,omitempty"`
	Others  []string `json:"mit,omitempty"` // weitere Domains in derselben Datei
	File    string   `json:"datei,omitempty"`
	Before  string   `json:"vorher,omitempty"`
	After   string   `json:"nachher,omitempty"`
	IPs     []string `json:"ips,omitempty"`
	Steps   []string `json:"schritte"`
	Back    string   `json:"rueckweg,omitempty"`
	Notes   []string `json:"hinweise"`
	Problem string   `json:"problem,omitempty"`
	Stand   string   `json:"stand"`

	files []caddy.File
	skip  []string
	ready bool
	dirs  []string
	open  []string // vorhandene Ordner, die Caddy nicht lesen kann
	sums  map[string]string
	cfg   *caddy.Caddy
}

var hostRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

// NormHost prüft eine Domain und schreibt sie klein.
func NormHost(raw string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(raw))
	h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
	h = strings.TrimSuffix(h, "/")
	if len(h) > 253 || !hostRe.MatchString(h) {
		return "", fmt.Errorf("%w: „%s“ ist keine gültige Domain (etwa app.example.de)", ErrBadChoice, raw)
	}
	return h, nil
}

// NormTarget macht aus „3005“ oder „127.0.0.1:3005“ ein Ziel für
// reverse_proxy.
func NormTarget(raw string) (string, error) {
	t := strings.TrimSpace(raw)
	t = strings.TrimPrefix(t, "http://")
	if n, err := strconv.Atoi(t); err == nil {
		if n < 1 || n > 65535 {
			return "", fmt.Errorf("%w: Port %d gibt es nicht", ErrBadChoice, n)
		}
		return "127.0.0.1:" + t, nil
	}
	host, port, err := net.SplitHostPort(t)
	if err != nil {
		return "", fmt.Errorf("%w: Ziel als Port (3005) oder Adresse mit Port (127.0.0.1:3005)", ErrBadChoice)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("%w: Port %s gibt es nicht", ErrBadChoice, port)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	if net.ParseIP(host) == nil && host != "localhost" && !hostRe.MatchString(strings.ToLower(host)) {
		return "", fmt.Errorf("%w: „%s“ ist kein gültiger Rechnername", ErrBadChoice, host)
	}
	return net.JoinHostPort(host, port), nil
}

func (s *Service) findSite(l *layout, host string) *Site {
	for i := range l.sites {
		if contains(l.sites[i].Hosts, host) {
			return &l.sites[i]
		}
	}
	return nil
}

// ChangePlan rechnet eine Änderung durch, ohne etwas zu ändern.
func (s *Service) ChangePlan(ctx context.Context, req ChangeReq) (ChangePlan, error) {
	l, err := s.read(ctx)
	if err != nil {
		return ChangePlan{}, err
	}
	p := ChangePlan{Action: req.Action, Steps: []string{}, Notes: []string{}, cfg: l.c, sums: map[string]string{}}
	host, err := NormHost(req.Host)
	if err != nil {
		return p, err
	}
	p.Host = host
	if !ordered(l) {
		p.Problem = "Erst Caddy einmal ordnen (oben auf dieser Seite); dann lassen sich Domains einzeln ändern."
		return p.finish(), nil
	}
	sitesDir := filepath.Join(l.dir, SitesDir)
	st := s.findSite(l, host)
	switch req.Action {
	case ActNew:
		if st != nil {
			p.Problem = host + " gibt es hier schon (" + kindText(st) + ")."
			return p.finish(), nil
		}
		t, err := NormTarget(req.Target)
		if err != nil {
			return p, err
		}
		p.Target = t
		if !s.pointsHere(ctx, l, host, &p) {
			return p.finish(), nil
		}
		name := fileName(Site{Hosts: []string{host}}, 0, map[string]bool{})
		path := filepath.Join(sitesDir, name)
		if _, err := os.Stat(path); err == nil {
			p.Problem = path + " gibt es schon."
			return p.finish(), nil
		}
		if _, err := os.Stat(filepath.Join(sitesDir, OffDir, name)); err == nil {
			p.Problem = host + " liegt abgeschaltet in " + filepath.Join(sitesDir, OffDir) + "; bitte dort wieder anschalten."
			return p.finish(), nil
		}
		data := []byte("# " + host + ": angelegt von HostDeck.\n" + host + " {\n\treverse_proxy " + t + "\n}\n")
		p.File, p.After = path, string(data)
		p.files = []caddy.File{{Path: path, Data: data, Was: []byte{}}}
		p.skip, p.ready = []string{host}, true
		p.sums[path] = sum(data)
		if s.Listening != nil && !s.Listening(ctx, t) {
			p.Notes = append(p.Notes, "Am Ziel "+t+" lauscht gerade nichts. Die Domain zeigt dann einen Fehler 502, bis dort etwas läuft.")
		}
		p.Steps = append(p.Steps,
			"Neue Datei "+path+" mit „reverse_proxy "+t+"“.",
			"Caddy prüft und lädt neu, holt das Zertifikat für "+host+" (das kann eine Minute dauern) und HostDeck wartet, bis die Domain antwortet.",
			"Alle anderen Domains müssen danach antworten wie vorher; sonst legt HostDeck alles zurück.")
		p.Back = "Abschalten auf dieser Seite; die Datei kommt dann nach sites/aus/."
	case ActTarget, ActOff, ActOn:
		if st == nil {
			return p, fmt.Errorf("%w: %s", ErrNotFound, host)
		}
		if msg := notMine(st, req.Action); msg != "" {
			p.Problem = msg
			return p.finish(), nil
		}
		old, err := os.ReadFile(st.File)
		if err != nil {
			return p, err
		}
		p.File, p.Before = st.File, string(old)
		for _, h := range st.Hosts {
			if h != host {
				p.Others = append(p.Others, h)
			}
		}
		if st.Hand {
			p.Notes = append(p.Notes, "Die Datei ist nicht mehr so, wie HostDeck sie geschrieben hat (von Hand geändert oder angelegt). HostDeck nimmt ihren jetzigen Inhalt als Grundlage; schau ihn dir unten an.")
		}
		switch req.Action {
		case ActTarget:
			t, err := NormTarget(req.Target)
			if err != nil {
				return p, err
			}
			p.Target = t
			if t == st.Upstreams[0] {
				p.Problem = host + " zeigt schon auf " + t + "."
				return p.finish(), nil
			}
			if s.Listening != nil && !s.Listening(ctx, t) {
				p.Problem = "Am Ziel " + t + " lauscht nichts. HostDeck könnte nicht prüfen, ob die Seite danach geht; erst den Dienst dort starten."
				return p.finish(), nil
			}
			data, err := replaceTarget(old, st, t)
			if err != nil {
				p.Problem = err.Error()
				return p.finish(), nil
			}
			p.After = string(data)
			p.files = []caddy.File{{Path: st.File, Data: data, Was: old}}
			p.sums[st.File] = sum(data)
			p.Steps = append(p.Steps,
				"In "+st.File+": „reverse_proxy "+st.Upstreams[0]+"“ wird „reverse_proxy "+t+"“.",
				"Caddy prüft und lädt neu. Danach müssen "+host+" und alle anderen Domains mindestens so gut antworten wie vorher; sonst legt HostDeck alles zurück.")
			p.Back = "Ziel wieder auf " + st.Upstreams[0] + " stellen."
		case ActOff:
			dst := filepath.Join(sitesDir, OffDir, filepath.Base(st.File))
			if _, err := os.Stat(dst); err == nil {
				p.Problem = dst + " gibt es schon."
				return p.finish(), nil
			}
			if _, err := os.Stat(filepath.Dir(dst)); err != nil {
				p.dirs = []string{filepath.Dir(dst)}
			}
			p.files = []caddy.File{{Path: st.File, Data: nil, Was: old}, {Path: dst, Data: old, Was: []byte{}}}
			p.skip = st.Hosts
			p.sums[dst] = sum(old)
			p.Steps = append(p.Steps,
				"Datei "+filepath.Base(st.File)+" zieht nach "+filepath.Dir(dst)+"/; Caddy lädt sie dann nicht mehr.",
				"Caddy prüft und lädt neu. Alle anderen Domains müssen danach antworten wie vorher; sonst legt HostDeck alles zurück.")
			if len(p.Others) > 0 {
				p.Notes = append(p.Notes, "In derselben Datei stehen auch "+strings.Join(p.Others, ", ")+"; sie gehen mit aus.")
			}
			p.Back = "Wieder anschalten auf dieser Seite; die Datei ist unverändert."
		case ActOn:
			dst := filepath.Join(sitesDir, filepath.Base(st.File))
			if _, err := os.Stat(dst); err == nil {
				p.Problem = dst + " gibt es schon."
				return p.finish(), nil
			}
			for _, h := range st.Hosts {
				if o := s.findSite(l, h); o != nil && o.Kind != KindOff {
					p.Problem = h + " ist schon an (" + kindText(o) + ")."
					return p.finish(), nil
				}
			}
			if !s.pointsHere(ctx, l, host, &p) {
				return p.finish(), nil
			}
			p.files = []caddy.File{{Path: st.File, Data: nil, Was: old}, {Path: dst, Data: old, Was: []byte{}}}
			p.skip, p.ready = st.Hosts, true
			p.sums[dst] = sum(old)
			p.Steps = append(p.Steps,
				"Datei "+filepath.Base(st.File)+" zieht zurück nach "+sitesDir+"/.",
				"Caddy prüft und lädt neu, HostDeck wartet, bis "+host+" mit gültigem Zertifikat antwortet. Alle anderen Domains müssen antworten wie vorher; sonst legt HostDeck alles zurück.")
			if len(st.Upstreams) > 0 && s.Listening != nil && !s.Listening(ctx, st.Upstreams[0]) {
				p.Notes = append(p.Notes, "Am Ziel "+st.Upstreams[0]+" lauscht gerade nichts; die Domain zeigt dann einen Fehler 502.")
			}
			p.Back = "Wieder abschalten auf dieser Seite."
		}
	default:
		return p, fmt.Errorf("%w: unbekannte Aktion „%s“", ErrBadChoice, req.Action)
	}
	if p.open = closedDirs(sitesDir); len(p.open) > 0 {
		p.Steps = append([]string{openStep(p.open)}, p.Steps...)
	}
	return p.finish(), nil
}

func kindText(st *Site) string {
	switch st.Kind {
	case KindOwn:
		return "in " + st.File
	case KindOff:
		return "abgeschaltet in " + st.File
	case KindLeitstand:
		return "Leitstand"
	case KindImported:
		if st.Project != "" {
			return "in " + st.File + ", Projekt " + st.Project
		}
		return "in " + st.File
	}
	return "im Caddyfile"
}

// notMine sagt, warum HostDeck diese Seite so nicht ändert.
func notMine(st *Site, act string) string {
	switch st.Kind {
	case KindLeitstand:
		return "Das ist die Adresse des Leitstands; sie ändert sich nur über „Von außen“."
	case KindImported:
		what := "Die Domain steht in " + st.File
		if st.Project != "" {
			what += ", die zum Projekt " + st.Project + " gehört"
		}
		return what + ". HostDeck schreibt fremde Dateien nicht um; ändere sie im Projekt."
	case KindInline:
		return "Die Domain steht noch im Caddyfile; erst Caddy einmal ordnen."
	case KindOff:
		if act != ActOn {
			return "Die Domain ist abgeschaltet; erst wieder anschalten."
		}
	case KindOwn:
		if act == ActOn {
			return "Die Domain ist schon an."
		}
		if act == ActTarget && !st.Simple {
			return "Die Datei ist mehr als ein einfaches „reverse_proxy Ziel“; das Ziel ändert HostDeck darum nicht von selbst."
		}
	}
	return ""
}

// replaceTarget tauscht das Ziel in der einen reverse_proxy-Zeile.
func replaceTarget(file []byte, st *Site, t string) ([]byte, error) {
	i := bytes.Index(file, []byte(st.text))
	if i < 0 {
		return nil, errors.New("die Datei hat sich gerade geändert; bitte noch einmal öffnen")
	}
	lines := strings.Split(st.text, "\n")
	for _, d := range caddy.Directives(st.text) {
		if d.Depth == 1 && d.Words[0] == "reverse_proxy" {
			ln := lines[d.Line]
			j := strings.Index(ln, st.Upstreams[0])
			if j < 0 {
				break
			}
			lines[d.Line] = ln[:j] + t + ln[j+len(st.Upstreams[0]):]
			text := strings.Join(lines, "\n")
			return append(append(append([]byte(nil), file[:i]...), text...), file[i+len(st.text):]...), nil
		}
	}
	return nil, errors.New("die Zeile mit reverse_proxy ist nicht zu finden")
}

// pointsHere prüft, ob die Domain im DNS auf diesen Server zeigt. Ohne das
// bekommt Caddy kein Zertifikat.
func (s *Service) pointsHere(ctx context.Context, l *layout, host string, p *ChangePlan) bool {
	if s.Resolve == nil {
		return true
	}
	ips, err := s.Resolve(ctx, host)
	if err != nil || len(ips) == 0 {
		p.Problem = host + " hat keinen DNS-Eintrag. Leg erst beim Domain-Anbieter einen A-Eintrag auf diesen Server an; dann klappt auch das Zertifikat."
		return false
	}
	for _, ip := range ips {
		p.IPs = append(p.IPs, ip.String())
	}
	mine := map[string]bool{}
	if s.Local != nil {
		for _, ip := range s.Local() {
			mine[ip.String()] = true
		}
	}
	// Hinter NAT kennt der Server seine öffentliche Adresse nicht; dann
	// zählen die Adressen der Domains, die hier schon laufen.
	n := 0
	for _, st := range l.sites {
		if n >= 3 || len(st.Hosts) == 0 || st.Kind == KindOff || st.Hosts[0] == host || strings.HasPrefix(st.Hosts[0], "*") {
			continue
		}
		if other, err := s.Resolve(ctx, st.Hosts[0]); err == nil {
			n++
			for _, ip := range other {
				mine[ip.String()] = true
			}
		}
	}
	for _, ip := range ips {
		if mine[ip.String()] {
			return true
		}
	}
	p.Problem = host + " zeigt auf " + strings.Join(p.IPs, ", ") + ", nicht auf diesen Server. Ändere erst den DNS-Eintrag; ohne ihn bekommt Caddy kein Zertifikat."
	return false
}

func (p ChangePlan) finish() ChangePlan {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s", p.Action, p.Host, p.Target, p.Problem, p.File, p.Before, p.After)
	p.Stand = hex.EncodeToString(h.Sum(nil))[:16]
	return p
}

// Change führt die Änderung aus, wenn der Plan noch derselbe ist wie in
// der Vorschau (stand).
func (s *Service) Change(ctx context.Context, req ChangeReq, stand string, event func(caddy.Event)) (string, error) {
	p, err := s.ChangePlan(ctx, req)
	if err != nil {
		return "", err
	}
	if p.Problem != "" {
		return "", errors.New(p.Problem)
	}
	if p.Stand != stand {
		return "", ErrChanged
	}
	ok := false
	reopen := func() {}
	defer func() {
		if !ok {
			reopen()
			for _, d := range p.dirs {
				os.Remove(d)
			}
		}
	}()
	for _, d := range p.dirs {
		if err := mkdirCaddy(d); err != nil {
			return "", fmt.Errorf("Ordner %s anlegen: %v. Nichts geändert", d, err)
		}
	}
	if reopen, err = openForCaddy(p.open); err != nil {
		reopen = func() {}
		return "", fmt.Errorf("Rechte für Caddy setzen: %v. Nichts geändert", err)
	}
	ch := caddy.Change{Files: p.files, Skip: p.skip}
	if p.ready && s.Ready != nil {
		ch.Check = func(ctx context.Context) error { return s.Ready(ctx, p.Host) }
	}
	undo, err := s.noteSums(p.sums)
	if err != nil {
		return "", fmt.Errorf("Stand ließ sich nicht merken: %v. Nichts geändert", err)
	}
	if err := s.apply(ctx, p.cfg, ch, "domains-"+p.Action, event); err != nil {
		undo()
		return "", err
	}
	ok = true
	s.changed()
	switch p.Action {
	case ActNew:
		return p.Host + " ist angelegt und zeigt auf " + p.Target + ".", nil
	case ActTarget:
		return p.Host + " zeigt jetzt auf " + p.Target + ".", nil
	case ActOff:
		return p.Host + " ist abgeschaltet; die Datei liegt in " + filepath.Join(filepath.Dir(p.File), OffDir) + ".", nil
	}
	return p.Host + " ist wieder an.", nil
}
