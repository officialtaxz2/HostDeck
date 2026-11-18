package web

import (
	"net/http"
	"strings"
)

// Umstieg von sbm auf HostDeck: Bis 1.0 hieß das Anmelde-Cookie __Host-sbm
// (am Notausgang sbm). Wer beim Umstieg angemeldet war, bleibt es: Das alte
// Cookie gilt einmal und wird durch das neue ersetzt. Die Anmeldung selbst
// prüft der Dienst wie immer. Kann weg, sobald keine Installation von sbm
// mehr läuft.

// legacyCookie ist der frühere Name des Cookies.
func legacyCookie(name string) string { return strings.Replace(name, "hostdeck", "sbm", 1) }

// sessionCookie liest das Anmelde-Cookie; moved: Es kam unter dem alten
// Namen.
func sessionCookie(r *http.Request, g Gate) (value string, moved bool) {
	if c, err := r.Cookie(g.Cookie); err == nil && c.Value != "" {
		return c.Value, false
	}
	if old := legacyCookie(g.Cookie); old != g.Cookie {
		if c, err := r.Cookie(old); err == nil && c.Value != "" {
			return c.Value, true
		}
	}
	return "", false
}

// moveCookie setzt das neue Cookie und löscht das alte.
func (f *Front) moveCookie(w http.ResponseWriter, g Gate, value string) {
	f.setCookie(w, g, value, false)
	http.SetCookie(w, &http.Cookie{Name: legacyCookie(g.Cookie), Value: "", Path: "/", HttpOnly: true, Secure: g.Secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}
