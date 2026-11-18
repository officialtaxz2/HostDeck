package anmeldung

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Regeln der Sperre aus dem Plan.
const (
	MaxFails   = 5                // Fehlversuche je Adresse …
	Window     = 15 * time.Minute // … innerhalb dieser Zeit
	LockFor    = 15 * time.Minute // sperren die Adresse so lange
	GlobalMax  = 30               // so viele Fehlversuche von überall …
	GlobalGap  = 10 * time.Second // … lassen nur noch einen Versuch je Abstand zu
	maxEntries = 1000
)

// Sperre zählt Fehlversuche je Adresse und von überall. Sie liegt in File und
// übersteht so einen Neustart. Der Notausgang zählt bei der Bremse für alle
// nicht mit (global=false), seine Adresse wird aber wie jede andere gesperrt.
type Sperre struct {
	File string
	Now  func() time.Time

	mu            sync.Mutex
	ready         bool
	st            sperrStand
	pending       map[string]int // laufende Prüfungen je Adresse
	pendingGlobal int            // laufende Prüfungen, die zur Bremse für alle zählen
}

type sperrEintrag struct {
	Fails int       `json:"fehlversuche"`
	First time.Time `json:"erster"`
	Until time.Time `json:"gesperrt_bis,omitempty"`
}

type sperrStand struct {
	Addr    map[string]*sperrEintrag `json:"adressen"`
	Global  []time.Time              `json:"fehlversuche_ueberall"`
	Last    time.Time                `json:"letzter_versuch_gebremst,omitempty"`
	Trusted map[string]time.Time     `json:"vertraut,omitempty"`
}

func (s *Sperre) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Sperre) load() {
	if s.ready {
		return
	}
	s.ready = true
	s.st = sperrStand{Addr: map[string]*sperrEintrag{}}
	if s.File == "" {
		return
	}
	b, err := os.ReadFile(s.File)
	if err != nil {
		return
	}
	var st sperrStand
	if json.Unmarshal(b, &st) == nil && st.Addr != nil {
		s.st = st
	}
}

func (s *Sperre) save() {
	if s.File == "" {
		return
	}
	b, err := json.Marshal(s.st)
	if err != nil {
		return
	}
	tmp := s.File + ".neu"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.File)
	}
}

func (s *Sperre) prune(now time.Time) {
	for a, e := range s.st.Addr {
		if now.After(e.Until) && now.Sub(e.First) > Window {
			delete(s.st.Addr, a)
		}
	}
	for a, t := range s.st.Trusted {
		if now.Sub(t) >= TrustFor {
			delete(s.st.Trusted, a)
		}
	}
	g := s.st.Global[:0]
	for _, t := range s.st.Global {
		if now.Sub(t) <= Window {
			g = append(g, t)
		}
	}
	s.st.Global = g
}

// Begin fragt vor einer Passwortprüfung, ob addr prüfen darf; 0 heißt ja.
// Dann hält die Sperre den Versuch fest, bis End ihn abschließt: Versuche,
// die gleichzeitig laufen, zählen schon mit. Sonst kämen 40 Versuche, die
// im selben Augenblick eintreffen, alle an der Sperre nach fünf vorbei.
// global: der Versuch zählt zur Bremse für alle (nicht beim Notausgang).
// Adressen, von denen zuletzt jemand mit dem richtigen Passwort kam (bei
// einem Versuch mit global), nimmt die Bremse für alle aus; so sperren
// Fremde den Besitzer nicht aus.
func (s *Sperre) Begin(addr string, global bool) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	now := s.now()
	s.prune(now)
	if s.pending == nil {
		s.pending = map[string]int{}
	}
	fails := 0
	if e := s.st.Addr[addr]; e != nil {
		if now.Before(e.Until) {
			return e.Until.Sub(now)
		}
		fails = e.Fails
	}
	if fails+s.pending[addr] >= MaxFails {
		return busyWait
	}
	if global && !s.trusted(addr, now) && len(s.st.Global)+s.pendingGlobal >= GlobalMax {
		if d := s.st.Last.Add(GlobalGap).Sub(now); d > 0 {
			return d
		}
		s.st.Last = now
		s.save()
	}
	s.pending[addr]++
	if global {
		s.pendingGlobal++
	}
	return 0
}

// busyWait ist die Antwort, solange schon genug Versuche derselben Adresse
// laufen; sie dauern höchstens ein, zwei Sekunden.
const busyWait = 2 * time.Second

// End schließt einen Versuch ab, den Begin erlaubt hat. ok: Das Passwort
// stimmte. End sagt, ob addr jetzt gesperrt ist.
func (s *Sperre) End(addr string, global, ok bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	now := s.now()
	if s.pending[addr] > 0 {
		s.pending[addr]--
		if s.pending[addr] == 0 {
			delete(s.pending, addr)
		}
	}
	if global && s.pendingGlobal > 0 {
		s.pendingGlobal--
	}
	s.prune(now)
	if ok {
		if e := s.st.Addr[addr]; e != nil && !now.Before(e.Until) {
			delete(s.st.Addr, addr)
		}
		if global {
			s.trust(addr, now)
		}
		s.save()
		return false
	}
	e := s.st.Addr[addr]
	if e == nil || now.Sub(e.First) > Window {
		if len(s.st.Addr) >= maxEntries {
			// Sehr viele Adressen: die älteste vergessen, die Bremse für
			// alle greift ohnehin.
			var oldest string
			var t time.Time
			for a, x := range s.st.Addr {
				if t.IsZero() || x.First.Before(t) {
					oldest, t = a, x.First
				}
			}
			delete(s.st.Addr, oldest)
		}
		e = &sperrEintrag{First: now, Until: s.st.Addr[addr].until()}
		s.st.Addr[addr] = e
	}
	e.Fails++
	locked := false
	if e.Fails >= MaxFails {
		e.Until, e.Fails, e.First = now.Add(LockFor), 0, now
		locked = true
	}
	if global {
		s.st.Global = append(s.st.Global, now)
	}
	s.save()
	return locked
}

// Cancel gibt einen Versuch frei, der nicht geprüft wurde.
func (s *Sperre) Cancel(addr string, global bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending[addr] > 0 {
		s.pending[addr]--
		if s.pending[addr] == 0 {
			delete(s.pending, addr)
		}
	}
	if global && s.pendingGlobal > 0 {
		s.pendingGlobal--
	}
}

func (e *sperrEintrag) until() time.Time {
	if e == nil {
		return time.Time{}
	}
	return e.Until
}

// Vertraute Adressen: von dort kam zuletzt das richtige Passwort.
const (
	TrustFor   = 30 * 24 * time.Hour
	maxTrusted = 20
)

func (s *Sperre) trusted(addr string, now time.Time) bool {
	t, ok := s.st.Trusted[addr]
	return ok && now.Sub(t) < TrustFor
}

func (s *Sperre) trust(addr string, now time.Time) {
	if s.st.Trusted == nil {
		s.st.Trusted = map[string]time.Time{}
	}
	s.st.Trusted[addr] = now
	for len(s.st.Trusted) > maxTrusted {
		var oldest string
		var t time.Time
		for a, x := range s.st.Trusted {
			if t.IsZero() || x.Before(t) {
				oldest, t = a, x
			}
		}
		delete(s.st.Trusted, oldest)
	}
}

// Reset hebt alle Sperren auf (neues Passwort).
func (s *Sperre) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	s.st = sperrStand{Addr: map[string]*sperrEintrag{}}
	s.save()
}
