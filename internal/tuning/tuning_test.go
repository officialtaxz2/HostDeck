package tuning

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Das eingebettete Skript ist das Original, Byte für Byte.
func TestSkriptUnveraendert(t *testing.T) {
	sum := sha256.Sum256(Script)
	if got := hex.EncodeToString(sum[:]); got != ScriptSHA256 {
		t.Fatalf("tune.sh hat sich geändert: %s", got)
	}
	if !bytes.Contains(Script, []byte("VERSION='"+Version+"'")) {
		t.Fatal("Version stimmt nicht")
	}
}

// Die Presets in HostDeck sind die aus preset_defaults.
func TestPresetsWieSkript(t *testing.T) {
	fn := regexp.MustCompile(`(?s)\npreset_defaults\(\) \{.*?\n\}\n`).Find(Script)
	if fn == nil {
		t.Fatal("preset_defaults nicht gefunden")
	}
	vars := map[string]string{"preempt": "O_PREEMPT", "haltpoll": "O_HALTPOLL", "mitigations": "O_MITIG", "hardening": "O_HARDEN", "net": "O_NET",
		"thp": "O_THP", "defrag": "O_DEFRAG", "rps": "O_RPS", "io": "O_IO", "udp": "O_UDP", "mtu": "O_MTU", "zram": "O_ZRAM"}
	for _, p := range Presets {
		var sh strings.Builder
		sh.Write(fn)
		sh.WriteString("die() { exit 9; }\npreset_defaults " + p.Name + "\n")
		for _, sw := range Switches {
			if v, ok := vars[sw.Key]; ok {
				sh.WriteString("echo " + sw.Key + "=$" + v + "\n")
			}
		}
		out, err := exec.Command("bash", "-c", sh.String()).Output()
		if err != nil {
			t.Fatalf("%s: %v", p.Name, err)
		}
		got := map[string]string{}
		for _, l := range strings.Fields(string(out)) {
			k, v, _ := strings.Cut(l, "=")
			got[k] = v
		}
		if !reflect.DeepEqual(got, p.Values) {
			t.Errorf("%s:\nSkript %v\nHostDeck    %v", p.Name, got, p.Values)
		}
	}
}

// Die Schalter und ihre Werte sind die aus main.
func TestSchalterWieSkript(t *testing.T) {
	re := regexp.MustCompile(`(?m)^\s+--([a-z|-]+)\)\s+in_list "\$val" ([a-z0-9+ ]+?) \|\|`)
	got := map[string][]string{}
	for _, m := range re.FindAllSubmatch(Script, -1) {
		for _, k := range strings.Split(strings.ReplaceAll(string(m[1]), "--", ""), "|") {
			got[k] = strings.Fields(string(m[2]))
		}
	}
	if !bytes.Contains(Script, []byte(`--iface)       is_iface "$val"`)) || !bytes.Contains(Script, []byte(`is_iface() { [[ "$1" =~ ^[A-Za-z0-9_.-]{1,15}$ ]]; }`)) {
		t.Error("--iface prüft anders")
	}
	want := map[string][]string{}
	for _, sw := range Switches {
		if sw.Values != nil {
			want[sw.Key] = sw.Values
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Skript %v\nHostDeck    %v", got, want)
	}
	if !bytes.Contains(Script, []byte(`case "$lvl" in high) w=1000 ;; low) w=20 ;; normal) w='' ;;`)) {
		t.Error("prio-Stufen anders")
	}
	if !bytes.Contains(Script, []byte(`[[ "$unit" =~ ^[A-Za-z0-9@_.:-]+$ ]]`)) {
		t.Error("prio prüft den Namen anders")
	}
}

// Der Warntext im Leitstand ist der von confirm_risk.
func TestWarntextWieSkript(t *testing.T) {
	for _, s := range []string{
		`printf '\nACHTUNG: %s schalten Schutzmechanismen des Gastkernels ab.\n' "${risky[*]}"`,
		`printf 'Prozesse in dieser VM können sich dadurch leichter gegenseitig oder den Kernel angreifen.\n'`,
		`mitigations=off|init_on_alloc=0|randomize_kstack_offset=off) risky+=("$p") ;;`,
	} {
		if !bytes.Contains(Script, []byte(s)) {
			t.Errorf("fehlt im Skript: %s", s)
		}
	}
	want := "ACHTUNG: mitigations=off init_on_alloc=0 schalten Schutzmechanismen des Gastkernels ab.\nProzesse in dieser VM können sich dadurch leichter gegenseitig oder den Kernel angreifen."
	if got := RiskText([]string{"mitigations=off", "init_on_alloc=0"}); got != want {
		t.Fatal(got)
	}
}

func TestOptionen(t *testing.T) {
	o := Options{Preset: "mixed", Set: map[string]string{"zram": "on", "preempt": "voluntary", "iface": "", "mtu": ""}}
	if err := o.Check(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(o.Args(), " "); got != "apply mixed --preempt voluntary --zram on" {
		t.Fatal(got)
	}
	if e := o.Effective(); e["preempt"] != "voluntary" || e["haltpoll"] != "on" || e["zram"] != "on" {
		t.Fatal(e)
	}
	for _, bad := range []Options{
		{Preset: "turbo"},
		{Preset: "safe", Set: map[string]string{"preempt": "lazy"}},
		{Preset: "safe", Set: map[string]string{"iface": "eth0; reboot"}},
		{Preset: "safe", Set: map[string]string{"dry-run": "on"}},
		{Preset: "safe", Set: map[string]string{"yes": "on"}},
	} {
		if err := bad.Check(); err == nil {
			t.Errorf("%+v angenommen", bad)
		}
	}
	for _, u := range []string{"docker", "caddy.service", "getty@tty1.service"} {
		if !ValidUnit(u) {
			t.Error(u)
		}
	}
	for _, u := range []string{"", "a b", "x;y", "-"} {
		if u != "-" && ValidUnit(u) {
			t.Error(u)
		}
	}
}

func TestStatusLesen(t *testing.T) {
	out := `
vps-tune 2.3.0-lean – Status
------------------------------------------------
Kernel:                  6.12.48+deb13-amd64
Virtualisierung:         kvm
Online-CPUs:             4
Angewendet:              preset=mixed preempt=full haltpoll=on

Bootparameter (vps-tune):
  mitigations=off                aktiv
  preempt=full                   NACH NEUSTART
  Hinweis: /etc/default/grub enthält selbst einen mitigations-Parameter (nicht von vps-tune).

Wirksamkeit:
  cpuidle-Treiber:             haltpoll
  Host-Poll-Control:           nein – Host pollt weiter, Haltpoll bringt wenig (--haltpoll off erwägen)
  RPS rx-0:                    0
  CPU-Vorrang (prio):          docker.service caddy.service

Hinweis: Kein eigenes Steal-Time-Sampling; vorhandenes Monitoring bleibt unberührt.

`
	s := ParseStatus(out)
	if s.Version != "2.3.0-lean" || len(s.Head) != 4 || s.Head[3].Value != "preset=mixed preempt=full haltpoll=on" {
		t.Fatalf("Kopf: %+v", s.Head)
	}
	if !reflect.DeepEqual(s.Boot, []BootParam{{"mitigations=off", true}, {"preempt=full", false}}) || len(s.BootNotes) != 1 {
		t.Fatalf("Boot: %+v %v", s.Boot, s.BootNotes)
	}
	if len(s.Rows) != 4 || s.Rows[1].Value != "nein – Host pollt weiter, Haltpoll bringt wenig (--haltpoll off erwägen)" || s.Rows[3].Value != "docker.service caddy.service" {
		t.Fatalf("Zeilen: %+v", s.Rows)
	}
	if !s.Reboot || s.RebootWhy != "Diese Bootparameter wirken erst nach einem Neustart: preempt=full." {
		t.Fatal(s.RebootWhy)
	}
	if len(s.Notes) != 1 || s.Lines[0] != "vps-tune 2.3.0-lean – Status" {
		t.Fatalf("%v %q", s.Notes, s.Lines[0])
	}
}

func TestAppliedOptionen(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, AppliedFile)
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("preset=safe preempt=full haltpoll=off mitigations=keep hardening=keep net=on thp=keep defrag=defer+madvise rps=all io=off udp=on mtu=on zram=off\nboot=preempt=full\n"), 0o644)
	a := ReadApplied(dir)
	if a == nil || a.Preset != "safe" || !slices.Equal(a.Boot, []string{"preempt=full"}) {
		t.Fatalf("%+v", a)
	}
	if o := a.Options(); !reflect.DeepEqual(o.Set, map[string]string{"preempt": "full", "rps": "all"}) {
		t.Fatalf("%+v", o)
	}
	if ReadApplied(t.TempDir()) != nil {
		t.Fatal("ohne Datei")
	}
}

// Der Marker ist der aus dem Skript; daran erkennt der Umzug die Dateien.
func TestMarkerWieSkript(t *testing.T) {
	if !strings.Contains(string(Script), "\nMARK='"+Marker+"'\n") {
		t.Fatal("MARK im Skript anders als tuning.Marker")
	}
	if !Managed([]byte(Marker+"\nnet.core.default_qdisc = fq\n")) || Managed([]byte("net.core.default_qdisc = fq\n")) {
		t.Fatal("Managed")
	}
}

func TestReadPrio(t *testing.T) {
	dir := t.TempDir()
	w := func(p, c string) {
		p = filepath.Join(dir, p)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(c), 0o644)
	}
	if ReadPrio(dir) != nil {
		t.Fatal("ohne prio.list")
	}
	w(PrioList, "docker.service\nbackup.service\nkaputt;x\nweg.service\nanders.service\n")
	w(PrioDropIn("docker.service"), "# created via set-property\n[Service]\nCPUWeight=1000\n")
	w(PrioDropIn("backup.service"), "[Service]\nCPUWeight=20\n")
	w(PrioDropIn("anders.service"), "[Service]\nCPUWeight=300\n")
	want := []PrioSet{{Unit: "docker.service", Level: "high"}, {Unit: "backup.service", Level: "low"}}
	if got := ReadPrio(dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
}

func TestAngebotOhneNetzwerkkarte(t *testing.T) {
	a := &Applied{Preset: "mixed", Values: map[string]string{"preempt": "full", "iface": "eth0", "zram": "on"}}
	if o := a.Offer(); o.Preset != "mixed" || !reflect.DeepEqual(o.Set, map[string]string{"zram": "on"}) {
		t.Fatalf("%+v", o)
	}
	if !a.Same(&Applied{Preset: "mixed", Values: map[string]string{"preempt": "full", "iface": "ens3", "zram": "on"}}) {
		t.Fatal("andere Netzwerkkarte zählt nicht")
	}
	for _, h := range []*Applied{nil, {Preset: "safe", Values: map[string]string{"zram": "on"}}, {Preset: "mixed", Values: map[string]string{"zram": "off"}}} {
		if a.Same(h) {
			t.Fatalf("gleich: %+v", h)
		}
	}
}
