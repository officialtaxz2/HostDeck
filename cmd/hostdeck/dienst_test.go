package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/fixture"
	"github.com/officialtaxz2/hostdeck/internal/web"
)

type fakeSystem struct {
	mu    sync.Mutex
	calls []string
	users map[string]bool
}

func (f *fakeSystem) run(ctx context.Context, name string, args ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err() // wie exec.CommandContext
	}
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if name == "useradd" {
		f.users[args[len(args)-1]] = true
	}
	return nil
}

func testInstaller(t *testing.T, old string) (*installer, *fakeSystem, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	exe := filepath.Join(root, "download", "hostdeck")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	os.WriteFile(exe, []byte("\x7fELF HostDeck"), 0o700)
	for _, d := range []string{"etc/systemd/system", "usr/local/bin"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	fs := &fakeSystem{users: map[string]bool{}}
	var out bytes.Buffer
	in := &installer{
		ProgDir:   filepath.Join(root, "usr/local/lib/hostdeck"),
		DataDir:   filepath.Join(root, "var/lib/hostdeck"),
		ResultDir: filepath.Join(root, "var/lib/hostdeck/ergebnis"),
		UnitDir:   filepath.Join(root, "etc/systemd/system"),
		BinLink:   filepath.Join(root, "usr/local/bin/hostdeck"),
		WebUser:   "hostdeck-web", Exe: exe, Out: &out,
		Run:        fs.run,
		UserExists: func(n string) bool { fs.mu.Lock(); defer fs.mu.Unlock(); return fs.users[n] },
		FindOld:    func() (string, error) { return old, nil },
		Now:        func() time.Time { return time.Date(2026, 11, 1, 16, 0, 0, 0, time.Local) },
	}
	return in, fs, &out
}

func TestEinrichtenUndUmzugDerDaten(t *testing.T) {
	old := filepath.Join(t.TempDir(), "hostdeck-ergebnis")
	if code, so, se := discoverOnce(t, fixture.Beispiel(t, 1), old); code != 0 {
		t.Fatalf("discover: %s %s", so, se)
	}
	os.WriteFile(filepath.Join(old, "backup-laeufe.json"), []byte("[]\n"), 0o600)
	in, fs, out := testInstaller(t, old)
	if err := in.install(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Programm am festen Ort, für alle lesbar, nur root schreibt.
	fi, err := os.Stat(filepath.Join(in.ProgDir, "hostdeck"))
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("Programm: %v %v", fi, err)
	}
	if t2, _ := os.Readlink(in.BinLink); t2 != filepath.Join(in.ProgDir, "hostdeck") {
		t.Errorf("Verweis %s zeigt auf %q", in.BinLink, t2)
	}
	// Daten umgezogen, nur für root.
	for _, d := range []string{in.DataDir, in.ResultDir} {
		if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, fi, err)
		}
	}
	for _, f := range []string{"inventory.json", "manifest.yaml", "bericht.txt", "backup-laeufe.json"} {
		a, _ := os.ReadFile(filepath.Join(old, f))
		b, err := os.ReadFile(filepath.Join(in.ResultDir, f))
		if err != nil || !bytes.Equal(a, b) {
			t.Errorf("%s nicht gleich umgezogen: %v", f, err)
		}
		if fi, _ := os.Stat(filepath.Join(in.ResultDir, f)); fi == nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: Rechte %v", f, fi)
		}
	}
	if b, err := os.ReadFile(filepath.Join(old, movedNote)); err != nil || !strings.Contains(string(b), in.ResultDir) {
		t.Errorf("Hinweis im alten Ordner: %v", err)
	}
	if !strings.Contains(out.String(), "übernommen aus "+old) {
		t.Errorf("Ausgabe: %s", out)
	}
	want := []string{
		"useradd --system --user-group --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin hostdeck-web",
		"systemctl daemon-reload",
		"systemctl enable hostdeck-dienst.service hostdeck-web.service",
		"systemctl restart hostdeck-dienst.service",
		"systemctl restart hostdeck-web.service",
	}
	if strings.Join(fs.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("Aufrufe:\n%s\nerwartet:\n%s", strings.Join(fs.calls, "\n"), strings.Join(want, "\n"))
	}

	// Ein zweites Einrichten (Update) lässt Daten und Benutzer, wie sie sind.
	os.WriteFile(filepath.Join(in.ResultDir, "manifest.yaml"), []byte("geändert im Leitstand\n"), 0o600)
	fs.calls = nil
	out.Reset()
	if err := in.install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(in.ResultDir, "manifest.yaml")); string(b) != "geändert im Leitstand\n" {
		t.Error("zweites Einrichten hat die Auswahl überschrieben")
	}
	for _, c := range fs.calls {
		if strings.HasPrefix(c, "useradd") {
			t.Error("Benutzer zweimal angelegt")
		}
	}
	if strings.Contains(out.String(), "übernommen") {
		t.Error("Daten zweimal umgezogen")
	}
}

func TestEinrichtenOhneFruehereDaten(t *testing.T) {
	in, _, out := testInstaller(t, "")
	in.FindOld = func() (string, error) { return "", os.ErrNotExist }
	if err := in.install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Keine frühere Bestandsaufnahme") {
		t.Errorf("Ausgabe: %s", out)
	}
}

func TestEinrichtenLaesstFremdeDateiInBin(t *testing.T) {
	in, _, out := testInstaller(t, "")
	os.WriteFile(in.BinLink, []byte("#!/bin/sh\necho eigenes HostDeck\n"), 0o755)
	if err := in.install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(in.BinLink); !strings.Contains(string(b), "eigenes HostDeck") {
		t.Error("fremde Datei ersetzt")
	}
	if !strings.Contains(out.String(), "eigene Datei") {
		t.Errorf("kein Hinweis: %s", out)
	}
}

func TestEntfernenLaesstDaten(t *testing.T) {
	in, fs, _ := testInstaller(t, "")
	if err := in.install(context.Background()); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(in.ResultDir, "manifest.yaml"), []byte("x"), 0o600)
	fs.calls = nil
	if err := in.remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"hostdeck-dienst.service", "hostdeck-web.service"} {
		if _, err := os.Stat(filepath.Join(in.UnitDir, u)); err == nil {
			t.Errorf("%s noch da", u)
		}
	}
	if _, err := os.Lstat(in.BinLink); err == nil {
		t.Error("Verweis noch da")
	}
	if _, err := os.Stat(filepath.Join(in.ResultDir, "manifest.yaml")); err != nil {
		t.Error("Daten gelöscht")
	}
	if fs.calls[0] != "systemctl disable --now hostdeck-web.service hostdeck-dienst.service" {
		t.Errorf("Aufrufe: %v", fs.calls)
	}
}

func TestUnitDerWebsiteIstEingesperrt(t *testing.T) {
	u := unitWeb("/usr/local/lib/hostdeck/hostdeck", "hostdeck-web", "")
	for _, want := range []string{
		"ExecStart=/usr/local/lib/hostdeck/hostdeck web", "User=hostdeck-web", "NoNewPrivileges=yes", "ProtectSystem=strict", "ProtectHome=yes",
		"CapabilityBoundingSet=\n", "RestrictAddressFamilies=AF_UNIX\n", "IPAddressDeny=any", "MemoryMax=",
		"-/var/lib/hostdeck", "-/opt", "-/root", "-/var/lib/docker", "-/etc/caddy", "-/run/docker.sock", "-/var/run/docker.sock",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("hostdeck-web.service ohne %q", want)
		}
	}
	if strings.Contains(u, "AF_INET") {
		t.Error("Website darf kein Netzwerk")
	}
	d := unitDienst("/usr/local/lib/hostdeck/hostdeck")
	for _, want := range []string{"ExecStart=/usr/local/lib/hostdeck/hostdeck dienst", "RuntimeDirectory=hostdeck\n", "RuntimeDirectoryPreserve=yes", "StateDirectoryMode=0700"} {
		if !strings.Contains(d, want) {
			t.Errorf("hostdeck-dienst.service ohne %q", want)
		}
	}
	if strings.Contains(d, "User=") {
		t.Error("der Dienst läuft als root")
	}
}

func TestDienstAuswahlGiltVorGemerktemOrdner(t *testing.T) {
	saved := serviceResultDir
	t.Cleanup(func() { serviceResultDir = saved })
	serviceResultDir = t.TempDir()
	if got, _ := findResultDir("/root/hostdeck/hostdeck-ergebnis"); got == serviceResultDir {
		t.Fatal("Dienstordner ohne Auswahl gewählt")
	}
	os.WriteFile(filepath.Join(serviceResultDir, "manifest.yaml"), []byte("x"), 0o600)
	if got, _ := findResultDir("/root/hostdeck/hostdeck-ergebnis"); got != serviceResultDir {
		t.Fatalf("gemerkter alter Ordner gewinnt: %s", got)
	}
	var stderr bytes.Buffer
	if code := serveCmd(context.Background(), nil, io.Discard, &stderr); code != 2 || !strings.Contains(stderr.String(), "sudo hostdeck notausgang") {
		t.Fatalf("serve bei eingerichtetem Dienst: %d %s", code, stderr.String())
	}
}

// Der ganze Weg: Dienst, Website und Notausgang wie auf dem Server, nur in
// einem Ordner und mit dem eigenen Benutzer statt hostdeck-web und root.
func TestNotausgangBisZurAuswahl(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("nur Linux")
	}
	result := filepath.Join(t.TempDir(), "ergebnis")
	if code, so, se := discoverOnce(t, fixture.Beispiel(t, 1), result); code != 0 {
		t.Fatalf("discover: %s %s", so, se)
	}
	dir, err := os.MkdirTemp("", "hostdeckn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	dSock, nSock := filepath.Join(dir, "d.sock"), filepath.Join(dir, "n.sock")
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() { cancel(); wg.Wait() })

	dl, err := listenUnix(dSock, 0o666)
	if err != nil {
		t.Fatal(err)
	}
	d := &dienst.Dienst{Data: (&web.Server{Dir: result}).DataHandler(), RunDir: filepath.Join(dir, "run"), Version: "test", WebUID: os.Getuid(), AdminUID: os.Getuid()}
	wg.Add(1)
	go func() { defer wg.Done(); _ = d.Serve(ctx, dl) }()

	nl, err := listenUnix(nSock, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(nSock); fi.Mode().Perm() != 0o600 {
		t.Errorf("Notausgang-Socket: %v", fi.Mode().Perm())
	}
	wg.Add(1)
	go func() { defer wg.Done(); serveHTTP(ctx, nl, newWeb(dienst.NewClient(dSock), io.Discard), io.Discard) }()

	pr, pw := io.Pipe()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer pw.Close()
		runNotausgang(ctx, dienst.NewClient(dSock), nSock, 0, pw, pw)
	}()
	var help strings.Builder
	re := regexp.MustCompile(`http://127\.0\.0\.1:(\d+)/#([0-9a-f]{32})`)
	buf := make([]byte, 4096)
	var m []string
	for m == nil {
		n, err := pr.Read(buf)
		help.Write(buf[:n])
		m = re.FindStringSubmatch(help.String())
		if err != nil {
			t.Fatalf("keine Adresse: %v\n%s", err, help.String())
		}
	}
	go io.Copy(io.Discard, pr)
	if !strings.Contains(help.String(), "ssh -N -L "+m[1]+":127.0.0.1:"+m[1]) || !strings.Contains(help.String(), "nur einmal") {
		t.Errorf("Hilfe:\n%s", help.String())
	}
	baseURL := "http://127.0.0.1:" + m[1]
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar, Timeout: 10 * time.Second}

	redeem := func() int {
		req, _ := http.NewRequest("POST", baseURL+"/api/einmal", strings.NewReader(`{"token":"`+m[2]+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", baseURL)
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if resp, err := hc.Get(baseURL + "/api/state"); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("vor der Anmeldung: %v %v", resp, err)
	}
	if code := redeem(); code != 200 {
		t.Fatalf("Link einlösen: %d", code)
	}
	resp, err := hc.Get(baseURL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	var st web.State
	json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(st.Projects) == 0 {
		t.Fatalf("Auswahl über den Notausgang: %d, %d Projekte", resp.StatusCode, len(st.Projects))
	}
	resp, err = hc.Get(baseURL + "/api/lauf")
	if err != nil {
		t.Fatal(err)
	}
	var ds dienst.Status
	json.NewDecoder(resp.Body).Decode(&ds)
	resp.Body.Close()
	if ds.Version != "test" {
		t.Fatalf("Stand: %+v", ds)
	}
	// Der Link gilt nur einmal: ein zweiter Browser kommt damit nicht rein.
	jar2, _ := cookiejar.New(nil)
	hc.Jar = jar2
	if code := redeem(); code != http.StatusForbidden {
		t.Fatalf("zweites Einlösen: %d", code)
	}
	if c, err := net.Dial("tcp", "127.0.0.1:"+m[1]); err == nil {
		c.Close()
	}
}

// Einen Ergebnisordner, in dem andere schreiben dürfen, übernimmt einrichten
// nicht; einem Symlink beim Hinweis folgt es nie.
func TestEinrichtenUebernimmtKeinenFremdenOrdner(t *testing.T) {
	old := filepath.Join(t.TempDir(), "hostdeck-ergebnis")
	if code, so, se := discoverOnce(t, fixture.Beispiel(t, 1), old); code != 0 {
		t.Fatalf("discover: %s %s", so, se)
	}
	os.Chmod(old, 0o777)
	in, _, out := testInstaller(t, old)
	if err := in.install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(in.ResultDir, "manifest.yaml")); err == nil || !strings.Contains(out.String(), "übernimmt HostDeck nicht") {
		t.Fatalf("fremder Ordner übernommen:\n%s", out.String())
	}

	os.Chmod(old, 0o700)
	target := filepath.Join(t.TempDir(), "wichtig")
	os.WriteFile(target, []byte("bleibt"), 0o644)
	os.Symlink(target, filepath.Join(old, movedNote))
	in2, _, _ := testInstaller(t, old)
	if err := in2.install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != "bleibt" {
		t.Fatal("einrichten schrieb durch einen Symlink")
	}
	if _, err := os.Stat(filepath.Join(in2.ResultDir, "manifest.yaml")); err != nil {
		t.Fatalf("eigener Ordner nicht übernommen: %v", err)
	}
}
