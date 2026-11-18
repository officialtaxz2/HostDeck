package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/fixture"
)

func discoverOnce(t *testing.T, s *fixture.Server, out string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	opts := s.Options()
	opts.Log = &stderr
	code := runDiscover(context.Background(), s.Deps(), opts, out, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestDiscoverZweiLaeufe(t *testing.T) {
	out := filepath.Join(t.TempDir(), "hostdeck-ergebnis")

	code, stdout, stderr := discoverOnce(t, fixture.Beispiel(t, 1), out)
	if code != 0 {
		t.Fatalf("Exit %d\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "manifest.yaml wurde als Vorschlag neu geschrieben") {
		t.Errorf("Erster Lauf soll das Manifest anlegen:\n%s", stdout)
	}
	for _, f := range []string{"inventory.json", "manifest.yaml", "bericht.txt"} {
		fi, err := os.Stat(filepath.Join(out, f))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s hat Modus %v, erwartet 0600", f, fi.Mode().Perm())
		}
	}
	if fi, _ := os.Stat(out); fi.Mode().Perm() != 0o700 {
		t.Errorf("Ergebnisordner hat Modus %v, erwartet 0700", fi.Mode().Perm())
	}
	first, _ := os.ReadFile(filepath.Join(out, "manifest.yaml"))

	// Zweiter Lauf nach Neustarts aller Container: nichts Neues.
	code, stdout, stderr = discoverOnce(t, fixture.Beispiel(t, 2), out)
	if code != 0 {
		t.Fatalf("Exit %d\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Keine neuen und keine weggefallenen Einträge.") {
		t.Errorf("Zweiter Lauf soll nichts Neues melden:\n%s", stdout)
	}
	second, _ := os.ReadFile(filepath.Join(out, "manifest.yaml"))
	if !bytes.Equal(first, second) {
		t.Error("zweiter Lauf hat manifest.yaml verändert")
	}

	for _, f := range []string{"inventory.json", "manifest.yaml", "bericht.txt"} {
		b, _ := os.ReadFile(filepath.Join(out, f))
		if strings.Contains(string(b), "geheim") {
			t.Errorf("%s enthält ein Geheimnis", f)
		}
	}
	if strings.Contains(stdout+stderr, "geheim") {
		t.Error("Terminal-Ausgabe enthält ein Geheimnis")
	}
	if dump := os.Getenv("HOSTDECK_BERICHT"); dump != "" {
		b, _ := os.ReadFile(filepath.Join(out, "bericht.txt"))
		os.WriteFile(dump, b, 0o644)
		os.WriteFile(dump+".manifest.yaml", first, 0o644)
		os.WriteFile(dump+".stdout.txt", []byte(stdout), 0o644)
		inv, _ := os.ReadFile(filepath.Join(out, "inventory.json"))
		os.WriteFile(dump+".inventory.json", inv, 0o644)
	}
}

func TestDiscoverNeueUndWeggefalleneEintraege(t *testing.T) {
	out := t.TempDir()
	if code, stdout, _ := discoverOnce(t, fixture.Beispiel(t, 1), out); code != 0 {
		t.Fatal(stdout)
	}
	s := fixture.Beispiel(t, 1)
	// Ein Container kommt dazu, ein Volume verschwindet.
	s.Docker.Find("mcguides-guide-1").C.Config.Labels["com.docker.compose.service"] = "guide2"
	var vols = s.Docker.Vols[:0]
	for _, v := range s.Docker.Vols {
		if v.Name != "streamingroom_uploads" {
			vols = append(vols, v)
		}
	}
	s.Docker.Vols = vols
	code, stdout, _ := discoverOnce(t, s, out)
	if code != 0 {
		t.Fatal(stdout)
	}
	for _, want := range []string{"+ compose:mcguides/guide2", "- compose:mcguides/guide", "- volume:streamingroom_uploads"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("Ausgabe enthält %q nicht:\n%s", want, stdout)
		}
	}
}

// Ein Manifest aus einer älteren Version kann für ein lokal gebautes Image
// noch rebuild enthalten; discover lässt das Manifest stehen, sagt es aber.
func TestDiscoverMeldetRebuildBeiGebautemImage(t *testing.T) {
	out := t.TempDir()
	if code, stdout, _ := discoverOnce(t, fixture.Beispiel(t, 1), out); code != 0 {
		t.Fatal(stdout)
	}
	path := filepath.Join(out, "manifest.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := "      app:\n        image: image-save\n"
	if !strings.Contains(string(b), old) {
		t.Fatalf("Vorschlag für pensum/app nicht gefunden:\n%s", b)
	}
	b = []byte(strings.Replace(string(b), old, "      app:\n        image: rebuild\n", 1))
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := discoverOnce(t, fixture.Beispiel(t, 1), out)
	if code != 0 {
		t.Fatal(stdout)
	}
	if !strings.Contains(stdout, "Bei 1 Dienst steht image: rebuild, das Image ist aber auf diesem Server gebaut: pensum/app.") {
		t.Errorf("Hinweis fehlt:\n%s", stdout)
	}
	if after, _ := os.ReadFile(path); string(after) != string(b) {
		t.Error("discover hat das Manifest verändert")
	}
}

func TestDiscoverUngueltigesManifest(t *testing.T) {
	out := t.TempDir()
	bad := "version: 1\nsource: {hostname: vps1}\nprojects:\n  pensum: {decision: vielleicht}\n"
	if err := os.WriteFile(filepath.Join(out, "manifest.yaml"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := discoverOnce(t, fixture.Beispiel(t, 1), out)
	if code != 1 {
		t.Errorf("Exit %d, erwartet 1", code)
	}
	if !strings.Contains(stdout, `decision "vielleicht" ist ungültig`) {
		t.Errorf("Fehler nicht im Bericht:\n%s", stdout)
	}
	b, _ := os.ReadFile(filepath.Join(out, "manifest.yaml"))
	if string(b) != bad {
		t.Error("ungültiges Manifest wurde überschrieben")
	}
	if _, err := os.Stat(filepath.Join(out, "inventory.json")); err != nil {
		t.Error("inventory.json soll trotzdem geschrieben werden")
	}
}

func TestBefehle(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"version"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "hostdeck") {
		t.Errorf("version: %d %q", code, out.String())
	}
	out.Reset()
	if code := run(context.Background(), []string{"backup"}, &out, &errOut); code != 2 {
		t.Errorf("unbekannter Befehl: Exit %d", code)
	}
	if code := run(context.Background(), []string{"discover", "--gibtsnicht"}, &out, &errOut); code != 2 {
		t.Errorf("unbekannte Option: Exit %d", code)
	}
}
