package aktualisieren

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
)

// PlanService ist ein Dienst in der Vorschau.
type PlanService struct {
	Service   string `json:"dienst"`
	Container string `json:"container"`
	Kind      string `json:"art"`
	Image     string `json:"image"`
	Action    string `json:"aktion"`          // etwa „Image holen“
	State     string `json:"stand,omitempty"` // vom letzten Nachsehen
	Detail    string `json:"detail,omitempty"`
	Database  bool   `json:"datenbank,omitempty"`
	Chosen    bool   `json:"gewaehlt"`
}

// Skip ist ein Dienst, der bleibt, wie er ist.
type Skip struct {
	Name   string `json:"name"`
	Reason string `json:"grund"`
}

// Plan ist die Vorschau einer Aktualisierung. Er ändert nichts.
type Plan struct {
	Project  string        `json:"projekt"`
	Options  []PlanService `json:"auswahl"` // alle Dienste, die in Frage kommen
	Services []string      `json:"dienste"` // gewählt
	Skipped  []Skip        `json:"ausgelassen,omitempty"`
	Steps    []string      `json:"schritte"` // was geschieht, der Reihe nach
	Back     string        `json:"rueckweg"`
	Notes    []string      `json:"hinweise,omitempty"`
	Domains  []string      `json:"domains,omitempty"`
	Problem  string        `json:"problem,omitempty"`
}

// Plan rechnet aus, was das Aktualisieren der Dienste want täte. Ohne want
// wählt er die Dienste, für die es Neues gibt (Datenbanken nur auf
// ausdrücklichen Wunsch); weiß er davon nichts, alle laufenden außer
// Datenbanken.
func (u *Updater) Plan(ctx context.Context, project string, want []string) (Plan, error) {
	if !ValidName(project) {
		return Plan{}, fmt.Errorf("%w: Projektname", ErrBadChoice)
	}
	for _, w := range want {
		if !ValidName(w) {
			return Plan{}, fmt.Errorf("%w: Dienst %q", ErrBadChoice, w)
		}
	}
	p, err := u.load(ctx, project)
	if err != nil {
		return Plan{}, err
	}
	return u.plan(ctx, p, want)
}

func (u *Updater) plan(ctx context.Context, p *Project, want []string) (Plan, error) {
	pl := Plan{Project: p.Name, Options: []PlanService{}, Services: []string{}, Steps: []string{}}
	last := map[string]ServiceCheck{}
	for _, pc := range u.LastReport().Projects {
		if pc.Project == p.Name {
			for _, s := range pc.Services {
				last[s.Service] = s
			}
		}
	}
	wanted := map[string]bool{}
	for _, w := range want {
		wanted[w] = true
		found := false
		for _, s := range p.Services {
			found = found || s.Name == w
		}
		if !found {
			return Plan{}, fmt.Errorf("%w: Dienst %s gehört nicht zu %s", ErrBadChoice, w, p.Name)
		}
	}
	// allCurrent: Das letzte Nachsehen kennt jeden laufenden Dienst mit
	// seinem jetzigen Image und fand nirgends Neues.
	anyNewer, allCurrent := false, true
	for _, s := range p.Services {
		if !s.Running {
			continue
		}
		c, ok := last[s.Name]
		if ok && c.State == Newer {
			anyNewer = true
		}
		if !ok || c.ImageID != s.ImageID || (c.State != Current && c.State != Pinned && c.State != Local) {
			allCurrent = false
		}
	}
	for _, s := range p.Services {
		if !s.Running {
			why := "läuft nicht; bekommt ein neues Image erst, wenn Compose ihn neu anlegt"
			if s.Job {
				why = "einmaliger Job, läuft gerade nicht"
			}
			pl.Skipped = append(pl.Skipped, Skip{s.Name, why})
			if wanted[s.Name] {
				return Plan{}, fmt.Errorf("%w: %s läuft nicht", ErrBadChoice, s.Name)
			}
			continue
		}
		ps := PlanService{Service: s.Name, Container: s.Container, Image: s.Ref, Database: IsDatabase(s.Ref)}
		if c, ok := last[s.Name]; ok && c.ImageID == s.ImageID {
			ps.State, ps.Detail = c.State, c.Detail
		}
		switch {
		case s.Build != nil:
			ps.Kind, ps.Action = KindBuild, "neu bauen"
			if s.Build.Local {
				if g, err := u.gitState(ctx, s.Build.Context, false); err == nil && g.Remote != "" && g.merge != "" && !g.Dirty {
					ps.Action = "Git holen und neu bauen"
				}
			}
		default:
			ps.Kind, ps.Action = KindRegistry, "Image holen"
			if ps.State == Local {
				pl.Skipped = append(pl.Skipped, Skip{s.Name, "Image gibt es nur auf diesem Server; aus keiner Registry zu holen"})
				if wanted[s.Name] {
					return Plan{}, fmt.Errorf("%w: %s hat ein Image nur auf diesem Server", ErrBadChoice, s.Name)
				}
				continue
			}
			if r, err := ParseRef(s.Ref); err == nil && r.Pinned() {
				ps.Action = ""
				pl.Skipped = append(pl.Skipped, Skip{s.Name, "Image fest per Digest eingestellt; ändern geht nur in der Compose-Datei"})
				if wanted[s.Name] {
					return Plan{}, fmt.Errorf("%w: %s ist fest eingestellt", ErrBadChoice, s.Name)
				}
				continue
			}
		}
		switch {
		case want != nil:
			ps.Chosen = wanted[s.Name]
		case anyNewer:
			ps.Chosen = ps.State == Newer && !ps.Database
		case allCurrent:
			ps.Chosen = false
		default:
			ps.Chosen = !ps.Database
		}
		pl.Options = append(pl.Options, ps)
		if ps.Chosen {
			pl.Services = append(pl.Services, s.Name)
		}
	}
	if len(pl.Options) == 0 {
		pl.Problem = "Hier läuft kein Dienst, den HostDeck aktualisieren könnte."
		return pl, nil
	}
	if len(pl.Services) == 0 && want == nil && allCurrent {
		pl.Problem = "Beim letzten Nachsehen war alles aktuell. Hak einen Dienst an, wenn du ihn trotzdem neu holen oder bauen willst."
		return pl, nil
	}
	if len(pl.Services) == 0 {
		pl.Problem = "Wähle mindestens einen Dienst."
		return pl, nil
	}
	var pulls, builds []string
	gitDirs := map[string]bool{}
	var gitOrder []string
	for _, ps := range pl.Options {
		if !ps.Chosen {
			continue
		}
		if ps.Database {
			pl.Notes = append(pl.Notes, ps.Service+" ist eine Datenbank. Ein neues Image kann ihre Dateien umstellen; dann hilft der Rückfall auf das alte Image nicht mehr, nur ein Backup. Mach vorher eins.")
		}
		if ps.Kind == KindRegistry {
			pulls = append(pulls, ps.Service)
			continue
		}
		builds = append(builds, ps.Service)
		s := p.service(ps.Service)
		if !s.Build.Local {
			continue
		}
		g, err := u.gitState(ctx, s.Build.Context, false)
		var refused *GitRefused
		switch {
		case errors.As(err, &refused):
			if !gitDirs[refused.Dir] {
				gitDirs[refused.Dir] = true
				pl.Notes = append(pl.Notes, refused.Error()+". HostDeck baut den jetzigen Stand.")
			}
		case err != nil:
		case g.Dirty:
			pl.Notes = append(pl.Notes, "Im Git-Ordner "+g.Dir+" gibt es nicht committete Änderungen. HostDeck holt dort keine Commits und baut den jetzigen Stand.")
		case g.Remote == "" || g.merge == "":
			pl.Notes = append(pl.Notes, "Der Git-Ordner "+g.Dir+" hat keine Gegenstelle; HostDeck baut den jetzigen Stand.")
		default:
			if !gitDirs[g.Dir] {
				gitDirs[g.Dir] = true
				gitOrder = append(gitOrder, g.Dir+" ("+g.Remote+"/"+strings.TrimPrefix(g.merge, "refs/heads/")+")")
			}
		}
	}
	if u.Domains != nil {
		pl.Domains = u.Domains(p.Name)
	}
	if len(gitOrder) > 0 {
		pl.Steps = append(pl.Steps, "Neue Commits holen in "+strings.Join(gitOrder, ", ")+", nur wenn das ohne Zusammenführen geht.")
	}
	if len(pulls) > 0 {
		pl.Steps = append(pl.Steps, "Images holen: "+strings.Join(pulls, ", ")+".")
	}
	if len(builds) > 0 {
		pl.Steps = append(pl.Steps, "Neu bauen, mit frischen Basis-Images: "+strings.Join(builds, ", ")+".")
	}
	pl.Steps = append(pl.Steps, "Rückfall sichern: Bei jedem Dienst mit neuem Image bekommt das alte den Namen hostdeck-rueckfall/…, damit es bleibt.",
		"Neu anlegen und starten, nur Dienste mit neuem Image. Daten in Volumes und Ordnern bleiben.")
	check := "Prüfen: Container laufen und bleiben ruhig, Healthchecks sind grün"
	if len(pl.Domains) > 0 {
		check += ", " + strings.Join(pl.Domains, ", ") + " antwortet wie vorher"
	}
	pl.Steps = append(pl.Steps, check+".")
	pl.Back = "Das alte Image bleibt. Läuft das neue nicht sauber, stellt HostDeck von selbst zurück; sonst später mit „Zurück auf vorher“."
	return pl, nil
}

func (p *Project) service(name string) *Service {
	for i := range p.Services {
		if p.Services[i].Name == name {
			return &p.Services[i]
		}
	}
	return nil
}

// Change ist ein Dienst, der ein neues Image bekam.
type Change struct {
	Service string `json:"dienst"`
	Old     string `json:"vorher"`
	New     string `json:"jetzt"`
}

// Result ist das Ergebnis einer Aktualisierung oder eines Zurückstellens.
type Result struct {
	Project    string   `json:"projekt"`
	OK         bool     `json:"ok"`
	RolledBack bool     `json:"zurueckgestellt,omitempty"`
	Changed    []Change `json:"geaendert"`
	Same       []string `json:"unveraendert,omitempty"`
	Problem    string   `json:"problem,omitempty"`
}

// Text fasst das Ergebnis für das Protokoll zusammen.
func (r Result) Text() string {
	var names []string
	for _, c := range r.Changed {
		names = append(names, c.Service)
	}
	switch {
	case r.OK && len(r.Changed) == 0:
		return "Projekt " + r.Project + ": schon aktuell, nichts neu gestartet"
	case r.OK:
		return "Projekt " + r.Project + " aktualisiert: " + strings.Join(names, ", ")
	case r.RolledBack:
		return "Projekt " + r.Project + ": Aktualisierung zurückgestellt (" + r.Problem + ")"
	}
	return "Projekt " + r.Project + ": Aktualisierung gescheitert (" + r.Problem + ")"
}

// Fallback ist der gemerkte Stand vor einer Aktualisierung.
type Fallback struct {
	Project  string            `json:"projekt"`
	Time     time.Time         `json:"zeit"`
	Services []FallbackService `json:"dienste"`
	Git      []FallbackGit     `json:"git,omitempty"`
	Reverted *time.Time        `json:"zurueck,omitempty"`
}

// FallbackService ist ein Dienst im Rückfall.
type FallbackService struct {
	Service string `json:"dienst"`
	Ref     string `json:"image"`    // wie konfiguriert
	ImageID string `json:"image_id"` // vorher
	Tag     string `json:"rueckfall"`
	New     string `json:"neu,omitempty"` // Image danach; leer: unverändert
}

// FallbackGit ist ein Git-Ordner im Rückfall.
type FallbackGit struct {
	Dir  string `json:"ordner"`
	Head string `json:"stand"`
	New  string `json:"neu,omitempty"`
}

func fallbackFile(project string) string { return "rueckfall-" + project + ".json" }

// FallbackTag ist der Name, unter dem das alte Image bleibt.
func FallbackTag(project, service string) string {
	return "hostdeck-rueckfall/" + project + "-" + service + ":vorher"
}

// LastFallback ist der gemerkte Rückfall eines Projekts, falls es einen gibt.
func (u *Updater) LastFallback(project string) *Fallback {
	if !ValidName(project) {
		return nil
	}
	var f Fallback
	b, err := os.ReadFile(filepath.Join(u.StateDir, fallbackFile(project)))
	if err != nil || jsonUnmarshal(b, &f) != nil || f.Project != project {
		return nil
	}
	return &f
}

// Do aktualisiert die Dienste want eines Projekts. Es rechnet den Plan mit
// dem Stand von jetzt neu und bricht ab, wenn er von want abweicht.
func (u *Updater) Do(ctx context.Context, project string, want []string, out io.Writer) (Result, error) {
	res := Result{Project: project, Changed: []Change{}}
	if want == nil {
		return res, fmt.Errorf("%w: keine Dienste gewählt", ErrBadChoice)
	}
	pl, err := u.Plan(ctx, project, want)
	if err != nil {
		return res, err
	}
	if pl.Problem != "" {
		return res, errors.New(pl.Problem)
	}
	p, err := u.load(ctx, project)
	if err != nil {
		return res, err
	}
	say := func(f string, a ...any) { fmt.Fprintf(out, f+"\n", a...) }
	before := u.probe(ctx, pl.Domains)

	// 1. Den Stand von jetzt merken. Die alten Images bleiben, solange ihre
	// Container laufen; ihren Rückfall-Namen bekommen sie, bevor HostDeck die
	// Container ersetzt (Schritt 4). So bleibt ein früherer Rückfall
	// unberührt, wenn sich am Ende nichts ändert.
	fb := Fallback{Project: project, Time: time.Now().UTC()}
	for _, name := range pl.Services {
		s := p.service(name)
		fb.Services = append(fb.Services, FallbackService{Service: name, Ref: s.Ref, ImageID: s.ImageID, Tag: FallbackTag(project, name)})
	}
	// 2. Git: nur vorspulen, nie zusammenführen.
	var builds, pulls []string
	done := map[string]bool{}
	for _, name := range pl.Services {
		s := p.service(name)
		if s.Build == nil {
			pulls = append(pulls, name)
			continue
		}
		builds = append(builds, name)
		if !s.Build.Local {
			continue
		}
		g, err := u.gitState(ctx, s.Build.Context, false)
		if err != nil || done[g.Dir] || g.Dirty || g.Remote == "" || g.merge == "" {
			continue
		}
		done[g.Dir] = true
		fb.Git = append(fb.Git, FallbackGit{Dir: g.Dir, Head: g.Head})
	}
	for i := range fb.Git {
		fg := &fb.Git[i]
		g, _ := u.gitState(ctx, fg.Dir, false)
		branch := strings.TrimPrefix(g.merge, "refs/heads/")
		say("Git: neue Commits holen in %s (%s/%s)", fg.Dir, g.Remote, branch)
		if _, err := u.git(ctx, fg.Dir, out, "pull", "--ff-only", "--no-rebase", "--quiet", "--", g.Remote, branch); err != nil {
			u.resetGit(ctx, fb.Git[:i+1], out)
			res.Problem = "Git ließ sich nicht vorspulen (" + firstLine(err.Error()) + "); der Zweig ist wohl abgewichen. Nichts neu gestartet."
			return res, nil
		}
		fg.New, _ = u.git(ctx, fg.Dir, nil, "rev-parse", "HEAD")
		if fg.New == fg.Head {
			say("  schon auf dem neuesten Stand")
		} else {
			say("  %s → %s", ShortID(fg.Head), ShortID(fg.New))
		}
	}
	// 3. Holen und bauen. Noch läuft alles wie vorher.
	if len(pulls) > 0 {
		say("Images holen: %s", strings.Join(pulls, ", "))
		if _, err := u.CLI.Run(ctx, p.Dir, out, "docker", composeArgs(p, append([]string{"pull", "--quiet"}, pulls...)...)...); err != nil {
			u.resetGit(ctx, fb.Git, out)
			res.Problem = "Images holen ging nicht: " + firstLine(short(err)) + ". Nichts neu gestartet."
			return res, nil
		}
	}
	if len(builds) > 0 {
		say("Neu bauen: %s (das kann einige Minuten dauern)", strings.Join(builds, ", "))
		if _, err := u.CLI.Run(ctx, p.Dir, out, "docker", composeArgs(p, append([]string{"build", "--pull"}, builds...)...)...); err != nil {
			u.resetGit(ctx, fb.Git, out)
			res.Problem = "Bauen ging nicht: " + firstLine(short(err)) + ". Nichts neu gestartet."
			return res, nil
		}
	}
	// 4. Welche Dienste haben jetzt ein anderes Image? Nur sie bekommen
	// einen Rückfall und werden neu angelegt.
	var changed []string
	var keep []FallbackService
	for i := range fb.Services {
		fs := &fb.Services[i]
		id, err := u.imageID(ctx, p.Dir, fs.Ref)
		if err != nil {
			u.resetGit(ctx, fb.Git, out)
			res.Problem = "Image " + fs.Ref + " nach dem Holen nicht gefunden. Nichts neu gestartet."
			return res, nil
		}
		if id == fs.ImageID {
			res.Same = append(res.Same, fs.Service)
			continue
		}
		fs.New = id
		changed = append(changed, fs.Service)
		keep = append(keep, *fs)
		res.Changed = append(res.Changed, Change{Service: fs.Service, Old: ShortID(fs.ImageID), New: ShortID(id)})
	}
	if len(changed) == 0 {
		say("Kein Dienst hat ein neues Image; nichts neu gestartet.")
		res.OK = true
		return res, nil
	}
	fb.Services = keep
	for _, fs := range fb.Services {
		say("Rückfall sichern: %s (%s) als %s", fs.Service, ShortID(fs.ImageID), fs.Tag)
		if _, err := u.CLI.Run(ctx, p.Dir, nil, "docker", "tag", fs.ImageID, fs.Tag); err != nil {
			u.resetGit(ctx, fb.Git, out)
			res.Changed, res.Problem = []Change{}, "Rückfall nicht gesichert: "+short(err)+". Nichts neu gestartet."
			return res, nil
		}
	}
	if err := u.saveJSON(fallbackFile(project), fb); err != nil {
		u.resetGit(ctx, fb.Git, out)
		res.Changed, res.Problem = []Change{}, "Rückfall nicht gespeichert: "+short(err)+". Nichts neu gestartet."
		return res, nil
	}
	// 5. Neu anlegen und starten.
	say("Neu starten: %s", strings.Join(changed, ", "))
	if _, err := u.CLI.Run(ctx, p.Dir, out, "docker", composeArgs(p, append([]string{"up", "--detach", "--no-deps", "--no-build", "--pull", "never", "--force-recreate"}, changed...)...)...); err != nil {
		res.Problem = "Neu starten ging nicht: " + firstLine(short(err))
		return u.rollback(ctx, p, fb, changed, before, pl.Domains, out, res), nil
	}
	// 6. Prüfen.
	if err := u.verify(ctx, p, changed, before, pl.Domains, out); err != nil {
		res.Problem = err.Error()
		return u.rollback(ctx, p, fb, changed, before, pl.Domains, out, res), nil
	}
	say("Alles läuft. Das alte Image bleibt als Rückfall.")
	res.OK = true
	return res, nil
}

// rollback stellt den gemerkten Stand wieder her, nachdem das neue nicht
// sauber lief.
func (u *Updater) rollback(ctx context.Context, p *Project, fb Fallback, services []string, before map[string]caddy.Answer, domains []string, out io.Writer, res Result) Result {
	fmt.Fprintf(out, "Das neue läuft nicht sauber (%s). HostDeck stellt zurück.\n", res.Problem)
	if err := u.restore(ctx, p, fb, services, out); err != nil {
		res.Problem += "; Zurückstellen ging auch nicht: " + short(err)
		fmt.Fprintln(out, "Zurückstellen gescheitert:", err)
		return res
	}
	if err := u.verify(ctx, p, services, before, domains, out); err != nil {
		res.Problem += "; nach dem Zurückstellen: " + err.Error()
		return res
	}
	now := time.Now().UTC()
	fb.Reverted = &now
	_ = u.saveJSON(fallbackFile(p.Name), fb)
	res.RolledBack = true
	fmt.Fprintln(out, "Zurückgestellt: Es läuft wieder der Stand von vorher.")
	return res
}

// restore gibt den Images den alten Namen zurück, setzt Git zurück und legt
// die Dienste neu an.
func (u *Updater) restore(ctx context.Context, p *Project, fb Fallback, services []string, out io.Writer) error {
	use := map[string]bool{}
	for _, s := range services {
		use[s] = true
	}
	for _, fs := range fb.Services {
		if !use[fs.Service] {
			continue
		}
		fmt.Fprintf(out, "Altes Image zurück: %s → %s\n", fs.Tag, fs.Ref)
		if _, err := u.CLI.Run(ctx, p.Dir, nil, "docker", "tag", fs.Tag, fs.Ref); err != nil {
			return err
		}
	}
	u.resetGit(ctx, fb.Git, out)
	_, err := u.CLI.Run(ctx, p.Dir, out, "docker", composeArgs(p, append([]string{"up", "--detach", "--no-deps", "--no-build", "--pull", "never", "--force-recreate"}, services...)...)...)
	return err
}

// resetGit setzt vorgespulte Git-Ordner auf den alten Stand. --keep
// verwirft keine eigenen Änderungen.
func (u *Updater) resetGit(ctx context.Context, gits []FallbackGit, out io.Writer) {
	for _, g := range gits {
		now, err := u.git(ctx, g.Dir, nil, "rev-parse", "HEAD")
		if err != nil || now == g.Head {
			continue
		}
		fmt.Fprintf(out, "Git zurück in %s: %s\n", g.Dir, ShortID(g.Head))
		if _, err := u.git(ctx, g.Dir, out, "reset", "--keep", "--quiet", g.Head); err != nil {
			fmt.Fprintf(out, "  ging nicht: %v\n", firstLine(err.Error()))
		}
	}
}

func (u *Updater) imageID(ctx context.Context, dir, ref string) (string, error) {
	b, err := u.CLI.Run(ctx, dir, nil, "docker", "image", "inspect", "--format", "{{.Id}}", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// Revert stellt ein Projekt auf den Stand vor der letzten Aktualisierung.
func (u *Updater) Revert(ctx context.Context, project string, out io.Writer) (Result, error) {
	res := Result{Project: project, Changed: []Change{}}
	fb := u.LastFallback(project)
	if fb == nil {
		return res, fmt.Errorf("%w: kein Rückfall für %s", ErrNotFound, project)
	}
	if fb.Reverted != nil {
		return res, errors.New("dieser Rückfall ist schon eingespielt")
	}
	p, err := u.load(ctx, project)
	if err != nil {
		return res, err
	}
	var services []string
	for _, fs := range fb.Services {
		if fs.New == "" || p.service(fs.Service) == nil {
			continue
		}
		services = append(services, fs.Service)
		res.Changed = append(res.Changed, Change{Service: fs.Service, Old: ShortID(fs.New), New: ShortID(fs.ImageID)})
	}
	if len(services) == 0 {
		return res, errors.New("die letzte Aktualisierung hat kein Image geändert; zurück gibt es nichts")
	}
	var domains []string
	if u.Domains != nil {
		domains = u.Domains(project)
	}
	before := u.probe(ctx, domains)
	if err := u.restore(ctx, p, *fb, services, out); err != nil {
		res.Problem = "Zurückstellen ging nicht: " + short(err)
		return res, nil
	}
	if err := u.verify(ctx, p, services, before, domains, out); err != nil {
		res.Problem = "nach dem Zurückstellen: " + err.Error()
		return res, nil
	}
	now := time.Now().UTC()
	fb.Reverted = &now
	_ = u.saveJSON(fallbackFile(project), *fb)
	res.OK, res.RolledBack = true, true
	fmt.Fprintln(out, "Zurückgestellt: Es läuft wieder der Stand von vorher.")
	return res, nil
}

// RevertPlan beschreibt, was Revert täte.
func (u *Updater) RevertPlan(project string) (Plan, error) {
	fb := u.LastFallback(project)
	if fb == nil {
		return Plan{}, fmt.Errorf("%w: kein Rückfall für %s", ErrNotFound, project)
	}
	pl := Plan{Project: project, Options: []PlanService{}, Services: []string{}, Steps: []string{}}
	for _, fs := range fb.Services {
		if fs.New != "" {
			pl.Services = append(pl.Services, fs.Service)
		}
	}
	switch {
	case fb.Reverted != nil:
		pl.Problem = "Der Stand von vor dem " + fb.Time.Local().Format("02.01. 15:04") + " läuft schon wieder."
		return pl, nil
	case len(pl.Services) == 0:
		pl.Problem = "Die letzte Aktualisierung hat kein Image geändert; zurück gibt es nichts."
		return pl, nil
	}
	pl.Steps = append(pl.Steps, "Images von vor dem "+fb.Time.Local().Format("02.01. 15:04")+" bekommen ihren Namen zurück: "+strings.Join(pl.Services, ", ")+".")
	if len(fb.Git) > 0 {
		var dirs []string
		for _, g := range fb.Git {
			if g.New != "" && g.New != g.Head {
				dirs = append(dirs, g.Dir+" auf "+ShortID(g.Head))
			}
		}
		if len(dirs) > 0 {
			pl.Steps = append(pl.Steps, "Git zurück: "+strings.Join(dirs, ", ")+" (eigene Änderungen bleiben).")
		}
	}
	pl.Steps = append(pl.Steps, "Neu anlegen und starten, dann prüfen wie beim Aktualisieren.")
	pl.Back = "Wieder aktualisieren."
	if u.Domains != nil {
		pl.Domains = u.Domains(project)
	}
	sort.Strings(pl.Services)
	return pl, nil
}

// probe fragt die Domains ab.
func (u *Updater) probe(ctx context.Context, hosts []string) map[string]caddy.Answer {
	out := map[string]caddy.Answer{}
	if u.Probe == nil {
		return out
	}
	for _, h := range hosts {
		out[h] = u.Probe(ctx, h)
	}
	return out
}
