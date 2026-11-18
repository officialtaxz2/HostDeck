package restore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Auffrischen: Liegt auf diesem Server schon, was ein früherer Lauf von
// HostDeck zurückgespielt hat (wiederherstellung.json), spielt HostDeck darüber, statt
// es vorher entfernen zu lassen. Das spart fast den ganzen Weg übers Netz:
// restic überträgt nur Teile von Dateien, die hier anders sind, gleiche
// Images bleiben liegen, und nur die Datenbanken, die HostDeck selbst angelegt
// hat, ersetzt es aus dem frischen Dump. Was nicht von HostDeck kommt, bleibt
// ein Konflikt wie bisher.

// owned ist, was der letzte Lauf hier angelegt hat.
type owned struct {
	projects map[string]bool
	volumes  map[string]bool
	paths    []string
}

func (j *Job) owned() *owned {
	if j.prev == nil {
		return nil
	}
	o := &owned{projects: map[string]bool{}, volumes: map[string]bool{}}
	for _, g := range j.prev.Groups {
		if g.Compose {
			o.projects[g.Name] = true
		}
		for _, v := range g.Volumes {
			o.volumes[v] = true
		}
		o.paths = append(o.paths, g.Paths...)
		if g.Dir != "" {
			o.paths = append(o.paths, g.Dir)
		}
	}
	for _, v := range j.prev.Volumes {
		o.volumes[v] = true
	}
	o.paths = append(o.paths, j.prev.Loose...)
	return o
}

// path sagt, ob p einer der Ordner des letzten Laufs ist oder darin liegt.
func (o *owned) path(p string) bool {
	if o == nil {
		return false
	}
	p = path.Clean(p)
	for _, d := range o.paths {
		d = path.Clean(d)
		if p == d || strings.HasPrefix(p, strings.TrimSuffix(d, "/")+"/") {
			return true
		}
	}
	return false
}

func (o *owned) volume(name string) bool { return o != nil && o.volumes[name] }

// db sagt, ob die Datenbank zu Dump d vom letzten Lauf kommt.
func (o *owned) db(d Dump) bool {
	if o == nil {
		return false
	}
	if name, ok := strings.CutPrefix(d.Key, "volume:"); ok {
		return o.volume(name)
	}
	if p, ok := strings.CutPrefix(d.Key, "path:"); ok {
		return o.path(p)
	}
	return false
}

// dbDir ist der Ordner mit den Dateien der Datenbank zu Dump d.
func (j *Job) dbDir(ctx context.Context, d Dump) (string, error) {
	if name, ok := strings.CutPrefix(d.Key, "volume:"); ok {
		dir, found, err := j.Docker.VolumeMountpoint(ctx, name)
		if err != nil {
			return "", err
		}
		if !found {
			return "", fmt.Errorf("Volume %s fehlt", name)
		}
		return dir, nil
	}
	if p, ok := strings.CutPrefix(d.Key, "path:"); ok {
		return p, nil
	}
	return "", errors.New("Ort der Datenbank unbekannt")
}

// emptyDir leert einen Ordner, ohne ihn selbst zu löschen: Besitzer und
// Rechte bleiben, wie das Projekt sie braucht. Systemordner fasst es nie an.
func emptyDir(dir string) error {
	if why := unsafeDir(dir); why != "" {
		return fmt.Errorf("%s: %s, HostDeck leert ihn nicht", dir, why)
	}
	es, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range es {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// unsafeDir sagt, warum ein Ordner nicht geleert werden darf; leer: darf.
// Volumes von Docker liegen in /var/lib/docker/volumes/<name>/_data.
func unsafeDir(d string) string {
	if !filepath.IsAbs(d) {
		return "kein fester Pfad"
	}
	d = filepath.Clean(d)
	parts := strings.Split(strings.Trim(d, "/"), "/")
	if n := len(parts); n >= 4 && parts[n-1] == "_data" && parts[n-3] == "volumes" {
		return "" // Volume von Docker, auch bei anderem Datenordner
	}
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
	return ""
}

// resetDB leert die Datenbank zu Dump d, die der letzte Lauf hier angelegt
// hat, und startet ihren Dienst neu; das Image legt dann eine leere an.
// Es liefert den Container danach.
func (j *Job) resetDB(ctx context.Context, g Group, d Dump, id string, sc Scripts) (string, error) {
	dir, err := j.dbDir(ctx, d)
	if err != nil {
		return "", err
	}
	if err := j.Docker.Stop(ctx, []string{id}); err != nil {
		return "", fmt.Errorf("anhalten: %v", err)
	}
	if err := emptyDir(dir); err != nil {
		return "", err
	}
	if err := j.Docker.ComposeUp(ctx, g, []string{d.Service}, true); err != nil {
		return "", fmt.Errorf("Start: %v", err)
	}
	if id, err = j.Docker.ServiceContainer(ctx, g.Name, d.Service); err != nil {
		return "", err
	}
	if err := j.waitReady(ctx, id, sc.Ready); err != nil {
		return "", err
	}
	return id, nil
}

// sameImage sucht hier das Image, das ein früherer Lauf aus diesem
// Backup-Image geladen hat. Leer: neu laden.
func (j *Job) sameImage(ctx context.Context, im Image) string {
	var cands []string
	if j.prev != nil {
		cands = append(cands, j.prev.ImageIDs[im.ID]...)
	}
	cands = append(cands, im.ID)
	for _, id := range cands {
		if id != "" && j.Docker.ImageExists(ctx, id) {
			return id
		}
	}
	return ""
}

// tagImage hängt die Namen eines Images an die erste ID, die es hier gibt.
func (j *Job) tagImage(ctx context.Context, ids []string, names []string) error {
	for _, tag := range names {
		var err error
		for _, id := range ids {
			if err = j.Docker.Tag(ctx, id, tag); err == nil {
				break
			}
		}
		if err != nil {
			return fmt.Errorf("Name %s: %v", tag, err)
		}
	}
	return nil
}

// carryOver übernimmt aus dem letzten Lauf, was dieser Lauf nicht mehr
// zurückgespielt hat (etwa ein Projekt, das seitdem aus der Auswahl fiel).
// Es liegt weiter hier; Entfernen soll es trotzdem kennen.
func (j *Job) carryOver(rec *Record) {
	if j.prev == nil {
		return
	}
	have := map[string]bool{}
	for _, g := range rec.Groups {
		have[g.Name] = true
	}
	for _, g := range j.prev.Groups {
		if have[g.Name] || g.Name == "(allgemein)" {
			continue
		}
		g.Services, g.Ports, g.Errors, g.NotRunning, g.DBs = nil, nil, nil, nil, nil
		rec.Groups = append(rec.Groups, g)
	}
	rec.Loose = uniq(append(rec.Loose, j.prev.Loose...))
	rec.Volumes = uniq(append(rec.Volumes, j.prev.Volumes...))
	rec.Images = uniq(append(rec.Images, j.prev.Images...))
	if rec.Caddy == nil && j.prev.Caddy != nil {
		rec.Caddy = j.prev.Caddy
	}
}
