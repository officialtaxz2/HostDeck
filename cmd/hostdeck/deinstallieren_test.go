package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeShield struct {
	closed string
	log    *[]string
	err    error
}

func (s *fakeShield) ClosedText() string { return s.closed }
func (s *fakeShield) OpenAll(context.Context) error {
	*s.log = append(*s.log, "ports")
	if s.err == nil {
		s.closed = ""
	}
	return s.err
}

func testUninstaller(t *testing.T) (*uninstaller, *fakeSystem, *[]string) {
	t.Helper()
	in, fs, out := testInstaller(t, "")
	if err := in.install(context.Background()); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(in.DataDir, "protokoll.jsonl"), []byte("{}\n"), 0o600)
	state := filepath.Join(filepath.Dir(in.DataDir), "root-hostdeck")
	os.MkdirAll(state, 0o700)
	log := &[]string{}
	fs.calls = nil
	u := &uninstaller{
		In:       in,
		StateDir: state,
		RunDirs:  []string{filepath.Join(t.TempDir(), "run")},
		Domain:   "leitstand.example.test",
		Unpublish: func(context.Context) error {
			*log = append(*log, "caddy")
			return nil
		},
		Shield: &fakeShield{closed: "25565 (TCP)", log: log},
		Tuned:  true,
		Restore: func(_ context.Context, out io.Writer) (bool, error) {
			*log = append(*log, "tuning")
			return true, nil
		},
		Remains: []string{"Swap-Datei /swapfile (2 GiB, in /etc/fstab)"},
		Size:    func(string) int64 { return 3 << 20 },
		Out:     out,
	}
	os.MkdirAll(u.RunDirs[0], 0o755)
	return u, fs, log
}

func TestDeinstallierenReihenfolge(t *testing.T) {
	u, fs, log := testUninstaller(t)
	p := &scriptPrompt{answers: []string{"j", "", ""}}
	ok, err := u.ask(p, false, false)
	if err != nil || !ok {
		t.Fatalf("ask: %v %v", ok, err)
	}
	if !u.RestoreTuning || u.DeleteData {
		t.Fatalf("Antworten: Tuning %v, Daten löschen %v", u.RestoreTuning, u.DeleteData)
	}
	plan := u.In.Out.(interface{ String() string }).String()
	for _, want := range []string{
		"https://leitstand.example.test",
		"25565 (TCP) ist danach wieder von außen erreichbar",
		"Swap-Datei /swapfile",
		"Daten von HostDeck: " + u.In.DataDir + " (3 MB) und " + u.StateDir,
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("Plan ohne %q:\n%s", want, plan)
		}
	}
	if !strings.Contains(p.out.String(), "Server-Tuning (tune.sh)") {
		t.Errorf("keine Frage zum Tuning: %s", p.out.String())
	}
	if err := u.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*log, ","); got != "tuning,caddy,ports" {
		t.Errorf("Reihenfolge %s", got)
	}
	if len(fs.calls) == 0 || !strings.HasPrefix(fs.calls[0], "systemctl disable --now") {
		t.Errorf("Aufrufe: %q", fs.calls)
	}
	for _, gone := range []string{u.In.ProgDir, u.In.BinLink, u.RunDirs[0], filepath.Join(u.In.UnitDir, "hostdeck-dienst.service")} {
		if _, err := os.Lstat(gone); err == nil {
			t.Errorf("%s ist noch da", gone)
		}
	}
	for _, kept := range []string{u.In.DataDir, u.StateDir} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s fehlt: %v", kept, err)
		}
	}
	out := u.In.Out.(interface{ String() string }).String()
	for _, want := range []string{"Neustart des Servers", "Die Daten liegen weiter in " + u.In.DataDir, "Bleibt: Swap-Datei"} {
		if !strings.Contains(out, want) {
			t.Errorf("Ausgabe ohne %q:\n%s", want, out)
		}
	}
}

func TestDeinstallierenAbbruch(t *testing.T) {
	for _, answers := range [][]string{{""}, {"n"}, {}} {
		u, fs, log := testUninstaller(t)
		ok, err := u.ask(&scriptPrompt{answers: answers}, false, false)
		if ok || (len(answers) > 0 && err != nil) {
			t.Errorf("%q: ok %v, err %v", answers, ok, err)
		}
		if len(*log) > 0 || len(fs.calls) > 0 {
			t.Errorf("%q: etwas geändert: %v %v", answers, *log, fs.calls)
		}
		if _, err := os.Stat(u.In.ProgDir); err != nil {
			t.Errorf("Programm fehlt: %v", err)
		}
	}
}

func TestDeinstallierenMitDaten(t *testing.T) {
	u, _, log := testUninstaller(t)
	p := &scriptPrompt{answers: []string{"ja", "n", " Löschen "}}
	if ok, err := u.ask(p, false, false); err != nil || !ok {
		t.Fatalf("ask: %v %v", ok, err)
	}
	if u.RestoreTuning || !u.DeleteData {
		t.Fatalf("Antworten: Tuning %v, Daten löschen %v", u.RestoreTuning, u.DeleteData)
	}
	if !strings.Contains(p.out.String(), "„löschen“") {
		t.Errorf("Frage: %s", p.out.String())
	}
	if !strings.Contains(u.In.Out.(interface{ String() string }).String(), "backup-passwort --zeigen") {
		t.Error("kein Hinweis auf das Backup-Passwort")
	}
	if err := u.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*log, ","); got != "caddy,ports" {
		t.Errorf("Reihenfolge %s", got)
	}
	for _, gone := range []string{u.In.DataDir, u.StateDir} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("%s ist noch da", gone)
		}
	}
	// Ein „j“ allein löscht die Daten nicht.
	u2, _, _ := testUninstaller(t)
	u2.ask(&scriptPrompt{answers: []string{"j", "", "j"}}, false, false)
	if u2.DeleteData {
		t.Error("„j“ löscht die Daten")
	}
}

func TestDeinstallierenOhneRueckfrage(t *testing.T) {
	u, _, _ := testUninstaller(t)
	p := &scriptPrompt{}
	if ok, err := u.ask(p, true, false); err != nil || !ok {
		t.Fatalf("ask: %v %v", ok, err)
	}
	if p.out.Len() > 0 || !u.RestoreTuning || u.DeleteData {
		t.Errorf("--ja: gefragt %q, Tuning %v, Daten %v", p.out.String(), u.RestoreTuning, u.DeleteData)
	}
	u2, _, _ := testUninstaller(t)
	u2.ask(&scriptPrompt{}, true, true)
	if !u2.DeleteData || strings.Contains(u2.plan(), "gleich fragt HostDeck") {
		t.Errorf("--daten-loeschen: %v\n%s", u2.DeleteData, u2.plan())
	}
}

func TestDeinstallierenHaeltBeiFehler(t *testing.T) {
	t.Run("Caddy", func(t *testing.T) {
		u, fs, log := testUninstaller(t)
		u.Unpublish = func(context.Context) error { *log = append(*log, "caddy"); return errors.New("Caddy lädt nicht") }
		err := u.run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "HostDeck bleibt eingerichtet") {
			t.Fatalf("Fehler: %v", err)
		}
		if strings.Join(*log, ",") != "caddy" || len(fs.calls) > 0 {
			t.Errorf("weitergemacht: %v %v", *log, fs.calls)
		}
	})
	t.Run("Ports", func(t *testing.T) {
		u, fs, _ := testUninstaller(t)
		u.Shield.(*fakeShield).err = errors.New("nft fehlt")
		if err := u.run(context.Background()); err == nil {
			t.Fatal("kein Fehler")
		}
		if len(fs.calls) > 0 {
			t.Errorf("Dienste angefasst: %v", fs.calls)
		}
		if _, err := os.Stat(u.In.ProgDir); err != nil {
			t.Errorf("Programm fehlt: %v", err)
		}
	})
	t.Run("Tuning", func(t *testing.T) {
		u, _, log := testUninstaller(t)
		u.RestoreTuning = true
		u.Restore = func(context.Context, io.Writer) (bool, error) { return false, errors.New("Code 1") }
		if err := u.run(context.Background()); err == nil {
			t.Fatal("kein Fehler")
		}
		if len(*log) > 0 {
			t.Errorf("weitergemacht: %v", *log)
		}
	})
}
