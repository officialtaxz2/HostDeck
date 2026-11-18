package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/dnscheck"
	"github.com/officialtaxz2/hostdeck/internal/outdir"
)

// lineWatch reicht die Ausgabe durch und merkt sich, ob needle darin
// vorkam, auch wenn es über zwei Schreibvorgänge verteilt ist.
type lineWatch struct {
	w      io.Writer
	needle []byte
	tail   []byte
	seen   bool
	line   []byte   // angefangene Zeile
	errs   []string // Zeilen mit ✗, höchstens maxErrLines
}

// maxErrLines: So viele Fehlerzeilen behält die Generalprobe für den Lotsen.
const maxErrLines = 5

func (l *lineWatch) Write(p []byte) (int, error) {
	for _, c := range p {
		if c != '\n' {
			if len(l.line) < 400 {
				l.line = append(l.line, c)
			}
			continue
		}
		if t := strings.TrimSpace(string(l.line)); strings.HasPrefix(t, "✗ ") && len(l.errs) < maxErrLines {
			l.errs = append(l.errs, strings.TrimPrefix(t, "✗ "))
		}
		l.line = l.line[:0]
	}
	if !l.seen {
		buf := append(append([]byte{}, l.tail...), p...)
		if bytes.Contains(buf, l.needle) {
			l.seen = true
		}
		if n := len(l.needle); len(buf) > n {
			buf = buf[len(buf)-n:]
		}
		l.tail = buf
	}
	return l.w.Write(p)
}

// siteResult ist eine Seite, abgefragt hier und über die neue Adresse.
type siteResult struct {
	host        string
	here, there caddy.Answer
}

// checkSites fragt jede Seite, die Caddy hier bedient, über die Adresse
// des neuen Servers ab: mit dem Hostnamen, als käme DNS schon dort an, und
// mit Prüfung des Zertifikats wie im Browser. Verglichen wird mit der
// Antwort hier.
// Ist base gesetzt, gilt es statt der Antworten hier (am Umzugstag sind
// die Projekte hier schon aus). Es liefert, wie viele von wie vielen
// Seiten drüben so gut antworten; total -1: nicht geprüft.
func (m *serverMoves) checkSites(ctx context.Context, t dienst.MoveTarget, base map[string]caddy.Answer, w io.Writer) (good, total int) {
	if m.Sites == nil {
		return 0, -1
	}
	hosts, err := m.Sites(ctx)
	fmt.Fprintf(w, "\nSeiten über die neue Adresse\n")
	if err != nil {
		fmt.Fprintf(w, "  ! Die Seiten von Caddy hier sind nicht lesbar (%v); HostDeck prüft nichts.\n", err)
		return 0, -1
	}
	if len(hosts) == 0 {
		fmt.Fprintf(w, "  Caddy bedient hier keine festen Domains.\n")
		return 0, 0
	}
	ip, err := m.targetIP(ctx, t)
	if err != nil {
		fmt.Fprintf(w, "  ! %v.\n", strings.ToUpper(err.Error()[:1])+err.Error()[1:])
		return 0, -1
	}
	here, there := m.siteHere(), m.SiteThere
	if base != nil {
		here = func(_ context.Context, h string) caddy.Answer { return base[h] }
	}
	if there == nil {
		there = caddy.AddrProber(ip, 443)
	}
	fmt.Fprintf(w, "  %d Domains über %s, mit Zertifikatsprüfung wie im Browser:\n", len(hosts), ip)
	res := make([]siteResult, len(hosts))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, h := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := siteResult{host: h, here: here(ctx, h), there: there(ctx, h)}
			// Caddy kann kurz nach dem Start noch Zertifikate laden.
			for try := 0; caddy.Worse(r.here, r.there) && try < 2 && ctx.Err() == nil; try++ {
				sleepFor(ctx, m.siteRetry())
				r.there = there(ctx, h)
			}
			res[i] = r
		}()
	}
	wg.Wait()
	sort.Slice(res, func(i, j int) bool { return res[i].host < res[j].host })
	own := ""
	if m.Own != nil {
		own = m.Own()
	}
	good = 0
	for _, r := range res {
		usable := r.there.Err == "" && r.there.Status > 0 && r.there.Status < 500
		switch {
		case r.there.Err != "":
			fmt.Fprintf(w, "  ✗ %s: %s\n", r.host, r.there.Err)
		case !caddy.Worse(r.here, r.there) && usable:
			good++
			fmt.Fprintf(w, "  ✓ %s: Zertifikat gültig, Status %d wie hier\n", r.host, r.there.Status)
		case !caddy.Worse(r.here, r.there):
			good++
			fmt.Fprintf(w, "  ✓ %s: Zertifikat gültig, Status %d (hier %s)\n", r.host, r.there.Status, r.here)
		default:
			fmt.Fprintf(w, "  ! %s: Zertifikat gültig, aber Status %d (hier %s)\n", r.host, r.there.Status, r.here)
		}
		if r.host == own && caddy.Worse(r.here, r.there) {
			fmt.Fprintf(w, "    Das ist der Leitstand selbst; was mit ihm ist, steht oben unter „Leitstand auf den neuen Server“.\n")
		}
	}
	how := "wie hier"
	if base != nil {
		how = "wie hier vor dem letzten Lauf"
	}
	fmt.Fprintf(w, "  %d von %d Seiten antworten über die neue Adresse %s.\n", good, len(res), how)
	return good, len(res)
}

func (m *serverMoves) siteHere() caddy.Prober {
	if m.SiteHere != nil {
		return m.SiteHere
	}
	return caddy.LocalProber(443, nil)
}

// siteBaseline fragt jede Seite hier ab, bevor der letzte Lauf die
// Projekte stoppt; nil: keine Seiten.
func (m *serverMoves) siteBaseline(ctx context.Context) map[string]caddy.Answer {
	if m.Sites == nil {
		return nil
	}
	hosts, err := m.Sites(ctx)
	if err != nil || len(hosts) == 0 {
		return nil
	}
	here := m.siteHere()
	out := make(map[string]caddy.Answer, len(hosts))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, h := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			a := here(ctx, h)
			mu.Lock()
			out[h] = a
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// checkTestDomain fragt die Testadresse des neuen Leitstands von hier aus
// ab, über das normale DNS wie ein Browser.
func (m *serverMoves) checkTestDomain(ctx context.Context, t dienst.MoveTarget, test string, w io.Writer) {
	ip, err := m.targetIP(ctx, t)
	if err != nil {
		return
	}
	there := m.SiteThere
	if there == nil {
		there = caddy.AddrProber(ip, 443)
	}
	a := there(ctx, test)
	for try := 0; (a.Err != "" || a.Status != 200) && try < 2 && ctx.Err() == nil; try++ {
		sleepFor(ctx, m.siteRetry())
		a = there(ctx, test)
	}
	fmt.Fprintf(w, "\nLeitstand unter der Testadresse\n")
	switch {
	case a.Err != "":
		fmt.Fprintf(w, "  ✗ https://%s: %s\n", test, a.Err)
	case a.Status != 200:
		fmt.Fprintf(w, "  ! https://%s: Zertifikat gültig, aber Status %d\n", test, a.Status)
	default:
		fmt.Fprintf(w, "  ✓ https://%s antwortet von außen, Zertifikat gültig. Anmelden mit deinem Leitstand-Passwort.\n", test)
	}
}

func (m *serverMoves) siteRetry() time.Duration {
	if m.SiteRetry > 0 {
		return m.SiteRetry
	}
	return 3 * time.Second
}

func sleepFor(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// DNS fragt für jede Domain, die Caddy hier bedient, den zuständigen
// Nameserver, wohin sie zeigt: hierher, auf den neuen Server (Ziel des
// letzten Probe-Umzugs) oder woandershin.
func (m *serverMoves) DNS(ctx context.Context) dienst.DNSReply {
	rep := dienst.DNSReply{Here: []string{}, Domains: []dienst.DNSDomain{}, Zones: []dienst.DNSZone{}, Suggest: []dienst.DNSDomain{}}
	local := m.LocalIPs
	if local == nil {
		local = localIPs
	}
	here := map[string]bool{}
	for _, ip := range local() {
		here[ip.String()] = true
		rep.Here = append(rep.Here, ip.String())
	}
	sort.Strings(rep.Here)
	fresh := map[string]bool{}
	if t := m.last(); t != nil {
		if ip := net.ParseIP(t.Host); ip != nil {
			fresh[ip.String()] = true
			rep.Target = ip.String()
		} else {
			rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			addrs, _ := net.DefaultResolver.LookupIPAddr(rctx, t.Host)
			cancel()
			for _, a := range addrs {
				fresh[a.IP.String()] = true
			}
			rep.Target = t.Host
		}
	}
	var hosts []string
	switch {
	case m.Sites == nil:
		rep.Problem = "Ohne Caddy kennt HostDeck hier keine Domains; trage sie unten von Hand ein."
	default:
		var err error
		if hosts, err = m.Sites(ctx); err != nil {
			rep.Problem = "Die Domains von Caddy sind nicht lesbar: " + err.Error()
			hosts = nil
		}
	}
	manual := map[string]bool{}
	for _, h := range m.extraDomains() {
		if !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
			manual[h] = true
		}
	}
	rep.Extra = m.extraDomains()
	res := m.DNSResolver
	if res == nil {
		res = &dnscheck.Resolver{}
	}
	for ip := range fresh {
		rep.TargetIPs = append(rep.TargetIPs, ip)
	}
	sort.Strings(rep.TargetIPs)
	check := func(h string) dienst.DNSDomain {
		a := res.Lookup(ctx, h)
		d := dienst.DNSDomain{Name: h, Server: a.Server, CNAME: a.CNAME, Err: a.Err, Records: []dienst.DNSRecord{}, Manual: manual[h]}
		for _, r := range a.Records {
			where := dienst.DNSOther
			switch ip := r.IP.String(); {
			case fresh[ip]:
				where = dienst.DNSNew
			case here[ip]:
				where = dienst.DNSHere
			}
			d.Records = append(d.Records, dienst.DNSRecord{Type: r.Type, IP: r.IP.String(), TTL: r.TTL, Where: where})
		}
		d.State = dienst.DNSStateOf(d.Records)
		return d
	}
	all := func(names []string) []dienst.DNSDomain {
		out := make([]dienst.DNSDomain, len(names))
		var wg sync.WaitGroup
		sem := make(chan struct{}, 6)
		for i, h := range names {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				out[i] = check(h)
			}()
		}
		wg.Wait()
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out
	}
	rep.Domains = all(hosts)
	if m.Own != nil {
		rep.Own = m.Own()
	}
	known := map[string]bool{}
	for _, h := range hosts {
		known[h] = true
	}
	if td := testDomain(rep.Own); td != "" {
		t := check(td)
		rep.Test = &t
		known[td] = true
	}
	rep.Zones, rep.Suggest = m.dnsHints(ctx, res, hosts, known, all)
	return rep
}

// commonSRV sind übliche SRV-Einträge, deren Ziele beim Umzug oft
// vergessen werden (Spielserver, Sprachserver, Mail, Chat).
var commonSRV = [][2]string{
	{"minecraft", "tcp"}, {"ts3", "udp"}, {"xmpp-client", "tcp"}, {"xmpp-server", "tcp"},
	{"sip", "udp"}, {"sip", "tcp"}, {"autodiscover", "tcp"}, {"submission", "tcp"}, {"imaps", "tcp"},
}

// dnsHints sucht die Zonen der Domains samt Nameservern und schlägt
// Namen vor, die noch hierher zeigen, aber in keiner Liste stehen.
// Höchstens 10 Zonen, 10 Sekunden.
func (m *serverMoves) dnsHints(ctx context.Context, res *dnscheck.Resolver, hosts []string, known map[string]bool, all func([]string) []dienst.DNSDomain) ([]dienst.DNSZone, []dienst.DNSDomain) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	zones := []dienst.DNSZone{}
	byZone := map[string]int{}
	for _, h := range hosts {
		z, ns, err := res.Zone(ctx, h)
		if err != nil {
			continue
		}
		if i, ok := byZone[z]; ok {
			zones[i].Domains++
			continue
		}
		if len(zones) >= 10 {
			continue
		}
		byZone[z] = len(zones)
		sort.Strings(ns)
		zones = append(zones, dienst.DNSZone{Name: z, Servers: ns, Domains: 1})
	}
	names := m.DNSNames
	if names == nil {
		names = systemDNSNames
	}
	var cand []string
	add := func(n string) {
		n = strings.ToLower(strings.TrimSuffix(n, "."))
		if n == "" || known[n] || !dienst.ValidDomain(n) || slices.Contains(cand, n) {
			return
		}
		cand = append(cand, n)
	}
	for _, z := range zones {
		add(z.Name)
		add("www." + z.Name)
		for _, n := range names(ctx, z.Name) {
			add(n)
		}
	}
	out := []dienst.DNSDomain{}
	for _, d := range all(cand) {
		for _, r := range d.Records {
			if r.Where == dienst.DNSHere {
				out = append(out, d)
				break
			}
		}
	}
	return zones, out
}

// systemDNSNames liefert die Ziele der MX-Einträge und der üblichen
// SRV-Einträge einer Zone über den normalen DNS-Weg.
func systemDNSNames(ctx context.Context, zone string) []string {
	var out []string
	if mx, err := net.DefaultResolver.LookupMX(ctx, zone); err == nil {
		for _, r := range mx {
			out = append(out, r.Host)
		}
	}
	for _, s := range commonSRV {
		if _, srv, err := net.DefaultResolver.LookupSRV(ctx, s[0], s[1], zone); err == nil {
			for _, r := range srv {
				out = append(out, r.Target)
			}
		}
	}
	return out
}

// dnsExtraFile hält Domains, die nicht in Caddy stehen (Spielserver, Mail,
// VPN), im Ordner des Umzugs. Die DNS-Liste prüft sie mit.
const dnsExtraFile = "dns-von-hand.json"

const maxExtraDomains = 50

func (m *serverMoves) extraDomains() []string {
	var v struct {
		Domains []string `json:"domains"`
	}
	b, err := os.ReadFile(filepath.Join(m.Dir, dnsExtraFile))
	if err != nil || json.Unmarshal(b, &v) != nil {
		return []string{}
	}
	return v.Domains
}

// SetExtraDomains speichert die Domains von Hand (ersetzt die Liste).
func (m *serverMoves) SetExtraDomains(list []string) error {
	if len(list) > maxExtraDomains {
		return fmt.Errorf("Höchstens %d Domains von Hand.", maxExtraDomains)
	}
	out := []string{}
	for _, d := range list {
		d = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(d), "."))
		if d == "" || slices.Contains(out, d) {
			continue
		}
		if !dienst.ValidDomain(d) {
			return fmt.Errorf("„%s“ ist keine Domain, etwa mc.example.org.", clipText(d, 80))
		}
		out = append(out, d)
	}
	sort.Strings(out)
	b, _ := json.MarshalIndent(map[string][]string{"domains": out}, "", "  ")
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return err
	}
	return outdir.WritePrivate(filepath.Join(m.Dir, dnsExtraFile), append(b, '\n'))
}

func clipText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
