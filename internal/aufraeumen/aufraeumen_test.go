package aufraeumen

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/restic"
)

type fakeDocker struct {
	mu        sync.Mutex
	root      string
	conts     []dockerapi.OpsContainer
	imgs      []dockerapi.Image
	vols      []dockerapi.CleanVolume
	cacheFree int64
	inUse     map[string]bool // Volumes und Image-Namen, die Docker ablehnt
	removed   []string
	pruned    int
}

func (f *fakeDocker) Containers(context.Context) ([]dockerapi.OpsContainer, error) {
	return f.conts, nil
}

func (f *fakeDocker) Images(context.Context) ([]dockerapi.Image, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]dockerapi.Image(nil), f.imgs...), nil
}

func (f *fakeDocker) Volumes(context.Context) ([]dockerapi.CleanVolume, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]dockerapi.CleanVolume(nil), f.vols...), nil
}

func (f *fakeDocker) DiskUsage(context.Context) (dockerapi.DiskUse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := dockerapi.DiskUse{BuildCache: f.cacheFree + 100, CacheFree: f.cacheFree, Images: 5 << 30, VolumeSizes: map[string]int64{}, ImageSizes: map[string]int64{}}
	for _, v := range f.vols {
		u.VolumeSizes[v.Name] = -1
	}
	return u, nil
}

func (f *fakeDocker) RemoveImage(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.inUse[ref] {
		return dockerapi.ErrInUse
	}
	f.removed = append(f.removed, "image "+ref)
	return nil
}

func (f *fakeDocker) RemoveVolume(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.inUse[name] {
		return dockerapi.ErrInUse
	}
	for i, v := range f.vols {
		if v.Name == name {
			f.vols = append(f.vols[:i], f.vols[i+1:]...)
			f.removed = append(f.removed, "volume "+name)
			return os.RemoveAll(filepath.Dir(v.Mountpoint))
		}
	}
	return dockerapi.ErrGone
}

func (f *fakeDocker) PruneBuildCache(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.cacheFree
	f.cacheFree = 0
	f.pruned++
	return n, nil
}

func (f *fakeDocker) CreateVolume(_ context.Context, name string, labels map[string]string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mp := filepath.Join(f.root, "volumes", name, "_data")
	if err := os.MkdirAll(mp, 0o755); err != nil {
		return "", err
	}
	f.vols = append(f.vols, dockerapi.CleanVolume{Name: name, Driver: "local", Mountpoint: mp, Labels: labels})
	return mp, nil
}

func resticBin(t *testing.T) string {
	t.Helper()
	p := os.Getenv("HOSTDECK_RESTIC")
	if p == "" {
		var err error
		if p, err = exec.LookPath("restic"); err != nil {
			t.Skip("restic nicht gefunden (HOSTDECK_RESTIC setzen)")
		}
	}
	if v, err := restic.BinaryVersion(context.Background(), p); err != nil || restic.CheckVersion(v) != nil {
		t.Skipf("restic %s ist zu alt oder nicht startbar (%v)", v, err)
	}
	return p
}

type rig struct {
	t      *testing.T
	d      *fakeDocker
	s      *Service
	caddy  string
	now    time.Time
	free   int64
	trash  string
	orphan string // Ort des verwaisten Volumes aus einem gelöschten Projekt
}

func vol(root, name string, labels map[string]string) dockerapi.CleanVolume {
	return dockerapi.CleanVolume{Name: name, Driver: "local", Mountpoint: filepath.Join(root, "volumes", name, "_data"), Labels: labels, CreatedAt: "2026-01-01T00:00:00Z"}
}

func newRig(t *testing.T, trash Trash) *rig {
	t.Helper()
	root := t.TempDir()
	r := &rig{t: t, now: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC), free: 50 << 30}
	anon := strings.Repeat("ab", 32)
	d := &fakeDocker{root: root, cacheFree: 3 << 30, inUse: map[string]bool{}}
	d.conts = []dockerapi.OpsContainer{
		{Names: []string{"/shop-web-1"}, Image: "shop-web", ImageID: "sha256:" + strings.Repeat("1", 64), Labels: map[string]string{labelProject: "shop"},
			Mounts: []dockerapi.MountPoint{{Type: "volume", Name: "shop_daten"}}},
		{Names: []string{"/alt-1"}, State: "exited", Image: "redis:7", ImageID: "sha256:" + strings.Repeat("2", 64)},
	}
	d.imgs = []dockerapi.Image{
		{ID: "sha256:" + strings.Repeat("1", 64), RepoTags: []string{"shop-web:latest"}, Size: 100},
		{ID: "sha256:" + strings.Repeat("2", 64), RepoTags: []string{"redis:7"}, RepoDigests: []string{"redis@sha256:x"}, Size: 200},
		{ID: "sha256:" + strings.Repeat("3", 64), RepoTags: []string{"nginx:1.25", "nginx:alt"}, RepoDigests: []string{"nginx@sha256:y"}, Size: 300},
		{ID: "sha256:" + strings.Repeat("4", 64), RepoTags: []string{"<none>:<none>"}, Size: 400},
		{ID: "sha256:" + strings.Repeat("5", 64), RepoTags: []string{"mein-bau:latest"}, Size: 500},
		{ID: "sha256:" + strings.Repeat("6", 64), RepoTags: []string{"hostdeck-rueckfall/shop-web:vorher"}, Size: 600},
	}
	nfs := vol(root, "netz", nil)
	nfs.Options = map[string]string{"type": "nfs"}
	d.vols = []dockerapi.CleanVolume{
		vol(root, "shop_daten", map[string]string{labelProject: "shop"}),
		vol(root, anon, nil),
		vol(root, "weg_db", map[string]string{labelProject: "weg", labelVolume: "db"}),
		vol(root, "shop_alt", map[string]string{labelProject: "shop"}),
		vol(root, "ruhe_daten", map[string]string{labelProject: "ruhe"}),
		vol(root, "lose", nil),
		nfs,
	}
	for _, v := range d.vols {
		if err := os.MkdirAll(filepath.Join(v.Mountpoint, "sub"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(v.Mountpoint, "sub", "daten.txt"), []byte("Inhalt von "+v.Name), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	r.orphan = d.vols[2].Mountpoint
	cdir := filepath.Join(root, "etc", "caddy")
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(cdir, "sites"), 0o755))
	r.caddy = filepath.Join(cdir, "Caddyfile")
	must(os.WriteFile(r.caddy, []byte("{\n\temail a@b.de\n}\nimport sites/*.caddy\nimport extra.bak\n"), 0o644))
	must(os.WriteFile(filepath.Join(cdir, "sites", "shop.caddy"), []byte("shop.example.org {\n\treverse_proxy 127.0.0.1:3000\n}\n"), 0o644))
	must(os.WriteFile(filepath.Join(cdir, "extra.bak"), []byte("# eingebunden\n"), 0o644))
	must(os.WriteFile(filepath.Join(cdir, "Caddyfile.bak-2026-10-13"), []byte("alte Kopie\n"), 0o640))
	must(os.WriteFile(filepath.Join(cdir, "hostdeck.caddy"), []byte("# HostDeck\n"), 0o644))
	must(os.WriteFile(filepath.Join(cdir, "notizen.txt"), []byte("bleibt\n"), 0o644))
	r.trash = filepath.Join(root, "papierkorb")
	r.d = d
	r.s = &Service{
		Docker:     d,
		Trash:      trash,
		Caddyfile:  func(context.Context) (string, error) { return r.caddy, nil },
		Disk:       func(string) (int64, int64, error) { return 100 << 30, r.free, nil },
		Dir:        r.trash,
		ProjectDir: func(p string) string { return map[string]string{"ruhe": "/srv/ruhe"}[p] },
		Now:        func() time.Time { return r.now },
	}
	return r
}

func keysOf(items []Item) map[string]Item {
	m := map[string]Item{}
	for _, i := range items {
		m[i.Key] = i
	}
	return m
}

func TestUebersicht(t *testing.T) {
	r := newRig(t, nil)
	ov := r.s.Overview(context.Background())
	if ov.Problem != "" {
		t.Fatal(ov.Problem)
	}
	m := keysOf(ov.Items)
	img := func(n string) string { return KindImage + ":sha256:" + strings.Repeat(n, 64) }
	anon := KindVolume + ":" + strings.Repeat("ab", 32)
	want := map[string]bool{ // Schlüssel → vorgeschlagen
		KindCache:                  true,
		img("3"):                   true,
		img("4"):                   true,
		img("5"):                   false,
		anon:                       true,
		KindVolume + ":weg_db":     true,
		KindVolume + ":shop_alt":   false,
		KindVolume + ":ruhe_daten": false,
		KindVolume + ":lose":       false,
		KindFile + ":" + filepath.Join(filepath.Dir(r.caddy), "Caddyfile.bak-2026-10-13"): true,
	}
	for k, pick := range want {
		it, ok := m[k]
		if !ok {
			t.Errorf("Vorschlag %s fehlt", k)
			continue
		}
		if it.Pick != pick {
			t.Errorf("%s: vorgeschlagen %v, erwartet %v", k, it.Pick, pick)
		}
	}
	if len(m) != len(want) {
		for k := range m {
			if _, ok := want[k]; !ok {
				t.Errorf("unerwarteter Vorschlag %s", k)
			}
		}
	}
	if m[KindVolume+":weg_db"].Size <= 0 || !m[KindVolume+":weg_db"].Trash {
		t.Errorf("Volume ohne Größe oder nicht in den Papierkorb: %+v", m[KindVolume+":weg_db"])
	}
	if m[KindCache].Trash || m[img("3")].Trash {
		t.Error("Build-Cache und Images brauchen keinen Papierkorb")
	}
	kept := ""
	for _, k := range ov.Kept {
		kept += k.Name + ": " + k.Why + "\n"
	}
	for _, w := range []string{"hostdeck-rueckfall/shop-web:vorher", "netz: Volume mit besonderen"} {
		if !strings.Contains(kept, w) {
			t.Errorf("bleibt ohne %q: %s", w, kept)
		}
	}
	if ov.Days != 30 || ov.Disk.Free != 50<<30 || ov.Disk.BuildCache == 0 {
		t.Errorf("Platte/Tage: %+v %d", ov.Disk, ov.Days)
	}
}

func TestVorschau(t *testing.T) {
	r := newRig(t, nil)
	ctx := context.Background()
	if _, err := r.s.Plan(ctx, nil); !errors.Is(err, ErrNothing) {
		t.Errorf("leer: %v", err)
	}
	if _, err := r.s.Plan(ctx, []string{"volume:gibtsnicht"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unbekannt: %v", err)
	}
	if _, err := r.s.Plan(ctx, []string{"volume:shop_daten"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("benutztes Volume ließ sich wählen: %v", err)
	}
	p, err := r.s.Plan(ctx, []string{KindVolume + ":weg_db", KindCache})
	if err != nil {
		t.Fatal(err)
	}
	if p.Stand == "" || p.Problem != "" || p.Need <= DefaultReserve || p.Until != "03.12.2026" || len(p.Steps) != 5 {
		t.Errorf("Vorschau: %+v", p)
	}
	if p.Items[0].Kind != KindCache {
		t.Errorf("Build-Cache zuerst: %+v", p.Items)
	}
	r.free = 1 << 30
	r.s.Refresh()
	p, _ = r.s.Plan(ctx, []string{KindVolume + ":weg_db"})
	if !strings.Contains(p.Problem, "fehlen") {
		t.Errorf("zu wenig Platz nicht gemeldet: %+v", p)
	}
	// Mit dem Build-Cache reicht es (1 GB frei + 3 GB Cache > 2 GB + Volume).
	p, _ = r.s.Plan(ctx, []string{KindVolume + ":weg_db", KindCache})
	if p.Problem != "" {
		t.Errorf("mit Build-Cache: %s", p.Problem)
	}
}

func TestLaufMitPapierkorb(t *testing.T) {
	bin := resticBin(t)
	ctx := context.Background()
	var r *rig
	tr := &ResticTrash{Bin: func(context.Context) (string, error) { return bin, nil }, Host: "test"}
	r = newRig(t, tr)
	tr.Dir = r.trash
	file := filepath.Join(filepath.Dir(r.caddy), "Caddyfile.bak-2026-10-13")
	anon := strings.Repeat("ab", 32)
	keys := []string{KindCache, KindImage + ":sha256:" + strings.Repeat("3", 64), KindImage + ":sha256:" + strings.Repeat("4", 64),
		KindVolume + ":weg_db", KindVolume + ":" + anon, KindFile + ":" + file}
	p, err := r.s.Plan(ctx, keys)
	if err != nil || p.Problem != "" {
		t.Fatal(err, p.Problem)
	}
	if _, err := r.s.Run(ctx, keys, "falsch", nil); !errors.Is(err, ErrChanged) {
		t.Fatalf("falscher Stand: %v", err)
	}
	// Ein Container nimmt das anonyme Volume, nachdem es gesichert ist.
	r.d.inUse[anon] = true
	r.d.inUse["nginx:alt"] = true
	var unselected []string
	r.s.Unselect = func(keys []string) (int, error) {
		unselected = append(unselected, keys...)
		return 1, nil
	}
	var steps []string
	msg, err := r.s.Run(ctx, keys, p.Stand, func(e Event) {
		if e.State != StateRunning {
			steps = append(steps, e.Step+"="+e.State)
		}
	})
	if err != nil {
		t.Fatalf("%v (%v)", err, steps)
	}
	t.Log(msg)
	// Nur wirklich Gelöschtes kommt aus der Auswahl, nicht das benutzte
	// anonyme Volume.
	if got, want := strings.Join(unselected, ","), KindVolume+":weg_db,path:"+file; got != want {
		t.Errorf("ausgetragen: %s, erwartet %s", got, want)
	}
	for _, w := range []string{"Aufgeräumt", "2 Einträge liegen im Papierkorb bis 03.12.2026", "bleibt", "1 Eintrag aus deiner Auswahl ausgetragen."} {
		if !strings.Contains(msg, w) {
			t.Errorf("Ergebnis ohne %q: %s", w, msg)
		}
	}
	got := strings.Join(steps, " ")
	for _, w := range []string{StepCache + "=ok", StepImages + "=ok", StepSpace + "=ok", StepSave + "=ok", StepCheck + "=ok", StepDelete + "=ok"} {
		if !strings.Contains(got, w) {
			t.Errorf("Schritt %s fehlt: %s", w, got)
		}
	}
	if r.d.pruned != 1 {
		t.Error("Build-Cache nicht geleert")
	}
	rm := strings.Join(r.d.removed, ",")
	for _, w := range []string{"image nginx:1.25", "image sha256:" + strings.Repeat("4", 64), "volume weg_db"} {
		if !strings.Contains(rm, w) {
			t.Errorf("nicht gelöscht: %s (%s)", w, rm)
		}
	}
	if strings.Contains(rm, "volume "+anon) {
		t.Error("benutztes Volume gelöscht")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		// Die Datei liegt nach dem Lauf im Papierkorb, nicht mehr neben Caddy.
		t.Errorf("Caddyfile-Kopie noch da: %v", err)
	}
	if _, err := os.Stat(r.orphan); !os.IsNotExist(err) {
		t.Errorf("Volume-Ordner noch da: %v", err)
	}
	ov := r.s.Overview(ctx)
	if len(ov.Trash) != 2 {
		t.Fatalf("Papierkorb: %+v", ov.Trash)
	}
	if b, _ := os.ReadFile(filepath.Join(r.trash, "inhalt.json")); strings.Contains(string(b), "schluessel") {
		t.Error("Schlüssel in der Liste")
	}
	if fi, err := os.Stat(filepath.Join(r.trash, "schluessel")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("Schlüssel: %v %v", fi, err)
	}

	// Zurückholen: Volume und Datei.
	for _, e := range ov.Trash {
		if _, err := r.s.Restore(ctx, e.ID, nil); err != nil {
			t.Fatalf("%s zurückholen: %v", e.Name, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(r.orphan, "sub", "daten.txt"))
	if err != nil || string(b) != "Inhalt von weg_db" {
		t.Errorf("Volume zurück: %q %v", b, err)
	}
	if fi, err := os.Stat(filepath.Join(r.orphan, "sub")); err != nil || fi.Mode().Perm() != 0o750 {
		t.Errorf("Rechte im Volume: %v %v", fi, err)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "alte Kopie\n" {
		t.Errorf("Datei zurück: %q %v", b, err)
	}
	if fi, _ := os.Stat(file); fi == nil || fi.Mode().Perm() != 0o640 {
		t.Errorf("Rechte der Datei: %v", fi)
	}
	var restored map[string]string
	for _, v := range r.d.vols {
		if v.Name == "weg_db" {
			restored = v.Labels
		}
	}
	if restored[labelProject] != "weg" || restored[labelVolume] != "db" {
		t.Errorf("Labels nicht zurück: %v", restored)
	}
	ov = r.s.Overview(ctx)
	if len(ov.Trash) != 0 {
		t.Errorf("Papierkorb nach dem Zurückholen: %+v", ov.Trash)
	}
	if it := keysOf(ov.Items)[KindVolume+":weg_db"]; it.Pick || !strings.Contains(it.Warn, "zurückgeholt") {
		t.Errorf("Zurückgeholtes wieder vorgewählt: %+v", it)
	}
	// Zweimal zurückholen geht nicht; vorhandenes wird nie überschrieben.
	if _, err := r.s.Restore(ctx, "gibtsnicht", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unbekannt: %v", err)
	}

	// Noch einmal wegräumen, dann nach 30 Tagen verfallen lassen.
	r.d.inUse = map[string]bool{}
	keys = []string{KindVolume + ":weg_db", KindFile + ":" + file}
	p, err = r.s.Plan(ctx, keys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.Run(ctx, keys, p.Stand, nil); err != nil {
		t.Fatal(err)
	}
	if msg, err := r.s.Expire(ctx); err != nil || msg != "" {
		t.Errorf("zu früh verfallen: %q %v", msg, err)
	}
	r.now = r.now.Add(31 * 24 * time.Hour)
	msg, err = r.s.Expire(ctx)
	if err != nil || !strings.Contains(msg, "2 Einträge nach 30 Tagen endgültig gelöscht") {
		t.Errorf("Verfall: %q %v", msg, err)
	}
	if snaps, err := tr.List(ctx); err != nil || len(snaps) != 0 {
		t.Errorf("Snapshots nach dem Verfall: %v %v", snaps, err)
	}
}

func TestZuWenigPlatzLoeschtNichts(t *testing.T) {
	r := newRig(t, &nullTrash{})
	ctx := context.Background()
	keys := []string{KindVolume + ":weg_db"}
	p, err := r.s.Plan(ctx, keys)
	if err != nil {
		t.Fatal(err)
	}
	r.free = 1 << 30
	_, err = r.s.Run(ctx, keys, p.Stand, nil)
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("erwartet zu wenig Platz: %v", err)
	}
	if len(r.d.removed) != 0 {
		t.Errorf("trotzdem gelöscht: %v", r.d.removed)
	}
}

func TestPruefungSchlaegtFehl(t *testing.T) {
	nt := &nullTrash{checkErr: errors.New("Paket kaputt")}
	r := newRig(t, nt)
	ctx := context.Background()
	keys := []string{KindVolume + ":weg_db", KindFile + ":" + filepath.Join(filepath.Dir(r.caddy), "Caddyfile.bak-2026-10-13")}
	p, _ := r.s.Plan(ctx, keys)
	_, err := r.s.Run(ctx, keys, p.Stand, nil)
	if err == nil || !strings.Contains(err.Error(), "nichts davon ist gelöscht") {
		t.Fatalf("Fehler: %v", err)
	}
	if len(r.d.removed) != 0 {
		t.Errorf("trotz kaputtem Papierkorb gelöscht: %v", r.d.removed)
	}
	if len(nt.forgot) != 2 {
		t.Errorf("Sicherungen nicht verworfen: %v", nt.forgot)
	}
}

type nullTrash struct {
	n        int
	checkErr error
	forgot   []string
}

func (n *nullTrash) Save(context.Context, []string, []string) (string, []string, error) {
	n.n++
	return "snap" + string(rune('0'+n.n)), nil, nil
}
func (n *nullTrash) Check(context.Context) error                           { return n.checkErr }
func (n *nullTrash) Restore(context.Context, string, string, string) error { return nil }
func (n *nullTrash) Forget(_ context.Context, ids []string) error {
	n.forgot = append(n.forgot, ids...)
	return nil
}
func (n *nullTrash) Prune(context.Context) error          { return nil }
func (n *nullTrash) List(context.Context) ([]Snap, error) { return nil, nil }

func TestKopieName(t *testing.T) {
	for name, want := range map[string]bool{
		"Caddyfile": false, "Caddyfile.bak": true, "Caddyfile.2026-10-13": true, "Caddyfile~": true,
		"caddy.conf.orig": true, "hostdeck.caddy": false, "shop.caddy": false, ".hostdeck-probe-Caddyfile": false,
		"notizen.txt": false, "backup.bak": false,
	} {
		if got := isCopyName(name); got != want {
			t.Errorf("%s: %v, erwartet %v", name, got, want)
		}
	}
}

func TestMitTag(t *testing.T) {
	for in, want := range map[string]string{"redis": "redis:latest", "redis:7": "redis:7", "localhost:5000/app": "localhost:5000/app:latest", "a@sha256:x": "a@sha256:x"} {
		if got := withTag(in); got != want {
			t.Errorf("%s → %s, erwartet %s", in, got, want)
		}
	}
}

// Images von Spielserver-Panels wählt HostDeck nie vor.
func TestPanelImagesNieVorgewaehlt(t *testing.T) {
	imgs := []dockerapi.Image{
		{ID: "sha256:" + strings.Repeat("a", 64), RepoTags: []string{"ghcr.io/pterodactyl/yolks:java_21"}, RepoDigests: []string{"ghcr.io/pterodactyl/yolks@sha256:x"}, Size: 700},
		{ID: "sha256:" + strings.Repeat("b", 64), RepoTags: []string{"redis:7"}, RepoDigests: []string{"redis@sha256:y"}, Size: 200},
	}
	items, _ := unusedImages(imgs, nil, nil)
	m := keysOf(items)
	if it := m[KindImage+":sha256:"+strings.Repeat("a", 64)]; it.Pick || !strings.Contains(it.Detail, "Spielserver") {
		t.Errorf("Panel-Image: %+v", it)
	}
	if it := m[KindImage+":sha256:"+strings.Repeat("b", 64)]; !it.Pick {
		t.Errorf("Registry-Image: %+v", it)
	}
}

// Seit dem containerd-Speicher von Docker tragen auch hier gebaute Images
// RepoDigests (03.11. auf vps1: my-neko/base, pensum-werkzeug:lokal).
// Vorgewählt bleibt nur, was eine Registry wirklich herausgibt.
func TestImagesNurVorgewaehltWennNeuLadbar(t *testing.T) {
	id := func(c string) string { return "sha256:" + strings.Repeat(c, 64) }
	imgs := []dockerapi.Image{
		{ID: id("a"), RepoTags: []string{"redis:7"}, RepoDigests: []string{"redis@sha256:y"}, Size: 200},
		{ID: id("b"), RepoTags: []string{"my-neko/base:latest"}, RepoDigests: []string{"my-neko/base@sha256:z"}, Size: 300},
		{ID: id("c"), RepoTags: []string{"ghcr.io/x/y:1"}, RepoDigests: []string{"ghcr.io/x/y@sha256:w"}, Size: 400},
		{ID: id("d"), RepoTags: []string{"ghcr.io/pelican-eggs/yolks:java_25"}, RepoDigests: []string{"ghcr.io/pelican-eggs/yolks@sha256:v"}, Size: 500},
	}
	asked := map[string]int{}
	var mu sync.Mutex
	s := &Service{Pullable: func(_ context.Context, ref string) error {
		mu.Lock()
		asked[ref]++
		mu.Unlock()
		switch ref {
		case "redis:7":
			return nil
		case "my-neko/base:latest":
			return ErrNotPullable
		}
		return errors.New("Zeitüberschreitung")
	}}
	for round := 0; round < 2; round++ {
		items, _ := unusedImages(imgs, nil, nil)
		s.checkPull(context.Background(), items)
		m := keysOf(items)
		if it := m[KindImage+":"+id("a")]; !it.Pick || it.Warn != "" || it.Detail != registryDetail {
			t.Errorf("Registry-Image: %+v", it)
		}
		if it := m[KindImage+":"+id("b")]; it.Pick || it.Warn != localWarn {
			t.Errorf("hier gebaut: %+v", it)
		}
		if it := m[KindImage+":"+id("c")]; it.Pick || !strings.Contains(it.Warn, "Zeitüberschreitung") {
			t.Errorf("nicht prüfbar: %+v", it)
		}
		if it := m[KindImage+":"+id("d")]; it.Pick || !strings.Contains(it.Detail, "Spielserver") {
			t.Errorf("Pelican-Image: %+v", it)
		}
	}
	// Antworten der Registry merkt sich HostDeck, Fehler nicht.
	if asked["redis:7"] != 1 || asked["my-neko/base:latest"] != 1 || asked["ghcr.io/x/y:1"] != 2 || asked["ghcr.io/pelican-eggs/yolks:java_25"] != 0 {
		t.Errorf("Nachfragen: %v", asked)
	}
}

// Speicherplatz legt Ordner vor dem Löschen in den Papierkorb und holt
// sie als Ganzes zurück.
func TestOrdnerInDenPapierkorb(t *testing.T) {
	bin := resticBin(t)
	ctx := context.Background()
	tr := &ResticTrash{Bin: func(context.Context) (string, error) { return bin, nil }, Host: "test"}
	r := newRig(t, tr)
	tr.Dir = r.trash
	dir := filepath.Join(t.TempDir(), "backups")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "a.tar"), []byte("Inhalt"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed := false
	n, notes, err := r.s.TrashPaths(ctx, []PathItem{{Path: dir, Size: 10, Remove: func(context.Context) (bool, error) {
		removed = true
		return false, os.RemoveAll(dir)
	}}}, nil)
	if err != nil || n != 1 || !removed {
		t.Fatalf("TrashPaths: %d %v %v", n, notes, err)
	}
	ov := r.s.Overview(ctx)
	if len(ov.Trash) != 1 || ov.Trash[0].Kind != KindPath || ov.Trash[0].Path != dir {
		t.Fatalf("Papierkorb: %+v", ov.Trash)
	}
	// Steht dort inzwischen wieder etwas, überschreibt HostDeck es nicht.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.Restore(ctx, ov.Trash[0].ID, nil); !errors.Is(err, ErrBlocked) {
		t.Fatalf("über Vorhandenes zurückgeholt: %v", err)
	}
	os.Remove(dir)
	if _, err := r.s.Restore(ctx, ov.Trash[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "sub", "a.tar")); err != nil || string(b) != "Inhalt" {
		t.Errorf("zurück: %q %v", b, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "sub")); err != nil || fi.Mode().Perm() != 0o750 {
		t.Errorf("Rechte: %v %v", fi, err)
	}
	if len(r.s.Overview(ctx).Trash) != 0 {
		t.Error("Eintrag noch im Papierkorb")
	}
	// Scheitert das Löschen, fällt die Sicherung weg und es bleibt.
	n, notes, err = r.s.TrashPaths(ctx, []PathItem{{Path: dir, Size: 10, Remove: func(context.Context) (bool, error) {
		return false, errors.New("geht nicht")
	}}}, nil)
	if err != nil || n != 0 || len(notes) != 1 || !strings.Contains(notes[0], "geht nicht") {
		t.Fatalf("Fehler beim Löschen: %d %v %v", n, notes, err)
	}
	if len(r.s.Overview(ctx).Trash) != 0 {
		t.Error("Eintrag trotz Fehler im Papierkorb")
	}
}

// Zurückholen folgt keiner Verknüpfung über dem alten Ort und legt nichts
// hin, wenn der Schutz es verbietet.
func TestZurueckholenOhneVerknuepfung(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("nur unter Linux")
	}
	bin := resticBin(t)
	ctx := context.Background()
	tr := &ResticTrash{Bin: func(context.Context) (string, error) { return bin, nil }, Host: "test"}
	r := newRig(t, tr)
	tr.Dir = r.trash
	base := t.TempDir()
	dir := filepath.Join(base, "a", "b")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("vom Container"), 0o600); err != nil {
		t.Fatal(err)
	}
	n, notes, err := r.s.TrashPaths(ctx, []PathItem{{Path: dir, Size: 10, Remove: func(context.Context) (bool, error) {
		return false, os.RemoveAll(dir)
	}}}, nil)
	if err != nil || n != 1 {
		t.Fatalf("TrashPaths: %d %v %v", n, notes, err)
	}
	id := r.s.Overview(ctx).Trash[0].ID
	// a wird durch eine Verknüpfung auf einen fremden Ordner ersetzt.
	outside := t.TempDir()
	if err := os.Remove(filepath.Join(base, "a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "a")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.Restore(ctx, id, nil); !errors.Is(err, ErrBlocked) {
		t.Fatalf("über Verknüpfung zurückgeholt: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "b")); !os.IsNotExist(err) {
		t.Fatal("im fremden Ordner gelandet")
	}
	// Verbietet der Schutz den Ort, legt HostDeck nichts hin.
	os.Remove(filepath.Join(base, "a"))
	os.Mkdir(filepath.Join(base, "a"), 0o755)
	r.s.RestoreCheck = func(_ context.Context, p string) error {
		if p != dir {
			t.Errorf("geprüft: %s", p)
		}
		return errors.New("ist jetzt geschützt")
	}
	if _, err := r.s.Restore(ctx, id, nil); !errors.Is(err, ErrBlocked) || !strings.Contains(err.Error(), "geschützt") {
		t.Fatalf("trotz Schutz: %v", err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatal("trotz Schutz hingelegt")
	}
	r.s.RestoreCheck = nil
	if _, err := r.s.Restore(ctx, id, nil); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "x")); err != nil || string(b) != "vom Container" {
		t.Fatalf("zurück: %q %v", b, err)
	}
}

// Blieb beim Löschen ein Teil stehen, bleibt die Sicherung, und
// Zurückholen legt nur das Fehlende hin.
func TestTeilweiseGeloeschtZurueckholen(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("nur unter Linux")
	}
	bin := resticBin(t)
	ctx := context.Background()
	tr := &ResticTrash{Bin: func(context.Context) (string, error) { return bin, nil }, Host: "test"}
	r := newRig(t, tr)
	tr.Dir = r.trash
	dir := filepath.Join(t.TempDir(), "alt")
	files := map[string]string{"sub/a.tar": "A", "sub/b.tar": "B", "bleibt.txt": "alt"}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	n, notes, err := r.s.TrashPaths(ctx, []PathItem{{Path: dir, Size: 10, Remove: func(context.Context) (bool, error) {
		return true, os.RemoveAll(filepath.Join(dir, "sub"))
	}}}, nil)
	if err != nil || n != 1 {
		t.Fatalf("TrashPaths: %d %v %v", n, notes, err)
	}
	ov := r.s.Overview(ctx)
	if len(ov.Trash) != 1 || !ov.Trash[0].Partial {
		t.Fatalf("Papierkorb: %+v", ov.Trash)
	}
	if err := os.WriteFile(filepath.Join(dir, "bleibt.txt"), []byte("neu"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.Restore(ctx, ov.Trash[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"sub/a.tar": "A", "sub/b.tar": "B", "bleibt.txt": "neu"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(b) != want {
			t.Errorf("%s: %q %v", name, b, err)
		}
	}
	if len(r.s.Overview(ctx).Trash) != 0 {
		t.Error("Eintrag noch im Papierkorb")
	}
}
