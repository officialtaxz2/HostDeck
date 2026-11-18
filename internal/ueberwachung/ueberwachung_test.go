package ueberwachung

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
)

type fakeDocker struct {
	list     []dockerapi.OpsContainer
	inspect  map[string]dockerapi.Container
	inspects int
}

func (f *fakeDocker) List(context.Context) ([]dockerapi.OpsContainer, error) { return f.list, nil }
func (f *fakeDocker) Inspect(_ context.Context, id string) (dockerapi.Container, error) {
	f.inspects++
	return f.inspect[id], nil
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// rig baut ein kleines /proc und /sys nach: ein Server mit 2 Kernen, einer
// Platte sda (mit Partition und LVM, die nicht doppelt zählen dürfen),
// eth0 als echte Karte, docker0 und wg0 als virtuelle, ein Container im
// Bridge-Netz (web, PID 4242) und einer im Host-Netz (mc-router, PID 4343).
type rig struct {
	proc, sys string
	cg        string
	d         *fakeDocker
	c         *Collector
}

func newRig(t *testing.T) *rig {
	dir := t.TempDir()
	r := &rig{proc: filepath.Join(dir, "proc"), sys: filepath.Join(dir, "sys")}
	r.cg = filepath.Join(r.sys, "fs", "cgroup", "system.slice", "docker-aaa.scope")
	for _, d := range []string{"block/sda", "block/loop0", "block/dm-0", "class/net/eth0/device", "class/net/docker0", "class/net/wg0"} {
		os.MkdirAll(filepath.Join(r.sys, d), 0o755)
	}
	write(t, filepath.Join(r.proc, "meminfo"), "MemTotal:       4000000 kB\nMemFree:  100 kB\nMemAvailable:   1000000 kB\nSwapTotal:  2000000 kB\nSwapFree:   1500000 kB\n")
	write(t, filepath.Join(r.proc, "loadavg"), "0.50 0.40 0.30 1/200 999\n")
	write(t, filepath.Join(r.proc, "1", "mounts"), "/dev/sda1 / ext4 rw 0 0\n/dev/sda1 /var/lib/docker/x ext4 rw 0 0\noverlay /var/lib/docker/overlay2/x/merged overlay rw 0 0\ntmpfs /run tmpfs rw 0 0\n/dev/sda15 /boot/efi vfat rw 0 0\n/dev/sdb1 /mnt/mit\\040leer ext4 rw 0 0\n")
	write(t, filepath.Join(r.proc, "4242", "cgroup"), "0::/system.slice/docker-aaa.scope\n")
	write(t, filepath.Join(r.proc, "4343", "cgroup"), "0::/system.slice/docker-bbb.scope\n")
	r.set(t, 0)
	r.d = &fakeDocker{
		list: []dockerapi.OpsContainer{
			{ID: "aaa", Names: []string{"/web"}, State: "running", Status: "Up 3 hours", Labels: map[string]string{"com.docker.compose.project": "pensum"}},
			{ID: "bbb", Names: []string{"/mc-router"}, State: "running", Status: "Up 3 hours (healthy)", Labels: map[string]string{"com.docker.compose.project": "mcserver"}},
			{ID: "ccc", Names: []string{"/alt"}, State: "exited", Status: "Exited (0) 2 days ago"},
		},
		inspect: map[string]dockerapi.Container{},
	}
	web := dockerapi.Container{}
	web.State.Pid = 4242
	web.HostConfig.NetworkMode = "pensum_default"
	web.HostConfig.Memory = 512 << 20
	r.d.inspect["aaa"] = web
	mc := dockerapi.Container{RestartCount: 2}
	mc.State.Pid = 4343
	mc.HostConfig.NetworkMode = "host"
	mc.State.Health = &struct {
		Status string `json:"Status"`
	}{"healthy"}
	r.d.inspect["bbb"] = mc
	r.c = &Collector{Docker: r.d, Proc: r.proc, Sys: r.sys, Disk: func(p string) (int64, int64, error) {
		switch p {
		case "/":
			return 100e9, 40e9, nil
		case "/mnt/mit leer":
			return 10e9, 10e9, nil
		}
		return 0, 0, os.ErrNotExist
	}}
	return r
}

// set schreibt die Zähler für Schritt i (je Schritt 10 s).
func (r *rig) set(t *testing.T, i int) {
	n := func(base, per int) string { return itoa(base + i*per) }
	// user nice system idle iowait irq softirq steal: in 10 s mit 2 Kernen
	// 2000 Ticks: 600 user, 200 system, 800 idle, 200 iowait, 200 steal.
	write(t, filepath.Join(r.proc, "stat"), "cpu  "+n(1000, 600)+" 0 "+n(500, 200)+" "+n(9000, 800)+" "+n(100, 200)+" 0 0 "+n(50, 200)+" 0 0\ncpu0 1 2 3 4 5 6 7 8\ncpu1 1 2 3 4 5 6 7 8\nintr 1\n")
	// sda: 2000 Sektoren gelesen, 4000 geschrieben je Schritt; sda1 und
	// dm-0 dürfen nicht zählen.
	write(t, filepath.Join(r.proc, "diskstats"), "   8       0 sda 1 0 "+n(100, 2000)+" 0 1 0 "+n(100, 4000)+" 0 0 0 0\n   8       1 sda1 1 0 "+n(100, 2000)+" 0 1 0 "+n(100, 4000)+" 0 0 0 0\n 253       0 dm-0 1 0 "+n(100, 2000)+" 0 1 0 "+n(100, 4000)+" 0 0 0 0\n   7       0 loop0 1 0 999999 0 1 0 999999 0 0 0 0\n")
	netdev := func(lines ...string) string {
		return "Inter-|   Receive  |  Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets\n" + strings.Join(lines, "\n") + "\n"
	}
	ifl := func(name string, rx, tx int) string {
		return "  " + name + ": " + itoa(rx) + " 1 0 0 0 0 0 0 " + itoa(tx) + " 1 0 0 0 0 0 0"
	}
	write(t, filepath.Join(r.proc, "1", "net", "dev"), netdev(ifl("lo", 5+i*999, 5+i*999), ifl("eth0", 1000+i*10000, 2000+i*20000), ifl("docker0", i*777, i*777), ifl("wg0", i*555, i*555)))
	write(t, filepath.Join(r.proc, "4242", "net", "dev"), netdev(ifl("lo", i*999, i*999), ifl("eth0", 100+i*1000, 200+i*3000)))
	// web: 5 s CPU in 10 s = 50 % eines Kerns.
	write(t, filepath.Join(r.cg, "cpu.stat"), "usage_usec "+n(1000000, 5000000)+"\nuser_usec 1\n")
	write(t, filepath.Join(r.cg, "memory.current"), "300000000\n")
	write(t, filepath.Join(r.cg, "memory.stat"), "anon 1\ninactive_file 100000000\n")
	write(t, filepath.Join(r.cg, "io.stat"), "8:0 rbytes="+n(0, 10000)+" wbytes="+n(0, 20000)+" rios=1 wios=1\n253:0 rbytes=0 wbytes="+n(0, 30000)+"\n")
}

func itoa(i int) string { return strconv.Itoa(i) }

func near(t *testing.T, f Frame, key string, want float64) {
	t.Helper()
	got, ok := f.Values[key]
	if !ok {
		t.Errorf("%s fehlt", key)
		return
	}
	if math.Abs(got-want) > 0.01*math.Max(1, math.Abs(want)) {
		t.Errorf("%s = %v, erwartet %v", key, got, want)
	}
}

func TestMessen(t *testing.T) {
	r := newRig(t)
	t0 := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	f, info := r.c.Collect(context.Background(), t0)
	if _, ok := f.Values[KeyCPU]; ok {
		t.Error("Prozessor schon bei der ersten Messung")
	}
	if _, ok := f.Values[ContainerKey("web", ValCPU)]; ok {
		t.Error("Rate schon bei der ersten Messung")
	}
	near(t, f, KeyRAM, 3e9*1.024)
	near(t, f, KeySwap, 0.5e9*1.024)
	near(t, f, KeyLoad, 0.5)
	if info.Cores != 2 || info.RAMTotal != 4096000000 {
		t.Errorf("Info: %+v", info)
	}
	if len(info.Disks) != 2 || info.Disks[0].Mount != "/" || info.Disks[0].Used != 60e9 || info.Disks[1].Mount != "/mnt/mit leer" {
		t.Errorf("Laufwerke: %+v", info.Disks)
	}
	near(t, f, "d//", 60e9)

	r.set(t, 1)
	f, info = r.c.Collect(context.Background(), t0.Add(10*time.Second))
	near(t, f, KeyCPU, 50)    // 1000 von 2000 belegt (ohne idle und iowait)
	near(t, f, KeyIOWait, 10) // 200 von 2000
	near(t, f, KeySteal, 10)
	near(t, f, KeyRead, 2000*512/10.0)
	near(t, f, KeyWrite, 4000*512/10.0)
	near(t, f, KeyNetIn, 1000) // nur eth0
	near(t, f, KeyNetOut, 2000)
	near(t, f, ContainerKey("web", ValCPU), 50)
	near(t, f, ContainerKey("web", ValRAM), 200000000)
	near(t, f, ContainerKey("web", ValRead), 1000)
	near(t, f, ContainerKey("web", ValWrite), 5000)
	near(t, f, ContainerKey("web", ValNetIn), 100) // ohne lo
	near(t, f, ContainerKey("web", ValNetOut), 300)
	if _, ok := f.Values[ContainerKey("mc-router", ValNetIn)]; ok {
		t.Error("Host-Netz beim Container gezählt")
	}
	// Summen je Compose-Projekt, damit die Spitze eines Projekts genau ist.
	near(t, f, ProjectKey("pensum", ValCPU), 50)
	near(t, f, ProjectKey("pensum", ValRAM), 200000000)
	near(t, f, ProjectKey("pensum", ValNetIn), 100)
	if _, ok := f.Values[ProjectKey("mcserver", ValNetIn)]; ok {
		t.Error("Host-Netz beim Projekt gezählt")
	}
	if len(info.Containers) != 3 {
		t.Fatalf("Container: %+v", info.Containers)
	}
	mc := info.Containers[1]
	if mc.Name != "mc-router" || !mc.HostNet || mc.Health != "healthy" || mc.Restarts != 2 || mc.Project != "mcserver" {
		t.Errorf("mc-router: %+v", mc)
	}
	if web := info.Containers[2]; web.Name != "web" || web.MemLimit != 512<<20 || web.Project != "pensum" {
		t.Errorf("web: %+v", web)
	}
	// Inspect nur beim ersten Mal (und für gestoppte einmal).
	before := r.d.inspects
	r.set(t, 2)
	r.c.Collect(context.Background(), t0.Add(20*time.Second))
	if r.d.inspects != before {
		t.Errorf("Inspect bei unverändertem Container: %d → %d", before, r.d.inspects)
	}
	// Neustart des Containers: Zähler fallen, keine negative Rate.
	r.d.list[0].Status = "Up 1 second"
	write(t, filepath.Join(r.cg, "cpu.stat"), "usage_usec 10\n")
	f, _ = r.c.Collect(context.Background(), t0.Add(30*time.Second))
	if _, ok := f.Values[ContainerKey("web", ValCPU)]; ok {
		t.Error("Rate trotz zurückgesetztem Zähler")
	}
	if r.d.inspects != before+1 {
		t.Errorf("kein neues Inspect nach Statuswechsel: %d", r.d.inspects-before)
	}
}

func TestSpeicherStufenUndDateien(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir}
	t0 := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	// 3 Stunden alle 10 s: cpu steigt linear, web erst ab Stunde 2.
	for i := 0; i < 3*360; i++ {
		v := map[string]float64{KeyCPU: float64(i % 360)}
		if i >= 2*360 {
			v[ContainerKey("web", ValCPU)] = 5
		}
		s.Add(Frame{At: t0.Add(time.Duration(i) * 10 * time.Second), Values: v})
		if i%60 == 59 {
			if err := s.Flush(t0.Add(time.Duration(i) * 10 * time.Second)); err != nil {
				t.Fatal(err)
			}
		}
	}
	now := t0.Add(3*time.Hour - 10*time.Second)
	h, err := s.Query("1h", []string{"s/"}, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if h.Step != 10 || len(h.Times) != 360 || len(h.Series[KeyCPU]) != 360 {
		t.Fatalf("1h: Schritt %d, %d Zeiten, %d Werte", h.Step, len(h.Times), len(h.Series[KeyCPU]))
	}
	if _, ok := h.Series[ContainerKey("web", ValCPU)]; ok {
		t.Error("Filter s/ lieferte Container")
	}
	// Muster mit „*“ und weniger Punkte: je 6 Werte gemittelt.
	h, _ = s.Query("1h", []string{"c/*/cpu", "c/*/ram"}, 60, now)
	if len(h.Series) != 1 || len(h.Series[ContainerKey("web", ValCPU)]) != 60 || h.Step != 60 {
		t.Errorf("Muster c/*/cpu: %d Reihen, Schritt %d", len(h.Series), h.Step)
	}
	if h, _ := s.Query("1h", []string{"s/c*"}, 0, now); len(h.Series) != 1 {
		t.Errorf("Muster s/c*: %v", len(h.Series))
	}
	// 24 Stunden in höchstens 720 Punkten: 2 Minuten je Punkt, auch wenn der
	// Verlauf erst 3 Stunden alt ist (Punkte nach Uhrzeit, D2).
	h, _ = s.Query("1d", nil, 0, now)
	if h.Step != 120 || len(h.Times) != 90 { // die laufende Minute im letzten Punkt
		t.Errorf("1d: Schritt %d, %d Punkte", h.Step, len(h.Times))
	}
	// Erste zwei Minuten: Mittel von 0..11.
	if got := float64(h.Series[KeyCPU][0]); math.Abs(got-5.5) > 0.01 {
		t.Errorf("Mittel der ersten zwei Minuten: %v", got)
	}
	if b, _ := json.Marshal(h.Series[ContainerKey("web", ValCPU)][:2]); string(b) != "[null,null]" {
		t.Errorf("fehlende Werte: %s", b)
	}

	// Neu laden: Minuten und 10-Minuten-Stufe kommen aus den Dateien.
	s2 := &Store{Dir: dir}
	if err := s2.Load(now); err != nil {
		t.Fatal(err)
	}
	h2, _ := s2.Query("1d", nil, 0, now)
	if len(h2.Times) < 85 {
		t.Errorf("nach dem Laden: %d Punkte", len(h2.Times))
	}
	h3, _ := s2.Query("1h", []string{"s/"}, 0, now)
	if h3.Step != 60 || len(h3.Times) < 55 {
		t.Errorf("1h direkt nach dem Start nimmt die Minuten: Schritt %d, %d Punkte", h3.Step, len(h3.Times))
	}
	// Nach dem Neustart kommen feine Punkte dazu: „1 Stunde“ zeigt davor
	// weiter die Minuten, danach die 10-Sekunden-Werte.
	for i := 1; i <= 30; i++ {
		s2.Add(Frame{At: now.Add(time.Duration(i) * 10 * time.Second), Values: map[string]float64{KeyCPU: 7}})
	}
	later := now.Add(300 * time.Second)
	hm, _ := s2.Query("1h", []string{"s/"}, 0, later)
	if hm.Step != 10 || hm.CoarseStep != 60 || hm.CoarseTo == 0 || len(hm.Times) < 80 {
		t.Errorf("1h nach dem Neustart: Schritt %d, grob %d bis %d, %d Punkte", hm.Step, hm.CoarseStep, hm.CoarseTo, len(hm.Times))
	}
	if hm.Times[0] < later.Add(-time.Hour).Unix() || hm.Times[len(hm.Times)-1] <= hm.CoarseTo {
		t.Errorf("Zeiten: %d … %d, grob bis %d", hm.Times[0], hm.Times[len(hm.Times)-1], hm.CoarseTo)
	}
	h7, _ := s2.Query("7d", nil, 0, now)
	if h7.Step != 900 || len(h7.Times) < 11 {
		t.Errorf("7d: Schritt %d, %d Punkte", h7.Step, len(h7.Times))
	}

	// Abgeschnittene Datei (Absturz beim Schreiben): Rest bleibt lesbar.
	files, _ := filepath.Glob(filepath.Join(dir, "minuten-*.jsonl.gz"))
	if len(files) != 1 {
		t.Fatalf("Dateien: %v", files)
	}
	b, _ := os.ReadFile(files[0])
	os.WriteFile(files[0], b[:len(b)-7], 0o600)
	s3 := &Store{Dir: dir}
	s3.Load(now)
	if h, _ := s3.Query("1d", nil, 0, now); len(h.Times) < 50 {
		t.Errorf("nach abgeschnittener Datei: %d Punkte", len(h.Times))
	}

	// Alte Dateien verschwinden nach 2 Tagen (Minuten) und 30 Tagen.
	if err := s.Flush(t0.Add(4 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "minuten-*")); len(files) != 0 {
		t.Errorf("alte Minuten-Dateien: %v", files)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "zehn-*")); len(files) != 1 {
		t.Errorf("10-Minuten-Datei zu früh weg: %v", files)
	}
	if _, err := s.Query("2h", nil, 0, now); err == nil {
		t.Error("unbekannter Zeitraum ohne Fehler")
	}
}

func TestDienst(t *testing.T) {
	r := newRig(t)
	t0 := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	now := t0
	self := filepath.Join(t.TempDir(), "self")
	write(t, filepath.Join(self, "stat"), "123 (hostdeck dienst) S 1 1 1 0 -1 0 0 0 0 0 100 50 0 0 20 0 1 0\n")
	write(t, filepath.Join(self, "status"), "Name: HostDeck\nVmRSS:\t   20480 kB\n")
	s := &Service{Collector: r.c, Store: &Store{Dir: t.TempDir()}, Now: func() time.Time { return now }, SelfProc: self}
	if snap := s.Snapshot(); snap.Running || snap.At != nil {
		t.Errorf("vor dem Start: %+v", snap)
	}
	s.Tick(context.Background())
	now = now.Add(10 * time.Second)
	r.set(t, 1)
	write(t, filepath.Join(self, "stat"), "123 (hostdeck dienst) S 1 1 1 0 -1 0 0 0 0 0 103 52 0 0 20 0 1 0\n")
	s.Tick(context.Background())
	snap := s.Snapshot()
	if !snap.Running || snap.Interval != 10 || snap.Values[KeyCPU] == 0 || len(snap.Info.Containers) != 3 {
		t.Errorf("Stand: %+v", snap)
	}
	// Kosten als Anteil am Server (E1): 0,5 % eines Kerns geteilt durch die Kerne.
	n := float64(snap.Info.Cores)
	if math.Abs(snap.Cost.CPU-0.5/n) > 0.01 || snap.Cost.RAM != 20480*1024 {
		t.Errorf("Kosten: %+v", snap.Cost)
	}
	// Schnitt seit dem Start: weitere 10 Sekunden mit 1,5 % ergeben 1 %.
	now = now.Add(10 * time.Second)
	write(t, filepath.Join(self, "stat"), "123 (hostdeck dienst) S 1 1 1 0 -1 0 0 0 0 0 113 57 0 0 20 0 1 0\n")
	s.Tick(context.Background())
	if c := s.Snapshot().Cost; math.Abs(c.CPU-1.5/n) > 0.01 || math.Abs(c.Avg-1/n) > 0.01 {
		t.Errorf("Schnitt: %+v", c)
	}
	if b, err := json.Marshal(snap); err != nil || !strings.Contains(string(b), `"kosten"`) {
		t.Errorf("JSON: %s %v", b, err)
	}
	h, err := s.History("1h", nil, 0, false)
	if err != nil || len(h.Times) != 3 {
		t.Errorf("Verlauf: %+v %v", h, err)
	}
}

// ui1: Die gröberen Stufen heben die Spitze je Abschnitt auf, auch über
// Datei und Neustart; eine kurze Spitze verschwindet nicht im Mittel.
func TestSpeicherSpitzen(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir}
	t0 := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	// 2 Stunden alle 10 s bei 10 %, einmal pro Stunde 10 s lang 90 %.
	for i := 0; i < 2*360; i++ {
		v := 10.0
		if i%360 == 100 {
			v = 90
		}
		s.Add(Frame{At: t0.Add(time.Duration(i) * 10 * time.Second), Values: map[string]float64{KeyCPU: v}})
	}
	now := t0.Add(2*time.Hour - 10*time.Second)
	if err := s.Flush(now); err != nil {
		t.Fatal(err)
	}
	check := func(name string, st *Store) {
		t.Helper()
		h, err := st.QueryPeaks("6h", []string{"s/"}, 0, now, true)
		if err != nil {
			t.Fatal(err)
		}
		var top, avg float64
		for i, v := range h.Peaks[KeyCPU] {
			top = max(top, float64(v))
			avg = max(avg, float64(h.Series[KeyCPU][i]))
		}
		if top != 90 || avg >= 30 {
			t.Errorf("%s: Spitze %v, höchstes Mittel %v", name, top, avg)
		}
		if len(h.Peaks[KeyCPU]) != len(h.Times) {
			t.Errorf("%s: %d Spitzen zu %d Zeiten", name, len(h.Peaks[KeyCPU]), len(h.Times))
		}
		// Ohne Wunsch keine Spitzen im JSON.
		h2, _ := st.Query("6h", []string{"s/"}, 0, now)
		if h2.Peaks != nil {
			t.Errorf("%s: Spitzen ohne Wunsch", name)
		}
	}
	check("Speicher", s)
	s2 := &Store{Dir: dir}
	if err := s2.Load(now); err != nil {
		t.Fatal(err)
	}
	check("nach Neustart", s2)
	// 15 Minuten aus der feinsten Stufe: Spitze ist der Wert selbst.
	h, err := s.QueryPeaks("15m", []string{"s/"}, 0, now, true)
	if err != nil || len(h.Times) < 90 || len(h.Times) > 91 {
		t.Fatalf("15m: %d Zeiten, %v", len(h.Times), err)
	}
	for i, v := range h.Peaks[KeyCPU] {
		if v != h.Series[KeyCPU][i] {
			t.Fatalf("15m: Spitze %v ungleich Wert %v", v, h.Series[KeyCPU][i])
		}
	}
}
