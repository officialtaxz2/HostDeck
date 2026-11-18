package ueberwachung

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Ereignisse: was auf dem Server passiert ist,
// für „Heute auf einen Blick“, „Was passiert ist“, die Marken in der
// Prozessor-Kurve und das Seitenfenster eines Projekts. 30 Tage.
//
// Die Quellen: die Warnungen der Regeln (Domain gestört und wieder da,
// Container unerwartet aus, Platte, Zertifikat läuft ab …), dazu, was die
// Regeln nicht als Warnung kennen: Projekt gestartet oder von dir gestoppt,
// Zertifikat erneuert (neues Ablaufdatum), Prozessor-Spitze. Der Dienst
// trägt Backup, Download und Update selbst ein.

// Arten von Ereignissen.
const (
	HapProject  = "projekt"
	HapDomain   = "domain"
	HapCert     = "zertifikat"
	HapSpike    = "spitze"
	HapBackup   = "backup"
	HapUpdate   = "update"
	HapServer   = "server"
	HapSystem   = "system"
	HapSecurity = "sicherheit"
)

// Happening ist ein Ereignis.
type Happening struct {
	At      time.Time `json:"zeit"`
	Kind    string    `json:"art"`
	Level   string    `json:"stufe"` // ok, info, warn, err
	Title   string    `json:"titel"`
	Text    string    `json:"text,omitempty"`
	Link    string    `json:"link,omitempty"`
	Project string    `json:"projekt,omitempty"`
	Host    string    `json:"domain,omitempty"`
	// Value: bei einer Spitze der höchste Wert, Prozent des Servers.
	Value float64 `json:"wert,omitempty"`
	// Until: bei einer Spitze ihr Ende.
	Until *time.Time `json:"bis,omitempty"`
}

// Grenzen des Ereignis-Verlaufs.
const (
	KeepHappenings = 30 * 24 * time.Hour
	MaxHappenings  = 2000
	// SpikeGap: Nach einer Spitze zählt die nächste erst nach dieser Zeit
	// als eigenes Ereignis; sonst füllt ein unruhiger Server die Liste.
	SpikeGap = 30 * time.Minute
)

// Journal sammelt die Ereignisse. Alle Methoden vertragen nil.
type Journal struct {
	File string // leer: nur im Speicher
	Now  func() time.Time

	mu     sync.Mutex
	loaded bool
	found  bool // die Datei lag schon beim Laden
	st     journalState
	run    map[string]containerSeen // letzter Stand je Container
	spike  *spikeRun
}

type journalState struct {
	List []Happening `json:"ereignisse"`
	// Certs: je Domain das zuletzt gesehene Ablaufdatum des Zertifikats.
	Certs map[string]time.Time `json:"zertifikate,omitempty"`
	// Version von HostDeck beim letzten Start (Update erkennen).
	Version string `json:"version,omitempty"`
	// Since: seit wann HostDeck Ereignisse sammelt („Deine Woche“ erst, wenn
	// die ganze Woche abgedeckt ist).
	Since time.Time `json:"seit,omitempty"`
}

type containerSeen struct {
	running  bool
	restarts int
}

type spikeRun struct {
	from, last time.Time
	peak       float64
	who        string
}

func (j *Journal) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}

// load liest den Stand einmal. Aufrufer hält j.mu.
func (j *Journal) load() {
	if j.loaded {
		return
	}
	j.loaded = true
	j.run = map[string]containerSeen{}
	if j.File == "" {
		return
	}
	b, err := os.ReadFile(j.File)
	if err != nil {
		return
	}
	j.found = true
	var st journalState
	if json.Unmarshal(b, &st) == nil {
		j.st = st
	}
	if j.st.Since.IsZero() && len(j.st.List) > 0 {
		j.st.Since = j.st.List[0].At
	}
}

// Begun sagt, seit wann HostDeck Ereignisse sammelt; null, solange noch nie.
func (j *Journal) Begun() time.Time {
	if j == nil {
		return time.Time{}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.load()
	return j.st.Since
}

// save schreibt den Stand. Aufrufer hält j.mu.
func (j *Journal) save() error {
	if j.File == "" {
		return nil
	}
	b, err := json.Marshal(j.st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(j.File), 0o700); err != nil {
		return err
	}
	tmp := j.File + ".neu"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, j.File)
}

// add hängt an, kürzt auf 30 Tage und schreibt. Aufrufer hält j.mu.
func (j *Journal) add(hs ...Happening) error {
	if len(hs) == 0 {
		return nil
	}
	now := j.now()
	for _, h := range hs {
		if h.At.IsZero() {
			h.At = now
		}
		j.st.List = append(j.st.List, h)
	}
	// Spitzen tragen ihren Beginn als Zeit: nach Zeit ordnen.
	sort.SliceStable(j.st.List, func(a, b int) bool { return j.st.List[a].At.Before(j.st.List[b].At) })
	cut := 0
	for cut < len(j.st.List) && now.Sub(j.st.List[cut].At) > KeepHappenings {
		cut++
	}
	if n := len(j.st.List) - cut; n > MaxHappenings {
		cut += n - MaxHappenings
	}
	if cut > 0 {
		j.st.List = append([]Happening(nil), j.st.List[cut:]...)
	}
	return j.save()
}

// Add trägt ein Ereignis ein (Backup, Download, Projekt aktualisiert).
func (j *Journal) Add(h Happening) error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.load()
	return j.add(h)
}

// Since liefert die Ereignisse ab from, neueste zuerst.
func (j *Journal) Since(from time.Time) []Happening {
	out := []Happening{}
	if j == nil {
		return out
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.load()
	for i := len(j.st.List) - 1; i >= 0; i-- {
		h := j.st.List[i]
		if h.At.Before(from) {
			break
		}
		out = append(out, h)
	}
	return out
}

// Started merkt sich die Version beim Start des Dienstes. Ist sie neu,
// war ein Update: ein Ereignis. Beim allerersten Start auf einem neuen
// Server nicht. Fehlt nur die Ereignis-Datei, lief HostDeck hier aber schon
// (Update von einer Fassung ohne Ereignisse), ist das Update das erste
// Ereignis.
func (j *Journal) Started(version string) error {
	if j == nil || version == "" {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.load()
	if j.st.Since.IsZero() {
		j.st.Since = j.now()
	}
	old := j.st.Version
	if old == version {
		return j.save()
	}
	j.st.Version = version
	if old == "" {
		if j.found || !j.ranBefore() {
			return j.save()
		}
		return j.add(Happening{Kind: HapUpdate, Level: LevelOK, Title: "HostDeck aktualisiert auf " + version,
			Text: "Ab jetzt sammelt HostDeck hier, was auf dem Server passiert.", Link: "#/einstellungen/updates"})
	}
	return j.add(Happening{Kind: HapUpdate, Level: LevelOK, Title: "HostDeck aktualisiert auf " + version,
		Text: "Vorher lief " + old + ".", Link: "#/einstellungen/updates"})
}

// ranBefore sagt, ob neben der Ereignis-Datei schon etwas von der
// Überwachung liegt (Verlauf, Warnungen): HostDeck lief hier also schon.
func (j *Journal) ranBefore() bool {
	if j.File == "" {
		return false
	}
	es, err := os.ReadDir(filepath.Dir(j.File))
	if err != nil {
		return false
	}
	base := filepath.Base(j.File)
	for _, e := range es {
		if n := e.Name(); n != base && n != base+".neu" {
			return true
		}
	}
	return false
}

// rule übernimmt ein Ereignis der Regeln. Erinnerungen wiederholen nur,
// was schon drinsteht.
func (j *Journal) rule(e Event) error {
	if j == nil || e.Kind == EvRemind {
		return nil
	}
	h := Happening{At: e.At, Title: e.Title, Text: e.Text, Link: e.Link, Level: e.Level}
	switch e.Kind {
	case EvClear:
		h.Level = LevelOK
	case EvNote:
		h.Level = LevelInfo
	}
	key := e.Key
	switch {
	case strings.HasPrefix(key, "seite:"):
		h.Kind, h.Host = HapDomain, strings.TrimPrefix(key, "seite:")
	case strings.HasPrefix(key, "zertifikat:"):
		h.Kind, h.Host = HapCert, strings.TrimPrefix(key, "zertifikat:")
	case strings.HasPrefix(key, "aus:"), strings.HasPrefix(key, "neustarts:"), strings.HasPrefix(key, "gesundheit:"):
		h.Kind = HapProject
		_, h.Project, _ = strings.Cut(key, ":")
	case key == "backup":
		h.Kind = HapBackup
	case key == "neustart":
		h.Kind = HapServer
	case key == "schutz-verloren":
		h.Kind = HapSecurity
	default:
		h.Kind = HapSystem
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.load()
	return j.add(h)
}

// containers vergleicht den Stand der Container mit dem letzten: gestartet
// oder von dir gestoppt, je Projekt ein Ereignis. Unerwartet aus und
// Neustart-Schleifen melden die Regeln als Warnung. Während eines Laufs
// von HostDeck (Backup, Umzug) startet und stoppt HostDeck selbst: dann nichts.
func (j *Journal) containers(cs []ContainerState, busy string, now time.Time) error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.load()
	first := len(j.run) == 0
	next := make(map[string]containerSeen, len(cs))
	type change struct{ started, stopped []string }
	byProject := map[string]*change{}
	var order []string
	for _, c := range cs {
		next[c.Name] = containerSeen{c.Running, c.Restarts}
		old, seen := j.run[c.Name]
		if first || !seen || busy != "" || c.Job || c.Managed || old.running == c.Running {
			continue
		}
		// Mehr Neustarts als zuletzt: Docker hat ihn nach einem Absturz neu
		// gestartet. Das zählt nicht als „gestartet“.
		if c.Running && c.Restarts > old.restarts {
			continue
		}
		if !c.Running && !c.StoppedByYou {
			continue
		}
		p := c.Project
		if p == "" {
			p = "\x00" + c.Name
		}
		ch := byProject[p]
		if ch == nil {
			ch = &change{}
			byProject[p] = ch
			order = append(order, p)
		}
		if c.Running {
			ch.started = append(ch.started, c.Name)
		} else {
			ch.stopped = append(ch.stopped, c.Name)
		}
	}
	j.run = next
	var hs []Happening
	for _, p := range order {
		ch := byProject[p]
		name, project := p, p
		if strings.HasPrefix(p, "\x00") {
			name, project = p[1:], ""
		}
		link := "#/projekte"
		if project != "" {
			link = "#/projekte/project:" + project
		}
		if len(ch.started) > 0 {
			hs = append(hs, Happening{At: now, Kind: HapProject, Level: LevelOK, Project: name, Title: name + " gestartet", Text: containerList(project, ch.started), Link: link})
		}
		if len(ch.stopped) > 0 {
			hs = append(hs, Happening{At: now, Kind: HapProject, Level: LevelInfo, Project: name, Title: name + " gestoppt", Text: joinText("Von dir gestoppt.", containerList(project, ch.stopped)), Link: link})
		}
	}
	return j.add(hs...)
}

func containerList(project string, names []string) string {
	if project == "" {
		return ""
	}
	if len(names) == 1 {
		return "Container " + names[0] + "."
	}
	return "Container " + strings.Join(names, ", ") + "."
}

func joinText(a, b string) string {
	if b == "" {
		return a
	}
	return a + " " + b
}

// sites erkennt erneuerte Zertifikate: Das Ablaufdatum rückt um mehr als
// einen Tag nach hinten.
func (j *Journal) sites(list []SiteCheck, now time.Time) error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.load()
	if j.st.Certs == nil {
		j.st.Certs = map[string]time.Time{}
	}
	var hs []Happening
	changed := false
	for _, sc := range list {
		if sc.CertUntil == nil {
			continue
		}
		until := *sc.CertUntil
		old, ok := j.st.Certs[sc.Host]
		if ok && until.Sub(old) > 24*time.Hour {
			hs = append(hs, Happening{At: now, Kind: HapCert, Level: LevelOK, Host: sc.Host, Title: "Zertifikat von " + sc.Host + " erneuert",
				Text: "Gültig bis " + until.Local().Format("02.01.2006") + ".", Link: "#/domains"})
		}
		if !ok || !until.Equal(old) {
			j.st.Certs[sc.Host] = until
			changed = true
		}
	}
	if len(hs) > 0 {
		return j.add(hs...)
	}
	if changed {
		return j.save()
	}
	return nil
}

// frame erkennt Spitzen des Prozessors: über der Grenze für „Prozessor
// dauerhaft voll“, aber kürzer als deren Wartezeit (sonst warnt die Regel).
// Das Ereignis entsteht, wenn die Spitze vorbei ist, mit dem höchsten Wert
// und dem Projekt, das in dem Moment am meisten brauchte.
func (j *Journal) frame(f Frame, info Info, lim Limits, now time.Time) error {
	if j == nil {
		return nil
	}
	cpu, ok := f.Values[KeyCPU]
	if !ok {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.load()
	if cpu > lim.CPUWarn {
		if j.spike == nil {
			j.spike = &spikeRun{from: now}
		}
		j.spike.last = now
		if cpu >= j.spike.peak {
			j.spike.peak = cpu
			j.spike.who = topConsumer(f, info)
		}
		return nil
	}
	s := j.spike
	if s == nil {
		return nil
	}
	j.spike = nil
	// Hielt es so lange, warnt die Regel „Prozessor dauerhaft voll“.
	if lim.CPUHold > 0 && now.Sub(s.from) >= lim.CPUHold {
		return nil
	}
	for i := len(j.st.List) - 1; i >= 0; i-- {
		h := j.st.List[i]
		if s.from.Sub(h.At) > SpikeGap {
			break
		}
		if h.Kind == HapSpike {
			return nil
		}
	}
	end := now
	took := end.Sub(s.from)
	text := ""
	if s.who != "" {
		text = "Am meisten brauchte " + s.who + "."
	}
	return j.add(Happening{At: s.from, Until: &end, Kind: HapSpike, Level: LevelWarn, Value: s.peak, Project: strings.TrimPrefix(s.who, "Projekt "),
		Title: "Prozessor " + pct(s.peak) + " für " + spikeDuration(took), Text: text, Link: "#/ueberwachung"})
}

// topConsumer ist das Projekt (oder der einzelne Container) mit dem
// meisten Prozessor in dieser Messung.
func topConsumer(f Frame, info Info) string {
	inProject := map[string]bool{}
	for _, c := range info.Containers {
		if c.Project != "" {
			inProject[c.Name] = true
		}
	}
	best, who := 0.0, ""
	for k, v := range f.Values {
		if !strings.HasSuffix(k, "/"+ValCPU) || v <= best {
			continue
		}
		switch {
		case strings.HasPrefix(k, "p/"):
			best, who = v, "Projekt "+strings.TrimSuffix(strings.TrimPrefix(k, "p/"), "/"+ValCPU)
		case strings.HasPrefix(k, "c/"):
			n := strings.TrimSuffix(strings.TrimPrefix(k, "c/"), "/"+ValCPU)
			if !inProject[n] {
				best, who = v, n
			}
		}
	}
	// Unter einem Zehntel Kern war es nicht ein Projekt, sondern das System.
	if best < 10 {
		return ""
	}
	return who
}

func spikeDuration(d time.Duration) string {
	if d < time.Minute {
		s := int((d + time.Second/2) / time.Second)
		if s < 2 {
			return "einen Moment"
		}
		return strconv.Itoa(s) + " Sekunden"
	}
	return Duration(d)
}
