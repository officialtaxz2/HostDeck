package altserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/ort"
	"github.com/officialtaxz2/hostdeck/internal/tuning"
)

func write(t *testing.T, p, c string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(c), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Das Tuning von vps-tune zieht als Angebot mit; ohne es bleibt
// alles wie vorher.
func TestTuningZiehtMit(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	if info := BuildIn(root, data, "alt"); !info.Empty() {
		t.Fatalf("ohne alles: %+v", info)
	}
	write(t, filepath.Join(root, tuning.AppliedFile), "preset=mixed preempt=full zram=on iface=eth0\nboot=mitigations=off\n")
	write(t, filepath.Join(root, tuning.PrioList), "docker.service\n")
	write(t, filepath.Join(root, tuning.PrioDropIn("docker.service")), "[Service]\nCPUWeight=1000\n")
	info := BuildIn(root, data, "alt")
	if info.Empty() || info.Tuning == nil || info.Tuning.Preset != "mixed" || info.Tuning.Values["zram"] != "on" ||
		!reflect.DeepEqual(info.Prio, []tuning.PrioSet{{Unit: "docker.service", Level: "high"}}) {
		t.Fatalf("%+v", info)
	}

	// Verwerfen nimmt nur das Tuning weg.
	write(t, filepath.Join(data, ort.ShieldFile), `{"geschlossen":[{"proto":"tcp","von":3552,"bis":3552}]}`)
	info = BuildIn(root, data, "alt")
	put := func(i Info) {
		b, _ := json.Marshal(i)
		write(t, filepath.Join(data, ort.OldServerFile), string(b))
	}
	put(info)
	if l := Load(data); l == nil || l.Tuning == nil || !l.Has("tcp", 3552, 3552) {
		t.Fatalf("Load: %+v", l)
	}
	if err := DropTuning(data); err != nil {
		t.Fatal(err)
	}
	if l := Load(data); l == nil || l.Tuning != nil || l.Prio != nil || !l.Has("tcp", 3552, 3552) {
		t.Fatalf("nach Verwerfen: %+v", l)
	}
	// Bleibt nichts, fällt die Datei weg.
	put(Info{From: "alt", Tuning: info.Tuning})
	if err := DropTuning(data); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, ort.OldServerFile)); !os.IsNotExist(err) {
		t.Fatalf("Datei bleibt: %v", err)
	}
	if err := DropTuning(data); err != nil {
		t.Fatalf("ohne Datei: %v", err)
	}
}
