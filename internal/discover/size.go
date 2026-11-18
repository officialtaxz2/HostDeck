package discover

import (
	"context"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// largeDir: ab dieser Größe nennt der Bericht die größten Unterordner.
var largeDir int64 = 1_000_000_000

type sizeJob struct {
	path     string
	excludes []string
	set      func(size int64, partial bool, largest []inventory.SubSize)
}

// sizes berechnet die Größe aller Ordner, die gesichert würden. Alle
// Berechnungen laufen nacheinander in einem eigenen Thread mit niedriger
// CPU- und I/O-Priorität, damit der Server nicht spürbar langsamer wird.
func (s *scan) sizes(ctx context.Context) {
	var jobs []sizeJob
	for i := range s.inv.Paths {
		e := &s.inv.Paths[i]
		if !e.Exists || e.Kind == inventory.KindParent || e.CoveredBy != "" {
			continue
		}
		var ex []string
		for _, k := range e.Excludes {
			ex = append(ex, strings.TrimPrefix(k, "path:"))
		}
		jobs = append(jobs, sizeJob{e.Path, ex, func(sz int64, partial bool, largest []inventory.SubSize) {
			e.Size, e.SizePartial, e.Largest = sz, partial, largest
		}})
	}
	for i := range s.inv.Unclaimed {
		u := &s.inv.Unclaimed[i]
		jobs = append(jobs, sizeJob{u.Path, nil, func(sz int64, _ bool, _ []inventory.SubSize) { u.Size = sz }})
		for j := range u.Caches {
			c := &u.Caches[j]
			jobs = append(jobs, sizeJob{c.Path, nil, func(sz int64, _ bool, _ []inventory.SubSize) { c.Size = sz }})
		}
	}
	for i := range s.inv.Managed {
		m := &s.inv.Managed[i]
		if m.Path == "" {
			continue
		}
		jobs = append(jobs, sizeJob{m.Path, nil, func(sz int64, _ bool, _ []inventory.SubSize) { m.Size = sz }})
	}
	for i := range s.inv.Volumes {
		v := &s.inv.Volumes[i]
		if v.Size >= 0 || v.Mountpoint == "" {
			continue
		}
		jobs = append(jobs, sizeJob{v.Mountpoint, nil, func(sz int64, _ bool, _ []inventory.SubSize) { v.Size = sz }})
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Thread festhalten und nicht wieder freigeben: Endet die Goroutine,
		// beendet Go den Thread samt seiner niedrigen Priorität.
		runtime.LockOSThread()
		lowerPriority()
		for _, j := range jobs {
			if ctx.Err() != nil {
				return
			}
			s.logf("  Größe: %s", j.path)
			sz, partial, largest := s.dirSize(ctx, j.path, j.excludes)
			j.set(sz, partial, largest)
			if partial {
				s.addFinding(inventory.LevelInfo, "groesse-unvollstaendig", "path:"+j.path, "Die Größe von "+j.path+" ist unvollständig (Zeitlimit "+s.o.SizeTimeout.String()+" erreicht).")
			}
		}
		s.dropSmallCaches()
		s.logf("  Platzübersicht der ganzen Platte")
		s.space(ctx)
	}()
	<-done
}

// minCache: Kleinere Caches lohnen keine eigene Frage in der Auswahl.
var minCache int64 = 1_000_000

func (s *scan) dropSmallCaches() {
	for i := range s.inv.Unclaimed {
		u := &s.inv.Unclaimed[i]
		var keep []inventory.SubSize
		for _, c := range u.Caches {
			if c.Size < 0 || c.Size >= minCache {
				keep = append(keep, c)
			}
		}
		u.Caches = keep
	}
}

// dirSize zählt den belegten Platz unter p ohne die Ordner in excludes.
// Symlinks werden nicht verfolgt, Hardlinks nur einmal gezählt.
func (s *scan) dirSize(ctx context.Context, p string, excludes []string) (int64, bool, []inventory.SubSize) {
	root := s.d.FS.Real(p)
	ex := map[string]bool{}
	for _, e := range excludes {
		ex[s.d.FS.Real(e)] = true
	}
	deadline := time.Now().Add(s.o.SizeTimeout)
	seen := map[[2]uint64]bool{}
	children := map[string]int64{}
	var total int64
	partial := false
	_ = filepath.WalkDir(root, func(fp string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			partial = true
			return fs.SkipAll
		}
		if d.IsDir() && ex[fp] {
			return fs.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		size, dev, ino, links := diskUsage(info)
		if links > 1 && !d.IsDir() {
			k := [2]uint64{dev, ino}
			if seen[k] {
				return nil
			}
			seen[k] = true
		}
		total += size
		if rel, err := filepath.Rel(root, fp); err == nil && rel != "." {
			first, _, _ := strings.Cut(rel, string(filepath.Separator))
			children[first] += size
		}
		return nil
	})
	var largest []inventory.SubSize
	if total >= largeDir {
		for name, sz := range children {
			largest = append(largest, inventory.SubSize{Path: filepath.ToSlash(filepath.Join(p, name)), Size: sz})
		}
		sort.Slice(largest, func(i, j int) bool {
			if largest[i].Size != largest[j].Size {
				return largest[i].Size > largest[j].Size
			}
			return largest[i].Path < largest[j].Path
		})
		if len(largest) > 5 {
			largest = largest[:5]
		}
	}
	return total, partial, largest
}
