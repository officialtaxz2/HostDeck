package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/ort"
	"github.com/officialtaxz2/hostdeck/internal/outdir"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
)

// Freischalten: Der Leitstand wird über Caddy unter einer eigenen Adresse
// erreichbar. HostDeck legt dafür eine eigene Datei neben das Caddyfile, bindet
// sie mit einer import-Zeile ein und lässt systemd einen Socket anlegen, an
// den nur Caddy herankommt. Jede Änderung an Caddy läuft über
// caddy.Applier: sichern, prüfen, neu laden, vergleichen, sonst zurück.

// accessState ist der Zugang von außen, wie HostDeck ihn eingerichtet hat
// (/var/lib/hostdeck/freischaltung.json).
type accessState struct {
	Domain    string    `json:"adresse"`
	Since     time.Time `json:"seit"`
	Group     string    `json:"gruppe"`                        // Gruppe von Caddy; darf an den Socket
	Caddyfile string    `json:"caddyfile"`                     // das geänderte Caddyfile
	Site      string    `json:"datei"`                         // eigene Datei, etwa /etc/caddy/hostdeck.caddy
	Import    bool      `json:"import"`                        // HostDeck hat die import-Zeile angelegt
	Newline   bool      `json:"zeilenende_ergaenzt,omitempty"` // dabei ein Zeilenende angehängt
	// Extra sind weitere Adressen des Leitstands, etwa die Testadresse
	// einer Probe-Kopie auf dem neuen Server (hostdeck-neu.example.de).
	Extra []string `json:"weitere,omitempty"`
}

func loadAccess(dataDir string) (*accessState, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, ort.AccessFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st accessState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	if validDomain(st.Domain) != nil || !validGroup(st.Group) || st.Site == "" {
		return nil, fmt.Errorf("%s ist unvollständig", ort.AccessFile)
	}
	for _, e := range st.Extra {
		if validDomain(e) != nil {
			return nil, fmt.Errorf("%s: ungültige weitere Adresse", ort.AccessFile)
		}
	}
	return &st, nil
}

func saveAccess(dataDir string, st *accessState) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return outdir.WritePrivate(filepath.Join(dataDir, ort.AccessFile), append(b, '\n'))
}

func removeAccess(dataDir string) error {
	err := os.Remove(filepath.Join(dataDir, ort.AccessFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// validGroup: Der Gruppenname landet in einer systemd-Unit.
func validGroup(g string) bool {
	if g == "" || len(g) > 32 {
		return false
	}
	for _, r := range g {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// normDomain macht aus einer Eingabe wie „https://HOSTDECK.example.de/“ einen
// Hostnamen.
func normDomain(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, p := range []string{"https://", "http://"} {
		s = strings.TrimPrefix(s, p)
	}
	s = strings.TrimSuffix(s, "/")
	return strings.TrimSuffix(s, ".")
}

// validDomain prüft einen Hostnamen. Die Texte erscheinen im Leitstand.
func validDomain(s string) error {
	switch {
	case s == "":
		return errors.New("Bitte eine Adresse eingeben, etwa hostdeck.example.de.")
	case net.ParseIP(s) != nil:
		return errors.New("Bitte einen Namen statt einer IP-Adresse eingeben; für HTTPS braucht Caddy einen Namen.")
	case len(s) > 253:
		return errors.New("Die Adresse ist zu lang.")
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return errors.New("Bitte die ganze Adresse eingeben, mit Punkt, etwa hostdeck.example.de.")
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return fmt.Errorf("„%s“ ist keine gültige Adresse.", s)
		}
		for _, r := range l {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return fmt.Errorf("„%s“ ist keine gültige Adresse. Erlaubt sind a bis z, Ziffern, Bindestrich und Punkt.", s)
			}
		}
	}
	if strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return fmt.Errorf("„%s“ ist keine gültige Adresse.", s)
	}
	return nil
}

// siteBlock ist die eigene Datei von HostDeck für Caddy.
//
// Alt-Svc clear: Caddy bietet Browsern von sich aus HTTP/3 an, das über
// UDP läuft. Viele Anbieter von vServern drosseln UDP stark; ein
// Backup-Download kroch so mit 300 KB/s, über TCP lief er mit voller
// Leitung. Der Leitstand bleibt darum bei HTTP/1.1 und HTTP/2, und Browser
// vergessen ein früher gemerktes HTTP/3 für diese Adresse.
func siteBlock(domain string, extra ...string) []byte {
	return []byte(`# Leitstand von HostDeck. Angelegt beim Freischalten, gelöscht beim Zurücknehmen.
` + strings.Join(append([]string{domain}, extra...), ", ") + ` {
	# Kein HTTP/3 (UDP): wird oft gedrosselt, Downloads kriechen dann.
	header Alt-Svc clear
	reverse_proxy unix/` + ort.CaddySocket + `
}
`)
}

// ownSiteHosts liest die Adressen aus der eigenen Datei von HostDeck, falls sie
// schon dasteht und von HostDeck stammt (erste Zeile wie in siteBlock).
func ownSiteHosts(path string) map[string]bool {
	b, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(b, []byte("# Leitstand von HostDeck.")) {
		return nil
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		h, ok := strings.CutSuffix(strings.TrimSpace(line), " {")
		if !ok || strings.HasPrefix(line, "\t") || strings.HasPrefix(line, " ") {
			continue
		}
		for _, n := range strings.Split(h, ",") {
			if n = normDomain(strings.TrimSpace(n)); n != "" {
				out[n] = true
			}
		}
	}
	return out
}

// brokenBlock ist absichtlich kaputt; damit probt HostDeck, ob caddy validate
// Fehler erkennt.
func brokenBlock(domain string) []byte {
	return []byte(`# Probe von HostDeck: absichtlich kaputt. caddy validate muss das ablehnen.
` + domain + ` {
	hostdeck_probe_absichtlich_kaputt
}
`)
}

// Schritte, die nicht von caddy.Applier kommen.
const (
	stepAddress   = "Adresse prüfen"
	stepGateOpen  = "Eingang für Caddy anlegen"
	stepGateClose = "Eingang für Caddy schließen"
	stepSave      = "Freischaltung merken"
)

// publisher schaltet frei und nimmt zurück. Felder für Tests.
type publisher struct {
	In         *installer
	Find       func(ctx context.Context) (*caddy.Caddy, error)
	Resolve    func(ctx context.Context, host string) ([]net.IP, error)
	Local      func() []net.IP
	Probe      caddy.Prober
	Site       func(ctx context.Context, domain string) error // ein Versuch: die Anmeldeseite kommt über Caddy
	Web        func(ctx context.Context, domain string) error // hostdeck web antwortet am Eingang für Caddy
	Wait       time.Duration                                  // so lange auf das Zertifikat warten
	Every      time.Duration
	Retries    int
	RetryDelay time.Duration
	Now        func() time.Time

	mu      sync.Mutex
	cached  dienst.AccessState
	cacheAt time.Time
}

func systemPublisher(in *installer) *publisher {
	return &publisher{
		In:         in,
		Find:       func(ctx context.Context) (*caddy.Caddy, error) { return caddy.Find(ctx, caddy.Exec) },
		Resolve:    resolveIPs,
		Local:      localIPs,
		Probe:      caddy.LocalProber(443, nil),
		Site:       siteAnswers,
		Web:        webAnswers,
		Wait:       2 * time.Minute,
		Every:      3 * time.Second,
		Retries:    3,
		RetryDelay: 3 * time.Second,
		Now:        time.Now,
	}
}

func resolveIPs(ctx context.Context, host string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var out []net.IP
	for _, a := range addrs {
		out = append(out, a.IP)
	}
	return out, nil
}

func localIPs() []net.IP {
	addrs, _ := net.InterfaceAddrs()
	var out []net.IP
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.IsGlobalUnicast() {
			out = append(out, n.IP)
		}
	}
	return out
}

const leitstandTitle = "<title>HostDeck Leitstand</title>"

func leitstandPage(resp *http.Response) error {
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), leitstandTitle) {
		return fmt.Errorf("statt der Anmeldeseite kam Status %d", resp.StatusCode)
	}
	return nil
}

// siteAnswers fragt die neue Adresse über Caddy ab, mit echter Prüfung des
// Zertifikats.
func siteAnswers(ctx context.Context, domain string) error {
	resp, err := caddy.LocalGet(ctx, domain, "/", 443, nil)
	if err != nil {
		return err
	}
	return leitstandPage(resp)
}

// webAnswers fragt hostdeck web direkt am Socket für Caddy ab.
func webAnswers(ctx context.Context, domain string) error {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", ort.CaddySocket)
	}, DisableKeepAlives: true}
	cl := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	var last error
	for i := 0; i < 20; i++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+domain+"/", nil)
		resp, err := cl.Do(req)
		if err == nil {
			if last = leitstandPage(resp); last == nil {
				return nil
			}
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("hostdeck web antwortet am Eingang für Caddy nicht: %v", last)
}

func (p *publisher) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *publisher) applier(c *caddy.Caddy, step func(dienst.Step)) *caddy.Applier {
	return &caddy.Applier{
		Caddy: c, Probe: p.Probe, Retries: p.Retries, RetryDelay: p.RetryDelay,
		Event: func(e caddy.Event) { step(dienst.Step{Name: e.Step, State: e.State, Detail: e.Detail}) },
	}
}

// notFound erklärt, warum Freischalten hier nicht geht.
func notFound(err error) string {
	if errors.Is(err, caddy.ErrNotFound) {
		return "Auf diesem Server läuft kein Caddy als Dienst. Der Leitstand geht dann nur über den Notausgang (sudo hostdeck notausgang)."
	}
	return "Freischalten geht hier nicht: " + err.Error() + ". Der Leitstand geht weiter über den Notausgang."
}

// State: Stand für den Leitstand. Caddy wird höchstens alle 30 Sekunden
// neu befragt, weil die Seite während eines Auftrags oft fragt.
func (p *publisher) State(ctx context.Context) dienst.AccessState {
	cur, err := loadAccess(p.In.DataDir)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.now().Sub(p.cacheAt) > 30*time.Second || p.cacheAt.IsZero() {
		st := dienst.AccessState{}
		if c, err := p.Find(ctx); err != nil {
			st.Problem = notFound(err)
		} else {
			st.Caddy, st.Config = true, c.Config
			if hosts, err := c.Hosts(ctx); err == nil {
				st.Suggest = suggestDomain(hosts)
			}
		}
		p.cached, p.cacheAt = st, p.now()
	}
	st := p.cached
	if err != nil {
		st.Problem = "Die Freischaltung ist nicht lesbar: " + err.Error()
	}
	if cur != nil {
		since := cur.Since
		st.Domain, st.Since, st.Suggest = cur.Domain, &since, ""
	}
	return st
}

// Domain ist die freigeschaltete Adresse; liest nur die eigene Datei.
func (p *publisher) Domain() string {
	if st, err := loadAccess(p.In.DataDir); err == nil && st != nil {
		return st.Domain
	}
	return ""
}

func (p *publisher) forget() {
	p.mu.Lock()
	p.cacheAt = time.Time{}
	p.mu.Unlock()
}

// suggestDomain schlägt HostDeck.<häufigste Domain> vor, etwa hostdeck.example.net.
func suggestDomain(hosts []string) string {
	count := map[string]int{}
	taken := map[string]bool{}
	for _, h := range hosts {
		taken[h] = true
		l := strings.Split(h, ".")
		if len(l) >= 2 {
			count[strings.Join(l[len(l)-2:], ".")]++
		}
	}
	best := ""
	for b, n := range count {
		if n > count[best] || n == count[best] && b < best {
			best = b
		}
	}
	if best == "" {
		return ""
	}
	for _, pre := range []string{"hostdeck", "leitstand", "hostdeck-leitstand"} {
		if !taken[pre+"."+best] {
			return pre + "." + best
		}
	}
	return ""
}

// serverIPs sind die Adressen dieses Servers: die eigenen und die, auf die
// die anderen Seiten in Caddy zeigen (bei NAT sind das die öffentlichen).
func (p *publisher) serverIPs(ctx context.Context, hosts []string) (all []net.IP, hint string) {
	all = append(all, p.Local()...)
	seen := map[string]int{}
	for i, h := range hosts {
		if i >= 8 {
			break
		}
		ips, err := p.Resolve(ctx, h)
		if err != nil {
			continue
		}
		for _, ip := range ips {
			all = append(all, ip)
			if ip.To4() != nil {
				seen[ip.String()]++
			}
		}
	}
	for ip, n := range seen {
		if hint == "" || n > seen[hint] || n == seen[hint] && ip < hint {
			hint = ip
		}
	}
	if hint == "" {
		for _, ip := range all {
			if ip.To4() != nil {
				hint = ip.String()
				break
			}
		}
	}
	if hint == "" {
		hint = "die IP-Adresse dieses Servers"
	}
	return all, hint
}

func ipList(ips []net.IP) string {
	var s []string
	for _, ip := range ips {
		s = append(s, ip.String())
	}
	sort.Strings(s)
	return strings.Join(s, ", ")
}

// Check prüft eine Adresse und zeigt, was das Freischalten ändert.
func (p *publisher) Check(ctx context.Context, raw string) (dienst.AccessPlan, error) {
	plan, _, err := p.check(ctx, raw)
	return plan, err
}

func (p *publisher) check(ctx context.Context, raw string) (dienst.AccessPlan, *caddy.Caddy, error) {
	d := normDomain(raw)
	if err := validDomain(d); err != nil {
		return dienst.AccessPlan{}, nil, err
	}
	cur, err := loadAccess(p.In.DataDir)
	if err != nil {
		return dienst.AccessPlan{}, nil, fmt.Errorf("Die Freischaltung ist nicht lesbar: %v", err)
	}
	if cur != nil {
		if cur.Domain == d {
			return dienst.AccessPlan{}, nil, fmt.Errorf("Der Leitstand ist schon unter %s freigeschaltet.", d)
		}
		return dienst.AccessPlan{}, nil, fmt.Errorf("Der Leitstand ist schon unter %s freigeschaltet. Erst das zurücknehmen, dann %s freischalten.", cur.Domain, d)
	}
	c, err := p.Find(ctx)
	if err != nil {
		return dienst.AccessPlan{}, nil, errors.New(notFound(err))
	}
	if !validGroup(c.Group) {
		return dienst.AccessPlan{}, nil, fmt.Errorf("Die Gruppe von Caddy (%q) kann HostDeck nicht verwenden.", c.Group)
	}
	hosts, err := c.Hosts(ctx)
	if err != nil {
		return dienst.AccessPlan{}, nil, fmt.Errorf("Die Konfiguration von Caddy ist gerade nicht lesbar (%v). Bitte erst das klären; HostDeck ändert nichts.", err)
	}
	site := filepath.Join(filepath.Dir(c.Config), ort.CaddySite)
	// Die eigene Datei von HostDeck kann schon dastehen, ohne dass HostDeck von einer
	// Freischaltung weiß: nach dem Zurückspielen eines Backups auf einen neu
	// aufgesetzten Server (Labor S11). Unter ihrer Adresse antwortet dann
	// keine fremde Seite; HostDeck übernimmt sie.
	own := ownSiteHosts(site)
	for _, h := range hosts {
		if h == d && !own[d] {
			alt := suggestDomain(append(hosts, d))
			return dienst.AccessPlan{}, nil, fmt.Errorf("Unter %s antwortet schon eine andere Seite in Caddy. Bitte eine andere Adresse wählen, etwa %s.", d, alt)
		}
	}
	server, hint := p.serverIPs(ctx, hosts)
	ips, err := p.Resolve(ctx, d)
	if err != nil || len(ips) == 0 {
		return dienst.AccessPlan{}, nil, fmt.Errorf("Für %s gibt es noch keinen DNS-Eintrag. Lege bei deinem DNS-Anbieter einen A-Eintrag an, der auf %s zeigt, wie bei deinen anderen Subdomains. Neue Einträge brauchen oft ein paar Minuten.", d, hint)
	}
	// Jede Adresse muss hierher zeigen: Let's Encrypt fragt bei einem
	// AAAA-Eintrag meist über IPv6 nach, ein falscher Eintrag verhindert
	// dann das Zertifikat.
	var stray []net.IP
	for _, ip := range ips {
		ours := false
		for _, s := range server {
			if ip.Equal(s) {
				ours = true
			}
		}
		if !ours {
			stray = append(stray, ip)
		}
	}
	switch {
	case len(stray) == len(ips):
		return dienst.AccessPlan{}, nil, fmt.Errorf("%s zeigt auf %s, dieser Server hat aber %s. Bitte den DNS-Eintrag auf %s ändern und ein paar Minuten warten.", d, ipList(ips), hint, hint)
	case len(stray) > 0:
		return dienst.AccessPlan{}, nil, fmt.Errorf("%s zeigt auch auf %s, und das ist nicht dieser Server. Bitte diesen Eintrag beim DNS-Anbieter löschen oder auf diesen Server ändern, sonst bekommt Caddy womöglich kein Zertifikat.", d, ipList(stray))
	}
	plan := dienst.AccessPlan{Domain: d}
	for _, h := range hosts {
		if !own[h] {
			plan.Pages++
		}
	}
	for _, ip := range ips {
		plan.IPs = append(plan.IPs, ip.String())
	}
	sort.Strings(plan.IPs)
	if len(own) > 0 {
		plan.Files = append(plan.Files, dienst.PlanFile{Path: site, Text: string(siteBlock(d)), Note: "liegt schon da, etwa aus einem zurückgespielten Backup; HostDeck schreibt sie neu"})
	} else {
		plan.Files = append(plan.Files, dienst.PlanFile{Path: site, New: true, Text: string(siteBlock(d)), Note: "neue Datei nur für den Leitstand"})
	}
	cf, err := os.ReadFile(c.Config)
	if err != nil {
		return dienst.AccessPlan{}, nil, err
	}
	if caddy.Covered(cf, filepath.Dir(c.Config), site) {
		plan.Files = append(plan.Files, dienst.PlanFile{Path: c.Config, Note: "bindet " + ort.CaddySite + " schon ein und bleibt unverändert"})
	} else {
		plan.Files = append(plan.Files, dienst.PlanFile{Path: c.Config, Text: caddy.ImportComment + "\nimport " + site + "\n", Note: "diese zwei Zeilen kommen ans Ende, sonst ändert sich nichts"})
	}
	return plan, c, nil
}

// Publish schaltet frei. Geht ein Schritt schief, ist danach alles wie
// vorher.
func (p *publisher) Publish(ctx context.Context, raw string, step func(dienst.Step)) error {
	defer p.forget()
	emit := func(name, state, detail string) { step(dienst.Step{Name: name, State: state, Detail: detail}) }
	emit(stepAddress, caddy.StateRunning, "")
	if msg, err := p.recoverChange(ctx); err != nil {
		return fmt.Errorf("Eine frühere, unterbrochene Änderung an Caddy ließ sich nicht aufräumen: %v", err)
	} else if msg != "" {
		emit("Aufräumen", caddy.StateOK, msg)
	}
	plan, c, err := p.check(ctx, raw)
	if err != nil {
		emit(stepAddress, caddy.StateFailed, err.Error())
		return unchanged(err)
	}
	d := plan.Domain
	emit(stepAddress, caddy.StateOK, d+" zeigt auf "+strings.Join(plan.IPs, ", ")+", also auf diesen Server")

	site := filepath.Join(filepath.Dir(c.Config), ort.CaddySite)
	cf, err := os.ReadFile(c.Config)
	if err != nil {
		return unchanged(err)
	}
	needImport := !caddy.Covered(cf, filepath.Dir(c.Config), site)
	// Kam die import-Zeile samt Kommentar von HostDeck mit dem Backup zurück,
	// nimmt das Zurücknehmen sie wieder heraus wie sonst auch.
	ownImport := needImport || bytes.Contains(cf, []byte(caddy.ImportComment+"\nimport "+site))
	newCF := cf
	if needImport {
		newCF = caddy.AddImport(cf, site)
	}
	a := p.applier(c, step)
	a.Journal, a.Note = p.journalPath(), "freischalten"
	if err := a.Rehearse(ctx, brokenBlock(d)); err != nil {
		return unchanged(fmt.Errorf("Die Probe vor dem Freischalten ging nicht: %v. Ich habe nichts geändert.", err))
	}

	st := &accessState{Domain: d, Since: p.now(), Group: c.Group, Caddyfile: c.Config, Site: site, Import: ownImport,
		Newline: needImport && len(cf) > 0 && !bytes.HasSuffix(cf, []byte("\n"))}
	// Ab hier räumt HostDeck auch nach einem Abbruch (Strg+C, Dienst hält an)
	// vollständig auf.
	uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	closeGate := func() error {
		emit(stepGateClose, caddy.StateRunning, "")
		if err := p.In.applyUnits(uctx, nil, false); err != nil {
			emit(stepGateClose, caddy.StateFailed, err.Error())
			return err
		}
		emit(stepGateClose, caddy.StateUndone, "nur noch der Notausgang")
		return nil
	}
	emit(stepGateOpen, caddy.StateRunning, "")
	if err := p.In.applyUnits(ctx, st, false); err != nil {
		emit(stepGateOpen, caddy.StateFailed, err.Error())
		if cerr := closeGate(); cerr != nil {
			return fmt.Errorf("Eingang für Caddy: %v; Schließen ging auch nicht: %v", err, cerr)
		}
		return unchanged(fmt.Errorf("Eingang für Caddy: %v. Caddy ist unverändert.", err))
	}
	if err := p.Web(ctx, d); err != nil {
		emit(stepGateOpen, caddy.StateFailed, err.Error())
		if cerr := closeGate(); cerr != nil {
			return fmt.Errorf("%v; Schließen ging auch nicht: %v", err, cerr)
		}
		return unchanged(fmt.Errorf("%v. Caddy ist unverändert.", err))
	}
	emit(stepGateOpen, caddy.StateOK, ort.CaddySocket+", nur für Caddy (Gruppe "+c.Group+")")

	ch := caddy.Change{
		Files: []caddy.File{{Path: c.Config, Data: newCF, Was: cf}, {Path: site, Data: siteBlock(d)}},
		Skip:  []string{d},
		Check: func(ctx context.Context) error { return p.waitSite(ctx, d, emit) },
	}
	if err := a.Apply(ctx, ch); err != nil {
		if cerr := closeGate(); cerr != nil {
			return fmt.Errorf("%v Eingang für Caddy schließen ging nicht: %v", err, cerr)
		}
		var undone *caddy.ErrUndone
		if !errors.As(err, &undone) {
			// Zurücklegen ging nicht: Das Journal bleibt, damit der nächste
			// Start es noch einmal versucht.
			return err
		}
		if jerr := caddy.ClearJournal(a.Journal); jerr != nil {
			return fmt.Errorf("%v (%v)", err, jerr)
		}
		return unchanged(err)
	}
	emit(stepSave, caddy.StateRunning, "")
	if err := saveAccess(p.In.DataDir, st); err != nil {
		emit(stepSave, caddy.StateFailed, err.Error())
		return fmt.Errorf("Caddy ist umgestellt und https://%s antwortet, aber HostDeck konnte sich das nicht merken: %v", d, err)
	}
	if err := caddy.ClearJournal(a.Journal); err != nil {
		emit(stepSave, caddy.StateFailed, err.Error())
		return err
	}
	emit(stepSave, caddy.StateOK, "https://"+d)
	return nil
}

// unchanged markiert einen Fehler, nach dem alles wie vorher ist.
func unchanged(err error) error { return &dienst.UnchangedError{Err: err} }

func (p *publisher) journalPath() string { return filepath.Join(p.In.DataDir, ort.CaddyJournal) }

// recoverChange räumt nach einer Änderung an Caddy auf, die ein Absturz
// oder Neustart unterbrochen hat. Der Aufrufer hält die Laufsperre. msg ist
// leer, wenn nichts zu tun war.
func (p *publisher) recoverChange(ctx context.Context) (string, error) {
	path := p.journalPath()
	note, found, err := caddy.PeekJournal(path)
	if !found {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if st, _ := loadAccess(p.In.DataDir); note == "freischalten" && st != nil {
		// Das Freischalten war fertig, nur das Aufräumen fehlte.
		return "", caddy.ClearJournal(path)
	}
	var c *caddy.Caddy
	if found, err := p.Find(ctx); err == nil {
		c = found
	}
	if _, _, err := caddy.Recover(ctx, c, path); err != nil {
		return "", err
	}
	msg := "Ein unterbrochenes Zurücknehmen ist rückgängig gemacht: Die Caddy-Dateien sind wie vorher, der Leitstand bleibt freigeschaltet."
	if strings.HasPrefix(note, "domains") {
		msg = "Eine unterbrochene Änderung an den Domains ist rückgängig gemacht: Die Caddy-Dateien sind wie vorher."
	}
	if note == noteRefresh {
		msg = "Ein unterbrochenes Auffrischen der Caddy-Datei des Leitstands ist rückgängig gemacht: Die Caddy-Dateien sind wie vorher. Das nächste Einrichten oder Update versucht es noch einmal."
	}
	if note == "freischalten" {
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		if err := p.In.applyUnits(uctx, nil, false); err != nil {
			return "", fmt.Errorf("Caddy-Dateien nach einem unterbrochenen Freischalten zurückgelegt, aber der Eingang für Caddy ließ sich nicht schließen: %v", err)
		}
		msg = "Ein unterbrochenes Freischalten ist zurückgenommen: Die Caddy-Dateien sind wie vorher, der Eingang für Caddy ist zu."
	}
	return msg, caddy.ClearJournal(path)
}

// noteRefresh steht im Journal, solange Refresh die Caddy-Datei ändert.
const noteRefresh = "auffrischen"

// Refresh bringt die eigene Caddy-Datei eines freigeschalteten Leitstands
// auf den Stand dieser Version, etwa nach einem Update. Es gilt dieselbe
// Prüfung wie beim Freischalten: Caddy prüft die Datei, lädt neu, der
// Leitstand und alle anderen Seiten müssen danach antworten wie vorher;
// sonst liegt die alte Datei wieder da. msg ist leer, wenn nichts zu tun
// war. Der Aufrufer hält die Laufsperre.
func (p *publisher) Refresh(ctx context.Context) (string, error) {
	st, err := loadAccess(p.In.DataDir)
	if err != nil || st == nil {
		return "", err
	}
	want := siteBlock(st.Domain, st.Extra...)
	old, err := os.ReadFile(st.Site)
	if err != nil {
		return "", fmt.Errorf("%s ist nicht lesbar: %v", st.Site, err)
	}
	if bytes.Equal(old, want) {
		return "", nil
	}
	defer p.forget()
	var msgs []string
	if msg, err := p.recoverChange(ctx); err != nil {
		return "", fmt.Errorf("eine frühere, unterbrochene Änderung an Caddy ließ sich nicht aufräumen: %v", err)
	} else if msg != "" {
		msgs = append(msgs, msg)
	}
	c, err := p.Find(ctx)
	if err != nil {
		return strings.Join(msgs, " "), fmt.Errorf("Caddy ist gerade nicht erreichbar (%v)", err)
	}
	var failed []string
	a := p.applier(c, func(s dienst.Step) {
		if s.State == caddy.StateFailed && s.Detail != "" {
			failed = append(failed, s.Name+": "+s.Detail)
		}
	})
	a.Journal, a.Note = p.journalPath(), noteRefresh
	ch := caddy.Change{
		Files: []caddy.File{{Path: st.Site, Data: want, Was: old}},
		// Auch Adressen, die gerade herausfallen (etwa die Testadresse nach
		// dem Einzug): Dass sie danach nicht mehr antworten, ist gewollt.
		Skip:  append(append([]string{st.Domain}, st.Extra...), blockHosts(old)...),
		Check: func(ctx context.Context) error { return p.waitSite(ctx, st.Domain, func(string, string, string) {}) },
	}
	if err := a.Apply(ctx, ch); err != nil {
		var undone *caddy.ErrUndone
		if errors.As(err, &undone) {
			if jerr := caddy.ClearJournal(a.Journal); jerr != nil {
				err = fmt.Errorf("%v (%v)", err, jerr)
			}
		}
		// Die Fehlermeldung nennt den gescheiterten Schritt meist schon.
		if f := strings.Join(failed, "; "); f != "" && !strings.Contains(err.Error(), f) {
			err = fmt.Errorf("%v (%s)", err, f)
		}
		return strings.Join(msgs, " "), err
	}
	if err := caddy.ClearJournal(a.Journal); err != nil {
		return strings.Join(msgs, " "), err
	}
	// Sagen, was sich geändert hat (Labor S4: nach dem Einzug fiel die
	// Testadresse heraus, die Meldung sprach aber von HTTP/3).
	msg := "Caddy-Datei des Leitstands aufgefrischt (" + st.Site + ")"
	var gone []string
	keep := map[string]bool{}
	for _, h := range blockHosts(want) {
		keep[h] = true
	}
	for _, h := range blockHosts(old) {
		if !keep[h] {
			gone = append(gone, h)
		}
	}
	switch {
	case len(gone) > 0:
		msg += ": Sie bedient " + strings.Join(gone, ", ") + " nicht mehr."
	case !bytes.Contains(old, []byte("Alt-Svc")):
		msg += ": Browser nehmen für https://" + st.Domain + " jetzt normales HTTPS statt HTTP/3, das bei vielen Anbietern gedrosselt ist."
	default:
		msg += "."
	}
	msgs = append(msgs, msg)
	return strings.Join(msgs, " "), nil
}

// waitSite wartet, bis die neue Adresse über Caddy die Anmeldeseite zeigt.
// Beim ersten Mal holt Caddy dafür ein Zertifikat.
func (p *publisher) waitSite(ctx context.Context, d string, emit func(name, state, detail string)) error {
	start := p.now()
	var last error
	for {
		if last = p.Site(ctx, d); last == nil {
			return nil
		}
		waited := p.now().Sub(start)
		if waited >= p.Wait {
			return fmt.Errorf("https://%s zeigt nach %d Sekunden nicht die Anmeldeseite (%v). Oft ist Port 80 oder 443 von außen zu, oder der DNS-Eintrag ist noch nicht überall angekommen", d, int(p.Wait.Seconds()), last)
		}
		emit(caddy.StepCheck, caddy.StateRunning, fmt.Sprintf("Caddy holt das Zertifikat für %s (%d s) …", d, int(waited.Seconds())))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.Every):
		}
	}
}

// Unpublish nimmt das Freischalten zurück: Caddy-Eintrag heraus (mit
// derselben Prüfung), Eingang für Caddy zu.
func (p *publisher) Unpublish(ctx context.Context, step func(dienst.Step)) error {
	defer p.forget()
	emit := func(name, state, detail string) { step(dienst.Step{Name: name, State: state, Detail: detail}) }
	if msg, err := p.recoverChange(ctx); err != nil {
		return fmt.Errorf("Eine frühere, unterbrochene Änderung an Caddy ließ sich nicht aufräumen: %v", err)
	} else if msg != "" {
		emit("Aufräumen", caddy.StateOK, msg)
	}
	st, err := loadAccess(p.In.DataDir)
	if err != nil {
		return unchanged(fmt.Errorf("Die Freischaltung ist nicht lesbar: %v", err))
	}
	if st == nil {
		return unchanged(errors.New("Der Leitstand ist nicht freigeschaltet."))
	}
	files := []caddy.File{{Path: st.Site, Data: nil}}
	if st.Import {
		cf, err := os.ReadFile(st.Caddyfile)
		if err != nil {
			return err
		}
		if back, ok := caddy.RemoveImport(cf, st.Site); ok {
			if st.Newline {
				back = bytes.TrimSuffix(back, []byte("\n"))
			}
			files = append([]caddy.File{{Path: st.Caddyfile, Data: back, Was: cf}}, files...)
		}
	}
	ch := caddy.Change{Files: files, Skip: []string{st.Domain}}
	c, err := p.Find(ctx)
	if err != nil {
		// Caddy läuft gerade nicht: die Dateien trotzdem zurücklegen. Eine
		// import-Zeile und eine eigene Datei herauszunehmen kann das
		// Caddyfile nicht kaputt machen; Caddy lädt es beim nächsten Start.
		emit(caddy.StepWrite, caddy.StateRunning, "")
		if err := caddy.ApplyOffline(st.Caddyfile, ch); err != nil {
			emit(caddy.StepWrite, caddy.StateFailed, err.Error())
			return unchanged(fmt.Errorf("Caddy läuft nicht, und die Dateien ließen sich nicht ändern: %v", err))
		}
		emit(caddy.StepWrite, caddy.StateOK, "Caddy läuft gerade nicht; die Dateien sind zurückgenommen und gelten beim nächsten Start")
	} else {
		a := p.applier(c, step)
		a.Journal, a.Note = p.journalPath(), "zuruecknehmen"
		if err := a.Apply(ctx, ch); err != nil {
			var undone *caddy.ErrUndone
			if errors.As(err, &undone) {
				if jerr := caddy.ClearJournal(a.Journal); jerr != nil {
					return fmt.Errorf("%v (%v)", err, jerr)
				}
				return unchanged(err)
			}
			return err
		}
		// Caddy kennt den Leitstand nicht mehr; ab hier ist Zurücklegen
		// nicht mehr nötig.
		if err := caddy.ClearJournal(a.Journal); err != nil {
			return err
		}
	}
	uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	emit(stepGateClose, caddy.StateRunning, "")
	if err := p.In.applyUnits(uctx, nil, false); err != nil {
		emit(stepGateClose, caddy.StateFailed, err.Error())
		return fmt.Errorf("Caddy-Eintrag ist heraus, aber der Eingang für Caddy ließ sich nicht schließen: %v", err)
	}
	if err := removeAccess(p.In.DataDir); err != nil {
		emit(stepGateClose, caddy.StateFailed, err.Error())
		return err
	}
	emit(stepGateClose, caddy.StateOK, "der Leitstand geht nur noch über den Notausgang")
	return nil
}

// printStep zeigt Schritte im Terminal.
func printStep(w io.Writer) func(dienst.Step) {
	return func(s dienst.Step) {
		mark := map[string]string{caddy.StateRunning: "…", caddy.StateOK: "✓", caddy.StateFailed: "✗", caddy.StateUndone: "↺"}[s.State]
		if s.State == caddy.StateRunning && s.Detail == "" {
			return
		}
		line := fmt.Sprintf("  %s %s", mark, s.Name)
		if s.Detail != "" {
			line += ": " + s.Detail
		}
		fmt.Fprintln(w, line)
	}
}

// freischaltenCmd ist dasselbe als Befehl auf dem Server.
func freischaltenCmd(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("freischalten", flag.ContinueOnError)
	fl.SetOutput(stderr)
	back := fl.Bool("zuruecknehmen", false, "")
	yes := fl.Bool("ja", false, "")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		fmt.Fprintln(stderr, "hostdeck freischalten läuft auf dem Server und braucht root: sudo hostdeck freischalten")
		return 2
	}
	in, err := systemInstaller(stdout)
	if err != nil {
		fmt.Fprintf(stderr, "Fehler: %v\n", err)
		return 1
	}
	p := systemPublisher(in)
	return runFreischalten(ctx, p, fl.Arg(0), *back, *yes, stdin, stdout, stderr)
}

func runFreischalten(ctx context.Context, p *publisher, raw string, back, yes bool, stdin io.Reader, stdout, stderr io.Writer) int {
	journal := &protokoll.Log{File: filepath.Join(p.In.DataDir, ort.JournalFile)}
	st := p.State(ctx)
	if !back && raw == "" {
		switch {
		case st.Domain != "":
			fmt.Fprintf(stdout, "Der Leitstand ist freigeschaltet: https://%s (seit %s).\nZurücknehmen: sudo hostdeck freischalten --zuruecknehmen\n", st.Domain, st.Since.Local().Format("02.01.2006 15:04"))
		case !st.Caddy:
			fmt.Fprintln(stdout, st.Problem)
		default:
			s := st.Suggest
			if s == "" {
				s = "hostdeck.example.de"
			}
			fmt.Fprintf(stdout, "Der Leitstand ist von außen noch nicht erreichbar.\nFreischalten: im Leitstand unter System > Zugang von außen, oder: sudo hostdeck freischalten %s\n", s)
		}
		return 0
	}
	ask := func(q string) bool {
		if yes {
			return true
		}
		fmt.Fprint(stdout, q+" Zum Bestätigen ja eingeben: ")
		var a string
		fmt.Fscanln(stdin, &a)
		return strings.EqualFold(strings.TrimSpace(a), "ja")
	}
	print := printStep(stdout)
	lock, ok := waitForLauf(ctx, "Freischalten", stdout)
	if !ok {
		return 1
	}
	defer lock.Release()
	if back {
		if st.Domain == "" {
			fmt.Fprintln(stdout, "Der Leitstand ist nicht freigeschaltet; es gibt nichts zurückzunehmen.")
			return 0
		}
		if !ask(fmt.Sprintf("Zugang von außen (https://%s) zurücknehmen? Danach geht der Leitstand nur noch über den Notausgang.", st.Domain)) {
			fmt.Fprintln(stdout, "Nichts geändert.")
			return 1
		}
		err := p.Unpublish(ctx, print)
		text := "Zugang von außen zurückgenommen (sudo hostdeck freischalten --zuruecknehmen)"
		if err != nil {
			text = "Zurücknehmen ging nicht: " + err.Error()
		}
		_ = journal.Add(protokoll.Entry{Kind: protokoll.Zugang, Text: text, Addr: dienst.AdminAddr})
		if err != nil {
			fmt.Fprintf(stderr, "Fehler: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "\nZurückgenommen. Der Leitstand geht nur noch über den Notausgang: sudo hostdeck notausgang")
		return 0
	}
	plan, err := p.Check(ctx, raw)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "\n%s zeigt auf %s, also auf diesen Server.\n\nDas ändert sich an Caddy:\n", plan.Domain, strings.Join(plan.IPs, ", "))
	for _, f := range plan.Files {
		fmt.Fprintf(stdout, "\n  %s (%s)\n", f.Path, f.Note)
		for _, l := range strings.Split(strings.TrimRight(f.Text, "\n"), "\n") {
			if l != "" {
				fmt.Fprintf(stdout, "    %s\n", l)
			}
		}
	}
	fmt.Fprintf(stdout, "\nHostDeck sichert beide Dateien, prüft mit caddy validate, lädt neu und vergleicht danach deine %d Seiten mit vorher.\nGeht etwas schief, legt es alles byte-gleich zurück.\n\n", plan.Pages)
	if !ask("Jetzt freischalten?") {
		fmt.Fprintln(stdout, "Nichts geändert.")
		return 1
	}
	err = p.Publish(ctx, plan.Domain, print)
	text := "Freigeschaltet: https://" + plan.Domain + " (sudo hostdeck freischalten)"
	if err != nil {
		text = "Freischalten von " + plan.Domain + " ging nicht: " + err.Error()
	}
	_ = journal.Add(protokoll.Entry{Kind: protokoll.Zugang, Text: text, Addr: dienst.AdminAddr})
	if err != nil {
		fmt.Fprintf(stderr, "\n%v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "\nFertig. Der Leitstand ist erreichbar unter https://%s\nAnmelden mit deinem Leitstand-Passwort.\n", plan.Domain)
	return 0
}

// blockHosts liest die Adressen aus der Kopfzeile eines Blocks, wie
// siteBlock ihn schreibt („a.de, b.de {“).
func blockHosts(b []byte) []string {
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		head, ok := strings.CutSuffix(line, "{")
		if !ok {
			return out
		}
		for _, h := range strings.Split(head, ",") {
			if h = strings.TrimSpace(h); h != "" {
				out = append(out, h)
			}
		}
		return out
	}
	return out
}
