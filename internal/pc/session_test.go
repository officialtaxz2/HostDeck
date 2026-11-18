package pc_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/pc"
	"github.com/officialtaxz2/hostdeck/internal/remote"
)

type fakeConsole struct{ lines chan string }

func (c *fakeConsole) Lines() <-chan string    { return c.lines }
func (c *fakeConsole) Secret() (string, error) { return "geheim", nil }

// fakeSSH schreibt ein Skript, das sich wie ssh verhält, aber den
// Agenten nur nachspielt: Hallo, Agent-Zeile, Config, eine Frage, Ende.
func fakeSSH(t *testing.T, agent string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "ssh")
	script := "#!/bin/sh\necho \"$@\" > \"$(dirname \"$0\")/args\"\n" + agent
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSitzungSprichtMitDemServer(t *testing.T) {
	ssh := fakeSSH(t, `echo "HOSTDECK-HALLO 1 x86_64 testhost test"
read -r cmd n
echo "HOSTDECK-AGENT test x"
read -r cfg
echo "$cfg" > "$(dirname "$0")/cfg"
echo "Hallo vom Server"
echo "@@hostdeck:frage 1 0 Notiert?"
pulse=0
while read -r ans; do [ "$ans" = puls ] || break; pulse=1; done
echo "Antwort war: $ans"
echo "Herzschlag: $pulse"
echo "@@hostdeck:ende 0"
`)
	var out, errb bytes.Buffer
	con := &fakeConsole{lines: make(chan string, 1)}
	con.lines <- "ja"
	folder := t.TempDir()
	s := &pc.Session{Server: "root@test", Folder: folder, SSH: ssh, Version: "test", Out: &out, Err: &errb, Console: con}
	code, err := s.Run(context.Background(), remote.Config{Action: remote.ActionBackup})
	if err != nil || code != 0 {
		t.Fatalf("Exit %d, %v\n%s\n%s", code, err, out.String(), errb.String())
	}
	for _, want := range []string{"Hallo vom Server", "Notiert?", "Antwort war: antwort 1 ja", "Herzschlag: 1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("Ausgabe ohne %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "@@hostdeck:") {
		t.Errorf("Steuerzeile in der Ausgabe:\n%s", out.String())
	}
	args, _ := os.ReadFile(filepath.Join(filepath.Dir(ssh), "args"))
	if !strings.Contains(string(args), "-R 127.0.0.1:18800:127.0.0.1:") || !strings.Contains(string(args), "ExitOnForwardFailure=yes") {
		t.Errorf("ssh ohne Rückwärts-Tunnel aufgerufen: %s", args)
	}
	cfg, _ := os.ReadFile(filepath.Join(filepath.Dir(ssh), "cfg"))
	if !strings.Contains(string(cfg), `"key":"`) || strings.Contains(string(args), "key") {
		t.Errorf("Zugangsschlüssel fehlt in der Anweisung oder steht in den Argumenten:\n%s\n%s", cfg, args)
	}
	if _, err := os.Stat(pc.RepoDir(folder, "testhost")); err == nil {
		t.Log("Repository-Ordner schon angelegt (erst beim ersten Zugriff nötig)")
	}
}

func TestSitzungOhneTunnelBeimProbelauf(t *testing.T) {
	ssh := fakeSSH(t, `echo "HOSTDECK-HALLO 1 x86_64 testhost test"
read -r cmd n
echo "HOSTDECK-AGENT test x"
read -r cfg
echo "@@hostdeck:ende 2"
`)
	var out, errb bytes.Buffer
	s := &pc.Session{Server: "root@test", SSH: ssh, Version: "test", Out: &out, Err: &errb, Console: &fakeConsole{}}
	code, err := s.Run(context.Background(), remote.Config{Action: remote.ActionBackup, Plan: true})
	if err != nil || code != 2 {
		t.Fatalf("Exit %d, %v", code, err)
	}
	args, _ := os.ReadFile(filepath.Join(filepath.Dir(ssh), "args"))
	if strings.Contains(string(args), "-R") {
		t.Errorf("Probelauf öffnet einen Tunnel: %s", args)
	}
}

func TestSitzungLehntFremdeVersionAb(t *testing.T) {
	ssh := fakeSSH(t, `echo "HOSTDECK-HALLO 1 x86_64 testhost alt"
read -r cmd n
echo "$cmd" > "$(dirname "$0")/cmd"
`)
	var out, errb bytes.Buffer
	s := &pc.Session{Server: "root@test", SSH: ssh, Version: "neu", BinDir: t.TempDir(), Out: &out, Err: &errb, Console: &fakeConsole{}}
	if _, err := s.Run(context.Background(), remote.Config{Action: remote.ActionStart}); err == nil || !strings.Contains(err.Error(), "hostdeck-linux-amd64 fehlt") {
		t.Errorf("fehlende Linux-Datei nicht gemeldet: %v", err)
	}
}

func TestVerbindungScheitert(t *testing.T) {
	ssh := fakeSSH(t, `echo "Permission denied (publickey,password)." >&2; exit 255`)
	var out, errb bytes.Buffer
	s := &pc.Session{Server: "root@test", SSH: ssh, Version: "test", Out: &out, Err: &errb, Console: &fakeConsole{}}
	_, err := s.Run(context.Background(), remote.Config{Action: remote.ActionStart})
	if err == nil || !strings.Contains(err.Error(), "Verbindung zum Server fehlgeschlagen") {
		t.Errorf("Fehler: %v", err)
	}
	if !strings.Contains(errb.String(), "Permission denied") {
		t.Error("Meldung von ssh nicht durchgereicht")
	}
}
