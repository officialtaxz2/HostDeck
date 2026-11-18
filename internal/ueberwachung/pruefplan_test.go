package ueberwachung

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Tests zu Übersicht und Überwachung (T1 bis T11).

// T1: Kennzahlen über alle sechs Zeiträume monoton, nach einem Neustart und
// mit laufendem Abschnitt; die Übersicht (144 Punkte) und die Überwachung
// (360 Punkte) bekommen für 24 Stunden dieselben Kennzahlen.
func TestT1KennzahlenUeberZeitraeume(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 11, 10, 0, 0, 3, 0, time.UTC)
	s := &Store{Dir: dir}
	restart := start.Add(50*time.Hour + 7*time.Minute + 5*time.Second)
	feedSteal(s, start, restart, spikeAt(start.Add(20*time.Hour), 30*time.Second))
	if err := s.Close(restart); err != nil {
		t.Fatal(err)
	}
	s = &Store{Dir: dir}
	if err := s.Load(restart); err != nil {
		t.Fatal(err)
	}
	// mitten im Abschnitt enden (nicht auf 10 Minuten ausgerichtet)
	now := restart.Add(2*time.Hour + 13*time.Minute + 40*time.Second)
	feedSteal(s, restart.Add(10*time.Second), now, spikeAt(now.Add(-20*time.Minute), 20*time.Second))
	all := checkRanges(t, s, now)
	wantPeak(t, all, KeySteal, 8, now.Add(-20*time.Minute), "1h", "6h", "1d")
	wantPeak(t, all, KeySteal, 8, start.Add(20*time.Hour), "7d", "30d")
	a, err := s.QueryPeaks("1d", nil, 144, now, true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.QueryPeaks("1d", nil, 360, now, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{KeyCPU, KeySteal} {
		x, y := a.Stats[k], b.Stats[k]
		if !nearly(float64(x.Peak), float64(y.Peak)) || x.PeakAt != y.PeakAt || !nearly(float64(x.Mean), float64(y.Mean)) || x.LastAt != y.LastAt {
			t.Errorf("%s: 144 Punkte %+v, 360 Punkte %+v", k, x, y)
		}
	}
	if !s.First().Equal(start.Truncate(10 * time.Minute)) {
		t.Errorf("Verlauf seit %v, erwartet %v", s.First(), start.Truncate(10*time.Minute))
	}
}

// T1b: Ein ganz neuer Verlauf beginnt mitten in einem Abschnitt. Mittel,
// Spitze und „seit“ zählen erst ab der ersten Messung; das Mittel aus den
// Punkten, wie der Leitstand es rechnet (seit, bis), ist dasselbe wie
// das des Dienstes, in jedem Zeitraum (I14 im Zustand frisch).
func TestT1bNeuerVerlaufMittenImAbschnitt(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	start := time.Date(2026, 11, 16, 10, 54, 40, 0, time.UTC)
	now := start.Add(6*time.Minute + 5*time.Second)
	var sum, wsum, peak float64
	for i, at := 0, start; at.Before(now); i, at = i+1, at.Add(10*time.Second) {
		// Der Prozessor braucht zwei Messungen: Die erste hat nur den
		// Arbeitsspeicher.
		if i == 0 {
			s.Add(Frame{At: at, Values: map[string]float64{KeyRAM: 1e9}})
			continue
		}
		v := 2 + float64(i%7)*0.5
		if i < 20 {
			v += 3 // die ersten Minuten höher: der erste Abschnitt zählt sonst zu viel
		}
		s.Add(Frame{At: at, Values: map[string]float64{KeyCPU: v, KeyRAM: 1e9}})
		w := math.Min(10, now.Sub(at).Seconds())
		sum += v * w
		wsum += w
		peak = math.Max(peak, v)
	}
	want := sum / wsum
	if !s.First().Equal(start) {
		t.Errorf("Verlauf seit %v, erwartet %v", s.First(), start)
	}
	for _, r := range rangesShortToLong {
		h, err := s.QueryPeaks(r, nil, 360, now, true)
		if err != nil {
			t.Fatal(err)
		}
		st := h.Stats[KeyCPU]
		cpuFrom := start.Add(10 * time.Second).Unix()
		if math.Abs(float64(st.Mean)-want) > 1e-4*want || st.Since != cpuFrom || float64(st.Peak) != peak {
			t.Errorf("%s: Kennzahlen %+v, erwartet Mittel %.4f, seit %d, Spitze %v", r, st, want, cpuFrom, peak)
		}
		// Die Spitze mit ihrer Zeit auf 10 Sekunden, nicht „zwischen 10:50
		// und 11:00“.
		if peakAt := start.Add(60 * time.Second).Unix(); st.PeakAt != peakAt || st.PeakStep != 10 {
			t.Errorf("%s: Spitze um %d (%d s), erwartet %d (10 s)", r, st.PeakAt, st.PeakStep, peakAt)
		}
		if h.Begin != start.Unix() || h.End != now.Unix() {
			t.Errorf("%s: beginn %d, bis %d", r, h.Begin, h.End)
		}
		// Wie uwStatsP im Leitstand (Beginn der Reihe aus den Kennzahlen).
		var ps, pw float64
		for i, x := range h.Series[KeyCPU] {
			if math.IsNaN(float64(x)) {
				continue
			}
			w := float64(h.Step)
			if i+1 < len(h.Times) {
				w = math.Min(w, float64(h.Times[i+1]-h.Times[i]))
			} else {
				w = math.Min(w, float64(h.End-h.Times[i]))
			}
			if h.Times[i] < st.Since {
				w -= float64(st.Since - h.Times[i])
			}
			w = math.Max(1, w)
			ps += float64(x) * w
			pw += w
		}
		// Der laufende Abschnitt ist ein einfaches Mittel seiner Messungen,
		// der Dienst wertet die letzte nur bis jetzt: wenige Hundertstel.
		if got := ps / pw; math.Abs(got-want) > 0.02 {
			t.Errorf("%s: Mittel aus den Punkten %.4f, Dienst %.4f", r, got, want)
		}
	}
}

// T2: Eine Seite ist gewarnt („antwortet nicht“), dann stoppst du ihr Ziel.
// Keine Entwarnung, keine Mitteilung; ein stiller Hinweis „pausiert“ ohne
// „antwortet nicht“ im Titel, und der Ausfall endet mit Beginn der Pause.
func TestT2PauseBeendetWarnungOhneEntwarnung(t *testing.T) {
	g := &ruleRig{now: time.Date(2026, 11, 13, 20, 0, 0, 0, time.UTC)}
	g.backup = g.now
	r := g.rules(filepath.Join(t.TempDir(), "warnungen.json"))
	ctx := context.Background()
	step := func(d time.Duration) {
		t.Helper()
		g.now = g.now.Add(d)
		if err := r.Check(ctx, Frame{At: g.now}, Info{}); err != nil {
			t.Fatal(err)
		}
	}
	g.sites = []SiteCheck{{Host: "a.example", Answer: "Status 502"}}
	step(SiteEvery)
	step(SiteEvery)
	if k := g.kinds(); k != "warnung seite:a.example" {
		t.Fatalf("Warnung: %q", k)
	}
	stopped := g.now.Add(2 * time.Minute)
	g.sites = []SiteCheck{{Host: "a.example", Answer: "Status 502", Paused: "Du hast das Projekt a gestoppt; die Seite ist pausiert, bis du es wieder startest.", PausedSince: &stopped}}
	var all []Event
	for i := 0; i < 6; i++ {
		step(SiteEvery)
		all = append(all, g.events...)
		g.events = nil
	}
	if len(all) != 1 {
		t.Fatalf("Ereignisse: %+v", all)
	}
	e := all[0]
	if e.Kind != EvNote || !e.Quiet || strings.Contains(e.Title, "antwortet nicht") || !strings.Contains(e.Title, "pausiert") {
		t.Errorf("Hinweis: %+v", e)
	}
	if e.End == nil || !e.End.Equal(stopped) {
		t.Errorf("Ende des Ausfalls: %v, erwartet %v", e.End, stopped)
	}
	v := r.View(10)
	if len(v.Active) != 0 {
		t.Errorf("Warnung läuft noch: %+v", v.Active)
	}
	sc := v.Sites[0]
	want := int64(stopped.Sub(stopped.Add(-2*time.Minute)) / time.Second) // Warnung bis Pause
	if sc.Outages != 1 || sc.Down != want {
		t.Errorf("Ausfall %d, %d s; erwartet 1, %d s", sc.Outages, sc.Down, want)
	}
}

// T3: Pausen zählen weder als Ausfall noch als Messzeit; reine Pausentage
// sind kein Ausfall; ohne Messzeit gibt es keine Verfügbarkeit (null).
func TestT3PausenNichtGemessen(t *testing.T) {
	g := &ruleRig{now: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)}
	g.backup = g.now
	r := g.rules(filepath.Join(t.TempDir(), "warnungen.json"))
	ctx := context.Background()
	step := func(d time.Duration) {
		t.Helper()
		g.now = g.now.Add(d)
		if err := r.Check(ctx, Frame{At: g.now}, Info{}); err != nil {
			t.Fatal(err)
		}
	}
	// Von Anfang an pausiert: keine Messzeit, keine Verfügbarkeit.
	since := g.now.Add(-time.Hour)
	g.sites = []SiteCheck{{Host: "a.example", Answer: "Status 502", Paused: "gestoppt", PausedSince: &since}}
	step(SiteEvery)
	step(SiteEvery)
	sc := r.View(0).Sites[0]
	if sc.Avail != nil || sc.Measured != 0 || sc.Outages != 0 {
		t.Fatalf("nur Pause: Verfügbarkeit %v, Messzeit %d, Ausfälle %d", sc.Avail, sc.Measured, sc.Outages)
	}
	b, _ := json.Marshal(sc)
	if !strings.Contains(string(b), `"verfuegbarkeit":null`) {
		t.Errorf("JSON ohne null: %s", b)
	}
	// Einen Tag pausiert, dann läuft es wieder: 100 % über die Messzeit.
	for i := 0; i < 24*12; i++ {
		step(SiteEvery)
	}
	g.sites = []SiteCheck{{Host: "a.example", OK: true}}
	for i := 0; i < 12; i++ {
		step(SiteEvery)
	}
	sc = r.View(0).Sites[0]
	if sc.Avail == nil || *sc.Avail != 100 || sc.Outages != 0 {
		t.Errorf("nach der Pause: %v, Ausfälle %d", sc.Avail, sc.Outages)
	}
	if sc.Measured <= 0 || sc.Measured > int64(time.Hour/time.Second)+60 {
		t.Errorf("Messzeit %d s, erwartet etwa eine Stunde", sc.Measured)
	}
	for _, d := range sc.DownDays {
		if d != 0 {
			t.Errorf("Pausentag zählt als Ausfall: %v", sc.DownDays)
			break
		}
	}
	if len(sc.Pauses) != 1 || sc.Pauses[0].To.IsZero() {
		t.Errorf("Pausen: %+v", sc.Pauses)
	}
}

// T4: Altbestand. Ein Ausfall, der ganz in eine noch andauernde Pause fällt
// (du hattest das Ziel schon vorher gestoppt, wie beim alten Fehler K1),
// zählt nach dem Update einmal und gleichbleibend nicht mehr; das steht
// einmal still im Protokoll. Ein Ausfall vor dem Stoppen bleibt.
func TestT4AltbestandWirdPause(t *testing.T) {
	file := filepath.Join(t.TempDir(), "warnungen.json")
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	stop := time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC)
	k := "seite:play.example"
	t0, t1 := time.Date(2026, 11, 13, 20, 33, 0, 0, time.UTC), time.Date(2026, 11, 14, 20, 19, 0, 0, time.UTC)
	real0, real1 := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC), time.Date(2026, 11, 11, 9, 40, 0, 0, time.UTC)
	st := rulesState{Active: map[string]*Alert{}, SitesSince: map[string]time.Time{"play.example": now.Add(-10 * 24 * time.Hour)}, Events: []Event{
		{At: real0, Kind: EvStart, Key: k, Level: LevelErr, Title: "play.example antwortet nicht"},
		{At: real1, Kind: EvClear, Key: k, Level: LevelInfo, Title: "Wieder in Ordnung: play.example antwortet nicht"},
		{At: t0, Kind: EvStart, Key: k, Level: LevelErr, Title: "play.example antwortet nicht"},
		{At: t1, Kind: EvClear, Key: k, Level: LevelInfo, Title: "Wieder in Ordnung: play.example antwortet nicht"},
	}}
	b, _ := json.Marshal(st)
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
	g := &ruleRig{now: now, backup: now}
	g.sites = []SiteCheck{{Host: "play.example", Answer: "Status 502", Paused: "Du hast das Projekt play gestoppt.", PausedSince: &stop}}
	ctx := context.Background()
	for round := 0; round < 2; round++ { // zweimal: gleichbleibend, ein Eintrag
		r := g.rules(file)
		for i := 0; i < 3; i++ {
			g.now = g.now.Add(SiteEvery)
			if err := r.Check(ctx, Frame{At: g.now}, Info{}); err != nil {
				t.Fatal(err)
			}
		}
		v := r.View(10)
		sc := v.Sites[0]
		if sc.Outages != 1 || sc.LastDown == nil || !sc.LastDown.Equal(real0) {
			t.Errorf("Runde %d: %d Ausfälle, zuletzt %v (erwartet 1, %v)", round, sc.Outages, sc.LastDown, real0)
		}
		if sc.Down != int64(40*time.Minute/time.Second)-600 {
			t.Errorf("Runde %d: Ausfallzeit %d s", round, sc.Down)
		}
		for _, e := range v.Events {
			if e.At.Equal(t0) && !e.InPause {
				t.Errorf("Warnung im Altbestand nicht als Pause markiert: %+v", e)
			}
			if e.At.Equal(real0) && e.InPause {
				t.Errorf("echter Ausfall als Pause markiert: %+v", e)
			}
			// V10: Entwarnungen älterer Versionen lauten wie neue.
			if e.Kind == EvClear && e.Title != "play.example antwortet wieder" {
				t.Errorf("alter Entwarnungs-Titel: %q", e.Title)
			}
		}
	}
	n := 0
	for _, e := range g.events {
		if e.Kind == EvNote && e.Key == k {
			n++
			if !e.Quiet {
				t.Errorf("Hinweis geht aufs Handy: %+v", e)
			}
			if e.Title != "play.example: Ausfall zählt nicht" || !strings.Contains(e.Text, "Ein früherer Ausfall von play.example lag in einer Pause") || !strings.HasSuffix(e.Text, " Er zählt nicht mehr als Ausfall.") {
				t.Errorf("Hinweis: %q / %q", e.Title, e.Text)
			}
		}
	}
	if n != 1 {
		t.Errorf("%d Hinweise zum Altbestand, erwartet 1: %+v", n, g.events)
	}
}

// R9: Mehrere Ausfälle in einer Pause: Mehrzahl im Hinweis.
func TestR9AusfaelleMehrzahl(t *testing.T) {
	file := filepath.Join(t.TempDir(), "warnungen.json")
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	stop := time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC)
	k := "seite:play.example"
	var evs []Event
	for _, d := range []int{13, 14} {
		a := time.Date(2026, 11, d, 20, 0, 0, 0, time.UTC)
		evs = append(evs, Event{At: a, Kind: EvStart, Key: k, Level: LevelErr, Title: "play.example antwortet nicht"},
			Event{At: a.Add(time.Hour), Kind: EvClear, Key: k, Level: LevelInfo, Title: "play.example antwortet wieder"})
	}
	b, _ := json.Marshal(rulesState{Active: map[string]*Alert{}, SitesSince: map[string]time.Time{"play.example": now.Add(-10 * 24 * time.Hour)}, Events: evs})
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
	g := &ruleRig{now: now, backup: now}
	g.sites = []SiteCheck{{Host: "play.example", Answer: "Status 502", Paused: "Du hast das Projekt play gestoppt.", PausedSince: &stop}}
	r := g.rules(file)
	for i := 0; i < 3; i++ {
		g.now = g.now.Add(SiteEvery)
		if err := r.Check(context.Background(), Frame{At: g.now}, Info{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range g.events {
		if e.Kind == EvNote && e.Key == k {
			if e.Title != "play.example: Ausfälle zählen nicht" || !strings.Contains(e.Text, "2 frühere Ausfälle von play.example lagen in einer Pause") || !strings.HasSuffix(e.Text, " Sie zählen nicht mehr als Ausfall.") {
				t.Errorf("Hinweis: %q / %q", e.Title, e.Text)
			}
			return
		}
	}
	t.Errorf("kein Hinweis: %+v", g.events)
}

// T7: Texte des Dienstes nennen keine Uhrzeit (sie wäre in der Zeitzone des
// Servers); Zeitpunkte stehen in Feldern, die der Leitstand in seiner Zeit
// zeigt.
func TestT7TexteOhneUhrzeit(t *testing.T) {
	g := &ruleRig{now: time.Date(2026, 11, 13, 20, 0, 0, 0, time.UTC), boot: time.Date(2026, 11, 13, 19, 50, 0, 0, time.UTC)}
	g.backup = g.now
	r := g.rules(filepath.Join(t.TempDir(), "warnungen.json"))
	ctx := context.Background()
	var all []Event
	step := func(d time.Duration) {
		g.now = g.now.Add(d)
		if err := r.Check(ctx, Frame{At: g.now}, Info{}); err != nil {
			t.Fatal(err)
		}
		all = append(all, g.events...)
		g.events = nil
	}
	g.sites = []SiteCheck{{Host: "a.example", Answer: "Status 502"}}
	step(SiteEvery)
	g.boot = g.now.Add(-time.Minute) // Server neu gestartet
	step(SiteEvery)
	g.sites = []SiteCheck{{Host: "a.example", OK: true}}
	for i := 0; i < 4; i++ {
		step(SiteEvery)
	}
	clock := regexp.MustCompile(`\b\d{1,2}:\d{2}\b`)
	kinds := map[string]bool{}
	for _, e := range all {
		kinds[e.Kind] = true
		if clock.MatchString(e.Title + " " + e.Text) {
			t.Errorf("Uhrzeit im Text: %q / %q", e.Title, e.Text)
		}
		if e.Kind == EvClear && (e.Since == nil || e.Good == nil || strings.Contains(e.Title, "antwortet nicht")) {
			t.Errorf("Entwarnung: %+v", e)
		}
		if e.Kind == EvNote && e.Key == "neustart" && e.Since == nil {
			t.Errorf("Neustart ohne Zeitpunkt: %+v", e)
		}
	}
	if !kinds[EvClear] || !kinds[EvNote] || !kinds[EvStart] {
		t.Errorf("nicht alle Arten: %v", kinds)
	}
}

// T9: Backup hat eine Stufe vom Dienst: ohne Backup neutral bis zur
// 14-Tage-Regel, dann gelb, ab 30 Tagen rot; mit frischem Backup grün.
func TestT9BackupStufe(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		backup time.Time
		noSeit time.Time
		level  string
	}{
		{"ohne Backup, neu", time.Time{}, now.Add(-2 * 24 * time.Hour), LevelIdle},
		{"ohne Backup, 15 Tage", time.Time{}, now.Add(-15 * 24 * time.Hour), LevelWarn},
		{"ohne Backup, 31 Tage", time.Time{}, now.Add(-31 * 24 * time.Hour), LevelErr},
		{"frisch", now.Add(-24 * time.Hour), time.Time{}, LevelOK},
		{"15 Tage alt", now.Add(-15 * 24 * time.Hour), time.Time{}, LevelWarn},
		{"31 Tage alt", now.Add(-31 * 24 * time.Hour), time.Time{}, LevelErr},
	}
	for _, c := range cases {
		g := &ruleRig{now: now, backup: c.backup}
		r := g.rules("")
		r.load()
		r.st.NoBackup = c.noSeit
		if err := r.Check(context.Background(), Frame{At: now}, Info{}); err != nil {
			t.Fatal(err)
		}
		v := r.View(10)
		if v.Backup.Level != c.level || !v.Backup.Known {
			t.Errorf("%s: Stufe %q, erwartet %q", c.name, v.Backup.Level, c.level)
		}
		// Die Warnung der Regel und die Stufe sagen dasselbe.
		warned := len(v.Active) > 0 && v.Active[0].Key == "backup"
		if warned != (c.level == LevelWarn || c.level == LevelErr) || (warned && v.Active[0].Level != c.level) {
			t.Errorf("%s: Warnung %+v gegen Stufe %q", c.name, v.Active, c.level)
		}
	}
}

// T10: Der Leitstand bekommt die geltenden Grenzen mit jedem Stand; sie
// sind die geänderten, nicht die Voreinstellungen.
func TestT10GrenzenImStand(t *testing.T) {
	g := &ruleRig{now: time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)}
	r := g.rules(filepath.Join(t.TempDir(), "warnungen.json"))
	lim := DefaultLimits
	lim.CPUWarn, lim.DiskWarn = 50, 0.5
	if _, err := r.SetLimits(lim); err != nil {
		t.Fatal(err)
	}
	v := r.View(0)
	if v.Limits.CPUWarn != 50 || v.Limits.DiskWarn != 0.5 || r.CurrentLimits().CPUWarn != 50 {
		t.Errorf("Grenzen: %+v", v.Limits)
	}
}

// T11: „keine Daten“ ist null, nicht 0: Eine Seite ohne Messzeit hat keine
// Verfügbarkeit, nicht 100 %.
func TestT11NullStattNull(t *testing.T) {
	g := &ruleRig{now: time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)}
	r := g.rules("")
	g.sites = []SiteCheck{{Host: "a.example", OK: true}}
	if err := r.Check(context.Background(), Frame{At: g.now}, Info{}); err != nil {
		t.Fatal(err)
	}
	sc := r.View(0).Sites[0]
	if sc.Avail != nil {
		t.Errorf("Verfügbarkeit ohne Messzeit: %v", *sc.Avail)
	}
	g.now = g.now.Add(SiteEvery)
	if err := r.Check(context.Background(), Frame{At: g.now}, Info{}); err != nil {
		t.Fatal(err)
	}
	if sc := r.View(0).Sites[0]; sc.Avail == nil || *sc.Avail != 100 {
		t.Errorf("nach 5 Minuten: %v", sc.Avail)
	}
}

// E1: Prozessor je Container und Projekt kommt als Anteil am ganzen Server
// (alle Kerne = 100 %): im Stand, im Verlauf und bei den Kosten von HostDeck.
// Der Server selbst (s/cpu) bleibt, wie er ist.
func TestE1AnteilAmServer(t *testing.T) {
	if !PerCore("c/web/cpu") || !PerCore("p/shop/cpu") || PerCore("s/cpu") || PerCore("c/web/ram") {
		t.Fatal("PerCore erkennt die Reihen falsch")
	}
	s := &Service{Store: &Store{Dir: t.TempDir()}}
	s.info.Cores = 4
	s.last = Frame{At: time.Now(), Values: map[string]float64{"c/web/cpu": 80, "s/cpu": 30, "c/web/ram": 1e6}}
	s.cost = Cost{CPU: 8, Avg: 4}
	snap := s.Snapshot()
	if snap.Values["c/web/cpu"] != 20 || snap.Values["s/cpu"] != 30 || snap.Values["c/web/ram"] != 1e6 {
		t.Errorf("Stand: %v", snap.Values)
	}
	if snap.Cost.CPU != 2 || snap.Cost.Avg != 1 {
		t.Errorf("Kosten: %+v", snap.Cost)
	}
	nan := Value(math.NaN())
	h := History{Series: map[string][]Value{"c/web/cpu": {40, nan}, "s/cpu": {50, 60}},
		Peaks: map[string][]Value{"c/web/cpu": {100, nan}},
		Stats: map[string]Stat{"c/web/cpu": {Last: 40, Mean: 20, Peak: 100}, "s/cpu": {Last: 60, Mean: 55, Peak: 60}}}
	shareHistory(&h, 4)
	if h.Series["c/web/cpu"][0] != 10 || !math.IsNaN(float64(h.Series["c/web/cpu"][1])) || h.Series["s/cpu"][0] != 50 {
		t.Errorf("Reihen: %v", h.Series)
	}
	if h.Peaks["c/web/cpu"][0] != 25 {
		t.Errorf("Spitzen: %v", h.Peaks)
	}
	if st := h.Stats["c/web/cpu"]; st.Last != 10 || st.Mean != 5 || st.Peak != 25 {
		t.Errorf("Kennzahlen: %+v", st)
	}
	if st := h.Stats["s/cpu"]; st.Peak != 60 {
		t.Errorf("Server verändert: %+v", st)
	}
}

// SK1: Die Warnung zu einer Seite nennt dasselbe Wort und denselben Grund wie
// der Zustand der Domain in Lageplan und Seiten.
func TestSeitenWarnungMitWortDesZustands(t *testing.T) {
	g := &ruleRig{now: time.Date(2026, 11, 13, 20, 0, 0, 0, time.UTC)}
	g.backup = g.now
	r := g.rules(filepath.Join(t.TempDir(), "warnungen.json"))
	g.sites = []SiteCheck{{Host: "leer.example", Answer: "Status 502", Word: "zeigt ins Leere", Why: "Am Ziel 127.0.0.1:8099 lauscht nichts."}}
	for i := 0; i < 2; i++ {
		g.now = g.now.Add(SiteEvery)
		if err := r.Check(context.Background(), Frame{At: g.now}, Info{}); err != nil {
			t.Fatal(err)
		}
	}
	v := r.View(10)
	if len(v.Active) != 1 || v.Active[0].Title != "leer.example zeigt ins Leere" || !strings.HasPrefix(v.Active[0].Text, "Am Ziel 127.0.0.1:8099 lauscht nichts. ") {
		t.Errorf("Warnung: %+v", v.Active)
	}
}

// R1: Endet eine Container-Warnung, weil du den Container
// gestoppt hast, kommt keine Entwarnung mit Mitteilung („web ist wieder
// gesund“), sondern ein stiller Hinweis, wie bei einer Seite in einer Pause.
func TestT2bContainerGestopptBeendetWarnungStill(t *testing.T) {
	for _, tc := range []struct {
		key  string
		info Info
		cs   []ContainerState
	}{
		{"gesundheit:web", Info{RAMTotal: 16e9, Containers: []ContainerInfo{{Name: "web", State: "running", Health: "unhealthy"}}},
			[]ContainerState{{Name: "web", State: "running", Running: true}}},
		{"neustarts:web", Info{RAMTotal: 16e9}, []ContainerState{{Name: "web", State: "restarting"}}},
		{"aus:web", Info{RAMTotal: 16e9}, []ContainerState{{Name: "web", State: "exited", ExitCode: 1}}},
	} {
		g := &ruleRig{now: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
		g.backup = g.now
		g.conts = tc.cs
		r := g.rules(filepath.Join(t.TempDir(), "warnungen.json"))
		ctx := context.Background()
		info := tc.info
		step := func(d time.Duration) {
			t.Helper()
			g.now = g.now.Add(d)
			if err := r.Check(ctx, Frame{At: g.now}, info); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 8; i++ {
			step(time.Minute)
		}
		if k := g.kinds(); !strings.Contains(k, "warnung "+tc.key) {
			t.Fatalf("%s: keine Warnung: %q", tc.key, k)
		}
		// Du stoppst ihn im Leitstand.
		info = Info{RAMTotal: 16e9}
		g.conts = []ContainerState{{Name: "web", State: "exited", StoppedByYou: true}}
		var all []Event
		for i := 0; i < 16; i++ {
			step(time.Minute)
			all = append(all, g.events...)
			g.events = nil
		}
		if len(all) != 1 || all[0].Kind != EvNote || !all[0].Quiet || all[0].Key != tc.key || !strings.Contains(all[0].Title, "von dir gestoppt") {
			t.Errorf("%s: Ereignisse %+v", tc.key, all)
		}
		if v := r.View(0); len(v.Active) != 0 {
			t.Errorf("%s: Warnung läuft noch: %+v", tc.key, v.Active)
		}
	}
}

// R5: Der Stand nennt die laufenden Wartezeiten, damit der Leitstand eine
// kurze Spitze nicht färbt; eine Warnung steht nicht mehr darunter.
func TestR5WartezeitImStand(t *testing.T) {
	file := filepath.Join(t.TempDir(), "warnungen.json")
	g := &ruleRig{now: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC), boot: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	g.backup = g.now.Add(-24 * time.Hour)
	r := g.rules(file)
	ctx := context.Background()
	info := Info{RAMTotal: 16e9}
	step := func(cpu float64) {
		t.Helper()
		g.now = g.now.Add(10 * time.Second)
		if err := r.Check(ctx, Frame{At: g.now, Values: map[string]float64{KeyCPU: cpu, KeyRAM: 4e9}}, info); err != nil {
			t.Fatal(err)
		}
	}
	step(5)
	if w := r.View(10).Waiting; len(w) != 0 {
		t.Fatalf("ohne Befund wartet: %+v", w)
	}
	step(95)
	start := g.now
	w := r.View(10).Waiting
	if len(w) != 1 || w[0].Key != "prozessor" || !w[0].Since.Equal(start) || w[0].Hold != int(DefaultLimits.CPUHold/time.Minute) {
		t.Fatalf("Spitze: %+v", w)
	}
	b, _ := json.Marshal(r.View(10))
	if !strings.Contains(string(b), `"wartet":[{"schluessel":"prozessor"`) {
		t.Errorf("JSON: %s", b)
	}
	for g.now.Sub(start) < DefaultLimits.CPUHold {
		step(95)
	}
	v := r.View(10)
	if len(v.Waiting) != 0 || len(v.Active) != 1 {
		t.Errorf("nach der Wartezeit: wartet %+v, aktiv %d", v.Waiting, len(v.Active))
	}
}

// R10: Eine eingehängte einzelne Datei ist kein Laufwerk, auch wenn ihr
// Pfad kürzer ist als der des Ordners auf demselben Gerät.
func TestR10DateiIstKeinLaufwerk(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "h")
	data := filepath.Join(dir, "daten-ordner")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(data, 0o700); err != nil {
		t.Fatal(err)
	}
	proc := filepath.Join(dir, "proc")
	if err := os.MkdirAll(filepath.Join(proc, "1"), 0o700); err != nil {
		t.Fatal(err)
	}
	mounts := "/dev/vda " + file + " ext4 rw 0 0\n/dev/vda " + data + " ext4 rw 0 0\n"
	if err := os.WriteFile(filepath.Join(proc, "1", "mounts"), []byte(mounts), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &Collector{Proc: proc, Disk: func(string) (int64, int64, error) { return 100e9, 60e9, nil }}
	f := Frame{Values: map[string]float64{}}
	var info Info
	c.disks(f, &info)
	if len(info.Disks) != 1 || info.Disks[0].Mount != data {
		t.Errorf("Laufwerke: %+v, erwartet nur %s", info.Disks, data)
	}
}
