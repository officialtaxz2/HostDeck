// Package update prüft neue Versionen von HostDeck, bevor der Dienst sie
// einspielt. Eine Update-Datei (.hdu) besteht aus
//
//	Zeile 1: "sbm-update 1"
//	Zeile 2: Kopf als JSON (Version, System, Prozessor, SHA-256, Größe)
//	Zeile 3: Ed25519-Signatur über Zeile 2, Base64
//	danach:  die Programmdatei
//
// HostDeck nimmt nur Dateien an, deren Signatur zum eingebauten öffentlichen
// Schlüssel passt. Wer das Leitstand-Passwort kennt, kann so trotzdem kein
// eigenes Programm als root auf den Server bringen.
package update

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// Magic ist die erste Zeile.
const Magic = "sbm-update 1"

// MaxSize begrenzt die Programmdatei.
const MaxSize = 90 << 20

// Header beschreibt die Programmdatei.
type Header struct {
	Version string    `json:"version"`
	OS      string    `json:"os"`
	Arch    string    `json:"arch"`
	SHA256  string    `json:"sha256"`
	Size    int64     `json:"groesse"`
	Made    time.Time `json:"erstellt"`
}

// Fehler, die der Leitstand zeigt.
var (
	ErrNotUpdate = errors.New("Das ist keine Update-Datei von HostDeck. Gesucht ist eine Datei, die auf .hdu endet.")
	ErrSignature = errors.New("Die Signatur passt nicht: Die Datei stammt nicht aus einem Release von HostDeck oder wurde verändert. Nichts eingespielt.")
	ErrDamaged   = errors.New("Die Datei ist unvollständig oder beschädigt. Bitte noch einmal herunterladen.")
)

var versionRE = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`)

// Write schreibt eine signierte Update-Datei.
func Write(w io.Writer, priv ed25519.PrivateKey, h Header, bin []byte) error {
	if !versionRE.MatchString(h.Version) {
		return fmt.Errorf("Version %q ungültig", h.Version)
	}
	sum := sha256.Sum256(bin)
	h.SHA256 = hex.EncodeToString(sum[:])
	h.Size = int64(len(bin))
	if h.Made.IsZero() {
		h.Made = time.Now().UTC()
	}
	head, err := json.Marshal(h)
	if err != nil {
		return err
	}
	sig := ed25519.Sign(priv, head)
	var b bytes.Buffer
	b.WriteString(Magic + "\n")
	b.Write(head)
	b.WriteString("\n" + base64.StdEncoding.EncodeToString(sig) + "\n")
	if _, err := w.Write(b.Bytes()); err != nil {
		return err
	}
	_, err = w.Write(bin)
	return err
}

func line(r *bufio.Reader) (string, error) {
	var b strings.Builder
	for b.Len() <= 4096 {
		c, err := r.ReadByte()
		if err != nil {
			return "", ErrNotUpdate
		}
		if c == '\n' {
			return b.String(), nil
		}
		b.WriteByte(c)
	}
	return "", ErrNotUpdate
}

// Verify liest eine Update-Datei aus r, prüft die Signatur mit pub und
// schreibt die Programmdatei nach dst. Erst wenn Verify ohne Fehler endet,
// ist der Inhalt von dst geprüft.
func Verify(r io.Reader, pub ed25519.PublicKey, dst io.Writer) (Header, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	if l, err := line(br); err != nil || l != Magic {
		return Header{}, ErrNotUpdate
	}
	head, err := line(br)
	if err != nil {
		return Header{}, ErrNotUpdate
	}
	sigLine, err := line(br)
	if err != nil {
		return Header{}, ErrNotUpdate
	}
	sig, err := base64.StdEncoding.DecodeString(sigLine)
	if err != nil || len(sig) != ed25519.SignatureSize || len(pub) != ed25519.PublicKeySize {
		return Header{}, ErrSignature
	}
	if !ed25519.Verify(pub, []byte(head), sig) {
		return Header{}, ErrSignature
	}
	var h Header
	if err := json.Unmarshal([]byte(head), &h); err != nil || !versionRE.MatchString(h.Version) || h.Size <= 0 || h.Size > MaxSize || len(h.SHA256) != 64 {
		return Header{}, ErrDamaged
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, sum), io.LimitReader(br, h.Size+1))
	if err != nil {
		return Header{}, err
	}
	if n != h.Size || hex.EncodeToString(sum.Sum(nil)) != h.SHA256 {
		return Header{}, ErrDamaged
	}
	return h, nil
}

// ParsePublicKey liest einen öffentlichen Schlüssel in Base64.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("öffentlicher Schlüssel ungültig")
	}
	return ed25519.PublicKey(b), nil
}

// ParsePrivateKey liest einen privaten Schlüssel (Seed in Base64).
func ParsePrivateKey(s string) (ed25519.PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != ed25519.SeedSize {
		return nil, errors.New("privater Schlüssel ungültig")
	}
	return ed25519.NewKeyFromSeed(b), nil
}
