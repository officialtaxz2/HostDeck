package ueberwachung

import (
	"math"
	"testing"
	"time"
)

// Datenprüfung 18.11.2026: Spielerzahlen erfasst HostDeck nicht mehr; alte Reihen
// g/<id>/spieler fallen beim Laden weg, alles andere bleibt.
func TestAlteSpielerzahlenFallenWeg(t *testing.T) {
	l := line{T: 1,
		V: map[string]float64{"g/abc/spieler": 3, "s/cpu": 5, "c/web/cpu": 1, "g/abc/andere": 2},
		M: map[string]float64{"g/abc/spieler": 4, "s/cpu": 7},
		N: map[string]int{"g/abc/spieler": 2, "s/cpu": 2}}
	dropRetired(l)
	if _, ok := l.V["g/abc/spieler"]; ok || len(l.V) != 3 {
		t.Errorf("Werte: %v", l.V)
	}
	if _, ok := l.M["g/abc/spieler"]; ok || len(l.M) != 1 {
		t.Errorf("Spitzen: %v", l.M)
	}
	if _, ok := l.N["g/abc/spieler"]; ok || len(l.N) != 1 {
		t.Errorf("Anzahlen: %v", l.N)
	}
}

// Datenprüfung H2: Der Wert jetzt je Container kommt aus der letzten
// Messung, als Anteil am Server; nur frisch und nur mit beiden Werten.
func TestContainersNow(t *testing.T) {
	at := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	now := at.Add(5 * time.Second)
	s := &Service{Interval: 10 * time.Second, Now: func() time.Time { return now }}
	s.info = Info{Cores: 4, Containers: []ContainerInfo{{Name: "web", MemLimit: 512 << 20}}}
	s.last = Frame{At: at, Values: map[string]float64{
		"c/web/cpu": 40, "c/web/ram": 100 << 20, // 40 % eines Kerns = 10 % des Servers
		"c/nur-cpu/cpu": 5, "p/app/cpu": 40, "s/cpu": 12}}
	got := s.ContainersNow()
	if len(got) != 1 || got["web"].CPU != 10 || got["web"].RAM != 100<<20 || got["web"].MemLimit != 512<<20 {
		t.Errorf("jetzt: %+v", got)
	}
	now = at.Add(31 * time.Second)
	if got := s.ContainersNow(); got != nil {
		t.Errorf("alte Messung: %+v", got)
	}
}

// Datenprüfung 18.11.2026 (Laborprüfer I14): Nach einem Neustart mitten in
// einer Minute ist „Mittel“ dasselbe wie das Mittel der Punkte im Diagramm
// und wie das der Messungen. Vorher zählte die angeschnittene Minute am
// Neustart (wenige Sekunden Messung) eine volle Minute. Eine Spitze in
// dieser Minute bleibt in allen Zeiträumen, die sie enthalten.
func TestMittelNachNeustartWieDiagramm(t *testing.T) {
	dir := t.TempDir()
	stop := time.Date(2026, 11, 18, 12, 1, 25, 0, time.UTC)
	restart := time.Date(2026, 11, 18, 12, 1, 50, 0, time.UTC)
	now := time.Date(2026, 11, 18, 12, 6, 0, 0, time.UTC)
	spike := time.Date(2026, 11, 18, 12, 1, 5, 0, time.UTC)
	before := func(x time.Time) float64 {
		switch {
		case x.Equal(spike):
			return 90
		case !x.Before(stop.Add(-3 * time.Minute)):
			return 60
		}
		return 4
	}
	var samples []struct {
		t time.Time
		v float64
	}
	s := &Store{Dir: dir}
	for x := stop.Add(-3 * time.Hour); x.Before(stop); x = x.Add(10 * time.Second) {
		s.Add(Frame{At: x, Values: map[string]float64{KeyCPU: before(x)}})
		samples = append(samples, struct {
			t time.Time
			v float64
		}{x, before(x)})
	}
	if err := s.Close(stop); err != nil {
		t.Fatal(err)
	}
	s2 := &Store{Dir: dir}
	if err := s2.Load(restart); err != nil {
		t.Fatal(err)
	}
	for x := restart; x.Before(now); x = x.Add(10 * time.Second) {
		s2.Add(Frame{At: x, Values: map[string]float64{KeyCPU: 2}})
		samples = append(samples, struct {
			t time.Time
			v float64
		}{x, 2})
	}
	all := checkRanges(t, s2, now)
	wantPeak(t, all, KeyCPU, 90, spike, "15m", "1h", "6h", "1d", "7d", "30d")
	for _, r := range []string{"15m", "1h"} {
		h, err := s2.QueryPeaks(r, nil, 360, now, true)
		if err != nil {
			t.Fatal(err)
		}
		st := h.Stats[KeyCPU]
		// Wie der Laborprüfer und das Diagramm: jeder Punkt so lange, wie
		// er steht, höchstens sein Schritt.
		vs := h.Series[KeyCPU]
		var sum, n float64
		for i, at := range h.Times {
			x := float64(vs[i])
			if x != x {
				continue
			}
			step := int64(h.Step)
			if at <= h.CoarseTo {
				step = int64(h.CoarseStep)
			}
			next := h.End
			if i+1 < len(h.Times) {
				next = h.Times[i+1]
			}
			w := min(step, next-at)
			if at < st.Since {
				w -= st.Since - at
			}
			sum += x * float64(max(1, w))
			n += float64(max(1, w))
		}
		if pts := sum / n; math.Abs(pts-float64(st.Mean)) > 0.05 {
			t.Errorf("%s: Mittel %.3f, Punkte im Diagramm %.3f", r, st.Mean, pts)
		}
		// Messungen im Zeitraum, außer der angeschnittenen Minute am
		// Neustart, die das Diagramm auch nicht zeigt.
		var ms, mn float64
		for _, m := range samples {
			if m.t.Unix() >= st.Since && (m.t.Before(time.Date(2026, 11, 18, 12, 1, 0, 0, time.UTC)) || !m.t.Before(restart)) {
				ms += m.v
				mn++
			}
		}
		if want := ms / mn; math.Abs(want-float64(st.Mean)) > 0.05 {
			t.Errorf("%s: Mittel %.3f, Mittel der Messungen %.3f", r, st.Mean, want)
		}
	}
}
