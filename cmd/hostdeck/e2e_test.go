package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/backup"
	"github.com/officialtaxz2/hostdeck/internal/pc"
	"github.com/officialtaxz2/hostdeck/internal/remote"
)

type e2eConsole struct{}

func (e2eConsole) Lines() <-chan string    { return make(chan string) }
func (e2eConsole) Secret() (string, error) { return "", nil }

// TestEndeZuEnde spielt den Weg vom PC zum Server ganz durch: echtes
// Startskript, echte Übertragung der Linux-Datei, echter Agent. Nur ssh
// ist ein Skript, das den Befehl hier ausführt, und Docker ein kleiner
// Nachbau am Socket.
func TestEndeZuEnde(t *testing.T) {
	if testing.Short() {
		t.Skip("baut HostDeck, dauert")
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("braucht Linux und root, weil der Agent sonst sudo nimmt")
	}
	bin := t.TempDir()
	name := "hostdeck-linux-" + runtime.GOARCH
	build := exec.Command("go", "build", "-ldflags", "-X main.version=e2e-test", "-o", filepath.Join(bin, name), ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("Bauen: %v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(bin, name))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if err := os.WriteFile(filepath.Join(bin, "SHA256SUMS"), []byte(hex.EncodeToString(sum[:])+"  "+name+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// ssh-Ersatz: führt das letzte Argument mit eigenem HOME aus und
	// zählt, wie oft er gerufen wurde.
	home := t.TempDir()
	ssh := filepath.Join(t.TempDir(), "ssh")
	script := "#!/bin/sh\nfor a; do last=$a; done\nexport HOME=" + home + "\nexec sh -c \"$last\"\n"
	if err := os.WriteFile(ssh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	t.Setenv("HOSTDECK_STATE_DIR", state)

	// Docker-Nachbau: kennt einen Container „spiel“, der nach dem Start läuft.
	sock := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var calls []string
	running := false
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/containers/abc123/start":
			running = true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/containers/abc123/json":
			json.NewEncoder(w).Encode(map[string]any{"Id": "abc123", "Name": "/spiel", "State": map[string]any{"Running": running, "Status": "running"}})
		default:
			http.Error(w, `{"message":"unbekannt"}`, http.StatusNotFound)
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()
	t.Setenv("HOSTDECK_DOCKER_SOCKET", sock)

	session := func() (int, string) {
		t.Helper()
		var out, errb bytes.Buffer
		s := &pc.Session{Server: "root@test", SSH: ssh, BinDir: bin, Version: "e2e-test", Out: &out, Err: &errb, Console: e2eConsole{}}
		code, err := s.Run(context.Background(), remote.Config{Action: remote.ActionStart})
		if err != nil {
			t.Fatalf("Sitzung: %v\n%s\n%s", err, out.String(), errb.String())
		}
		return code, out.String() + errb.String()
	}

	// 1. Lauf: HostDeck fehlt auf dem Server und wird übertragen; nichts vermerkt.
	code, out := session()
	if code != 0 || !strings.Contains(out, "Nichts zu tun") {
		t.Fatalf("erster Lauf: Exit %d\n%s", code, out)
	}
	got, err := os.ReadFile(filepath.Join(home, ".hostdeck", "bin", "hostdeck"))
	if err != nil || !bytes.Equal(got, b) {
		t.Fatalf("HostDeck nicht oder falsch auf dem Server abgelegt: %v", err)
	}
	if fi, _ := os.Stat(filepath.Join(home, ".hostdeck", "bin", "hostdeck")); fi.Mode().Perm() != 0o700 {
		t.Errorf("HostDeck auf dem Server hat Modus %v", fi.Mode().Perm())
	}

	// 2. Lauf: ein Container ist als gestoppt vermerkt; HostDeck ist schon da.
	st, err := backup.LoadStopState(backup.StopStatePath(state))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Add(backup.StopEntry{ID: "abc123", Name: "spiel", Run: "20261031-170000"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(filepath.Join(home, ".hostdeck", "bin", "hostdeck"))
	code, out = session()
	if code != 0 || !strings.Contains(out, "starte spiel") || !strings.Contains(out, "Alle laufen wieder") {
		t.Fatalf("zweiter Lauf: Exit %d\n%s", code, out)
	}
	after, _ := os.Stat(filepath.Join(home, ".hostdeck", "bin", "hostdeck"))
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("gleiche Version wurde trotzdem neu übertragen")
	}
	if _, err := os.Stat(backup.StopStatePath(state)); !os.IsNotExist(err) {
		t.Errorf("Vermerk nach dem Start nicht gelöscht: %v", err)
	}
	logs, _ := filepath.Glob(filepath.Join(state, "protokolle", "*-dienste-starten.log"))
	if len(logs) == 0 {
		t.Error("kein Protokoll auf dem Server")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, c := range calls {
		if !strings.HasSuffix(c, "/abc123/start") && !strings.HasSuffix(c, "/abc123/json") {
			t.Errorf("unerwarteter Docker-Aufruf %s", c)
		}
	}
}
