package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Die Signatur muss ssh-keygen genau so annehmen, wie das
// Installationsskript prüft; eine veränderte Datei darf es nicht.
func TestSSHSignMitSshKeygen(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen fehlt")
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	sums := []byte("abc  hostdeck-1.0.0-linux-amd64\n")
	signers := filepath.Join(dir, "allowed_signers")
	os.WriteFile(signers, []byte(AllowedSigners(pub)+"\n"), 0o644)
	sig := filepath.Join(dir, "SHA256SUMS.sig")
	os.WriteFile(sig, SSHSign(priv, sums), 0o644)
	verify := func(data []byte, ns string) (string, error) {
		cmd := exec.Command(keygen, "-Y", "verify", "-f", signers, "-I", SignerName, "-n", ns, "-s", sig)
		cmd.Stdin = bytes.NewReader(data)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := verify(sums, SigNamespace); err != nil {
		t.Fatalf("ssh-keygen lehnt ab: %v %s", err, out)
	}
	if out, err := verify(append(sums, 'x'), SigNamespace); err == nil {
		t.Errorf("veränderte Datei angenommen: %s", out)
	}
	if out, err := verify(sums, "file"); err == nil {
		t.Errorf("anderer Namensraum angenommen: %s", out)
	}
	if !strings.HasPrefix(AuthorizedKey(pub), "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI") {
		t.Errorf("Schlüssel: %s", AuthorizedKey(pub))
	}
}
