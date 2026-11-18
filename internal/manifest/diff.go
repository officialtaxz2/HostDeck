package manifest

import (
	"os"
	"sort"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// Diff vergleicht ein bestehendes Manifest mit einem neuen Inventar.
type Diff struct {
	New  []string // im Inventar, aber nicht im Manifest: braucht eine Entscheidung
	Gone []string // im Manifest, aber nicht mehr auf dem Server
}

func (d Diff) Empty() bool { return len(d.New) == 0 && len(d.Gone) == 0 }

// Keys gibt alle Schlüssel zurück, zu denen das Manifest eine Entscheidung
// enthält.
func (m *Manifest) Keys() []string {
	set := map[string]bool{}
	for name, p := range m.Projects {
		set["project:"+name] = true
		if p == nil {
			continue
		}
		for svc := range p.Services {
			set["compose:"+name+"/"+svc] = true
		}
		for mn := range p.Managed {
			set["managed:"+mn] = true
		}
		for k := range p.Data {
			set[k] = true
		}
	}
	for k := range m.Unassigned {
		set[k] = true
	}
	for k := range m.Containers {
		set[k] = true
	}
	if m.Host != nil && m.Host.Caddy != nil {
		set["host:caddy"] = true
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Compare ermittelt, was seit dem Manifest neu dazugekommen oder
// weggefallen ist. Eigene Unterpfade eines erkannten Ordners oder
// Projektordners (etwa path:/opt/calagopus/wings/backups mit skip) gelten
// nicht als weggefallen.
func Compare(m *Manifest, inv *inventory.Inventory) Diff {
	want := Propose(inv).Keys()
	have := m.Keys()
	inWant := map[string]bool{}
	for _, k := range want {
		inWant[k] = true
	}
	inHave := map[string]bool{}
	for _, k := range have {
		inHave[k] = true
	}
	parents := append([]string{}, want...)
	for _, p := range inv.Projects {
		if p.Path != "" {
			parents = append(parents, "path:"+p.Path)
		}
	}
	var d Diff
	for _, k := range want {
		if parent := splitParent(k, want); parent != "" && inHave[parent] {
			continue // vorgeschlagener Unterordner eines bekannten Eintrags
		}
		if !inHave[k] {
			d.New = append(d.New, k)
		}
	}
	for _, k := range have {
		if inWant[k] || isUserSplit(k, parents) {
			continue
		}
		d.Gone = append(d.Gone, k)
	}
	return d
}

func isUserSplit(key string, known []string) bool {
	p, ok := strings.CutPrefix(key, "path:")
	if !ok {
		return false
	}
	for _, k := range known {
		if parent, ok := strings.CutPrefix(k, "path:"); ok && strings.HasPrefix(p, parent+"/") {
			return true
		}
	}
	return false
}

// splitParent gibt den Eintrag zurück, unter dem der Pfad-Schlüssel k liegt,
// wenn es unter keys einen gibt.
func splitParent(k string, keys []string) string {
	p, ok := strings.CutPrefix(k, "path:")
	if !ok {
		return ""
	}
	best := ""
	for _, other := range keys {
		if parent, ok := strings.CutPrefix(other, "path:"); ok && strings.HasPrefix(p, parent+"/") && len(other) > len(best) {
			best = other
		}
	}
	return best
}

// Missing lässt aus weggefallenen Schlüsseln die Pfade weg, die es auf dem
// Server noch gibt. Solche Einträge fehlen nur in der Bestandsaufnahme,
// etwa weil HostDeck sie inzwischen zum System zählt (/vmlinuz unter Debian)
// oder ohne Größen nicht mehr vorschlägt. Backup und Leitstand nennen sie
// deshalb nicht „nicht mehr gefunden“.
func Missing(keys []string) []string {
	var out []string
	for _, k := range keys {
		if p, ok := strings.CutPrefix(k, "path:"); ok {
			if _, err := os.Lstat(p); err == nil {
				continue
			}
		}
		out = append(out, k)
	}
	return out
}
