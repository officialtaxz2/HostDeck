package serverebene

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/tuning"
	"github.com/officialtaxz2/hostdeck/internal/tuning/tuningtest"
)

// Was tune.sh (vps-tune) geschrieben hat, zieht nicht einzeln
// mit. Es steht nur als Hinweis in der Liste; der neue Leitstand bietet das
// Tuning als Ganzes an.
func TestTuningZiehtNichtEinzelnMit(t *testing.T) {
	g := tuningtest.New(t)
	ctx := context.Background()
	var out bytes.Buffer
	if o, err := g.S.Apply(ctx, tuning.Options{Preset: "mixed", Set: map[string]string{"zram": "on"}}, true, nil, &out); err != nil || o.Code != 0 {
		t.Fatalf("apply: %v %+v\n%s", err, o, out.String())
	}
	if o, err := g.S.Prio(ctx, "docker", "high", &out); err != nil || o.Code != 0 {
		t.Fatalf("prio: %v %+v", err, o)
	}
	// zramctl fehlt im Testaufbau; die Unit sieht aus wie die von tune.sh.
	put(t, g.Root, "/etc/systemd/system/vps-tune-zram.service", tuning.Marker+"\n[Service]\nExecStart=/usr/sbin/zramctl /dev/zram0\n", 0o644)
	put(t, g.Root, "/etc/sysctl.d/60-eigen.conf", "vm.max_map_count = 262144\n", 0o644)
	put(t, g.Root, "/etc/fail2ban/jail.local", "[sshd]\nenabled = true\n", 0o644)
	fakeIDs(t, g.Root)
	var set []inventory.Setting
	for _, p := range []string{"/etc/sysctl.d/90-vps-tune.conf", "/etc/sysctl.d/60-eigen.conf", "/etc/default/grub.d/99-vps-tune.cfg",
		"/etc/modules-load.d/vps-tune.conf", "/etc/systemd/system.control/docker.service.d/50-CPUWeight.conf", "/etc/fail2ban/jail.local"} {
		set = append(set, inventory.Setting{Path: p, Change: inventory.ChangeAdded})
	}
	inv := &inventory.Inventory{Settings: set, Tasks: []inventory.Task{
		{Kind: "dienst", Source: "/etc/systemd/system/vps-tune-zram.service", Name: "vps-tune-zram.service", Enabled: true}}}
	if g.Read("etc/systemd/system/vps-tune-zram.service") == "" || g.Read("etc/modules-load.d/vps-tune.conf") == "" {
		t.Fatalf("Testaufbau: zram-Dienst oder modules-load fehlt\n%s", out.String())
	}
	s := Source{Root: g.Root, Inv: inv, Host: "alt"}
	items := byKey(s.Items())
	k := items["kernel"]
	if len(k.Files) != 1 || string(k.Files[0].Data) != "# Kernel-Einstellungen von alt, beim Umzug übernommen von HostDeck.\nvm.max_map_count = 262144\n" {
		t.Fatalf("Kernel: %+v", k)
	}
	for key := range items {
		if strings.Contains(key, "vps-tune") {
			t.Errorf("Punkt %s zieht mit", key)
		}
	}
	tu, ok := items["tuning"]
	if !ok || tu.Selectable() || tu.Title != "Tuning (vps-tune, Preset mixed)" || !strings.Contains(tu.Manual, "Überwachung > Tuning") ||
		!strings.Contains(tu.Detail, "--zram on") || !strings.Contains(tu.Detail, "Bootparameter ") || !strings.Contains(tu.Detail, "CPU-Vorrang docker.service high") {
		t.Fatalf("Tuning: %+v", tu)
	}
	if len(tu.Files) != 0 {
		t.Fatal("Tuning bringt Dateien mit")
	}
	if rest := s.Rest(); strings.Join(rest, " ") != "/etc/fail2ban/jail.local" {
		t.Fatalf("Rest: %v", rest)
	}
	// Ohne Tuning kein Hinweis.
	s.Root = t.TempDir()
	if _, ok := byKey(s.Items())["tuning"]; ok {
		t.Fatal("Hinweis ohne Tuning")
	}
}
