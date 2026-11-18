package melden

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// Web Push (RFC 8030, 8291, 8292): Der Leitstand auf dem Home-Bildschirm
// abonniert im Browser; HostDeck verschlüsselt jede Nachricht für das Gerät und
// schickt sie an den Push-Dienst des Herstellers (Apple, Google, Mozilla,
// Microsoft). Der sieht nur verschlüsselte Bytes.

// MaxDevices begrenzt die Geräte.
const MaxDevices = 20

// pushHosts sind die Push-Dienste der Browser. Andere Adressen nimmt HostDeck
// nicht an, damit niemand den Dienst (root) an beliebige Adressen
// schicken lässt.
var pushHosts = []string{"push.apple.com", "fcm.googleapis.com", "push.services.mozilla.com", "notify.windows.com"}

// Device ist ein Gerät, das Mitteilungen bekommt.
type Device struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Endpoint string    `json:"endpoint"`
	P256dh   string    `json:"p256dh"`
	Auth     string    `json:"auth"`
	Added    time.Time `json:"seit"`
}

// Push ist der Teil der Einstellungen für Web Push.
type Push struct {
	Key     string    `json:"vapid"` // privater Schlüssel, PKCS#8, base64
	Devices []*Device `json:"geraete,omitempty"`
}

// DeviceView zeigt ein Gerät ohne Schlüssel.
type DeviceView struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Service string     `json:"dienst"` // Apple, Google …
	Added   time.Time  `json:"seit"`
	LastOK  *time.Time `json:"zuletzt_ok,omitempty"`
	Error   string     `json:"fehler,omitempty"`
}

// PushView ist der Stand für den Leitstand.
type PushView struct {
	Possible  bool         `json:"moeglich"`
	Why       string       `json:"grund,omitempty"`
	PublicKey string       `json:"schluessel,omitempty"` // applicationServerKey
	Devices   []DeviceView `json:"geraete"`
}

type deviceState struct {
	ok  time.Time
	err string
}

// Subscription ist, was der Browser beim Abonnieren liefert.
type Subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

var b64 = base64.RawURLEncoding

func decodeB64(s string) ([]byte, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	if b, err := b64.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}

// CheckEndpoint lässt nur Push-Dienste der Browser zu.
func CheckEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Port() != "" {
		return errors.New("Der Browser hat eine ungültige Push-Adresse geliefert.")
	}
	h := strings.ToLower(u.Hostname())
	for _, p := range pushHosts {
		if h == p || strings.HasSuffix(h, "."+p) {
			return nil
		}
	}
	return fmt.Errorf("Push-Dienst %s ist HostDeck nicht bekannt.", h)
}

func serviceName(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	switch {
	case strings.HasSuffix(h, "push.apple.com"):
		return "Apple"
	case strings.HasSuffix(h, "googleapis.com"):
		return "Google"
	case strings.HasSuffix(h, "mozilla.com"):
		return "Mozilla"
	case strings.HasSuffix(h, "windows.com"):
		return "Microsoft"
	}
	return h
}

func deviceID(endpoint string) string {
	h := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(h[:6])
}

// cleanName macht aus dem Gerätenamen eine kurze, harmlose Zeile.
// CleanName ist der Gerätename so, wie HostDeck ihn speichert und ins
// Protokoll schreibt: ohne Steuerzeichen, gekürzt.
func CleanName(s string) string { return cleanName(s) }

func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > 40 {
		s = string(r[:40])
	}
	if s == "" {
		s = "Gerät"
	}
	return s
}

// vapidKey liest oder erzeugt den Schlüssel. Aufrufer hält s.mu.
func (s *Service) vapidKey() (*ecdsa.PrivateKey, error) {
	if s.cfg.Push != nil && s.cfg.Push.Key != "" {
		der, err := base64.StdEncoding.DecodeString(s.cfg.Push.Key)
		if err != nil {
			return nil, err
		}
		k, err := x509.ParsePKCS8PrivateKey(der)
		if err != nil {
			return nil, err
		}
		if ek, ok := k.(*ecdsa.PrivateKey); ok {
			return ek, nil
		}
		return nil, errors.New("VAPID-Schlüssel hat die falsche Art")
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}
	if s.cfg.Push == nil {
		s.cfg.Push = &Push{}
	}
	s.cfg.Push.Key = base64.StdEncoding.EncodeToString(der)
	if err := s.save(); err != nil {
		s.cfg.Push.Key = ""
		return nil, err
	}
	return k, nil
}

func publicKeyBytes(k *ecdsa.PrivateKey) []byte {
	pk, err := k.PublicKey.ECDH()
	if err != nil {
		return nil
	}
	return pk.Bytes() // 65 Byte, unkomprimiert
}

// pushView ist der Stand für Web Push. Aufrufer hält s.mu.
func (s *Service) pushView() *PushView {
	v := &PushView{Devices: []DeviceView{}}
	base := ""
	if s.Base != nil {
		base = s.Base()
	}
	if base == "" {
		v.Why = "Mitteilungen gehen nur über die freigeschaltete Adresse des Leitstands (mit https). Schalte ihn unter „Von außen“ frei."
	} else if k, err := s.vapidKey(); err != nil {
		v.Why = "Schlüssel für Mitteilungen nicht anlegbar: " + err.Error()
	} else {
		v.Possible = true
		v.PublicKey = b64.EncodeToString(publicKeyBytes(k))
	}
	if s.cfg.Push != nil {
		for _, d := range s.cfg.Push.Devices {
			dv := DeviceView{ID: d.ID, Name: d.Name, Service: serviceName(d.Endpoint), Added: d.Added}
			if st, ok := s.devState[d.ID]; ok {
				if !st.ok.IsZero() {
					t := st.ok
					dv.LastOK = &t
				}
				dv.Error = st.err
			}
			v.Devices = append(v.Devices, dv)
		}
	}
	return v
}

// Subscribe trägt ein Gerät ein (oder erneuert es).
func (s *Service) Subscribe(sub Subscription, name string) (View, string, error) {
	if err := CheckEndpoint(sub.Endpoint); err != nil {
		return View{}, "", err
	}
	p, err := decodeB64(sub.Keys.P256dh)
	if err != nil || len(p) != 65 || p[0] != 4 {
		return View{}, "", errors.New("Der Browser hat einen ungültigen Schlüssel geliefert.")
	}
	if _, err := ecdh.P256().NewPublicKey(p); err != nil {
		return View{}, "", errors.New("Der Browser hat einen ungültigen Schlüssel geliefert.")
	}
	a, err := decodeB64(sub.Keys.Auth)
	if err != nil || len(a) != 16 {
		return View{}, "", errors.New("Der Browser hat einen ungültigen Schlüssel geliefert.")
	}
	s.mu.Lock()
	s.load()
	if s.Base == nil || s.Base() == "" {
		s.mu.Unlock()
		return View{}, "", errors.New("Mitteilungen gehen nur über die freigeschaltete Adresse des Leitstands.")
	}
	if _, err := s.vapidKey(); err != nil {
		s.mu.Unlock()
		return View{}, "", err
	}
	old := append([]*Device(nil), s.cfg.Push.Devices...)
	d := &Device{ID: deviceID(sub.Endpoint), Name: cleanName(name), Endpoint: sub.Endpoint,
		P256dh: b64.EncodeToString(p), Auth: b64.EncodeToString(a), Added: s.now()}
	list := []*Device{}
	for _, x := range s.cfg.Push.Devices {
		if x.ID != d.ID {
			list = append(list, x)
		}
	}
	if len(list) >= MaxDevices {
		s.mu.Unlock()
		return View{}, "", fmt.Errorf("Es sind schon %d Geräte eingetragen; entferne erst eins.", MaxDevices)
	}
	s.cfg.Push.Devices = append(list, d)
	if err := s.save(); err != nil {
		s.cfg.Push.Devices = old
		s.mu.Unlock()
		return View{}, "", err
	}
	s.mu.Unlock()
	return s.View(), d.ID, nil
}

// RemoveDevice trägt ein Gerät aus. Den Namen gibt es fürs Protokoll.
func (s *Service) RemoveDevice(id string) (View, string, error) {
	s.mu.Lock()
	s.load()
	if s.cfg.Push == nil {
		s.mu.Unlock()
		return View{}, "", errors.New("Dieses Gerät ist nicht eingetragen.")
	}
	old := s.cfg.Push.Devices
	var keep []*Device
	name := ""
	for _, d := range old {
		if d.ID == id {
			name = d.Name
			continue
		}
		keep = append(keep, d)
	}
	if name == "" {
		s.mu.Unlock()
		return View{}, "", errors.New("Dieses Gerät ist nicht eingetragen.")
	}
	s.cfg.Push.Devices = keep
	if err := s.save(); err != nil {
		s.cfg.Push.Devices = old
		s.mu.Unlock()
		return View{}, "", err
	}
	delete(s.devState, id)
	s.mu.Unlock()
	return s.View(), name, nil
}

// pushPayload ist, was der Service Worker im Browser bekommt.
type pushPayload struct {
	Title string `json:"titel"`
	Text  string `json:"text"`
	URL   string `json:"link,omitempty"`
	Tag   string `json:"tag,omitempty"`
}

// sendPush verschlüsselt und schickt eine Nachricht an ein Gerät.
func (s *Service) sendPush(ctx context.Context, key *ecdsa.PrivateKey, sub string, d Device, m Message) error {
	payload, _ := json.Marshal(pushPayload{Title: m.Title, Text: m.Text, URL: m.Click, Tag: m.Tag})
	body, err := encrypt(payload, d.P256dh, d.Auth, nil, nil)
	if err != nil {
		return &permanentError{"Schlüssel des Geräts unbrauchbar: " + err.Error()}
	}
	u, err := url.Parse(d.Endpoint)
	if err != nil {
		return &permanentError{"Push-Adresse ungültig"}
	}
	jwt, err := vapidJWT(key, u.Scheme+"://"+u.Host, sub, s.now())
	if err != nil {
		return &permanentError{err.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, sendWait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.Endpoint, bytes.NewReader(body))
	if err != nil {
		return &permanentError{"Push-Adresse ungültig"}
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", "86400")
	urgency := "normal"
	if m.Priority >= 4 {
		urgency = "high"
	} else if m.Priority > 0 && m.Priority <= 2 {
		urgency = "low"
	}
	req.Header.Set("Urgency", urgency)
	if m.Tag != "" {
		req.Header.Set("Topic", topicHeader(m.Tag))
	}
	req.Header.Set("Authorization", "vapid t="+jwt+", k="+b64.EncodeToString(publicKeyBytes(key)))
	resp, err := s.client().Do(req)
	if err != nil {
		return fmt.Errorf("%s antwortet nicht: %v", serviceName(d.Endpoint), shortErr(err))
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode/100 == 2 {
		return nil
	}
	text := fmt.Sprintf("Push-Dienst von %s meldet Status %d", serviceName(d.Endpoint), resp.StatusCode)
	if r := strings.TrimSpace(string(msg)); r != "" && len(r) < 200 && !strings.ContainsAny(r, "<>") {
		text += " (" + r + ")"
	}
	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusGone:
		return &goneError{text}
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestEntityTooLarge:
		return &permanentError{text}
	}
	return errors.New(text)
}

// goneError: Das Gerät hat das Abo beendet (App gelöscht, Mitteilungen
// abgeschaltet); HostDeck trägt es aus.
type goneError struct{ msg string }

func (e *goneError) Error() string { return e.msg }

// topicHeader macht aus dem Schlüssel einer Warnung einen gültigen
// Topic-Header (höchstens 32 Zeichen aus dem URL-Base64-Alphabet). Eine
// neue Nachricht zum selben Anlass ersetzt dann die alte, falls sie noch
// beim Push-Dienst wartet.
func topicHeader(tag string) string {
	h := sha256.Sum256([]byte(tag))
	return b64.EncodeToString(h[:])[:32]
}

// vapidJWT unterschreibt den Nachweis für den Push-Dienst (RFC 8292).
func vapidJWT(key *ecdsa.PrivateKey, aud, sub string, now time.Time) (string, error) {
	head := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{"aud": aud, "exp": now.Add(12 * time.Hour).Unix(), "sub": sub})
	in := head + "." + b64.EncodeToString(claims)
	h := sha256.Sum256([]byte(in))
	r, sg, err := ecdsa.Sign(rand.Reader, key, h[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	sg.FillBytes(sig[32:])
	return in + "." + b64.EncodeToString(sig), nil
}

// encrypt verschlüsselt nach RFC 8291 (aes128gcm, ein Datensatz). Für
// Tests lassen sich Absender-Schlüssel und Salz vorgeben.
func encrypt(plain []byte, p256dh, auth string, as *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	uaRaw, err := decodeB64(p256dh)
	if err != nil {
		return nil, err
	}
	ua, err := ecdh.P256().NewPublicKey(uaRaw)
	if err != nil {
		return nil, err
	}
	secret, err := decodeB64(auth)
	if err != nil || len(secret) != 16 {
		return nil, errors.New("auth-Schlüssel ungültig")
	}
	if as == nil {
		if as, err = ecdh.P256().GenerateKey(rand.Reader); err != nil {
			return nil, err
		}
	}
	if salt == nil {
		salt = make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return nil, err
		}
	}
	shared, err := as.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPub := as.PublicKey().Bytes()
	prkKey, err := hkdf.Extract(sha256.New, shared, secret)
	if err != nil {
		return nil, err
	}
	info := "WebPush: info\x00" + string(uaRaw) + string(asPub)
	ikm, err := hkdf.Expand(sha256.New, prkKey, info, 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	blk, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	const rs = 4096
	if len(plain)+1+16 > rs {
		return nil, errors.New("Nachricht zu lang")
	}
	rec := append(append([]byte(nil), plain...), 2) // letzter Datensatz
	var out bytes.Buffer
	out.Write(salt)
	_ = binary.Write(&out, binary.BigEndian, uint32(rs))
	out.WriteByte(byte(len(asPub)))
	out.Write(asPub)
	out.Write(gcm.Seal(nil, nonce, rec, nil))
	return out.Bytes(), nil
}
