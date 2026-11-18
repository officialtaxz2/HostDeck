package restic_test

import (
	"bytes"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/pc"
	"github.com/officialtaxz2/hostdeck/internal/restic"
)

// resticBin sucht ein echtes restic für die Tests: HOSTDECK_RESTIC oder restic im
// PATH. Ohne restic werden die Tests übersprungen.
func resticBin(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("HOSTDECK_RESTIC"); p != "" {
		return p
	}
	p, err := exec.LookPath("restic")
	if err != nil {
		t.Skip("restic nicht gefunden (HOSTDECK_RESTIC setzen)")
	}
	return p
}

// newRepo startet einen Empfänger wie auf dem PC und gibt ein Repo zurück,
// das ihn über HTTP anspricht.
func newRepo(t *testing.T) (*restic.Repo, string) {
	t.Helper()
	bin := resticBin(t)
	if v, err := restic.BinaryVersion(context.Background(), bin); err != nil || restic.CheckVersion(v) != nil {
		t.Skipf("restic %s ist zu alt oder nicht startbar (%v)", v, err)
	}
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "pc", "vps1")
	rc, err := pc.NewReceiver(repoDir, filepath.Join(dir, "pc", "berichte"), "hostdeck", "schluessel", "hallo-123")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(rc)
	t.Cleanup(srv.Close)
	pw := filepath.Join(dir, "passwort")
	if err := os.WriteFile(pw, []byte("test-passwort\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &restic.Repo{
		Bin:          bin,
		URL:          "rest:" + srv.URL + "/",
		PasswordFile: pw,
		RestUser:     "hostdeck",
		RestPass:     "schluessel",
		CacheDir:     filepath.Join(dir, "cache"),
		Host:         "vps1",
	}, repoDir
}

func TestSichernUeberDenEmpfaenger(t *testing.T) {
	r, repoDir := newRepo(t)
	ctx := context.Background()
	var log strings.Builder
	r.Log = &log
	if ok, err := r.Exists(ctx); err != nil || ok {
		t.Fatalf("Exists vor init: %v %v", ok, err)
	}
	if log.Len() > 0 {
		t.Errorf("erwartete Antwort „gibt es noch nicht“ landet im Protokoll: %q", log.String())
	}
	r.Log = nil
	if err := r.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := r.Exists(ctx); err != nil || !ok {
		t.Fatalf("Exists nach init: %v %v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "config")); err != nil {
		t.Fatalf("Repository liegt nicht im Ordner auf dem PC: %v", err)
	}

	src := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(src, "daten", "cache[1]"), 0o755))
	must(os.WriteFile(filepath.Join(src, "daten", "a.txt"), []byte("hallo"), 0o640))
	must(os.WriteFile(filepath.Join(src, "daten", "cache[1]", "b.txt"), []byte("weg"), 0o644))
	var progress int
	res, err := r.Backup(ctx, restic.BackupArgs{
		Paths:    []string{filepath.Join(src, "daten")},
		Excludes: []string{filepath.Join(src, "daten", "cache[1]")},
		Tags:     []string{"hostdeck", "sbm-run=20261101-120000"},
		Progress: func(restic.Status) { progress++ },
	})
	must(err)
	if res.SnapshotID == "" || res.TotalFiles != 1 {
		t.Errorf("Snapshot %q mit %d Dateien; erwartet 1 Datei ohne den ausgeschlossenen Ordner", res.SnapshotID, res.TotalFiles)
	}

	dump, err := r.Backup(ctx, restic.BackupArgs{
		Command:  []string{"sh", "-c", "printf 'CREATE TABLE x;\\n-- PostgreSQL database cluster dump complete\\n'"},
		Filename: "dumps/pensum/volume-pensum_pensum_db.sql",
		Tags:     []string{"hostdeck"},
	})
	must(err)
	if dump.SnapshotID == "" {
		t.Error("Dump ohne Snapshot")
	}

	// Scheitert der Dump-Befehl, entsteht kein Snapshot.
	before, err := r.Snapshots(ctx, "hostdeck")
	must(err)
	if _, err := r.Backup(ctx, restic.BackupArgs{
		Command:  []string{"sh", "-c", "echo halb; exit 3"},
		Filename: "dumps/kaputt.sql",
		Tags:     []string{"hostdeck"},
	}); err == nil {
		t.Error("gescheiterter Dump-Befehl meldet keinen Fehler")
	}
	after, err := r.Snapshots(ctx, "hostdeck")
	must(err)
	if len(after) != len(before) || len(after) != 2 {
		t.Errorf("Snapshots: vorher %d, nachher %d; erwartet 2 und 2", len(before), len(after))
	}

	must(r.Check(ctx, "1/1"))

	// Nur anhängen: Der Server darf nichts löschen.
	cmd := exec.Command(r.Bin, "forget", "--keep-last", "1", "--group-by", "", "--prune")
	cmd.Env = append(os.Environ(),
		"RESTIC_REPOSITORY="+r.URL, "RESTIC_PASSWORD_FILE="+r.PasswordFile,
		"RESTIC_REST_USERNAME=hostdeck", "RESTIC_REST_PASSWORD=schluessel", "RESTIC_CACHE_DIR="+r.CacheDir)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Errorf("forget --prune über den Empfänger hat geklappt; er muss nur anhängen erlauben:\n%s", out)
	}
	after, err = r.Snapshots(ctx, "hostdeck")
	must(err)
	if len(after) != 2 {
		t.Errorf("nach forget noch %d Snapshots; erwartet 2", len(after))
	}
}

func TestEmpfaengerVerlangtSchluessel(t *testing.T) {
	dir := t.TempDir()
	rc, err := pc.NewReceiver(filepath.Join(dir, "repo"), filepath.Join(dir, "berichte"), "hostdeck", "schluessel", "hallo-123")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(rc)
	defer srv.Close()
	for _, tc := range []struct {
		user, pass string
		want       int
	}{{"", "", 401}, {"hostdeck", "falsch", 401}, {"hostdeck", "schluessel", 200}} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/.hostdeck/hallo", nil)
		if tc.user != "" {
			req.SetBasicAuth(tc.user, tc.pass)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s/%s: %d, erwartet %d", tc.user, tc.pass, resp.StatusCode, tc.want)
		}
	}
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/.hostdeck/bericht?lauf=20261101-120000", strings.NewReader("Bericht"))
	req.SetBasicAuth("hostdeck", "schluessel")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if b, err := os.ReadFile(filepath.Join(dir, "berichte", "backup-bericht-20261101-120000.txt")); err != nil || string(b) != "Bericht" {
		t.Errorf("Bericht nicht gespeichert: %q %v", b, err)
	}
	req, _ = http.NewRequest(http.MethodPut, srv.URL+"/.hostdeck/bericht?lauf=../../boese", strings.NewReader("x"))
	req.SetBasicAuth("hostdeck", "schluessel")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Bericht mit fremdem Namen: %d, erwartet 400", resp.StatusCode)
	}
}

func TestInstallPrueftPruefsumme(t *testing.T) {
	bin := resticBin(t)
	raw, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	// bzip2 kann Go nur lesen; die Testdatei kommt deshalb vom System.
	bz, err := exec.Command("bzip2", "-c", bin).Output()
	if err != nil {
		t.Skip("bzip2 fehlt")
	}
	if _, err := bzip2.NewReader(bytes.NewReader(bz)).Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(bz)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v"+restic.Version+"/restic_"+restic.Version+"_linux_amd64.bz2") {
			http.NotFound(w, r)
			return
		}
		w.Write(bz)
	}))
	defer srv.Close()
	ctx := context.Background()
	v, err := restic.BinaryVersion(ctx, bin)
	if err != nil || v != restic.Version {
		t.Skipf("Test braucht restic %s, gefunden %s", restic.Version, v)
	}

	bad := restic.Installer{Dir: t.TempDir(), BaseURL: srv.URL, GOOS: "linux", GOARCH: "amd64",
		Sums: map[string]string{"linux/amd64": strings.Repeat("0", 64)}}
	if _, err := bad.Install(ctx); err == nil || !strings.Contains(err.Error(), "Prüfsumme") {
		t.Errorf("falsche Prüfsumme nicht erkannt: %v", err)
	}
	if _, err := os.Stat(bad.Path()); err == nil {
		t.Error("restic trotz falscher Prüfsumme installiert")
	}

	good := restic.Installer{Dir: t.TempDir(), BaseURL: srv.URL, GOOS: "linux", GOARCH: "amd64",
		Sums: map[string]string{"linux/amd64": hex.EncodeToString(sum[:])}}
	p, err := good.Install(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, raw) {
		t.Error("installiertes restic weicht ab")
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o700 {
		t.Errorf("Rechte %v, erwartet 0700", fi.Mode().Perm())
	}
}

func TestVersionspruefung(t *testing.T) {
	for v, ok := range map[string]bool{"0.16.4": false, "0.17.0": true, "0.19.1": true, "1.0.0": true, "x": false} {
		if (restic.CheckVersion(v) == nil) != ok {
			t.Errorf("CheckVersion(%q) = %v", v, restic.CheckVersion(v))
		}
	}
}
