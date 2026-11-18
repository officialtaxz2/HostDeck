package web

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Gate beschreibt einen Eingang der Website: welche Hostnamen er annimmt,
// wie das Anmelde-Cookie heißt und ob man sich mit Passwort anmelden kann.
type Gate struct {
	Name     string                 // für Meldungen und das Protokoll, etwa „Notausgang“
	Hosts    func(host string) bool // erlaubte Hostnamen ohne Port
	Cookie   string                 // Name des Anmelde-Cookies
	Secure   bool                   // nur über HTTPS (Cookie mit Secure, HSTS)
	Hint     string                 // Hinweis, wenn die Anmeldung fehlt
	Password bool                   // Anmelden mit Passwort anbieten
	NoLinks  bool                   // keine Einmal-Links (nur am Notausgang)
	// Addr liefert die Adresse des Besuchers fürs Protokoll und die Sperre.
	// Ohne: der Name des Eingangs.
	Addr func(r *http.Request) string
}

// Front ist die Website: die Seite selbst, die Anmeldung und die
// Weiterleitung von /api an den Teil, der die Daten kennt.
type Front struct {
	API    http.Handler // /api/… für angemeldete Besucher
	Door   Door         // wer hineinkommt
	Bearer string       // nur hostdeck serve: fester Schlüssel statt Cookie (für Tests und Skripte)
	Log    io.Writer    // kurze Meldungen
	Now    func() time.Time

	mu  sync.Mutex
	lim map[string]*limiter
}

// Kopfzeilen zwischen Website und Datenteil.
const (
	BackgroundHeader = "X-Hostdeck-Hintergrund" // Abfrage zählt nicht als Aktivität
	SessionHeader    = "X-Hostdeck-Sitzung"     // Kennung der Anmeldung
	AddrHeader       = "X-Hostdeck-Adresse"     // Adresse des Besuchers
	GateHeader       = "X-Hostdeck-Eingang"     // Name des Eingangs
)

// UploadPath nimmt große Anfragen an (ein Update von HostDeck).
const UploadPath = "/api/update"

// MaxUpload begrenzt ein hochgeladenes Update.
const MaxUpload = 96 << 20

// UploadTime ist die Zeit, die ein Update zum Hochladen haben darf.
const UploadTime = 15 * time.Minute

// DownloadPath ist der Anfang der Adresse, unter der ein Backup zum
// Herunterladen liegt. Ein Download darf Stunden dauern, solange Daten
// fließen.
const DownloadPath = "/api/backup/datei/"

// DownloadStall ist die Zeit, die ein Download ohne Fortschritt hängen
// darf.
const DownloadStall = 2 * time.Minute

// Handler liefert die Website für einen Eingang.
func (f *Front) Handler(g Gate) http.Handler {
	mux := http.NewServeMux()
	files, _ := fs.Sub(static, "static")
	mux.Handle("GET /", http.FileServerFS(files))
	if !g.NoLinks {
		mux.HandleFunc("POST /api/einmal", f.handleRedeem(g))
	}
	mux.HandleFunc("POST /api/anmelden", f.handleLogin(g))
	mux.HandleFunc("POST /api/abmelden", f.handleLogout(g))
	api := f.auth(g, f.API)
	mux.Handle("GET /api/", api)
	mux.Handle("POST /api/", api)
	return f.guard(g, mux)
}

// guard setzt Sicherheits-Header, nimmt nur die erlaubten Hostnamen an und
// weist Anfragen fremder Seiten ab.
func (f *Front) guard(g Gate, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; connect-src 'self'; img-src 'self' data:; manifest-src 'self'; worker-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		if g.Secure {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		if !g.Hosts(hostOnly(r.Host)) {
			http.Error(w, "Diese Adresse ist für HostDeck nicht freigegeben.", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || u.Host != r.Host {
				http.Error(w, "Anfrage von fremder Seite abgewiesen.", http.StatusForbidden)
				return
			}
		}
		// Browser sagen, woher eine Anfrage kommt. Für /api nur von der
		// eigenen Seite.
		if s := r.Header.Get("Sec-Fetch-Site"); s != "" && s != "same-origin" && s != "none" && strings.HasPrefix(r.URL.Path, "/api/") {
			http.Error(w, "Anfrage von fremder Seite abgewiesen.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hostOnly(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return strings.ToLower(strings.Trim(host, "[]"))
}

// LoopbackHost erlaubt localhost und Loopback-Adressen, etwa für einen
// SSH-Tunnel.
func LoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ForwardedFor liest die Adresse des Besuchers aus X-Forwarded-For, wie
// Caddy es setzt: der letzte Eintrag, nur eine gültige IP-Adresse. Nur für
// einen Eingang, den allein Caddy erreicht.
func ForwardedFor(r *http.Request) string {
	vals := r.Header.Values("X-Forwarded-For")
	if len(vals) == 0 {
		return ""
	}
	parts := strings.Split(vals[len(vals)-1], ",")
	ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1]))
	if ip == nil {
		return ""
	}
	return ip.String()
}

// ExactHost erlaubt genau einen Hostnamen.
func ExactHost(name string) func(string) bool { return ExactHosts(name) }

// ExactHosts lässt genau diese Hostnamen zu (etwa die Adresse des
// Leitstands und eine Testadresse während der Generalprobe).
func ExactHosts(names ...string) func(string) bool {
	ok := map[string]bool{}
	for _, n := range names {
		if n = strings.ToLower(strings.TrimSuffix(n, ".")); n != "" {
			ok[n] = true
		}
	}
	return func(host string) bool { return ok[host] }
}

func (f *Front) visitor(g Gate, r *http.Request) Visitor {
	v := Visitor{Gate: g.Name, Addr: g.Name}
	if g.Addr != nil {
		if a := g.Addr(r); a != "" {
			v.Addr = a
		}
	}
	return v
}

// unauthorized sagt der Seite, dass die Anmeldung fehlt, und ob sie ein
// Anmeldefeld zeigen soll.
func unauthorized(w http.ResponseWriter, g Gate) {
	writeJSON(w, http.StatusUnauthorized, map[string]any{"error": g.Hint, "anmelden": g.Password})
}

func (f *Front) auth(g Gate, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(maxBody)
		if r.URL.Path == UploadPath {
			limit = MaxUpload
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		if f.Bearer != "" {
			if got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && subtle.ConstantTimeCompare([]byte(got), []byte(f.Bearer)) == 1 {
				next.ServeHTTP(w, clean(r, "", f.visitor(g, r)))
				return
			}
		}
		value, moved := sessionCookie(r, g)
		if value == "" {
			unauthorized(w, g)
			return
		}
		if err := f.Door.Check(r.Context(), value, r.Header.Get(BackgroundHeader) == ""); err != nil {
			if !errors.Is(err, ErrSession) {
				f.logf("Anmeldung nicht geprüft: %v", err)
				// Der Grund (Socket, Pfad) steht im Log; im Leitstand ein Satz,
				// was los ist und was hilft (Labor S10).
				writeJSON(w, http.StatusBadGateway, apiError{Error: "Der HostDeck-Dienst antwortet gerade nicht; meist startet er nur neu und ist gleich wieder da. Hält es an: sudo systemctl status hostdeck-dienst."})
				return
			}
			unauthorized(w, g)
			return
		}
		if moved {
			f.moveCookie(w, g, value)
		}
		// Mit Cookie muss jede ändernde Anfrage sagen, woher sie kommt.
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("Origin") == "" {
			writeJSON(w, http.StatusForbidden, apiError{Error: "Anfrage ohne Herkunft abgewiesen."})
			return
		}
		if r.URL.Path == UploadPath {
			// Ein Update über eine langsame Leitung braucht länger als die
			// üblichen 30 Sekunden; erst nach geprüfter Anmeldung.
			rc := http.NewResponseController(w)
			_ = rc.SetReadDeadline(time.Now().Add(UploadTime))
			_ = rc.SetWriteDeadline(time.Now().Add(UploadTime + time.Minute))
		}
		if strings.HasPrefix(r.URL.Path, DownloadPath) && r.Method == http.MethodGet {
			w = &stallWriter{ResponseWriter: w, rc: http.NewResponseController(w)}
			w.(*stallWriter).extend()
		}
		next.ServeHTTP(w, clean(r, value, f.visitor(g, r)))
	})
}

// stallWriter schiebt die Frist zum Schreiben bei jedem Stück hinaus. So
// bricht ein langer Download nicht nach den üblichen zwei Minuten ab,
// wohl aber, wenn nichts mehr ankommt.
type stallWriter struct {
	http.ResponseWriter
	rc   *http.ResponseController
	last time.Time
}

func (s *stallWriter) extend() {
	now := time.Now()
	if now.Sub(s.last) < time.Second {
		return
	}
	s.last = now
	// Auch die Frist zum Lesen: Läuft sie ab, bricht Go die Anfrage ab,
	// obwohl der Browser nur auf Daten wartet.
	_ = s.rc.SetReadDeadline(now.Add(DownloadStall))
	_ = s.rc.SetWriteDeadline(now.Add(DownloadStall))
}

func (s *stallWriter) Write(p []byte) (int, error) {
	s.extend()
	return s.ResponseWriter.Write(p)
}

func (s *stallWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// clean entfernt Anmeldedaten und alles, was ein Besucher als Kopfzeile des
// Datenteils ausgeben könnte, und setzt die geprüften Werte.
func clean(r *http.Request, sid string, v Visitor) *http.Request {
	r2 := r.Clone(r.Context())
	for _, h := range []string{"Cookie", "Authorization", SessionHeader, AddrHeader, GateHeader} {
		r2.Header.Del(h)
	}
	if sid != "" {
		r2.Header.Set(SessionHeader, sid)
	}
	r2.Header.Set(AddrHeader, v.Addr)
	r2.Header.Set(GateHeader, v.Gate)
	return r2
}

// origin prüft, dass eine Anfrage sagt, woher sie kommt; guard hat schon
// geprüft, dass es die eigene Seite ist.
func origin(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Origin") == "" {
		writeJSON(w, http.StatusForbidden, apiError{Error: "Anfrage ohne Herkunft abgewiesen."})
		return false
	}
	return true
}

type redeemRequest struct {
	Token string `json:"token"`
}

func (f *Front) handleRedeem(g Gate) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !origin(w, r) {
			return
		}
		var req redeemRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil || req.Token == "" {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "Anfrage nicht lesbar."})
			return
		}
		v := f.visitor(g, r)
		if !f.allow(g, v) {
			writeJSON(w, http.StatusTooManyRequests, apiError{Error: "Zu viele Versuche. Bitte eine Minute warten."})
			return
		}
		id, err := f.Door.Redeem(r.Context(), req.Token, v)
		if err != nil {
			if !errors.Is(err, ErrBadLink) {
				f.logf("Link nicht geprüft: %v", err)
			}
			writeJSON(w, http.StatusForbidden, apiError{Error: "Der Link ist ungültig oder abgelaufen. " + g.Hint})
			return
		}
		f.setCookie(w, g, id, false)
		f.logf("Angemeldet über %s (Link)", g.Name)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

type loginRequest struct {
	Password string `json:"passwort"`
}

func (f *Front) handleLogin(g Gate) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !g.Password {
			writeJSON(w, http.StatusNotFound, apiError{Error: ErrNoLogin.Error()})
			return
		}
		if !origin(w, r) {
			return
		}
		var req loginRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "Anfrage nicht lesbar."})
			return
		}
		v := f.visitor(g, r)
		if !f.allow(g, v) {
			writeJSON(w, http.StatusTooManyRequests, apiError{Error: "Zu viele Versuche. Bitte eine Minute warten."})
			return
		}
		id, err := f.Door.Login(r.Context(), req.Password, v)
		var locked *LockedError
		switch {
		case err == nil:
		case errors.As(err, &locked):
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": locked.Error(), "warten": int(locked.Wait.Seconds())})
			return
		case errors.Is(err, ErrBadPassword):
			writeJSON(w, http.StatusUnauthorized, apiError{Error: ErrBadPassword.Error()})
			return
		case errors.Is(err, ErrNoPassword), errors.Is(err, ErrNoLogin):
			writeJSON(w, http.StatusConflict, apiError{Error: err.Error()})
			return
		default:
			f.logf("Anmeldung nicht geprüft: %v", err)
			writeJSON(w, http.StatusBadGateway, apiError{Error: "Anmelden ging gerade nicht: " + err.Error()})
			return
		}
		f.setCookie(w, g, id, false)
		f.logf("Angemeldet über %s (Passwort)", g.Name)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

type logoutRequest struct {
	All bool `json:"ueberall"`
}

func (f *Front) handleLogout(g Gate) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !origin(w, r) {
			return
		}
		var req logoutRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "Anfrage nicht lesbar."})
			return
		}
		c, err := r.Cookie(g.Cookie)
		if err == nil && c.Value != "" {
			// Überall abmelden nur mit gültiger Anmeldung.
			all := req.All && f.Door.Check(r.Context(), c.Value, true) == nil
			if req.All && !all {
				unauthorized(w, g)
				return
			}
			if err := f.Door.Logout(r.Context(), c.Value, all, f.visitor(g, r)); err != nil {
				f.logf("Abmelden: %v", err)
			}
		}
		f.setCookie(w, g, "", true)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func (f *Front) setCookie(w http.ResponseWriter, g Gate, value string, remove bool) {
	c := &http.Cookie{Name: g.Cookie, Value: value, Path: "/", HttpOnly: true, Secure: g.Secure, SameSite: http.SameSiteStrictMode}
	if remove {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}

func (f *Front) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func (f *Front) logf(format string, args ...any) {
	if f.Log == nil {
		return
	}
	fmt.Fprintf(f.Log, "%s  %s\n", f.now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// allow bremst Versuche mit Links und Passwörtern je Besucher: höchstens
// 20 je Minute und Adresse (bei IPv6 je /64-Netz). Je Eingang zu zählen
// hieße, dass ein Fremder mit Müll-Anfragen auch den Besitzer aussperrt.
// Die eigentliche Sperre nach Fehlversuchen führt der Dienst.
func (f *Front) allow(g Gate, v Visitor) bool {
	key := g.Name + " " + NetKey(v.Addr)
	now := f.now()
	f.mu.Lock()
	if f.lim == nil {
		f.lim = map[string]*limiter{}
	}
	l := f.lim[key]
	if l == nil {
		if len(f.lim) >= maxLimiters {
			for k, x := range f.lim {
				if x.idle(now) {
					delete(f.lim, k)
				}
			}
		}
		if len(f.lim) >= maxLimiters {
			// Sehr viele Adressen auf einmal: die Bremse des Dienstes
			// greift ohnehin; hier nur nicht unbegrenzt Speicher belegen.
			f.mu.Unlock()
			return false
		}
		l = &limiter{}
		f.lim[key] = l
	}
	f.mu.Unlock()
	return l.allow(now)
}

const maxLimiters = 4096

// NetKey fasst Adressen zusammen, die demselben Anschluss gehören: bei IPv6
// das /64-Netz, sonst die Adresse selbst.
func NetKey(addr string) string {
	ip := net.ParseIP(addr)
	if ip == nil || ip.To4() != nil {
		return addr
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// limiter zählt Versuche je Minute.
type limiter struct {
	mu    sync.Mutex
	start time.Time
	n     int
}

func (l *limiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.start) > time.Minute {
		l.start, l.n = now, 0
	}
	l.n++
	return l.n <= 20
}

func (l *limiter) idle(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return now.Sub(l.start) > time.Minute
}
