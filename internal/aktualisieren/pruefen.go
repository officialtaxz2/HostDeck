package aktualisieren

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Fehler, die der Dienst in Antworten übersetzt.
var (
	ErrNotFound  = errors.New("unbekannt")
	ErrBadChoice = errors.New("ungültige Auswahl")
)

// Stand eines Dienstes beim Nachsehen.
const (
	Current = "aktuell"   // nichts Neues
	Newer   = "neu"       // es gibt Neues
	Unknown = "unbekannt" // ließ sich nicht prüfen
	Pinned  = "fest"      // Image fest per Digest; Neues gibt es dafür nie
	Local   = "lokal"     // Image gibt es nur auf diesem Server; keine Registry gibt es heraus
)

// localDetail erklärt den Stand Local.
const localDetail = "das Image gibt es nur auf diesem Server: Docker kennt keine Herkunft, und die Registry gibt es nicht heraus (selbst gebaut oder privat). HostDeck kann nicht nachsehen und holt es beim Aktualisieren nicht"

// Arten von Diensten.
const (
	KindRegistry = "registry" // Image aus einer Registry
	KindBuild    = "gebaut"   // Compose baut das Image
)

// ServiceCheck ist das Ergebnis für einen Dienst.
type ServiceCheck struct {
	Service   string    `json:"dienst"`
	Container string    `json:"container"`
	Kind      string    `json:"art"`
	Image     string    `json:"image"`
	ImageID   string    `json:"image_id"`
	State     string    `json:"stand"`
	Detail    string    `json:"detail,omitempty"`
	Running   bool      `json:"laeuft"`
	Job       bool      `json:"job,omitempty"`
	Database  bool      `json:"datenbank,omitempty"`
	Git       *GitState `json:"git,omitempty"`
}

// ProjectCheck ist das Ergebnis für ein Projekt.
type ProjectCheck struct {
	Project  string         `json:"projekt"`
	Time     time.Time      `json:"zeit"`
	Services []ServiceCheck `json:"dienste"`
	Error    string         `json:"fehler,omitempty"`
}

// Newer zählt die Dienste mit Neuem.
func (p ProjectCheck) NewerCount() int {
	n := 0
	for _, s := range p.Services {
		if s.State == Newer {
			n++
		}
	}
	return n
}

// Report ist der letzte Prüfstand aller Projekte.
type Report struct {
	Time     time.Time      `json:"zeit"`
	Projects []ProjectCheck `json:"projekte"`
}

const reportFile = "pruefstand.json"

// Check sieht für ein Projekt (oder alle, wenn project leer ist) nach, ob
// es Neues gibt. Es liest nur: Registry, Git-Gegenstelle, Docker. Das
// Ergebnis merkt es sich.
func (u *Updater) Check(ctx context.Context, project string) (Report, error) {
	u.check.Lock()
	defer u.check.Unlock()
	all, err := projects(ctx, u.Docker)
	if err != nil {
		return Report{}, err
	}
	var names []string
	for n := range all {
		if project == "" || n == project {
			names = append(names, n)
		}
	}
	if project != "" && len(names) == 0 {
		return Report{}, fmt.Errorf("%w: Compose-Projekt %s", ErrNotFound, project)
	}
	sort.Strings(names)
	res := make([]ProjectCheck, len(names))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, n := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			res[i] = u.checkProject(pctx, n)
		}()
	}
	wg.Wait()
	rep := u.LastReport()
	rep.Time = time.Now().UTC()
	if project == "" {
		rep.Projects = res
	} else {
		merged := false
		for i := range rep.Projects {
			if rep.Projects[i].Project == project {
				rep.Projects[i], merged = res[0], true
			}
		}
		if !merged {
			rep.Projects = append(rep.Projects, res[0])
		}
		// Projekte, die es nicht mehr gibt, fallen heraus.
		kept := rep.Projects[:0]
		for _, p := range rep.Projects {
			if all[p.Project] != nil {
				kept = append(kept, p)
			}
		}
		rep.Projects = kept
		sort.Slice(rep.Projects, func(i, j int) bool { return rep.Projects[i].Project < rep.Projects[j].Project })
	}
	if err := u.saveJSON(reportFile, rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// LastReport ist der gemerkte Prüfstand.
func (u *Updater) LastReport() Report {
	var r Report
	if b, err := os.ReadFile(filepath.Join(u.StateDir, reportFile)); err == nil {
		_ = json.Unmarshal(b, &r)
	}
	return r
}

func (u *Updater) checkProject(ctx context.Context, name string) ProjectCheck {
	pc := ProjectCheck{Project: name, Time: time.Now().UTC(), Services: []ServiceCheck{}}
	p, err := u.load(ctx, name)
	if err != nil {
		pc.Error = short(err)
		return pc
	}
	git := map[string]*GitState{}
	gitErr := map[string]error{}
	for _, s := range p.Services {
		sc := ServiceCheck{Service: s.Name, Container: s.Container, Image: s.Ref, ImageID: s.ImageID, Running: s.Running, Job: s.Job, Database: IsDatabase(s.Ref)}
		if s.Build != nil {
			sc.Kind = KindBuild
			u.checkBuild(ctx, &sc, s, git, gitErr)
		} else {
			sc.Kind = KindRegistry
			u.checkRegistry(ctx, &sc, s)
		}
		pc.Services = append(pc.Services, sc)
	}
	return pc
}

func (u *Updater) checkRegistry(ctx context.Context, sc *ServiceCheck, s Service) {
	ref, err := ParseRef(s.Ref)
	if err != nil {
		sc.State, sc.Detail = Unknown, "Image-Name „"+s.Ref+"“ ist keine Referenz einer Registry"
		return
	}
	if ref.Pinned() {
		sc.State, sc.Detail = Pinned, "fest auf "+ShortID(ref.Digest)+" eingestellt"
		return
	}
	img, err := u.Docker.ImageInspect(ctx, s.ImageID)
	if err != nil {
		sc.State, sc.Detail = Unknown, "Image des Containers nicht lesbar: "+short(err)
		return
	}
	if u.Registry == nil {
		sc.State, sc.Detail = Unknown, "ohne Zugang zu Registries"
		return
	}
	dg, err := u.Registry.Digest(ctx, ref)
	if err != nil {
		sc.State, sc.Detail = Unknown, short(err)
		if len(img.RepoDigests) == 0 && (errors.Is(err, ErrNoAccess) || errors.Is(err, ErrNoSuchImage)) {
			sc.State, sc.Detail = Local, localDetail
		}
		return
	}
	known, same := ref.MatchDigest(img.RepoDigests, dg)
	switch {
	case same:
		sc.State = Current
	case known:
		sc.State, sc.Detail = Newer, "neueres Image für "+ref.Tag+" in der Registry"
	default:
		sc.State, sc.Detail = Unknown, "das laufende Image trägt keine Herkunft aus der Registry (etwa per docker load übertragen); Aktualisieren holt es frisch"
	}
}

func (u *Updater) checkBuild(ctx context.Context, sc *ServiceCheck, s Service, cache map[string]*GitState, errs map[string]error) {
	if !s.Build.Local {
		sc.State, sc.Detail = Unknown, "baut aus "+s.Build.Context+"; neu bauen holt den Stand von dort"
		return
	}
	g, seen := cache[s.Build.Context]
	err := errs[s.Build.Context]
	if !seen {
		g, err = u.gitState(ctx, s.Build.Context, true)
		cache[s.Build.Context], errs[s.Build.Context] = g, err
	}
	sc.Git = g
	if errors.Is(err, errNoGit) {
		sc.State, sc.Detail = Unknown, "baut aus "+s.Build.Context+" ohne Git; neu bauen holt frische Basis-Images"
		return
	}
	if g == nil {
		sc.State, sc.Detail = Unknown, short(err)
		return
	}
	// Ist der Quelltext neuer als das laufende Image, wurde nach einem
	// git pull nicht neu gebaut.
	if img, ierr := u.Docker.ImageInspect(ctx, s.ImageID); ierr == nil && !g.HeadTime.IsZero() {
		if t, perr := time.Parse(time.RFC3339Nano, img.Created); perr == nil && g.HeadTime.After(t.Add(time.Minute)) {
			sc.State, sc.Detail = Newer, "der Quelltext ("+ShortID(g.Head)+") ist neuer als das laufende Image; neu bauen"
		}
	}
	switch {
	case err != nil:
		if sc.State == "" {
			sc.State, sc.Detail = Unknown, short(err)
		}
	case g.RemoteHead == "":
		if sc.State == "" {
			sc.State, sc.Detail = Current, "Git ohne Gegenstelle; neu bauen baut den jetzigen Stand"
		}
	case g.RemoteHead == g.Head:
		if sc.State == "" {
			sc.State = Current
		}
	default:
		ahead, known := u.ahead(ctx, g)
		switch {
		case ahead:
			if sc.State == "" {
				sc.State, sc.Detail = Current, "lokal schon weiter als "+g.Remote
			}
		case known:
			sc.State, sc.Detail = Newer, "neue Commits auf "+g.Remote+"/"+g.Branch+" ("+ShortID(g.RemoteHead)+")"
		default:
			sc.State, sc.Detail = Newer, "neue Commits auf "+g.Remote+"/"+g.Branch+" ("+ShortID(g.RemoteHead)+")"
		}
	}
	if g.Dirty && sc.Detail == "" {
		sc.Detail = "nicht committete Änderungen im Git-Ordner"
	}
}

// saveJSON schreibt eine Datei in StateDir, erst daneben, dann umbenannt.
func (u *Updater) saveJSON(name string, v any) error {
	if err := os.MkdirAll(u.StateDir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(u.StateDir, name)
	tmp := p + ".neu"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
