package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

func TestSignierenUndPruefen(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	bin := bytes.Repeat([]byte("ELF-programm"), 1000)
	var file bytes.Buffer
	if err := Write(&file, priv, Header{Version: "1b-abc1234", OS: "linux", Arch: "amd64"}, bin); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	h, err := Verify(bytes.NewReader(file.Bytes()), pub, &out)
	if err != nil || h.Version != "1b-abc1234" || h.Arch != "amd64" || !bytes.Equal(out.Bytes(), bin) {
		t.Fatalf("%+v %v", h, err)
	}
	// Fremder Schlüssel.
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := Verify(bytes.NewReader(file.Bytes()), other, &bytes.Buffer{}); err != ErrSignature {
		t.Fatalf("fremder Schlüssel: %v", err)
	}
	// Ein Byte im Programm geändert.
	bad := bytes.Clone(file.Bytes())
	bad[len(bad)-10] ^= 1
	if _, err := Verify(bytes.NewReader(bad), pub, &bytes.Buffer{}); err != ErrDamaged {
		t.Fatalf("verändertes Programm: %v", err)
	}
	// Kopf verändert (andere Version): Signatur passt nicht.
	head := bytes.Replace(file.Bytes(), []byte("1b-abc1234"), []byte("9z-abc1234"), 1)
	if _, err := Verify(bytes.NewReader(head), pub, &bytes.Buffer{}); err != ErrSignature {
		t.Fatalf("veränderter Kopf: %v", err)
	}
	// Abgeschnitten und mit Anhang.
	if _, err := Verify(bytes.NewReader(file.Bytes()[:file.Len()-1]), pub, &bytes.Buffer{}); err != ErrDamaged {
		t.Fatalf("abgeschnitten: %v", err)
	}
	if _, err := Verify(bytes.NewReader(append(file.Bytes(), 'x')), pub, &bytes.Buffer{}); err != ErrDamaged {
		t.Fatalf("mit Anhang: %v", err)
	}
	if _, err := Verify(bytes.NewReader([]byte("MZ\x90\x00 eine exe")), pub, &bytes.Buffer{}); err != ErrNotUpdate {
		t.Fatalf("keine Update-Datei: %v", err)
	}
}

func TestEingebauterSchluessel(t *testing.T) {
	if _, err := ParsePublicKey(PublicKey); err != nil {
		t.Fatal(err)
	}
}
