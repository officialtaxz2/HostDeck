package ueberwachung

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Standardtakt: alle 10 Sekunden messen, alle 10 Minuten schreiben.
const (
	DefaultInterval = 10 * time.Second
	FlushEvery      = 10 * time.Minute
)

// Service misst im Takt, hebt den Verlauf auf und beantwortet Fragen des
// Leitstands.
type Service struct {
	Collector *Collector
	Store     *Store
	Interval  time.Duration // Standard 10 s
	Now       func() time.Time
	Log       func(format string, args ...any)
	// SelfProc ist /proc/self (Standard), für die Kosten von HostDeck.
	SelfProc string
	// Rules prüft nach jeder Messung die Warnungen (4c); optional.
	Rules *Rules
	// Live misst jede Sekunde, solange jemand zuschaut; optional.
	Live *Live
	// Games erkennt Spielserver (nur zur Einordnung, ohne eigene Abfrage);
	// optional.
	Games Games

	mu        sync.Mutex
	since     time.Time
	last      Frame
	info      Info
	cost      Cost
	selfCPU   counter
	selfStart counter // erster Wert, für den Schnitt seit dem Start
}

// Cost ist, was der HostDeck-Dienst selbst braucht (alles, nicht nur die
// Messung).
//
// CPU und Avg sind, wie jeder Prozessorwert nach außen, ein Anteil am ganzen
// Server (alle Kerne = 100 %, Entscheidung E1); gemessen und gespeichert
// wird je Container in Prozent eines Kerns, umgerechnet nur hier.
type Cost struct {
	CPU float64 `json:"cpu"`         // % des Servers im letzten Takt
	Avg float64 `json:"cpu_schnitt"` // % des Servers im Schnitt seit dem Start der Messung
	RAM int64   `json:"ram"`         // Byte (RSS)
}

// PerCore sagt, ob eine Reihe in Prozent eines Kerns gespeichert ist
// (Prozessor je Container und je Projekt). Der Server selbst (s/cpu) ist
// schon ein Anteil am Ganzen.
func PerCore(key string) bool {
	return strings.HasSuffix(key, "/"+ValCPU) && (strings.HasPrefix(key, "c/") || strings.HasPrefix(key, "p/"))
}

// Cores sind die Kerne des Servers (für den Anteil am Server).
func (s *Service) Cores() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cores()
}

// Now ist ein Wert je Container aus der letzten Messung: Prozessor als Anteil
// am ganzen Server, Speicher in Byte, eingestellte Grenze (0: keine).
type Now struct {
	CPU      float64
	RAM      int64
	MemLimit int64
}

// ContainersNow sind Prozessor und Speicher je laufendem Container aus der
// letzten Messung, nur wenn sie frisch ist (höchstens drei Takte alt) und
// beide Werte hat. Der Betrieb nimmt sie statt docker stats, damit überall
// dieselbe Zahl steht (Datenprüfung H2). Misst der Server je Container
// nichts (cgroup v1), ist die Liste leer.
func (s *Service) ContainersNow() map[string]Now {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last.At.IsZero() || s.now().Sub(s.last.At) > 3*s.interval() {
		return nil
	}
	cores := s.cores()
	limits := map[string]int64{}
	for _, c := range s.info.Containers {
		limits[c.Name] = c.MemLimit
	}
	out := map[string]Now{}
	for k, v := range s.last.Values {
		name, ok := strings.CutPrefix(k, "c/")
		if !ok {
			continue
		}
		name, ok = strings.CutSuffix(name, "/"+ValCPU)
		if !ok {
			continue
		}
		ram, ok := s.last.Values[ContainerKey(name, ValRAM)]
		if !ok {
			continue
		}
		out[name] = Now{CPU: Share(v, cores), RAM: int64(ram), MemLimit: limits[name]}
	}
	return out
}

// cores: Kerne laut letzter Messung, vor der ersten Messung die des
// Prozesses. Aufrufen mit s.mu.
func (s *Service) cores() int {
	if s.info.Cores > 0 {
		return s.info.Cores
	}
	return runtime.NumCPU()
}

// Share rechnet Prozent eines Kerns in den Anteil am ganzen Server um.
func Share(v float64, cores int) float64 {
	if cores <= 1 {
		return v
	}
	return v / float64(cores)
}

// shareHistory rechnet alle Reihen in Prozent eines Kerns in den Anteil am
// Server um: Punkte, Spitzen und Kennzahlen.
func shareHistory(h *History, cores int) {
	if cores <= 1 {
		return
	}
	f := Value(1 / float64(cores))
	scale := func(m map[string][]Value) {
		for k, vs := range m {
			if !PerCore(k) {
				continue
			}
			for i := range vs {
				vs[i] *= f // NaN bleibt NaN (keine Messung)
			}
		}
	}
	scale(h.Series)
	scale(h.Peaks)
	for k, st := range h.Stats {
		if PerCore(k) {
			st.Last *= f
			st.Mean *= f
			st.Peak *= f
			h.Stats[k] = st
		}
	}
}

// Snapshot ist der Stand für GET /api/ueberwachung.
type Snapshot struct {
	Running  bool               `json:"laeuft"`
	Since    *time.Time         `json:"seit,omitempty"`
	At       *time.Time         `json:"stand_von,omitempty"`
	Interval int                `json:"takt"` // Sekunden
	Values   map[string]float64 `json:"werte"`
	Info     Info               `json:"info"`
	Cost     Cost               `json:"kosten"`
	Disk     int64              `json:"verlauf_platte"` // Byte
	// HistSince: ältester Wert im Verlauf; Since ist nur der Start des
	// Dienstes (V18).
	HistSince *time.Time `json:"verlauf_seit,omitempty"`
	Series    int        `json:"reihen"`
	Keep      []string   `json:"zeitraeume"`
	Alerts    *RulesView `json:"warnungen,omitempty"`
	// Games: erkannte Spielserver (nur mit Games), sonst fehlt das Feld.
	Games any `json:"spielserver,omitempty"`
}

// Games erkennt Spielserver (Paket spiele) am Image und an den Dateien;
// Servers ist die Liste für den Leitstand. HostDeck fragt Spiele nicht ab und
// erfasst keine Spielerzahlen: Ein Spielserver ist ein Container wie jeder
// andere.
type Games interface {
	Run(ctx context.Context)
	Servers() any
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) interval() time.Duration {
	if s.Interval > 0 {
		return s.Interval
	}
	return DefaultInterval
}

func (s *Service) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

// Run misst, bis ctx endet, und schreibt zum Schluss den Rest.
func (s *Service) Run(ctx context.Context) {
	start := s.now()
	if err := s.Store.Load(start); err != nil {
		s.logf("Überwachung: Verlauf teilweise nicht lesbar: %v", err)
	}
	s.mu.Lock()
	s.since = start
	s.mu.Unlock()
	if s.Live != nil {
		s.Live.start(ctx)
	}
	if s.Games != nil {
		go s.Games.Run(ctx)
	}
	tick := time.NewTicker(s.interval())
	defer tick.Stop()
	lastFlush := start
	s.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			if err := s.Store.Close(s.now()); err != nil {
				s.logf("Überwachung: Verlauf nicht geschrieben: %v", err)
			}
			return
		case <-tick.C:
			s.Tick(ctx)
			if now := s.now(); now.Sub(lastFlush) >= FlushEvery {
				lastFlush = now
				if err := s.Store.Flush(now); err != nil {
					s.logf("Überwachung: Verlauf nicht geschrieben: %v", err)
				}
			}
		}
	}
}

// Tick misst einmal und nimmt die Messung auf.
func (s *Service) Tick(ctx context.Context) {
	mctx, cancel := context.WithTimeout(ctx, s.interval())
	defer cancel()
	now := s.now()
	f, info := s.Collector.Collect(mctx, now)
	s.Store.Add(f)
	s.mu.Lock()
	if s.since.IsZero() {
		s.since = now
	}
	s.last, s.info = f, info
	s.cost = s.selfCost(now)
	s.mu.Unlock()
	if s.Rules != nil {
		if err := s.Rules.Check(ctx, f, info); err != nil {
			s.logf("Überwachung: %v", err)
		}
	}
}

// Snapshot ist der letzte gemessene Stand.
func (s *Service) Snapshot() Snapshot {
	var alerts *RulesView
	if s.Rules != nil {
		v := s.Rules.View(50)
		alerts = &v
	}
	disk, series := s.Store.Size()
	first := s.Store.First()
	var games any
	if s.Games != nil {
		games = s.Games.Servers()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{Interval: int(s.interval() / time.Second), Values: map[string]float64{}, Info: s.info, Cost: s.cost, Disk: disk, Series: series,
		Keep: []string{"15m", "1h", "6h", "1d", "7d", "30d"}, Alerts: alerts, Games: games}
	if snap.Info.Disks == nil {
		snap.Info.Disks = []DiskInfo{}
	}
	if snap.Info.Containers == nil {
		snap.Info.Containers = []ContainerInfo{}
	}
	if !s.since.IsZero() {
		t := s.since
		snap.Since, snap.Running = &t, true
	}
	if !first.IsZero() {
		snap.HistSince = &first
	}
	if !s.last.At.IsZero() {
		t := s.last.At
		snap.At = &t
		for k, v := range s.last.Values {
			if PerCore(k) {
				v = Share(v, s.cores())
			}
			snap.Values[k] = v
		}
	}
	snap.Cost.CPU, snap.Cost.Avg = Share(snap.Cost.CPU, s.cores()), Share(snap.Cost.Avg, s.cores())
	return snap
}

// History ist der Verlauf für den Leitstand.
// Prozessorwerte je Container und Projekt kommen als Anteil am Server.
func (s *Service) History(rng string, patterns []string, points int, peaks bool) (History, error) {
	h, err := s.Store.QueryPeaks(rng, patterns, points, s.now(), peaks)
	if err == nil {
		s.mu.Lock()
		cores := s.cores()
		s.mu.Unlock()
		shareHistory(&h, cores)
	}
	return h, err
}

// selfCost liest /proc/self/stat (utime, stime in Ticks zu 1/100 s) und
// VmRSS. Aufrufen mit s.mu.
func (s *Service) selfCost(now time.Time) Cost {
	dir := s.SelfProc
	if dir == "" {
		dir = "/proc/self"
	}
	var c Cost
	if b, err := os.ReadFile(filepath.Join(dir, "stat")); err == nil {
		// Der Name in Klammern kann Leerzeichen enthalten; gezählt wird
		// ab der schließenden Klammer.
		if i := strings.LastIndexByte(string(b), ')'); i > 0 {
			fs := strings.Fields(string(b)[i+1:])
			if len(fs) > 12 {
				u, _ := strconv.ParseFloat(fs[11], 64)
				st, _ := strconv.ParseFloat(fs[12], 64)
				ticks := u + st
				if p := s.selfCPU; !p.at.IsZero() && ticks >= p.v {
					if dt := now.Sub(p.at).Seconds(); dt > 0 {
						c.CPU = (ticks - p.v) / dt // Ticks/s bei 100 Hz = %
					}
				}
				s.selfCPU = counter{ticks, now}
				if p := s.selfStart; p.at.IsZero() {
					s.selfStart = s.selfCPU
				} else if dt := now.Sub(p.at).Seconds(); dt > 0 && ticks >= p.v {
					c.Avg = (ticks - p.v) / dt
				}
			}
		}
	}
	if kv := readKV(filepath.Join(dir, "status")); kv["VmRSS"] > 0 {
		c.RAM = kv["VmRSS"] * 1024
	}
	return c
}
