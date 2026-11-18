package restore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/betrieb"
)

type probeDocker struct {
	conts    map[string][]ContainerState
	restart  []string
	stopped  []string
	down     []string
	volumes  map[string][]string
	removedV []string
	removedI []string
	dirs     map[string]string
	inUse    map[string]bool
	mounts   map[string][]string
	rmConts  []string
}

func (d *probeDocker) MountUsers(context.Context) (map[string][]string, error) { return d.mounts, nil }
func (d *probeDocker) RemoveContainers(_ context.Context, n []string) error {
	d.rmConts = append(d.rmConts, n...)
	return nil
}

func (d *probeDocker) ProjectContainers(_ context.Context, p string) ([]ContainerState, error) {
	return d.conts[p], nil
}
func (d *probeDocker) SetRestart(_ context.Context, ids []string, policy string) error {
	for _, id := range ids {
		d.restart = append(d.restart, id+"="+policy)
	}
	return nil
}
func (d *probeDocker) Stop(_ context.Context, ids []string) error {
	d.stopped = append(d.stopped, ids...)
	return nil
}
func (d *probeDocker) ComposeDown(_ context.Context, p string) error {
	d.down = append(d.down, p)
	return nil
}
func (d *probeDocker) ProjectVolumes(_ context.Context, p string) ([]string, error) {
	return d.volumes[p], nil
}
func (d *probeDocker) ProjectDir(_ context.Context, p string) string { return d.dirs[p] }
func (d *probeDocker) VolumeRemove(_ context.Context, n string) error {
	d.removedV = append(d.removedV, n)
	return nil
}
func (d *probeDocker) ImageRemove(_ context.Context, r string) error {
	if d.inUse[r] {
		return os.ErrPermission
	}
	d.removedI = append(d.removedI, r)
	return nil
}

func probeRig(t *testing.T, rec *Record, answers ...string) (*Probe, *probeDocker, *bytes.Buffer, *[]string) {
	t.Helper()
	dir := t.TempDir()
	if rec != nil {
		if err := (&Job{StateDir: dir}).saveRecord(rec); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "passwort"), []byte("geheim"), 0o600)
	d := &probeDocker{conts: map[string][]ContainerState{}, volumes: map[string][]string{}, dirs: map[string]string{}, inUse: map[string]bool{}}
	var out bytes.Buffer
	var removed []string
	p := &Probe{StateDir: dir, Docker: d, Input: &fakeInput{answers: answers}, Out: &out, Hostname: "neu",
		UserStoppedFile: filepath.Join(dir, "gestoppt.json"),
		RemoveAll: func(s string) error {
			removed = append(removed, s)
			if !strings.HasPrefix(s, dir+"/") {
				return nil // nie echte Ordner des Rechners löschen
			}
			return os.RemoveAll(s)
		},
		Now: func() time.Time { return time.Date(2026, 11, 3, 18, 0, 0, 0, time.UTC) }}
	return p, d, &out, &removed
}

func TestProbeAnhalten(t *testing.T) {
	rec := &Record{Run: "r1", Source: "alt", Version: "x", Groups: []GroupRecord{
		{Name: "app", Compose: true}, {Name: "lose"}, {Name: "leer", Compose: true}}}
	p, d, out, _ := probeRig(t, rec, "anhalten")
	d.conts["app"] = []ContainerState{{ID: "a1", Running: true}, {ID: "a2"}}
	if code := p.Halt(context.Background()); code != ExitOK {
		t.Fatalf("code %d\n%s", code, out)
	}
	if strings.Join(d.restart, " ") != "a1=no a2=no" || strings.Join(d.stopped, " ") != "a1" {
		t.Errorf("restart %v, stop %v", d.restart, d.stopped)
	}
	got, err := LoadRecord(p.StateDir)
	if err != nil || got.Halted == nil {
		t.Errorf("angehalten nicht vermerkt: %v %+v", err, got)
	}

	// Ohne „anhalten“ passiert nichts.
	p, d, _, _ = probeRig(t, rec, "nein")
	d.conts["app"] = []ContainerState{{ID: "a1", Running: true}}
	if code := p.Halt(context.Background()); code != ExitBlocked || len(d.restart)+len(d.stopped) != 0 {
		t.Errorf("abgelehnt: code %d, %v %v", code, d.restart, d.stopped)
	}

	// Ohne wiederherstellung.json: keine Probe-Kopie.
	p, _, out, _ = probeRig(t, nil)
	if code := p.Halt(context.Background()); code != ExitBlocked || !strings.Contains(out.String(), "keine Probe-Kopie") {
		t.Errorf("ohne Kopie: %d %s", code, out)
	}
}

func TestProbeEntfernen(t *testing.T) {
	rec := &Record{Run: "r1", Source: "alt", Version: "x",
		Loose: []string{"/srv/lose", "/etc/caddy"}, Volumes: []string{"frei"}, Images: []string{"app:1", "geteilt:2"},
		Groups: []GroupRecord{
			{Name: "app", Compose: true, Dir: "/opt/app", Paths: []string{"/opt/app", "/opt/app/data", "/srv/app-daten"}, Volumes: []string{"app_db"}, KeptOff: []string{"app-worker-1"},
				DBs: []DBRecord{{Title: "/srv/app-pg"}, {Title: "Volume app_db"}}},
			{Name: "alt", Compose: true}, // älterer Stand: Ordner aus dem Etikett
			{Name: "home", Paths: []string{"/home/demo", "/root"}},
		}}
	p, d, out, removed := probeRig(t, rec, "entfernen")
	d.volumes["app"] = []string{"app_db", "app_anon"}
	d.dirs["alt"] = "/opt/alt"
	d.inUse["geteilt:2"] = true
	d.conts["app"] = []ContainerState{{ID: "a1", Name: "app-web-1"}}
	d.mounts = map[string][]string{
		"app-web-1":   {"/opt/app/data"},            // gehört zum Projekt: compose down
		"spielserver": {"/srv/app-daten/volumes/x"}, // vom Panel der Kopie angelegt
		"fremd":       {"/srv/anderes", "/etc/localtime"},
	}
	_ = betrieb.SaveStopped(p.UserStoppedFile, map[string]time.Time{"app-worker-1": time.Now(), "anderes": time.Now()})
	if code := p.Remove(context.Background()); code != ExitOK {
		t.Fatalf("code %d\n%s", code, out)
	}
	s := out.String()
	if strings.Join(d.down, " ") != "app alt" {
		t.Errorf("compose down: %v", d.down)
	}
	sort.Strings(d.removedV)
	if strings.Join(d.removedV, " ") != "app_anon app_db app_db frei" {
		t.Errorf("Volumes: %v", d.removedV)
	}
	var dirs []string
	for _, r := range *removed {
		if !strings.HasPrefix(r, p.StateDir) {
			dirs = append(dirs, r)
		}
	}
	if strings.Join(dirs, " ") != "/opt/alt /opt/app /srv/app-daten /srv/app-pg /srv/lose" {
		t.Errorf("Ordner: %v", dirs)
	}
	if strings.Join(d.rmConts, " ") != "spielserver" {
		t.Errorf("Container in Ordnern der Kopie: %v", d.rmConts)
	}
	for _, want := range []string{"/etc/caddy (Systemordner)", "/home/demo (Benutzerordner)", "/root (zu weit oben)", "älteren Version", "1 bleiben"} {
		if !strings.Contains(s, want) {
			t.Errorf("Ausgabe ohne %q:\n%s", want, s)
		}
	}
	if m := betrieb.LoadStopped(p.UserStoppedFile); len(m) != 1 || m["anderes"].IsZero() {
		t.Errorf("gestoppt.json: %v", m)
	}
	for _, f := range []string{"passwort", "wiederherstellung.json"} {
		if _, err := os.Stat(filepath.Join(p.StateDir, f)); !os.IsNotExist(err) {
			t.Errorf("%s noch da", f)
		}
	}
}

func TestProbeEntfernenNichtAufEchtemServer(t *testing.T) {
	rec := &Record{Run: "r1", Source: "alt", Groups: []GroupRecord{{Name: "app", Compose: true, Dir: "/opt/app"}}}
	p, d, out, removed := probeRig(t, rec, "entfernen")
	p.OwnBackups = filepath.Join(t.TempDir(), "backup")
	_ = os.MkdirAll(filepath.Join(p.OwnBackups, "data"), 0o700)
	if code := p.Remove(context.Background()); code != ExitBlocked || len(d.down)+len(*removed) != 0 {
		t.Errorf("eigene Backups: code %d, down %v, gelöscht %v\n%s", code, d.down, *removed, out)
	}
	// Ohne Bestätigung passiert nichts.
	p, d, _, removed = probeRig(t, rec, "ja")
	if code := p.Remove(context.Background()); code != ExitBlocked || len(d.down)+len(*removed) != 0 {
		t.Errorf("ohne entfernen: code %d, %v %v", code, d.down, *removed)
	}
}

func TestProbeKeep(t *testing.T) {
	p := &Probe{StateDir: "/root/.hostdeck"}
	for d, want := range map[string]string{
		"/opt/app": "", "/srv/x/y": "", "/home/demo/app": "",
		"/opt": "zu weit oben", "/": "zu weit oben", "rel/x": "kein fester Pfad",
		"/var/lib/docker/volumes/x": "Systemordner", "/etc/caddy": "Systemordner", "/var/lib/hostdeck/backup": "Systemordner",
		"/home/demo": "Benutzerordner", "/root/.hostdeck/cache": "Ordner von HostDeck",
	} {
		if got := p.keep(d); got != want {
			t.Errorf("keep(%q) = %q, erwartet %q", d, got, want)
		}
	}
}
