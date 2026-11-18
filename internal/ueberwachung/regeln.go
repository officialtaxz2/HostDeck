package ueberwachung

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/lauf"
)

// Warnungen. Regeln prüfen die Messung im Takt, die Seiten
// alle 5 Minuten und die Container jede Minute. Eine Warnung beginnt,
// wenn eine Bedingung lange genug anhält, kommt nach 24 Stunden als
// Erinnerung wieder und endet mit einer Entwarnung, wenn 10 Minuten lang
// alles gut war. Regeln zeigen und melden nur; sie ändern nie etwas.

// Stufen und Arten von Ereignissen.
const (
	LevelWarn = "warn"
	LevelErr  = "err"
	LevelInfo = "info"
	// Nur für den Stand des Backups (BackupView): grün und neutral.
	LevelOK   = "ok"
	LevelIdle = "idle"

	EvStart  = "warnung"
	EvWorse  = "dringend"
	EvRemind = "erinnerung"
	EvClear  = "entwarnung"
	EvNote   = "hinweis"
)

// Limits sind die Grenzen der Regeln.
type Limits struct {
	DiskWarn     float64       `json:"platte_gelb"`        // belegt, Anteil
	DiskErr      float64       `json:"platte_rot"`         // belegt, Anteil
	DiskMinFree  int64         `json:"platte_min_frei"`    // Byte; darunter rot
	MemWarn      float64       `json:"speicher_gelb"`      // verfügbar, Anteil
	MemErr       float64       `json:"speicher_rot"`       // verfügbar, Anteil
	MemHold      time.Duration `json:"-"`                  // so lange knapp
	CPUWarn      float64       `json:"prozessor_gelb"`     // %
	CPUHold      time.Duration `json:"-"`                  //
	StealWarn    float64       `json:"steal_gelb"`         // %
	StealHold    time.Duration `json:"-"`                  //
	SiteFails    int           `json:"seite_fehlversuche"` // Prüfungen hintereinander
	Restarts     int           `json:"neustarts"`          // so viele in RestartWindow
	RestartsIn   time.Duration `json:"-"`                  //
	UnhealthyFor time.Duration `json:"-"`                  //
	CertWarnDays int           `json:"zertifikat_gelb_tage"`
	CertErrDays  int           `json:"zertifikat_rot_tage"`
	BackupWarn   int           `json:"backup_gelb_tage"`
	BackupErr    int           `json:"backup_rot_tage"`
	ClearAfter   time.Duration `json:"-"` // so lange gut bis zur Entwarnung
	RemindAfter  time.Duration `json:"-"`
}

// DefaultLimits sind die Voreinstellungen aus dem Plan.
var DefaultLimits = Limits{
	DiskWarn: 0.80, DiskErr: 0.90, DiskMinFree: 5_000_000_000,
	MemWarn: 0.10, MemErr: 0.05, MemHold: 5 * time.Minute,
	CPUWarn: 90, CPUHold: 15 * time.Minute,
	StealWarn: 20, StealHold: 30 * time.Minute,
	SiteFails: 2, Restarts: 3, RestartsIn: time.Hour, UnhealthyFor: 5 * time.Minute,
	CertWarnDays: 14, CertErrDays: 7, BackupWarn: 14, BackupErr: 30,
	ClearAfter: 10 * time.Minute, RemindAfter: 24 * time.Hour,
}

// Finding ist eine Bedingung, die gerade nicht in Ordnung ist.
type Finding struct {
	Key   string
	Level string
	Icon  string
	Title string
	Text  string
	Link  string
	Go    string
	Hold  time.Duration // so lange muss es anhalten
}

// Alert ist eine laufende Warnung.
type Alert struct {
	Key      string     `json:"schluessel"`
	Level    string     `json:"stufe"`
	Icon     string     `json:"symbol"`
	Title    string     `json:"titel"`
	Text     string     `json:"text"`
	Link     string     `json:"link,omitempty"`
	Go       string     `json:"knopf,omitempty"`
	Since    time.Time  `json:"seit"`
	GoodFrom *time.Time `json:"gut_seit,omitempty"`
	Notified time.Time  `json:"gemeldet"`
	// MildFrom ist gesetzt, solange der Befund unter der Stufe der
	// Warnung liegt. Die Stufe geht erst herunter, wenn es ClearAfter
	// lang milder blieb: Sonst schickt ein Wert, der um die rote Grenze
	// pendelt, bei jedem Übertritt eine neue dringende Mitteilung.
	MildFrom *time.Time `json:"milder_seit,omitempty"`
	// Accepted: Du hast die Warnung bewusst so gelassen (Designvorgabe,
	// Grundregel 4). Sie steht dann nicht unter „Braucht dich“, erinnert
	// nicht und meldet sich erst wieder, wenn es schlimmer wird oder wenn
	// sie vorbei war und neu auftritt.
	Accepted *time.Time `json:"so_gelassen,omitempty"`
	// CanAccept (nur in View): Für diese Warnung gibt es „Bewusst so
	// lassen“.
	CanAccept bool `json:"so_lassen,omitempty"`
}

// Acceptable sagt, ob sich eine Warnung bewusst so lassen lässt: nur, wo
// es ein gewollter Zustand sein kann (ein Container ohne funktionierende
// eigene Prüfung, ein Programm, das mit Fehlercode endet, gewollt hohe
// Last, Steal beim Hoster, eine Domain, die absichtlich nicht hierher
// zeigt, Backups nur von Hand). Platte, Arbeitsspeicher, Neustart-Schleifen,
// Docker und die Regeln von HostDeck bleiben immer Warnungen: Dort hilft nur
// Handeln oder eine andere Grenze.
func Acceptable(key string) bool {
	switch key {
	case "prozessor", "steal", "backup":
		return true
	}
	for _, p := range []string{"gesundheit:", "aus:", "seite:", "zertifikat:"} {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// Fehler von Accept.
var (
	ErrNoAlert       = errors.New("Diese Warnung läuft gerade nicht.")
	ErrNotAcceptable = errors.New("Diese Warnung lässt sich nicht bewusst so lassen.")
)

// rank ordnet die Stufen: höher heißt schlimmer.
func rank(level string) int {
	switch level {
	case LevelErr:
		return 2
	case LevelWarn:
		return 1
	}
	return 0
}

// Event ist ein Ereignis für Verlauf, Protokoll und Nachrichten.
type Event struct {
	At    time.Time `json:"zeit"`
	Kind  string    `json:"art"`
	Key   string    `json:"schluessel"`
	Level string    `json:"stufe"`
	Title string    `json:"titel"`
	Text  string    `json:"text,omitempty"`
	Link  string    `json:"link,omitempty"`
	// Since und Good: Beginn der Warnung und seit wann es wieder gut war
	// (Entwarnung), Start des Servers (Neustart). Die Texte nennen keine
	// Uhrzeit; der Leitstand zeigt sie aus diesen Feldern in seiner Zeit.
	Since *time.Time `json:"seit,omitempty"`
	Good  *time.Time `json:"gut_seit,omitempty"`
	// End: Hier endete ein Ausfall, ohne dass die Seite wieder antwortet
	// (Pause, nicht mehr geprüft).
	End *time.Time `json:"ende,omitempty"`
	// Quiet: nur Verlauf und Protokoll, keine Mitteilung aufs Handy.
	Quiet bool `json:"still,omitempty"`
	// InPause (nur in View): Das Ereignis fällt in eine Pause der Seite
	// (du hattest ihr Ziel gestoppt); es zählt nicht als Ausfall.
	InPause bool `json:"in_pause,omitempty"`
}

// Span ist eine Zeitspanne; To leer: dauert an.
type Span struct {
	From time.Time `json:"von"`
	To   time.Time `json:"bis,omitzero"`
}

// SiteCheck ist die letzte Prüfung einer Domain.
type SiteCheck struct {
	Host      string     `json:"domain"`
	OK        bool       `json:"ok"`
	Answer    string     `json:"antwort"`
	Took      int64      `json:"dauer_ms,omitempty"`
	CertUntil *time.Time `json:"zertifikat_bis,omitempty"`
	At        time.Time  `json:"geprueft"`
	Fails     int        `json:"fehlversuche"`       // hintereinander
	Paused    string     `json:"pausiert,omitempty"` // Ziel hast du gestoppt: warum, in einem Satz
	// PausedSince: seit wann (du hast das Ziel da gestoppt), sonst nil.
	PausedSince *time.Time `json:"pausiert_seit,omitempty"`
	// Starting: Das Ziel läuft erst seit kurzem; ein Fehlversuch zählt
	// noch nicht (wie „startet gerade“ im Zustand der Domain).
	Starting bool `json:"-"`
	// Hint: Ein anderer Container des Projekts ist von dir gestoppt. Antwortet
	// die Seite nicht, warnt HostDeck gelb statt rot und nennt ihn.
	Hint string `json:"hinweis,omitempty"`
	// Word und Why: Wort und Grund aus dem Zustand der Domain, wenn er rot
	// ist (etwa „zeigt ins Leere“, „Am Ziel … lauscht nichts.“). Die Warnung
	// nennt dann dasselbe wie Lageplan und Seiten.
	Word     string     `json:"-"`
	Why      string     `json:"-"`
	Outages  int        `json:"ausfaelle"`             // Warnungen in 30 Tagen
	LastDown *time.Time `json:"zuletzt_aus,omitempty"` // Beginn der letzten
	// DownDays sind die Minuten ohne Antwort je Tag, die letzten DownDayCount
	// Tage in Ortszeit, der heutige zuletzt. Gezählt wird von der Warnung bis
	// zur Rückkehr; vor der ersten Messung steht 0.
	DownDays []int `json:"ausfall_tage,omitempty"`
	// Since: seit wann HostDeck diese Domain prüft. Überdauert Neustarts und
	// Updates, damit die Tagesbalken nicht jedes Mal neu anfangen.
	Since *time.Time `json:"geprueft_seit,omitempty"`
	// Verfügbarkeit (nur in View), die eine Rechnung für alle Anzeigen:
	// From ist der Beginn (geprüft seit, höchstens 30 Tage zurück).
	// Measured ist die Messzeit ohne Pausen, Down die Ausfallzeit ohne
	// Pausen, beide in Sekunden. Avail ist der Anteil in Prozent; nil,
	// solange es keine Messzeit gibt. Outs und Pauses sind die Spannen im
	// Zeitraum, damit der Leitstand die Tage in seiner Zeit zeichnet.
	From     *time.Time `json:"verfuegbar_ab,omitempty"`
	Measured int64      `json:"messzeit_s"`
	Down     int64      `json:"ausfall_s"`
	Avail    *float64   `json:"verfuegbarkeit"`
	Outs     []Span     `json:"ausfall_zeiten"`
	Pauses   []Span     `json:"pausen"`
}

// DownDayCount ist die Zahl der Tage in SiteCheck.DownDays.
const DownDayCount = 30

// outageSpans sind die Ausfälle einer Seite: von der Warnung bis zur
// Rückkehr (eine Entwarnung kommt ClearAfter danach) oder bis zum Hinweis,
// der sie beendet (Pause, nicht mehr geprüft: Ende im Ereignis).
func outageSpans(events []Event, active *Alert, key string, clearAfter time.Duration, now time.Time) []Span {
	var out []Span
	var open time.Time
	closeAt := func(end time.Time) {
		if end.Before(open) {
			end = open
		}
		out = append(out, Span{open, end})
		open = time.Time{}
	}
	for _, e := range events {
		if e.Key != key {
			continue
		}
		switch e.Kind {
		case EvStart:
			if open.IsZero() {
				open = e.At
			}
		case EvClear:
			if !open.IsZero() {
				end := e.At.Add(-clearAfter)
				if e.Good != nil {
					end = *e.Good
				}
				closeAt(end)
			}
		case EvNote:
			if !open.IsZero() && e.End != nil {
				closeAt(*e.End)
			}
		}
	}
	if active != nil {
		if open.IsZero() || active.Since.Before(open) {
			open = active.Since
		}
	}
	if !open.IsZero() {
		end := now
		if active != nil && active.GoodFrom != nil {
			end = *active.GoodFrom
		}
		closeAt(end)
	}
	return out
}

// clip schneidet Spannen auf [from, to]; offene enden bei to.
func clip(xs []Span, from, to time.Time) []Span {
	var out []Span
	for _, x := range xs {
		a, b := x.From, x.To
		if b.IsZero() || b.After(to) {
			b = to
		}
		if a.Before(from) {
			a = from
		}
		if b.After(a) {
			out = append(out, Span{a, b})
		}
	}
	return out
}

// minus zieht die Spannen ys von xs ab (beide geschlossen).
func minus(xs, ys []Span) []Span {
	out := append([]Span(nil), xs...)
	for _, y := range ys {
		var next []Span
		for _, x := range out {
			if !y.To.After(x.From) || !x.To.After(y.From) {
				next = append(next, x)
				continue
			}
			if y.From.After(x.From) {
				next = append(next, Span{x.From, y.From})
			}
			if x.To.After(y.To) {
				next = append(next, Span{y.To, x.To})
			}
		}
		out = next
	}
	return out
}

func total(xs []Span) time.Duration {
	var d time.Duration
	for _, x := range xs {
		d += x.To.Sub(x.From)
	}
	return d
}

// within sagt, ob t in einer der Spannen liegt (offene dauern bis now).
func within(xs []Span, t, now time.Time) bool {
	for _, x := range xs {
		to := x.To
		if to.IsZero() {
			to = now
		}
		if !t.Before(x.From) && !t.After(to) {
			return true
		}
	}
	return false
}

// perDay sind die Minuten je Tag, die letzten DownDayCount Tage in der
// Zeitzone von now, der heutige zuletzt.
func perDay(xs []Span, now time.Time) []int {
	out := make([]int, DownDayCount)
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	for i := range out {
		d0 := today.AddDate(0, 0, i-(DownDayCount-1))
		sum := total(clip(xs, d0, d0.AddDate(0, 0, 1)))
		out[i] = int((sum + 30*time.Second) / time.Minute)
		if out[i] == 0 && sum > 0 {
			out[i] = 1
		}
	}
	return out
}

// ContainerState ist, was die Regeln über einen Container wissen müssen.
type ContainerState struct {
	Name         string
	Project      string // Compose-Projekt, sonst leer
	State        string // running, exited, restarting …
	Running      bool
	StoppedByYou bool
	Job          bool // einmal gelaufen und fertig
	Managed      bool // angelegt von einem anderen Container (etwa einem Spiele-Panel): nur Anzeige
	ExitCode     int
	Restarts     int
}

// Rules wertet die Regeln aus. Alle Quellen sind optional.
type Rules struct {
	File       string // Zustand, Standard keiner (nur im Speicher)
	Limits     Limits
	Sites      func(ctx context.Context) ([]SiteCheck, error)
	Containers func(ctx context.Context) ([]ContainerState, error)
	LastBackup func() (at time.Time, ok bool) // letztes gültiges Backup
	Extra      func(ctx context.Context) []Finding
	Boot       func() time.Time
	// Busy nennt den Lauf von HostDeck, der gerade arbeitet (Backup, letzter
	// Lauf, Umzug), sonst "". Dabei stoppt HostDeck selbst Container und
	// Seiten antworten nicht; das ist keine Warnung wert.
	Busy func() string
	// Event bekommt jedes Ereignis (Protokoll, Nachrichten). Es läuft
	// ohne Sperre und darf langsam sein.
	Event func(Event)
	// Journal sammelt die Ereignisse für den Leitstand (nil: keins).
	Journal *Journal
	Now     func() time.Time

	mu        sync.Mutex
	loaded    bool
	st        rulesState
	first     map[string]time.Time
	gone      map[string]time.Time     // Bedingung wartet noch und fehlt gerade
	hold      map[string]time.Duration // Wartezeit der Bedingungen in first
	contsBad  bool                     // letzte Abfrage der Container scheiterte
	dirty     bool                     // st geändert ohne Ereignis, beim nächsten record schreiben
	busyWhat  string                   // Lauf von HostDeck, der gerade arbeitet
	sites     map[string]*SiteCheck
	sitesAt   time.Time
	sitesBusy bool
	sitesNext bool // während einer Prüfung bestellt: gleich danach noch einmal
	conts     []ContainerState
	contsAt   time.Time
	restarts  map[string][]restartSample
	syncSites bool    // Tests: Seiten nicht im Hintergrund prüfen
	pending   []Event // Hinweise aus der Seitenprüfung, für den nächsten Check
}

type restartSample struct {
	at time.Time
	n  int
}

// LoopFresh: So lange nach dem letzten Neustart gilt ein Container mit
// vielen Neustarts noch als Schleife; dieselbe Grenze wie looping() im
// Leitstand (15 Min.). Danach läuft er wieder ruhig, auch wenn die
// Neustarts der letzten Stunde noch zählen (Labor S6).
const LoopFresh = 15 * time.Minute

// lastRestart ist der Zeitpunkt der Messung, bei der die Zahl der Neustarts
// zuletzt stieg.
func lastRestart(h []restartSample) time.Time {
	for i := len(h) - 1; i > 0; i-- {
		if h[i].n > h[i-1].n {
			return h[i].at
		}
	}
	return h[0].at
}

type rulesState struct {
	Boot   time.Time         `json:"start_des_servers"`
	Active map[string]*Alert `json:"aktiv"`
	Events []Event           `json:"verlauf"`
	// NoBackup: seit wann HostDeck hier kein gültiges Backup findet, etwa auf
	// einem frisch eingerichteten oder gerade umgezogenen Server.
	NoBackup time.Time `json:"ohne_backup_seit,omitzero"`
	// SitesSince: je Domain der erste Zeitpunkt, an dem HostDeck sie geprüft hat.
	SitesSince map[string]time.Time `json:"seiten_seit,omitempty"`
	// Pauses: je Domain die Zeiten, in denen du ihr Ziel gestoppt hattest
	// (30 Tage). Sie zählen weder als Ausfall noch als Messzeit.
	Pauses map[string][]Span `json:"pausen,omitempty"`
}

// Abstände der Quellen.
const (
	SiteEvery      = 5 * time.Minute
	ContainerEvery = time.Minute
	KeepEvents     = 30 * 24 * time.Hour
	MaxEvents      = 500
)

func (r *Rules) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Rules) limits() Limits {
	if r.Limits == (Limits{}) {
		return DefaultLimits
	}
	return r.Limits
}

// load liest den Zustand einmal. Aufrufer hält r.mu.
func (r *Rules) load() {
	if r.loaded {
		return
	}
	r.loaded = true
	r.first = map[string]time.Time{}
	r.gone = map[string]time.Time{}
	r.hold = map[string]time.Duration{}
	r.sites = map[string]*SiteCheck{}
	r.restarts = map[string][]restartSample{}
	r.st = rulesState{Active: map[string]*Alert{}}
	r.loadLimits()
	if r.File == "" {
		return
	}
	b, err := os.ReadFile(r.File)
	if err != nil {
		return
	}
	var st rulesState
	if json.Unmarshal(b, &st) == nil {
		if st.Active == nil {
			st.Active = map[string]*Alert{}
		}
		r.st = st
	}
}

// save schreibt den Zustand. Aufrufer hält r.mu.
func (r *Rules) save() error {
	if r.File == "" {
		return nil
	}
	b, err := json.MarshalIndent(r.st, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.File), 0o700); err != nil {
		return err
	}
	tmp := r.File + ".neu"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.File)
}

// Check wertet die Regeln mit der letzten Messung aus. Seiten und
// Container fragt es nur in ihrem Abstand ab; die Seiten im Hintergrund.
func (r *Rules) Check(ctx context.Context, f Frame, info Info) error {
	now := r.now()
	r.mu.Lock()
	r.load()
	var events []Event
	// Neustart des Servers: nur ein Hinweis.
	if r.Boot != nil {
		if b := r.Boot(); !b.IsZero() {
			if !r.st.Boot.IsZero() && b.Sub(r.st.Boot).Abs() > time.Minute {
				bt := b
				events = append(events, Event{At: now, Kind: EvNote, Key: "neustart", Level: LevelInfo, Title: "Server neu gestartet",
					Text: "Der Server war kurz aus und läuft wieder; Projekte mit Neustart-Regel starten von selbst.", Link: "#/ueberwachung", Since: &bt})
			}
			r.st.Boot = b
		}
	}
	needSites := r.Sites != nil && !r.sitesBusy && now.Sub(r.sitesAt) >= SiteEvery
	if needSites {
		r.sitesBusy = true
	}
	needConts := r.Containers != nil && now.Sub(r.contsAt) >= ContainerEvery
	lim := r.limits()
	r.mu.Unlock()
	var fresh []ContainerState

	if needSites {
		if r.syncSites {
			r.checkSites(ctx)
		} else {
			go r.checkSites(ctx)
		}
	}
	if needConts {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		cs, err := r.Containers(cctx)
		cancel()
		r.mu.Lock()
		r.contsAt = now
		r.contsBad = err != nil
		if err == nil {
			r.conts = cs
			r.noteRestarts(cs, now)
			fresh = cs
		}
		r.mu.Unlock()
	}
	var extra []Finding
	if r.Extra != nil {
		extra = r.Extra(ctx)
	}
	busy := ""
	if r.Busy != nil {
		busy = r.Busy()
	}

	// Die Ereignis-Liste ist eine Zugabe: Kann sie nicht schreiben (etwa
	// bei voller Platte), laufen die Warnungen trotzdem weiter; der Fehler
	// kommt am Ende mit zurück.
	var jerr error
	if fresh != nil {
		jerr = r.Journal.containers(fresh, busy, now)
	}
	if err := r.Journal.frame(f, info, lim, now); err != nil {
		jerr = errors.Join(jerr, err)
	}

	r.mu.Lock()
	r.busyWhat = busy
	found := append(r.findings(f, info, now), extra...)
	if busy != "" {
		// Ein Lauf von HostDeck stoppt selbst Container und nimmt Seiten vom
		// Netz: Diese Befunde zählen dann nicht (eine Stelle für alle).
		keep := found[:0]
		for _, x := range found {
			if !pausedDuringRun(x.Key, busy) {
				keep = append(keep, x)
			}
		}
		found = keep
	}
	events = append(events, r.pending...)
	r.pending = nil
	events = append(events, r.apply(found, r.unknown(info), now)...)
	err := r.record(events, now)
	if err != nil {
		err = fmt.Errorf("Warnungen nicht gespeichert: %w", err)
	}
	r.mu.Unlock()
	r.emit(events)
	if jerr != nil {
		err = errors.Join(err, fmt.Errorf("Ereignisse: %w", jerr))
	}
	return err
}

func (r *Rules) emit(events []Event) {
	for _, e := range events {
		_ = r.Journal.rule(e)
		if r.Event != nil {
			r.Event(e)
		}
	}
}

// record hängt Ereignisse an und schreibt den Zustand, wenn sich etwas
// geändert hat. Aufrufer hält r.mu.
func (r *Rules) record(events []Event, now time.Time) error {
	changed := len(events) > 0 || r.dirty
	r.dirty = false
	r.st.Events = append(r.st.Events, events...)
	cut := 0
	for cut < len(r.st.Events) && now.Sub(r.st.Events[cut].At) > KeepEvents {
		cut++
	}
	if n := len(r.st.Events) - cut; n > MaxEvents {
		cut += n - MaxEvents
	}
	if cut > 0 {
		r.st.Events = append([]Event(nil), r.st.Events[cut:]...)
		changed = true
	}
	if !changed {
		return nil
	}
	return r.save()
}

func (r *Rules) checkSites(ctx context.Context) {
	sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	list, err := r.Sites(sctx)
	cancel()
	now := r.now()
	if err == nil {
		_ = r.Journal.sites(list, now)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sitesBusy = false
	r.sitesAt = now
	if r.sitesNext {
		// Die Änderung kam vielleicht erst nach dem Lesen von Caddy.
		r.sitesAt, r.sitesNext = time.Time{}, false
	}
	if err != nil {
		return
	}
	next := map[string]*SiteCheck{}
	if r.st.SitesSince == nil {
		r.st.SitesSince = map[string]time.Time{}
	}
	for _, sc := range list {
		sc := sc
		sc.At = now
		if _, ok := r.st.SitesSince[sc.Host]; !ok {
			// Erste Prüfung seit diesem Update: Gibt es ältere Ereignisse
			// der Domain, lief die Prüfung schon damals.
			first := now
			for _, e := range r.st.Events {
				if e.Key == "seite:"+sc.Host && e.At.Before(first) {
					first = e.At
				}
			}
			r.st.SitesSince[sc.Host], r.dirty = first, true
		}
		old, had := r.sites[sc.Host]
		r.notePause(sc, now)
		switch {
		case sc.Paused != "":
			// Ziel von dir gestoppt: Nichts zählt, auch nicht für die Zeit
			// nach dem Starten. Sonst wäre nach dem Starten schon eine
			// einzige Prüfung beim Hochfahren rot.
			sc.Fails = 0
		case sc.Starting:
			// Das Ziel startet gerade: Der Fehlversuch zählt nicht, der
			// Stand davor bleibt.
			sc.Fails = 0
			if had {
				sc.Fails = old.Fails
			}
		case r.busyWhat != "" && r.busyWhat != lauf.Leistungstest && r.busyWhat != lauf.Tuning:
			// Während ein Lauf arbeitet, sind die Projekte aus: Der
			// Fehlversuch zählt nicht, der Stand davor bleibt. Eine Seite,
			// die schon vorher ausgefallen war, gilt danach also weiter
			// als ausgefallen.
			sc.Fails = 0
			if had {
				sc.Fails = old.Fails
			}
		case !sc.OK:
			sc.Fails = 1
			if had {
				sc.Fails = old.Fails + 1
			}
		}
		next[sc.Host] = &sc
	}
	for h := range r.st.SitesSince {
		if next[h] == nil {
			delete(r.st.SitesSince, h)
			r.dirty = true
		}
	}
	for h, ps := range r.st.Pauses {
		// Was 30 Tage vorbei ist, zählt nirgends mehr; eine Domain, die
		// nicht mehr geprüft wird, beendet ihre Pause.
		var keep []Span
		for _, x := range ps {
			if x.To.IsZero() && next[h] == nil {
				x.To = now
			}
			if x.To.IsZero() || now.Sub(x.To) <= KeepEvents {
				keep = append(keep, x)
			}
		}
		if len(keep) != len(ps) || (len(keep) > 0 && keep[len(keep)-1] != ps[len(ps)-1]) {
			r.dirty = true
		}
		if len(keep) == 0 {
			delete(r.st.Pauses, h)
		} else {
			r.st.Pauses[h] = keep
		}
	}
	r.sites = next
}

// notePause führt die Pausen einer Domain nach: Ist ihr Ziel von dir
// gestoppt, beginnt eine Pause zu dem Zeitpunkt, an dem du es gestoppt
// hast (nicht erst bei dieser Prüfung); sonst endet eine laufende Pause.
// Fällt ein früherer Ausfall ganz in die neue Pause (etwa aus der Zeit vor
// der Korrektur K1), zählt er damit nicht mehr; das steht einmal im
// Protokoll. Aufrufer hält r.mu.
func (r *Rules) notePause(sc SiteCheck, now time.Time) {
	if r.st.Pauses == nil {
		r.st.Pauses = map[string][]Span{}
	}
	ps := r.st.Pauses[sc.Host]
	open := len(ps) > 0 && ps[len(ps)-1].To.IsZero()
	if sc.Paused == "" {
		if open {
			ps[len(ps)-1].To = now
			r.dirty = true
		}
		return
	}
	if open {
		return
	}
	from := now
	if sc.PausedSince != nil && sc.PausedSince.Before(now) && now.Sub(*sc.PausedSince) <= KeepEvents {
		from = *sc.PausedSince
	}
	// Überlappt die neue Pause die letzte (Dienst neu gestartet), gehören
	// beide zusammen.
	if n := len(ps); n > 0 && !ps[n-1].To.Before(from) {
		if ps[n-1].From.Before(from) {
			from = ps[n-1].From
		}
		ps = ps[:n-1]
	}
	ps = append(ps, Span{From: from})
	r.st.Pauses[sc.Host], r.dirty = ps, true
	key := "seite:" + sc.Host
	n := 0
	for _, o := range outageSpans(r.st.Events, nil, key, r.limits().ClearAfter, now) {
		if !o.From.Before(from) && total(minus([]Span{o}, []Span{{from, now}})) == 0 {
			n++
		}
	}
	if n > 0 {
		title, what, rest := ": Ausfall zählt nicht", "Ein früherer Ausfall", " lag in einer Pause: "+sc.Paused+" Er zählt nicht mehr als Ausfall."
		if n > 1 {
			title, what, rest = ": Ausfälle zählen nicht", strconv.Itoa(n)+" frühere Ausfälle", " lagen in einer Pause: "+sc.Paused+" Sie zählen nicht mehr als Ausfall."
		}
		r.pending = append(r.pending, Event{At: now, Kind: EvNote, Key: key, Level: LevelInfo, Title: sc.Host + title,
			Text: what + " von " + sc.Host + rest, Link: "#/ueberwachung", Quiet: true, Since: &from})
	}
}

// noteRestarts merkt sich die Neustarts je Container. Aufrufer hält r.mu.
func (r *Rules) noteRestarts(cs []ContainerState, now time.Time) {
	lim := r.limits()
	seen := map[string]bool{}
	for _, c := range cs {
		seen[c.Name] = true
		h := append(r.restarts[c.Name], restartSample{now, c.Restarts})
		for len(h) > 0 && now.Sub(h[0].at) > lim.RestartsIn {
			h = h[1:]
		}
		r.restarts[c.Name] = h
	}
	for n := range r.restarts {
		if !seen[n] {
			delete(r.restarts, n)
		}
	}
}

func pct(v float64) string { return strconv.Itoa(int(v+0.5)) + " %" }

func gbText(n int64) string {
	f := float64(n) / 1e9
	if f >= 100 {
		return strconv.FormatFloat(f, 'f', 0, 64) + " GB"
	}
	return strings.Replace(strconv.FormatFloat(f, 'f', 1, 64), ".", ",", 1) + " GB"
}

// minFree ist die feste Untergrenze für freien Platz. Sie gilt nur für
// Platten ab dem Vierfachen der Grenze; kleine Partitionen wie /boot mit
// 1 GB lägen sonst immer darunter und werden nur nach Anteil bewertet.
func minFree(limit, total int64) int64 {
	if total < 4*limit {
		return 0
	}
	return limit
}

// dockerProblem sagt, ob die Messung Docker nicht erreicht hat.
func dockerProblem(info Info) bool {
	for _, p := range info.Problems {
		if strings.HasPrefix(p, "Docker: ") {
			return true
		}
	}
	return false
}

// Abgeloest nennt Busy für einen abgelösten Server: Seine Domains zeigen
// auf den neuen Server, darum ruht hier nur die Prüfung der Seiten. Was der
// letzte Lauf gestoppt hat, zählt ohnehin als absichtlich aus (zustand,
// StoppedByMove); was hier trotzdem läuft, neu startet oder sich füllt,
// meldet die Überwachung weiter wie überall (Labor S6).
const Abgeloest = "abgelöst"

// pausedDuringRun sind die Warnungen, die ein Lauf von HostDeck selbst
// auslösen würde: Er stoppt Container (Backup, letzter Lauf) und legt sie
// auf dem Zielserver neu an (Umzug), und solange antworten Seiten nicht.
// Der Leistungstest stoppt nichts, lastet aber den Prozessor absichtlich
// voll aus: Bei ihm ruhen nur Prozessor und Steal. Tuning stoppt nichts:
// Alles bleibt an.
func pausedDuringRun(key, what string) bool {
	if what == Abgeloest {
		return strings.HasPrefix(key, "seite:")
	}
	if what == lauf.Leistungstest {
		return key == "prozessor" || key == "steal"
	}
	if what == lauf.Tuning {
		return false
	}
	if key == "docker" { // ein Umzug installiert oder startet Docker neu
		return true
	}
	for _, p := range []string{"aus:", "neustarts:", "gesundheit:", "seite:"} {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// unknown liefert die Prüfung, für welche Schlüssel die Quelle gerade
// ausgefallen ist. Solche Warnungen bleiben stehen, statt sich in eine
// Entwarnung aufzulösen. Aufrufer hält r.mu.
func (r *Rules) unknown(info Info) func(string) bool {
	badDisk := map[string]bool{}
	for _, p := range info.Problems {
		if rest, ok := strings.CutPrefix(p, "Platte "); ok {
			if i := strings.Index(rest, ": "); i > 0 {
				badDisk[rest[:i]] = true
			}
		}
	}
	docker, conts, busy := dockerProblem(info), r.contsBad, r.busyWhat
	sites, checked := r.sites, !r.sitesAt.IsZero()
	return func(k string) bool {
		if busy != "" && pausedDuringRun(k, busy) {
			return true
		}
		// Eine Seite ist erst wieder gut, wenn sie antwortet. Nach einem
		// Neustart des Dienstes fängt der Zähler bei 0 an; die ersten
		// Fehlversuche sind noch keine Warnung, aber auch keine Entwarnung.
		if h, ok := strings.CutPrefix(k, "seite:"); ok {
			if sc := sites[h]; (sc == nil && !checked) || (sc != nil && !sc.OK && sc.Paused == "") {
				return true
			}
		}
		switch {
		case docker && strings.HasPrefix(k, "gesundheit:"):
			return true
		case (docker || conts) && (strings.HasPrefix(k, "aus:") || strings.HasPrefix(k, "neustarts:")):
			return true
		}
		if m, ok := strings.CutPrefix(k, "platz:"); ok {
			return badDisk[m]
		}
		return false
	}
}

// findings sammelt, was gerade nicht in Ordnung ist. Aufrufer hält r.mu.
func (r *Rules) findings(f Frame, info Info, now time.Time) []Finding {
	lim := r.limits()
	var out []Finding
	add := func(x Finding) { out = append(out, x) }
	// Fällt die Messquelle selbst aus, weiß HostDeck über die Container nichts
	// mehr. Das ist eine eigene Warnung, keine Entwarnung.
	if dockerProblem(info) || r.contsBad {
		add(Finding{Key: "docker", Level: LevelErr, Icon: "projekte", Title: "Docker antwortet nicht", Hold: 2 * time.Minute,
			Text: "HostDeck erreicht den Docker-Dienst seit mindestens 2 Minuten nicht. Solange weiß niemand, wie es den Containern geht; laufende Container laufen meist weiter. Ein Blick in die Logs des Dienstes zeigt den Grund.",
			Link: "#/projekte", Go: "Projekte prüfen"})
	}
	// Platte.
	for _, d := range info.Disks {
		if d.Total <= 0 {
			continue
		}
		used := float64(d.Used) / float64(d.Total)
		free := d.Total - d.Used
		lvl := ""
		switch {
		case used >= lim.DiskErr || free < minFree(lim.DiskMinFree, d.Total):
			lvl = LevelErr
		case used >= lim.DiskWarn:
			lvl = LevelWarn
		default:
			continue
		}
		t := "Platte wird knapp"
		if lvl == LevelErr {
			t = "Platte fast voll"
		}
		// Immer mit Pfad: Bei mehreren Laufwerken sieht man sonst nicht,
		// welches gemeint ist.
		t += " (" + d.Mount + ")"
		add(Finding{Key: "platz:" + d.Mount, Level: lvl, Icon: "platz", Title: t,
			Text: pct(used*100) + " belegt, noch " + gbText(free) + " frei. Läuft sie voll, bleiben Datenbanken und Container stehen. Unter Speicherplatz siehst du, was weg kann.",
			Link: "#/platz", Go: "Aufräumen"})
	}
	v := f.Values
	// Arbeitsspeicher.
	if ram, ok := v[KeyRAM]; ok && info.RAMTotal > 0 {
		avail := float64(info.RAMTotal) - ram
		share := avail / float64(info.RAMTotal)
		if share < lim.MemWarn {
			lvl := LevelWarn
			if share < lim.MemErr {
				lvl = LevelErr
			}
			add(Finding{Key: "speicher", Level: lvl, Icon: "leistung", Title: "Arbeitsspeicher knapp", Hold: lim.MemHold,
				Text: "Seit mindestens " + minutes(lim.MemHold) + " nur noch " + gbText(int64(avail)) + " von " + gbText(info.RAMTotal) + " frei. Wird es eng, beendet Linux einen Prozess. Unter Überwachung siehst du, wer viel braucht.",
				Link: "#/ueberwachung", Go: "Verbrauch ansehen"})
		}
	}
	if cpu, ok := v[KeyCPU]; ok && cpu > lim.CPUWarn {
		add(Finding{Key: "prozessor", Level: LevelWarn, Icon: "leistung", Title: "Prozessor dauerhaft voll", Hold: lim.CPUHold,
			Text: "Seit mindestens " + minutes(lim.CPUHold) + " über " + pct(lim.CPUWarn) + " ausgelastet. Seiten antworten dann langsam. Unter Überwachung siehst du, welches Projekt so viel braucht.",
			Link: "#/ueberwachung", Go: "Verbrauch ansehen"})
	}
	if st, ok := v[KeySteal]; ok && st > lim.StealWarn {
		add(Finding{Key: "steal", Level: LevelWarn, Icon: "leistung", Title: "Der Hoster teilt den Prozessor knapp zu", Hold: lim.StealHold,
			Text: "Seit mindestens " + minutes(lim.StealHold) + " gehen über " + pct(lim.StealWarn) + " der Rechenzeit an andere Kunden des Hosters (Steal). Daran kannst du am Server nichts ändern; hält es an, lohnt eine Frage an den Hoster.",
			Link: "#/ueberwachung", Go: "Verlauf ansehen"})
	}
	// Container „unhealthy“ (aus der Messung, alle 10 s). Verwaltete
	// Server (etwa die Spielserver eines Panels) sind nur Anzeige und
	// lösen nie eine Warnung aus; die Messung kennt sie nicht, die
	// Abfrage jede Minute schon.
	managed := map[string]bool{}
	for _, c := range r.conts {
		if c.Managed {
			managed[c.Name] = true
		}
	}
	var sick []string
	for _, c := range info.Containers {
		if c.State == "running" && c.Health == "unhealthy" && !managed[c.Name] {
			sick = append(sick, c.Name)
		}
	}
	for _, n := range sick {
		add(Finding{Key: "gesundheit:" + n, Level: LevelWarn, Icon: "projekte", Title: n + " ist „unhealthy“", Hold: lim.UnhealthyFor,
			Text: "Die eigene Prüfung des Containers schlägt seit mindestens " + minutes(lim.UnhealthyFor) + " fehl. Er läuft noch, tut aber vielleicht nicht, was er soll. Meist steht der Grund am Ende der Logs.",
			Link: "#/projekte", Go: "Logs ansehen"})
	}
	// Container aus und Neustarts (jede Minute).
	for _, c := range r.conts {
		if c.Managed {
			continue
		}
		h := r.restarts[c.Name]
		// Von dir (oder vom letzten Lauf) gestoppt, startet er nicht mehr
		// neu, auch wenn die Neustarts der letzten Stunde noch zählen
		// (Labor S6: Schleife angehalten, Warnung blieb eine Stunde rot).
		if !c.StoppedByYou && (c.State == "restarting" || (len(h) > 1 && c.Restarts-h[0].n >= lim.Restarts && now.Sub(lastRestart(h)) < LoopFresh)) {
			add(Finding{Key: "neustarts:" + c.Name, Level: LevelErr, Icon: "projekte", Title: c.Name + " startet ständig neu",
				Text: "Mindestens " + strconv.Itoa(lim.Restarts) + " Neustarts in der letzten Stunde. Meist steht der Grund am Ende der Logs. Unter Projekte kannst du ihn ansehen und anhalten.",
				Link: "#/projekte", Go: "Logs ansehen"})
			continue
		}
		if !c.Running && !c.StoppedByYou && !c.Job && c.State != "created" && c.ExitCode != 0 {
			add(Finding{Key: "aus:" + c.Name, Level: LevelErr, Icon: "projekte", Title: c.Name + " ist unerwartet aus",
				Text: "Beendet mit Code " + strconv.Itoa(c.ExitCode) + exitHint(c.ExitCode) + ", ohne dass du ihn im Leitstand gestoppt hast. Unter Projekte siehst du die Logs und kannst ihn starten.",
				Link: "#/projekte", Go: "Projekt prüfen"})
		}
	}
	// Seiten.
	for _, sc := range r.sites {
		if sc.Paused != "" {
			continue
		}
		if sc.Fails >= lim.SiteFails {
			word := "antwortet nicht"
			if sc.Word != "" {
				word = sc.Word
			}
			f := Finding{Key: "seite:" + sc.Host, Level: LevelErr, Icon: "unlink", Title: sc.Host + " " + word,
				Text: "Seit " + strconv.Itoa(sc.Fails) + " Prüfungen hintereinander (" + sc.Answer + "). Geprüft wird vom Server selbst; ein Fehler bei DNS oder beim Hoster fällt hier nicht auf.",
				Link: "#/domains", Go: "Domain prüfen"}
			if sc.Why != "" {
				f.Text = sc.Why + " " + f.Text
			}
			if sc.Hint != "" {
				f.Level, f.Text = LevelWarn, sc.Hint+" "+f.Text
			}
			add(f)
		}
		if sc.CertUntil != nil {
			days := int(sc.CertUntil.Sub(now).Hours() / 24)
			if sc.CertUntil.Before(now) {
				days = -1
			}
			if days < lim.CertWarnDays {
				lvl := LevelWarn
				if days < lim.CertErrDays {
					lvl = LevelErr
				}
				when := "Es läuft in " + strconv.Itoa(days) + " Tagen ab."
				switch {
				case days < 0:
					when = "Es ist abgelaufen."
				case days == 0:
					when = "Es läuft heute ab."
				case days == 1:
					when = "Es läuft morgen ab."
				}
				add(Finding{Key: "zertifikat:" + sc.Host, Level: lvl, Icon: "domains", Title: "Zertifikat von " + sc.Host + " läuft ab",
					Text: when + " Caddy erneuert sonst rechtzeitig von selbst; klappt es nicht, erreicht die Domain meist den Server nicht (DNS) oder Port 80 ist zu.",
					Link: "#/domains", Go: "Zertifikat prüfen"})
			}
		}
	}
	// Backup.
	if r.LastBackup != nil {
		at, ok := r.LastBackup()
		if ok {
			if !r.st.NoBackup.IsZero() {
				r.st.NoBackup, r.dirty = time.Time{}, true
			}
		} else if r.st.NoBackup.IsZero() {
			// Kein einziges gültiges Backup: ab jetzt zählen, damit ein
			// neuer Server nicht still ohne Sicherheitsnetz bleibt.
			r.st.NoBackup, r.dirty = now, true
		}
		since := at
		if !ok {
			since = r.st.NoBackup
		}
		days := int(now.Sub(since).Hours() / 24)
		if days >= lim.BackupWarn {
			lvl := LevelWarn
			if days >= lim.BackupErr {
				lvl = LevelErr
			}
			x := Finding{Key: "backup", Level: lvl, Icon: "backup", Title: "Letztes gültiges Backup ist " + strconv.Itoa(days) + " Tage alt",
				Text: "Was seitdem dazukam, fehlt im Sicherheitsnetz. Ein neues Backup startest du unter Backup.",
				Link: "#/backup", Go: "Zum Backup"}
			if !ok {
				x.Title = "Seit " + strconv.Itoa(days) + " Tagen kein gültiges Backup"
				x.Text = "Auf diesem Server gibt es noch kein gültiges Backup. Backups laufen nur, wenn du sie startest; das geht unter Backup."
			}
			add(x)
		}
	}
	return out
}

func exitHint(code int) string {
	switch code {
	case 137:
		return " (beendet von außen, oft weil der Arbeitsspeicher voll war)"
	case 139:
		return " (Absturz)"
	case 143:
		return " (von außen beendet)"
	}
	return ""
}

func minutes(d time.Duration) string {
	m := int(d.Minutes())
	if m == 1 {
		return "1 Minute"
	}
	return strconv.Itoa(m) + " Minuten"
}

// apply führt die Warnungen nach. unknown sagt, für welche Schlüssel die
// Quelle gerade ausgefallen ist: Sie bekommen keine Entwarnung, weil
// niemand weiß, ob es besser wurde. Aufrufer hält r.mu.
func (r *Rules) apply(found []Finding, unknown func(string) bool, now time.Time) []Event {
	lim := r.limits()
	var events []Event
	ev := func(kind string, a *Alert) {
		events = append(events, Event{At: now, Kind: kind, Key: a.Key, Level: a.Level, Title: a.Title, Text: a.Text, Link: a.Link})
	}
	seen := map[string]bool{}
	for _, x := range found {
		if seen[x.Key] {
			continue
		}
		seen[x.Key] = true
		if a, ok := r.st.Active[x.Key]; ok {
			worse := rank(x.Level) > rank(a.Level)
			a.GoodFrom = nil
			// Solange die Stufe oben gehalten wird, bleiben auch Titel
			// und Text der schlimmeren Stufe: Eine rote Erinnerung mit
			// gelbem Wortlaut wäre widersprüchlich.
			take := true
			switch {
			case rank(x.Level) >= rank(a.Level):
				a.Level, a.MildFrom = x.Level, nil
			case a.MildFrom == nil:
				t := now
				a.MildFrom = &t
				take = false
			case now.Sub(*a.MildFrom) >= lim.ClearAfter:
				a.Level, a.MildFrom = x.Level, nil
			default:
				take = false
			}
			if take {
				a.Icon, a.Title, a.Text, a.Link, a.Go = x.Icon, x.Title, x.Text, x.Link, x.Go
			}
			switch {
			case worse:
				// Schlimmer als das, was du so gelassen hast: wieder eine
				// Warnung wie jede andere.
				a.Notified, a.Accepted = now, nil
				ev(EvWorse, a)
			case a.Accepted != nil:
				// Bewusst so gelassen: keine Erinnerung.
			case now.Sub(a.Notified) >= lim.RemindAfter:
				a.Notified = now
				ev(EvRemind, a)
			}
			continue
		}
		delete(r.gone, x.Key)
		r.hold[x.Key] = x.Hold
		first, ok := r.first[x.Key]
		if !ok {
			first = now
			r.first[x.Key] = now
		}
		if now.Sub(first) < x.Hold {
			continue
		}
		delete(r.first, x.Key)
		delete(r.hold, x.Key)
		a := &Alert{Key: x.Key, Level: x.Level, Icon: x.Icon, Title: x.Title, Text: x.Text, Link: x.Link, Go: x.Go, Since: first, Notified: now}
		r.st.Active[x.Key] = a
		ev(EvStart, a)
	}
	// Eine Wartezeit verträgt kurze Lücken: Ein Server, der bei 95 % steht
	// und kurz einbricht, meldet sonst nie. Die Lücke darf höchstens ein
	// Zehntel der Wartezeit lang sein (bei 15 Minuten 90 Sekunden, bei
	// 2 Minuten zwei Messungen); sonst fängt die Zeit neu an, und zwei
	// einzelne Aussetzer Minuten auseinander lösen keine Warnung aus.
	for k := range r.first {
		if seen[k] {
			continue
		}
		g, ok := r.gone[k]
		if !ok {
			r.gone[k] = now
			continue
		}
		if now.Sub(g) > r.hold[k]/10 {
			delete(r.first, k)
			delete(r.gone, k)
			delete(r.hold, k)
		}
	}
	for k, a := range r.st.Active {
		if seen[k] {
			continue
		}
		// Eine Domain, die nicht mehr geprüft wird (abgeschaltet oder aus
		// Caddy entfernt), ist nicht „wieder in Ordnung“: Die Warnung endet
		// gleich, mit einem Hinweis statt einer Entwarnung.
		if h, ok := strings.CutPrefix(k, "seite:"); ok && !r.sitesAt.IsZero() && r.sites[h] == nil {
			delete(r.st.Active, k)
			end := now
			events = append(events, Event{At: now, Kind: EvNote, Key: k, Level: LevelInfo, Title: h + " wird nicht mehr geprüft",
				Text: "Die Domain ist abgeschaltet oder steht nicht mehr in Caddy; die Warnung „" + a.Title + "“ ist damit erledigt.", Link: "#/domains", End: &end})
			continue
		}
		// Pausiert: Du hast das Ziel gestoppt, während die Seite als Ausfall
		// gewarnt war. Das ist keine Entwarnung (nichts antwortet wieder),
		// sondern ein stiller Hinweis ohne Mitteilung; der Ausfall endet mit
		// Beginn der Pause.
		if h, ok := strings.CutPrefix(k, "seite:"); ok && r.sites[h] != nil && r.sites[h].Paused != "" {
			sc := r.sites[h]
			end := now
			if sc.PausedSince != nil && sc.PausedSince.After(a.Since) && sc.PausedSince.Before(now) {
				end = *sc.PausedSince
			}
			delete(r.st.Active, k)
			since := a.Since
			events = append(events, Event{At: now, Kind: EvNote, Key: k, Level: LevelInfo, Title: h + " ist pausiert",
				Text: sc.Paused + " Die Warnung ist damit erledigt; bis zur Pause zählt sie als Ausfall.", Link: "#/ueberwachung", Since: &since, End: &end, Quiet: true})
			continue
		}
		// Von dir gestoppt: Eine Warnung zu einem Container (unhealthy,
		// Neustarts, unerwartet aus), der jetzt von dir gestoppt ist, endet
		// still wie eine Seite in einer Pause. Sonst käme nach 10 Minuten
		// „… ist wieder gesund“ als Mitteilung, obwohl er aus ist.
		if n := alertContainer(k); n != "" && r.stoppedByYou(n) {
			delete(r.st.Active, k)
			end, since := now, a.Since
			events = append(events, Event{At: now, Kind: EvNote, Key: k, Level: LevelInfo, Title: n + " ist von dir gestoppt",
				Text: "Die Warnung „" + a.Title + "“ ist damit erledigt.", Link: "#/projekte", Since: &since, End: &end, Quiet: true})
			continue
		}
		// Abgelöst: Die Seitenprüfung ruht hier für immer, die Domains prüft
		// der neue Server. Eine alte Seiten-Warnung bliebe sonst ewig rot.
		if h, ok := strings.CutPrefix(k, "seite:"); ok && r.busyWhat == Abgeloest {
			delete(r.st.Active, k)
			end := now
			events = append(events, Event{At: now, Kind: EvNote, Key: k, Level: LevelInfo, Title: h + " wird hier nicht mehr geprüft",
				Text: "Dieser Server ist abgelöst, seine Domains prüft der neue Server; die Warnung „" + a.Title + "“ ist hier damit erledigt.", Link: "#/domains", End: &end})
			continue
		}
		// Unbekannt (Quelle aus, oder ein Lauf von HostDeck arbeitet): Was
		// vorher gut aussah, zählt nicht weiter zur Entwarnung.
		if unknown != nil && unknown(k) {
			a.GoodFrom = nil
			continue
		}
		if a.GoodFrom == nil {
			t := now
			a.GoodFrom = &t
			continue
		}
		if now.Sub(*a.GoodFrom) >= lim.ClearAfter {
			delete(r.st.Active, k)
			since, good := a.Since, *a.GoodFrom
			e := Event{At: now, Kind: EvClear, Key: a.Key, Level: LevelInfo, Title: ClearTitle(a.Key, a.Title),
				Text: "Die Warnung „" + a.Title + "“ lief " + Duration(good.Sub(since)) + ".", Link: a.Link, Since: &since, Good: &good}
			if a.Accepted != nil {
				// Du hattest sie so gelassen: keine Mitteilung, nur Verlauf.
				e.Quiet = true
				e.Text += " Du hattest sie bewusst so gelassen; tritt sie neu auf, warnt HostDeck wieder."
			}
			events = append(events, e)
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Key < events[j].Key })
	return events
}

// RulesView ist der Stand für den Leitstand.
type RulesView struct {
	Active  []Alert     `json:"aktiv"`
	Events  []Event     `json:"verlauf"` // neueste zuerst
	Sites   []SiteCheck `json:"seiten"`
	SitesAt *time.Time  `json:"seiten_geprueft,omitempty"`
	Limits  Limits      `json:"grenzen"`
	// Backup: die eine Stufe für Pille, Sicherheitsnetz und Hinweise.
	Backup BackupView `json:"backup"`
	// Waiting: Bedingungen, deren Wartezeit läuft (noch keine Warnung).
	// Der Leitstand färbt Prozessor und Arbeitsspeicher erst, wenn eine
	// davon eine Weile läuft, nicht bei jeder kurzen Spitze.
	Waiting []Waiting `json:"wartet"`
}

// Waiting ist eine Bedingung, die ihre Wartezeit abwartet.
type Waiting struct {
	Key   string    `json:"schluessel"`
	Since time.Time `json:"seit"`
	Hold  int       `json:"minuten"` // Wartezeit bis zur Warnung
}

// BackupView ist der Stand des Backups nach der Regel „Backup alt“.
type BackupView struct {
	Known bool       `json:"bekannt"` // ohne Quelle (Tests): false
	Valid bool       `json:"gueltig"` // es gibt ein gültiges Backup
	At    *time.Time `json:"zuletzt,omitempty"`
	Since *time.Time `json:"ohne_seit,omitempty"` // ohne gültiges Backup seit
	Days  int        `json:"tage"`                // Alter in Tagen
	Level string     `json:"stufe"`               // ok, idle (neutral), warn, err
}

// backupView rechnet die Stufe wie die Regel: gelb ab BackupWarn Tagen,
// rot ab BackupErr; davor grün mit Backup, neutral ohne. Aufrufer hält r.mu.
func (r *Rules) backupView(now time.Time) BackupView {
	if r.LastBackup == nil {
		return BackupView{Level: LevelIdle}
	}
	lim := r.limits()
	at, ok := r.LastBackup()
	b := BackupView{Known: true, Valid: ok}
	since := at
	if ok {
		b.At = &at
	} else {
		since = r.st.NoBackup
		if since.IsZero() {
			since = now
		}
		b.Since = &since
	}
	b.Days = int(now.Sub(since).Hours() / 24)
	switch {
	case b.Days >= lim.BackupErr:
		b.Level = LevelErr
	case b.Days >= lim.BackupWarn:
		b.Level = LevelWarn
	case ok:
		b.Level = LevelOK
	default:
		b.Level = LevelIdle
	}
	return b
}

// siteView rechnet Ausfälle, Pausen und Verfügbarkeit einer Seite: eine
// Rechnung für Seiten, Tagesbalken und Domains. Pausen zählen
// weder als Ausfall noch als Messzeit. Aufrufer hält r.mu.
func (r *Rules) siteView(x SiteCheck, now time.Time) SiteCheck {
	key := "seite:" + x.Host
	clearAfter := r.limits().ClearAfter
	if clearAfter <= 0 {
		clearAfter = DefaultLimits.ClearAfter
	}
	act := r.st.Active[key]
	from := now.Add(-KeepEvents)
	if t, ok := r.st.SitesSince[x.Host]; ok {
		t := t
		x.Since = &t
		if t.After(from) {
			from = t
		}
	}
	x.From = &from
	pauses := clip(r.st.Pauses[x.Host], from, now)
	var outs []Span
	x.Outages, x.LastDown = 0, nil
	for _, o := range outageSpans(r.st.Events, act, key, clearAfter, now) {
		part := minus(clip([]Span{o}, from, now), pauses)
		// Eine Warnung, die gerade erst beginnt, ist schon ein Ausfall,
		// auch wenn sie noch keine Minute dauert.
		running := act != nil && act.GoodFrom == nil && o.To.Equal(now) && !o.From.Before(from) && !within(pauses, now, now)
		if total(part) <= 0 && !running {
			continue
		}
		outs = append(outs, part...)
		x.Outages++
		t := o.From
		if len(part) > 0 {
			t = part[0].From
		}
		x.LastDown = &t
	}
	x.Pauses, x.Outs = pauses, outs
	if x.Pauses == nil {
		x.Pauses = []Span{}
	}
	if x.Outs == nil {
		x.Outs = []Span{}
	}
	measured := now.Sub(from) - total(pauses)
	down := total(outs)
	x.Measured, x.Down = int64(measured/time.Second), int64(down/time.Second)
	if measured > 0 {
		a := 100 * (1 - float64(down)/float64(measured))
		x.Avail = &a
	}
	x.DownDays = perDay(outs, now)
	return x
}

// ClearTitle ist der Titel einer Entwarnung: was wieder gut ist, nicht der
// Satz der Warnung. title ist der Titel der Warnung.
func ClearTitle(key, title string) string {
	kind, what, _ := strings.Cut(key, ":")
	switch kind {
	case "seite":
		return what + " antwortet wieder"
	case "zertifikat":
		return "Zertifikat von " + what + " erneuert"
	case "platz":
		return "Platte " + what + " hat wieder Platz"
	case "speicher":
		return "Arbeitsspeicher reicht wieder"
	case "prozessor":
		return "Prozessor wieder im normalen Bereich"
	case "steal":
		return "Steal wieder niedrig"
	case "gesundheit":
		return what + " ist wieder gesund"
	case "neustarts":
		return what + " startet nicht mehr ständig neu"
	case "aus":
		return what + " läuft wieder"
	case "docker":
		return "Docker antwortet wieder"
	case "backup":
		return "Backup wieder aktuell"
	}
	return "Wieder in Ordnung: " + title
}

// Duration ist eine Dauer in Worten ohne Uhrzeit: „8 Minuten“, „2 Stunden
// 5 Minuten“, „3 Tage 4 Stunden“.
func Duration(d time.Duration) string {
	m := int((d + 30*time.Second) / time.Minute)
	if m < 1 {
		return "weniger als eine Minute"
	}
	unit := func(n int, one, many string) string {
		if n == 1 {
			return "1 " + one
		}
		return strconv.Itoa(n) + " " + many
	}
	switch {
	case m < 60:
		return unit(m, "Minute", "Minuten")
	case m < 48*60:
		s := unit(m/60, "Stunde", "Stunden")
		if m%60 > 0 {
			s += " " + unit(m%60, "Minute", "Minuten")
		}
		return s
	}
	s := unit(m/1440, "Tag", "Tage")
	if h := m % 1440 / 60; h > 0 {
		s += " " + unit(h, "Stunde", "Stunden")
	}
	return s
}

// CurrentLimits sind die geltenden Grenzen (für Hinweise und Leitstand).
func (r *Rules) CurrentLimits() Limits {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.load()
	return r.limits()
}

// alertContainer nennt den Container einer Container-Warnung, sonst "".
func alertContainer(key string) string {
	for _, p := range []string{"gesundheit:", "neustarts:", "aus:"} {
		if n, ok := strings.CutPrefix(key, p); ok {
			return n
		}
	}
	return ""
}

// stoppedByYou: Laut der letzten Abfrage (jede Minute) hast du den
// Container gestoppt. Aufrufer hält r.mu.
func (r *Rules) stoppedByYou(name string) bool {
	for _, c := range r.conts {
		if c.Name == name {
			return c.StoppedByYou && !c.Running
		}
	}
	return false
}

// RecheckSites lässt die nächste Prüfung (alle 10 s) die Seiten gleich neu
// prüfen, etwa nachdem du eine Domain abgeschaltet hast. Läuft gerade eine
// Seitenprüfung, folgt gleich danach noch eine.
func (r *Rules) RecheckSites() {
	r.mu.Lock()
	if r.sitesBusy {
		r.sitesNext = true
	} else {
		r.sitesAt = time.Time{}
	}
	r.mu.Unlock()
}

// View liefert laufende Warnungen (rote zuerst), die letzten Ereignisse
// und den Stand der Seiten.
func (r *Rules) View(events int) RulesView {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.load()
	v := RulesView{Active: []Alert{}, Events: []Event{}, Sites: []SiteCheck{}, Limits: r.limits()}
	for _, a := range r.st.Active {
		x := *a
		x.CanAccept = Acceptable(x.Key)
		v.Active = append(v.Active, x)
	}
	sort.Slice(v.Active, func(i, j int) bool {
		if (v.Active[i].Level == LevelErr) != (v.Active[j].Level == LevelErr) {
			return v.Active[i].Level == LevelErr
		}
		return v.Active[i].Since.Before(v.Active[j].Since)
	})
	for i := len(r.st.Events) - 1; i >= 0 && len(v.Events) < events; i-- {
		v.Events = append(v.Events, r.st.Events[i])
	}
	now := r.now()
	for i, e := range v.Events {
		// Ereignisse in einer Pause ihrer Seite zählen nicht (Altbestand K1).
		if h, ok := strings.CutPrefix(e.Key, "seite:"); ok && e.Kind != EvNote && within(r.st.Pauses[h], e.At, now) {
			v.Events[i].InPause = true
		}
		// Entwarnungen aus älteren Versionen tragen noch den Satz der
		// Warnung („Wieder in Ordnung: … antwortet nicht“, V10).
		if old, ok := strings.CutPrefix(e.Title, "Wieder in Ordnung: "); ok && e.Kind == EvClear {
			v.Events[i].Title = ClearTitle(e.Key, old)
		}
	}
	for _, sc := range r.sites {
		v.Sites = append(v.Sites, r.siteView(*sc, now))
	}
	v.Backup = r.backupView(now)
	v.Waiting = []Waiting{}
	for k, t := range r.first {
		v.Waiting = append(v.Waiting, Waiting{Key: k, Since: t, Hold: int(r.hold[k] / time.Minute)})
	}
	sort.Slice(v.Waiting, func(i, j int) bool { return v.Waiting[i].Key < v.Waiting[j].Key })
	sort.Slice(v.Sites, func(i, j int) bool { return v.Sites[i].Host < v.Sites[j].Host })
	if !r.sitesAt.IsZero() {
		t := r.sitesAt
		v.SitesAt = &t
	}
	return v
}

// Accept lässt eine laufende Warnung bewusst so (on) oder nimmt das zurück.
// Es ändert nichts am Server; Verlauf und Ereignisse halten es fest, ohne
// Mitteilung. Der Text ist der Satz fürs Protokoll, leer, wenn es schon so
// war.
func (r *Rules) Accept(key string, on bool) (string, error) {
	now := r.now()
	r.mu.Lock()
	r.load()
	a, ok := r.st.Active[key]
	if !ok {
		r.mu.Unlock()
		return "", ErrNoAlert
	}
	if on && !Acceptable(key) {
		r.mu.Unlock()
		return "", ErrNotAcceptable
	}
	if (a.Accepted != nil) == on {
		// Schon so: nichts zu tun, nichts fürs Protokoll.
		r.mu.Unlock()
		return "", nil
	}
	e := Event{At: now, Kind: EvNote, Key: key, Level: LevelInfo, Link: a.Link, Quiet: true}
	var text string
	if on {
		t := now
		a.Accepted = &t
		e.Title = "Bewusst so gelassen: " + a.Title
		e.Text = "HostDeck warnt dazu nicht weiter. Wird es schlimmer oder tritt es nach seinem Ende neu auf, warnt HostDeck wieder."
		text = "Bleibt bewusst so: " + a.Title
	} else {
		a.Accepted = nil
		a.Notified = now
		e.Title = "Warnt wieder: " + a.Title
		e.Text = "Die Warnung steht wieder unter „Braucht dich“."
		text = "Warnt wieder: " + a.Title
	}
	err := r.record([]Event{e}, now)
	r.mu.Unlock()
	_ = r.Journal.rule(e)
	return text, err
}

// BootTime liest den Start des Servers aus /proc/stat (btime).
func BootTime(proc string) time.Time {
	if proc == "" {
		proc = "/proc"
	}
	b, err := os.ReadFile(filepath.Join(proc, "stat"))
	if err != nil {
		return time.Time{}
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "btime "); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64); err == nil {
				return time.Unix(n, 0)
			}
		}
	}
	return time.Time{}
}
