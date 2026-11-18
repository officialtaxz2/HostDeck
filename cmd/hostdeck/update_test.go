package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/lauf"
	"github.com/officialtaxz2/hostdeck/internal/protokoll"
	"github.com/officialtaxz2/hostdeck/internal/update"
)

func signed(t *testing.T, priv ed25519.PrivateKey, h update.Header, bin []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := update.Write(&b, priv, h, bin); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestUpdateAnnehmen(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	var started []string
	u := &updateReceiver{
		Dir: filepath.Join(dir, "update"), RunDir: filepath.Join(dir, "run"), Key: pub, Version: "1b-alt",
		Start: func(_ context.Context, prog string) error { started = append(started, prog); return nil },
	}
	ctx := context.Background()
	prog := filepath.Join(u.Dir, "hostdeck")
	here := update.Header{Version: "1b-neu", OS: runtime.GOOS, Arch: runtime.GOARCH}
	bin := []byte("\x7fELF neue Version")

	// Falscher Schlüssel: nichts liegt danach in der Ablage.
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := u.receive(ctx, bytes.NewReader(signed(t, otherPriv, here, bin))); !errors.Is(err, update.ErrSignature) {
		t.Fatalf("fremde Signatur: %v", err)
	}
	if _, err := os.Stat(prog); err == nil {
		t.Fatal("Programm mit fremder Signatur liegt in der Ablage")
	}
	if _, err := os.Stat(prog + ".teil"); err == nil {
		t.Fatal("Teildatei bleibt liegen")
	}

	// Falsches System.
	other := here
	other.Arch = "riscv64"
	if runtime.GOARCH == "riscv64" {
		other.Arch = "amd64"
	}
	if _, err := u.receive(ctx, bytes.NewReader(signed(t, priv, other, bin))); err == nil || !strings.Contains(err.Error(), "dieser Server braucht") {
		t.Fatalf("falsches System: %v", err)
	}

	// Gleiche Version.
	same := here
	same.Version = "1b-alt"
	if _, err := u.receive(ctx, bytes.NewReader(signed(t, priv, same, bin))); err == nil || !strings.Contains(err.Error(), "läuft schon") {
		t.Fatalf("gleiche Version: %v", err)
	}
	if len(started) != 0 {
		t.Fatalf("Einspielen gestartet: %v", started)
	}

	// Während eines anderen Laufs: abgelehnt.
	l, err := lauf.TakeIn(u.RunDir, "Backup")
	if err != nil {
		t.Fatal(err)
	}
	var busy *lauf.BusyError
	if _, err := u.receive(ctx, bytes.NewReader(signed(t, priv, here, bin))); !errors.As(err, &busy) {
		t.Fatalf("während eines Backups: %v", err)
	}
	l.Release()

	// Passt: Programm liegt bereit, Einspielen angestoßen.
	h, err := u.receive(ctx, bytes.NewReader(signed(t, priv, here, bin)))
	if err != nil || h.Version != "1b-neu" {
		t.Fatalf("Update: %+v %v", h, err)
	}
	if got, _ := os.ReadFile(prog); !bytes.Equal(got, bin) {
		t.Fatalf("Programm in der Ablage: %q", got)
	}
	if len(started) != 1 || started[0] != prog {
		t.Fatalf("Einspielen: %v", started)
	}
	// Die Laufsperre ist danach wieder frei (das Einspielen braucht sie).
	l, err = lauf.TakeIn(u.RunDir, "Einrichten")
	if err != nil {
		t.Fatalf("Laufsperre nach dem Annehmen: %v", err)
	}
	l.Release()
	if _, err := os.Stat(prog + headSuffix); err != nil {
		t.Fatalf("Kopf liegt nicht neben dem Programm: %v", err)
	}
}

// Ältere signierte Dateien spielt HostDeck nicht wieder ein.
func TestUpdateNichtZurueck(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	started := 0
	u := &updateReceiver{
		Dir: filepath.Join(dir, "update"), RunDir: filepath.Join(dir, "run"), Key: pub, Version: "3e",
		Start: func(context.Context, string) error { started++; return nil },
	}
	ctx := context.Background()
	prog := filepath.Join(u.Dir, "hostdeck")
	t0 := time.Date(2026, 11, 2, 18, 0, 0, 0, time.UTC)
	h := func(v string, made time.Time) update.Header {
		return update.Header{Version: v, OS: runtime.GOOS, Arch: runtime.GOARCH, Made: made}
	}
	bin := []byte("\x7fELF")

	// Ohne gemerkten Stand geht jede Version (etwa das erste Update).
	if _, err := u.receive(ctx, bytes.NewReader(signed(t, priv, h("3d", t0), bin))); err != nil {
		t.Fatalf("erstes Update: %v", err)
	}
	// Einspielen glückt: Der Kopf wird zum Stand.
	if err := markInstalled(prog); err != nil {
		t.Fatal(err)
	}
	if last, ok := readInstalled(u.Dir); !ok || last.Version != "3d" || !last.Made.Equal(t0) {
		t.Fatalf("Stand: %+v %v", last, ok)
	}
	if _, err := os.Stat(prog + headSuffix); err == nil {
		t.Fatal("Kopf bleibt liegen")
	}
	// Älter oder gleich alt: abgelehnt, nichts gestartet.
	for _, made := range []time.Time{t0.Add(-time.Hour), t0} {
		_, err := u.receive(ctx, bytes.NewReader(signed(t, priv, h("3c", made), bin)))
		if err == nil || !strings.Contains(err.Error(), "nicht neuer als das zuletzt eingespielte Update (Version 3d") {
			t.Fatalf("ältere Datei: %v", err)
		}
	}
	if started != 1 {
		t.Fatalf("gestartet: %d", started)
	}
	// Neuer: geht.
	if _, err := u.receive(ctx, bytes.NewReader(signed(t, priv, h("3g", t0.Add(time.Hour)), bin))); err != nil || started != 2 {
		t.Fatalf("neuere Datei: %v %d", err, started)
	}
	// Ohne Kopf (von Hand eingerichtet) bleibt der Stand.
	os.Remove(prog + headSuffix)
	if err := markInstalled(prog); err != nil {
		t.Fatal(err)
	}
	if last, _ := readInstalled(u.Dir); last.Version != "3d" {
		t.Fatalf("Stand nach Einrichten von Hand: %+v", last)
	}
}

func installedOld(t *testing.T, in *installer) {
	t.Helper()
	os.MkdirAll(in.ProgDir, 0o755)
	if err := os.WriteFile(in.prog(), []byte("\x7fELF alte Version"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(in.DataDir, 0o700)
}

func TestUpdateEinspielen(t *testing.T) {
	in, fs, _ := testInstaller(t, "")
	installedOld(t, in)
	journal := &protokoll.Log{File: filepath.Join(in.DataDir, "protokoll.jsonl")}
	calls := 0
	running := func(context.Context) (string, error) {
		calls++
		if calls == 1 {
			return "1b-alt", nil
		}
		return version, nil
	}
	if err := applyUpdate(context.Background(), in, journal, running, 0); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(in.prog()); string(got) != "\x7fELF HostDeck" {
		t.Fatalf("eingespieltes Programm: %q", got)
	}
	if got, _ := os.ReadFile(in.prog() + ".alt"); string(got) != "\x7fELF alte Version" {
		t.Fatalf("beiseitegelegte Version: %q", got)
	}
	if !strings.Contains(strings.Join(fs.calls, "\n"), "systemctl restart hostdeck-dienst") {
		t.Fatalf("Dienst nicht neu gestartet: %v", fs.calls)
	}
	e, _ := journal.Last(5)
	if len(e) != 1 || !strings.Contains(e[0].Text, "Update eingespielt: Version "+version+" (vorher 1b-alt)") {
		t.Fatalf("Protokoll: %+v", e)
	}
}

func TestUpdateHoltAlteVersionZurueck(t *testing.T) {
	in, fs, _ := testInstaller(t, "")
	installedOld(t, in)
	// Die alte Version hatte eigene Dienst-Dateien; die neue schreibt sie um.
	oldUnit := []byte("[Unit]\nDescription=alte Version\n")
	unitPath := filepath.Join(in.UnitDir, "hostdeck-dienst.service")
	if err := os.WriteFile(unitPath, oldUnit, 0o644); err != nil {
		t.Fatal(err)
	}
	// Der Kopf neben dem bereitgelegten Programm verfällt.
	os.WriteFile(in.Exe+headSuffix, []byte(`{"version":"x"}`), 0o600)
	journal := &protokoll.Log{File: filepath.Join(in.DataDir, "protokoll.jsonl")}
	running := func(context.Context) (string, error) { return "1b-alt", nil }
	err := applyUpdate(context.Background(), in, journal, running, 0)
	if err == nil || !strings.Contains(err.Error(), "Version 1b-alt läuft wieder") {
		t.Fatalf("Ergebnis: %v", err)
	}
	if got, _ := os.ReadFile(in.prog()); string(got) != "\x7fELF alte Version" {
		t.Fatalf("Programm nach dem Zurückholen: %q", got)
	}
	restarts := 0
	for _, c := range fs.calls {
		if strings.HasPrefix(c, "systemctl restart") {
			restarts++
		}
	}
	if restarts != 4 { // zwei beim Einspielen, zwei beim Zurückholen
		t.Fatalf("Neustarts: %v", fs.calls)
	}
	if got, _ := os.ReadFile(unitPath); string(got) != string(oldUnit) {
		t.Fatalf("Dienst-Datei nach dem Zurückholen: %q", got)
	}
	if _, err := os.Stat(filepath.Join(in.UnitDir, "hostdeck-web.service")); err == nil {
		t.Fatal("neue Dienst-Datei bleibt, obwohl die alte Version keine hatte")
	}
	if _, err := os.Stat(in.Exe + headSuffix); err == nil {
		t.Fatal("Kopf bleibt nach dem Fehlschlag")
	}
	all := strings.Join(fs.calls, "\n")
	if i, j := strings.LastIndex(all, "daemon-reload"), strings.LastIndex(all, "systemctl restart"); i < 0 || i > j {
		t.Fatalf("daemon-reload fehlt vor dem Neustart: %v", fs.calls)
	}
	e, _ := journal.Last(5)
	if len(e) != 1 || !strings.Contains(e[0].Text, "fehlgeschlagen") {
		t.Fatalf("Protokoll: %+v", e)
	}
}
