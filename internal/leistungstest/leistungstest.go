// Package leistungstest führt benchmark.sh als Lauf von HostDeck aus:
// Geekbench 6.7.1 und Sysbench mit mpstat und vmstat,
// danach Messübersicht und Support-ZIP.
//
// Das Skript steckt unverändert im Programm (benchmark.sh, eingebettet).
// Einzige Ergänzung gegenüber dem Original ist der Schalter GEEKBENCH=0,
// der Geekbench weglässt (kein Download, kein Hochladen; auch auf arm64).
// HostDeck setzt nur die Umgebung: HOME zeigt auf den Arbeitsordner, weil ein
// systemd-Dienst ohne Benutzer kein $HOME hat und das Skript mit set -u
// sonst abbricht. Was das Skript ablegt, räumt HostDeck danach in den
// Ergebnisordner: je Test eine ZIP, eine Kurzfassung (JSON) und, falls
// Geekbench einen geliefert hat, den privaten Claim-Link.
package leistungstest

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Script ist benchmark.sh, wie HostDeck es ausführt.
//
//go:embed benchmark.sh
var Script []byte

// ScriptSHA256 ist der Fingerabdruck von benchmark.sh. Ändert sich das
// Skript, muss er bewusst mitgeändert werden (Test).
const ScriptSHA256 = "bd6ac25d53de205d6bdb1f4c7f0c36b76d52324e957d7d199109bc30336308cc"

// GeekbenchVersion ist die Version, die das Skript festpinnt.
const GeekbenchVersion = "6.7.1"

// Dauer je Sysbench-Test in Sekunden.
const (
	DefaultSeconds = 120 // wie im Skript
	MinSeconds     = 10
	MaxSeconds     = 1800
)

// Path ist der Suchpfad für das Skript und die Prüfung der Pakete.
const Path = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// WorkDir ist der Arbeitsordner im Ergebnisordner: HOME des Skripts, dort
// liegt auch das heruntergeladene Geekbench für den nächsten Test.
const WorkDir = "arbeit"

// Fehler.
var (
	ErrNotFound = errors.New("diesen Test kennt HostDeck nicht")
	ErrBadID    = errors.New("ungültige Kennung eines Tests")
	ErrOptions  = errors.New("ungültige Einstellung")
)

// Stände eines Tests.
const (
	StateDone       = "fertig"         // alle Phasen ohne Fehler
	StateIncomplete = "unvollstaendig" // mindestens eine Phase ging schief
	StateCancelled  = "abgebrochen"
	StateFailed     = "gescheitert" // keine Messung entstanden
)

var idRe = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}$`)

// ValidID prüft eine Kennung wie 20261109-151738.
func ValidID(id string) bool { return idRe.MatchString(id) }

// Options sind die Einstellungen eines Tests.
type Options struct {
	Geekbench bool `json:"geekbench"`
	// Seconds je Sysbench-Test; 0 heißt DefaultSeconds.
	Seconds int `json:"sekunden"`
}

func (o Options) seconds() int {
	if o.Seconds == 0 {
		return DefaultSeconds
	}
	return o.Seconds
}

// Check prüft die Einstellungen.
func (o Options) Check() error {
	if s := o.seconds(); s < MinSeconds || s > MaxSeconds {
		return fmt.Errorf("%w: die Dauer je Sysbench-Test muss zwischen %d und %d Sekunden liegen", ErrOptions, MinSeconds, MaxSeconds)
	}
	return nil
}

// Steal ist die Auswertung von mpstat für eine Phase, so wie das Skript
// sie in die Messübersicht schreibt.
type Steal struct {
	Phase     string   `json:"phase"`
	Intervals int      `json:"sekunden"`
	Mean      float64  `json:"mittel"`
	Max       float64  `json:"spitze"`
	MaxVCPU   float64  `json:"spitze_vcpu"`
	Over10    int      `json:"ueber10"`
	Over20    int      `json:"ueber20"`
	LoadMean  *float64 `json:"last_mittel,omitempty"`
	LoadMax   *float64 `json:"last_spitze,omitempty"`
}

// Result ist die Kurzfassung eines Tests.
type Result struct {
	ID        string    `json:"id"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"ende"`
	State     string    `json:"stand"`
	Code      int       `json:"code"` // Rückgabewert des Skripts; -1: abgebrochen
	Host      string    `json:"server,omitempty"`
	CPU       string    `json:"prozessor,omitempty"` // Model name aus lscpu
	VCPUs     int       `json:"vcpus,omitempty"`
	Geekbench bool      `json:"geekbench"`
	Seconds   int       `json:"sekunden"`
	Threads   int       `json:"threads,omitempty"`

	SysbenchSingle float64 `json:"sysbench_1,omitempty"` // events per second
	SysbenchMulti  float64 `json:"sysbench_n,omitempty"`
	GeekSingle     int     `json:"geekbench_1,omitempty"`
	GeekMulti      int     `json:"geekbench_n,omitempty"`
	GeekURL        string  `json:"geekbench_link,omitempty"`
	Steal          []Steal `json:"steal,omitempty"`

	Claim   bool  `json:"claim_link,omitempty"` // privater Claim-Link liegt vor
	ZipSize int64 `json:"zip,omitempty"`        // Größe der ZIP; 0: keine
	// Summary ist 00-Messuebersicht.txt; die Liste lässt sie weg.
	Summary string `json:"uebersicht,omitempty"`
	// Tail sind die letzten Zeilen der Ausgabe, wenn keine Messübersicht
	// entstand (etwa weil Pakete fehlten).
	Tail []string `json:"ausgabe,omitempty"`
	// Packed: Das Skript konnte keine ZIP bauen; HostDeck hat den Ordner selbst
	// gepackt.
	Packed bool `json:"von_sbm_gepackt,omitempty"` // Kennung im Datenformat
}

// Info sagt dem Leitstand, was vor einem Test gilt.
type Info struct {
	Arch             string   `json:"arch"`
	Threads          int      `json:"threads"`
	Seconds          int      `json:"sekunden"`
	GeekbenchVersion string   `json:"geekbench_version"`
	GeekbenchOK      bool     `json:"geekbench_moeglich"`
	GeekbenchReady   bool     `json:"geekbench_da"` // schon heruntergeladen
	GeekbenchWhy     string   `json:"geekbench_grund,omitempty"`
	Missing          []string `json:"pakete_fehlen"`
	NoApt            bool     `json:"ohne_apt,omitempty"` // fehlende Pakete, aber kein apt-get
	// GeekbenchBytes ist, was der Download von Geekbench im Arbeitsordner
	// belegt (Archiv und entpackt); 0: nichts da.
	GeekbenchBytes int64 `json:"geekbench_belegt,omitempty"`
}

// Service führt Tests aus und verwaltet die Ergebnisse.
type Service struct {
	Dir    string           // Ergebnisordner, etwa /var/lib/hostdeck/leistungstest
	Script []byte           // nil: das eingebettete benchmark.sh
	Arch   string           // leer: runtime.GOARCH
	Host   string           // Name des Servers für die Liste
	Now    func() time.Time // nil: time.Now
	// Root ist die Wurzel für die Prüfung der Pakete (Tests); leer: /.
	Root string
	// Bash ist die Shell; leer: /bin/bash.
	Bash string
	// Grace ist die Zeit zwischen SIGTERM und SIGKILL beim Abbrechen;
	// 0: 90 Sekunden.
	Grace time.Duration

	mu sync.Mutex // ein Test zur Zeit; die Laufsperre hält der Aufrufer
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) arch() string {
	if s.Arch != "" {
		return s.Arch
	}
	return runtime.GOARCH
}

func (s *Service) script() []byte {
	if s.Script != nil {
		return s.Script
	}
	return Script
}

func (s *Service) work() string { return filepath.Join(s.Dir, WorkDir) }

func (s *Service) rooted(p string) string {
	if s.Root == "" {
		return p
	}
	return filepath.Join(s.Root, p)
}

// packages ist die Liste aus benchmark.sh: Programm und Paket dazu.
var packages = [][2]string{
	{"sysbench", "sysbench"}, {"mpstat", "sysstat"}, {"vmstat", "procps"}, {"zip", "zip"}, {"unzip", "unzip"},
	{"curl", "curl"}, {"tar", "tar"}, {"lscpu", "util-linux"}, {"nproc", "coreutils"}, {"stdbuf", "coreutils"},
	{"tee", "coreutils"}, {"mktemp", "coreutils"}, {"awk", "mawk"}, {"sed", "sed"}, {"grep", "grep"},
}

func (s *Service) have(cmd string) bool {
	for _, d := range filepath.SplitList(Path) {
		fi, err := os.Stat(s.rooted(filepath.Join(d, cmd)))
		if err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return true
		}
	}
	return false
}

// Missing sind die Pakete, die das Skript vor dem Test installieren würde.
func (s *Service) Missing() []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range packages {
		if s.have(p[0]) || seen[p[1]] {
			continue
		}
		seen[p[1]] = true
		out = append(out, p[1])
	}
	if fi, err := os.Stat(s.rooted("/etc/ssl/certs/ca-certificates.crt")); err != nil || fi.Size() == 0 {
		out = append(out, "ca-certificates")
	}
	return out
}

func (s *Service) geekbenchDir() string { return filepath.Join(s.work(), "benchmarks") }

func (s *Service) geekbenchBin() string {
	return filepath.Join(s.geekbenchDir(), "Geekbench-"+GeekbenchVersion+"-Linux", "geekbench6")
}

// GeekbenchSize zählt, was der Geekbench-Ordner belegt.
func (s *Service) GeekbenchSize() int64 {
	var n int64
	filepath.WalkDir(s.geekbenchDir(), func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// RemoveGeekbench löscht den heruntergeladenen Geekbench samt Archiv. Der
// nächste Test mit Geekbench lädt ihn neu. Gibt zurück, wie viel frei wurde.
func (s *Service) RemoveGeekbench() (int64, error) {
	n := s.GeekbenchSize()
	if err := os.RemoveAll(s.geekbenchDir()); err != nil {
		return 0, err
	}
	return n, nil
}

// Info beschreibt, was ein Test jetzt bräuchte.
func (s *Service) Info() Info {
	in := Info{Arch: s.arch(), Threads: runtime.NumCPU(), Seconds: DefaultSeconds, GeekbenchVersion: GeekbenchVersion, Missing: s.Missing()}
	if fi, err := os.Stat(s.geekbenchBin()); err == nil && fi.Mode()&0o111 != 0 {
		in.GeekbenchReady = true
	}
	in.GeekbenchBytes = s.GeekbenchSize()
	switch {
	case in.GeekbenchReady || in.Arch == "amd64":
		in.GeekbenchOK = true
	default:
		in.GeekbenchWhy = "Geekbench " + GeekbenchVersion + " lädt das Skript nur für x86_64 herunter; dieser Server hat " + in.Arch + "."
	}
	if len(in.Missing) > 0 && !s.have("apt-get") {
		in.NoApt = true
	}
	if in.Missing == nil {
		in.Missing = []string{}
	}
	return in
}

// tail behält die letzten Zeilen der Ausgabe.
type tail struct {
	out   io.Writer
	lines []string
	part  []byte
}

const tailLines = 40

func (t *tail) Write(p []byte) (int, error) {
	n, err := t.out.Write(p)
	t.part = append(t.part, p[:n]...)
	for {
		i := bytes.IndexAny(t.part, "\r\n")
		if i < 0 {
			break
		}
		if l := strings.TrimRight(string(t.part[:i]), " "); l != "" {
			t.lines = append(t.lines, l)
			if len(t.lines) > tailLines {
				t.lines = t.lines[len(t.lines)-tailLines:]
			}
		}
		t.part = t.part[i+1:]
	}
	if len(t.part) > 4096 {
		t.part = nil
	}
	return n, err
}

const evidencePrefix = "vps-cpu-evidence-"

// evidence liefert die Messordner im Arbeitsordner.
func (s *Service) evidence() map[string]bool {
	out := map[string]bool{}
	es, _ := os.ReadDir(s.work())
	for _, e := range es {
		if e.IsDir() && strings.HasPrefix(e.Name(), evidencePrefix) && ValidID(strings.TrimPrefix(e.Name(), evidencePrefix)) {
			out[e.Name()] = true
		}
	}
	return out
}

// Run führt einen Test aus. Die Ausgabe des Skripts geht an out. Endet
// ctx, bekommt das Skript SIGTERM, schreibt seine Messübersicht und packt
// die ZIP wie bei Strg+C.
func (s *Service) Run(ctx context.Context, opt Options, out io.Writer) (Result, error) {
	if err := opt.Check(); err != nil {
		return Result{}, err
	}
	if !s.mu.TryLock() {
		return Result{}, errors.New("es läuft schon ein Leistungstest")
	}
	defer s.mu.Unlock()
	if sum := sha256.Sum256(s.script()); s.Script == nil && hex.EncodeToString(sum[:]) != ScriptSHA256 {
		return Result{}, errors.New("benchmark.sh im Programm ist beschädigt")
	}
	if err := os.MkdirAll(s.work(), 0o700); err != nil {
		return Result{}, err
	}
	script := filepath.Join(s.work(), "benchmark.sh")
	if err := writeFile(script, s.script(), 0o700); err != nil {
		return Result{}, err
	}
	before := s.evidence()
	gb := "0"
	if opt.Geekbench {
		gb = "1"
	}
	bash := s.Bash
	if bash == "" {
		bash = "/bin/bash"
	}
	start := s.now()
	t := &tail{out: out}
	cmd := exec.Command(bash, script)
	cmd.Dir = s.work()
	cmd.Env = []string{"PATH=" + Path, "HOME=" + s.work(), "LANG=C.UTF-8", "GEEKBENCH=" + gb, "SYSBENCH_SECONDS=" + strconv.Itoa(opt.seconds())}
	cmd.Stdout, cmd.Stderr = t, t
	grace := s.Grace
	if grace == 0 {
		grace = 90 * time.Second
	}
	err := runGroup(ctx, cmd, grace)
	code := 0
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		code = ee.ExitCode()
	case err != nil:
		return Result{}, err
	}
	res := Result{Start: start, End: s.now(), Code: code, Host: s.Host, Geekbench: opt.Geekbench, Seconds: opt.seconds()}
	if ctx.Err() != nil {
		res.State = StateCancelled
	}
	// Den neuen Messordner suchen.
	var dir string
	for name := range s.evidence() {
		if !before[name] && name > dir {
			dir = name
		}
	}
	if dir == "" {
		res.ID = start.Local().Format("20060102-150405")
		if res.State == "" {
			res.State = StateFailed
		}
		res.Tail = t.lines
		return res, s.save(res)
	}
	res.ID = strings.TrimPrefix(dir, evidencePrefix)
	full := filepath.Join(s.work(), dir)
	parseInto(&res, full)
	if res.State == "" {
		res.State = StateDone
		if code != 0 || strings.Contains(res.Summary, "Status: Unvollstaendig") {
			res.State = StateIncomplete
		}
	}
	if res.Summary == "" {
		res.Tail = t.lines
	}
	if err := s.collect(&res, dir); err != nil {
		return res, err
	}
	return res, s.save(res)
}

// collect legt ZIP und Claim-Link in den Ergebnisordner und entfernt den
// Messordner; die ZIP enthält ihn ganz.
func (s *Service) collect(res *Result, dir string) error {
	full := filepath.Join(s.work(), dir)
	zipDst := filepath.Join(s.Dir, res.ID+".zip")
	if err := os.Rename(full+".zip", zipDst); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := packDir(full, zipDst); err != nil {
			return fmt.Errorf("Messordner packen: %w", err)
		}
		res.Packed = true
	}
	if fi, err := os.Stat(zipDst); err == nil {
		res.ZipSize = fi.Size()
	}
	claim := filepath.Join(s.work(), "geekbench-private-claim-"+res.ID+".txt")
	if b, err := os.ReadFile(claim); err == nil {
		if err := writeFile(filepath.Join(s.Dir, res.ID+".claim"), b, 0o600); err != nil {
			return err
		}
		os.Remove(claim)
		res.Claim = true
	}
	return os.RemoveAll(full)
}

func (s *Service) save(res Result) error {
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(s.Dir, res.ID+".json"), append(b, '\n'), 0o600)
}

func writeFile(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".hostdeck-neu"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// packDir packt einen Messordner so, wie zip -r es täte: mit dem Ordner
// als oberster Ebene.
func packDir(dir, dst string) error {
	f, err := os.OpenFile(dst+".hostdeck-neu", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	base := filepath.Dir(dir)
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return err
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		r, err := os.Open(p)
		if err != nil {
			return err
		}
		defer r.Close()
		_, err = io.Copy(w, r)
		return err
	})
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst + ".hostdeck-neu")
		return err
	}
	return os.Rename(dst+".hostdeck-neu", dst)
}

// ---------- Messübersicht lesen ----------

var (
	sysbenchRe = regexp.MustCompile(`(?m)^Sysbench (single|multi)-core:.*events per second:\s*([0-9.]+)`)
	threadsRe  = regexp.MustCompile(`(?m)^Sysbench: 1 Thread und ([0-9]+) Threads`)
	gbURLRe    = regexp.MustCompile(`https://browser\.geekbench\.com/v6/cpu/[0-9]+`)
	gbScoreRe  = regexp.MustCompile(`(?m)^\s*(Single|Multi)-Core Score\s+([0-9]+)\s*$`)
	modelRe    = regexp.MustCompile(`(?m)^Model name:\s+(.+?)\s*$`)
	vcpuRe     = regexp.MustCompile(`(?m)^Verfuegbare vCPUs:\n([0-9]+)`)
	stealHead  = regexp.MustCompile(`^\[(.+)\]$`)
	pctRe      = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?) %`)
)

func readSmall(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, 1<<20))
	return string(b)
}

func parseInto(res *Result, dir string) {
	sum := readSmall(filepath.Join(dir, "00-Messuebersicht.txt"))
	res.Summary = sum
	for _, m := range sysbenchRe.FindAllStringSubmatch(sum, -1) {
		v, _ := strconv.ParseFloat(m[2], 64)
		if m[1] == "single" {
			res.SysbenchSingle = v
		} else {
			res.SysbenchMulti = v
		}
	}
	if m := threadsRe.FindStringSubmatch(sum); m != nil {
		res.Threads, _ = strconv.Atoi(m[1])
	}
	res.GeekURL = gbURLRe.FindString(sum)
	res.Steal = parseSteal(sum)
	gb := readSmall(filepath.Join(dir, "01-geekbench", "geekbench.log"))
	for _, m := range gbScoreRe.FindAllStringSubmatch(gb, -1) {
		v, _ := strconv.Atoi(m[2])
		if m[1] == "Single" && res.GeekSingle == 0 {
			res.GeekSingle = v
		} else if m[1] == "Multi" && res.GeekMulti == 0 {
			res.GeekMulti = v
		}
	}
	sys := readSmall(filepath.Join(dir, "system.txt"))
	if m := modelRe.FindStringSubmatch(sys); m != nil {
		res.CPU = m[1]
	}
	if m := vcpuRe.FindStringSubmatch(sys); m != nil {
		res.VCPUs, _ = strconv.Atoi(m[1])
	}
}

// parseSteal liest Abschnitt 4 der Messübersicht.
func parseSteal(sum string) []Steal {
	var out []Steal
	var cur *Steal
	sc := bufio.NewScanner(strings.NewReader(sum))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if m := stealHead.FindStringSubmatch(line); m != nil {
			out = append(out, Steal{Phase: m[1]})
			cur = &out[len(out)-1]
			continue
		}
		if cur == nil {
			continue
		}
		if line == "" || strings.HasPrefix(line, "5. ") {
			cur = nil
			continue
		}
		pcts := pctRe.FindAllStringSubmatch(line, -1)
		num := func(i int) float64 {
			if i >= len(pcts) {
				return 0
			}
			v, _ := strconv.ParseFloat(pcts[i][1], 64)
			return v
		}
		switch {
		case strings.HasPrefix(line, "Sekundenintervalle:"):
			cur.Intervals, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Sekundenintervalle:")))
		case strings.HasPrefix(line, "Steal, Mittel aller Intervalle:"):
			cur.Mean = num(0)
		case strings.HasPrefix(line, "Steal, Maximum (CPU all):"):
			cur.Max = num(0)
		case strings.HasPrefix(line, "Maximaler Steal einer einzelnen vCPU:"):
			cur.MaxVCPU = num(0)
		case strings.HasPrefix(line, "Intervalle mit >=10 % / >=20 % Steal:"):
			f := strings.Fields(strings.TrimPrefix(line, "Intervalle mit >=10 % / >=20 % Steal:"))
			if len(f) == 3 {
				cur.Over10, _ = strconv.Atoi(f[0])
				cur.Over20, _ = strconv.Atoi(f[2])
			}
		case strings.HasPrefix(line, "Steal bei CPU-Arbeit"):
			if i := strings.Index(line, "Mittel"); i >= 0 {
				p := pctRe.FindAllStringSubmatch(line[i:], -1)
				if len(p) == 2 {
					a, _ := strconv.ParseFloat(p[0][1], 64)
					b, _ := strconv.ParseFloat(p[1][1], 64)
					cur.LoadMean, cur.LoadMax = &a, &b
				}
			}
		}
	}
	// Phasen ohne Messung (etwa Geekbench ausgelassen) zählen nicht.
	keep := out[:0]
	for _, x := range out {
		if x.Intervals > 0 {
			keep = append(keep, x)
		}
	}
	return keep
}

// ---------- Ergebnisse ----------

// List liefert alle Tests, die neuesten zuerst, ohne Messübersicht.
func (s *Service) List() ([]Result, error) {
	es, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []Result{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Result{}
	for _, e := range es {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !ValidID(id) || !e.Type().IsRegular() {
			continue
		}
		r, err := s.Report(id)
		if err != nil {
			continue
		}
		r.Summary = ""
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// Report liefert einen Test samt Messübersicht.
func (s *Service) Report(id string) (Result, error) {
	if !ValidID(id) {
		return Result{}, ErrBadID
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, id+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return Result{}, ErrNotFound
	}
	if err != nil {
		return Result{}, err
	}
	var r Result
	if err := json.Unmarshal(b, &r); err != nil {
		return Result{}, fmt.Errorf("%s: %w", id, err)
	}
	if r.ZipSize > 0 {
		if _, err := os.Stat(filepath.Join(s.Dir, id+".zip")); err != nil {
			r.ZipSize = 0
		}
	}
	return r, nil
}

// ZipPath ist die ZIP eines Tests.
func (s *Service) ZipPath(id string) (string, error) {
	if !ValidID(id) {
		return "", ErrBadID
	}
	p := filepath.Join(s.Dir, id+".zip")
	if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
		return "", ErrNotFound
	}
	return p, nil
}

// ZipName ist der Dateiname beim Herunterladen, wie ihn das Skript
// vergibt.
func ZipName(id string) string { return evidencePrefix + id + ".zip" }

// Claim liefert den privaten Claim-Link von Geekbench.
func (s *Service) Claim(id string) (string, error) {
	if !ValidID(id) {
		return "", ErrBadID
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, id+".claim"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	link := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
	if !strings.HasPrefix(link, "https://browser.geekbench.com/") {
		return "", ErrNotFound
	}
	return link, nil
}

// Delete entfernt einen Test.
func (s *Service) Delete(id string) error {
	if !ValidID(id) {
		return ErrBadID
	}
	if _, err := os.Stat(filepath.Join(s.Dir, id+".json")); errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	for _, ext := range []string{".zip", ".claim", ".json"} {
		if err := os.Remove(filepath.Join(s.Dir, id+ext)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
