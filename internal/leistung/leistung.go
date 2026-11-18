// Package leistung misst Speicher, Prozessor und Container-Logs des Servers
// und macht daraus Vorschläge mit Rückweg.
//
// Erst messen, dann ändern: HostDeck liest /proc und die Einstellungen von
// Docker, schlägt höchstens zwei Änderungen vor und macht jede nur auf
// deinen Wunsch:
//
//   - Container-Logs begrenzen: HostDeck trägt in /etc/docker/daemon.json eine
//     Größengrenze ein (3 Dateien zu je 10 MB je Container). Andere
//     Einstellungen bleiben, wie sie sind. HostDeck startet Docker dafür nicht
//     neu; die Grenze gilt für Container, die nach dem nächsten Neustart von
//     Docker neu angelegt werden. Logs leert HostDeck nie.
//   - Swap-Puffer: Hat der Server keinen Swap, legt HostDeck eine Datei
//     /swapfile-hostdeck an, damit Linux bei knappem Speicher nicht sofort einen
//     Prozess beendet. vm.swappiness bleibt, wie es ist.
//
// Beides lässt sich zurücknehmen, solange niemand die Dateien seitdem von
// Hand geändert hat. Speichergrenzen je Container schlägt HostDeck erst nach
// einer Woche Messung vor (Überwachung).
package leistung

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/altserver"
	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/ueberwachung"
)

// Fehler.
var (
	ErrChanged  = errors.New("seit der Vorschau hat sich etwas geändert; bitte die Vorschau neu öffnen")
	ErrNotFound = errors.New("diesen Vorschlag kennt HostDeck nicht")
	ErrEdited   = errors.New("die Datei wurde geändert, seit HostDeck sie geschrieben hat; HostDeck nimmt nichts zurück, damit deine Änderung bleibt")
	ErrNoRoom   = errors.New("ohne den Swap wäre zu wenig Arbeitsspeicher frei; HostDeck lässt ihn, bis wieder mehr frei ist")
)

// Runner führt ein Programm aus und liefert seine Ausgabe.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Docker ist, was Leistung von Docker liest (dockerapi.Ops).
type Docker interface {
	List(ctx context.Context) ([]dockerapi.OpsContainer, error)
	Inspect(ctx context.Context, id string) (dockerapi.Container, error)
}

// Vorschläge.
const (
	KeyLogs     = "logs"
	KeyLogsBack = "logs-zurueck"
	KeySwap     = "swap"
	KeySwapOff  = "swap-weg"
)

// Grenze für Container-Logs, wie sie HostDeck vorschlägt.
const (
	LogMaxSize = "10m"
	LogMaxFile = "3"
)

// Service misst und ändert.
type Service struct {
	Docker Docker
	Run    Runner
	Proc   string // Standard /proc
	Etc    string // Standard /etc; daemon.json und fstab
	// SwapFile ist die Swap-Datei, die HostDeck anlegt (Standard /swapfile-hostdeck).
	SwapFile string
	// Dir hält Vorher-Stände und den Zustand (/var/lib/hostdeck/leistung).
	Dir string
	// Disk misst Größe und freien Platz des Dateisystems von path.
	Disk func(path string) (total, free int64, err error)
	// Sample ist die Messdauer für den Prozessor (Standard 1 s).
	Sample time.Duration
	Now    func() time.Time
	// Old liefert, was HostDeck auf dem alten Server eingestellt hatte (nach
	// einem Umzug); nil: nichts.
	Old func() *altserver.Info
	// Limits sind die geltenden Grenzen der Überwachung (Steal); nil:
	// ueberwachung.DefaultLimits.
	Limits func() ueberwachung.Limits

	mu sync.Mutex
}

func (s *Service) limits() ueberwachung.Limits {
	if s.Limits != nil {
		return s.Limits()
	}
	return ueberwachung.DefaultLimits
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) proc() string {
	if s.Proc != "" {
		return s.Proc
	}
	return "/proc"
}

func (s *Service) etc() string {
	if s.Etc != "" {
		return s.Etc
	}
	return "/etc"
}

func (s *Service) swapFile() string {
	if s.SwapFile != "" {
		return s.SwapFile
	}
	return "/swapfile-hostdeck"
}

func (s *Service) daemonJSON() string { return filepath.Join(s.etc(), "docker", "daemon.json") }
func (s *Service) fstab() string      { return filepath.Join(s.etc(), "fstab") }

// Memory ist der Arbeitsspeicher.
type Memory struct {
	Total        int64 `json:"gesamt"`
	Available    int64 `json:"verfuegbar"`
	SwapTotal    int64 `json:"swap"`
	SwapFree     int64 `json:"swap_frei"`
	Swappiness   int   `json:"swappiness"`
	HostdeckSwap bool  `json:"swap_von_hostdeck"`
}

// CPU ist der Prozessor.
type CPU struct {
	Cores  int        `json:"kerne"`
	Load   [3]float64 `json:"last"` // 1, 5, 15 Minuten
	Busy   float64    `json:"belegt"`
	Steal  float64    `json:"steal"`
	IOWait float64    `json:"iowait"`
}

// LogSetup ist die Log-Einstellung von Docker.
type LogSetup struct {
	Known      bool   `json:"bekannt"` // Docker läuft und ließ sich lesen
	Driver     string `json:"treiber"`
	MaxSize    string `json:"max_size,omitempty"`
	MaxFile    string `json:"max_file,omitempty"`
	Limited    bool   `json:"begrenzt"` // Standard für neue Container
	ByHostdeck bool   `json:"von_hostdeck"`
	Total      int64  `json:"gesamt"`      // alle Log-Dateien zusammen
	Unlimited  int    `json:"ohne_grenze"` // Container ohne eigene Grenze
	Problem    string `json:"problem,omitempty"`
	NoDocker   bool   `json:"ohne_docker,omitempty"` // auf dem Server gibt es kein Docker
}

// ContainerLog ist ein Container mit Log und Speichergrenze.
type ContainerLog struct {
	Name     string `json:"name"`
	Project  string `json:"projekt,omitempty"`
	Running  bool   `json:"laeuft"`
	Driver   string `json:"treiber"`
	Size     int64  `json:"groesse"`
	Limited  bool   `json:"begrenzt"`
	MemLimit int64  `json:"speicher_grenze,omitempty"`
}

// Suggestion ist ein Vorschlag.
type Suggestion struct {
	Key     string `json:"schluessel"`
	Title   string `json:"titel"`
	Text    string `json:"text"`
	Gain    string `json:"gewinn"`
	Risk    string `json:"risiko"`
	Back    string `json:"rueckweg"`
	Undo    bool   `json:"zuruecknehmen,omitempty"` // nimmt eine Änderung von HostDeck zurück
	Problem string `json:"problem,omitempty"`       // geht gerade nicht, weil …
	Before  bool   `json:"wie_vorher,omitempty"`    // war so auf dem alten Server (Umzug)
}

// Overview ist der gemessene Stand.
type Overview struct {
	Memory      Memory         `json:"speicher"`
	CPU         CPU            `json:"prozessor"`
	Logs        LogSetup       `json:"logs"`
	Containers  []ContainerLog `json:"container"`
	Suggestions []Suggestion   `json:"vorschlaege"`
	Notes       []string       `json:"hinweise,omitempty"`
	Problem     string         `json:"problem,omitempty"`
	At          time.Time      `json:"stand_von"`
}

// Overview misst den Server. Das dauert etwa Sample (Prozessor).
func (s *Service) Overview(ctx context.Context) Overview {
	ov := Overview{At: s.now(), Containers: []ContainerLog{}, Suggestions: []Suggestion{}}
	ov.Memory = s.memory()
	ov.CPU = s.cpu(ctx)
	st := s.state()
	ov.Memory.HostdeckSwap = st.Swap != nil
	ov.Logs, ov.Containers = s.logs(ctx, st)
	ov.Suggestions = s.suggest(ov, st)
	ov.Notes = append(ov.Notes, s.stealNote(ov.CPU.Steal)...)
	return ov
}

// stealNote: dieselbe Grenze wie Kachel und Regel „Steal hoch“.
func (s *Service) stealNote(steal float64) []string {
	if steal <= s.limits().StealWarn {
		return nil
	}
	return []string{"Steal liegt bei " + pct(steal) + ": Der Hoster teilt die Hardware gerade mit vielen anderen. Das behebt keine Einstellung; hält es an, hilft nur ein anderer Tarif oder Anbieter."}
}

func pct(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', 1, 64), ".", ",", 1) + " %"
}

// memInfo liest /proc/meminfo in Byte.
func (s *Service) memInfo() map[string]int64 {
	m := map[string]int64{}
	b, err := os.ReadFile(filepath.Join(s.proc(), "meminfo"))
	if err != nil {
		return m
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			continue
		}
		if len(f) > 1 && f[1] == "kB" {
			n *= 1024
		}
		m[k] = n
	}
	return m
}

// Mem liefert Arbeitsspeicher gesamt und verfügbar (für die Hinweise).
func (s *Service) Mem() (total, avail int64) {
	m := s.memInfo()
	return m["MemTotal"], m["MemAvailable"]
}

func (s *Service) memory() Memory {
	m := s.memInfo()
	mem := Memory{Total: m["MemTotal"], Available: m["MemAvailable"], SwapTotal: m["SwapTotal"], SwapFree: m["SwapFree"], Swappiness: -1}
	if b, err := os.ReadFile(filepath.Join(s.proc(), "sys", "vm", "swappiness")); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			mem.Swappiness = n
		}
	}
	return mem
}

// cpuTimes liest die Summenzeile aus /proc/stat.
func (s *Service) cpuTimes() (vals []uint64, cores int) {
	b, err := os.ReadFile(filepath.Join(s.proc(), "stat"))
	if err != nil {
		return nil, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch {
		case f[0] == "cpu":
			for _, x := range f[1:] {
				n, _ := strconv.ParseUint(x, 10, 64)
				vals = append(vals, n)
			}
		case strings.HasPrefix(f[0], "cpu"):
			cores++
		}
	}
	return vals, cores
}

func (s *Service) cpu(ctx context.Context) CPU {
	var c CPU
	if b, err := os.ReadFile(filepath.Join(s.proc(), "loadavg")); err == nil {
		f := strings.Fields(string(b))
		for i := 0; i < 3 && i < len(f); i++ {
			c.Load[i], _ = strconv.ParseFloat(f[i], 64)
		}
	}
	a, cores := s.cpuTimes()
	c.Cores = cores
	wait := s.Sample
	if wait == 0 {
		wait = time.Second
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return c
	case <-t.C:
	}
	b, _ := s.cpuTimes()
	if len(a) < 8 || len(b) < 8 {
		return c
	}
	// user nice system idle iowait irq softirq steal
	var total float64
	d := make([]float64, 8)
	for i := 0; i < 8; i++ {
		if b[i] >= a[i] {
			d[i] = float64(b[i] - a[i])
		}
		total += d[i]
	}
	if total == 0 {
		return c
	}
	c.Busy = 100 * (total - d[3] - d[4]) / total
	c.IOWait = 100 * d[4] / total
	c.Steal = 100 * d[7] / total
	return c
}

// daemonConfig ist /etc/docker/daemon.json.
type daemonConfig struct {
	raw    []byte
	exists bool
	cfg    map[string]json.RawMessage
	err    error
}

// readDaemonAt liest daemon.json; fehlt sie, ist sie leer.
func readDaemonAt(path string) daemonConfig {
	var d daemonConfig
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		d.cfg = map[string]json.RawMessage{}
		return d
	}
	if err != nil {
		d.err = err
		return d
	}
	d.raw, d.exists = b, true
	if len(bytes.TrimSpace(b)) == 0 {
		d.cfg = map[string]json.RawMessage{}
		return d
	}
	if err := json.Unmarshal(b, &d.cfg); err != nil {
		d.err = errors.New("daemon.json ist kein gültiges JSON")
		d.cfg = nil
	}
	return d
}

func (d daemonConfig) driver() string {
	var v string
	_ = json.Unmarshal(d.cfg["log-driver"], &v)
	return v
}

func (d daemonConfig) opts() map[string]string {
	m := map[string]string{}
	_ = json.Unmarshal(d.cfg["log-opts"], &m)
	return m
}

// limitedDriver: Dieser Treiber begrenzt von sich aus oder schreibt nicht
// auf die Platte des Servers.
func limitedDriver(drv string, opts map[string]string) bool {
	switch drv {
	case "", "json-file":
		return opts["max-size"] != ""
	case "local":
		return true // Standard 5 Dateien zu je 20 MB
	}
	return true // journald, syslog, none und andere: nicht Sache von Docker
}

func (s *Service) logs(ctx context.Context, st state) (LogSetup, []ContainerLog) {
	ls := LogSetup{}
	list := []ContainerLog{}
	d, _, _ := s.daemonFile()
	if d.err != nil {
		ls.Problem = d.err.Error()
	} else {
		ls.Driver = d.driver()
		o := d.opts()
		ls.MaxSize, ls.MaxFile = o["max-size"], o["max-file"]
		ls.Limited = limitedDriver(ls.Driver, o)
		ls.ByHostdeck = st.Logs != nil && hashOf(d.raw) == st.Logs.Written
	}
	if ls.Driver == "" {
		ls.Driver = "json-file"
	}
	if s.Docker == nil {
		return ls, list
	}
	cs, err := s.Docker.List(ctx)
	if err != nil {
		if dockerapi.Missing(err) {
			ls.NoDocker, ls.Problem = true, "Auf diesem Server ist Docker nicht installiert."
		} else if ls.Problem == "" {
			ls.Problem = "Docker antwortet nicht: " + err.Error()
		}
		return ls, list
	}
	ls.Known = true
	for _, c := range cs {
		info, err := s.Docker.Inspect(ctx, c.ID)
		if err != nil {
			continue
		}
		lc := info.HostConfig.LogConfig
		cl := ContainerLog{
			Name:     strings.TrimPrefix(info.Name, "/"),
			Project:  info.Config.Labels["com.docker.compose.project"],
			Running:  info.State.Running,
			Driver:   lc.Type,
			Limited:  limitedDriver(lc.Type, lc.Config),
			MemLimit: info.HostConfig.Memory,
		}
		if cl.Name == "" {
			cl.Name = c.Name()
		}
		if info.LogPath != "" {
			cl.Size = logSize(info.LogPath)
		}
		ls.Total += cl.Size
		if !cl.Limited {
			ls.Unlimited++
		}
		list = append(list, cl)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Size != list[j].Size {
			return list[i].Size > list[j].Size
		}
		return list[i].Name < list[j].Name
	})
	return ls, list
}

// logSize zählt die Log-Datei samt gedrehten Teilen (.1, .2 …).
func logSize(path string) int64 {
	var n int64
	if fi, err := os.Stat(path); err == nil {
		n += fi.Size()
	}
	more, _ := filepath.Glob(path + ".*")
	for _, p := range more {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			n += fi.Size()
		}
	}
	return n
}

func hashOf(b []byte) string {
	if b == nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Bytes schreibt eine Größe wie der Leitstand (1,2 GB).
func Bytes(n int64) string {
	const unit = 1000
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	f := float64(n)
	for _, u := range []string{"kB", "MB", "GB", "TB"} {
		f /= unit
		if f < unit {
			p := 1
			if f >= 100 {
				p = 0
			}
			return strings.Replace(strconv.FormatFloat(f, 'f', p, 64), ".", ",", 1) + " " + u
		}
	}
	return strconv.FormatInt(n, 10) + " B"
}

// gib ist eine Größe in GiB für Swap und Speicher (2 GB).
func gib(n int64) string {
	f := float64(n) / (1 << 30)
	p := 1
	if f == float64(int64(f)) {
		p = 0
	}
	return strings.Replace(strconv.FormatFloat(f, 'f', p, 64), ".", ",", 1) + " GB"
}

func (s *Service) suggest(ov Overview, st state) []Suggestion {
	out := []Suggestion{}
	ls := ov.Logs
	switch {
	case ls.NoDocker:
		// Ohne Docker gibt es keine Container-Logs zu begrenzen.
	case ls.ByHostdeck:
		out = append(out, Suggestion{Key: KeyLogsBack, Title: "Log-Grenze zurücknehmen", Undo: true,
			Text: "HostDeck hat in daemon.json eingetragen: höchstens " + LogMaxFile + " Dateien zu je 10 MB pro Container. Zurücknehmen legt die Datei wieder so hin, wie sie vorher war.",
			Gain: "wie vorher", Risk: "gering", Back: "Die Grenze lässt sich danach wieder eintragen."})
	case ls.Problem != "":
		out = append(out, Suggestion{Key: KeyLogs, Title: "Container-Logs begrenzen", Problem: ls.Problem + "; HostDeck ändert sie nicht.",
			Text: "Docker kürzt Logs von sich aus nie.", Gain: Bytes(ls.Total), Risk: "gering"})
	case !ls.Limited:
		sg := Suggestion{Key: KeyLogs, Title: "Container-Logs begrenzen",
			Text: "Docker kürzt Logs von sich aus nie. Vorschlag: höchstens " + LogMaxFile + " Dateien zu je 10 MB pro Container. HostDeck trägt das in /etc/docker/daemon.json ein und lässt alles andere dort, wie es ist. Logs leert HostDeck nicht.",
			Gain: Bytes(ls.Total), Risk: "gering",
			Back: "Zurücknehmen legt daemon.json wieder so hin, wie sie vorher war."}
		if !ls.Known {
			sg.Problem = "Docker läuft nicht oder antwortet nicht."
		}
		out = append(out, sg)
	}
	m := ov.Memory
	switch {
	case st.Swap != nil:
		out = append(out, Suggestion{Key: KeySwapOff, Title: "Swap-Puffer entfernen", Undo: true,
			Text: "HostDeck hat " + st.Swap.File + " mit " + gib(st.Swap.Size) + " als Swap angelegt. Entfernen schaltet ihn ab, nimmt die Zeile aus /etc/fstab und löscht die Datei.",
			Gain: gib(st.Swap.Size), Risk: "gering", Back: "Der Puffer lässt sich danach wieder anlegen."})
	case m.Total > 0 && m.SwapTotal == 0:
		size := swapSize(m.Total)
		sg := Suggestion{Key: KeySwap, Title: "Kleine Swap-Datei als Puffer",
			Text: "Der Server hat keinen Swap. Wird der Speicher knapp, beendet Linux sofort einen Prozess, im schlimmsten Fall eine Datenbank. Ein Puffer von " + gib(size) + " gibt Luft. vm.swappiness bleibt, wie es ist.",
			Gain: "Stabil", Risk: "gering", Back: "Entfernen schaltet den Swap ab und löscht die Datei."}
		if why := s.swapProblem(size); why != "" {
			sg.Problem = why
		}
		out = append(out, sg)
	}
	if s.Old != nil {
		if old := s.Old(); old != nil {
			for i := range out {
				sg := &out[i]
				if (sg.Key == KeyLogs && old.Logs) || (sg.Key == KeySwap && old.Swap > 0) {
					sg.Before = true
					sg.Text = "Auf dem alten Server hattest du das mit HostDeck eingestellt; hier fehlt es noch. " + sg.Text
				}
			}
		}
	}
	return out
}

// swapSize: 2 GB, auf kleinen Servern 1 GB.
func swapSize(total int64) int64 {
	if total < 2<<30 {
		return 1 << 30
	}
	return 2 << 30
}

// Remains nennt, was HostDeck unter Leistung geändert hat und beim
// Deinstallieren bleibt (zurücknehmen geht vorher im Leitstand).
func (s *Service) Remains() []string {
	st := s.state()
	var out []string
	if st.Logs != nil {
		out = append(out, "Log-Grenze für Docker in "+st.Logs.Path)
	}
	if st.Swap != nil {
		out = append(out, "Swap-Datei "+st.Swap.File+" ("+gib(st.Swap.Size)+", in /etc/fstab)")
	}
	return out
}
