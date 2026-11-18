package aktualisieren

// Regressionstest zu einem früheren Fund. Erwartet: rot, solange HostDeck Git
// mit safe.directory=* in fremden Ordnern aufruft. Die Konfiguration des
// Ordners (.git/config) darf dann Programme starten, als root im Dienst.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestReviewGitOrdnerKonfigStartetKeineProgramme(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git fehlt")
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "projekt")
	marker := filepath.Join(dir, "ausgefuehrt")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.email=a@b", "-c", "user.name=a"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "f")
	run("commit", "-qm", "x")
	// Das kann jeder, der in den Projektordner schreiben darf, etwa ein
	// Container mit „.:/app“.
	run("config", "core.fsmonitor", "touch "+marker+"; echo 0 #")

	u := &Updater{CLI: Exec{}}
	if _, err := u.gitState(context.Background(), repo, false); err != nil {
		t.Logf("gitState: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("core.fsmonitor aus .git/config des Projekts lief beim Nachsehen (git status); im Dienst wäre das root")
	}
}
