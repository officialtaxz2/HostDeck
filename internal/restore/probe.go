package restore

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

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/betrieb"
)

// Probe-Kopie anhalten und entfernen. Ein Probe-Umzug lässt
// auf dem neuen Server laufende Projekte zurück. Anhalten stoppt sie und
// setzt ihre Neustart-Regel auf „no“; Entfernen löscht genau das, was
// wiederherstellung.json als von HostDeck angelegt nennt. Beides fragt vorher
// und zeigt, was passiert.

// ProbeDocker ist, was Anhalten und Entfernen von Docker brauchen.
type ProbeDocker interface {
	ProjectContainers(ctx context.Context, project string) ([]ContainerState, error)
	SetRestart(ctx context.Context, ids []string, policy string) error
	Stop(ctx context.Context, ids []string) error
	ComposeDown(ctx context.Context, project string) error
	ProjectVolumes(ctx context.Context, project string) ([]string, error)
	ProjectDir(ctx context.Context, project string) string
	VolumeRemove(ctx context.Context, name string) error
	ImageRemove(ctx context.Context, ref string) error
	// MountUsers: Container (Name) mit ihren Host-Ordnern.
	MountUsers(ctx context.Context) (map[string][]string, error)
	RemoveContainers(ctx context.Context, names []string) error
}

// Probe hält eine Probe-Kopie an oder entfernt sie.
type Probe struct {
	StateDir string // mit wiederherstellung.json, passwort, cache
	Docker   ProbeDocker
	Input    backup.Input
	Out      io.Writer
	Hostname string
	// UserStoppedFile: gestoppt.json; das Zurückspielen hat dort
	// eingetragen, was du auf dem alten Server gestoppt hattest.
	UserStoppedFile string
	// OwnBackups ist der Backup-Ordner von HostDeck auf diesem Server. Liegt
	// darin etwas, ist das kein Probe-Server: Entfernen verweigert.
	OwnBackups string
	RemoveAll  func(string) error
	Now        func() time.Time
	// Caddy: Hat das Zurückspielen Caddy eingerichtet, hält Anhalten es an
	// und Entfernen nimmt es wieder weg. nil: Caddy bleibt unberührt.
	Caddy CaddyHost
	// Leitstand: HostDeck als Dienst, falls der Probe-Umzug ihn als Probe-Kopie
	// eingerichtet hat. nil: bleibt unberührt.
	Leitstand LeitstandHost
	// Confirmed: Entfernen ist schon bestätigt (Umzugstag). Dann fragt
	// Remove nicht, und ohne Probe-Kopie gibt es nichts zu tun.
	Confirmed bool
}

// LeitstandHost ist der Leitstand von HostDeck auf diesem Server.
type LeitstandHost interface {
	// Copy: Der Leitstand hier ist eine Probe-Kopie (probe-kopie.json).
	Copy() bool
	// Stop hält ihn an; nach einem Neustart bleibt er aus.
	Stop(ctx context.Context) error
	// Remove nimmt Dienste, Programm und Daten weg.
	Remove(ctx context.Context) error
}

func (p *Probe) leitstand() bool { return p.Leitstand != nil && p.Leitstand.Copy() }

func (p *Probe) printf(format string, args ...any) { fmt.Fprintf(p.Out, format, args...) }

func (p *Probe) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// record liest wiederherstellung.json und zeigt, woher die Kopie kommt.
func (p *Probe) record() (*Record, bool) {
	rec, err := LoadRecord(p.StateDir)
	if err != nil {
		p.printf("  ✗ Hier liegt keine Probe-Kopie von HostDeck (%v).\n", err)
		return nil, false
	}
	if rec.Adopted != nil {
		p.printf("  ✗ %s. HostDeck hält hier nichts an und entfernt nichts.\n", rec.AdoptedText())
		return nil, false
	}
	p.printf("Probe-Kopie von %s, Lauf %s, eingespielt am %s mit HostDeck %s\n", rec.Source, rec.Run,
		rec.Restored.Local().Format("02.01.2006 15:04"), rec.Version)
	if rec.Halted != nil {
		p.printf("Angehalten am %s\n", rec.Halted.Local().Format("02.01.2006 15:04"))
	}
	return rec, true
}

func (p *Probe) ask(ctx context.Context, want string, q backup.Question) bool {
	p.printf("\n")
	ans, err := p.Input.Ask(ctx, q)
	if err != nil || !strings.EqualFold(strings.TrimSpace(ans), want) {
		p.printf("Nicht gestartet. Verändert wurde nichts.\n")
		return false
	}
	return true
}

// Halt stoppt alle Container der Probe-Kopie und setzt ihre
// Neustart-Regel auf „no“.
func (p *Probe) Halt(ctx context.Context) int {
	p.printf("HostDeck: Probe-Kopie anhalten auf %s\n\n", p.Hostname)
	rec, ok := p.record()
	if !ok {
		return ExitBlocked
	}
	type proj struct {
		name    string
		all     []string
		running []string
	}
	var projs []proj
	total := 0
	p.printf("\n")
	for _, g := range rec.Groups {
		if !g.Compose {
			continue
		}
		cs, err := p.Docker.ProjectContainers(ctx, g.Name)
		if err != nil {
			p.printf("  ✗ %s: %v\n", g.Name, err)
			return ExitBlocked
		}
		x := proj{name: g.Name}
		for _, c := range cs {
			x.all = append(x.all, c.ID)
			if c.Running {
				x.running = append(x.running, c.ID)
			}
		}
		if len(x.all) == 0 {
			continue
		}
		total += len(x.running)
		p.printf("  %s: %d Container, davon %d laufen\n", g.Name, len(x.all), len(x.running))
		projs = append(projs, x)
	}
	caddy := rec.Caddy != nil && p.Caddy != nil
	if caddy {
		p.printf("  Caddy (von HostDeck eingerichtet)\n")
	}
	ls := p.leitstand()
	if ls {
		p.printf("  Leitstand von HostDeck (Probe-Kopie)\n")
	}
	if len(projs) == 0 && !caddy && !ls {
		p.printf("  Von der Probe-Kopie gibt es hier keine Container mehr. Nichts zu tun.\n")
		return ExitOK
	}
	also := ""
	switch {
	case caddy && ls:
		also = " Caddy und der Leitstand der Kopie halten ebenfalls an."
	case caddy:
		also = " Caddy hält ebenfalls an."
	case ls:
		also = " Der Leitstand der Kopie hält ebenfalls an."
	}
	if !p.ask(ctx, "anhalten", backup.Question{
		Text:    "Probe-Kopie anhalten? Tippe anhalten:",
		Note:    fmt.Sprintf("Auf diesem Server (Rechnername %s) halten alle Container der Probe-Kopie an (%d laufen gerade) und starten auch nach einem Neustart nicht von selbst.%s Daten und Container bleiben liegen; entfernen kannst du sie danach immer noch.", p.Hostname, total, also),
		Choices: []backup.Choice{{Label: "Probe-Kopie anhalten", Answer: "anhalten"}, {Label: "Nicht anhalten", Answer: "nein"}},
	}) {
		return ExitBlocked
	}
	failed := false
	for _, x := range projs {
		if err := p.Docker.SetRestart(ctx, x.all, "no"); err != nil {
			p.printf("  ✗ %s: Neustart-Regel: %v\n", x.name, err)
			failed = true
		}
		if err := p.Docker.Stop(ctx, x.running); err != nil {
			p.printf("  ✗ %s: anhalten: %v\n", x.name, err)
			failed = true
			continue
		}
		p.printf("  ✓ %s angehalten\n", x.name)
	}
	if !p.stopCaddy(ctx, rec) {
		failed = true
	}
	if ls {
		if err := p.Leitstand.Stop(ctx); err != nil {
			p.printf("  ✗ Leitstand anhalten: %v\n", err)
			failed = true
		} else {
			p.printf("  ✓ Leitstand der Kopie angehalten (bleibt auch nach einem Neustart aus)\n")
		}
	}
	now := p.now().UTC()
	rec.Halted = &now
	if err := p.save(rec); err != nil {
		p.printf("Hinweis: %s nicht gespeichert: %v\n", RecordPath(p.StateDir), err)
	}
	if failed {
		p.printf("\nErgebnis: nicht alles angehalten, siehe ✗.\n")
		return ExitFailed
	}
	p.printf("\nErgebnis: Die Probe-Kopie ist angehalten und startet nicht von selbst.\n")
	return ExitOK
}

func (p *Probe) save(rec *Record) error {
	j := &Job{StateDir: p.StateDir}
	return j.saveRecord(rec)
}

// removal ist, was Entfernen löschen würde.
type removal struct {
	projects []string
	volumes  []string
	dirs     []string
	kept     []string // Ordner, die bleiben, mit Grund
	images   []string
	stopped  []string // Namen in gestoppt.json
	guessed  bool     // Projektordner aus den Etiketten (älterer Stand)
	// users: Container außerhalb der Projekte, die Ordner der Kopie nutzen,
	// etwa Spielserver, die ein Panel der Kopie angelegt hat.
	users []string
}

func (p *Probe) collect(ctx context.Context, rec *Record) removal {
	var r removal
	var dirs []string
	for _, g := range rec.Groups {
		if g.Compose {
			r.projects = append(r.projects, g.Name)
			dir := g.Dir
			if dir == "" {
				if dir = p.Docker.ProjectDir(ctx, g.Name); dir != "" {
					r.guessed = true
				}
			}
			if dir != "" {
				dirs = append(dirs, dir)
			}
		}
		dirs = append(dirs, g.Paths...)
		r.volumes = append(r.volumes, g.Volumes...)
		// Auch ohne Pfade im Projekt (Stand 2a-8243389): Der Name einer
		// Datenbank ist ihr Ordner oder ihr Volume.
		for _, db := range g.DBs {
			switch {
			case strings.HasPrefix(db.Title, "/"):
				dirs = append(dirs, db.Title)
			case strings.HasPrefix(db.Title, "Volume "):
				r.volumes = append(r.volumes, strings.TrimPrefix(db.Title, "Volume "))
			}
		}
		r.stopped = append(r.stopped, g.KeptOff...)
	}
	dirs = append(dirs, rec.Loose...)
	r.volumes = append(r.volumes, rec.Volumes...)
	r.images = uniq(rec.Images)
	r.volumes = uniq(r.volumes)
	// Nur die obersten Ordner: Was in einem gelöschten Ordner liegt, ist
	// mit weg.
	sort.Strings(dirs)
	for _, d := range uniq(dirs) {
		d = filepath.Clean(d)
		if len(r.dirs) > 0 {
			last := r.dirs[len(r.dirs)-1]
			if d == last || strings.HasPrefix(d, strings.TrimSuffix(last, "/")+"/") {
				continue
			}
		}
		if why := p.keep(d); why != "" {
			r.kept = append(r.kept, d+" ("+why+")")
			continue
		}
		r.dirs = append(r.dirs, d)
	}
	if mounts, err := p.Docker.MountUsers(ctx); err == nil {
		proj := map[string]bool{}
		for _, n := range r.projects {
			proj[n] = true
		}
		owned := map[string]bool{}
		for _, n := range r.projects {
			cs, _ := p.Docker.ProjectContainers(ctx, n)
			for _, c := range cs {
				owned[strings.TrimPrefix(c.Name, "/")] = true
			}
		}
		for name, srcs := range mounts {
			if owned[name] {
				continue
			}
			for _, src := range srcs {
				if under(src, r.dirs) {
					r.users = append(r.users, name)
					break
				}
			}
		}
		sort.Strings(r.users)
	}
	return r
}

// under meldet, ob p in einem der Ordner liegt (oder einer ist).
func under(p string, dirs []string) bool {
	p = filepath.Clean(p)
	for _, d := range dirs {
		if p == d || strings.HasPrefix(p, strings.TrimSuffix(d, "/")+"/") {
			return true
		}
	}
	return false
}

// systemDirs fasst HostDeck beim Entfernen nie an, auch nichts darin.
var systemDirs = []string{"/etc", "/usr", "/boot", "/proc", "/sys", "/dev", "/run", "/bin", "/sbin",
	"/lib", "/lib32", "/lib64", "/libx32", "/snap", "/var/lib/docker", "/var/lib/containerd",
	"/var/lib/hostdeck", "/var/log", "/var/lib/dpkg", "/var/lib/apt", "/var/cache", "/var/spool", "/var/backups"}

// keep sagt, warum HostDeck einen Ordner liegen lässt; leer: darf weg.
func (p *Probe) keep(d string) string {
	if !filepath.IsAbs(d) {
		return "kein fester Pfad"
	}
	parts := strings.Split(strings.Trim(d, "/"), "/")
	if d == "/" || len(parts) < 2 {
		return "zu weit oben"
	}
	for _, s := range systemDirs {
		if d == s || strings.HasPrefix(d, s+"/") {
			return "Systemordner"
		}
	}
	if (parts[0] == "home" || parts[0] == "root") && len(parts) < 3 {
		return "Benutzerordner"
	}
	if st := filepath.Clean(p.StateDir); d == st || strings.HasPrefix(d, st+"/") || strings.HasPrefix(st+"/", d+"/") {
		return "Ordner von HostDeck"
	}
	return ""
}

func uniq(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// Remove löscht die Probe-Kopie: Projekte samt Volumes, Ordner und Images,
// die HostDeck angelegt hat, und das Backup-Passwort auf diesem Server.
func (p *Probe) Remove(ctx context.Context) int {
	p.printf("HostDeck: Probe-Kopie entfernen auf %s\n\n", p.Hostname)
	if p.OwnBackups != "" {
		if empty, err := dirEmpty(p.OwnBackups); err == nil && !empty {
			p.printf("  ✗ Auf diesem Server liegen eigene Backups von HostDeck (%s). Das ist kein Probe-Server; HostDeck entfernt hier nichts.\n", p.OwnBackups)
			return ExitBlocked
		}
	}
	if p.Confirmed {
		if _, err := os.Stat(RecordPath(p.StateDir)); errors.Is(err, os.ErrNotExist) && !p.leitstand() {
			p.printf("  ✓ Hier liegt keine Probe-Kopie; nichts zu entfernen.\n")
			return ExitOK
		}
	}
	rec, ok := p.record()
	if !ok {
		return ExitBlocked
	}
	r := p.collect(ctx, rec)
	p.printf("\nWird endgültig gelöscht:\n")
	if len(r.projects) > 0 {
		p.printf("  Projekte mit Containern, Netzen und Volumes: %s\n", strings.Join(r.projects, ", "))
	}
	if len(r.volumes) > 0 {
		p.printf("  Volumes: %s\n", strings.Join(r.volumes, ", "))
	}
	if len(r.users) > 0 {
		p.printf("  Container, die Ordner der Kopie nutzen: %s\n", strings.Join(r.users, ", "))
	}
	for _, d := range r.dirs {
		p.printf("  Ordner %s\n", d)
	}
	if len(r.images) > 0 {
		p.printf("  %d Images (außer solchen, die noch ein anderer Container braucht)\n", len(r.images))
	}
	if rec.Caddy != nil && p.Caddy != nil {
		if rec.Caddy.Installed {
			p.printf("  Caddy: Programm, Konfiguration und Zertifikate (HostDeck hat es hier installiert)\n")
		} else {
			p.printf("  Caddy wird nur angehalten: Es war schon vor HostDeck hier.\n")
		}
	}
	ls := p.leitstand()
	if ls {
		p.printf("  Leitstand der Kopie: Dienste, Programm und Daten von HostDeck (/var/lib/hostdeck)\n")
	}
	p.printf("  Von HostDeck: Backup-Passwort, Zwischenspeicher von restic, wiederherstellung.json\n")
	if len(r.kept) > 0 {
		p.printf("\nBleibt liegen, dort löscht HostDeck nichts:\n")
		for _, k := range r.kept {
			p.printf("  %s\n", k)
		}
	}
	if r.guessed {
		p.printf("\nHinweis: Diese Probe-Kopie stammt von einer älteren Version von HostDeck. Die Projektordner kommen aus den Etiketten der Container; Ordner außerhalb davon kennt HostDeck nicht und lässt sie liegen.\n")
	}
	if !p.Confirmed && !p.ask(ctx, "entfernen", backup.Question{
		Text:    "Probe-Kopie endgültig entfernen? Tippe entfernen:",
		Note:    "Löscht auf diesem Server (Rechnername " + p.Hostname + ") alles, was oben steht. Das lässt sich nicht rückgängig machen. Der Server, von dem das Backup kommt, und das Backup selbst bleiben unberührt.",
		Choices: []backup.Choice{{Label: "Endgültig entfernen", Answer: "entfernen"}, {Label: "Nicht entfernen", Answer: "nein"}},
	}) {
		return ExitBlocked
	}
	failed := false
	fail := func(format string, args ...any) {
		failed = true
		p.printf("  ✗ "+format+"\n", args...)
	}
	p.printf("\n")
	rm := p.RemoveAll
	if rm == nil {
		rm = os.RemoveAll
	}
	if !p.removeCaddy(ctx, rec, rm) {
		failed = true
	}
	for _, name := range r.projects {
		if err := p.Docker.ComposeDown(ctx, name); err != nil {
			fail("%s: %v", name, err)
			continue
		}
		vols, _ := p.Docker.ProjectVolumes(ctx, name)
		for _, v := range vols {
			if err := p.Docker.VolumeRemove(ctx, v); err != nil {
				fail("Volume %s: %v", v, err)
			}
		}
		p.printf("  ✓ %s entfernt\n", name)
	}
	if err := p.Docker.RemoveContainers(ctx, r.users); err != nil {
		fail("Container %s: %v", strings.Join(r.users, ", "), err)
	}
	for _, v := range r.volumes {
		if err := p.Docker.VolumeRemove(ctx, v); err != nil {
			fail("Volume %s: %v", v, err)
		}
	}
	for _, d := range r.dirs {
		if err := rm(d); err != nil {
			fail("Ordner %s: %v", d, err)
		}
	}
	if len(r.dirs) > 0 {
		p.printf("  ✓ %d Ordner gelöscht\n", len(r.dirs))
	}
	inUse := 0
	for _, im := range r.images {
		if p.Docker.ImageRemove(ctx, im) != nil {
			inUse++
		}
	}
	if len(r.images) > 0 {
		p.printf("  ✓ Images gelöscht")
		if inUse > 0 {
			p.printf(" (%d bleiben, ein anderer Container braucht sie)", inUse)
		}
		p.printf("\n")
	}
	if p.UserStoppedFile != "" && len(r.stopped) > 0 {
		m := betrieb.LoadStopped(p.UserStoppedFile)
		for _, n := range r.stopped {
			delete(m, n)
		}
		if err := betrieb.SaveStopped(p.UserStoppedFile, m); err != nil {
			fail("gestoppt.json: %v", err)
		}
	}
	if ls {
		if err := p.Leitstand.Remove(ctx); err != nil {
			fail("Leitstand: %v", err)
		} else {
			p.printf("  ✓ Leitstand der Kopie entfernt\n")
		}
	}
	for _, f := range []string{filepath.Join(p.StateDir, "passwort"), filepath.Join(p.StateDir, "cache")} {
		if err := rm(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			fail("%s: %v", f, err)
		}
	}
	if failed {
		// wiederherstellung.json bleibt: Ein zweiter Versuch weiß dann noch,
		// was dazugehört.
		p.printf("\nErgebnis: nicht alles entfernt, siehe ✗. Ein zweiter Versuch räumt den Rest auf.\n")
		return ExitFailed
	}
	if err := rm(RecordPath(p.StateDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		p.printf("Hinweis: %s nicht gelöscht: %v\n", RecordPath(p.StateDir), err)
	}
	p.printf("\nErgebnis: Die Probe-Kopie ist entfernt.\n")
	return ExitOK
}
