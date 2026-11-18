package backup

import (
	"encoding/json"
	"os"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// LoadInventory liest inventory.json von hostdeck discover.
func LoadInventory(path string) (*inventory.Inventory, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var inv inventory.Inventory
	if err := json.Unmarshal(b, &inv); err != nil {
		return nil, err
	}
	return &inv, nil
}

// FillSizes übernimmt Größen aus dem gespeicherten Inventar von HostDeck
// discover in das frisch erfasste. Der frische Lauf misst nicht, weil das
// Minuten dauert; für Plan und Fortschritt reichen die Größen vom letzten
// discover. Funde der Platzsuche, die nur discover mit Größen findet,
// kommen dazu, solange es sie noch gibt.
func FillSizes(fresh, stored *inventory.Inventory) {
	if fresh == nil || stored == nil {
		return
	}
	paths := map[string]inventory.Path{}
	for _, p := range stored.Paths {
		paths[p.Key] = p
	}
	for i := range fresh.Paths {
		p := &fresh.Paths[i]
		if s, ok := paths[p.Key]; ok && p.Size < 0 && s.Path == p.Path {
			p.Size, p.SizePartial, p.Largest = s.Size, s.SizePartial, s.Largest
		}
	}
	vols := map[string]inventory.Volume{}
	for _, v := range stored.Volumes {
		vols[v.Key] = v
	}
	for i := range fresh.Volumes {
		v := &fresh.Volumes[i]
		if s, ok := vols[v.Key]; ok && v.Size < 0 {
			v.Size = s.Size
		}
	}
	managed := map[string]inventory.Managed{}
	for _, m := range stored.Managed {
		managed[m.Key] = m
	}
	for i := range fresh.Managed {
		m := &fresh.Managed[i]
		if s, ok := managed[m.Key]; ok && m.Size < 0 && s.Path == m.Path {
			m.Size = s.Size
		}
	}
	unclaimed := map[string]inventory.Unclaimed{}
	have := map[string]bool{}
	for _, u := range fresh.Unclaimed {
		have[u.Key] = true
	}
	for _, u := range stored.Unclaimed {
		unclaimed[u.Key] = u
		if !have[u.Key] && u.Area == inventory.AreaFound {
			if _, err := os.Lstat(u.Path); err == nil {
				fresh.Unclaimed = append(fresh.Unclaimed, u)
			}
		}
	}
	for i := range fresh.Unclaimed {
		u := &fresh.Unclaimed[i]
		s, ok := unclaimed[u.Key]
		if !ok || s.Path != u.Path {
			continue
		}
		if u.Size < 0 {
			u.Size = s.Size
		}
		sizes := map[string]int64{}
		for _, c := range s.Caches {
			sizes[c.Path] = c.Size
		}
		for j := range u.Caches {
			if n, ok := sizes[u.Caches[j].Path]; ok && u.Caches[j].Size < 0 {
				u.Caches[j].Size = n
			}
		}
	}
}
