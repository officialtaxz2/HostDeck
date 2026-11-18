package restore

// Regressionstest zu einem früheren Fund, seit dem Fix ein
// Regressionstest.

import (
	"bytes"
	"os"
	"testing"
)

// M1: CheckKey prüft nur den Fingerabdruck des ersten Pakets und gibt dann
// die ganze Datei als Schlüsselbund für signed-by zurück. Ein zweiter
// Hauptschlüssel (Paket-Typ 6) dahinter kommt ungeprüft mit; apt nimmt
// Signaturen von jedem Schlüssel im Bund an.
func TestReview2CaddySchluesselNurDerEine(t *testing.T) {
	armored, err := os.ReadFile("testdata/caddy-stable.gpg.key")
	if err != nil {
		t.Fatal(err)
	}
	bin, err := CheckKey(armored, CaddyKeyFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	// Ein zweiter, fremder Hauptschlüssel (V4, Ed25519-Kopf genügt hier).
	foreign := []byte{0x98, 0x33, 0x04, 0x65, 0x00, 0x00, 0x00, 0x16, 0x09, 0x2b, 0x06, 0x01, 0x04, 0x01, 0xda, 0x47, 0x0f, 0x01, 0x01, 0x07, 0x40}
	foreign = append(foreign, bytes.Repeat([]byte{0x42}, 0x33+2-len(foreign))...)
	got, err := CheckKey(append(append([]byte{}, bin...), foreign...), CaddyKeyFingerprint)
	if err == nil && bytes.Contains(got, foreign) {
		t.Errorf("Schlüsselbund mit einem zweiten, ungeprüften Hauptschlüssel angenommen (%d statt %d Bytes)", len(got), len(bin))
	}
}
