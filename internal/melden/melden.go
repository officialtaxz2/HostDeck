// Package melden schickt Warnungen nach außen: Nachrichten
// über ntfy aufs Handy und einen Herzschlag an einen Dienst wie
// healthchecks.io, der sich meldet, wenn der ganze Server ausfällt.
//
// Einrichten und Ändern geht nur mit Passwort; das prüft der Dienst.
// Die Einstellungen liegen nur für root lesbar auf dem Server. Ein Token
// für einen eigenen ntfy-Server erscheint nie im Leitstand; das Thema
// schon, weil du es zum Abonnieren brauchst.
package melden

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultServer ist der öffentliche ntfy-Dienst.
const DefaultServer = "https://ntfy.sh"

// Abstände und Grenzen.
const (
	BeatEvery = 5 * time.Minute  // Herzschlag
	GiveUp    = time.Hour        // so lange versucht es eine Nachricht
	MaxQueue  = 100              // so viele warten höchstens
	sendWait  = 15 * time.Second // je Versuch
)

// retryAfter sind die Abstände der Wiederholungen; danach alle 15 Minuten.
var retryAfter = []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute}

// Ntfy ist der Kanal aufs Handy.
type Ntfy struct {
	Server string `json:"server"`
	Topic  string `json:"thema"`
	Token  string `json:"token,omitempty"`
}

// Beat ist der Herzschlag nach außen.
type Beat struct {
	URL string `json:"adresse"`
}

// Config steht in der Datei.
type Config struct {
	Push *Push `json:"web_push,omitempty"`
	Ntfy *Ntfy `json:"ntfy,omitempty"`
	Beat *Beat `json:"herzschlag,omitempty"`
}

// Message ist eine Nachricht. Text enthält nur den Anlass, nie Inhalte von
// Projekten oder Zugangsdaten.
type Message struct {
	Title    string
	Text     string
	Priority int      // 1 (leise) bis 5 (dringend); 0 = 3
	Tags     []string // ntfy zeigt bekannte Namen als Emoji
	Click    string   // Link beim Antippen
	Tag      string   // Anlass; eine neue Nachricht dazu ersetzt die alte
}

// Delivery ist das Ergebnis einer Nachricht.
type Delivery struct {
	At    time.Time `json:"zeit"`
	Title string    `json:"titel"`
	OK    bool      `json:"ok"`
	Error string    `json:"fehler,omitempty"`
	Tries int       `json:"versuche"`
}

// NtfyView zeigt den Kanal im Leitstand, ohne Token.
type NtfyView struct {
	Server   string `json:"server"`
	Host     string `json:"host"`
	Topic    string `json:"thema"`
	Web      string `json:"web"` // Adresse zum Abonnieren im Browser
	App      string `json:"app"` // öffnet die App direkt (ntfy://…)
	Default  bool   `json:"oeffentlich"`
	WithAuth bool   `json:"mit_token"`
}

// BeatView zeigt den Herzschlag, mit gekürzter Adresse.
type BeatView struct {
	Address string     `json:"adresse"`
	Last    *time.Time `json:"zuletzt,omitempty"`
	LastOK  *time.Time `json:"zuletzt_ok,omitempty"`
	Error   string     `json:"fehler,omitempty"`
	Every   int        `json:"abstand_minuten"`
}

// View ist der Stand für den Leitstand.
type View struct {
	Push    *PushView `json:"web_push"`
	Ntfy    *NtfyView `json:"ntfy,omitempty"`
	Beat    *BeatView `json:"herzschlag,omitempty"`
	Pending int       `json:"ausstehend"`
	Last    *Delivery `json:"zuletzt,omitempty"`
}

// Service verschickt Nachrichten und den Herzschlag.
type Service struct {
	File   string       // Einstellungen; leer: nur im Speicher
	Client *http.Client // Standard: eigener Client ohne Weiterleitungen
	// Note bekommt jedes Ergebnis fürs Protokoll (zugestellt, aufgegeben,
	// Herzschlag ausgefallen oder wieder da).
	Note func(text string, details []string)
	Now  func() time.Time
	// Base ist die freigeschaltete Adresse des Leitstands
	// (https://hostdeck.example.de) oder leer. Ohne sie gibt es kein Web Push.
	Base func() string
	// Quiet: Dieser Server schweigt (Probe-Kopie eines Leitstands auf einem
	// neuen Server). Warnungen und Herzschlag gehen dann nicht hinaus, nur
	// eine Probe-Nachricht auf Knopfdruck. nil: nie still.
	Quiet func() bool

	mu       sync.Mutex
	devState map[string]deviceState
	loaded   bool
	cfg      Config
	queue    []*item
	wake     chan struct{}
	last     *Delivery
	beatAt   time.Time
	beatOK   time.Time
	beatErr  string
	beatDue  bool
	beatNew  bool // gerade eingerichtet: den ersten Erfolg ins Protokoll
}

type item struct {
	to    string // "ntfy" oder "push:<id>"
	name  string // Ziel fürs Protokoll (nur Probe)
	msg   Message
	added time.Time
	next  time.Time
	tries int
	err   string
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) quiet() bool { return s.Quiet != nil && s.Quiet() }

func (s *Service) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{
		Timeout: sendWait,
		// Eine Weiterleitung führt woandershin, als du eingetragen hast.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (s *Service) note(text string, details ...string) {
	if s.Note != nil {
		s.Note(text, details)
	}
}

// load liest die Einstellungen einmal. Aufrufer hält s.mu.
func (s *Service) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	if s.wake == nil {
		s.wake = make(chan struct{}, 1)
	}
	s.devState = map[string]deviceState{}
	if s.File == "" {
		return
	}
	b, err := os.ReadFile(s.File)
	if err != nil {
		return
	}
	var c Config
	if json.Unmarshal(b, &c) == nil {
		s.cfg = c
	}
}

// save schreibt die Einstellungen, nur für root lesbar. Aufrufer hält s.mu.
func (s *Service) save() error {
	if s.File == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.cfg, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.File), 0o700); err != nil {
		return err
	}
	tmp := s.File + ".neu"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.File)
}

func (s *Service) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// targets sind die Ziele einer Nachricht. Aufrufer hält s.mu.
func (s *Service) targets() []string {
	var out []string
	if s.cfg.Push != nil && s.Base != nil && s.Base() != "" {
		for _, d := range s.cfg.Push.Devices {
			out = append(out, "push:"+d.ID)
		}
	}
	if s.cfg.Ntfy != nil {
		out = append(out, "ntfy")
	}
	return out
}

// Enabled sagt, ob ein Kanal eingerichtet ist.
func (s *Service) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	return len(s.targets()) > 0
}

// Send stellt eine Nachricht für jedes Ziel in den Ausgang. Ohne Kanal
// passiert nichts.
func (s *Service) Send(m Message) {
	if s.quiet() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	now := s.now()
	// Eine neuere Nachricht zum selben Anlass ersetzt die wartende: Sonst
	// kann eine Warnung, die in der Wiederholung hängt, nach ihrer
	// Entwarnung ankommen und sie auf dem Handy überschreiben.
	if m.Tag != "" {
		keep := s.queue[:0]
		for _, it := range s.queue {
			if it.msg.Tag != m.Tag || it.name != "" {
				keep = append(keep, it)
			}
		}
		s.queue = keep
	}
	for _, to := range s.targets() {
		if len(s.queue) >= MaxQueue {
			s.queue = s.queue[1:]
		}
		s.queue = append(s.queue, &item{to: to, msg: m, added: now, next: now})
	}
	s.poke()
}

// View ist der Stand für den Leitstand.
func (s *Service) View() View {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	v := View{Pending: len(s.queue), Push: s.pushView()}
	if n := s.cfg.Ntfy; n != nil {
		v.Ntfy = ntfyView(n)
	}
	if b := s.cfg.Beat; b != nil {
		bv := &BeatView{Address: maskURL(b.URL), Error: s.beatErr, Every: int(BeatEvery / time.Minute)}
		if !s.beatAt.IsZero() {
			t := s.beatAt
			bv.Last = &t
		}
		if !s.beatOK.IsZero() {
			t := s.beatOK
			bv.LastOK = &t
		}
		v.Beat = bv
	}
	if s.last != nil {
		d := *s.last
		v.Last = &d
	}
	return v
}

func ntfyView(n *Ntfy) *NtfyView {
	u, _ := url.Parse(n.Server)
	host := n.Server
	if u != nil {
		host = u.Host + strings.TrimSuffix(u.Path, "/")
	}
	return &NtfyView{Server: n.Server, Host: host, Topic: n.Topic, Web: n.Server + "/" + n.Topic,
		App: "ntfy://" + host + "/" + n.Topic, Default: n.Server == DefaultServer, WithAuth: n.Token != ""}
}

// maskURL kürzt eine Adresse auf Rechner und Ende, damit niemand sie aus
// dem Leitstand abschreibt und falsche Herzschläge schickt.
func maskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "…"
	}
	p := u.Path
	if len(p) > 6 {
		p = "/…" + p[len(p)-4:]
	}
	return u.Scheme + "://" + u.Host + p
}

// CheckServer prüft die Adresse eines ntfy-Servers und gibt sie in einer
// festen Form zurück.
func CheckServer(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultServer, nil
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := checkURL(raw)
	if err != nil {
		return "", err
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("Die Adresse des ntfy-Servers hat kein ? und kein #.")
	}
	return strings.TrimSuffix(u.Scheme+"://"+u.Host+u.EscapedPath(), "/"), nil
}

// CheckBeat prüft die Adresse für den Herzschlag.
func CheckBeat(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("Bitte die Ping-Adresse eintragen, etwa https://hc-ping.com/… von healthchecks.io.")
	}
	u, err := checkURL(raw)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func checkURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New("Das ist keine gültige Adresse.")
	}
	if u.Scheme != "https" {
		return nil, errors.New("Die Adresse muss mit https:// beginnen, damit niemand unterwegs mitliest.")
	}
	if u.User != nil {
		return nil, errors.New("Bitte keine Zugangsdaten in die Adresse schreiben; ein Token hat ein eigenes Feld.")
	}
	return u, nil
}

// NewTopic erzeugt einen langen Zufallsnamen. Wer ihn kennt, kann bei
// ntfy.sh mitlesen; erraten lässt er sich nicht.
func NewTopic() string {
	const abc = "abcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, 22)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return "hostdeck-" + string(b)
}

// SetNtfy richtet den Kanal ein oder ändert ihn. newTopic erzeugt ein
// neues Thema; sonst bleibt das bisherige.
func (s *Service) SetNtfy(server, token string, newTopic bool) (View, error) {
	srv, err := CheckServer(server)
	if err != nil {
		return View{}, err
	}
	token = strings.TrimSpace(token)
	if strings.ContainsAny(token, " \r\n\t") {
		return View{}, errors.New("Das Token enthält Leerzeichen; bitte genau so einfügen, wie ntfy es zeigt.")
	}
	s.mu.Lock()
	s.load()
	old := s.cfg.Ntfy
	n := &Ntfy{Server: srv, Token: token}
	// Ein leeres Feld heißt „nicht angerührt“: Der Leitstand zeigt das
	// Token nie an, und „Neues Thema“ schickt es deshalb nicht mit.
	// Löschen geht über Abschalten und neu einrichten.
	if token == "" && old != nil && old.Server == srv {
		n.Token = old.Token
	}
	if old != nil && !newTopic {
		n.Topic = old.Topic
	} else {
		n.Topic = NewTopic()
	}
	s.cfg.Ntfy = n
	err = s.save()
	if err != nil {
		s.cfg.Ntfy = old
	}
	s.mu.Unlock()
	if err != nil {
		return View{}, err
	}
	return s.View(), nil
}

// SetBeat richtet den Herzschlag ein. Der erste geht sofort raus.
func (s *Service) SetBeat(addr string) (View, error) {
	a, err := CheckBeat(addr)
	if err != nil {
		return View{}, err
	}
	s.mu.Lock()
	s.load()
	old := s.cfg.Beat
	s.cfg.Beat = &Beat{URL: a}
	if err = s.save(); err != nil {
		s.cfg.Beat = old
	} else {
		s.beatAt, s.beatOK, s.beatErr = time.Time{}, time.Time{}, ""
		s.beatDue, s.beatNew = true, true
		s.poke()
	}
	s.mu.Unlock()
	if err != nil {
		return View{}, err
	}
	return s.View(), nil
}

// Disable schaltet "ntfy" oder "herzschlag" ab.
func (s *Service) Disable(kind string) (View, error) {
	s.mu.Lock()
	s.load()
	old := s.cfg
	switch kind {
	case "ntfy":
		s.cfg.Ntfy = nil
		var keep []*item
		for _, it := range s.queue {
			if it.to != "ntfy" {
				keep = append(keep, it)
			}
		}
		s.queue = keep
	case "herzschlag":
		s.cfg.Beat = nil
		s.beatAt, s.beatOK, s.beatErr = time.Time{}, time.Time{}, ""
	default:
		s.mu.Unlock()
		return View{}, fmt.Errorf("unbekannter Kanal %q", kind)
	}
	err := s.save()
	if err != nil {
		s.cfg = old
	}
	s.mu.Unlock()
	if err != nil {
		return View{}, err
	}
	return s.View(), nil
}

// Result ist das Ergebnis einer Probe je Ziel.
type Result struct {
	Target string `json:"ziel"`
	OK     bool   `json:"ok"`
	Error  string `json:"fehler,omitempty"`
}

// Test schickt sofort eine Probe-Nachricht an alle Ziele (oder nur an das
// Gerät only) und wartet auf die Antworten.
func (s *Service) Test(ctx context.Context, m Message, only string) ([]Result, error) {
	s.mu.Lock()
	s.load()
	var tos []string
	for _, to := range s.targets() {
		if only == "" || to == "push:"+only {
			tos = append(tos, to)
		}
	}
	s.mu.Unlock()
	if len(tos) == 0 {
		return nil, errors.New("Es ist noch kein Gerät und kein Kanal eingerichtet.")
	}
	var out []Result
	for _, to := range tos {
		it := &item{to: to, msg: m, added: s.now()}
		name, err := s.deliver(ctx, it)
		it.name = name
		r := Result{Target: name, OK: err == nil}
		if err != nil {
			r.Error = err.Error()
		}
		s.finish(it, err, true)
		out = append(out, r)
	}
	return out, nil
}

// Run verschickt, bis ctx endet: den Ausgang mit Wiederholung und alle
// 5 Minuten den Herzschlag.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.load()
	s.beatDue = true
	s.mu.Unlock()
	beat := time.NewTicker(BeatEvery)
	defer beat.Stop()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		s.step(ctx)
		wait := s.nextWait()
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-timer.C:
		case <-beat.C:
			s.mu.Lock()
			s.beatDue = true
			s.mu.Unlock()
		}
	}
}

// nextWait ist die Zeit bis zur nächsten fälligen Nachricht.
func (s *Service) nextWait() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	wait := BeatEvery
	now := s.now()
	for _, it := range s.queue {
		if d := it.next.Sub(now); d < wait {
			wait = d
		}
	}
	if wait < time.Second {
		wait = time.Second
	}
	return wait
}

// step erledigt, was fällig ist. Tests rufen es direkt.
func (s *Service) step(ctx context.Context) {
	s.mu.Lock()
	s.load()
	now := s.now()
	var due []*item
	for _, it := range s.queue {
		if !it.next.After(now) {
			due = append(due, it)
		}
	}
	var beat *Beat
	if s.beatDue && s.cfg.Beat != nil && !s.quiet() {
		b := *s.cfg.Beat
		beat = &b
	}
	s.beatDue = false
	s.mu.Unlock()

	for _, it := range due {
		_, err := s.deliver(ctx, it)
		if ctx.Err() != nil {
			return
		}
		s.finish(it, err, false)
	}
	if beat != nil {
		s.ping(ctx, beat.URL)
	}
}

// errGone: Das Ziel gibt es nicht mehr (abgeschaltet, Gerät entfernt).
var errGone = errors.New("Ziel gibt es nicht mehr")

// deliver schickt eine Nachricht an ihr Ziel und nennt das Ziel.
func (s *Service) deliver(ctx context.Context, it *item) (string, error) {
	s.mu.Lock()
	s.load()
	var n *Ntfy
	var dev *Device
	var key *ecdsa.PrivateKey
	var err error
	sub := ""
	if s.Base != nil {
		sub = s.Base()
	}
	switch {
	case it.to == "ntfy" && s.cfg.Ntfy != nil:
		x := *s.cfg.Ntfy
		n = &x
	case strings.HasPrefix(it.to, "push:") && s.cfg.Push != nil && sub != "":
		for _, d := range s.cfg.Push.Devices {
			if "push:"+d.ID == it.to {
				x := *d
				dev = &x
			}
		}
		if dev != nil {
			key, err = s.vapidKey()
		}
	}
	s.mu.Unlock()
	switch {
	case n != nil:
		return "ntfy (" + hostOf(n.Server) + ")", s.post(ctx, *n, it.msg)
	case dev != nil && err != nil:
		return dev.Name, &permanentError{"Schlüssel für Mitteilungen fehlt: " + err.Error()}
	case dev != nil:
		return dev.Name, s.sendPush(ctx, key, sub, *dev, it.msg)
	}
	return it.to, errGone
}

// finish hält das Ergebnis eines Versuchs fest. test: eine Probe, die
// nicht im Ausgang stand.
func (s *Service) finish(it *item, err error, test bool) {
	s.mu.Lock()
	now := s.now()
	it.tries++
	var perm *permanentError
	var gone *goneError
	drop := errors.Is(err, errGone)
	removed := ""
	if errors.As(err, &gone) && strings.HasPrefix(it.to, "push:") && s.cfg.Push != nil {
		id := strings.TrimPrefix(it.to, "push:")
		var keep []*Device
		for _, d := range s.cfg.Push.Devices {
			if d.ID == id {
				removed = d.Name
				continue
			}
			keep = append(keep, d)
		}
		if removed != "" {
			s.cfg.Push.Devices = keep
			_ = s.save()
			delete(s.devState, id)
		}
	}
	if id, ok := strings.CutPrefix(it.to, "push:"); ok && removed == "" && !drop {
		st := s.devState[id]
		if err == nil {
			st.ok, st.err = now, ""
		} else {
			st.err = err.Error()
		}
		s.devState[id] = st
	}
	giveUp := err != nil && (test || drop || gone != nil || errors.As(err, &perm) || now.Sub(it.added) >= GiveUp)
	if err == nil || giveUp {
		for i, x := range s.queue {
			if x == it {
				s.queue = append(s.queue[:i:i], s.queue[i+1:]...)
				break
			}
		}
		if !drop {
			d := &Delivery{At: now, Title: it.msg.Title, OK: err == nil, Tries: it.tries}
			if err != nil {
				d.Error = err.Error()
			}
			s.last = d
		}
	} else {
		it.err = err.Error()
		w := 15 * time.Minute
		if it.tries-1 < len(retryAfter) {
			w = retryAfter[it.tries-1]
		}
		it.next = now.Add(w)
	}
	s.mu.Unlock()
	switch {
	case removed != "":
		s.note("Gerät „"+removed+"“ bekommt keine Mitteilungen mehr und ist ausgetragen", err.Error())
	case test && err == nil:
		s.note("Probe-Nachricht zugestellt an " + it.name)
	case test:
		s.note("Probe-Nachricht nicht zugestellt an "+it.name, err.Error())
	case drop:
	case err == nil && it.tries > 1:
		s.note(fmt.Sprintf("Nachricht zugestellt nach %d Versuchen: %s", it.tries, it.msg.Title))
	case giveUp:
		s.note("Nachricht nicht zugestellt, aufgegeben: "+it.msg.Title, err.Error())
	}
}

// permanentError lohnt keinen weiteren Versuch (falsches Token, Thema
// gesperrt).
type permanentError struct{ msg string }

func (e *permanentError) Error() string { return e.msg }

// post schickt eine Nachricht an ntfy (JSON an die Wurzel des Servers).
func (s *Service) post(ctx context.Context, n Ntfy, m Message) error {
	body := map[string]any{"topic": n.Topic, "title": m.Title, "message": m.Text}
	if m.Priority > 0 {
		body["priority"] = m.Priority
	}
	if len(m.Tags) > 0 {
		body["tags"] = m.Tags
	}
	if m.Click != "" {
		body["click"] = m.Click
	}
	b, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(ctx, sendWait)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.Server+"/", bytes.NewReader(b))
	if err != nil {
		return &permanentError{err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	if n.Token != "" {
		req.Header.Set("Authorization", "Bearer "+n.Token)
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return fmt.Errorf("%s antwortet nicht: %v", hostOf(n.Server), shortErr(err))
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode/100 == 2 {
		return nil
	}
	text := fmt.Sprintf("%s meldet Status %d", hostOf(n.Server), resp.StatusCode)
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(msg, &e) == nil && e.Error != "" {
		text += " (" + e.Error + ")"
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &permanentError{text + "; stimmt das Token?"}
	case http.StatusBadRequest, http.StatusNotFound, http.StatusRequestEntityTooLarge:
		return &permanentError{text}
	}
	return errors.New(text)
}

// ping schickt den Herzschlag und merkt sich das Ergebnis; Wechsel
// zwischen gut und ausgefallen stehen im Protokoll.
func (s *Service) ping(ctx context.Context, addr string) {
	ctx, cancel := context.WithTimeout(ctx, sendWait)
	defer cancel()
	var err error
	req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
	if rerr != nil {
		err = errors.New("Die Adresse ist ungültig.")
	} else if resp, derr := s.client().Do(req); derr != nil {
		err = fmt.Errorf("%s antwortet nicht: %v", hostOf(addr), shortErr(derr))
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			err = fmt.Errorf("%s meldet Status %d", hostOf(addr), resp.StatusCode)
		}
	}
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	s.mu.Lock()
	if s.cfg.Beat == nil || s.cfg.Beat.URL != addr {
		s.mu.Unlock()
		return
	}
	now := s.now()
	was, fresh := s.beatErr, s.beatNew
	s.beatNew = false
	s.beatAt = now
	if err == nil {
		s.beatOK, s.beatErr = now, ""
	} else {
		s.beatErr = err.Error()
	}
	s.mu.Unlock()
	switch {
	case err != nil && was == "":
		s.note("Herzschlag nicht angekommen", err.Error())
	case err == nil && fresh:
		s.note("Herzschlag eingerichtet; der erste ist angekommen")
	case err == nil && was != "":
		s.note("Herzschlag kommt wieder an")
	}
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return "Der Dienst"
}

// shortErr nimmt die Adresse aus Fehlern von net/http heraus; sie kann
// Geheimes enthalten (Ping-Adresse).
func shortErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "keine Antwort in 15 Sekunden"
	}
	return err.Error()
}
