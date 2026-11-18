package backup

import (
	"fmt"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

func TestFillSizesNimmtGroessenVomLetztenDiscover(t *testing.T) {
	dir := t.TempDir()
	u := inventory.Unknown
	stored := &inventory.Inventory{
		Paths:   []inventory.Path{{Key: "path:/opt/a", Path: "/opt/a", Size: 100, Largest: []inventory.SubSize{{Path: "/opt/a/x", Size: 40}}}},
		Volumes: []inventory.Volume{{Key: "volume:v", Size: 200}},
		Managed: []inventory.Managed{{Key: "managed:s", Path: "/srv/s", Size: 300}},
		Unclaimed: []inventory.Unclaimed{
			{Key: "path:/root", Path: "/root", Size: 50, Caches: []inventory.SubSize{{Path: "/root/.cache", Size: 7}}},
			{Key: "path:" + dir, Path: dir, Area: inventory.AreaFound, Size: 60},
			{Key: "path:/gibt/es/nicht", Path: "/gibt/es/nicht", Area: inventory.AreaFound, Size: 70},
		},
	}
	fresh := &inventory.Inventory{
		Paths:     []inventory.Path{{Key: "path:/opt/a", Path: "/opt/a", Size: u}, {Key: "path:/opt/neu", Path: "/opt/neu", Size: u}},
		Volumes:   []inventory.Volume{{Key: "volume:v", Size: u}},
		Managed:   []inventory.Managed{{Key: "managed:s", Path: "/srv/s", Size: u}},
		Unclaimed: []inventory.Unclaimed{{Key: "path:/root", Path: "/root", Size: u, Caches: []inventory.SubSize{{Path: "/root/.cache", Size: u}}}},
	}
	FillSizes(fresh, stored)
	got := fmt.Sprint(fresh.Paths[0].Size, len(fresh.Paths[0].Largest), fresh.Paths[1].Size, fresh.Volumes[0].Size,
		fresh.Managed[0].Size, fresh.Unclaimed[0].Size, fresh.Unclaimed[0].Caches[0].Size, len(fresh.Unclaimed))
	if want := "100 1 -1 200 300 50 7 2"; got != want {
		t.Errorf("Größen %s, erwartet %s", got, want)
	}
	if fresh.Unclaimed[1].Path != dir || fresh.Unclaimed[1].Size != 60 {
		t.Errorf("Fund der Platzsuche fehlt: %+v", fresh.Unclaimed)
	}
}

func TestVieleArchivordnerInEinerZeile(t *testing.T) {
	var items []Item
	for i := range 6 {
		p := fmt.Sprintf("/opt/x/test-%d", i)
		items = append(items, Item{Key: "path:" + p, Title: p, Paths: []string{p}, Archive: true, Size: 10})
	}
	items = append(items, Item{Key: "path:/opt/y", Title: "/opt/y", Paths: []string{"/opt/y"}, Archive: true, Size: -1})
	lines := archiveLines(items)
	if len(lines) != 2 || !strings.Contains(lines[0], "6 Ordner in /opt/x, zusammen 60 B") || !strings.Contains(lines[1], "/opt/y, Größe unbekannt [Archiv]") {
		t.Errorf("Zeilen: %q", lines)
	}
}
