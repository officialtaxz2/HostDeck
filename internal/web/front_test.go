package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/sitzung"
)

const goodLink = "0123456789abcdef0123456789abcdef"

type frontRig struct {
	t    *testing.T
	srv  *httptest.Server
	sess *sitzung.Store
	door *pwDoor
	seen http.Header // Kopfzeilen, die beim Datenteil ankamen
	mu   sync.Mutex
	now  time.Time
}

func newFront(t *testing.T, g Gate) *frontRig {
	r := &frontRig{t: t, now: time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC)}
	r.sess = sitzung.New("")
	r.sess.Now = r.clock
	api := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.seen = req.Header.Clone()
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	})
	r.door = &pwDoor{MemoryDoor: MemoryDoor{Store: r.sess, Link: func(tok string) bool { return tok == goodLink }}}
	f := &Front{API: api, Door: r.door, Now: r.clock}
	if g.Hosts == nil {
		g.Hosts = LoopbackHost
	}
	if g.Cookie == "" {
		g.Cookie = "hostdeck"
	}
	if g.Hint == "" {
		g.Hint = "Bitte anmelden."
	}
	if g.Name == "" {
		g.Name = "Test"
	}
	r.srv = httptest.NewServer(f.Handler(g))
	t.Cleanup(r.srv.Close)
	return r
}

// pwDoor kennt zusätzlich ein Passwort und sperrt nach fünf Fehlversuchen.
type pwDoor struct {
	MemoryDoor
	mu    sync.Mutex
	fails int
	who   []Visitor
	down  error // gesetzt: der Dienst ist nicht erreichbar
}

func (d *pwDoor) Check(ctx context.Context, id string, active bool) error {
	if d.down != nil {
		return d.down
	}
	return d.MemoryDoor.Check(ctx, id, active)
}

const goodPassword = "richtig-und-lang"

func (d *pwDoor) Login(_ context.Context, pw string, v Visitor) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.who = append(d.who, v)
	if d.fails >= 5 {
		return "", &LockedError{Wait: 15 * time.Minute}
	}
	if pw != goodPassword {
		d.fails++
		return "", ErrBadPassword
	}
	return d.Store.NewSession(sitzung.ViaPassword, v.Gate, v.Addr)
}

func (r *frontRig) clock() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now
}

func (r *frontRig) advance(d time.Duration) {
	r.mu.Lock()
	r.now = r.now.Add(d)
	r.mu.Unlock()
}

func (r *frontRig) req(method, path, body string, hdr map[string]string) *http.Response {
	r.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, r.srv.URL+path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func (r *frontRig) origin() string { return r.srv.URL }

func (r *frontRig) login() string {
	r.t.Helper()
	resp := r.req("POST", "/api/einmal", `{"token":"`+goodLink+`"}`, map[string]string{"Origin": r.origin()})
	if resp.StatusCode != 200 {
		r.t.Fatalf("Anmeldung: %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "hostdeck" || c.Name == "__Host-hostdeck" {
			return c.Name + "=" + c.Value
		}
	}
	r.t.Fatal("kein Cookie")
	return ""
}

func TestLinkGegenAnmeldung(t *testing.T) {
	r := newFront(t, Gate{})
	if resp := r.req("POST", "/api/einmal", `{"token":"`+goodLink+`"}`, nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("ohne Origin: %d", resp.StatusCode)
	}
	if resp := r.req("POST", "/api/einmal", `{"token":"ffffffffffffffffffffffffffffffff"}`, map[string]string{"Origin": r.origin()}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("falscher Link: %d", resp.StatusCode)
	}
	resp := r.req("POST", "/api/einmal", `{"token":"`+goodLink+`"}`, map[string]string{"Origin": r.origin()})
	if resp.StatusCode != 200 {
		t.Fatalf("Anmeldung: %d", resp.StatusCode)
	}
	sc := resp.Header.Get("Set-Cookie")
	for _, want := range []string{"hostdeck=", "HttpOnly", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(sc, want) {
			t.Errorf("Cookie ohne %s: %s", want, sc)
		}
	}
	if strings.Contains(sc, "Secure") {
		t.Errorf("Notausgang über http darf kein Secure-Cookie setzen: %s", sc)
	}
	cookie := r.login()
	if resp := r.req("GET", "/api/state", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("ohne Cookie: %d", resp.StatusCode)
	}
	if resp := r.req("GET", "/api/state", "", map[string]string{"Cookie": "hostdeck=erfunden"}); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("erfundenes Cookie: %d", resp.StatusCode)
	}
	if resp := r.req("GET", "/api/state", "", map[string]string{"Cookie": cookie}); resp.StatusCode != 200 {
		t.Fatalf("mit Cookie: %d", resp.StatusCode)
	}
	if r.seen.Get("Cookie") != "" || r.seen.Get("Authorization") != "" {
		t.Errorf("Anmeldedaten gingen an den Datenteil weiter: %v", r.seen)
	}
}

func TestAendernNurMitHerkunft(t *testing.T) {
	r := newFront(t, Gate{})
	cookie := r.login()
	if resp := r.req("POST", "/api/save", `{}`, map[string]string{"Cookie": cookie}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST ohne Origin: %d", resp.StatusCode)
	}
	if resp := r.req("POST", "/api/save", `{}`, map[string]string{"Cookie": cookie, "Origin": "https://evil.example"}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST mit fremdem Origin: %d", resp.StatusCode)
	}
	if resp := r.req("GET", "/api/state", "", map[string]string{"Cookie": cookie, "Sec-Fetch-Site": "cross-site"}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET von fremder Seite: %d", resp.StatusCode)
	}
	if resp := r.req("POST", "/api/save", `{}`, map[string]string{"Cookie": cookie, "Origin": r.origin(), "Sec-Fetch-Site": "same-origin"}); resp.StatusCode != 200 {
		t.Errorf("POST von der eigenen Seite: %d", resp.StatusCode)
	}
}

func TestNurErlaubteHosts(t *testing.T) {
	r := newFront(t, Gate{})
	for host, want := range map[string]int{"127.0.0.1:7322": 200, "localhost:9": 200, "evil.example": 403, "hostdeck.example.net": 403} {
		if resp := r.req("GET", "/", "", map[string]string{"Host": host}); resp.StatusCode != want {
			t.Errorf("Host %s: %d, erwartet %d", host, resp.StatusCode, want)
		}
	}
	s := newFront(t, Gate{Hosts: func(h string) bool { return h == "hostdeck.example.net" }, Cookie: "__Host-hostdeck", Secure: true})
	if resp := s.req("GET", "/", "", map[string]string{"Host": "hostdeck.example.net"}); resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Strict-Transport-Security"), "max-age") {
		t.Errorf("Subdomain: %d HSTS=%q", resp.StatusCode, resp.Header.Get("Strict-Transport-Security"))
	}
	if resp := s.req("GET", "/", "", map[string]string{"Host": "127.0.0.1"}); resp.StatusCode != 403 {
		t.Errorf("Subdomain-Eingang nimmt 127.0.0.1 an: %d", resp.StatusCode)
	}
}

func TestSecureCookie(t *testing.T) {
	r := newFront(t, Gate{Cookie: "__Host-hostdeck", Secure: true})
	resp := r.req("POST", "/api/einmal", `{"token":"`+goodLink+`"}`, map[string]string{"Origin": r.origin()})
	sc := resp.Header.Get("Set-Cookie")
	if !strings.HasPrefix(sc, "__Host-hostdeck=") || !strings.Contains(sc, "Secure") {
		t.Fatalf("Cookie: %s", sc)
	}
}

func TestSitzungLaeuftAb(t *testing.T) {
	r := newFront(t, Gate{})
	cookie := r.login()
	ok := func() int { return r.req("GET", "/api/state", "", map[string]string{"Cookie": cookie}).StatusCode }
	bg := func() int {
		return r.req("GET", "/api/lauf", "", map[string]string{"Cookie": cookie, BackgroundHeader: "1"}).StatusCode
	}
	r.advance(29 * time.Minute)
	if ok() != 200 {
		t.Fatal("nach 29 Minuten abgemeldet")
	}
	// Abfragen im Hintergrund verlängern nicht.
	r.advance(20 * time.Minute)
	if bg() != 200 {
		t.Fatal("Hintergrund nach 20 Minuten abgewiesen")
	}
	r.advance(11 * time.Minute)
	if bg() != http.StatusUnauthorized {
		t.Fatal("Hintergrund-Abfragen halten die Anmeldung am Leben")
	}
	// Spätestens nach 12 Stunden ist Schluss, auch bei Aktivität.
	cookie = r.login()
	for i := 0; i < 26; i++ {
		r.advance(29 * time.Minute)
		if code := ok(); code != 200 {
			if i < 24 {
				t.Fatalf("nach %d Minuten abgemeldet", (i+1)*29)
			}
			return
		}
	}
	t.Fatal("nach mehr als 12 Stunden noch angemeldet")
}

func TestAbmelden(t *testing.T) {
	r := newFront(t, Gate{})
	cookie := r.login()
	if resp := r.req("POST", "/api/abmelden", "", map[string]string{"Cookie": cookie, "Origin": r.origin()}); resp.StatusCode != 200 {
		t.Fatalf("abmelden: %d", resp.StatusCode)
	}
	if resp := r.req("GET", "/api/state", "", map[string]string{"Cookie": cookie}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("nach dem Abmelden: %d", resp.StatusCode)
	}
}

func TestLinksGebremst(t *testing.T) {
	r := newFront(t, Gate{})
	for i := 0; i < 20; i++ {
		r.req("POST", "/api/einmal", `{"token":"ffffffffffffffffffffffffffffffff"}`, map[string]string{"Origin": r.origin()})
	}
	if resp := r.req("POST", "/api/einmal", `{"token":"`+goodLink+`"}`, map[string]string{"Origin": r.origin()}); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("21. Versuch: %d", resp.StatusCode)
	}
	r.advance(61 * time.Second)
	if resp := r.req("POST", "/api/einmal", `{"token":"`+goodLink+`"}`, map[string]string{"Origin": r.origin()}); resp.StatusCode != 200 {
		t.Fatalf("nach einer Minute: %d", resp.StatusCode)
	}
}

func TestHostdeckServeTauschtLink(t *testing.T) {
	s := &Server{Dir: prepare(t, nil, true), Token: testToken}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/einmal", strings.NewReader(`{"token":"`+testToken+`"}`))
	req.Header.Set("Origin", ts.URL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || len(resp.Cookies()) != 1 {
		t.Fatalf("Tausch: %d %v", resp.StatusCode, resp.Cookies())
	}
	req, _ = http.NewRequest("GET", ts.URL+"/api/state", nil)
	req.AddCookie(resp.Cookies()[0])
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"projects"`) {
		t.Fatalf("Stand mit Cookie: %d", resp.StatusCode)
	}
}

func TestAnmeldenMitPasswort(t *testing.T) {
	r := newFront(t, Gate{Password: true, Addr: func(*http.Request) string { return "203.0.113.7" }})
	login := func(pw string, hdr map[string]string) *http.Response {
		return r.req("POST", "/api/anmelden", `{"passwort":"`+pw+`"}`, hdr)
	}
	if resp := login(goodPassword, nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("ohne Origin: %d", resp.StatusCode)
	}
	resp := login(goodPassword, map[string]string{"Origin": r.origin()})
	if resp.StatusCode != 200 || len(resp.Cookies()) != 1 {
		t.Fatalf("Anmeldung: %d", resp.StatusCode)
	}
	cookie := "hostdeck=" + resp.Cookies()[0].Value
	if resp := r.req("GET", "/api/state", "", map[string]string{"Cookie": cookie}); resp.StatusCode != 200 {
		t.Fatalf("mit Cookie: %d", resp.StatusCode)
	}
	if r.seen.Get(SessionHeader) == "" || r.seen.Get(AddrHeader) != "203.0.113.7" || r.seen.Get(GateHeader) != "Test" {
		t.Errorf("Kopfzeilen beim Datenteil: %v", r.seen)
	}
	for i := 0; i < 5; i++ {
		if resp := login("falsch", map[string]string{"Origin": r.origin()}); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("falsches Passwort %d: %d", i, resp.StatusCode)
		}
	}
	if resp := login(goodPassword, map[string]string{"Origin": r.origin()}); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("gesperrt: %d", resp.StatusCode)
	}
	if v := r.door.who[0]; v.Addr != "203.0.113.7" || v.Gate != "Test" {
		t.Errorf("Besucher: %+v", v)
	}
}

func TestPasswortNurWoErlaubt(t *testing.T) {
	r := newFront(t, Gate{})
	if resp := r.req("POST", "/api/anmelden", `{"passwort":"`+goodPassword+`"}`, map[string]string{"Origin": r.origin()}); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("Eingang ohne Passwort nimmt eins an: %d", resp.StatusCode)
	}
	resp := r.req("GET", "/api/state", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ohne Anmeldung: %d", resp.StatusCode)
	}
}

func TestBesucherKannKopfzeilenNichtVorgeben(t *testing.T) {
	r := newFront(t, Gate{})
	cookie := r.login()
	r.req("GET", "/api/state", "", map[string]string{"Cookie": cookie, SessionHeader: "fremd", AddrHeader: "1.2.3.4", GateHeader: "Caddy"})
	if r.seen.Get(SessionHeader) == "fremd" || r.seen.Get(AddrHeader) != "Test" || r.seen.Get(GateHeader) != "Test" {
		t.Errorf("Kopfzeilen vom Besucher kamen durch: %v", r.seen)
	}
}

func TestUeberallAbmelden(t *testing.T) {
	r := newFront(t, Gate{})
	a, b := r.login(), r.login()
	if resp := r.req("POST", "/api/abmelden", `{"ueberall":true}`, map[string]string{"Cookie": "hostdeck=erfunden", "Origin": r.origin()}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("überall abmelden ohne Anmeldung: %d", resp.StatusCode)
	}
	if resp := r.req("POST", "/api/abmelden", `{"ueberall":true}`, map[string]string{"Cookie": a, "Origin": r.origin()}); resp.StatusCode != 200 {
		t.Fatalf("überall abmelden: %d", resp.StatusCode)
	}
	if resp := r.req("GET", "/api/state", "", map[string]string{"Cookie": b}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("andere Anmeldung noch gültig: %d", resp.StatusCode)
	}
}

func TestEingangUeberCaddy(t *testing.T) {
	r := newFront(t, Gate{Name: "hostdeck.example.de", Hosts: ExactHost("HOSTDECK.example.de."), Cookie: "__Host-hostdeck", Secure: true, Password: true, NoLinks: true, Addr: ForwardedFor})
	host := map[string]string{"Host": "hostdeck.example.de", "Origin": "https://hostdeck.example.de"}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range host {
			m[a] = b
		}
		m[k] = v
		return m
	}
	// Nur die freigeschaltete Adresse.
	if resp := r.req("GET", "/", "", map[string]string{"Host": "127.0.0.1"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("fremder Host: %d", resp.StatusCode)
	}
	if resp := r.req("GET", "/", "", map[string]string{"Host": "hostdeck.example.de"}); resp.StatusCode != 200 || resp.Header.Get("Strict-Transport-Security") == "" {
		t.Fatalf("Seite: %d %v", resp.StatusCode, resp.Header)
	}
	// Kein Einmal-Link über Caddy, auch kein gültiger.
	if resp := r.req("POST", "/api/einmal", `{"token":"`+goodLink+`"}`, host); resp.StatusCode == 200 || len(resp.Cookies()) > 0 {
		t.Fatalf("Einmal-Link über Caddy: %d", resp.StatusCode)
	}
	// Anmelden: Adresse aus dem letzten Eintrag von X-Forwarded-For, Cookie nur über HTTPS.
	resp := r.req("POST", "/api/anmelden", `{"passwort":"`+goodPassword+`"}`, with("X-Forwarded-For", "198.51.100.1, 203.0.113.50"))
	if resp.StatusCode != 200 {
		t.Fatalf("Anmelden: %d", resp.StatusCode)
	}
	c := resp.Cookies()
	if len(c) != 1 || c[0].Name != "__Host-hostdeck" || !c[0].Secure || !c[0].HttpOnly || c[0].Path != "/" || c[0].Domain != "" || c[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("Cookie: %+v", c)
	}
	r.door.mu.Lock()
	who := r.door.who[len(r.door.who)-1]
	r.door.mu.Unlock()
	if who.Addr != "203.0.113.50" || who.Gate != "hostdeck.example.de" {
		t.Fatalf("Besucher: %+v", who)
	}
	// Ohne gültige Adresse von Caddy: der Name des Eingangs.
	r.req("POST", "/api/anmelden", `{"passwort":"x"}`, with("X-Forwarded-For", "<script>"))
	r.door.mu.Lock()
	who = r.door.who[len(r.door.who)-1]
	r.door.mu.Unlock()
	if who.Addr != "hostdeck.example.de" {
		t.Fatalf("Besucher ohne Adresse: %+v", who)
	}
}

func TestForwardedFor(t *testing.T) {
	for in, want := range map[string]string{
		"":                           "",
		"203.0.113.5":                "203.0.113.5",
		"10.0.0.1, 203.0.113.5":      "203.0.113.5",
		"2001:db8::1":                "2001:db8::1",
		"203.0.113.5, kaputt":        "",
		" 2001:DB8:0:0::1 ":          "2001:db8::1",
		"198.51.100.1,203.0.113.5 ,": "",
	} {
		req, _ := http.NewRequest("GET", "/", nil)
		if in != "" {
			req.Header.Set("X-Forwarded-For", in)
		}
		if got := ForwardedFor(req); got != want {
			t.Errorf("%q: %q statt %q", in, got, want)
		}
	}
}

// Die Bremse zählt je Besucher: Ein Fremder mit vielen Anfragen sperrt den
// Besitzer nicht aus, und unlesbare Anfragen zählen gar nicht.
func TestBremseJeBesucher(t *testing.T) {
	r := newFront(t, Gate{Name: "hostdeck.example.de", Hosts: ExactHost("hostdeck.example.de"), Cookie: "__Host-hostdeck", Secure: true, Password: true, NoLinks: true, Addr: ForwardedFor})
	from := func(ip string) map[string]string {
		return map[string]string{"Host": "hostdeck.example.de", "Origin": "https://hostdeck.example.de", "X-Forwarded-For": ip}
	}
	for i := 0; i < 50; i++ {
		r.req("POST", "/api/anmelden", `kaputt`, from("198.51.100.7"))
	}
	if resp := r.req("POST", "/api/anmelden", `{"passwort":"`+goodPassword+`"}`, from("198.51.100.7")); resp.StatusCode != 200 {
		t.Fatalf("unlesbare Anfragen zählen mit: %d", resp.StatusCode)
	}
	for i := 0; i < 19; i++ {
		r.req("POST", "/api/anmelden", `{"passwort":"`+goodPassword+`"}`, from("2001:db8:1:2::"+strconv.Itoa(i+1)))
	}
	if resp := r.req("POST", "/api/anmelden", `{"passwort":"`+goodPassword+`"}`, from("2001:db8:1:2::ff")); resp.StatusCode != 200 {
		t.Fatalf("20. Versuch: %d", resp.StatusCode)
	}
	if resp := r.req("POST", "/api/anmelden", `{"passwort":"`+goodPassword+`"}`, from("2001:db8:1:2::fe")); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("21. Versuch aus demselben /64: %d", resp.StatusCode)
	}
	if resp := r.req("POST", "/api/anmelden", `{"passwort":"`+goodPassword+`"}`, from("203.0.113.50")); resp.StatusCode != 200 {
		t.Fatalf("Besitzer ausgesperrt: %d", resp.StatusCode)
	}
}

// Ein Download darf länger dauern als die Fristen des Servers, solange
// Daten fließen. Andere Anfragen nicht.
func TestDownloadUeberDieFristHinaus(t *testing.T) {
	sess := sitzung.New("")
	api := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		for i := 0; i < 6; i++ {
			io.WriteString(w, strings.Repeat("x", 1000))
			http.NewResponseController(w).Flush()
			time.Sleep(100 * time.Millisecond)
		}
	})
	door := &MemoryDoor{Store: sess, Link: func(tok string) bool { return tok == goodLink }}
	f := &Front{API: api, Door: door}
	srv := httptest.NewUnstartedServer(f.Handler(Gate{Hosts: LoopbackHost, Cookie: "hostdeck", Hint: "Bitte anmelden.", Name: "Test"}))
	srv.Config.ReadTimeout = 250 * time.Millisecond
	srv.Config.WriteTimeout = 250 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)
	login, _ := http.NewRequest("POST", srv.URL+"/api/einmal", strings.NewReader(`{"token":"`+goodLink+`"}`))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", srv.URL)
	resp, err := http.DefaultClient.Do(login)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var cookie string
	for _, c := range resp.Cookies() {
		cookie = c.Name + "=" + c.Value
	}
	get := func(path string) (int, error) {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		req.Header.Set("Cookie", cookie)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		return len(b), err
	}
	if n, err := get(DownloadPath + "abc"); err != nil || n != 6000 {
		t.Fatalf("Download: %d Bytes, %v", n, err)
	}
	if n, err := get("/api/state"); err == nil && n == 6000 {
		t.Fatal("andere Anfrage lief über die Frist hinaus; der Test prüft nichts")
	}
}

// Labor S10: Ist der Dienst weg, sagt der Leitstand in einem Satz, was los
// ist; Socket und Pfad stehen nur im Log.
func TestDienstWegVerstaendlich(t *testing.T) {
	r := newFront(t, Gate{})
	r.door.down = errors.New("HostDeck-Dienst nicht erreichbar (Post \"http://hostdeck-dienst/intern/sitzung\": dial unix /run/hostdeck/dienst.sock: connect: no such file or directory)")
	req, _ := http.NewRequest(http.MethodGet, r.srv.URL+"/api/stand", nil)
	req.AddCookie(&http.Cookie{Name: "hostdeck", Value: "irgendwas"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(string(b), "antwortet gerade nicht") || strings.Contains(string(b), "dial unix") {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
}

// Umstieg von sbm: Wer mit dem alten Cookie kommt, bleibt angemeldet und
// bekommt das neue.
func TestAltesCookieVonSbm(t *testing.T) {
	r := newFront(t, Gate{Cookie: "__Host-hostdeck", Secure: true})
	value := strings.TrimPrefix(r.login(), "__Host-hostdeck=")
	resp := r.req("GET", "/api/state", "", map[string]string{"Cookie": "__Host-sbm=" + value})
	if resp.StatusCode != 200 {
		t.Fatalf("altes Cookie: %d", resp.StatusCode)
	}
	var set, drop bool
	for _, c := range resp.Cookies() {
		set = set || (c.Name == "__Host-hostdeck" && c.Value == value && c.Secure && c.HttpOnly)
		drop = drop || (c.Name == "__Host-sbm" && c.MaxAge < 0)
	}
	if !set || !drop {
		t.Errorf("Cookies: %v", resp.Header.Values("Set-Cookie"))
	}
	if resp := r.req("GET", "/api/state", "", map[string]string{"Cookie": "__Host-sbm=erfunden"}); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("erfundenes altes Cookie: %d", resp.StatusCode)
	}
	if resp := r.req("GET", "/api/state", "", map[string]string{"Cookie": "__Host-hostdeck=" + value}); resp.StatusCode != 200 || len(resp.Cookies()) != 0 {
		t.Errorf("neues Cookie: %d %v", resp.StatusCode, resp.Cookies())
	}
}
