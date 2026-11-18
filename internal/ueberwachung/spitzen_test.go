package ueberwachung

import (
	"math"
	"strings"
	"testing"
	"time"
)

// Die Spitze eines längeren Zeitraums darf nie unter der
// eines enthaltenen kürzeren liegen, und „Zuletzt“ ist überall derselbe
// letzte Messwert. Nachgestellte Fälle A bis C.

var rangesShortToLong = []string{"15m", "1h", "6h", "1d", "7d", "30d"}

// feedSteal misst alle 10 s Steal nach val und web nach webVal.
func feedSteal(s *Store, from, to time.Time, val func(time.Time) float64) {
	for t := from; t.Before(to); t = t.Add(10 * time.Second) {
		v := val(t)
		s.Add(Frame{At: t, Values: map[string]float64{KeySteal: v, KeyCPU: 3 * v, ContainerKey("web", ValRAM): 1e9 + v*1e8}})
	}
}

func spikeAt(at time.Time, d time.Duration) func(time.Time) float64 {
	return func(x time.Time) float64 {
		if !x.Before(at) && x.Before(at.Add(d)) {
			return 8
		}
		return 0.5
	}
}

// nearly: gleich bis auf die Rundung der Dateien (5 Stellen) und float32.
func nearly(a, b float64) bool { return math.Abs(a-b) <= 1e-4*math.Max(1, math.Abs(b)) }

// checkRanges prüft alle Reihen über alle Zeiträume und liefert die
// Kennzahlen je Zeitraum.
func checkRanges(t *testing.T, s *Store, now time.Time) map[string]map[string]Stat {
	t.Helper()
	all := map[string]map[string]Stat{}
	for _, r := range rangesShortToLong {
		h, err := s.QueryPeaks(r, nil, 360, now, true)
		if err != nil {
			t.Fatal(err)
		}
		all[r] = h.Stats
		// Das Diagramm zeigt keine Spitze, die die Kennzahl nicht kennt.
		for k, pk := range h.Peaks {
			st, ok := h.Stats[k]
			if !ok {
				t.Errorf("%s %s: Reihe ohne Kennzahlen", r, k)
				continue
			}
			for i, p := range pk {
				if !math.IsNaN(float64(p)) && float64(p) > float64(st.Peak)+1e-4*math.Max(1, float64(st.Peak)) && h.Times[i] >= now.Add(-Ranges[r].Span).Unix() {
					t.Errorf("%s %s: Diagramm-Spitze %v um %d über Kennzahl %v", r, k, p, h.Times[i], st.Peak)
				}
			}
		}
	}
	for i := 1; i < len(rangesShortToLong); i++ {
		short, long := rangesShortToLong[i-1], rangesShortToLong[i]
		for k, a := range all[short] {
			b, ok := all[long][k]
			if !ok {
				t.Errorf("%s fehlt in %s, steht aber in %s", k, long, short)
				continue
			}
			if float64(b.Peak) < float64(a.Peak) && !nearly(float64(b.Peak), float64(a.Peak)) {
				t.Errorf("%s: Spitze %s %v kleiner als %s %v", k, long, b.Peak, short, a.Peak)
			}
			if a.LastAt != b.LastAt || !nearly(float64(a.Last), float64(b.Last)) {
				t.Errorf("%s: Zuletzt %s %v (%d), %s %v (%d)", k, short, a.Last, a.LastAt, long, b.Last, b.LastAt)
			}
		}
	}
	return all
}

func wantPeak(t *testing.T, all map[string]map[string]Stat, k string, want float64, at time.Time, ranges ...string) {
	t.Helper()
	for _, r := range ranges {
		st := all[r][k]
		if !nearly(float64(st.Peak), want) {
			t.Errorf("%s %s: Spitze %v, erwartet %v", r, k, st.Peak, want)
			continue
		}
		if u := at.Unix(); u < st.PeakAt || u >= st.PeakAt+int64(st.PeakStep) {
			t.Errorf("%s %s: Spitze um %d (+%d s), erwartet %d", r, k, st.PeakAt, st.PeakStep, u)
		}
	}
}

// A: 60 s mit 8 % vor zwei Stunden (beobachtet: 6 Std 5,6 %, 30 Tage 1,5 %).
func TestSpitzeFallA(t *testing.T) {
	now := time.Date(2026, 11, 8, 17, 3, 0, 0, time.UTC)
	spike := now.Add(-2 * time.Hour)
	s := &Store{}
	feedSteal(s, now.Add(-5*24*time.Hour), now, spikeAt(spike, 60*time.Second))
	all := checkRanges(t, s, now)
	wantPeak(t, all, KeySteal, 8, spike, "6h", "1d", "7d", "30d")
	if p := float64(all["15m"][KeySteal].Peak); !nearly(p, 0.5) {
		t.Errorf("15m: Spitze %v", p)
	}
	// Die Uhrzeit ist überall dieselbe Minute (aus der Minutenstufe).
	if a, b := all["6h"][KeySteal], all["30d"][KeySteal]; a.PeakAt != b.PeakAt || b.PeakStep != 60 {
		t.Errorf("Spitze um: 6h %d, 30d %d (Schritt %d)", a.PeakAt, b.PeakAt, b.PeakStep)
	}
	// Mittel nach Zeit: 60 s von 5 Tagen mit 8 statt 0,5.
	want := 0.5 + 7.5*60/(5*24*3600.0)
	if m := float64(all["30d"][KeySteal].Mean); math.Abs(m-want) > 1e-3 {
		t.Errorf("Mittel 30d %v, erwartet %v", m, want)
	}
}

// B: 20 s mit 8 % vor 4 Minuten, im laufenden Abschnitt.
func TestSpitzeFallB(t *testing.T) {
	now := time.Date(2026, 11, 8, 17, 9, 0, 0, time.UTC)
	spike := now.Add(-4 * time.Minute)
	s := &Store{}
	feedSteal(s, now.Add(-3*24*time.Hour), now, spikeAt(spike, 20*time.Second))
	all := checkRanges(t, s, now)
	wantPeak(t, all, KeySteal, 8, spike, rangesShortToLong...)
	// Auch im Diagramm von 30 Tagen: der laufende Abschnitt als letzter Punkt.
	h, _ := s.QueryPeaks("30d", []string{"s/steal"}, 360, now, true)
	if p := h.Peaks[KeySteal]; float64(p[len(p)-1]) != 8 {
		t.Errorf("30 Tage: letzter Punkt %v", p[len(p)-1])
	}
}

// C: Spitze 17:01:30, Update um 17:05, weiter bis 17:30. Mit Close gehen
// die halbfertigen Abschnitte nicht verloren; auch das Diagramm hat sie.
func TestSpitzeFallCNeustart(t *testing.T) {
	for _, clean := range []bool{true, false} {
		name := map[bool]string{true: "Beenden", false: "Absturz"}[clean]
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			stop := time.Date(2026, 11, 8, 17, 5, 0, 0, time.UTC)
			spike := time.Date(2026, 11, 8, 17, 1, 30, 0, time.UTC)
			val := spikeAt(spike, 20*time.Second)
			s := &Store{Dir: dir}
			feedSteal(s, stop.Add(-24*time.Hour), stop, val)
			var err error
			if clean {
				err = s.Close(stop)
			} else {
				err = s.Flush(stop) // nur was fertig ist, wie bei einem Absturz kurz nach dem Schreiben
			}
			if err != nil {
				t.Fatal(err)
			}
			s2 := &Store{Dir: dir}
			if err := s2.Load(stop.Add(20 * time.Second)); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 11, 8, 17, 30, 0, 0, time.UTC)
			feedSteal(s2, stop.Add(20*time.Second), now, val)
			all := checkRanges(t, s2, now)
			wantPeak(t, all, KeySteal, 8, spike, "1h", "6h", "1d", "7d", "30d")
			h, _ := s2.QueryPeaks("30d", []string{"s/steal"}, 360, now, true)
			top := 0.0
			for _, p := range h.Peaks[KeySteal] {
				top = math.Max(top, float64(p))
			}
			if clean && top != 8 {
				t.Errorf("Diagramm 30 Tage nach Update: Spitze %v", top)
			}
			// Der Abschnitt 17:00–17:10 hat das Mittel aller seiner Messungen.
			if clean {
				for i, at := range h.Times {
					if at == time.Date(2026, 11, 8, 17, 0, 0, 0, time.UTC).Unix() {
						want := (0.5*58 + 8*2) / 60 // 17:05:00 und 17:05:10 fehlen: 58 Messungen
						if got := float64(h.Series[KeySteal][i]); math.Abs(got-want) > 0.05 {
							t.Errorf("Mittel 17:00: %v, erwartet etwa %v", got, want)
						}
					}
				}
			}
		})
	}
}

// Ein zweites Beenden im selben Abschnitt behält, was vor dem ersten war.
func TestSpitzeZweiNeustarts(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 11, 8, 17, 0, 0, 0, time.UTC)
	s := &Store{Dir: dir}
	feedSteal(s, t0.Add(-time.Hour), t0.Add(2*time.Minute), spikeAt(t0.Add(30*time.Second), 10*time.Second))
	if err := s.Close(t0.Add(2 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s = &Store{Dir: dir}
		at := t0.Add(time.Duration(2+3*i) * time.Minute)
		if err := s.Load(at); err != nil {
			t.Fatal(err)
		}
		feedSteal(s, at, at.Add(2*time.Minute), spikeAt(time.Time{}, 0))
		if err := s.Close(at.Add(2 * time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	s = &Store{Dir: dir}
	now := t0.Add(20 * time.Minute)
	if err := s.Load(now); err != nil {
		t.Fatal(err)
	}
	h, _ := s.QueryPeaks("7d", []string{"s/steal"}, 0, now, true)
	if st := h.Stats[KeySteal]; st.Peak != 8 {
		t.Errorf("Spitze nach zwei Neustarts: %v", st.Peak)
	}
	top := 0.0
	for _, p := range h.Peaks[KeySteal] {
		top = math.Max(top, float64(p))
	}
	if top != 8 {
		t.Errorf("Diagramm nach zwei Neustarts: %v", top)
	}
}

// Nach einem Neustart vor 20 Minuten: Die 10-Sekunden-Stufe beginnt leer,
// „1 Std“ nimmt davor die Minuten. Auch dann bleibt alles stimmig.
func TestSpitzeNeustartInDerStunde(t *testing.T) {
	dir := t.TempDir()
	stop := time.Date(2026, 11, 8, 16, 47, 20, 0, time.UTC)
	spike := stop.Add(-15 * time.Minute)
	val := spikeAt(spike, 10*time.Second)
	s := &Store{Dir: dir}
	feedSteal(s, stop.Add(-3*time.Hour), stop, val)
	if err := s.Close(stop); err != nil {
		t.Fatal(err)
	}
	s2 := &Store{Dir: dir}
	if err := s2.Load(stop); err != nil {
		t.Fatal(err)
	}
	now := stop.Add(20 * time.Minute)
	feedSteal(s2, stop.Add(10*time.Second), now, func(time.Time) float64 { return 0.7 })
	all := checkRanges(t, s2, now)
	wantPeak(t, all, KeySteal, 8, spike, "1h", "6h", "1d", "7d", "30d")
	if st := all["15m"][KeySteal]; float64(st.Last) != float64(float32(0.7)) || st.LastAt != now.Add(-10*time.Second).Unix() {
		t.Errorf("Zuletzt: %v um %d", st.Last, st.LastAt)
	}
}

// Kennzahlen gibt es nur auf Wunsch und nur für die gewählten Reihen.
func TestKennzahlenFilter(t *testing.T) {
	now := time.Date(2026, 11, 8, 17, 0, 0, 0, time.UTC)
	s := &Store{}
	feedSteal(s, now.Add(-time.Hour), now, func(time.Time) float64 { return 1 })
	h, _ := s.QueryPeaks("1d", []string{"s/"}, 0, now, true)
	for k := range h.Stats {
		if !strings.HasPrefix(k, "s/") {
			t.Errorf("Kennzahl für %s", k)
		}
	}
	if st := h.Stats[KeySteal]; st.Since != now.Add(-time.Hour).Unix() {
		t.Errorf("seit %d", st.Since)
	}
	if h, _ := s.Query("1d", nil, 0, now); h.Stats != nil {
		t.Error("Kennzahlen ohne Wunsch")
	}
}
