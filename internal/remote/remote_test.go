package remote_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/remote"
)

const fakeHostdeck = `#!/bin/sh
if [ "$1" = version ]; then echo "HostDeck test-1"; exit 0; fi
echo "AGENT $1"
cat
`

func runBootstrap(t *testing.T, home string, stdin []byte) string {
	t.Helper()
	cmd := exec.Command("sh", "-c", remote.BootstrapCommand())
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Startskript: %v\n%s", err, out)
	}
	return string(out)
}

func TestStartskriptUebertraegtProgrammUndReichtEingabeWeiter(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("Test braucht root (sonst ruft das Skript sudo)")
	}
	for _, tool := range []string{"sh", "base64", "head", "uname"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s fehlt", tool)
		}
	}
	home := t.TempDir()
	var in bytes.Buffer
	in.WriteString("BIN " + itoa(len(fakeHostdeck)) + "\n")
	in.WriteString(fakeHostdeck)
	in.WriteString("{\"v\":1}\nantwort 1 ja\n")
	out := runBootstrap(t, home, in.Bytes())
	lines := strings.Split(strings.TrimSpace(out), "\n")
	h, err := remote.ParseHello(lines[0])
	if err != nil || h.Version != "keins" {
		t.Fatalf("Hallo: %q %v", lines[0], err)
	}
	if !strings.Contains(out, "AGENT agent\n{\"v\":1}\nantwort 1 ja") {
		t.Errorf("Eingabe kam nicht vollständig beim Agenten an:\n%s", out)
	}
	fi, err := os.Stat(filepath.Join(home, ".hostdeck", "bin", "hostdeck"))
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("Programm nicht mit 0700 abgelegt: %v %v", fi, err)
	}

	// Zweiter Start: Version ist bekannt, nichts wird übertragen.
	out = runBootstrap(t, home, []byte("WEITER 0\nrest\n"))
	h, _ = remote.ParseHello(strings.Split(out, "\n")[0])
	if h.Version != "test-1" || !strings.Contains(out, "AGENT agent\nrest") {
		t.Errorf("zweiter Start: %q\n%s", h.Version, out)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestSteuerzeilen(t *testing.T) {
	c, ok := remote.ParseControl(remote.Ask(7, true, "Passwort:"))
	if !ok || c.Kind != "frage" || c.ID != 7 || !c.Secret || c.Text != "Passwort:" {
		t.Errorf("Frage: %+v", c)
	}
	if c, ok := remote.ParseControl(remote.End(2)); !ok || c.Code != 2 {
		t.Errorf("Ende: %+v", c)
	}
	if _, ok := remote.ParseControl("normale Zeile"); ok {
		t.Error("normale Zeile als Steuerzeile erkannt")
	}
	if _, err := remote.ParseHello("HOSTDECK-HALLO 1 riscv64 host keins"); err == nil {
		t.Error("unbekannter Prozessor angenommen")
	}
	if h, err := remote.ParseHello("HOSTDECK-HALLO 1 x86_64 host keins 0123abcd"); err != nil || h.MachineID != "0123abcd" {
		t.Errorf("Rechner-Kennung: %+v %v", h, err)
	}
	if h, err := remote.ParseHello("HOSTDECK-HALLO 1 x86_64 host keins -"); err != nil || h.MachineID != "" {
		t.Errorf("ohne Rechner-Kennung: %+v %v", h, err)
	}
}

func TestServerInputFragenUndAbbruch(t *testing.T) {
	pr, pw := io.Pipe()
	var out bytes.Buffer
	sw := &remote.SyncWriter{W: &out}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		pw.Write([]byte(`{"v":1,"action":"backup","port":18800}` + "\n"))
	}()
	cfg, in, err := remote.ReadConfig(pr, sw, cancel)
	if err != nil || cfg.Action != "backup" || cfg.Port != 18800 {
		t.Fatalf("Config: %+v %v", cfg, err)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		pw.Write([]byte("antwort 99 alt\nantwort 1 ja\n"))
	}()
	ans, err := in.Ask(ctx, backup.Question{Text: "Notiert?"})
	if err != nil || ans != "ja" {
		t.Errorf("Antwort: %q %v", ans, err)
	}
	if !strings.Contains(out.String(), "@@hostdeck:frage 1 0 Notiert?") {
		t.Errorf("Frage nicht gesendet: %q", out.String())
	}
	// Frage mit Zeitlimit endet mit frage-ende.
	actx, acancel := context.WithTimeout(ctx, 10*time.Millisecond)
	if _, err := in.Ask(actx, backup.Question{Text: "Enter"}); err == nil {
		t.Error("Frage ohne Antwort endet ohne Fehler")
	}
	acancel()
	if !strings.Contains(out.String(), "@@hostdeck:frage-ende 2") {
		t.Errorf("frage-ende fehlt: %q", out.String())
	}
	// Verbindung weg: Lauf bricht ab.
	pw.Close()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Error("Ende der Eingabe bricht nicht ab")
	}
}

func TestHerzschlagBleibtAus(t *testing.T) {
	old := remote.HeartbeatTimeout
	remote.HeartbeatTimeout = 120 * time.Millisecond
	t.Cleanup(func() { remote.HeartbeatTimeout = old })

	start := func() (*io.PipeWriter, context.Context, *remote.ServerInput, *atomic.Bool) {
		pr, pw := io.Pipe()
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(func() { cancel(); pw.Close() })
		go pw.Write([]byte(`{"v":1,"action":"backup"}` + "\n"))
		_, in, err := remote.ReadConfig(pr, &remote.SyncWriter{W: io.Discard}, cancel)
		if err != nil {
			t.Fatal(err)
		}
		logged := new(atomic.Bool)
		in.OnTimeout(func() { logged.Store(true) })
		return pw, ctx, in, logged
	}

	// Ohne einen ersten Herzschlag (etwa hostdeck agent von Hand) gibt es keine Frist.
	_, ctx, _, _ := start()
	select {
	case <-ctx.Done():
		t.Error("Abbruch ohne je einen Herzschlag")
	case <-time.After(400 * time.Millisecond):
	}

	// Regelmäßiger Herzschlag hält den Lauf am Leben, Ausbleiben bricht ab.
	pw, ctx, _, logged := start()
	for i := 0; i < 8; i++ {
		pw.Write([]byte("puls\n"))
		time.Sleep(40 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("Abbruch trotz Herzschlag")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("kein Abbruch, obwohl der Herzschlag ausbleibt")
	}
	time.Sleep(10 * time.Millisecond)
	if !logged.Load() {
		t.Error("Abbruch nicht protokolliert")
	}
}

func TestFrageInfo(t *testing.T) {
	q := backup.Question{Text: "Tippe ja:", Note: "Jetzt?", Choices: []backup.Choice{{Label: "Ja", Answer: "ja"}}}
	l := remote.AskInfoLine(3, q)
	c, ok := remote.ParseControl(l)
	if !ok || c.Kind != "frage-info" || c.ID != 3 || c.Info.Note != "Jetzt?" || len(c.Info.Choices) != 1 || c.Info.Choices[0].Answer != "ja" {
		t.Fatalf("%q → %+v %v", l, c, ok)
	}
	if remote.AskInfoLine(1, backup.Question{Text: "x"}) != "" {
		t.Fatal("Zeile ohne Inhalt")
	}
	if _, ok := remote.ParseControl(remote.Prefix + "frage-info 1 {kaputt"); ok {
		t.Fatal("kaputtes JSON als Steuerzeile erkannt")
	}
}
