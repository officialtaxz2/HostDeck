package ueberwachung

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Stufen des Verlaufs: je feiner, desto kürzer.
type tierSpec struct {
	Step time.Duration
	Keep time.Duration
	File string // Dateiname-Anfang; leer: nur im Speicher
}

// Tiers sind die Stufen: 1 Stunde in 10 s, 2 Tage in Minuten, 30 Tage in
// 10 Minuten. Die feinste lebt nur im Speicher.
var Tiers = []tierSpec{
	{Step: 10 * time.Second, Keep: time.Hour},
	{Step: time.Minute, Keep: 48 * time.Hour, File: "minuten"},
	{Step: 10 * time.Minute, Keep: 30 * 24 * time.Hour, File: "zehn"},
}

// Store hält den Verlauf in Ringen fester Größe und schreibt die gröberen
// Stufen als gzip-Zeilen je Tag in Dir.
type Store struct {
	Dir   string
	Tiers []tierSpec // Standard Tiers

	mu    sync.Mutex
	tiers []*tier
}

type tier struct {
	spec   tierSpec
	step   int64
	n      int
	times  []int64 // Beginn des Abschnitts je Platz; 0 leer
	series map[string][]float32
	// Spitze je Abschnitt in den gröberen Stufen: der höchste Einzelwert,
	// aus dem der Mittelwert entstand. Eine kurze Spitze von 90 % bleibt
	// so sichtbar, auch wenn der Mittelwert 30 % sagt.
	peaks map[string][]float32
	// Mittelwert und Spitze des laufenden Abschnitts.
	accAt   int64
	accSum  map[string]float64
	accN    map[string]int
	accMax  map[string]float64
	pending []line // noch nicht geschrieben
	// lastPut: zuletzt geschriebener Abschnitt. Plätze gelten nur, wenn sie
	// in der Haltezeit davor liegen (valid); put leert, was es überspringt.
	lastPut int64
}

type line struct {
	T int64              `json:"t"`
	V map[string]float64 `json:"v"`
	M map[string]float64 `json:"m,omitempty"` // Spitzen; fehlt in Dateien vor ui1
	// N: Anzahl der Messungen je Reihe, nur bei einem Teilabschnitt, den
	// HostDeck beim Beenden mitten im Abschnitt schreibt. Beim Start sammelt es
	// dort weiter, statt den Abschnitt neu zu beginnen.
	N map[string]int `json:"n,omitempty"`
}

func (s *Store) init() {
	if s.tiers != nil {
		return
	}
	specs := s.Tiers
	if specs == nil {
		specs = Tiers
	}
	for _, sp := range specs {
		step := int64(sp.Step / time.Second)
		n := int(sp.Keep / sp.Step)
		s.tiers = append(s.tiers, &tier{spec: sp, step: step, n: n, times: make([]int64, n), series: map[string][]float32{}, peaks: map[string][]float32{},
			accSum: map[string]float64{}, accN: map[string]int{}, accMax: map[string]float64{}})
	}
}

// Add nimmt eine Messung auf.
func (s *Store) Add(f Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	t := f.At.Unix()
	for i, tr := range s.tiers {
		b := t - t%tr.step
		if i == 0 {
			tr.put(b, f.Values, nil)
			continue
		}
		if tr.accAt != 0 && tr.accAt != b {
			tr.flushAcc()
		}
		tr.accAt = b
		for k, v := range f.Values {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			if m, ok := tr.accMax[k]; !ok || v > m {
				tr.accMax[k] = v
			}
			tr.accSum[k] += v
			tr.accN[k]++
		}
	}
}

// flushAcc schreibt den Mittelwert des laufenden Abschnitts in den Ring.
func (tr *tier) flushAcc() {
	if len(tr.accN) == 0 {
		return
	}
	vals := make(map[string]float64, len(tr.accN))
	peaks := make(map[string]float64, len(tr.accN))
	for k, n := range tr.accN {
		vals[k] = tr.accSum[k] / float64(n)
		// Nur, wo die Spitze vom Mittel abweicht; spart Platz.
		if m := tr.accMax[k]; n > 1 && m > vals[k] {
			peaks[k] = m
		}
	}
	tr.put(tr.accAt, vals, peaks)
	if tr.spec.File != "" {
		tr.pending = append(tr.pending, line{T: tr.accAt, V: vals, M: peaks})
	}
	tr.accSum, tr.accN, tr.accMax = map[string]float64{}, map[string]int{}, map[string]float64{}
}

// partial ist der laufende Abschnitt als Teilzeile mit Anzahl.
func (tr *tier) partial() (line, bool) {
	if len(tr.accN) == 0 {
		return line{}, false
	}
	l := line{T: tr.accAt, V: map[string]float64{}, M: map[string]float64{}, N: map[string]int{}}
	for k, n := range tr.accN {
		l.V[k] = tr.accSum[k] / float64(n)
		l.N[k] = n
		if m := tr.accMax[k]; m > l.V[k] {
			l.M[k] = m
		}
	}
	return l, true
}

// resume sammelt in einem Teilabschnitt aus der Datei weiter.
func (tr *tier) resume(l line) {
	tr.accAt = l.T
	tr.accSum, tr.accN, tr.accMax = map[string]float64{}, map[string]int{}, map[string]float64{}
	for k, v := range l.V {
		n := l.N[k]
		if n <= 0 {
			n = 1
		}
		tr.accSum[k] = v * float64(n)
		tr.accN[k] = n
		tr.accMax[k] = math.Max(v, l.M[k])
	}
}

func (tr *tier) slot(b int64) int { return int((b / tr.step) % int64(tr.n)) }

// valid sagt, ob ein Platz mit Beginn t zum Ring gehört: nicht leer und
// innerhalb der Haltezeit vor dem zuletzt geschriebenen Abschnitt. Ein
// Platz, den eine Lücke übersprungen hat, oder einer aus der Zeit nach
// einem Rücksprung der Uhr zählt so nie mit (D1).
func (tr *tier) valid(t int64) bool {
	return t != 0 && t <= tr.lastPut && t > tr.lastPut-int64(tr.n)*tr.step
}

// span liefert den ältesten und den jüngsten Abschnitt der Stufe samt dem
// laufenden; first ist 0, wenn die Stufe leer ist.
func (tr *tier) span() (first, last int64) {
	for _, t := range tr.times {
		if !tr.valid(t) {
			continue
		}
		if first == 0 || t < first {
			first = t
		}
		last = max(last, t)
	}
	if len(tr.accN) > 0 {
		if first == 0 || tr.accAt < first {
			first = tr.accAt
		}
		last = max(last, tr.accAt)
	}
	return first, last
}

// begin ist, wann der Verlauf anfängt, so genau es die Stufen wissen. Der
// älteste Abschnitt beginnt an seiner Grenze; bei einem ganz neuen Verlauf
// ist er nur zum Teil gefüllt (der erste 10-Minuten-Abschnitt etwa ab der
// vierten Minute). Liegt der erste Abschnitt einer feineren Stufe noch in
// ihm, beginnen die Messungen erst dort. Eine feinere Stufe, deren Ring
// schon alte Abschnitte verworfen haben kann, sagt darüber nichts. Nach
// einem Neustart beginnt die feinste Stufe neu; das verschiebt begin nur,
// wenn der Verlauf da jünger als ein Abschnitt der nächsten Stufe war.
// Aufrufer hält s.mu.
func (s *Store) begin() int64 {
	var b, end int64
	for _, tr := range s.tiers {
		if f, _ := tr.span(); f != 0 && (b == 0 || f < b) {
			b, end = f, f+tr.step
		}
	}
	for i := len(s.tiers) - 1; i >= 0 && b != 0; i-- {
		tr := s.tiers[i]
		f, last := tr.span()
		if f > b && f < end && f > last-int64(tr.n-1)*tr.step {
			b, end = f, min(end, f+tr.step)
		}
	}
	return b
}

func (tr *tier) put(b int64, vals, peaks map[string]float64) {
	// Fehlen Messungen, stünden in den übersprungenen Plätzen noch Werte von
	// vor einer Haltezeit; sie wären ein Stück Verlauf, das es nicht gibt (D1).
	if tr.lastPut != 0 && b > tr.lastPut+tr.step {
		for t, k := tr.lastPut+tr.step, 0; t < b && k < tr.n; t, k = t+tr.step, k+1 {
			tr.clearSlot(tr.slot(t))
		}
	}
	tr.lastPut = b
	i := tr.slot(b)
	if tr.times[i] != b {
		for _, sv := range tr.series {
			sv[i] = float32(math.NaN())
		}
		for _, sv := range tr.peaks {
			sv[i] = float32(math.NaN())
		}
		tr.times[i] = b
	}
	ring := func(m map[string][]float32, k string) []float32 {
		sv, ok := m[k]
		if !ok {
			sv = make([]float32, tr.n)
			for j := range sv {
				sv[j] = float32(math.NaN())
			}
			m[k] = sv
		}
		return sv
	}
	for k, v := range vals {
		ring(tr.series, k)[i] = float32(v)
		// Ohne eigene Spitze ist der Mittelwert die Spitze.
		if pk, ok := tr.peaks[k]; ok {
			pk[i] = float32(math.NaN())
		}
	}
	for k, v := range peaks {
		ring(tr.peaks, k)[i] = float32(v)
	}
}

// clear leert den Platz von Abschnitt b.
func (tr *tier) clear(b int64) {
	i := tr.slot(b)
	if tr.times[i] != b {
		return
	}
	tr.clearSlot(i)
}

// clearSlot leert Platz i.
func (tr *tier) clearSlot(i int) {
	tr.times[i] = 0
	for _, sv := range tr.series {
		sv[i] = float32(math.NaN())
	}
	for _, sv := range tr.peaks {
		sv[i] = float32(math.NaN())
	}
}

// prune wirft Reihen weg, die in dieser Stufe keinen Wert mehr haben
// (etwa ein gelöschter Container).
func (tr *tier) prune() {
	for k, sv := range tr.series {
		empty := true
		for i, v := range sv {
			if tr.valid(tr.times[i]) && !math.IsNaN(float64(v)) {
				empty = false
				break
			}
		}
		if empty {
			delete(tr.series, k)
			delete(tr.peaks, k)
		}
	}
}

// Flush schreibt, was seit dem letzten Mal dazukam, und räumt alte Dateien
// weg.
func (s *Store) Flush(now time.Time) error {
	s.mu.Lock()
	s.init()
	type job struct {
		file  string
		lines []line
	}
	var jobs []job
	for _, tr := range s.tiers {
		tr.prune()
		if tr.spec.File == "" || len(tr.pending) == 0 {
			continue
		}
		byDay := map[string][]line{}
		for _, l := range tr.pending {
			day := time.Unix(l.T, 0).UTC().Format("20060102")
			byDay[day] = append(byDay[day], l)
		}
		for day, ls := range byDay {
			jobs = append(jobs, job{filepath.Join(s.Dir, tr.spec.File+"-"+day+".jsonl.gz"), ls})
		}
		tr.pending = nil
	}
	specs := make([]tierSpec, len(s.tiers))
	for i, tr := range s.tiers {
		specs[i] = tr.spec
	}
	s.mu.Unlock()

	if s.Dir == "" {
		return nil
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	var errs []error
	for _, j := range jobs {
		if err := appendLines(j.file, j.lines); err != nil {
			errs = append(errs, err)
		}
	}
	errs = append(errs, s.removeOld(specs, now))
	return errors.Join(errs...)
}

// Close schreibt beim Beenden auch die laufenden Abschnitte als
// Teilzeilen; Load sammelt dort weiter. Ohne das fehlte eine Spitze kurz
// vor einem Neustart (etwa einem Update) in den gröberen Stufen für immer.
func (s *Store) Close(now time.Time) error {
	s.mu.Lock()
	s.init()
	for _, tr := range s.tiers {
		if tr.spec.File == "" {
			continue
		}
		if l, ok := tr.partial(); ok {
			tr.pending = append(tr.pending, l)
		}
	}
	s.mu.Unlock()
	return s.Flush(now)
}

// appendLines hängt ein gzip-Stück an; gzip liest mehrere Stücke
// hintereinander als eine Datei.
func appendLines(path string, ls []line) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	enc := json.NewEncoder(zw)
	for _, l := range ls {
		if err := enc.Encode(compact(l)); err != nil {
			zw.Close()
			f.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// compact kürzt Werte auf fünf gültige Stellen; das spart Platz und ist
// genauer, als jede Messung es ist.
func compact(l line) any {
	num := func(m map[string]float64) map[string]json.Number {
		if len(m) == 0 {
			return nil
		}
		v := make(map[string]json.Number, len(m))
		for k, x := range m {
			v[k] = json.Number(strconv.FormatFloat(x, 'g', 5, 64))
		}
		return v
	}
	v := num(l.V)
	if v == nil {
		v = map[string]json.Number{}
	}
	return struct {
		T int64                  `json:"t"`
		V map[string]json.Number `json:"v"`
		M map[string]json.Number `json:"m,omitempty"`
		N map[string]int         `json:"n,omitempty"`
	}{l.T, v, num(l.M), l.N}
}

func (s *Store) removeOld(specs []tierSpec, now time.Time) error {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		for _, sp := range specs {
			if sp.File == "" {
				continue
			}
			day, ok := dayOf(e.Name(), sp.File)
			if !ok {
				continue
			}
			// Weg, wenn der ganze Tag vor dem Aufbewahrten liegt.
			if day.Add(24 * time.Hour).Before(now.Add(-sp.Keep)) {
				_ = os.Remove(filepath.Join(s.Dir, e.Name()))
			}
		}
	}
	return nil
}

func dayOf(name, prefix string) (time.Time, bool) {
	d, ok := strings.CutPrefix(name, prefix+"-")
	if !ok {
		return time.Time{}, false
	}
	d, ok = strings.CutSuffix(d, ".jsonl.gz")
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse("20060102", d)
	return t, err == nil
}

// Load liest die Dateien in die Ringe; kaputte Zeilen am Ende (Absturz
// mitten im Schreiben) überspringt es.
func (s *Store) Load(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, tr := range s.tiers {
		if tr.spec.File == "" {
			continue
		}
		var names []string
		for _, e := range entries {
			if _, ok := dayOf(e.Name(), tr.spec.File); ok {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		from := now.Add(-tr.spec.Keep).Unix()
		// Eine spätere Zeile zum selben Abschnitt ersetzt die frühere; sie
		// enthält sie (siehe resume).
		var last line
		for _, n := range names {
			if err := readLines(filepath.Join(s.Dir, n), func(l line) {
				dropRetired(l)
				if l.T >= from && l.T <= now.Unix() {
					tr.put(l.T, l.V, l.M)
					if l.T >= last.T {
						last = l
					}
				}
			}); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", n, err))
			}
		}
		// Endet die Datei mit einem Teilabschnitt, sammelt HostDeck dort weiter.
		if len(last.N) > 0 && len(tr.accN) == 0 {
			tr.clear(last.T)
			tr.resume(last)
		}
	}
	return errors.Join(errs...)
}

// retired: Reihen, die HostDeck nicht mehr misst. Spielerzahlen (g/<id>/spieler)
// erfasst HostDeck seit dem 18.11.2026 nicht mehr (Spielserver sind Container wie
// alle anderen); alte Werte fallen beim Laden weg, statt 30 Tage zu bleiben.
func retired(key string) bool {
	return strings.HasPrefix(key, "g/") && strings.HasSuffix(key, "/spieler")
}

func dropRetired(l line) {
	for _, m := range []map[string]float64{l.V, l.M} {
		for k := range m {
			if retired(k) {
				delete(m, k)
			}
		}
	}
	for k := range l.N {
		if retired(k) {
			delete(l.N, k)
		}
	}
}

func readLines(path string, fn func(line)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(bufio.NewReader(f))
	if err != nil {
		return err
	}
	dec := json.NewDecoder(zr)
	for {
		var l line
		err := dec.Decode(&l)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			// Abgeschnittenes Ende: Was davor stand, ist schon gelesen.
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		fn(l)
	}
}

// Value ist ein Wert im Verlauf; fehlt er, steht null.
type Value float32

func (v Value) MarshalJSON() ([]byte, error) {
	f := float64(v)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return []byte("null"), nil
	}
	return []byte(strconv.FormatFloat(f, 'g', 5, 32)), nil
}

// History ist ein Verlauf.
type History struct {
	Step   int                `json:"schritt"` // Sekunden je Punkt
	Times  []int64            `json:"zeiten"`  // Unix-Sekunden, Beginn des Abschnitts
	Series map[string][]Value `json:"serien"`
	// Peaks: höchster Einzelwert je Punkt, nur auf Wunsch (spitzen=1).
	Peaks map[string][]Value `json:"spitzen,omitempty"`
	// Stats: Zuletzt, Mittel und Spitze je Reihe über den ganzen Zeitraum,
	// nur auf Wunsch (spitzen=1). Siehe stats.
	Stats map[string]Stat `json:"kennzahlen,omitempty"`
	// Kurz nach einem Neustart: Punkte bis CoarseTo stammen aus der
	// gröberen Stufe und liegen CoarseStep Sekunden auseinander.
	CoarseTo   int64 `json:"grob_bis,omitempty"`
	CoarseStep int   `json:"grob_schritt,omitempty"`
	// Begin: Beginn des Verlaufs (wie verlauf_seit in /api/ueberwachung).
	// Der erste Punkt eines neuen Verlaufs kann früher beginnen und zählt im
	// Mittel erst ab hier (je Reihe genauer: seit in den Kennzahlen). End:
	// Ende der jüngsten Messung, höchstens die Zeit der Abfrage; der letzte
	// Punkt ist der laufende Abschnitt und zählt im Mittel nur bis hier.
	Begin int64 `json:"beginn,omitempty"`
	End   int64 `json:"bis"`
}

// Stat sind die Kennzahlen einer Reihe in einem Zeitraum.
type Stat struct {
	Last   Value `json:"zuletzt"`    // letzter gemessener Wert
	LastAt int64 `json:"zuletzt_um"` // Unix-Sekunden
	Mean   Value `json:"mittel"`     // nach Zeit gewichtet
	Peak   Value `json:"spitze"`     // höchster Einzelwert
	PeakAt int64 `json:"spitze_um"`  // Beginn des Abschnitts mit der Spitze
	// PeakStep: Länge dieses Abschnitts in Sekunden, so genau ist PeakAt.
	PeakStep int   `json:"spitze_schritt"`
	Since    int64 `json:"seit"` // erster Wert im Zeitraum
}

// Ranges sind die Zeiträume, die der Leitstand zeigt.
var Ranges = map[string]struct {
	Span time.Duration
	Tier int
}{
	"15m": {15 * time.Minute, 0},
	"1h":  {time.Hour, 0},
	"6h":  {6 * time.Hour, 1},
	"1d":  {24 * time.Hour, 1},
	"7d":  {7 * 24 * time.Hour, 2},
	"30d": {30 * 24 * time.Hour, 2},
}

// MaxPoints: mehr Punkte zeigt kein Diagramm sinnvoll.
const MaxPoints = 720

// Query liefert den Verlauf im Zeitraum rng bis now, höchstens points
// Punkte (0: MaxPoints). patterns wählt die Reihen (leer: alle): ein
// Muster mit „*“ gilt wie path.Match für den ganzen Schlüssel (etwa
// „c/*/cpu“, „*“ reicht nie über „/“), sonst als Anfang des Schlüssels.
func (s *Store) Query(rng string, patterns []string, points int, now time.Time) (History, error) {
	return s.QueryPeaks(rng, patterns, points, now, false)
}

// cell ist ein Abschnitt einer Stufe: ein Platz im Ring (slot) oder der
// laufende Abschnitt (slot -1).
type cell struct {
	t    int64
	slot int
	tr   *tier
}

// at liefert Mittel und Spitze einer Reihe in einem Abschnitt.
func (c cell) at(k string) (v, pk float64, ok bool) {
	tr := c.tr
	if c.slot < 0 {
		n := tr.accN[k]
		if n == 0 {
			return 0, 0, false
		}
		// Wie im Ring als float32, damit dieselbe Messung in jeder Stufe
		// gleich groß ist.
		v = float64(float32(tr.accSum[k] / float64(n)))
		return v, math.Max(v, float64(float32(tr.accMax[k]))), true
	}
	sv, ok := tr.series[k]
	if !ok {
		return 0, 0, false
	}
	v = float64(sv[c.slot])
	if math.IsNaN(v) {
		return 0, 0, false
	}
	pk = v
	if p, ok := tr.peaks[k]; ok && !math.IsNaN(float64(p[c.slot])) {
		pk = math.Max(pk, float64(p[c.slot]))
	}
	return v, pk, true
}

// cells liefert die Abschnitte der Stufe mit Beginn in [from, to], auf
// Wunsch mit dem laufenden.
func (tr *tier) cells(from, to int64, running bool) []cell {
	var out []cell
	for i, t := range tr.times {
		if tr.valid(t) && t >= from && t <= to {
			out = append(out, cell{t, i, tr})
		}
	}
	if running && len(tr.accN) > 0 && tr.accAt >= from && tr.accAt <= to {
		out = append(out, cell{tr.accAt, -1, tr})
	}
	return out
}

// Steps ist die Leiter der Schritte je Punkt in Sekunden. Alle Schritte bis
// zu einer Stunde teilen die Stunde; Punkte beginnen darum zu festen
// Uhrzeiten (10:00, 10:05, …) und verschieben sich beim Nachladen nicht (D2).
var Steps = []int64{10, 20, 30, 60, 120, 300, 600, 900, 1200, 1800, 3600, 7200, 10800, 21600, 43200}

// pickStep ist der kleinste Schritt der Leiter, nicht feiner als die Stufe,
// mit dem span in höchstens points Schritte passt.
func pickStep(span, min int64, points int) int64 {
	for _, st := range Steps {
		if st >= min && span <= st*int64(points) {
			return st
		}
	}
	return max(min, Steps[len(Steps)-1])
}

// bin ist ein Punkt im Diagramm: alle Abschnitte mit Beginn in [t, t+Schritt).
type bin struct {
	t     int64
	cells []cell
}

// bins teilt Abschnitte in Punkte zu festen Uhrzeiten: von dem Punkt mit
// dem ältesten bis zu dem mit dem jüngsten Abschnitt; Punkte ohne Abschnitt
// bleiben leer (keine Messung). Abschnitte sortiert.
func bins(cs []cell, step int64) []bin {
	if len(cs) == 0 {
		return nil
	}
	first := cs[0].t - cs[0].t%step
	last := cs[len(cs)-1].t - cs[len(cs)-1].t%step
	out := make([]bin, 0, (last-first)/step+1)
	j := 0
	for t := first; t <= last; t += step {
		b := bin{t: t}
		for j < len(cs) && cs[j].t < t+step {
			b.cells = append(b.cells, cs[j])
			j++
		}
		out = append(out, b)
	}
	return out
}

// QueryPeaks wie Query; mit peaks auch die Spitze je Punkt (der höchste
// Einzelwert, den die Stufe kennt; feinste Stufe: der Wert selbst) und die
// Kennzahlen je Reihe.
//
// Punkte liegen auf Vielfachen von Schritt in Unix-Zeit (Leiter Steps). Ein
// Punkt ohne Messung oder mit Messung über höchstens die Hälfte seines
// Schritts ist null; so bleibt jede Lücke ab einem Schritt sichtbar, auch
// wenn ein Punkt viele Abschnitte zusammenfasst.
func (s *Store) QueryPeaks(rng string, patterns []string, points int, now time.Time, peaks bool) (History, error) {
	r, ok := Ranges[rng]
	if !ok {
		return History{}, fmt.Errorf("unbekannter Zeitraum %q", rng)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	if r.Tier >= len(s.tiers) {
		return History{}, fmt.Errorf("Zeitraum %q gibt es hier nicht", rng)
	}
	if points <= 0 || points > MaxPoints {
		points = MaxPoints
	}
	span := int64(r.Span / time.Second)
	from := now.Add(-r.Span).Unix()
	tr := s.tiers[r.Tier]
	// Der laufende Abschnitt ist der vorläufige letzte Punkt; sonst fehlte
	// in langen Zeiträumen, was in den letzten Minuten war.
	pts := tr.cells(from, now.Unix(), true)
	sort.Slice(pts, func(i, j int) bool { return pts[i].t < pts[j].t })
	step := pickStep(span, tr.step, points)
	var bs []bin
	var coarseTo, coarseStep int64
	// Die feinste Stufe lebt nur im Speicher und beginnt nach einem
	// Neustart leer. Was davor liegt, kommt aus der nächsten Stufe; nur
	// Abschnitte, die ganz vor dem ersten feinen Punkt enden.
	if r.Tier == 0 && len(s.tiers) > 1 {
		first := now.Unix() + 1
		if len(pts) > 0 {
			first = pts[0].t
		}
		next := s.tiers[1]
		more := next.cells(from, first-next.step, true)
		sort.Slice(more, func(i, j int) bool { return more[i].t < more[j].t })
		if len(more) > 0 {
			cs := pickStep(span, next.step, points)
			if len(pts) == 0 {
				step, pts = cs, more
			} else {
				coarseStep = cs
				bs = bins(more, cs)
				coarseTo = bs[len(bs)-1].t
			}
		}
	}
	main := bins(pts, step)
	// Die feinen Punkte beginnen nach dem letzten groben.
	for len(main) > 0 && coarseTo > 0 && main[0].t <= coarseTo {
		main = main[1:]
	}
	bs = append(bs, main...)
	h := History{Step: int(step), Times: []int64{}, Series: map[string][]Value{}, Begin: s.begin(), End: now.Unix()}
	if _, last := s.tiers[0].span(); last != 0 {
		h.End = min(h.End, last+s.tiers[0].step)
	}
	if peaks {
		h.Peaks = map[string][]Value{}
	}
	if coarseTo > 0 && step != coarseStep {
		h.CoarseTo, h.CoarseStep = coarseTo, int(coarseStep)
	}
	match := func(k string) bool {
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
	var keys []string
	seen := map[string]bool{}
	add := func(k string) {
		if !seen[k] && match(k) {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	tiers := []*tier{tr}
	if peaks {
		tiers = s.tiers // Kennzahlen nutzen alle Stufen
	} else if r.Tier+1 < len(s.tiers) {
		tiers = append(tiers, s.tiers[r.Tier+1])
	}
	for _, t := range tiers {
		for k := range t.series {
			add(k)
		}
		for k := range t.accN {
			add(k)
		}
	}
	sort.Strings(keys)
	// Gewicht eines Abschnitts im Punkt wie in stats: so lange, wie er
	// Messungen hat (der erste eines neuen Verlaufs ab begin, der laufende
	// bis jetzt).
	weight := func(c cell) float64 {
		f := max(c.t, h.Begin)
		w := float64(c.tr.step - (f - c.t))
		if c.slot < 0 || c.t+c.tr.step > now.Unix() {
			w = math.Min(w, float64(now.Unix()-f))
		}
		return math.Max(1, w)
	}
	// Ein Punkt, dessen Abschnitte höchstens die Hälfte seines Schritts
	// abdecken, ist null: Sonst stünde ein Punkt aus 3 Minuten Messung als
	// voller 2-Stunden-Punkt da, und jede Lücke unter zwei Schritten bliebe
	// unsichtbar. Ausgenommen sind der erste Punkt (Anfang des Zeitraums oder
	// des Verlaufs), der erste feine nach groben und der laufende letzte.
	nCoarse := len(bs) - len(main)
	thin := func(i int, b bin) bool {
		if i == 0 || i == nCoarse || i == len(bs)-1 {
			return false
		}
		st := step
		if i < nCoarse {
			st = coarseStep
		}
		var cov int64
		for _, c := range b.cells {
			cov += max(0, min(c.t+c.tr.step, now.Unix())-max(c.t, h.Begin))
		}
		return 2*cov <= st
	}
	for i, b := range bs {
		h.Times = append(h.Times, b.t)
		gap := thin(i, b)
		for _, k := range keys {
			if gap {
				h.Series[k] = append(h.Series[k], Value(math.NaN()))
				if peaks {
					h.Peaks[k] = append(h.Peaks[k], Value(math.NaN()))
				}
				continue
			}
			var sum, wsum float64
			top := math.NaN()
			for _, p := range b.cells {
				v, pk, ok := p.at(k)
				if !ok {
					continue
				}
				w := weight(p)
				sum += v * w
				wsum += w
				if math.IsNaN(top) || pk > top {
					top = pk
				}
			}
			v := Value(math.NaN())
			if wsum > 0 {
				v = Value(sum / wsum)
			}
			h.Series[k] = append(h.Series[k], v)
			if peaks {
				h.Peaks[k] = append(h.Peaks[k], Value(top))
			}
		}
	}
	// Reihen, die nur in der anderen Stufe vorkamen und hier keinen Wert
	// haben, fallen weg.
	for k, v := range h.Series {
		empty := true
		for _, x := range v {
			if !math.IsNaN(float64(x)) {
				empty = false
				break
			}
		}
		if empty {
			delete(h.Series, k)
			delete(h.Peaks, k)
		}
	}
	if peaks {
		h.Stats = s.stats(keys, from, now.Unix())
	}
	return h, nil
}

// stats rechnet die Kennzahlen je Reihe im Zeitraum [from, now]. Jedes
// Stück Zeit kommt aus der feinsten Stufe, die es dafür hat, samt dem
// laufenden Abschnitt. Welche Stufe ein Stück liefert, hängt nur von der
// Zeit ab, nicht vom Zeitraum: Ein längerer Zeitraum enthält darum alle
// Abschnitte eines kürzeren, und seine Spitze ist nie kleiner. Aufrufer
// hält s.mu.
func (s *Store) stats(keys []string, from, now int64) map[string]Stat {
	// Alle Grenzen auf den gröbsten Schritt, dann schließen die Stufen
	// lückenlos aneinander an. Ausnahme ist die feinste Stufe: Sie lebt nur
	// im Speicher und beginnt nach einem Neustart mitten in einer Minute.
	// Sie liefert ab ihrer ersten Messung, die Minuten davor nur, soweit
	// sie vorher enden, wie im Diagramm (QueryPeaks). Sonst zählte die
	// angeschnittene Minute am Neustart, die nur wenige Sekunden Messung
	// hat, im Mittel eine volle Minute.
	align := s.tiers[len(s.tiers)-1].step
	begin := s.begin()
	var use, peakOnly []cell
	limit := int64(math.MaxInt64) // ab hier liefern feinere Stufen
	for i, tr := range s.tiers {
		all := tr.cells(math.MinInt64, now, true)
		first := int64(math.MaxInt64)
		for _, c := range all {
			first = min(first, c.t)
		}
		lo := from
		if i < len(s.tiers)-1 {
			if first == math.MaxInt64 {
				continue // Stufe leer
			}
			b := (first + align - 1) / align * align
			// Reicht die Stufe bis zum Beginn des Verlaufs, gibt es davor
			// nichts, woran sie anschließen müsste: Ein neuer Verlauf hat so
			// von Anfang an Werte in 10 Sekunden statt eines halb gefüllten
			// 10-Minuten-Abschnitts.
			if first <= begin || i == 0 {
				b = first
			}
			if b >= limit {
				continue
			}
			lo = max(lo, b)
		}
		for _, c := range all {
			// Am Anfang des Zeitraums zählt auch der angeschnittene
			// Abschnitt, mit dem Stück im Zeitraum (D3).
			cut := lo == from && c.t < from && c.t+tr.step > from
			if (c.t >= lo || cut) && c.t+tr.step <= limit {
				use = append(use, c)
			} else if c.t < limit && c.t+tr.step > limit && c.t+tr.step > from {
				// Reicht in die feinere Stufe hinein: zählt nicht im
				// Mittel (das liefert die feinere), aber seine Spitze
				// bleibt, denn das Diagramm längerer Zeiträume zeigt sie.
				peakOnly = append(peakOnly, c)
			}
		}
		if lo < limit {
			limit = lo
		}
	}
	sort.Slice(use, func(i, j int) bool { return use[i].t < use[j].t })
	out := map[string]Stat{}
	for _, k := range keys {
		st := Stat{}
		var sum, wsum float64
		found := false
		for _, c := range use {
			v, pk, ok := c.at(k)
			if !ok {
				continue
			}
			// Jeder Abschnitt zählt so lange, wie er Messungen hat: der
			// laufende bis jetzt, der erste eines neuen Verlaufs ab begin.
			from := max(c.t, begin, from)
			w := float64(c.tr.step - (from - c.t))
			if c.slot < 0 || c.t+c.tr.step > now {
				w = math.Min(w, float64(now-from))
			}
			w = math.Max(1, w)
			sum += v * w
			wsum += w
			if !found {
				st.Since = from
			}
			if !found || pk > float64(st.Peak) {
				st.Peak, st.PeakAt, st.PeakStep = Value(pk), from, int(c.tr.step-(from-c.t))
			}
			found = true
		}
		if !found {
			continue
		}
		st.Mean = Value(sum / wsum)
		for _, c := range peakOnly {
			if _, pk, ok := c.at(k); ok && pk > float64(st.Peak) {
				from := max(c.t, begin, from)
				st.Peak, st.PeakAt, st.PeakStep = Value(pk), from, int(c.tr.step-(from-c.t))
			}
		}
		// Zuletzt: der jüngste Wert aus der feinsten Stufe, die ihn hat.
		st.LastAt = -1
		for _, tr := range s.tiers {
			for _, c := range tr.cells(from, now, true) {
				if v, _, ok := c.at(k); ok && c.t > st.LastAt {
					st.Last, st.LastAt = Value(v), c.t
				}
			}
			if st.LastAt >= 0 {
				break
			}
		}
		out[k] = st
	}
	return out
}

// First ist der Beginn des Verlaufs (alle Stufen, siehe begin), sonst null:
// „Verlauf seit“ im Leitstand. Anders als der Start des Dienstes springt er
// nicht bei jedem Update (V18).
func (s *Store) First() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.init()
	first := s.begin()
	if first == 0 {
		return time.Time{}
	}
	return time.Unix(first, 0)
}

// Size ist, was der Verlauf auf der Platte belegt, und wie viele Reihen
// er gerade führt.
func (s *Store) Size() (bytes int64, series int) {
	s.mu.Lock()
	s.init()
	for _, tr := range s.tiers {
		series = max(series, len(tr.series))
	}
	s.mu.Unlock()
	entries, _ := os.ReadDir(s.Dir)
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl.gz") {
			bytes += fi.Size()
		}
	}
	return bytes, series
}
