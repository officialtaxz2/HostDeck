// Package domains verwaltet die Domains, die Caddy auf diesem Server
// bedient: einmal umordnen (jede Domain eine eigene Datei in sites/),
// danach anlegen, Ziel ändern, abschalten und wieder anschalten. Jede
// Änderung läuft über caddy.Applier: sichern, prüfen, neu laden, alle
// Seiten mit vorher vergleichen, sonst alles zurück.
package domains

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
)

// Ordner neben dem Caddyfile.
const (
	SitesDir  = "sites"              // eine Datei je Domain
	OffDir    = "aus"                // in sites/: abgeschaltete Domains
	BackupDir = "hostdeck-sicherung" // Kopien vor dem Umordnen
	SitesGlob = SitesDir + "/*.caddy"
)

// Arten einer Seite.
const (
	KindOwn       = "eigen"       // sites/<domain>.caddy, verwaltet HostDeck
	KindOff       = "aus"         // sites/aus/<domain>.caddy, lädt Caddy nicht
	KindInline    = "caddyfile"   // steht noch im Caddyfile
	KindImported  = "eingebunden" // andere Datei, etwa aus einem Projekt
	KindLeitstand = "leitstand"   // hostdeck.caddy, gehört zum Zugang von außen
)

// Errors.
var (
	ErrNoCaddy   = errors.New("auf diesem Server läuft kein Caddy als Dienst")
	ErrNotFound  = errors.New("diese Domain gibt es hier nicht")
	ErrBadChoice = errors.New("das geht so nicht")
	ErrChanged   = errors.New("seit der Vorschau hat sich etwas geändert; bitte die Vorschau noch einmal öffnen")
)

// Site ist ein Site-Block.
type Site struct {
	Hosts     []string `json:"domains"`
	Addrs     []string `json:"adressen"`
	File      string   `json:"datei"`
	Kind      string   `json:"art"`
	Upstreams []string `json:"ziele"`
	Simple    bool     `json:"einfach"`            // genau ein reverse_proxy mit einem Ziel
	Hand      bool     `json:"von_hand,omitempty"` // nicht so, wie HostDeck sie geschrieben hat
	Project   string   `json:"projekt,omitempty"`
	Live      *Live    `json:"live,omitempty"`
	// Each ist die Prüfung je Domain des Blocks (Live gilt nur für die
	// erste). Stehen mehrere Domains in einem Block, prüft HostDeck jede.
	Each map[string]Live `json:"-"`
	// Main sind die Ziele der reverse_proxy ohne Pfad-Matcher: das
	// eigentliche Ziel, wenn ein Block mehrere hat (etwa /api/* woanders).
	Main []string `json:"-"`

	text string
}

// Live ist, was eine Domain gerade tut.
type Live struct {
	Answer    string     `json:"antwort"`
	OK        bool       `json:"ok"`
	Listening *bool      `json:"ziel_lauscht,omitempty"`
	CertUntil *time.Time `json:"zertifikat_bis,omitempty"`
	CertErr   string     `json:"zertifikat_fehler,omitempty"`
	Took      int64      `json:"dauer_ms,omitempty"` // Antwortzeit in Millisekunden
}

// Overview ist der Stand für den Leitstand.
type Overview struct {
	Found   bool      `json:"caddy"`
	Problem string    `json:"problem,omitempty"`
	Config  string    `json:"caddyfile,omitempty"`
	Ordered bool      `json:"geordnet"`
	HTTP3   bool      `json:"http3"`
	Sites   []Site    `json:"seiten"`
	Pending bool      `json:"pruefung_laeuft"`
	Checked time.Time `json:"geprueft,omitempty"`
}

// CertInfo ist das Zertifikat, das Caddy für eine Domain zeigt.
type CertInfo struct {
	Until time.Time
	Err   string
}

// Service verwaltet die Domains. Alle Funktionsfelder sind für Tests
// austauschbar.
type Service struct {
	Find      func(ctx context.Context) (*caddy.Caddy, error)
	Probe     caddy.Prober
	Cert      func(ctx context.Context, host string) CertInfo
	Listening func(ctx context.Context, upstream string) bool
	Resolve   func(ctx context.Context, host string) ([]net.IP, error)
	Local     func() []net.IP
	ProjectOf func(path string) string
	// Ready wartet nach dem Anlegen oder Anschalten, bis die Domain mit
	// gültigem Zertifikat antwortet.
	Ready      func(ctx context.Context, host string) error
	StateFile  string // Prüfsummen der Dateien, die HostDeck geschrieben hat
	Journal    string // Rückweg, solange eine Änderung läuft
	Retries    int
	RetryDelay time.Duration
	Now        func() time.Time
	Event      func(caddy.Event) // optional, für Tests
	// Changed läuft nach jeder Änderung an Caddy (anlegen, Ziel, aus, an,
	// ordnen), etwa damit die Überwachung gleich neu prüft und eine
	// abgeschaltete Domain nicht weiter rot meldet.
	Changed func()

	mu      sync.Mutex
	live    map[string]Live
	liveAt  time.Time
	running bool
	done    chan struct{}
	// Welche Domains mit welchem Ziel zuletzt im Caddyfile standen; so
	// fällt auf, wenn jemand Caddy von Hand ändert.
	hosts     string
	hostsSeen bool
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// layout ist das gelesene Caddyfile mit allen Seiten.
type layout struct {
	c      *caddy.Caddy
	dir    string
	main   []byte
	segs   []caddy.Segment
	sites  []Site
	other  string // Grund, warum das Caddyfile nicht umzuordnen ist
	http3  bool
	h3edit bool // HTTP/3 lässt sich im globalen Block abschalten
}

func (s *Service) read(ctx context.Context) (*layout, error) {
	c, err := s.Find(ctx)
	if err != nil {
		return nil, err
	}
	main, err := os.ReadFile(c.Config)
	if err != nil {
		return nil, err
	}
	segs, err := caddy.Segments(main)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.Config, err)
	}
	l := &layout{c: c, dir: filepath.Dir(c.Config), main: main, segs: segs, http3: true}
	state := s.loadState()
	seen := map[string]bool{c.Config: true}
	for _, sg := range segs {
		switch sg.Kind {
		case caddy.SegSite:
			l.sites = append(l.sites, s.site(sg, c.Config, KindInline, state))
		case caddy.SegImport:
			for _, f := range importFiles(sg.Args[1], l.dir) {
				s.readImported(l, f, 0, seen, state)
			}
		case caddy.SegGlobal:
			_, l.http3, l.h3edit = http3Off(sg.Text)
		case caddy.SegOther:
			if l.other == "" {
				l.other = fmt.Sprintf("Zeile %d im Caddyfile gehört zu keinem Block (%s)", sg.Line, strings.Join(sg.Args, " "))
			}
		}
	}
	if !hasGlobal(segs) {
		l.h3edit = true
	}
	off, _ := filepath.Glob(filepath.Join(l.dir, SitesDir, OffDir, "*.caddy"))
	sort.Strings(off)
	for _, f := range off {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		fsegs, err := caddy.Segments(b)
		if err != nil {
			continue
		}
		for _, sg := range fsegs {
			if sg.Kind == caddy.SegSite {
				l.sites = append(l.sites, s.site(sg, f, KindOff, state))
			}
		}
	}
	return l, nil
}

func hasGlobal(segs []caddy.Segment) bool {
	return len(segs) > 0 && segs[0].Kind == caddy.SegGlobal
}

// importFiles löst ein import-Argument in Dateien auf; Snippets liefern
// nichts.
func importFiles(arg, dir string) []string {
	if !strings.ContainsAny(arg, "/.*") {
		return nil
	}
	p := arg
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	if strings.ContainsAny(p, "*?[") {
		m, _ := filepath.Glob(p)
		sort.Strings(m)
		return m
	}
	return []string{p}
}

func (s *Service) readImported(l *layout, f string, depth int, seen map[string]bool, state map[string]string) {
	if depth > 4 || seen[f] {
		return
	}
	seen[f] = true
	b, err := os.ReadFile(f)
	if err != nil {
		return
	}
	segs, err := caddy.Segments(b)
	if err != nil {
		return
	}
	kind := KindImported
	switch {
	case filepath.Dir(f) == filepath.Join(l.dir, SitesDir):
		kind = KindOwn
	case filepath.Base(f) == "hostdeck.caddy" && filepath.Dir(f) == l.dir:
		kind = KindLeitstand
	}
	for _, sg := range segs {
		switch sg.Kind {
		case caddy.SegSite:
			l.sites = append(l.sites, s.site(sg, f, kind, state))
		case caddy.SegImport:
			for _, g := range importFiles(sg.Args[1], filepath.Dir(f)) {
				s.readImported(l, g, depth+1, seen, state)
			}
		}
	}
}

func (s *Service) site(sg caddy.Segment, file, kind string, state map[string]string) Site {
	st := Site{Addrs: sg.Addrs, File: file, Kind: kind, Upstreams: []string{}, text: sg.Text}
	for _, a := range sg.Addrs {
		if h := caddy.HostOf(a); h != "" && !contains(st.Hosts, h) {
			st.Hosts = append(st.Hosts, h)
		}
	}
	proxies := 0
	for _, d := range caddy.Directives(sg.Text) {
		if d.Words[0] != "reverse_proxy" {
			continue
		}
		proxies++
		ups := upstreamsOf(d.Words[1:])
		st.Upstreams = append(st.Upstreams, ups...)
		if a := d.Words[1:]; len(a) > 0 && !strings.HasPrefix(a[0], "@") && !strings.HasPrefix(a[0], "/") {
			st.Main = append(st.Main, ups...)
		}
		if d.Depth == 1 && len(ups) == 1 && len(d.Words) == 2 {
			st.Simple = true
		}
	}
	if proxies != 1 || len(st.Upstreams) != 1 {
		st.Simple = false
	}
	if kind == KindOwn || kind == KindOff {
		if b, err := os.ReadFile(file); err == nil {
			st.Hand = state[file] != sum(b) && state[offTwin(file)] != sum(b)
		}
	}
	if kind == KindImported && s.ProjectOf != nil {
		st.Project = s.ProjectOf(file)
	}
	return st
}

// offTwin ist für sites/x.caddy die Datei sites/aus/x.caddy und umgekehrt;
// beim Ab- und Anschalten zieht die Datei um, die Prüfsumme bleibt.
func offTwin(p string) string {
	dir, base := filepath.Dir(p), filepath.Base(p)
	if filepath.Base(dir) == OffDir {
		return filepath.Join(filepath.Dir(dir), base)
	}
	return filepath.Join(dir, OffDir, base)
}

// upstreamsOf liest die Ziele eines reverse_proxy, ohne Matcher.
func upstreamsOf(args []string) []string {
	var out []string
	for i, a := range args {
		if i == 0 && (strings.HasPrefix(a, "@") || strings.HasPrefix(a, "/") || a == "*") {
			continue
		}
		out = append(out, a)
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Overview liefert den Stand sofort. Die Live-Prüfung (Antwort,
// Zertifikat, Ziel) läuft im Hintergrund; Pending sagt, dass sie noch
// läuft.
func (s *Service) Overview(ctx context.Context) Overview {
	l, err := s.read(ctx)
	if err != nil {
		if errors.Is(err, caddy.ErrNotFound) || errors.Is(err, ErrNoCaddy) {
			return Overview{Sites: []Site{}}
		}
		return Overview{Found: true, Problem: err.Error(), Sites: []Site{}}
	}
	ov := Overview{Found: true, Config: l.c.Config, Ordered: ordered(l), HTTP3: l.http3, Sites: l.sites}
	if ov.Sites == nil {
		ov.Sites = []Site{}
	}
	s.mu.Lock()
	// Von Hand geändert (nicht über HostDeck): Die Überwachung soll gleich neu
	// prüfen, sonst steht die Warnung einer entfernten Domain bis zu fünf
	// Minuten weiter da, obwohl sie hier schon fehlt.
	sig := hostSig(l.sites)
	moved := s.hostsSeen && sig != s.hosts
	s.hosts, s.hostsSeen = sig, true
	if moved && s.Changed != nil {
		defer s.Changed()
	}
	stale := s.live == nil || s.now().Sub(s.liveAt) > time.Minute
	if stale && !s.running {
		s.startLive(l.sites)
	}
	ov.Pending = s.running
	ov.Checked = s.liveAt
	for i := range ov.Sites {
		if len(ov.Sites[i].Hosts) == 0 || ov.Sites[i].Kind == KindOff {
			continue
		}
		s.attach(&ov.Sites[i])
	}
	s.mu.Unlock()
	return ov
}

// ordered: Das Caddyfile bindet sites/*.caddy ein und hat keine eigenen
// Seiten mehr.
func ordered(l *layout) bool {
	imp := false
	for _, sg := range l.segs {
		switch {
		case sg.Kind == caddy.SegSite:
			return false
		case sg.Kind == caddy.SegImport && sg.Args[1] == SitesGlob:
			imp = true
		}
	}
	return imp
}

// hostSig fasst zusammen, welche Domains mit welchem Ziel geprüft werden
// (wie in startLive).
func hostSig(sites []Site) string {
	var out []string
	for _, st := range sites {
		if len(st.Hosts) == 0 || st.Kind == KindOff {
			continue
		}
		for _, h := range st.Hosts {
			if !strings.HasPrefix(h, "*") {
				out = append(out, h+">"+strings.Join(st.Upstreams, ","))
			}
		}
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// startLive prüft alle Domains im Hintergrund. Aufrufer hält s.mu.
func (s *Service) startLive(sites []Site) {
	s.running = true
	s.done = make(chan struct{})
	type job struct {
		host string
		ups  []string
	}
	var jobs []job
	for _, st := range sites {
		if len(st.Hosts) == 0 || st.Kind == KindOff {
			continue
		}
		for _, h := range st.Hosts {
			if !strings.HasPrefix(h, "*") {
				jobs = append(jobs, job{h, st.Upstreams})
			}
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		out := map[string]Live{}
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)
		for _, j := range jobs {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				lv := s.check(ctx, j.host, j.ups)
				mu.Lock()
				out[j.host] = lv
				mu.Unlock()
			}()
		}
		wg.Wait()
		s.mu.Lock()
		s.live, s.liveAt, s.running = out, s.now(), false
		close(s.done)
		s.mu.Unlock()
	}()
}

func (s *Service) check(ctx context.Context, host string, ups []string) Live {
	var lv Live
	if s.Probe != nil {
		t0 := time.Now()
		a := s.Probe(ctx, host)
		lv.Took = max(1, time.Since(t0).Milliseconds())
		lv.Answer, lv.OK = a.String(), a.Err == "" && a.Status > 0 && a.Status < 500
	}
	if s.Cert != nil {
		ci := s.Cert(ctx, host)
		if ci.Err != "" {
			lv.CertErr = ci.Err
		} else if !ci.Until.IsZero() {
			u := ci.Until
			lv.CertUntil = &u
		}
	}
	if s.Listening != nil && len(ups) > 0 {
		any := false
		for _, u := range ups {
			if s.Listening(ctx, u) {
				any = true
				break
			}
		}
		lv.Listening = &any
	}
	return lv
}

// CheckNow prüft alle Domains sofort und wartet darauf (Überwachung, alle
// 5 Minuten). Das Ergebnis gilt auch für die Seite Domains.
func (s *Service) CheckNow(ctx context.Context) ([]Site, error) {
	l, err := s.read(ctx)
	if err != nil {
		if errors.Is(err, caddy.ErrNotFound) || errors.Is(err, ErrNoCaddy) {
			return nil, nil
		}
		return nil, err
	}
	s.mu.Lock()
	s.hosts, s.hostsSeen = hostSig(l.sites), true
	if !s.running {
		s.startLive(l.sites)
	}
	done := s.done
	s.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Site, 0, len(l.sites))
	for _, st := range l.sites {
		if len(st.Hosts) == 0 || st.Kind == KindOff {
			continue
		}
		if s.attach(&st) {
			out = append(out, st)
		}
	}
	return out, nil
}

// attach setzt die letzte Prüfung je Domain des Blocks; Live ist die der
// ersten. Aufrufer hält s.mu.
func (s *Service) attach(st *Site) bool {
	any := false
	for _, h := range st.Hosts {
		lv, ok := s.live[h]
		if !ok {
			continue
		}
		if st.Each == nil {
			st.Each = map[string]Live{}
		}
		st.Each[h] = lv
		if h == st.Hosts[0] {
			lv := lv
			st.Live = &lv
		}
		any = true
	}
	return any
}

// Refresh verwirft die Live-Werte, etwa nach einer Änderung.
func (s *Service) Refresh() {
	s.mu.Lock()
	s.live = nil
	s.mu.Unlock()
}

// changed verwirft die Live-Werte nach einer Änderung an Caddy und sagt es
// weiter (Changed).
func (s *Service) changed() {
	s.Refresh()
	s.mu.Lock()
	s.hostsSeen = false // schon weitergesagt
	s.mu.Unlock()
	if s.Changed != nil {
		s.Changed()
	}
}

// WaitLive wartet, bis die laufende Live-Prüfung fertig ist (für Tests).
func (s *Service) WaitLive() {
	s.mu.Lock()
	d := s.done
	s.mu.Unlock()
	if d != nil {
		<-d
	}
}

// Zustand: Prüfsummen der Dateien, die HostDeck geschrieben hat.
type stateFile struct {
	Files map[string]string `json:"dateien"`
}

func (s *Service) loadState() map[string]string {
	st := stateFile{}
	if b, err := os.ReadFile(s.StateFile); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.Files == nil {
		st.Files = map[string]string{}
	}
	return st.Files
}

// mkdirCaddy legt einen Ordner an, den Caddy lesen kann. Der Dienst von HostDeck
// läuft mit UMask 0077; os.Mkdir allein ergäbe 0700, Caddy (eigener
// Benutzer) sähe den Ordner leer, und import sites/*.caddy fände nichts.
func mkdirCaddy(d string) error {
	if err := os.Mkdir(d, 0o755); err != nil {
		return err
	}
	return os.Chmod(d, 0o755)
}

// closedDirs nennt die vorhandenen Ordner aus ds, die außer ihrem Besitzer
// niemand lesen kann, etwa ein sites/ mit 0700 aus einem Lauf vor dem Fix in
// mkdirCaddy. Caddy (eigener Benutzer) sähe sie leer.
func closedDirs(ds ...string) []string {
	var out []string
	for _, d := range ds {
		fi, err := os.Stat(d)
		if err == nil && fi.IsDir() && fi.Mode().Perm()&0o077 == 0 {
			out = append(out, d)
		}
	}
	return out
}

// openStep beschreibt in der Vorschau, dass HostDeck die Rechte von ds richtet.
func openStep(ds []string) string {
	return "Rechte: " + strings.Join(ds, ", ") + " kann nur root lesen; HostDeck setzt " + plural(len(ds), "ihn", "sie") + " auf 0755, damit Caddy die Dateien findet."
}

// openForCaddy setzt die Ordner ds auf 0755. undo stellt die alten Rechte
// wieder her; scheitert es mittendrin, ist schon alles zurückgestellt.
func openForCaddy(ds []string) (undo func(), err error) {
	type was struct {
		d string
		m os.FileMode
	}
	var done []was
	undo = func() {
		for i := len(done) - 1; i >= 0; i-- {
			_ = os.Chmod(done[i].d, done[i].m)
		}
	}
	for _, d := range ds {
		fi, err := os.Stat(d)
		if err != nil {
			undo()
			return nil, err
		}
		if err := os.Chmod(d, 0o755); err != nil {
			undo()
			return nil, err
		}
		done = append(done, was{d, fi.Mode().Perm()})
	}
	return undo, nil
}

// noteSums merkt die Prüfsummen der Dateien, die HostDeck gleich schreibt, schon
// vor dem Schreiben: Sonst gälten sie während der langen Prüfung danach als
// „von Hand geändert“. undo stellt den alten Stand wieder her, wenn das
// Schreiben scheitert und Caddy zurückgelegt wird.
func (s *Service) noteSums(sums map[string]string) (undo func(), err error) {
	old := s.loadState()
	merged := make(map[string]string, len(old)+len(sums))
	for k, v := range old {
		merged[k] = v
	}
	for k, v := range sums {
		merged[k] = v
	}
	if err := s.saveState(merged); err != nil {
		return nil, err
	}
	return func() { _ = s.saveState(old) }, nil
}

func (s *Service) saveState(files map[string]string) error {
	b, err := json.MarshalIndent(stateFile{Files: files}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.StateFile), 0o700); err != nil {
		return err
	}
	tmp := s.StateFile + ".neu"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.StateFile)
}

// LocalListening fragt, ob am Ziel eines reverse_proxy etwas lauscht.
func LocalListening(ctx context.Context, upstream string) bool {
	u := upstream
	for _, p := range []string{"http://", "https://", "h2c://"} {
		u = strings.TrimPrefix(u, p)
	}
	if rest, ok := strings.CutPrefix(u, "unix/"); ok {
		fi, err := os.Stat(rest)
		return err == nil && fi.Mode()&fs.ModeSocket != 0
	}
	if strings.ContainsAny(u, "{}") {
		return true // Platzhalter: nicht prüfbar, nicht als tot zählen
	}
	host, port, err := net.SplitHostPort(u)
	if err != nil {
		host, port = u, "80"
		if strings.HasPrefix(upstream, "https://") {
			port = "443"
		}
	}
	if host == "" {
		host = "127.0.0.1"
	}
	d := net.Dialer{Timeout: 1500 * time.Millisecond}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// LocalCert holt das Zertifikat, das Caddy auf diesem Server für host
// zeigt, und prüft es wie ein Browser.
func LocalCert(ctx context.Context, host string) CertInfo {
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: &tls.Config{ServerName: host, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	conn, err := d.DialContext(ctx, "tcp", "127.0.0.1:443")
	if err != nil {
		return CertInfo{Err: shortErr(err)}
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return CertInfo{Err: "kein Zertifikat"}
	}
	pool := x509.NewCertPool()
	for _, c := range certs[1:] {
		pool.AddCert(c)
	}
	ci := CertInfo{Until: certs[0].NotAfter}
	if _, err := certs[0].Verify(x509.VerifyOptions{DNSName: host, Intermediates: pool}); err != nil {
		ci.Err = "ungültig: " + shortErr(err)
	}
	return ci
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && len(s)-i > 8 {
		s = s[i+2:]
	}
	if len(s) > 160 {
		s = s[:160] + " …"
	}
	return s
}
