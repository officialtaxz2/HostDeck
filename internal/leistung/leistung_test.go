package leistung

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/altserver"
	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
)

type fakeDocker struct {
	list []dockerapi.OpsContainer
	info map[string]dockerapi.Container
}

func (f *fakeDocker) List(context.Context) ([]dockerapi.OpsContainer, error) { return f.list, nil }
func (f *fakeDocker) Inspect(_ context.Context, id string) (dockerapi.Container, error) {
	return f.info[id], nil
}

type rig struct {
	s     *Service
	root  string
	calls []string
	fail  map[string]bool
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newRig(t *testing.T) *rig {
	root := t.TempDir()
	r := &rig{root: root, fail: map[string]bool{}}
	proc := filepath.Join(root, "proc")
	write(t, filepath.Join(proc, "meminfo"), "MemTotal:        8000000 kB\nMemAvailable:    3000000 kB\nSwapTotal:             0 kB\nSwapFree:              0 kB\n")
	write(t, filepath.Join(proc, "sys/vm/swappiness"), "0\n")
	write(t, filepath.Join(proc, "loadavg"), "0.52 0.40 0.31 1/300 1234\n")
	write(t, filepath.Join(proc, "stat"), "cpu  100 0 50 800 10 0 0 40 0 0\ncpu0 50 0 25 400 5 0 0 20 0 0\ncpu1 50 0 25 400 5 0 0 20 0 0\n")
	write(t, filepath.Join(proc, "swaps"), "Filename\t\t\t\tType\t\tSize\t\tUsed\t\tPriority\n")
	write(t, filepath.Join(proc, "mounts"), "/dev/vda1 / ext4 rw 0 0\nproc /proc proc rw 0 0\n")
	write(t, filepath.Join(root, "etc/fstab"), "UUID=abc / ext4 defaults 0 1\n")
	logs := filepath.Join(root, "docker")
	write(t, filepath.Join(logs, "a-json.log"), strings.Repeat("x", 3000))
	write(t, filepath.Join(logs, "a-json.log.1"), strings.Repeat("x", 1000))
	write(t, filepath.Join(logs, "b-json.log"), strings.Repeat("x", 500))
	d := &fakeDocker{
		list: []dockerapi.OpsContainer{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		info: map[string]dockerapi.Container{},
	}
	mk := func(id, name, typ string, cfg map[string]string, path string) {
		c := dockerapi.Container{ID: id, Name: "/" + name, LogPath: path}
		c.State.Running = true
		c.Config.Labels = map[string]string{"com.docker.compose.project": "neko"}
		c.HostConfig.LogConfig = dockerapi.LogConfig{Type: typ, Config: cfg}
		d.info[id] = c
	}
	mk("a", "neko-neko-1", "json-file", map[string]string{}, filepath.Join(logs, "a-json.log"))
	mk("b", "neko-db-1", "json-file", map[string]string{"max-size": "5m"}, filepath.Join(logs, "b-json.log"))
	mk("c", "panel", "journald", nil, "")
	r.s = &Service{
		Docker:   d,
		Proc:     proc,
		Etc:      filepath.Join(root, "etc"),
		SwapFile: filepath.Join(root, "swapfile-hostdeck"),
		Dir:      filepath.Join(root, "var/leistung"),
		Disk:     func(string) (int64, int64, error) { return 100e9, 50e9, nil },
		Sample:   time.Millisecond,
		Now:      func() time.Time { return time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC) },
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			r.calls = append(r.calls, name+" "+strings.Join(args, " "))
			if r.fail[name] {
				return []byte(name + ": geht nicht\n"), errors.New("exit status 1")
			}
			switch name {
			case "fallocate":
				return nil, os.WriteFile(args[len(args)-1], []byte("swap"), 0o644)
			case "dd":
				return nil, os.WriteFile(strings.TrimPrefix(args[1], "of="), []byte("swap"), 0o644)
			case "swapon":
				write(t, filepath.Join(proc, "swaps"), "Filename\tType\tSize\tUsed\tPriority\n"+args[0]+"\tfile\t2097148\t1024\t-2\n")
			case "swapoff":
				write(t, filepath.Join(proc, "swaps"), "Filename\tType\tSize\tUsed\tPriority\n")
			}
			return nil, nil
		},
	}
	return r
}

func keys(list []Suggestion) string {
	var k []string
	for _, s := range list {
		k = append(k, s.Key)
		if s.Problem != "" {
			k[len(k)-1] += "!"
		}
	}
	return strings.Join(k, ",")
}

func TestUebersicht(t *testing.T) {
	r := newRig(t)
	ov := r.s.Overview(context.Background())
	if ov.Memory.Total != 8000000*1024 || ov.Memory.Swappiness != 0 || ov.Memory.SwapTotal != 0 {
		t.Errorf("Speicher: %+v", ov.Memory)
	}
	if ov.CPU.Cores != 2 || ov.CPU.Load[0] != 0.52 {
		t.Errorf("Prozessor: %+v", ov.CPU)
	}
	if !ov.Logs.Known || ov.Logs.Limited || ov.Logs.Driver != "json-file" || ov.Logs.Total != 4500 || ov.Logs.Unlimited != 1 {
		t.Errorf("Logs: %+v", ov.Logs)
	}
	if len(ov.Containers) != 3 || ov.Containers[0].Name != "neko-neko-1" || ov.Containers[0].Size != 4000 || ov.Containers[0].Limited || !ov.Containers[1].Limited {
		t.Errorf("Container: %+v", ov.Containers)
	}
	if got := keys(ov.Suggestions); got != "logs,swap" {
		t.Errorf("Vorschläge: %s", got)
	}
}

func TestLogsBegrenzenUndZurueck(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	daemon := filepath.Join(r.root, "etc/docker/daemon.json")
	orig := "{\n  \"data-root\": \"/srv/docker\",\n  \"log-opts\": {\"labels\": \"x\"}\n}\n"
	write(t, daemon, orig)
	p, err := r.s.Plan(ctx, KeyLogs)
	if err != nil || p.Problem != "" {
		t.Fatalf("Plan: %v %+v", err, p)
	}
	if !strings.Contains(p.After, `"max-size":"10m"`) || !strings.Contains(p.After, `"labels":"x"`) || strings.Contains(p.Before, "data-root") {
		t.Errorf("Vorher/Nachher: %q → %q", p.Before, p.After)
	}
	if len(p.Notes) != 1 || !strings.Contains(p.Notes[0], "1 Container behält") {
		t.Errorf("Hinweise: %v", p.Notes)
	}
	if _, err := r.s.Apply(ctx, KeyLogs, "alt"); !errors.Is(err, ErrChanged) {
		t.Fatalf("alter Stand: %v", err)
	}
	msg, err := r.s.Apply(ctx, KeyLogs, p.Stand)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "höchstens 3 Dateien") || !strings.Contains(r.calls[0], "dockerd --validate --config-file") {
		t.Errorf("%s %v", msg, r.calls)
	}
	b, _ := os.ReadFile(daemon)
	if !strings.Contains(string(b), `"data-root": "/srv/docker"`) || !strings.Contains(string(b), `"max-file": "3"`) {
		t.Errorf("daemon.json: %s", b)
	}
	ov := r.s.Overview(ctx)
	if !ov.Logs.Limited || !ov.Logs.ByHostdeck || keys(ov.Suggestions) != "logs-zurueck,swap" {
		t.Errorf("danach: %+v %s", ov.Logs, keys(ov.Suggestions))
	}
	// Zurücknehmen legt die Datei genau wie vorher hin.
	p, _ = r.s.Plan(ctx, KeyLogsBack)
	if _, err := r.s.Apply(ctx, KeyLogsBack, p.Stand); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(daemon); string(b) != orig {
		t.Errorf("nicht wie vorher: %s", b)
	}
	if got := keys(r.s.Overview(ctx).Suggestions); got != "logs,swap" {
		t.Errorf("Vorschläge: %s", got)
	}
}

func TestLogsOhneDateiUndVonHandGeaendert(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	daemon := filepath.Join(r.root, "etc/docker/daemon.json")
	p, _ := r.s.Plan(ctx, KeyLogs)
	if _, err := r.s.Apply(ctx, KeyLogs, p.Stand); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(daemon); err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("neu angelegt: %v %v", fi, err)
	}
	// Jemand ändert die Datei: HostDeck nimmt nichts zurück.
	write(t, daemon, "{\"log-opts\":{\"max-size\":\"50m\"}}\n")
	p, _ = r.s.Plan(ctx, KeyLogsBack)
	if !strings.Contains(p.Problem, "geändert") {
		t.Errorf("Problem: %q", p.Problem)
	}
	if _, err := r.s.Apply(ctx, KeyLogsBack, p.Stand); err == nil {
		t.Error("zurückgenommen trotz Änderung")
	}
	if got := keys(r.s.Overview(ctx).Suggestions); got != "swap" {
		t.Errorf("Vorschläge: %s", got)
	}
	// Ohne Vorher-Datei: zurück heißt löschen.
	os.Remove(daemon)
	r2 := newRig(t)
	p, _ = r2.s.Plan(ctx, KeyLogs)
	r2.s.Apply(ctx, KeyLogs, p.Stand)
	p, _ = r2.s.Plan(ctx, KeyLogsBack)
	if _, err := r2.s.Apply(ctx, KeyLogsBack, p.Stand); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r2.root, "etc/docker/daemon.json")); !os.IsNotExist(err) {
		t.Errorf("daemon.json noch da: %v", err)
	}
}

func TestLogsNichtAnfassen(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct{ file, want string }{
		"kaputt":   {"{ nein", "kein gültiges JSON"},
		"journald": {`{"log-driver":"journald"}`, "schon eine Log-Grenze"},
		"local":    {`{"log-driver":"local"}`, "schon eine Log-Grenze"},
	} {
		r := newRig(t)
		write(t, filepath.Join(r.root, "etc/docker/daemon.json"), tc.file)
		p, _ := r.s.Plan(ctx, KeyLogs)
		if !strings.Contains(p.Problem, tc.want) {
			t.Errorf("%s: %q", name, p.Problem)
		}
		if _, err := r.s.Apply(ctx, KeyLogs, p.Stand); err == nil {
			t.Errorf("%s: geändert", name)
		}
	}
	// dockerd mit --log-opt im Startbefehl.
	r := newRig(t)
	write(t, filepath.Join(r.s.Proc, "77/comm"), "dockerd\n")
	write(t, filepath.Join(r.s.Proc, "77/cmdline"), "/usr/bin/dockerd\x00-H\x00fd://\x00--log-opt\x00max-size=1m\x00")
	if p, _ := r.s.Plan(ctx, KeyLogs); !strings.Contains(p.Problem, "Startbefehl") {
		t.Errorf("Startbefehl: %q", p.Problem)
	}
	// Ein zweiter dockerd (Docker in Docker) davor zählt nicht; der von
	// systemd gestartete (Elternprozess 1) gilt.
	r = newRig(t)
	write(t, filepath.Join(r.s.Proc, "50/comm"), "dockerd\n")
	write(t, filepath.Join(r.s.Proc, "50/cmdline"), "dockerd\x00--config-file\x00/srv/dind/daemon.json\x00")
	write(t, filepath.Join(r.s.Proc, "50/stat"), "50 (dockerd) S 4711 50 50 0")
	write(t, filepath.Join(r.s.Proc, "60/comm"), "dockerd\n")
	write(t, filepath.Join(r.s.Proc, "60/cmdline"), "/usr/bin/dockerd\x00-H\x00fd://\x00")
	write(t, filepath.Join(r.s.Proc, "60/stat"), "60 (dockerd) S 1 60 60 0")
	if p, _ := r.s.Plan(ctx, KeyLogs); strings.Contains(p.File, "dind") || p.Problem != "" {
		t.Errorf("zweiter dockerd: %q %q", p.File, p.Problem)
	}
	// Docker lehnt die Datei ab: nichts geschrieben.
	r = newRig(t)
	r.fail["dockerd"] = true
	p, _ := r.s.Plan(ctx, KeyLogs)
	if _, err := r.s.Apply(ctx, KeyLogs, p.Stand); err == nil || !strings.Contains(err.Error(), "lehnt") {
		t.Errorf("abgelehnt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.root, "etc/docker/daemon.json")); !os.IsNotExist(err) {
		t.Error("trotzdem geschrieben")
	}
}

func TestSwapAnlegenUndEntfernen(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	p, err := r.s.Plan(ctx, KeySwap)
	if err != nil || p.Problem != "" {
		t.Fatalf("%v %+v", err, p)
	}
	if !strings.Contains(p.Steps[0], "2 GB") || len(p.Notes) != 1 || !strings.Contains(p.Notes[0], "auf 0") {
		t.Errorf("Plan: %+v", p)
	}
	msg, err := r.s.Apply(ctx, KeySwap, p.Stand)
	if err != nil {
		t.Fatal(err)
	}
	f := r.s.SwapFile
	if fi, err := os.Stat(f); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("Datei: %v %v", fi, err)
	}
	tab, _ := os.ReadFile(filepath.Join(r.root, "etc/fstab"))
	if !strings.HasPrefix(string(tab), "UUID=abc") || !strings.Contains(string(tab), f+" none swap sw 0 0\n") {
		t.Errorf("fstab: %s", tab)
	}
	if !strings.Contains(msg, "2 GB") || !strings.HasPrefix(r.calls[0], "fallocate -l 2147483648 ") {
		t.Errorf("%s %v", msg, r.calls)
	}
	// /proc/meminfo meldet jetzt Swap; HostDeck bietet das Entfernen an.
	write(t, filepath.Join(r.s.Proc, "meminfo"), "MemTotal: 8000000 kB\nMemAvailable: 3000000 kB\nSwapTotal: 2097148 kB\nSwapFree: 2096124 kB\n")
	ov := r.s.Overview(ctx)
	if !ov.Memory.HostdeckSwap || keys(ov.Suggestions) != "logs,swap-weg" {
		t.Errorf("danach: %+v %s", ov.Memory, keys(ov.Suggestions))
	}
	p, _ = r.s.Plan(ctx, KeySwapOff)
	if p.Problem != "" || len(p.Steps) != 3 {
		t.Fatalf("Plan weg: %+v", p)
	}
	if _, err := r.s.Apply(ctx, KeySwapOff, p.Stand); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Error("Datei noch da")
	}
	if tab, _ := os.ReadFile(filepath.Join(r.root, "etc/fstab")); string(tab) != "UUID=abc / ext4 defaults 0 1\n" {
		t.Errorf("fstab nicht wie vorher: %q", tab)
	}
}

func TestSwapFehlerRaeumtAuf(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.fail["mkswap"] = true
	p, _ := r.s.Plan(ctx, KeySwap)
	if _, err := r.s.Apply(ctx, KeySwap, p.Stand); err == nil || !strings.Contains(err.Error(), "mkswap") {
		t.Fatalf("Fehler: %v", err)
	}
	if _, err := os.Stat(r.s.SwapFile); !os.IsNotExist(err) {
		t.Error("halbe Datei liegt noch")
	}
	if tab, _ := os.ReadFile(filepath.Join(r.root, "etc/fstab")); strings.Contains(string(tab), "swap") {
		t.Errorf("fstab geändert: %s", tab)
	}
	// fallocate geht nicht: dd springt ein.
	r = newRig(t)
	r.fail["fallocate"] = true
	p, _ = r.s.Plan(ctx, KeySwap)
	if _, err := r.s.Apply(ctx, KeySwap, p.Stand); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.calls[1], "dd if=/dev/zero") {
		t.Errorf("kein dd: %v", r.calls)
	}
}

func TestSwapGrenzen(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.s.Disk = func(string) (int64, int64, error) { return 20e9, 6e9, nil }
	if p, _ := r.s.Plan(ctx, KeySwap); !strings.Contains(p.Problem, "Zu wenig Platz") {
		t.Errorf("Platz: %q", p.Problem)
	}
	r = newRig(t)
	write(t, filepath.Join(r.s.Proc, "mounts"), "/dev/vda1 / btrfs rw 0 0\n")
	if p, _ := r.s.Plan(ctx, KeySwap); !strings.Contains(p.Problem, "btrfs") {
		t.Errorf("btrfs: %q", p.Problem)
	}
	r = newRig(t)
	write(t, filepath.Join(r.s.Proc, "meminfo"), "MemTotal: 1000000 kB\nMemAvailable: 500000 kB\nSwapTotal: 0 kB\n")
	if p, _ := r.s.Plan(ctx, KeySwap); !strings.Contains(p.Steps[0], "1 GB") {
		t.Errorf("klein: %+v", p)
	}
	// Swap voll belegt und kaum Speicher frei: Entfernen wartet.
	r = newRig(t)
	p, _ := r.s.Plan(ctx, KeySwap)
	r.s.Apply(ctx, KeySwap, p.Stand)
	write(t, filepath.Join(r.s.Proc, "swaps"), "Filename\tType\tSize\tUsed\tPriority\n"+r.s.SwapFile+"\tfile\t2097148\t2000000\t-2\n")
	write(t, filepath.Join(r.s.Proc, "meminfo"), "MemTotal: 8000000 kB\nMemAvailable: 1000000 kB\nSwapTotal: 2097148 kB\n")
	if p, _ := r.s.Plan(ctx, KeySwapOff); !strings.Contains(p.Problem, "zu wenig Arbeitsspeicher") {
		t.Errorf("voll: %q", p.Problem)
	}
	if _, err := r.s.Plan(ctx, "alles"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unbekannt: %v", err)
	}
}

func TestVorschlagWieAufDemAltenServer(t *testing.T) {
	r := newRig(t)
	// Der alte Server: HostDeck hatte Logs begrenzt und Swap angelegt.
	old := t.TempDir()
	write(t, filepath.Join(old, "leistung", "zustand.json"), `{"logs":{"datei":"/etc/docker/daemon.json"},"swap":{"datei":"/swapfile-hostdeck","groesse":2147483648}}`)
	info := altserver.Build(old, "alt")
	if !info.Logs || info.Swap != 2<<30 {
		t.Fatalf("vom alten Server: %+v", info)
	}
	r.s.Old = func() *altserver.Info { return &info }
	ov := r.s.Overview(context.Background())
	for _, sg := range ov.Suggestions {
		if !sg.Before || !strings.HasPrefix(sg.Text, "Auf dem alten Server hattest du das") {
			t.Errorf("%s: %+v", sg.Key, sg)
		}
	}
	info.Swap = 0
	for _, sg := range r.s.Overview(context.Background()).Suggestions {
		if sg.Before != (sg.Key == KeyLogs) {
			t.Errorf("nur Logs wie vorher: %+v", sg)
		}
	}
}
