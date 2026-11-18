// Package betrieb steuert im Leitstand die Container des Servers: ansehen,
// Logs lesen, starten, stoppen, neu starten.
//
// Die Regeln, die hier sitzen:
//   - Nur diese drei Aktionen; mehr kann der Docker-Zugang (dockerapi.Ops)
//     gar nicht.
//   - Erst zeigen, dann tun: Plan sagt vorher genau, welche Container es
//     trifft und was danach gilt; Do führt nur einen solchen Plan aus.
//   - Container, die ein Panel wie Calagopus/Wings anlegt, steuert HostDeck nie;
//     sie stehen nur in der Liste.
//   - Was du stoppst, bleibt gestoppt: HostDeck merkt sich, welche Container du
//     angehalten hast, und ein Neustart des Projekts lässt sie aus.
//
// Laufsperre, Bestätigung mit Passwort und Protokoll macht der Dienst.
package betrieb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/officialtaxz2/hostdeck/internal/discover"
	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/hostfs"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// Docker ist der Teil von dockerapi.Ops, den der Betrieb braucht.
// PolicySetter kann die Neustart-Regel eines Containers setzen
// (dockerapi.Ops). Kann der Zugang das nicht, bleibt die Regel.
type PolicySetter interface {
	SetRestartPolicy(ctx context.Context, id, policy string) error
}

type Docker interface {
	List(ctx context.Context) ([]dockerapi.OpsContainer, error)
	Inspect(ctx context.Context, id string) (dockerapi.Container, error)
	Stats(ctx context.Context, id string) (dockerapi.Usage, error)
	Logs(ctx context.Context, id string, tail int, tty bool, max int64) ([]byte, error)
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string, timeout time.Duration) error
	Restart(ctx context.Context, id string, timeout time.Duration) error
}

const (
	labelProject   = "com.docker.compose.project"
	labelService   = "com.docker.compose.service"
	labelOneoff    = "com.docker.compose.oneoff"
	labelDependsOn = "com.docker.compose.depends_on"
)

// Container ist ein Container, wie ihn der Leitstand zeigt.
type Container struct {
	Name          string     `json:"name"`
	Project       string     `json:"projekt,omitempty"`
	Service       string     `json:"dienst,omitempty"`
	Image         string     `json:"image"`
	State         string     `json:"zustand"` // running, exited, restarting, paused, created, dead
	Running       bool       `json:"laeuft"`
	ExitCode      int        `json:"exit_code,omitempty"`
	OOM           bool       `json:"oom,omitempty"`        // zuletzt vom Kernel beendet: Speicher voll
	Health        string     `json:"gesundheit,omitempty"` // starting, healthy, unhealthy; nur mit Healthcheck
	Restarts      int        `json:"neustarts"`
	Since         *time.Time `json:"seit,omitempty"` // läuft seit oder gestoppt seit
	RestartPolicy string     `json:"neustart_regel,omitempty"`
	ManagedBy     string     `json:"verwaltet_von,omitempty"` // Name des Containers, der ihn anlegt
	CPU           *float64   `json:"cpu,omitempty"`           // Anteil am ganzen Server in Prozent (alle Kerne = 100 %)
	Mem           int64      `json:"speicher,omitempty"`
	MemLimit      int64      `json:"speicher_grenze,omitempty"`
	StoppedByYou  *time.Time `json:"von_dir_gestoppt,omitempty"`
	// StoppedByMove: Der letzte Lauf des Umzugstags hat ihn gestoppt; er
	// bleibt absichtlich aus, bis die Domains umgestellt sind oder du den
	// Rückweg nimmst.
	StoppedByMove bool `json:"letzter_lauf,omitempty"`
	// Job: ein Container, der einmal läuft und sich dann sauber beendet
	// (Code 0, Neustart-Regel „no“ oder „on-failure“), etwa ein Aufräum-Job. Dass er aus
	// ist, ist sein normaler Zustand.
	Job bool `json:"job,omitempty"`
	// Class, Word, Level und Why: der Zustand in Worten, die eine Quelle
	// für alle Anzeigen (zustand.go). Why nennt einen Grund, etwa den Code
	// eines Absturzes.
	Class string `json:"klasse"`
	Word  string `json:"wort"`
	Level string `json:"stufe"`
	Why   string `json:"grund,omitempty"`
	// HostPorts: TCP-Ports, die Docker für ihn auf dem Host veröffentlicht
	// (laut Einstellung, also auch, wenn er gestoppt ist). Daraus sieht der
	// Zustand der Domains, welcher Container hinter einer Weiterleitung steht.
	HostPorts []int `json:"-"`
	// HostNetwork: läuft im Netz des Hosts, Ports sind dann nicht eingetragen.
	HostNetwork bool `json:"-"`

	tty  bool
	deps []string // Dienste, von denen er abhängt
	// started: letzter Start laut Docker, auch bei einem gestoppten
	// Container (für die Merkung „von dir gestoppt“, R2).
	started *time.Time
	order   int
}

// Measured ist ein Messwert der Überwachung: Prozessor als Anteil am ganzen
// Server (alle Kerne = 100 %), Speicher in Byte ohne Seiten-Cache (wie
// docker stats) und die eingestellte Grenze (0: keine).
type Measured struct {
	CPU      float64
	Mem      int64
	MemLimit int64
}

// Snapshot ist der Stand aller Container zu einem Zeitpunkt.
type Snapshot struct {
	Time       time.Time   `json:"zeit"`
	Containers []Container `json:"container"`
	// UsagePending: Für laufende Container fehlen noch Messwerte, HostDeck misst
	// gerade. Die Website fragt dann bald noch einmal.
	UsagePending bool `json:"messung_laeuft,omitempty"`
	// Groups: Zustand je Projekt, Container allein und verwaltete Server;
	// Sum: alle eigenen Container zusammen (Container-Kachel).
	Groups []Group `json:"gruppen"`
	Sum    Group   `json:"summe"`
}

// Service ist der Betrieb.
type Service struct {
	Docker      Docker
	StoppedFile string // merkt sich, was du gestoppt hast; leer: nur im Speicher
	// MoveStopped nennt die Container, die der letzte Lauf eines Umzugs aus
	// gelassen hat (nach Namen); nil: keine.
	MoveStopped func() map[string]bool
	StopTimeout time.Duration // so lange darf ein Container zum Anhalten brauchen; 0: wie eingestellt
	Now         func() time.Time
	// Cores: Kerne des Servers; docker stats rechnet in Prozent eines
	// Kerns, HostDeck zeigt den Anteil am ganzen Server. nil: runtime.NumCPU.
	Cores func() int
	// Measured: Prozessor und Speicher je Container aus der letzten Messung
	// der Überwachung (nach Namen), wenn sie frisch ist; nil oder leer: HostDeck
	// misst mit docker stats. So zeigen Projekte, Übersicht, Leistung und
	// Überwachung für „jetzt“ dieselbe Zahl aus einer Quelle (Datenprüfung
	// H2); nur wo die Überwachung je Container nichts misst (cgroup v1),
	// misst docker stats.
	Measured func() map[string]Measured

	mu      sync.Mutex
	stopped map[string]time.Time // nur ohne StoppedFile

	use usageCache
}

// Messwerte für Prozessor und Speicher. Docker braucht für jeden Wert gut
// eine Sekunde (es misst zweimal); deshalb misst HostDeck im Hintergrund, und die
// Liste nimmt den letzten Stand. So steht die Liste sofort da.
const (
	usageMaxAge = 5 * time.Second        // älter: neu messen
	usageWait   = 300 * time.Millisecond // so lange wartet die Liste auf eine laufende Messung
)

type usageCache struct {
	mu   sync.Mutex
	at   time.Time
	vals map[string]dockerapi.Usage // nach Container-ID
	busy chan struct{}              // offen, solange gemessen wird
}

// usage gibt die letzten Messwerte zurück und stößt, wenn sie alt sind, eine
// neue Messung an. Läuft eine, wartet es kurz auf sie.
func (s *Service) usage(ctx context.Context, running []string) (map[string]dockerapi.Usage, bool) {
	u := &s.use
	u.mu.Lock()
	if u.busy == nil && s.now().Sub(u.at) > usageMaxAge && len(running) > 0 {
		done := make(chan struct{})
		u.busy = done
		ids := append([]string(nil), running...)
		go s.measure(ids, done)
	}
	busy := u.busy
	u.mu.Unlock()
	if busy != nil {
		t := time.NewTimer(usageWait)
		select {
		case <-busy:
		case <-t.C:
		case <-ctx.Done():
		}
		t.Stop()
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	pending := false
	for _, id := range running {
		if _, ok := u.vals[id]; !ok && u.busy != nil {
			pending = true
		}
	}
	return u.vals, pending
}

func (s *Service) measure(ids []string, done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	vals := make(map[string]dockerapi.Usage, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			sctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			if v, err := s.Docker.Stats(sctx, id); err == nil {
				mu.Lock()
				vals[id] = v
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	u := &s.use
	u.mu.Lock()
	u.vals, u.at, u.busy = vals, s.now(), nil
	u.mu.Unlock()
	close(done)
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// List liest alle Container mit Zustand und, für laufende, einem Messwert
// für Prozessor und Speicher.
func (s *Service) List(ctx context.Context) (Snapshot, error) {
	return s.list(ctx, true)
}

// States ist List ohne Verbrauch: schneller, für die Überwachung.
func (s *Service) States(ctx context.Context) (Snapshot, error) {
	return s.list(ctx, false)
}

func (s *Service) list(ctx context.Context, withUsage bool) (Snapshot, error) {
	list, err := s.Docker.List(ctx)
	if dockerapi.Missing(err) {
		// Ohne Docker keine Container; das ist kein Fehler.
		list, err = nil, nil
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("Docker antwortet nicht: %w", err)
	}
	out := make([]Container, len(list))
	stopped := s.loadStopped()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i, c := range list {
		out[i] = Container{
			Name: c.Name(), Image: c.Image, State: c.State, Running: c.State == "running" || c.State == "restarting",
		}
		if !strings.EqualFold(c.Labels[labelOneoff], "true") {
			out[i].Project = c.Labels[labelProject]
			out[i].Service = c.Labels[labelService]
		}
		out[i].deps = dependsOn(c.Labels[labelDependsOn])
		wg.Add(1)
		go func(x *Container, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ictx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if v, err := s.Docker.Inspect(ictx, id); err == nil {
				x.Restarts = v.RestartCount
				x.RestartPolicy = v.HostConfig.RestartPolicy.Name
				x.ExitCode = v.State.ExitCode
				x.OOM = v.State.OOMKilled
				if v.State.Health != nil {
					x.Health = v.State.Health.Status
				}
				x.tty = v.Config.Tty
				x.HostNetwork = v.HostConfig.NetworkMode == "host"
				x.HostPorts = hostPorts(v.HostConfig.PortBindings)
				if t, err := time.Parse(time.RFC3339Nano, v.State.StartedAt); err == nil && t.Year() > 1 {
					x.started = &t
				}
				at := v.State.FinishedAt
				if v.State.Running {
					at = v.State.StartedAt
				}
				if t, err := time.Parse(time.RFC3339Nano, at); err == nil && t.Year() > 1 {
					x.Since = &t
				}
				pol := v.HostConfig.RestartPolicy.Name
				// Mit „always“ oder „unless-stopped“ startet Docker einen
				// Container nach Code 0 sofort wieder; ist er trotzdem aus,
				// hat ihn jemand gestoppt.
				x.Job = x.State == "exited" && v.State.ExitCode == 0 && (pol == "" || pol == "no" || pol == "on-failure")
			}
		}(&out[i], c.ID)
	}
	wg.Wait()
	snap := Snapshot{}
	if withUsage {
		var meas map[string]Measured
		if s.Measured != nil {
			meas = s.Measured()
		}
		// Was die Überwachung gerade gemessen hat, gilt; docker stats nur für
		// die übrigen laufenden Container.
		var rest []string
		for i, c := range list {
			if c.State != "running" {
				continue
			}
			if m, ok := meas[out[i].Name]; ok {
				cpu := m.CPU
				out[i].CPU = &cpu
				out[i].Mem, out[i].MemLimit = m.Mem, m.MemLimit
				continue
			}
			rest = append(rest, c.ID)
		}
		if len(rest) > 0 {
			vals, pending := s.usage(ctx, rest)
			snap.UsagePending = pending
			for i, c := range list {
				if u, ok := vals[c.ID]; ok && c.State == "running" && out[i].CPU == nil {
					cpu := u.CPU
					if n := s.cores(); n > 1 {
						cpu /= float64(n)
					}
					out[i].CPU = &cpu
					out[i].Mem, out[i].MemLimit = u.Mem, u.MemLimit
				}
			}
		}
	}
	managers := managedBy(list)
	var moved map[string]bool
	if s.MoveStopped != nil {
		moved = s.MoveStopped()
	}
	var stale []string
	for i := range out {
		if moved[out[i].Name] && !out[i].Running {
			out[i].StoppedByMove = true
			out[i].Job = false
		}
		out[i].ManagedBy = managers[out[i].Name]
		t, ok := stopped[out[i].Name]
		// Er läuft wieder, gestartet nach deinem Stopp von außerhalb von HostDeck
		// (docker compose up, ein anderes Werkzeug): Die Merkung gilt nicht
		// mehr. Sonst hieße ein späterer Absturz „von dir gestoppt“, und
		// seine Ausfälle zählten als Pause. Nur ein laufender zählt:
		// Bei einem gestoppten kann der Zeitpunkt der Merkung älter sein
		// als sein letzter Start, etwa nach einem Umzug. Der Vergleich mit
		// dem Start schützt vor einer Liste, die Docker kurz vor deinem
		// Stopp geliefert hat.
		if ok && out[i].Running && out[i].started != nil && out[i].started.After(t.Add(stoppedSlack)) {
			stale = append(stale, out[i].Name)
			continue
		}
		if ok && !out[i].Running {
			t := t
			out[i].StoppedByYou = &t
			out[i].Job = false
		}
	}
	for _, n := range stale {
		s.remember(n, false)
	}
	order(out)
	snap.Time, snap.Containers = s.now(), out
	for i := range out {
		out[i].classify(snap.Time)
	}
	snap.Groups, snap.Sum = Groups(out), Sum(out)
	return snap, nil
}

// managedBy erkennt mit derselben Regel wie die Bestandsaufnahme, welche
// Container ein anderer anlegt. Nur Container ohne Compose-Projekt kommen
// dafür in Frage.
func managedBy(list []dockerapi.OpsContainer) map[string]string {
	all := make([]inventory.Container, len(list))
	for i, c := range list {
		ic := inventory.Container{Name: c.Name()}
		for _, m := range c.Mounts {
			src := m.Source
			if m.Type == "bind" {
				src = hostfs.Clean(src)
				if discover.IsDockerSocket(src) {
					ic.DockerSocket = true
				}
			}
			ic.Mounts = append(ic.Mounts, inventory.Mount{Type: m.Type, Source: src, Destination: m.Destination, RW: m.RW})
		}
		all[i] = ic
	}
	out := map[string]string{}
	for i, c := range list {
		if c.Labels[labelProject] != "" && !strings.EqualFold(c.Labels[labelOneoff], "true") {
			continue
		}
		if j, _ := discover.ManagerOf(all[i], all); j >= 0 {
			out[all[i].Name] = all[j].Name
		}
	}
	return out
}

// hostPorts liest die veröffentlichten TCP-Ports aus den Einstellungen des
// Containers, sortiert und ohne doppelte.
func hostPorts(b map[string][]dockerapi.PortBinding) []int {
	seen := map[int]bool{}
	var out []int
	for spec, list := range b {
		if !strings.HasSuffix(spec, "/tcp") && strings.Contains(spec, "/") {
			continue
		}
		for _, x := range list {
			if p, err := strconv.Atoi(x.HostPort); err == nil && p > 0 && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Ints(out)
	return out
}

// dependsOn liest das Compose-Label depends_on, etwa
// "db:service_healthy:false,redis:service_started:true".
func dependsOn(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		name, _, _ := strings.Cut(strings.TrimSpace(part), ":")
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// order sortiert nach Projekt und darin so, dass jeder Dienst nach denen
// kommt, von denen er abhängt (Startreihenfolge). Container ohne Projekt
// stehen am Ende nach Namen.
func order(cs []Container) {
	byProject := map[string][]*Container{}
	for i := range cs {
		byProject[cs[i].Project] = append(byProject[cs[i].Project], &cs[i])
	}
	for _, list := range byProject {
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		depth := map[string]int{}
		var walk func(svc string, seen map[string]bool) int
		bySvc := map[string]*Container{}
		for _, c := range list {
			if c.Service != "" {
				bySvc[c.Service] = c
			}
		}
		walk = func(svc string, seen map[string]bool) int {
			if d, ok := depth[svc]; ok {
				return d
			}
			c := bySvc[svc]
			if c == nil || seen[svc] {
				return 0
			}
			seen[svc] = true
			d := 0
			for _, dep := range c.deps {
				if x := walk(dep, seen) + 1; x > d {
					d = x
				}
			}
			depth[svc] = d
			return d
		}
		for _, c := range list {
			c.order = walk(c.Service, map[string]bool{})
		}
	}
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if (a.Project == "") != (b.Project == "") {
			return b.Project == ""
		}
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		if a.order != b.order {
			return a.order < b.order
		}
		return a.Name < b.Name
	})
}

// ---------- Aktionen ----------

// Aktionen, die der Betrieb kennt.
const (
	Start   = "starten"
	Stop    = "stoppen"
	Restart = "neustarten"
)

// Fehler, die der Leitstand als Text zeigt.
var (
	ErrBadAction = errors.New("Diese Aktion kennt HostDeck nicht. Möglich sind Starten, Stoppen und Neustarten.")
	ErrBadTarget = errors.New("Ziel nicht lesbar; erwartet ist projekt:<Name> oder container:<Name>.")
	ErrNotFound  = errors.New("Dieses Projekt oder diesen Container gibt es auf dem Server nicht (mehr).")
)

// Skip ist ein Container, den eine Aktion auslässt, mit Grund.
type Skip struct {
	Name   string `json:"name"`
	Reason string `json:"grund"`
}

// Plan sagt vor einer Aktion, was passiert.
type Plan struct {
	Action     string   `json:"aktion"`
	Target     string   `json:"ziel"`
	Label      string   `json:"titel"`     // etwa „Projekt pensum“
	Containers []string `json:"container"` // in dieser Reihenfolge
	Skipped    []Skip   `json:"ausgelassen,omitempty"`
	Effect     string   `json:"auswirkung"` // was danach gilt
	Back       string   `json:"rueckweg"`   // wie du zurückkommst
	Notes      []string `json:"hinweise,omitempty"`
	// Problem: Die Aktion geht so nicht; Containers ist dann leer.
	Problem string `json:"problem,omitempty"`

	always map[string]bool // Container mit der Neustart-Regel „always“
}

// Step ist das Ergebnis für einen Container.
type Step struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"fehler,omitempty"`
}

// Result ist das Ergebnis einer Aktion.
type Result struct {
	Plan  Plan   `json:"plan"`
	Steps []Step `json:"schritte"`
	OK    bool   `json:"ok"`
}

// Text fasst das Ergebnis in einem Satz zusammen, für Protokoll und Seite.
func (r Result) Text() string {
	verb := map[string]string{Start: "gestartet", Stop: "gestoppt", Restart: "neu gestartet"}[r.Plan.Action]
	var bad []string
	for _, s := range r.Steps {
		if !s.OK {
			bad = append(bad, s.Name)
		}
	}
	if len(bad) == 0 {
		return fmt.Sprintf("%s %s (%s)", r.Plan.Label, verb, strings.Join(r.Plan.Containers, ", "))
	}
	return fmt.Sprintf("%s: nicht %s: %s", r.Plan.Label, verb, strings.Join(bad, ", "))
}

// ParseTarget zerlegt „projekt:<Name>“ oder „container:<Name>“.
func ParseTarget(t string) (kind, name string, err error) {
	kind, name, ok := strings.Cut(t, ":")
	if !ok || (kind != "projekt" && kind != "container") || !validName.MatchString(name) {
		return "", "", ErrBadTarget
	}
	return kind, name, nil
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// Plan berechnet, was eine Aktion auf ein Ziel täte. Er ändert nichts.
func (s *Service) Plan(ctx context.Context, target, action string) (Plan, error) {
	if action != Start && action != Stop && action != Restart {
		return Plan{}, ErrBadAction
	}
	kind, name, err := ParseTarget(target)
	if err != nil {
		return Plan{}, err
	}
	snap, err := s.list(ctx, false)
	if err != nil {
		return Plan{}, err
	}
	_, durable := s.Docker.(PolicySetter)
	return planFor(snap.Containers, kind, name, target, action, durable)
}

func planFor(all []Container, kind, name, target, action string, durable bool) (Plan, error) {
	p := Plan{Action: action, Target: target, Containers: []string{}}
	var pick []Container
	if kind == "projekt" {
		p.Label = "Projekt " + name
		for _, c := range all {
			if c.Project == name {
				pick = append(pick, c)
			}
		}
	} else {
		p.Label = "Container " + name
		for _, c := range all {
			if c.Name == name {
				pick = append(pick, c)
			}
		}
	}
	if len(pick) == 0 {
		return Plan{}, ErrNotFound
	}
	var always []string
	for _, c := range pick {
		switch {
		case c.ManagedBy != "":
			p.Skipped = append(p.Skipped, Skip{c.Name, "steuert das Panel in " + c.ManagedBy})
		case action == Start && c.Running:
			p.Skipped = append(p.Skipped, Skip{c.Name, "läuft schon"})
		case (action == Start || action == Restart) && c.Job && kind == "projekt":
			p.Skipped = append(p.Skipped, Skip{c.Name, "einmaliger Job, ist erledigt"})
		case action == Stop && !c.Running:
			p.Skipped = append(p.Skipped, Skip{c.Name, "ist schon gestoppt"})
		case action == Restart && !c.Running && kind == "projekt":
			why := "ist gestoppt und bleibt es"
			if c.StoppedByYou != nil {
				why = "hast du gestoppt; bleibt gestoppt"
			}
			p.Skipped = append(p.Skipped, Skip{c.Name, why})
		default:
			p.Containers = append(p.Containers, c.Name)
			if action == Start && c.Job {
				p.Notes = append(p.Notes, c.Name+" ist ein einmaliger Job. Er tut beim Starten seine Arbeit noch einmal, ein Aufräum-Job löscht zum Beispiel wieder. Starte ihn nur, wenn du das willst.")
			}
			if action == Start && c.RestartPolicy == "unless-stopped" && c.StoppedByYou != nil {
				p.Notes = append(p.Notes, c.Name+" behält die Neustart-Regel „unless-stopped“, auch wenn HostDeck sie beim Stoppen von „always“ umgestellt hat. Im Alltag verhält sie sich gleich; „docker compose up“ setzt wieder die Regel aus der Compose-Datei.")
			}
			if action == Stop && c.RestartPolicy == "always" {
				always = append(always, c.Name)
				if p.always == nil {
					p.always = map[string]bool{}
				}
				p.always[c.Name] = true
			}
		}
	}
	if action == Stop {
		// Anhalten in umgekehrter Startreihenfolge: erst die App, dann die
		// Datenbank.
		for i, j := 0, len(p.Containers)-1; i < j; i, j = i+1, j-1 {
			p.Containers[i], p.Containers[j] = p.Containers[j], p.Containers[i]
		}
	}
	n := len(p.Containers)
	switch {
	case n == 0 && len(pick) == 1 && pick[0].ManagedBy != "":
		p.Problem = "Diesen Container legt das Panel in " + pick[0].ManagedBy + " an. Starten und Stoppen geht nur dort, sonst kommen sich HostDeck und das Panel in die Quere."
	case n == 0 && action == Start:
		p.Problem = "Hier läuft schon alles."
	case n == 0 && action == Stop:
		p.Problem = "Hier ist schon alles gestoppt."
	case n == 0:
		p.Problem = "Hier läuft nichts, was neu starten könnte. Nimm stattdessen Starten."
	}
	if p.Problem != "" {
		p.Containers = []string{}
		return p, nil
	}
	cs := plural(n, "Container", "Container")
	one := n == 1
	pick2 := func(a, b string) string {
		if one {
			return a
		}
		return b
	}
	switch action {
	case Start:
		p.Effect = cs + pick2(" startet.", " starten in dieser Reihenfolge. Was von anderen abhängt, startet nach ihnen.")
		p.Back = "Stoppen."
		if one && pick[0].Job && len(pick) == 1 {
			p.Effect = "Der Job läuft einmal und beendet sich danach von selbst."
			p.Back = "Nicht nötig."
		}
	case Stop:
		p.Effect = cs + pick2(" hält an", " halten an") + "; was daran hängt, ist so lange nicht erreichbar. HostDeck merkt sich, dass du " + pick2("ihn", "sie") +
			" gestoppt hast: Ein Backup oder ein Neustart des Projekts startet " + pick2("ihn", "sie") + " nicht wieder."
		p.Back = "Starten. Daten und Einstellungen bleiben, wie sie sind."
	case Restart:
		p.Effect = cs + pick2(" startet neu und ist dabei einige Sekunden nicht erreichbar.", " starten neu, einer nach dem anderen. Jeder ist dabei einige Sekunden nicht erreichbar.")
		p.Back = "Nicht nötig: Danach läuft alles wie vorher."
	}
	if len(always) > 0 {
		if durable {
			p.Notes = append(p.Notes, strings.Join(always, ", ")+" hat die Neustart-Regel „always“. Damit "+map[bool]string{true: "er", false: "sie"}[len(always) == 1]+" auch nach einem Neustart des Servers aus bleibt, stellt HostDeck die Regel auf „unless-stopped“ um. Sonst verhält sie sich gleich.")
		} else {
			p.Notes = append(p.Notes, strings.Join(always, ", ")+" hat die Neustart-Regel „always“. Nach einem Neustart des Servers startet Docker "+map[bool]string{true: "ihn", false: "sie"}[len(always) == 1]+" von selbst wieder.")
		}
	}
	return p, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// Do führt eine Aktion aus. Es rechnet den Plan mit dem Stand von jetzt neu,
// damit nichts geschieht, was die Vorschau nicht gezeigt hätte; weicht er von
// want ab (andere Container), bricht Do ab, ohne etwas zu tun.
func (s *Service) Do(ctx context.Context, target, action string, want []string) (Result, error) {
	p, err := s.Plan(ctx, target, action)
	if err != nil {
		return Result{}, err
	}
	if p.Problem != "" {
		return Result{Plan: p}, errors.New(p.Problem)
	}
	if want != nil && !sameSet(p.Containers, want) {
		return Result{Plan: p}, errors.New("Der Stand hat sich seit der Vorschau geändert. Bitte schau dir die Vorschau noch einmal an.")
	}
	res := Result{Plan: p, OK: true}
	for _, name := range p.Containers {
		var err error
		switch action {
		case Start:
			err = s.Docker.Start(ctx, name)
		case Stop:
			// „always“ würde Docker nach einem Neustart des Servers wieder
			// starten; „unless-stopped“ merkt sich das Stoppen.
			if ps, ok := s.Docker.(PolicySetter); ok && p.always[name] {
				err = ps.SetRestartPolicy(ctx, name, "unless-stopped")
			}
			if err == nil {
				err = s.Docker.Stop(ctx, name, s.StopTimeout)
			}
		case Restart:
			err = s.Docker.Restart(ctx, name, s.StopTimeout)
		}
		st := Step{Name: name, OK: err == nil}
		if err != nil {
			st.Error = err.Error()
			res.OK = false
		} else {
			s.remember(name, action == Stop)
		}
		res.Steps = append(res.Steps, st)
		if ctx.Err() != nil {
			break
		}
	}
	return res, nil
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}

// ---------- Logs ----------

// MaxLogBytes begrenzt, was der Leitstand von den Logs eines Containers holt.
const MaxLogBytes = 512 << 10

// Logs ist ein Ausschnitt der Logs eines Containers.
type Logs struct {
	Name  string    `json:"name"`
	Time  time.Time `json:"zeit"`
	Lines []string  `json:"zeilen"`
	Cut   bool      `json:"gekuerzt,omitempty"` // mehr als MaxLogBytes; der Anfang fehlt
	// Err sind die Nummern der Zeilen (ab 0), die auf stderr kamen. Bei
	// Containern mit TTY gibt es nur einen Strom; dann bleibt es leer.
	Err []int `json:"stderr,omitempty"`
}

var ansi = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b[@-_]")

// Logs liest die letzten lines Zeilen eines Containers. Das geht auch bei
// Containern des Panels: Lesen ändert nichts.
func (s *Service) Logs(ctx context.Context, name string, lines int) (Logs, error) {
	if !validName.MatchString(name) {
		return Logs{}, ErrBadTarget
	}
	if lines < 1 || lines > 2000 {
		lines = 200
	}
	v, err := s.Docker.Inspect(ctx, name)
	if errors.Is(err, dockerapi.ErrNoSuchContainer) {
		return Logs{}, ErrNotFound
	}
	if err != nil {
		return Logs{}, err
	}
	b, err := s.Docker.Logs(ctx, name, lines, v.Config.Tty, MaxLogBytes+1)
	if err != nil {
		return Logs{}, err
	}
	out := Logs{Name: name, Time: s.now(), Lines: []string{}}
	if len(b) > MaxLogBytes {
		out.Cut = true
		b = b[len(b)-MaxLogBytes:]
		if i := strings.IndexByte(string(b), '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	text := strings.ToValidUTF8(string(b), "�")
	text = ansi.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if rest, ok := strings.CutPrefix(l, dockerapi.StderrMark); ok {
			l = rest
			out.Err = append(out.Err, len(out.Lines))
		}
		if i := strings.LastIndexByte(l, '\r'); i >= 0 {
			l = l[i+1:]
		}
		l = strings.Map(func(r rune) rune {
			if r < 0x20 && r != '\t' || r == 0x7f {
				return -1
			}
			return r
		}, l)
		if utf8.RuneCountInString(l) > 4000 {
			l = string([]rune(l)[:4000]) + " …"
		}
		out.Lines = append(out.Lines, l)
	}
	if len(out.Lines) == 1 && out.Lines[0] == "" {
		out.Lines = []string{}
		out.Err = nil
	}
	return out, nil
}

// ---------- Einzelheiten ----------

// ProcessLister kann die Prozesse eines laufenden Containers auflisten
// (dockerapi.Ops). Kann der Zugang das nicht, fehlen sie in den
// Einzelheiten.
type ProcessLister interface {
	Top(ctx context.Context, id string) (dockerapi.Top, error)
}

// Processes sind die Prozesse in einem Container, wie docker top sie zeigt,
// ohne Befehlszeile (siehe dockerapi.TopArgs).
type Processes struct {
	Titles []string   `json:"titel"`
	Rows   [][]string `json:"zeilen"`
}

// Port ist ein veröffentlichter Port.
type Port struct {
	Container string `json:"container"` // etwa 8080/tcp
	HostIP    string `json:"host_ip,omitempty"`
	HostPort  string `json:"host_port,omitempty"`
}

// Mount ist ein eingehängter Ordner oder ein Volume.
type Mount struct {
	Type   string `json:"typ"`
	Source string `json:"quelle"`
	Target string `json:"ziel"`
	RW     bool   `json:"schreibbar"`
}

// Config ist, wie ein Container eingerichtet ist. Sie enthält bewusst keine
// Werte von Umgebungsvariablen und keine Befehlszeile: Darin stehen oft
// Passwörter. Von den Labels bleiben nur die von Compose.
type Config struct {
	Image         string            `json:"image"`
	RestartPolicy string            `json:"neustart_regel,omitempty"`
	Privileged    bool              `json:"privilegiert,omitempty"`
	MemLimit      int64             `json:"speicher_grenze,omitempty"`
	NetworkMode   string            `json:"netzwerk_modus,omitempty"`
	Networks      []string          `json:"netzwerke,omitempty"`
	Ports         []Port            `json:"ports,omitempty"`
	Mounts        []Mount           `json:"mounts,omitempty"`
	EnvNames      []string          `json:"umgebung,omitempty"` // nur Namen
	LogDriver     string            `json:"log_treiber,omitempty"`
	LogOptions    map[string]string `json:"log_optionen,omitempty"`
	Compose       map[string]string `json:"compose,omitempty"` // Projekt, Dienst, Ordner, Dateien
	StartedAt     string            `json:"gestartet,omitempty"`
	FinishedAt    string            `json:"beendet,omitempty"`
}

// safeLogOpt sind die Log-Optionen, die die Konfiguration zeigt.
var safeLogOpt = map[string]bool{"max-size": true, "max-file": true, "mode": true, "compress": true, "tag": true,
	"labels": true, "env": true, "max-buffer-size": true}

// Details sind die Einzelheiten eines Containers für die Vollansicht.
type Details struct {
	Name      string     `json:"name"`
	Time      time.Time  `json:"zeit"`
	Config    Config     `json:"konfig"`
	Processes *Processes `json:"prozesse,omitempty"` // nur, wenn er läuft und der Zugang es kann
	// ProcError sagt, warum die Prozesse fehlen, obwohl er läuft.
	ProcError string `json:"prozesse_fehler,omitempty"`
}

// composeLabels sind die Labels, die die Einzelheiten zeigen.
var composeLabels = map[string]string{
	labelProject:                              "projekt",
	labelService:                              "dienst",
	"com.docker.compose.project.working_dir":  "ordner",
	"com.docker.compose.project.config_files": "dateien",
}

// Details liest, wie ein Container eingerichtet ist und welche Prozesse in
// ihm laufen. Nur lesen, wie Logs.
func (s *Service) Details(ctx context.Context, name string) (Details, error) {
	if !validName.MatchString(name) {
		return Details{}, ErrBadTarget
	}
	v, err := s.Docker.Inspect(ctx, name)
	if errors.Is(err, dockerapi.ErrNoSuchContainer) {
		return Details{}, ErrNotFound
	}
	if err != nil {
		return Details{}, err
	}
	c := Config{
		Image:         v.Config.Image,
		RestartPolicy: v.HostConfig.RestartPolicy.Name,
		Privileged:    v.HostConfig.Privileged,
		MemLimit:      v.HostConfig.Memory,
		NetworkMode:   v.HostConfig.NetworkMode,
		EnvNames:      append([]string(nil), v.Config.Env...),
		LogDriver:     v.HostConfig.LogConfig.Type,
		StartedAt:     v.State.StartedAt,
		FinishedAt:    v.State.FinishedAt,
	}
	// Log-Optionen nur mit bekannten, harmlosen Schlüsseln: Andere Treiber
	// (splunk-token, loki-url mit Zugangsdaten) tragen dort Geheimnisse.
	for k, val := range v.HostConfig.LogConfig.Config {
		if !safeLogOpt[k] {
			continue
		}
		if c.LogOptions == nil {
			c.LogOptions = map[string]string{}
		}
		c.LogOptions[k] = val
	}
	for n := range v.NetworkSettings.Networks {
		c.Networks = append(c.Networks, n)
	}
	sort.Strings(c.Networks)
	for cp, binds := range v.HostConfig.PortBindings {
		if len(binds) == 0 {
			c.Ports = append(c.Ports, Port{Container: cp})
		}
		for _, b := range binds {
			c.Ports = append(c.Ports, Port{Container: cp, HostIP: b.HostIP, HostPort: b.HostPort})
		}
	}
	sort.Slice(c.Ports, func(i, j int) bool {
		if c.Ports[i].Container != c.Ports[j].Container {
			return c.Ports[i].Container < c.Ports[j].Container
		}
		return c.Ports[i].HostIP+c.Ports[i].HostPort < c.Ports[j].HostIP+c.Ports[j].HostPort
	})
	for _, m := range v.Mounts {
		src := m.Source
		if m.Type == "volume" && m.Name != "" {
			src = m.Name
		}
		c.Mounts = append(c.Mounts, Mount{Type: m.Type, Source: src, Target: m.Destination, RW: m.RW})
	}
	sort.Slice(c.Mounts, func(i, j int) bool { return c.Mounts[i].Target < c.Mounts[j].Target })
	sort.Strings(c.EnvNames)
	for k, key := range composeLabels {
		if val := v.Config.Labels[k]; val != "" {
			if c.Compose == nil {
				c.Compose = map[string]string{}
			}
			c.Compose[key] = val
		}
	}
	out := Details{Name: name, Time: s.now(), Config: c}
	if pl, ok := s.Docker.(ProcessLister); ok && v.State.Running {
		top, err := pl.Top(ctx, name)
		if err != nil {
			out.ProcError = "Docker nennt die Prozesse gerade nicht: " + err.Error()
		} else {
			p := &Processes{Titles: top.Titles, Rows: top.Processes}
			if len(p.Rows) > 500 {
				p.Rows = p.Rows[:500]
			}
			if p.Titles == nil {
				p.Titles = []string{}
			}
			if p.Rows == nil {
				p.Rows = [][]string{}
			}
			out.Processes = p
		}
	}
	return out, nil
}

// ---------- was du gestoppt hast ----------

// LoadStopped liest die Liste der Container, die du im Leitstand gestoppt
// hast (Name → Zeitpunkt). Fehlt die Datei, ist sie leer.
func LoadStopped(path string) map[string]time.Time {
	m := map[string]time.Time{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// SaveStopped schreibt die Liste (nur root, atomar).
func SaveStopped(path string, m map[string]time.Time) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".neu"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Service) loadStopped() map[string]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadStoppedLocked()
}

func (s *Service) loadStoppedLocked() map[string]time.Time {
	m := map[string]time.Time{}
	if s.StoppedFile == "" {
		for k, v := range s.stopped {
			m[k] = v
		}
		return m
	}
	return LoadStopped(s.StoppedFile)
}

// remember merkt sich einen gestoppten Container oder vergisst ihn wieder.
// stoppedSlack: Docker setzt den Start eines Containers, den HostDeck gerade
// gestoppt hat, nicht nach der Merkung; Uhren zwischen Daemon und HostDeck
// dürfen aber ein wenig auseinanderliegen.
const stoppedSlack = 5 * time.Second

func (s *Service) remember(name string, stopped bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.loadStoppedLocked()
	if stopped {
		m[name] = s.now().UTC()
	} else {
		delete(m, name)
	}
	if s.StoppedFile == "" {
		s.stopped = m
		return
	}
	_ = SaveStopped(s.StoppedFile, m)
}

// ManagedBy sagt für jeden Container, den ein anderer anlegt (etwa ein
// Panel die Spielserver), wer ihn anlegt.
func ManagedBy(list []dockerapi.OpsContainer) map[string]string { return managedBy(list) }

func (s *Service) cores() int {
	if s.Cores != nil {
		if n := s.Cores(); n > 0 {
			return n
		}
	}
	return runtime.NumCPU()
}
