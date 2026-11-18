package tuning_test

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/tuning"
	"github.com/officialtaxz2/hostdeck/internal/tuning/tuningtest"
)

// Ein ganzer Kreis wie im Leitstand: Status, Vorschau, Anwenden, Status
// mit wartenden Bootparametern, CPU-Vorrang, Zurücknehmen.
func TestEchterAblauf(t *testing.T) {
	g := tuningtest.New(t)
	ctx := context.Background()
	st, err := g.S.Status(ctx)
	if err != nil || st.Error != "" {
		t.Fatal(err, st.Error, st.Lines)
	}
	if st.Version != tuning.Version || st.Applied != nil || len(st.Boot) != 0 || st.Reboot {
		t.Fatalf("Vorher: %+v", st)
	}
	if v := rowValue(st.Head, "Angewendet"); v != "nichts (vps-tune nicht aktiv)" {
		t.Fatalf("Angewendet: %q", v)
	}
	if v := rowValue(st.Rows, "TCP-Congestion-Control"); v != "cubic" {
		t.Fatalf("Congestion: %q in %+v", v, st.Rows)
	}
	if v := rowValue(st.Rows, "Speicher-Init"); v != "mem auto-init: stack:off, heap alloc:on, heap free:off" {
		t.Fatalf("Speicher-Init: %q", v)
	}
	if v := rowValue(st.Rows, "THP enabled / defrag"); v != "madvise / madvise" {
		t.Fatalf("THP: %q", v)
	}

	// Sicher: kein Bootparameter, keine Rückfrage.
	p, err := g.S.Preview(ctx, tuning.Options{Preset: "safe"})
	if err != nil || p.Code != 0 {
		t.Fatal(err, p)
	}
	if p.BootChange || len(p.Risky) > 0 || p.Changes == 0 {
		t.Fatalf("safe: %+v", p)
	}

	p, err = g.S.Preview(ctx, tuning.Options{Preset: "mixed"})
	if err != nil || p.Code != 0 || p.Problem != "" {
		t.Fatal(err, p)
	}
	if !p.BootChange || !slices.Equal(p.Risky, tuning.Risky) || !strings.HasPrefix(p.RiskText, "ACHTUNG: mitigations=off init_on_alloc=0 randomize_kstack_offset=off ") {
		t.Fatalf("mixed: %+v", p)
	}
	if !slices.Equal(p.Boot, []string{"mitigations=off", "init_on_alloc=0", "randomize_kstack_offset=off", "preempt=full", "cpuidle_haltpoll.force=1"}) {
		t.Fatalf("Boot: %v", p.Boot)
	}
	if g.Read("var/lib/vps-tune/applied.conf") != "" || g.Read("etc/sysctl.d/90-vps-tune.conf") != "" {
		t.Fatal("Die Vorschau hat etwas geändert")
	}
	if !hasLine(p.Lines, "detail", "net.ipv4.tcp_congestion_control = bbr") {
		t.Fatalf("Zeilen: %+v", p.Lines)
	}

	// Ohne --yes und ohne Terminal bricht das Skript ab, wie im Original.
	var out bytes.Buffer
	o, err := g.S.Apply(ctx, tuning.Options{Preset: "mixed"}, false, nil, &out)
	if err != nil || o.Code != 1 || !strings.Contains(out.String(), "Nicht interaktiv: zum Bestätigen --yes angeben.") {
		t.Fatalf("ohne --yes: %v %+v %s", err, o, out.String())
	}

	out.Reset()
	o, err = g.S.Apply(ctx, tuning.Options{Preset: "mixed"}, true, nil, &out)
	if err != nil || o.Code != 0 || !o.Reboot || o.Warnings != 0 {
		t.Fatalf("Anwenden: %v %+v\n%s", err, o, out.String())
	}
	if g.Read("proc/sys/net/ipv4/tcp_congestion_control") != "bbr" || !strings.Contains(g.Read("boot/grub/grub.cfg"), "mitigations=off") {
		t.Fatal("nicht angewendet")
	}
	if !strings.Contains(g.S.RebootNote(), "NEUSTART") {
		t.Fatalf("Neustart nicht gemerkt: %q", g.S.RebootNote())
	}

	st, err = g.S.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Applied == nil || st.Applied.Preset != "mixed" || st.Applied.Values["preempt"] != "full" || len(st.Applied.Boot) != 5 || st.Applied.At.IsZero() {
		t.Fatalf("applied.conf: %+v", st.Applied)
	}
	if o := st.Applied.Options(); o.Preset != "mixed" || len(o.Set) != 0 {
		t.Fatalf("Options aus applied.conf: %+v", o)
	}
	if len(st.Boot) != 5 || st.Boot[0] != (tuning.BootParam{Param: "mitigations=off"}) || !st.Reboot || !strings.Contains(st.RebootWhy, "cpuidle_haltpoll.force=1") {
		t.Fatalf("Boot: %+v %v %q", st.Boot, st.Reboot, st.RebootWhy)
	}

	// Nach dem Neustart: Parameter aktiv, /run ist leer.
	g.Rebooted()
	st, _ = g.S.Status(ctx)
	if st.Reboot || !st.Boot[4].Active {
		t.Fatalf("nach Neustart: %+v", st)
	}

	// CPU-Vorrang.
	units, err := g.S.Services(ctx)
	if err != nil || len(units) != 3 || units[1].Name != "docker.service" || units[1].Level != "normal" || !units[1].Running {
		t.Fatalf("Dienste: %v %+v", err, units)
	}
	out.Reset()
	if o, err := g.S.Prio(ctx, "docker", "high", &out); err != nil || o.Code != 0 {
		t.Fatalf("prio: %v %+v %s", err, o, out.String())
	}
	units, _ = g.S.Services(ctx)
	if units[0].Name != "docker.service" || !units[0].Managed || units[0].Level != "high" || units[0].Weight != 1000 {
		t.Fatalf("nach prio: %+v", units)
	}
	if _, err := g.S.Prio(ctx, "docker", "turbo", &out); err == nil {
		t.Fatal("Stufe turbo angenommen")
	}
	// Was der Umzug davon mitnimmt.
	if ps := tuning.ReadPrio(g.Root); !slices.Equal(ps, []tuning.PrioSet{{Unit: "docker.service", Level: "high"}}) {
		t.Fatalf("ReadPrio: %+v", ps)
	}
	if !tuning.Managed([]byte(g.Read("etc/sysctl.d/90-vps-tune.conf"))) || !tuning.Managed([]byte(g.Read("etc/default/grub.d/99-vps-tune.cfg"))) {
		t.Fatal("Dateien von vps-tune ohne Marker")
	}

	// Zurücknehmen: Vorschau ändert nichts, dann alles weg.
	p, err = g.S.RestorePreview(ctx)
	if err != nil || p.Code != 0 || !p.BootChange || !hasLine(p.Lines, "plan", "docker.service: CPUWeight zurück") {
		t.Fatalf("restore --dry-run: %v %+v", err, p)
	}
	if g.Read("var/lib/vps-tune/applied.conf") == "" {
		t.Fatal("Die Vorschau hat zurückgenommen")
	}
	out.Reset()
	o, err = g.S.Restore(ctx, &out)
	if err != nil || o.Code != 0 || !o.Reboot {
		t.Fatalf("restore: %v %+v\n%s", err, o, out.String())
	}
	if g.Read("proc/sys/net/ipv4/tcp_congestion_control") != "cubic" || g.Read("etc/sysctl.d/90-vps-tune.conf") != "" {
		t.Fatal("nicht zurückgenommen")
	}
	st, _ = g.S.Status(ctx)
	if st.Applied != nil || len(st.Boot) != 0 || !st.Reboot || !strings.Contains(st.RebootWhy, "Zurückgesetzt") {
		t.Fatalf("nach restore: %+v", st)
	}
	// Das Skript liegt nach dem Aufruf nicht mehr herum.
	if es, _ := os.ReadDir(g.S.RunDir); len(es) != 1 {
		t.Fatalf("im Laufordner: %v", es)
	}
}

func rowValue(rs []tuning.Row, label string) string {
	for _, r := range rs {
		if r.Label == label {
			return r.Value
		}
	}
	return "(fehlt)"
}

func hasLine(ls []tuning.Line, kind, text string) bool {
	for _, l := range ls {
		if l.Kind == kind && l.Text == text {
			return true
		}
	}
	return false
}

// Ein Fehler des Skripts kommt als Problem der Vorschau an.
func TestVorschauMitFehler(t *testing.T) {
	g := tuningtest.New(t)
	g.S.Script = []byte("#!/bin/bash\nprintf '[FEHLER] Bitte mit sudo/root ausführen.\\n' >&2; exit 1\n")
	p, err := g.S.Preview(context.Background(), tuning.Options{Preset: "safe"})
	if err != nil || p.Code != 1 || p.Problem != "Bitte mit sudo/root ausführen." {
		t.Fatalf("%v %+v", err, p)
	}
}

// Mit VT_PREFIX läuft nur etwas, wenn VT_BIN den Ersatz für
// alle Programme hat, die etwas ändern.
func TestFromEnvNurMitErsatz(t *testing.T) {
	t.Setenv("VT_PREFIX", "")
	if s := tuning.FromEnv("/run/x"); s.Prefix != "" || s.Refuse != nil || s.Path != "" {
		t.Fatalf("Betrieb: %+v", s)
	}
	g := tuningtest.New(t)
	t.Setenv("VT_PREFIX", g.Root)
	t.Setenv("VT_BIN", "")
	s := tuning.FromEnv(t.TempDir())
	if s.Refuse == nil {
		t.Fatal("ohne VT_BIN angenommen")
	}
	if _, err := s.Status(context.Background()); err == nil {
		t.Fatal("Status lief ohne Ersatz")
	}
	if _, err := s.Services(context.Background()); err == nil {
		t.Fatal("systemctl lief ohne Ersatz")
	}
	t.Setenv("VT_BIN", g.Bin)
	t.Setenv("VT_KREL", "6.12.0-test")
	t.Setenv("VT_VIRT", "kvm")
	s = tuning.FromEnv(t.TempDir())
	if s.Refuse != nil || !strings.HasPrefix(s.Path, g.Bin+":") {
		t.Fatalf("mit Ersatz: %+v", s)
	}
	if st, err := s.Status(context.Background()); err != nil || st.Error != "" {
		t.Fatal(err, st.Error)
	}
	os.Remove(g.Bin + "/tc")
	if tuning.FromEnv(t.TempDir()).Refuse == nil {
		t.Fatal("ohne tc-Ersatz angenommen")
	}
}
