package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/restic"
	"github.com/officialtaxz2/hostdeck/internal/units"
)

// Docker ist, was ein Lauf von Docker braucht.
type Docker interface {
	State(ctx context.Context, id string) (dockerapi.ContainerState, error)
	Stop(ctx context.Context, id string, timeout time.Duration) error
	Start(ctx context.Context, id string) error
	Running(ctx context.Context, names []string) ([]string, error)
}

// Store nimmt Snapshots entgegen.
type Store interface {
	Backup(ctx context.Context, a restic.BackupArgs) (restic.Result, error)
}

// Question ist eine Frage an den Benutzer.
type Question struct {
	Text   string
	Secret bool // Eingabe nicht anzeigen
	// Choices sind feste Antworten für Knöpfe im Leitstand. Im Terminal
	// tippt man die Antwort selbst; der Text sagt, welche es gibt.
	Choices []Choice
	// Note ersetzt Text im Leitstand, wo es kein Enter gibt. Leer: Text.
	Note string
}

// Choice ist eine feste Antwort auf eine Frage.
type Choice struct {
	Label  string `json:"text"`
	Answer string `json:"antwort"`
}

// Input fragt den Benutzer. Ask wartet auf eine Antwort, bis ctx endet.
type Input interface {
	Ask(ctx context.Context, q Question) (string, error)
}

// Phasen eines Snapshots.
const (
	PhasePre   = "vorlauf" // nur, damit der zweite Durchgang wenig übertragen muss
	PhaseFinal = "final"   // gehört zum Backup
)

// StepResult ist das Ergebnis eines Snapshots.
type StepResult struct {
	Item       Item          `json:"item"`
	Phase      string        `json:"phase"`
	Snapshot   string        `json:"snapshot,omitempty"`
	Added      int64         `json:"added"`
	Files      int64         `json:"files"`
	Bytes      int64         `json:"bytes"`
	Duration   time.Duration `json:"duration"`
	Err        string        `json:"error,omitempty"`
	Unreadable []string      `json:"unreadable,omitempty"`
}

// GroupResult hält fest, wie lange ein Projekt stand.
type GroupResult struct {
	Name       string        `json:"name"`
	Stopped    []string      `json:"stopped,omitempty"`
	Downtime   time.Duration `json:"downtime"`
	NotRunning []string      `json:"not_running,omitempty"` // lief vorher, läuft nach dem Start nicht
	LeftOff    bool          `json:"left_off,omitempty"`    // letzter Lauf: bleibt aus
	// PanelStopped: verwaltete Server, die du für diesen Lauf im Panel
	// gestoppt hast. HostDeck startet sie nie selbst; das sagt es dir am Ende.
	PanelStopped []string `json:"panel_gestoppt,omitempty"`
}

// Result ist das Ergebnis eines Laufs.
type Result struct {
	RunID    string        `json:"run"`
	Final    bool          `json:"final"`
	Started  time.Time     `json:"started"`
	Finished time.Time     `json:"finished"`
	Steps    []StepResult  `json:"steps"`
	Groups   []GroupResult `json:"groups"`
	Warnings []string      `json:"warnings,omitempty"`
	Err      string        `json:"error,omitempty"`
	Aborted  bool          `json:"aborted,omitempty"`
}

// OK meldet, ob der Lauf ohne Fehler durchlief.
func (r *Result) OK() bool { return r.Err == "" && !r.Aborted }

// Runner führt einen Plan aus.
type Runner struct {
	Plan   *Plan
	RunID  string
	Docker Docker
	Store  Store
	Input  Input
	State  *StopState
	Out    io.Writer
	// Self startet HostDeck selbst für Dumps und Images, etwa
	// ["/root/.hostdeck/bin/hostdeck", "intern-dump"] ohne den Unterbefehl:
	// ["/root/.hostdeck/bin/hostdeck", "--docker-socket", "/var/run/docker.sock"].
	Self     []string
	Platform string // für docker save, als JSON
	Tags     []string
	// Exclude bleibt in jedem Ordner draußen, etwa der Backup-Ordner
	// selbst, falls er unter einem gesicherten Ordner liegt.
	Exclude []string

	StopTimeout time.Duration // Standard 30 s
	StartWait   time.Duration // so lange auf eine Datenbank warten, Standard 60 s
	Settle      time.Duration // vor der Kontrolle nach dem Start, Standard 10 s
	Poll        time.Duration // Abstand beim Warten auf verwaltete Server, Standard 10 s
	Now         func() time.Time

	res   *Result
	step  int
	steps int
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) printf(format string, args ...any) {
	fmt.Fprintf(r.Out, format, args...)
}

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// errAbort heißt: der Benutzer hat abgebrochen.
var errAbort = errors.New("vom Benutzer abgebrochen")

// Run führt den Plan aus. Was auch passiert: Container, die HostDeck gestoppt
// hat, laufen danach wieder, außer im erfolgreichen letzten Lauf.
func (r *Runner) Run(ctx context.Context) *Result {
	r.res = &Result{RunID: r.RunID, Final: r.Plan.Final, Started: r.now()}
	r.steps = r.countSteps()
	err := r.run(ctx)
	if err != nil {
		r.res.Err = err.Error()
		if errors.Is(err, errAbort) || ctx.Err() != nil {
			r.res.Aborted = true
		}
		r.printf("\nDer Lauf ist abgebrochen: %v\n", err)
		r.restartAll()
	} else if r.Plan.Final {
		if err := r.State.MarkFinal(r.RunID); err != nil {
			r.warn("Liste der gestoppten Container: %v", err)
		}
	}
	r.res.Finished = r.now()
	return r.res
}

func (r *Runner) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	r.res.Warnings = append(r.res.Warnings, msg)
	r.printf("  Hinweis: %s\n", msg)
}

func (r *Runner) countSteps() int {
	n := 0
	for _, g := range r.Plan.Groups {
		n += len(g.Live) + len(g.Dumps) + len(g.Stopped)
		if g.NeedsStop(r.Plan.Final) {
			n += len(g.Stopped) // Vorlauf und zweiter Durchgang
		}
	}
	return n + len(r.Plan.Archive) + len(r.Plan.Images)
}

func (r *Runner) run(ctx context.Context) error {
	p := r.Plan
	r.printf("\nVorlauf, alles läuft weiter\n")
	for _, g := range p.Groups {
		for _, it := range g.Live {
			if err := r.snapshot(ctx, it, PhaseFinal); err != nil {
				return err
			}
		}
		phase := PhaseFinal
		if g.NeedsStop(p.Final) {
			phase = PhasePre
		}
		for _, it := range g.Stopped {
			if err := r.snapshot(ctx, it, phase); err != nil {
				return err
			}
		}
	}
	for _, it := range p.Archive {
		if err := r.snapshot(ctx, it, PhaseFinal); err != nil {
			return err
		}
	}

	if !p.Final {
		header := false
		for _, g := range p.Groups {
			for _, it := range g.Dumps {
				if !header {
					r.printf("\nDatenbanken, laufen weiter\n")
					header = true
				}
				if err := r.snapshot(ctx, it, PhaseFinal); err != nil {
					return err
				}
			}
		}
	}

	header := false
	for i := range p.Groups {
		g := &p.Groups[i]
		if !g.NeedsStop(p.Final) {
			if p.Final && len(g.Dumps) > 0 {
				for _, it := range g.Dumps {
					if err := r.snapshot(ctx, it, PhaseFinal); err != nil {
						return err
					}
				}
			}
			continue
		}
		if !header {
			if p.Final {
				r.printf("\nProjekt für Projekt stoppen; sie bleiben danach aus (letzter Lauf)\n")
			} else {
				r.printf("\nProjekt für Projekt kurz stoppen\n")
			}
			header = true
		}
		if err := r.stopGroup(ctx, g); err != nil {
			return err
		}
	}

	if len(p.Images) > 0 {
		r.printf("\nImages\n")
		for _, it := range p.Images {
			if err := r.snapshot(ctx, it, PhaseFinal); err != nil {
				return err
			}
		}
	}
	return nil
}

// stopGroup stoppt ein Projekt, sichert, was nur gestoppt geht, und startet
// es wieder.
func (r *Runner) stopGroup(ctx context.Context, g *Group) error {
	r.printf("  %s\n", g.Name)
	var panel []string
	if len(g.Managed) > 0 {
		var err error
		if panel, err = r.waitManagedOff(ctx, g); err != nil {
			return err
		}
	}
	var apps, dbs []Ctr
	for _, c := range g.Containers {
		if !c.Running {
			continue
		}
		if c.DB {
			dbs = append(dbs, c)
		} else {
			apps = append(apps, c)
		}
	}
	gr := GroupResult{Name: g.Name, PanelStopped: panel}
	start := r.now()
	// Erst die Anwendungen, dann die Datenbanken: So schreibt nichts mehr,
	// während die Datenbank herunterfährt.
	// Anwendungen bekommen die Zeit, die für sie eingestellt ist;
	// Datenbanken mindestens StopTimeout, damit sie sauber herunterfahren.
	if err := r.stopAll(ctx, apps, &gr, 0); err != nil {
		return err
	}
	if r.Plan.Final {
		for _, it := range g.Dumps {
			if err := r.snapshot(ctx, it, PhaseFinal); err != nil {
				return err
			}
		}
	}
	if err := r.stopAll(ctx, dbs, &gr, orDefault(r.StopTimeout, 30*time.Second)); err != nil {
		return err
	}
	for _, it := range g.Stopped {
		if err := r.snapshot(ctx, it, PhaseFinal); err != nil {
			return err
		}
	}
	if r.Plan.Final {
		gr.LeftOff = true
		gr.Downtime = r.now().Sub(start)
		r.res.Groups = append(r.res.Groups, gr)
		r.printf("    bleibt aus\n")
		return nil
	}
	var up time.Time
	gr.NotRunning, up = r.startAll(append(dbs, apps...))
	gr.Downtime = up.Sub(start)
	r.res.Groups = append(r.res.Groups, gr)
	if len(gr.NotRunning) > 0 {
		r.warn("%s: läuft nach dem Start nicht: %s", g.Name, strings.Join(gr.NotRunning, ", "))
	}
	r.printf("    wieder gestartet, Stillstand %s\n", duration(gr.Downtime))
	if len(panel) > 0 {
		r.printf("    Im Panel wieder starten, wenn sie laufen sollen: %s (HostDeck startet verwaltete Server nie selbst)\n", strings.Join(panel, ", "))
	}
	return nil
}

func (r *Runner) stopAll(ctx context.Context, cs []Ctr, gr *GroupResult, timeout time.Duration) error {
	for _, c := range cs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.State.Add(StopEntry{ID: c.ID, Name: c.Name, Run: r.RunID, Final: false}); err != nil {
			return fmt.Errorf("Liste der gestoppten Container: %w", err)
		}
		r.printf("    stoppe %s\n", c.Name)
		gr.Stopped = append(gr.Stopped, c.Name)
		sctx, cancel := context.WithTimeout(ctx, timeout+2*time.Minute)
		err := r.Docker.Stop(sctx, c.ID, timeout)
		cancel()
		if err != nil {
			return fmt.Errorf("%s stoppen: %w", c.Name, err)
		}
	}
	return nil
}

// startAll startet Container in der angegebenen Reihenfolge, wartet nach
// Datenbanken, bis sie laufen, und meldet, welche danach nicht laufen, und
// wann alle gestartet waren. Es nutzt einen eigenen Kontext, damit es auch
// nach einem Abbruch arbeitet.
func (r *Runner) startAll(cs []Ctr) ([]string, time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var started []Ctr
	for _, c := range cs {
		r.printf("    starte %s\n", c.Name)
		if err := r.Docker.Start(ctx, c.ID); err != nil {
			r.warn("%s starten: %v", c.Name, err)
			continue
		}
		started = append(started, c)
		_ = r.State.Remove(c.ID)
		if c.DB {
			r.waitRunning(ctx, c, orDefault(r.StartWait, time.Minute))
		}
	}
	up := r.now()
	sleep(ctx, orDefault(r.Settle, 10*time.Second))
	var bad []string
	for _, c := range started {
		st, err := r.Docker.State(ctx, c.ID)
		if err != nil || !st.Running {
			bad = append(bad, c.Name)
		}
	}
	for _, c := range cs {
		if !contains(started, c.ID) {
			bad = append(bad, c.Name)
		}
	}
	return bad, up
}

func contains(cs []Ctr, id string) bool {
	for _, c := range cs {
		if c.ID == id {
			return true
		}
	}
	return false
}

func (r *Runner) waitRunning(ctx context.Context, c Ctr, max time.Duration) {
	deadline := r.now().Add(max)
	for r.now().Before(deadline) {
		if st, err := r.Docker.State(ctx, c.ID); err == nil && st.Running {
			return
		}
		if !sleep(ctx, time.Second) {
			return
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// restartAll startet alles, was dieser Lauf gestoppt und noch nicht wieder
// gestartet hat.
func (r *Runner) restartAll() {
	var cs []Ctr
	for _, e := range r.State.Entries() {
		if e.Run != r.RunID {
			continue
		}
		c := Ctr{ID: e.ID, Name: e.Name}
		for _, g := range r.Plan.Groups {
			for _, gc := range g.Containers {
				if gc.ID == e.ID {
					c.DB = gc.DB
				}
			}
		}
		cs = append(cs, c)
	}
	if len(cs) == 0 {
		return
	}
	// Datenbanken zuerst.
	var ordered []Ctr
	for _, c := range cs {
		if c.DB {
			ordered = append(ordered, c)
		}
	}
	for _, c := range cs {
		if !c.DB {
			ordered = append(ordered, c)
		}
	}
	r.printf("Starte die gestoppten Container wieder:\n")
	if bad, _ := r.startAll(ordered); len(bad) > 0 {
		r.warn("läuft nach dem Abbruch nicht wieder: %s; bitte prüfen (hostdeck dienste-starten)", strings.Join(bad, ", "))
	}
}

// waitManagedOff wartet, bis alle verwalteten Server der Gruppe aus sind.
// Stoppen muss sie der Benutzer im Panel; HostDeck stoppt sie nie selbst. Es
// nennt die, die beim Warten noch liefen, also für den Lauf gestoppt wurden.
func (r *Runner) waitManagedOff(ctx context.Context, g *Group) ([]string, error) {
	var names []string
	label := map[string]string{}
	for _, m := range g.Managed {
		names = append(names, m.Name)
		label[m.Name] = m.Name
		if m.Label != "" {
			label[m.Name] = m.Label
		}
	}
	poll := orDefault(r.Poll, 10*time.Second)
	// Die Frage steht einmal da, geprüft wird nebenher; nur nach Enter
	// kommt sie neu.
	answers := make(chan string, 1)
	var stopAsk context.CancelFunc
	note := ""
	ask := func() {
		actx, cancel := context.WithCancel(ctx)
		stopAsk = cancel
		go func() {
			if ans, err := r.Input.Ask(actx, Question{Text: "Enter = jetzt prüfen, a = Lauf abbrechen", Note: note, Choices: []Choice{{Label: "Jetzt prüfen", Answer: ""}, {Label: "Lauf abbrechen", Answer: "a"}}}); err == nil {
				answers <- ans
			}
		}()
	}
	defer func() {
		if stopAsk != nil {
			stopAsk()
		}
	}()
	tick := time.NewTicker(poll)
	defer tick.Stop()
	asked := false
	var stopped []string
	for {
		running, err := r.Docker.Running(ctx, names)
		if err != nil {
			return nil, fmt.Errorf("verwaltete Server prüfen: %w", err)
		}
		if len(running) == 0 {
			if asked {
				r.printf("    alle verwalteten Server sind aus\n")
			}
			return stopped, nil
		}
		if !asked {
			var ls []string
			for _, n := range running {
				ls = append(ls, label[n])
			}
			stopped = ls
			r.printf("    Diese verwalteten Server laufen noch: %s\n", strings.Join(ls, ", "))
			r.printf("    Bitte stoppe sie im Panel. HostDeck prüft alle %s und macht dann allein weiter.\n", duration(poll))
			note = fmt.Sprintf("Diese verwalteten Server laufen noch: %s. Bitte stoppe sie im Panel; HostDeck prüft alle %s und macht dann von selbst weiter.", strings.Join(ls, ", "), duration(poll))
			asked = true
			ask()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		case ans := <-answers:
			if strings.EqualFold(strings.TrimSpace(ans), "a") {
				return nil, errAbort
			}
			ask()
		}
	}
}

// snapshot sichert einen Schritt.
func (r *Runner) snapshot(ctx context.Context, it Item, phase string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.step++
	label := it.Title
	if it.Strategy == StratDump {
		label = fmt.Sprintf("%s (%s-Dump aus %s)", it.Title, it.Engine, it.ContainerName)
	}
	if phase == PhasePre {
		label += ", Vorlauf"
	}
	r.printf("  [%d/%d] %s\n", r.step, r.steps, label)
	tags := append([]string{}, r.Tags...)
	if phase == PhasePre {
		tags = append(tags, "sbm-vorlauf")
	} else {
		tags = append(tags, "sbm") // Kennung im Datenformat (ältere Backups), bleibt
	}
	tags = append(tags, "sbm-run="+r.RunID)
	if !strings.Contains(it.Key, ",") {
		tags = append(tags, "sbm-key="+it.Key)
	}
	args := restic.BackupArgs{Tags: tags, Paths: it.Paths, Excludes: it.Excludes}
	for _, x := range r.Exclude {
		args.Excludes = append(append([]string{}, args.Excludes...), restic.EscapePattern(x))
	}
	switch it.Strategy {
	case StratDump:
		args.Paths, args.Excludes = nil, nil
		args.Command = append(append([]string{}, r.Self...), "intern-dump", "--container", it.Container, "--engine", it.Engine)
		args.Filename = it.Filename
	case StratImage:
		args.Paths, args.Excludes = nil, nil
		args.Command = append(append([]string{}, r.Self...), "intern-image", "--image", it.ImageID)
		for _, ref := range it.Refs {
			args.Command = append(args.Command, "--ref", ref)
		}
		if r.Platform != "" {
			args.Command = append(args.Command, "--platform", r.Platform)
		}
		args.Filename = it.Filename
	}
	last := r.now()
	args.Progress = func(s restic.Status) {
		if r.now().Sub(last) < 30*time.Second {
			return
		}
		last = r.now()
		switch {
		case s.TotalBytes > 0:
			r.printf("        … %d %% (%s von %s)\n", int(s.PercentDone*100), units.HumanSize(s.BytesDone), units.HumanSize(s.TotalBytes))
		case s.BytesDone > 0:
			r.printf("        … %s gelesen\n", units.HumanSize(s.BytesDone))
		}
	}
	begin := r.now()
	res, err := r.Store.Backup(ctx, args)
	sr := StepResult{Item: it, Phase: phase, Snapshot: res.SnapshotID, Added: res.DataAdded,
		Files: res.TotalFiles, Bytes: res.TotalBytes, Duration: r.now().Sub(begin), Unreadable: res.Unreadable}
	if err != nil {
		sr.Err = err.Error()
		r.res.Steps = append(r.res.Steps, sr)
		return fmt.Errorf("%s: %w", it.Title, err)
	}
	r.res.Steps = append(r.res.Steps, sr)
	r.printf("        fertig: %s neu, %s geprüft, %s\n", units.HumanSize(res.DataAdded), units.HumanSize(res.TotalBytes), duration(sr.Duration))
	if len(res.Unreadable) > 0 {
		r.warn("%s: %d Dateien waren nicht lesbar, etwa %s", it.Title, len(res.Unreadable), res.Unreadable[0])
	}
	return nil
}

func duration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%d s", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d min %d s", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%d h %d min", int(d.Hours()), int(d.Minutes())%60)
}
