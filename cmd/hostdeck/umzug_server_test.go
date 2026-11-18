package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// Nach dem Probe-Umzug prüft der neue Server die Server-Ebene,
// nur als Vorschau und ohne Inhalte; dann sucht HostDeck hier die alte Adresse.
func TestProbeUmzugServerEbene(t *testing.T) {
	m, f, target := moveRig(t)
	root := t.TempDir()
	for p, s := range map[string]string{
		"/etc/timezone":                 "Europe/Berlin\n",
		"/etc/passwd":                   "root:x:0:0::/root:/bin/bash\n",
		"/var/spool/cron/crontabs/root": "30 3 * * * /opt/app/backup.sh\n",
		"/root/.ssh/id_ed25519":         "PRIVAT-GEHEIM\n",
		"/root/.ssh/id_ed25519.pub":     "ssh-ed25519 AAAA\n",
		"/opt/app/.env":                 "SERVER=203.0.113.7\n",
		"/etc/sysctl.d/60-x.conf":       "vm.max_map_count=262144\n",
	} {
		os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755)
		os.WriteFile(filepath.Join(root, p), []byte(s), 0o600)
	}
	inv := &inventory.Inventory{
		Projects: []inventory.Project{{Name: "app", WorkingDir: "/opt/app", DirExists: true}},
		Tasks:    []inventory.Task{{Kind: "cron", Source: "/var/spool/cron/crontabs/root", User: "root", Schedule: "30 3 * * *", When: "täglich 03:30", Command: "/opt/app/backup.sh"}},
		Settings: []inventory.Setting{{Path: "/etc/sysctl.d/60-x.conf", Change: inventory.ChangeAdded}, {Path: "/etc/ssh/sshd_config", Change: inventory.ChangeModified}},
	}
	m.Root = root
	m.Inventory = func() (*inventory.Inventory, error) { return inv, nil }
	m.LocalIPs = func() []net.IP { return []net.IP{net.ParseIP("203.0.113.7"), net.ParseIP("10.0.0.2")} }

	// Die Liste für den Leitstand, dann ein Haken.
	lv := m.ServerLevel(context.Background())
	var keys []string
	for _, it := range lv.Items {
		keys = append(keys, it.Key)
	}
	if strings.Join(keys, " ") != "zeitzone kernel aufgabe:/var/spool/cron/crontabs/root zugang:ssh:/root" || len(lv.Chosen) != 0 || strings.Join(lv.Rest, " ") != "/etc/ssh/sshd_config" {
		t.Fatalf("Liste: %+v", lv)
	}
	if err := m.ChooseServer([]string{"zeitzone"}); err == nil {
		t.Error("automatischer Punkt ließ sich ankreuzen")
	}
	if err := m.ChooseServer([]string{"zugang:ssh:/root", "zugang:ssh:/root"}); err != nil {
		t.Fatal(err)
	}
	if lv := m.ServerLevel(context.Background()); strings.Join(lv.Chosen, " ") != "zugang:ssh:/root" {
		t.Errorf("angekreuzt: %v", lv.Chosen)
	}

	var out strings.Builder
	if code := m.Run(context.Background(), target, dienst.MoveRun, &autoInput{}, &out); code != 0 || f.err != nil {
		t.Fatalf("Code %d, %v\n%s", code, f.err, out.String())
	}
	f.mu.Lock()
	sv := f.server
	f.mu.Unlock()
	if sv == nil || sv.Server == nil {
		t.Fatalf("keine Server-Ebene\n%s", out.String())
	}
	if sv.Server.Apply {
		t.Error("Probe-Umzug übernimmt")
	}
	b, _ := json.Marshal(sv)
	if strings.Contains(string(b), "PRIVAT") || strings.Contains(string(b), "UFJJVkFU") {
		t.Errorf("Inhalt ging mit: %s", b)
	}
	var got []string
	for _, it := range sv.Server.Items {
		got = append(got, it.Key)
	}
	if strings.Join(got, " ") != "zeitzone kernel zugang:ssh:/root" {
		t.Errorf("geschickt: %v", got)
	}
	for _, want := range []string{"Server-Ebene (Vorschau)", "1 Aufgabe oder Zugang ist nicht angekreuzt", "stellt HostDeck Europe/Berlin ein",
		"Von Hand vergleichen: 1 geänderte Datei in /etc", "Alte Adresse in Dateien", "! 203.0.113.7 steht noch in 1 Datei", "/opt/app/.env, Zeile 1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("Ausgabe ohne %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "10.0.0.2") {
		t.Errorf("private Adresse gesucht:\n%s", out.String())
	}
}

func TestAdressSucheOrdnerUndSchnittstellen(t *testing.T) {
	inv := &inventory.Inventory{
		Projects: []inventory.Project{{WorkingDir: "/opt/docker/neko", DirExists: true}},
		Paths: []inventory.Path{
			{Path: "/opt/calagopus/wings", Exists: true, IsDir: true, Mounts: []inventory.MountRef{{Container: "wings"}}},
			{Path: "/opt/docker/neko-results", Exists: true, IsDir: true},
			{Path: "/opt/docker", Kind: inventory.KindParent, Exists: true, IsDir: true, Mounts: []inventory.MountRef{{Container: "x"}}},
			{Path: "/opt/calagopus/postgres", Exists: true, IsDir: true, DB: &inventory.DB{}, Mounts: []inventory.MountRef{{Container: "db"}}},
		},
	}
	dirs, skip := scanDirs(inv)
	if strings.Join(dirs, " ") != "/etc /opt/docker/neko /opt/calagopus/wings" {
		t.Errorf("Ordner %v", dirs)
	}
	if !strings.Contains(strings.Join(skip, " "), "/opt/calagopus/postgres") || !strings.Contains(strings.Join(skip, " "), "/etc/netplan") {
		t.Errorf("ausgelassen %v", skip)
	}
	for name, virt := range map[string]bool{"eth0": false, "ens3": false, "enp1s0": false, "docker0": true, "br-1a2b": true, "veth12": true, "tailscale0": true, "wg0": true} {
		if virtualIface(name) != virt {
			t.Errorf("%s: virtuell %v", name, !virt)
		}
	}
}

func TestStandardroute(t *testing.T) {
	dir := t.TempDir()
	v4 := filepath.Join(dir, "route")
	v6 := filepath.Join(dir, "ipv6_route")
	os.WriteFile(v4, []byte("Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"+
		"eth0\t00000000\t01F5AF05\t0003\t0\t0\t0\t00000000\t0\t0\t0\n"+
		"eth0\t00F5AF05\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n"+
		"br-mc\t00002000\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"), 0o644)
	os.WriteFile(v6, []byte("00000000000000000000000000000000 00 00000000000000000000000000000000 00 fe800000000000000000000000000001 00000400 00000001 00000000 00000003 ens3\n"+
		"00000000000000000000000000000000 00 00000000000000000000000000000000 00 00000000000000000000000000000000 ffffffff 00000001 00000000 00200200       lo\n"+
		"2a14000000000000000000000000000 40 00000000000000000000000000000000 00 00000000000000000000000000000000 00000100 00000001 00000000 00000001 ens3\n"), 0o644)
	got := defaultRouteIfaces(v4, v6)
	if len(got) != 2 || !got["eth0"] || !got["ens3"] {
		t.Errorf("Standardroute: %v", got)
	}
}
