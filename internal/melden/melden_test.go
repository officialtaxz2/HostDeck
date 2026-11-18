package melden

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// decrypt ist die Seite des Browsers (RFC 8291), nur für Tests.
func decrypt(t *testing.T, body []byte, ua *ecdh.PrivateKey, auth []byte) []byte {
	t.Helper()
	salt, rs, idlen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	if rs != 4096 || idlen != 65 {
		t.Fatalf("Kopf: rs %d, idlen %d", rs, idlen)
	}
	asPub, ct := body[21:21+idlen], body[21+idlen:]
	pk, err := ecdh.P256().NewPublicKey(asPub)
	if err != nil {
		t.Fatal(err)
	}
	shared, _ := ua.ECDH(pk)
	prkKey, _ := hkdf.Extract(sha256.New, shared, auth)
	ikm, _ := hkdf.Expand(sha256.New, prkKey, "WebPush: info\x00"+string(ua.PublicKey().Bytes())+string(asPub), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	blk, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(blk)
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		t.Fatalf("entschlüsseln: %v", err)
	}
	if plain[len(plain)-1] != 2 {
		t.Fatalf("Trenner fehlt: %v", plain[len(plain)-5:])
	}
	return plain[:len(plain)-1]
}

type browser struct {
	key  *ecdh.PrivateKey
	auth []byte
}

func newBrowser(t *testing.T) browser {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	a := make([]byte, 16)
	_, _ = rand.Read(a)
	return browser{k, a}
}

func (b browser) sub(endpoint string) Subscription {
	var s Subscription
	s.Endpoint = endpoint
	s.Keys.P256dh = b64.EncodeToString(b.key.PublicKey().Bytes())
	s.Keys.Auth = b64.EncodeToString(b.auth)
	return s
}

func TestVerschluesseln(t *testing.T) {
	br := newBrowser(t)
	s := br.sub("https://web.push.apple.com/x")
	msg := []byte(`{"titel":"Platte fast voll","text":"ä ö ü"}`)
	body, err := encrypt(msg, s.Keys.P256dh, s.Keys.Auth, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := decrypt(t, body, br.key, br.auth); string(got) != string(msg) {
		t.Errorf("zurück: %s", got)
	}
	if _, err := encrypt(make([]byte, 5000), s.Keys.P256dh, s.Keys.Auth, nil, nil); err == nil {
		t.Error("zu lange Nachricht ohne Fehler")
	}
}

func verifyJWT(t *testing.T, jwt string, pub *ecdsa.PublicKey) map[string]any {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT: %s", jwt)
	}
	sig, _ := b64.DecodeString(parts[2])
	h := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(pub, h[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("Unterschrift ungültig")
	}
	var c map[string]any
	raw, _ := b64.DecodeString(parts[1])
	_ = json.Unmarshal(raw, &c)
	return c
}

func TestAdressen(t *testing.T) {
	for raw, want := range map[string]string{"": DefaultServer, "ntfy.example.de": "https://ntfy.example.de", "https://ntfy.example.de/": "https://ntfy.example.de", "https://x.example/ntfy": "https://x.example/ntfy"} {
		if got, err := CheckServer(raw); err != nil || got != want {
			t.Errorf("CheckServer(%q) = %q, %v", raw, got, err)
		}
	}
	for _, bad := range []string{"http://ntfy.example.de", "https://a:b@ntfy.example.de", "https://x.example/?a=1", "https://"} {
		if _, err := CheckServer(bad); err == nil {
			t.Errorf("CheckServer(%q) ohne Fehler", bad)
		}
	}
	for _, ok := range []string{"https://web.push.apple.com/QG", "https://fcm.googleapis.com/fcm/send/x", "https://updates.push.services.mozilla.com/wpush/v2/x", "https://wns2-par02p.notify.windows.com/w/?token=x"} {
		if err := CheckEndpoint(ok); err != nil {
			t.Errorf("CheckEndpoint(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"https://evil.example/apple", "http://web.push.apple.com/x", "https://web.push.apple.com:8443/x", "https://push.apple.com.evil.example/x", "https://127.0.0.1/x"} {
		if err := CheckEndpoint(bad); err == nil {
			t.Errorf("CheckEndpoint(%q) ohne Fehler", bad)
		}
	}
	if m := maskURL("https://hc-ping.com/0f8c2f2a-1234-4cde-9abc-1234567890ab"); m != "https://hc-ping.com/…90ab" {
		t.Errorf("maskURL: %s", m)
	}
}

type notes struct {
	mu sync.Mutex
	l  []string
}

func (n *notes) add(text string, _ []string) {
	n.mu.Lock()
	n.l = append(n.l, text)
	n.mu.Unlock()
}

func (n *notes) take() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := strings.Join(n.l, " | ")
	n.l = nil
	return s
}

func TestNtfyUndHerzschlag(t *testing.T) {
	var mu sync.Mutex
	var got []map[string]any
	var auth []string
	fail := 0
	beats := 0
	beatFail := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/ping/abc123" {
			beats++
			if beatFail {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			return
		}
		if fail > 0 {
			fail--
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if r.Header.Get("Authorization") == "Bearer falsch" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"unauthorized"}`)
			return
		}
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		got = append(got, m)
		auth = append(auth, r.Header.Get("Authorization"))
	}))
	defer srv.Close()
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	file := filepath.Join(t.TempDir(), "melden.json")
	n := &notes{}
	s := &Service{File: file, Client: srv.Client(), Note: n.add, Now: func() time.Time { return now }}
	ctx := context.Background()

	// Ohne Kanal passiert nichts.
	s.Send(Message{Title: "x"})
	if v := s.View(); v.Pending != 0 || v.Ntfy != nil {
		t.Errorf("ohne Kanal: %+v", v)
	}
	v, err := s.SetNtfy(srv.URL, "geheim", false)
	if err != nil || v.Ntfy == nil || !strings.HasPrefix(v.Ntfy.Topic, "hostdeck-") || len(v.Ntfy.Topic) != 31 || !v.Ntfy.WithAuth {
		t.Fatalf("SetNtfy: %+v %v", v.Ntfy, err)
	}
	topic := v.Ntfy.Topic
	if b, _ := json.Marshal(v); strings.Contains(string(b), "geheim") {
		t.Errorf("Token im Leitstand: %s", b)
	}
	if fi, err := os.Stat(file); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("Datei: %v %v", fi, err)
	}
	if !strings.HasPrefix(v.Ntfy.App, "ntfy://127.0.0.1:") || !strings.HasSuffix(v.Ntfy.Web, "/"+topic) {
		t.Errorf("Links: %+v", v.Ntfy)
	}
	// Ohne neues Thema bleibt es.
	if v, _ := s.SetNtfy(srv.URL, "geheim", false); v.Ntfy.Topic != topic {
		t.Error("Thema gewechselt")
	}

	// Zustellen, beim zweiten Versuch.
	mu.Lock()
	fail = 1
	mu.Unlock()
	s.Send(Message{Title: "Warnung: Platte fast voll", Text: "91 % belegt", Priority: 4, Tags: []string{"warning"}, Click: "https://hostdeck.example.de/#/ueberwachung"})
	s.step(ctx)
	if v := s.View(); v.Pending != 1 || v.Last != nil {
		t.Errorf("nach Fehlversuch: %+v %+v", v, v.Last)
	}
	s.step(ctx) // noch nicht fällig
	now = now.Add(31 * time.Second)
	s.step(ctx)
	mu.Lock()
	if len(got) != 1 || got[0]["topic"] != topic || got[0]["title"] != "Warnung: Platte fast voll" || got[0]["priority"] != float64(4) || got[0]["click"] == nil || auth[0] != "Bearer geheim" {
		t.Errorf("ntfy bekam: %v %v", got, auth)
	}
	mu.Unlock()
	if v := s.View(); v.Pending != 0 || v.Last == nil || !v.Last.OK || v.Last.Tries != 2 {
		t.Errorf("zugestellt: %+v", v.Last)
	}
	if k := n.take(); !strings.Contains(k, "zugestellt nach 2 Versuchen") {
		t.Errorf("Protokoll: %s", k)
	}

	// Nach einer Stunde gibt HostDeck auf.
	mu.Lock()
	fail = 1000
	mu.Unlock()
	s.Send(Message{Title: "Seite antwortet nicht"})
	for i := 0; i < 12 && s.View().Pending > 0; i++ {
		s.step(ctx)
		now = now.Add(15 * time.Minute)
	}
	if v := s.View(); v.Pending != 0 || v.Last.OK {
		t.Errorf("aufgegeben: %+v", v)
	}
	if k := n.take(); !strings.Contains(k, "aufgegeben: Seite antwortet nicht") {
		t.Errorf("Protokoll: %s", k)
	}
	mu.Lock()
	fail = 0
	mu.Unlock()

	// Falsches Token: kein weiterer Versuch.
	if _, err := s.SetNtfy(srv.URL, "falsch", false); err != nil {
		t.Fatal(err)
	}
	res, err := s.Test(ctx, Message{Title: "Probe"}, "")
	if err != nil || len(res) != 1 || res[0].OK || !strings.Contains(res[0].Error, "Token") {
		t.Errorf("Probe mit falschem Token: %+v %v", res, err)
	}
	n.take()

	// Herzschlag: sofort, Ausfall und Rückkehr im Protokoll.
	if _, err := s.SetBeat("http://x.example/ping"); err == nil {
		t.Error("http ohne Fehler")
	}
	if _, err := s.SetBeat(srv.URL + "/ping/abc123"); err != nil {
		t.Fatal(err)
	}
	s.step(ctx)
	if v := s.View(); v.Beat == nil || v.Beat.LastOK == nil || v.Beat.Error != "" || strings.Contains(v.Beat.Address, "abc123") {
		t.Errorf("Herzschlag: %+v", v.Beat)
	}
	if k := n.take(); !strings.Contains(k, "der erste ist angekommen") {
		t.Errorf("Protokoll: %s", k)
	}
	mu.Lock()
	beatFail = true
	mu.Unlock()
	s.beatDue = true
	s.step(ctx)
	s.beatDue = true
	s.step(ctx)
	if k := n.take(); k != "Herzschlag nicht angekommen" {
		t.Errorf("Ausfall einmal: %q", k)
	}
	mu.Lock()
	beatFail = false
	mu.Unlock()
	s.beatDue = true
	s.step(ctx)
	if k := n.take(); k != "Herzschlag kommt wieder an" {
		t.Errorf("Rückkehr: %q", k)
	}

	// Neu laden: Einstellungen bleiben.
	s2 := &Service{File: file}
	if v := s2.View(); v.Ntfy == nil || v.Ntfy.Topic != topic || v.Beat == nil {
		t.Errorf("neu geladen: %+v", v)
	}
	if v, err := s2.Disable("ntfy"); err != nil || v.Ntfy != nil {
		t.Errorf("abschalten: %+v %v", v, err)
	}
	if v, err := s2.Disable("herzschlag"); err != nil || v.Beat != nil {
		t.Errorf("abschalten: %+v %v", v, err)
	}
}

func TestWebPush(t *testing.T) {
	br := newBrowser(t)
	var mu sync.Mutex
	var bodies [][]byte
	var heads []http.Header
	status := http.StatusCreated
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		bodies = append(bodies, b)
		heads = append(heads, r.Header.Clone())
		w.WriteHeader(status)
	}))
	defer srv.Close()
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	base := ""
	n := &notes{}
	s := &Service{File: filepath.Join(t.TempDir(), "melden.json"), Client: srv.Client(), Note: n.add, Now: func() time.Time { return now }, Base: func() string { return base }}
	ctx := context.Background()

	// Ohne freigeschaltete Adresse geht es nicht.
	if v := s.View(); v.Push.Possible || v.Push.Why == "" {
		t.Errorf("ohne Adresse: %+v", v.Push)
	}
	if _, _, err := s.Subscribe(br.sub("https://web.push.apple.com/abc"), "iPhone"); err == nil {
		t.Error("Abo ohne Adresse")
	}
	base = "https://hostdeck.example.de"
	v := s.View()
	if !v.Push.Possible || v.Push.PublicKey == "" {
		t.Fatalf("mit Adresse: %+v", v.Push)
	}
	pub, _ := decodeB64(v.Push.PublicKey)
	if len(pub) != 65 {
		t.Fatalf("Schlüssel: %d Byte", len(pub))
	}
	// Fremde Adressen nimmt HostDeck nicht an.
	if _, _, err := s.Subscribe(br.sub(srv.URL+"/push"), "iPhone"); err == nil {
		t.Error("fremde Push-Adresse angenommen")
	}
	bad := br.sub("https://web.push.apple.com/abc")
	bad.Keys.Auth = "kurz"
	if _, _, err := s.Subscribe(bad, "iPhone"); err == nil {
		t.Error("kaputter Schlüssel angenommen")
	}
	v, id, err := s.Subscribe(br.sub("https://web.push.apple.com/abc"), "  iPhone von Demo\n")
	if err != nil || len(v.Push.Devices) != 1 || v.Push.Devices[0].Name != "iPhone von Demo" || v.Push.Devices[0].Service != "Apple" {
		t.Fatalf("Abo: %+v %v", v.Push, err)
	}
	if b, _ := json.Marshal(v); strings.Contains(string(b), "web.push.apple.com/abc") || strings.Contains(string(b), br.sub("").Keys.Auth) {
		t.Errorf("Geheimes im Leitstand: %s", b)
	}
	// Für den Test zeigt das Gerät auf den Testserver.
	s.mu.Lock()
	s.cfg.Push.Devices[0].Endpoint = srv.URL + "/push/abc"
	s.mu.Unlock()

	s.Send(Message{Title: "Warnung: Platte fast voll", Text: "91 % belegt", Priority: 4, Click: "https://hostdeck.example.de/#/ueberwachung", Tag: "platz:/"})
	s.step(ctx)
	mu.Lock()
	if len(bodies) != 1 {
		t.Fatalf("Push: %d", len(bodies))
	}
	h := heads[0]
	if h.Get("Content-Encoding") != "aes128gcm" || h.Get("TTL") == "" || h.Get("Urgency") != "high" || len(h.Get("Topic")) != 32 {
		t.Errorf("Kopf: %v", h)
	}
	var p pushPayload
	if err := json.Unmarshal(decrypt(t, bodies[0], br.key, br.auth), &p); err != nil || p.Title != "Warnung: Platte fast voll" || p.URL == "" {
		t.Errorf("Inhalt: %+v %v", p, err)
	}
	a := h.Get("Authorization")
	mu.Unlock()
	if !strings.HasPrefix(a, "vapid t=") || !strings.Contains(a, ", k="+v.Push.PublicKey) {
		t.Fatalf("Authorization: %s", a)
	}
	jwt := strings.TrimSuffix(strings.TrimPrefix(a, "vapid t="), ", k="+v.Push.PublicKey)
	s.mu.Lock()
	key, _ := s.vapidKey()
	s.mu.Unlock()
	c := verifyJWT(t, jwt, &key.PublicKey)
	if c["aud"] != srv.URL || c["sub"] != "https://hostdeck.example.de" {
		t.Errorf("JWT: %v", c)
	}
	if v := s.View(); v.Push.Devices[0].LastOK == nil {
		t.Errorf("Gerät: %+v", v.Push.Devices[0])
	}

	// Probe nur an dieses Gerät.
	res, err := s.Test(ctx, Message{Title: "Probe"}, id)
	if err != nil || len(res) != 1 || !res[0].OK || res[0].Target != "iPhone von Demo" {
		t.Errorf("Probe: %+v %v", res, err)
	}
	n.take()

	// 410: Gerät hat das Abo beendet; HostDeck trägt es aus.
	mu.Lock()
	status = http.StatusGone
	mu.Unlock()
	s.Send(Message{Title: "Entwarnung"})
	s.step(ctx)
	if v := s.View(); len(v.Push.Devices) != 0 || v.Pending != 0 {
		t.Errorf("nach 410: %+v", v)
	}
	if k := n.take(); !strings.Contains(k, "ausgetragen") {
		t.Errorf("Protokoll: %s", k)
	}
	if _, _, err := s.RemoveDevice(id); err == nil {
		t.Error("entfernen eines fehlenden Geräts ohne Fehler")
	}
}

func TestStillAufProbeKopie(t *testing.T) {
	hits := 0
	var mu sync.Mutex
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
	}))
	defer srv.Close()
	quiet := true
	s := &Service{File: filepath.Join(t.TempDir(), "melden.json"), Client: srv.Client(), Quiet: func() bool { return quiet }}
	if _, err := s.SetNtfy(srv.URL, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetBeat(srv.URL + "/ping/abc"); err != nil {
		t.Fatal(err)
	}
	s.Send(Message{Title: "Warnung"})
	s.mu.Lock()
	s.beatDue = true
	s.mu.Unlock()
	s.step(context.Background())
	mu.Lock()
	n := hits
	mu.Unlock()
	if v := s.View(); v.Pending != 0 || n != 0 {
		t.Fatalf("still: %d wartend, %d Aufrufe", v.Pending, n)
	}
	quiet = false
	s.Send(Message{Title: "Warnung"})
	if v := s.View(); v.Pending != 1 {
		t.Fatalf("laut: %d wartend", v.Pending)
	}
}
