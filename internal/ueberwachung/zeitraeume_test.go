package ueberwachung

import (
	"math"
	"testing"
	"time"
)

// 31 Tage künstliche Messwerte im
// 10-Sekunden-Takt mit drei Lücken. A: 2 Stunden vor 3 Tagen (HostDeck aus),
// B: 20 Minuten vor 5 Stunden, C: 3 Minuten vor 40 Minuten (HostDeck lief, maß
// aber nicht; trifft die alten Plätze im 10-Sekunden-Ring, D1).

func probeValue(t int64) float64 {
	v := 50 + 40*math.Sin(2*math.Pi*float64(t)/7200)
	if t%86400 == 43210 { // ein 10-s-Ausreißer täglich
		v = 100
	}
	return v
}

type probeRun struct {
	s   *Store
	now int64
	ts  []int64
	vs  []float64
}

func newProbe(t *testing.T, gaps [][2]int64, extra map[int64]float64) probeRun {
	t.Helper()
	now := time.Date(2026, 11, 16, 22, 3, 17, 0, time.UTC).Unix()
	p := probeRun{s: &Store{}, now: now}
	in := func(x int64) bool {
		for _, g := range gaps {
			if x >= now+g[0] && x < now+g[1] {
				return true
			}
		}
		return false
	}
	start := now - 31*86400
	for x := start - start%10; x <= now; x += 10 {
		if in(x) {
			continue
		}
		v := probeValue(x)
		if e, ok := extra[x-now]; ok {
			v = e
		}
		p.s.Add(Frame{At: time.Unix(x, 0), Values: map[string]float64{KeyCPU: v}})
		p.ts = append(p.ts, x)
		p.vs = append(p.vs, v)
	}
	return p
}

// truth: Mittel nach Zeit (jede Messung 10 s, die letzte bis jetzt) und
// höchster Einzelwert in [a, b].
func (p probeRun) truth(a, b int64) (mean, peak float64, last int64) {
	var sum, w float64
	peak = math.Inf(-1)
	for i, x := range p.ts {
		if x+10 <= a || x > b {
			continue
		}
		d := math.Min(10, float64(b-x))
		if x < a {
			d -= float64(a - x)
		}
		d = math.Max(d, 1)
		sum += p.vs[i] * d
		w += d
		peak = math.Max(peak, p.vs[i])
		last = x
	}
	return sum / w, peak, last
}

var probeGaps = [][2]int64{
	{-3 * 86400, -3*86400 + 2*3600}, // A
	{-5 * 3600, -5*3600 + 20*60},    // B
	{-40 * 60, -40*60 + 3*60},       // C
}

func TestZeitraeumeNachrechnung(t *testing.T) {
	p := newProbe(t, probeGaps, nil)
	for _, rng := range rangesShortToLong {
		span := int64(Ranges[rng].Span / time.Second)
		for _, pts := range []int{360, 144} {
			h, err := p.s.QueryPeaks(rng, []string{KeyCPU}, pts, time.Unix(p.now, 0), true)
			if err != nil {
				t.Fatal(err)
			}
			st := h.Stats[KeyCPU]
			mean, peak, last := p.truth(p.now-span, p.now)
			if d := math.Abs(float64(st.Mean) - mean); d > 0.1 {
				t.Errorf("%s/%d: Mittel %.3f, richtig %.3f", rng, pts, st.Mean, mean)
			}
			if math.Abs(float64(st.Peak)-peak) > 1e-3 {
				t.Errorf("%s/%d: Spitze %.3f, richtig %.3f", rng, pts, st.Peak, peak)
			}
			if st.LastAt > last || st.LastAt+int64(Tiers[0].Step/time.Second) <= last {
				t.Errorf("%s/%d: zuletzt um %d, jüngste Messung %d", rng, pts, st.LastAt, last)
			}
			if len(h.Times) > pts+1 {
				t.Errorf("%s/%d: %d Punkte", rng, pts, len(h.Times))
			}
			// Jeder Punkt liegt auf einem Vielfachen des Schritts (grobe Punkte
			// nach einem Neustart auf ihrem).
			for _, x := range h.Times {
				st := int64(h.Step)
				if h.CoarseTo > 0 && x <= h.CoarseTo {
					st = int64(h.CoarseStep)
				}
				if x%st != 0 {
					t.Errorf("%s/%d: Punkt %d nicht auf %d s", rng, pts, x, st)
					break
				}
			}
			// Jede Lücke ab einem Schritt (plus je einem angeschnittenen
			// Abschnitt der Stufe an beiden Enden) ist sichtbar: als null oder
			// als Sprung in den Zeiten. Auch Lücke A (2 Std) in 30 Tagen.
			cell := int64(Tiers[Ranges[rng].Tier].Step / time.Second)
			for gi, g := range probeGaps {
				a, b := p.now+g[0], p.now+g[1]
				if b-a < int64(h.Step)+2*cell || b < p.now-span {
					continue
				}
				seen := false
				for i, x := range h.Times {
					if x < b && x+int64(h.Step) > a && math.IsNaN(float64(h.Series[KeyCPU][i])) {
						seen = true
					}
					if i > 0 && h.Times[i-1] < a && x >= b {
						seen = true
					}
				}
				if !seen {
					t.Errorf("%s/%d: Lücke %c nicht sichtbar (Schritt %d)", rng, pts, 'A'+gi, h.Step)
				}
			}
		}
	}
}

// Zwei Abfragen im Abstand einer Minute liefern an gleichen Zeiten gleiche
// Werte, außer am laufenden letzten Punkt: Die Linie wackelt nicht.
func TestZeitraeumeStabil(t *testing.T) {
	p := newProbe(t, probeGaps, nil)
	for _, rng := range rangesShortToLong {
		h1, _ := p.s.QueryPeaks(rng, []string{KeyCPU}, 360, time.Unix(p.now-60, 0), true)
		h2, _ := p.s.QueryPeaks(rng, []string{KeyCPU}, 360, time.Unix(p.now, 0), true)
		if h1.Step != h2.Step {
			t.Errorf("%s: Schritt %d, dann %d", rng, h1.Step, h2.Step)
			continue
		}
		at := map[int64]Value{}
		for i, x := range h2.Times {
			at[x] = h2.Series[KeyCPU][i]
		}
		// Die letzten Punkte der ersten Abfrage können noch laufende
		// Abschnitte enthalten haben.
		cut := p.now - 60 - int64(Tiers[Ranges[rng].Tier].Step/time.Second) - int64(h1.Step)
		diff := 0
		for i, x := range h1.Times {
			v2, ok := at[x]
			// Der erste Punkt ist am Anfang des Zeitraums angeschnitten.
			if !ok || x >= cut || i == 0 {
				continue
			}
			v1 := h1.Series[KeyCPU][i]
			if math.IsNaN(float64(v1)) != math.IsNaN(float64(v2)) || (!math.IsNaN(float64(v1)) && math.Abs(float64(v1-v2)) > 1e-4) {
				diff++
			}
		}
		if diff > 0 {
			t.Errorf("%s: %d Punkte geändert", rng, diff)
		}
	}
}

// D1: Fehlt mitten im Betrieb eine Messung, stehen im 10-Sekunden-Ring noch
// Plätze von vor einer Stunde. Sie dürfen weder Mittel noch Spitze der
// Zeiträume 6 Stunden bis 30 Tage verfälschen; eine Spitze in der Stunde
// danach bleibt.
func TestLueckeImRingAlteWerte(t *testing.T) {
	gap := [][2]int64{{-40 * 60, -40*60 + 10}}   // eine einzige Messung fehlt
	spike := map[int64]float64{-20*60 - 7: 99.5} // now endet auf :17, Messungen auf :x0
	p := newProbe(t, gap, spike)
	for _, rng := range []string{"6h", "1d", "7d", "30d"} {
		span := int64(Ranges[rng].Span / time.Second)
		h, _ := p.s.QueryPeaks(rng, []string{KeyCPU}, 360, time.Unix(p.now, 0), true)
		st := h.Stats[KeyCPU]
		mean, peak, _ := p.truth(p.now-span, p.now)
		if math.Abs(float64(st.Mean)-mean) > 0.1 {
			t.Errorf("%s: Mittel %.3f, richtig %.3f", rng, st.Mean, mean)
		}
		if rng == "6h" && math.Abs(float64(st.Peak)-99.5) > 1e-3 {
			t.Errorf("%s: Spitze %.3f, erwartet 99,5 (richtig %.3f)", rng, st.Peak, peak)
		}
	}
	// Auch „Verlauf seit“ liest keinen alten Platz.
	if f := p.s.First().Unix(); f < p.ts[0]-600 {
		t.Errorf("Verlauf seit %d, erste Messung %d", f, p.ts[0])
	}
}

// Springt die Uhr zurück, zählen Plätze aus der „Zukunft“ nicht mehr.
func TestUhrSpringtZurueck(t *testing.T) {
	s := &Store{}
	t0 := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 360; i++ {
		s.Add(Frame{At: t0.Add(time.Duration(i) * 10 * time.Second), Values: map[string]float64{KeyCPU: 90}})
	}
	back := t0.Add(30 * time.Minute)
	for i := 0; i < 6; i++ {
		s.Add(Frame{At: back.Add(time.Duration(i) * 10 * time.Second), Values: map[string]float64{KeyCPU: 10}})
	}
	h, _ := s.QueryPeaks("15m", []string{KeyCPU}, 360, back.Add(60*time.Second), true)
	for i, x := range h.Times {
		if x > back.Add(60*time.Second).Unix() {
			t.Errorf("Punkt %d nach jetzt: %v", x, h.Series[KeyCPU][i])
		}
	}
}

// Eine Pause von 2 Stunden ist auch in 30 Tagen (Schritt 2 Std)
// eine Lücke; der Punkt aus 3 Minuten Messung davor ist null, die Punkte
// rundherum haben Werte.
func TestLueckeAIn30Tagen(t *testing.T) {
	p := newProbe(t, probeGaps, nil)
	h, err := p.s.QueryPeaks("30d", []string{KeyCPU}, 360, time.Unix(p.now, 0), true)
	if err != nil {
		t.Fatal(err)
	}
	if h.Step != 7200 {
		t.Fatalf("Schritt %d", h.Step)
	}
	a := p.now + probeGaps[0][0]
	nulls, vals := 0, 0
	for i, x := range h.Times {
		v := float64(h.Series[KeyCPU][i])
		switch {
		case x+7200 > a && x < a+7200:
			if math.IsNaN(v) {
				nulls++
			}
		case x+3*7200 > a && x < a+3*7200:
			if !math.IsNaN(v) {
				vals++
			}
		}
	}
	if nulls == 0 || vals < 3 {
		t.Errorf("Lücke A: %d Punkte null, %d mit Wert daneben", nulls, vals)
	}
}
