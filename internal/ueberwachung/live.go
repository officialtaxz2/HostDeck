package ueberwachung

import (
	"context"
	"math"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// Live misst jede Sekunde, solange jemand die Live-Ansicht offen hat
// („jede Sekunde, nur beim Zuschauen“). Es ist ein eigener Collector mit eigenen Zählerständen; der
// 10-Sekunden-Takt, der Verlauf, die Warnungen und die Mitteilungen bleiben
// davon unberührt. Die Messungen leben nur im Speicher (5 Minuten).
type Live struct {
	Collector *Collector
	Interval  time.Duration // Standard 1 s
	Keep      int           // Plätze im Ring, Standard 300 (5 Minuten)
	// Idle: so lange misst Live weiter, nachdem zuletzt jemand gefragt
	// hat. Standard 30 s.
	Idle time.Duration
	Now  func() time.Time

	mu      sync.Mutex
	base    context.Context
	frames  []LiveFrame
	asked   time.Time
	running bool
}

// Voreinstellungen für Live.
const (
	LiveInterval = time.Second
	LiveKeep     = 300
	LiveIdle     = 30 * time.Second
	// LiveListEvery: Die Liste der Container fragt der schnelle Collector
	// nur alle 5 Sekunden bei Docker; dazwischen liest er die bekannten
	// cgroups.
	LiveListEvery = 5 * time.Second
)

// LiveFrame ist eine Messung für die Live-Ansicht.
type LiveFrame struct {
	T int64              `json:"t"` // Unix-Millisekunden
	V map[string]float64 `json:"v"`
}

// LiveView ist die Antwort auf GET /api/ueberwachung/live.
type LiveView struct {
	Interval int   `json:"takt"`  // Sekunden zwischen zwei schnellen Messungen
	At       int64 `json:"stand"` // Unix-Millisekunden der jüngsten Messung, 0: noch keine
	// FineFrom: ab hier (Unix-ms) kommen die Messungen jede Sekunde;
	// davor stammen sie aus dem 10-Sekunden-Verlauf (damit das Diagramm
	// beim Öffnen sofort gefüllt ist). 0: noch keine schnelle Messung.
	FineFrom int64       `json:"fein_ab"`
	Frames   []LiveFrame `json:"messungen"`
	Cores    int         `json:"kerne"`
	RAMTotal int64       `json:"ram_gesamt"`
	// Running: Der schnelle Takt läuft (jemand schaut zu).
	Running bool `json:"laeuft"`
}

func (l *Live) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Live) interval() time.Duration {
	if l.Interval > 0 {
		return l.Interval
	}
	return LiveInterval
}

func (l *Live) keep() int {
	if l.Keep > 0 {
		return l.Keep
	}
	return LiveKeep
}

func (l *Live) idle() time.Duration {
	if l.Idle > 0 {
		return l.Idle
	}
	return LiveIdle
}

// start merkt sich den Kontext des Dienstes; endet er, hört Live auf.
func (l *Live) start(ctx context.Context) {
	l.mu.Lock()
	l.base = ctx
	l.mu.Unlock()
}

// Watch sagt, dass gerade jemand zuschaut, und startet den schnellen Takt,
// wenn er nicht läuft.
func (l *Live) Watch() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.asked = l.now()
	if l.running {
		return
	}
	ctx := l.base
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return
	}
	l.running = true
	go l.loop(ctx)
}

// Active sagt, ob gerade jemand zuschaut.
func (l *Live) Active() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running
}

func (l *Live) loop(ctx context.Context) {
	tick := time.NewTicker(l.interval())
	defer tick.Stop()
	for {
		l.mu.Lock()
		idle := l.now().Sub(l.asked) > l.idle()
		if idle {
			// Wer nicht mehr zuschaut, braucht die alten Werte nicht; beim
			// nächsten Öffnen füllt der Verlauf das Diagramm.
			l.running = false
			l.frames = nil
			l.mu.Unlock()
			// Ohne die alten Zählerstände ist die erste Messung beim nächsten
			// Öffnen kein Mittel über die ganze Pause.
			l.Collector.Reset()
			return
		}
		l.mu.Unlock()
		l.measure(ctx)
		select {
		case <-ctx.Done():
			l.mu.Lock()
			l.running = false
			l.mu.Unlock()
			return
		case <-tick.C:
		}
	}
}

func (l *Live) measure(ctx context.Context) {
	mctx, cancel := context.WithTimeout(ctx, l.interval())
	defer cancel()
	now := l.now()
	f, _ := l.Collector.Collect(mctx, now)
	// Laufwerke misst nur der 10-Sekunden-Takt; die Last über eine Minute
	// ändert sich nicht je Sekunde.
	delete(f.Values, KeyLoad)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.frames = append(l.frames, LiveFrame{T: now.UnixMilli(), V: f.Values})
	if n := len(l.frames) - l.keep(); n > 0 {
		l.frames = append([]LiveFrame(nil), l.frames[n:]...)
	}
}

// matcher wählt Reihen wie Query: „*“ wie path.Match für den ganzen
// Schlüssel, sonst als Anfang; leer: alle.
func matcher(patterns []string) func(string) bool {
	return func(k string) bool {
		if len(patterns) == 0 {
			return true
		}
		for _, p := range patterns {
			if strings.Contains(p, "*") {
				if ok, _ := path.Match(p, k); ok {
					return true
				}
			} else if strings.HasPrefix(k, p) {
				return true
			}
		}
		return false
	}
}

// LiveView liefert die Messungen nach since (Unix-ms; 0: die letzten
// 5 Minuten) und startet den schnellen Takt. Prozessor je Container und
// Projekt als Anteil am ganzen Server, wie überall.
func (s *Service) LiveView(since int64, patterns []string) LiveView {
	match := matcher(patterns)
	s.mu.Lock()
	cores, ram := s.cores(), s.info.RAMTotal
	s.mu.Unlock()
	v := LiveView{Interval: int(LiveInterval / time.Second), Frames: []LiveFrame{}, Cores: cores, RAMTotal: ram}
	conv := func(in map[string]float64) map[string]float64 {
		out := make(map[string]float64, len(in))
		for k, x := range in {
			if !match(k) || math.IsNaN(x) || math.IsInf(x, 0) {
				continue
			}
			if PerCore(k) {
				x = Share(x, cores)
			}
			out[k] = x
		}
		return out
	}
	now := s.now()
	keep := LiveKeep
	if s.Live != nil {
		s.Live.Watch()
		v.Interval = int(s.Live.interval() / time.Second)
		keep = s.Live.keep()
		s.Live.mu.Lock()
		v.Running = s.Live.running
		fr := s.Live.frames
		if len(fr) > 0 {
			v.FineFrom = fr[0].T
		}
		for _, f := range fr {
			if f.T > since {
				v.Frames = append(v.Frames, LiveFrame{T: f.T, V: conv(f.V)})
			}
		}
		s.Live.mu.Unlock()
	}
	// Was vor der ersten schnellen Messung liegt, aus dem Verlauf: So ist
	// das Diagramm beim Öffnen gleich gefüllt.
	window := now.Add(-time.Duration(keep) * time.Second * time.Duration(max(1, v.Interval))).UnixMilli()
	end := v.FineFrom
	if end == 0 {
		end = now.UnixMilli() + 1
	}
	if since < end {
		var old []LiveFrame
		for _, f := range s.Store.recent(max(since+1, window), end) {
			old = append(old, LiveFrame{T: f.T, V: conv(f.V)})
		}
		v.Frames = append(old, v.Frames...)
	}
	if n := len(v.Frames); n > 0 {
		v.At = v.Frames[n-1].T
	}
	return v
}

// recent liefert die Abschnitte der feinsten Stufe mit Beginn in
// [from, to) (Unix-ms) als Messungen, älteste zuerst.
func (s *Store) recent(from, to int64) []LiveFrame {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	tr := s.tiers[0]
	var out []LiveFrame
	for _, c := range tr.cells(math.MinInt64, math.MaxInt64, false) {
		ms := c.t * 1000
		if ms < from || ms >= to {
			continue
		}
		f := LiveFrame{T: ms, V: map[string]float64{}}
		for k, sv := range tr.series {
			if x := float64(sv[c.slot]); !math.IsNaN(x) {
				f.V[k] = x
			}
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out
}
