package main

import (
	"os"
	"regexp"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/neueversion"
	"github.com/officialtaxz2/hostdeck/internal/update"
)

// Das Installationsskript prüft Releases mit demselben Schlüssel und lädt
// aus demselben Repository wie das Programm selbst.
func TestInstallskriptPasstZumProgramm(t *testing.T) {
	b, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	repo := regexp.MustCompile(`(?m)^\s*local REPO="([^"]+)"`).FindStringSubmatch(s)
	if repo == nil || repo[1] != neueversion.Repo {
		t.Errorf("REPO im Skript %v, im Programm %s", repo, neueversion.Repo)
	}
	pub, err := update.ParsePublicKey(update.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	signers := regexp.MustCompile(`(?m)^\s*local SIGNERS='([^']+)'`).FindStringSubmatch(s)
	if signers == nil || signers[1] != update.AllowedSigners(pub) {
		t.Errorf("SIGNERS im Skript %v,\nerwartet %s", signers, update.AllowedSigners(pub))
	}
	for _, want := range []string{update.SigNamespace, "-I " + update.SignerName} {
		if !regexp.MustCompile(regexp.QuoteMeta(want)).MatchString(s) {
			t.Errorf("Skript ohne %q", want)
		}
	}
}
