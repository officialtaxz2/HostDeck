package leistungstest

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Das eingebettete Skript ist benchmark.sh mit dem Schalter
// GEEKBENCH. Jede Änderung muss den Fingerabdruck bewusst mitändern.
func TestSkriptUnveraendert(t *testing.T) {
	sum := sha256.Sum256(Script)
	if got := hex.EncodeToString(sum[:]); got != ScriptSHA256 {
		t.Fatalf("benchmark.sh hat sich geändert: %s, erwartet %s", got, ScriptSHA256)
	}
	for _, want := range []string{`GEEKBENCH="${GEEKBENCH:-1}"`, "GEEKBENCH_VERSION='6.7.1'", `SYSBENCH_SECONDS="${SYSBENCH_SECONDS:-120}"`} {
		if !bytes.Contains(Script, []byte(want)) {
			t.Errorf("Skript ohne %q", want)
		}
	}
	if _, err := exec.LookPath("bash"); err == nil {
		if out, err := exec.Command("bash", "-n", "benchmark.sh").CombinedOutput(); err != nil {
			t.Fatalf("bash -n: %v\n%s", err, out)
		}
	}
}

func TestMessuebersichtLesen(t *testing.T) {
	dir := t.TempDir()
	copyDir(t, "testdata/messung", dir)
	write(t, filepath.Join(dir, "01-geekbench", "geekbench.log"), "Geekbench 6.7.1\n\n  Single-Core Score          1234\n  Multi-Core Score           4567\n")
	var r Result
	parseInto(&r, dir)
	if r.SysbenchSingle != 1065.44 || r.SysbenchMulti != 4333.51 || r.Threads != 4 {
		t.Fatalf("Sysbench: %+v", r)
	}
	if r.GeekSingle != 1234 || r.GeekMulti != 4567 || r.GeekURL != "" {
		t.Fatalf("Geekbench: %+v", r)
	}
	if r.CPU != "Intel(R) Xeon(R) Processor @ 2.10GHz" || r.VCPUs != 4 {
		t.Fatalf("System: %q %d", r.CPU, r.VCPUs)
	}
	if len(r.Steal) != 2 {
		t.Fatalf("Steal-Phasen: %+v", r.Steal)
	}
	m := r.Steal[1]
	if m.Phase != "Sysbench 4 Threads" || m.Intervals != 5 || m.Mean != 0.85 || m.Max != 2 || m.MaxVCPU != 4 || m.LoadMean == nil || *m.LoadMean != 1.25 || *m.LoadMax != 2 {
		t.Fatalf("Steal Multi: %+v", m)
	}
	if !strings.Contains(r.Summary, "CPU-PERFORMANCEMESSUNG") {
		t.Fatal("Übersicht fehlt")
	}
}

func TestStealMitGeekbenchUndLink(t *testing.T) {
	sum := "3. Benchmark-Ergebnisse\nGeekbench-Ergebnis (oeffentliche URL, sofern vorhanden):\nhttps://browser.geekbench.com/v6/cpu/123456\n\n4. CPU-Steal-Time (mpstat)\n\n[Geekbench 6]\nSekundenintervalle: 200\nSteal, Mittel aller Intervalle: 3.10 %\nSteal, Maximum (CPU all): 12.50 %\nIntervalle mit >=10 % / >=20 % Steal: 4 / 0\nKeine Intervalle mit CPU-Arbeit >=20 % beobachtet\n\n5. Messprotokolle\n"
	dir := t.TempDir()
	write(t, filepath.Join(dir, "00-Messuebersicht.txt"), sum)
	var r Result
	parseInto(&r, dir)
	if r.GeekURL != "https://browser.geekbench.com/v6/cpu/123456" {
		t.Fatalf("Link: %q", r.GeekURL)
	}
	if len(r.Steal) != 1 || r.Steal[0].Over10 != 4 || r.Steal[0].Max != 12.5 || r.Steal[0].LoadMean != nil {
		t.Fatalf("Steal: %+v", r.Steal)
	}
}

// fakeScript tut, was benchmark.sh am Ende hinterlässt.
const fakeScript = `#!/bin/bash
set -eu
[[ "$HOME" == "$PWD" ]] || { echo "HOME falsch: $HOME" >&2; exit 9; }
echo "GEEKBENCH=$GEEKBENCH SYSBENCH_SECONDS=$SYSBENCH_SECONDS"
S=20261109-120000
D="$HOME/vps-cpu-evidence-$S"
mkdir -p "$D/02-sysbench"
cp -r "$FIXTURE/." "$D/"
printf 'https://browser.geekbench.com/v6/cpu/1/claim?key=abc\n' > "$HOME/geekbench-private-claim-$S.txt"
if [[ "${OHNE_ZIP:-}" != 1 ]]; then (cd "$HOME" && zip -q -r "vps-cpu-evidence-$S.zip" "vps-cpu-evidence-$S"); fi
exit "${CODE:-0}"
`

func rig(t *testing.T, script string, env ...string) *Service {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash fehlt")
	}
	fix, _ := filepath.Abs("testdata/messung")
	head := "#!/bin/bash\nexport FIXTURE=" + fix + "\n"
	for _, e := range env {
		head += "export " + e + "\n"
	}
	return &Service{Dir: filepath.Join(t.TempDir(), "leistungstest"), Script: []byte(head + script), Host: "testserver", Grace: 5 * time.Second}
}

func TestLaufLegtErgebnisAb(t *testing.T) {
	if _, err := exec.LookPath("zip"); err != nil {
		t.Skip("zip fehlt")
	}
	s := rig(t, fakeScript)
	var out bytes.Buffer
	r, err := s.Run(context.Background(), Options{Geekbench: true, Seconds: 30}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "GEEKBENCH=1 SYSBENCH_SECONDS=30") {
		t.Fatalf("Umgebung: %s", out.String())
	}
	if r.ID != "20261109-120000" || r.State != StateDone || r.Host != "testserver" || r.SysbenchMulti != 4333.51 || !r.Claim || r.ZipSize == 0 || r.Packed {
		t.Fatalf("Ergebnis: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(s.work(), "vps-cpu-evidence-"+r.ID)); !os.IsNotExist(err) {
		t.Fatal("Messordner liegt noch im Arbeitsordner")
	}
	fi, err := os.Stat(filepath.Join(s.Dir, r.ID+".claim"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("Claim-Datei: %v %v", fi, err)
	}
	if l, err := s.Claim(r.ID); err != nil || !strings.HasSuffix(l, "claim?key=abc") {
		t.Fatalf("Claim: %q %v", l, err)
	}
	list, err := s.List()
	if err != nil || len(list) != 1 || list[0].Summary != "" || list[0].SysbenchSingle != 1065.44 {
		t.Fatalf("Liste: %+v %v", list, err)
	}
	rep, err := s.Report(r.ID)
	if err != nil || !strings.Contains(rep.Summary, "Sysbench multi-core") {
		t.Fatalf("Bericht: %v", err)
	}
	p, err := s.ZipPath(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range zr.File {
		found = found || f.Name == "vps-cpu-evidence-20261109-120000/00-Messuebersicht.txt"
	}
	zr.Close()
	if !found {
		t.Fatal("ZIP ohne Messübersicht")
	}
	if err := s.Delete(r.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Fatal("nach Löschen noch da")
	}
	if err := s.Delete(r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("zweites Löschen: %v", err)
	}
}

func TestOhneZipPacktHostdeckSelbst(t *testing.T) {
	s := rig(t, fakeScript, "OHNE_ZIP=1", "CODE=1")
	r, err := s.Run(context.Background(), Options{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if r.State != StateIncomplete || r.Code != 1 || !r.Packed || r.ZipSize == 0 || r.Seconds != DefaultSeconds || r.Geekbench {
		t.Fatalf("Ergebnis: %+v", r)
	}
	zr, err := zip.OpenReader(filepath.Join(s.Dir, r.ID+".zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["vps-cpu-evidence-20261109-120000/02-sysbench/multi-core-sysbench.log"] {
		t.Fatalf("ZIP: %v", names)
	}
}

func TestOhneMessungBleibtAusgabe(t *testing.T) {
	s := rig(t, "echo 'APT-Installation fehlgeschlagen.' >&2\nexit 1\n")
	s.Now = func() time.Time { return time.Date(2026, 11, 9, 12, 0, 0, 0, time.Local) }
	r, err := s.Run(context.Background(), Options{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if r.State != StateFailed || r.ID != "20261109-120000" || len(r.Tail) != 1 || r.Tail[0] != "APT-Installation fehlgeschlagen." {
		t.Fatalf("Ergebnis: %+v", r)
	}
	if list, _ := s.List(); len(list) != 1 {
		t.Fatal("gescheiterter Test fehlt in der Liste")
	}
}

// Abbrechen schickt SIGTERM; das Skript schreibt wie bei Strg+C seine
// Übersicht (hier nachgestellt), HostDeck packt den Rest.
func TestAbbrechen(t *testing.T) {
	script := `S=20261109-130000
D="$HOME/vps-cpu-evidence-$S"
mkdir -p "$D"
finish() { echo "Status: Unvollstaendig" > "$D/00-Messuebersicht.txt"; exit 143; }
trap finish TERM
echo bereit
sleep 30 & wait
`
	s := rig(t, script)
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw, _ := os.Pipe()
	go func() {
		buf := make([]byte, 64)
		n, _ := pr.Read(buf)
		if strings.Contains(string(buf[:n]), "bereit") {
			cancel()
		}
	}()
	start := time.Now()
	r, err := s.Run(ctx, Options{}, pw)
	pw.Close()
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("Abbrechen dauerte zu lange")
	}
	if r.State != StateCancelled || r.ID != "20261109-130000" || !r.Packed {
		t.Fatalf("Ergebnis: %+v", r)
	}
}

func TestEinstellungen(t *testing.T) {
	for _, o := range []Options{{Seconds: 5}, {Seconds: 4000}, {Seconds: -1}} {
		if err := o.Check(); !errors.Is(err, ErrOptions) {
			t.Errorf("%+v: %v", o, err)
		}
	}
	if err := (Options{}).Check(); err != nil {
		t.Fatal(err)
	}
	s := &Service{Dir: t.TempDir()}
	for _, id := range []string{"../x", "2026", "20261109-120000/../a", ""} {
		if _, err := s.ZipPath(id); !errors.Is(err, ErrBadID) {
			t.Errorf("%q: %v", id, err)
		}
	}
}

func TestPaketeUndGeekbench(t *testing.T) {
	root := t.TempDir()
	for _, c := range []string{"zip", "unzip", "curl", "tar", "lscpu", "nproc", "stdbuf", "tee", "mktemp", "awk", "sed", "grep", "vmstat", "apt-get"} {
		write(t, filepath.Join(root, "usr/bin", c), "")
		os.Chmod(filepath.Join(root, "usr/bin", c), 0o755)
	}
	write(t, filepath.Join(root, "etc/ssl/certs/ca-certificates.crt"), "x")
	s := &Service{Dir: filepath.Join(root, "lt"), Root: root, Arch: "arm64"}
	in := s.Info()
	if strings.Join(in.Missing, " ") != "sysbench sysstat" || in.NoApt {
		t.Fatalf("Pakete: %+v", in)
	}
	if in.GeekbenchOK || in.GeekbenchWhy == "" {
		t.Fatalf("arm64 ohne Geekbench: %+v", in)
	}
	write(t, s.geekbenchBin(), "")
	os.Chmod(s.geekbenchBin(), 0o755)
	if in := s.Info(); !in.GeekbenchOK || !in.GeekbenchReady {
		t.Fatalf("Geekbench schon da: %+v", in)
	}
	os.Remove(filepath.Join(root, "usr/bin/apt-get"))
	os.Remove(filepath.Join(root, "etc/ssl/certs/ca-certificates.crt"))
	in = (&Service{Dir: s.Dir, Root: root, Arch: "amd64"}).Info()
	if !in.NoApt || in.Missing[len(in.Missing)-1] != "ca-certificates" || !in.GeekbenchOK {
		t.Fatalf("ohne apt: %+v", in)
	}
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

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if fi.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Im EXIT-Trap nach einem Signal gab ein nacktes `return` in
// analyze_mpstat den Signal-Status zurück, und `set -e` beendete finish()
// mitten in der Messübersicht. Mit `return 0` läuft finish() durch.
func TestAnalyseImTrapNachSignal(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("ohne bash")
	}
	src := string(Script)
	i := strings.Index(src, "\nanalyze_mpstat() {")
	if i < 0 {
		t.Fatal("analyze_mpstat fehlt")
	}
	j := strings.Index(src[i:], "\n}\n")
	fn := src[i : i+j+3]
	prog := "set -euo pipefail\n" + fn + `
finish() { analyze_mpstat /gibt-es-nicht 'Geekbench 6'; analyze_mpstat /gibt-es-nicht 'Sysbench 1 Thread'; echo ENDE; }
trap 'finish $?' EXIT
trap 'exit 143' TERM
kill -TERM $$
sleep 5
`
	out, _ := exec.Command("bash", "-c", prog).CombinedOutput()
	if !strings.Contains(string(out), "[Sysbench 1 Thread]") || !strings.Contains(string(out), "ENDE") {
		t.Fatalf("finish() bricht nach einem Signal ab:\n%s", out)
	}
}

// Echter Abbruch mit GEEKBENCH=0 nach der ersten Sysbench-Phase: Das Skript
// schreibt die ganze Messübersicht samt Steal und packt seine ZIP selbst.
// Braucht root, sysbench, mpstat und zip; dauert etwa eine halbe Minute.
func TestEchterAbbruchOhneGeekbench(t *testing.T) {
	if testing.Short() || os.Geteuid() != 0 {
		t.Skip("nur als root, nicht mit -short")
	}
	for _, p := range []string{"bash", "sysbench", "mpstat", "vmstat", "zip", "awk"} {
		if _, err := exec.LookPath(p); err != nil {
			t.Skip("ohne " + p)
		}
	}
	s := &Service{Dir: filepath.Join(t.TempDir(), "leistungstest"), Host: "testserver", Grace: 30 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr, pw, _ := os.Pipe()
	go func() {
		var seen strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := pr.Read(buf)
			seen.Write(buf[:n])
			if strings.Contains(seen.String(), "START Sysbench Multi-Core") {
				time.Sleep(2 * time.Second)
				cancel()
				return
			}
			if err != nil {
				return
			}
		}
	}()
	r, err := s.Run(ctx, Options{Seconds: 10}, pw)
	pw.Close()
	if err != nil {
		t.Fatal(err)
	}
	if r.State != StateCancelled || r.Packed || r.ZipSize == 0 {
		t.Fatalf("Ergebnis: Stand %s, von HostDeck gepackt %v, ZIP %d", r.State, r.Packed, r.ZipSize)
	}
	single := false
	for _, st := range r.Steal {
		if st.Phase == "Sysbench 1 Thread" && st.Intervals > 0 {
			single = true
		}
	}
	full, err := s.Report(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !single || !strings.Contains(full.Summary, "5. Messprotokolle") {
		t.Fatalf("Messübersicht unvollständig (Steal %+v):\n%s", r.Steal, full.Summary)
	}
}

func TestGeekbenchEntfernen(t *testing.T) {
	s := &Service{Dir: filepath.Join(t.TempDir(), "leistungstest"), Arch: "amd64"}
	bin := s.geekbenchBin()
	os.MkdirAll(filepath.Dir(bin), 0o700)
	os.WriteFile(bin, make([]byte, 1000), 0o700)
	os.WriteFile(filepath.Join(s.geekbenchDir(), "Geekbench-6.7.1-Linux.tar.gz"), make([]byte, 500), 0o600)
	if in := s.Info(); !in.GeekbenchReady || in.GeekbenchBytes != 1500 {
		t.Fatalf("vorher: %+v", in)
	}
	n, err := s.RemoveGeekbench()
	if err != nil || n != 1500 {
		t.Fatalf("Entfernen: %d %v", n, err)
	}
	if in := s.Info(); in.GeekbenchReady || in.GeekbenchBytes != 0 || !in.GeekbenchOK {
		t.Fatalf("nachher: %+v", in)
	}
}
