// Package restic bindet das Backup-Programm restic an: die feste Version
// herunterladen und prüfen, ein Repository anlegen, sichern und prüfen.
package restic

import (
	"bytes"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Version ist die restic-Version, die HostDeck verwendet. Alter und neuer
// Server nutzen dieselbe.
const Version = "0.19.1"

// Checksums sind die SHA-256-Summen der .bz2-Dateien aus SHA256SUMS des
// Releases v0.19.1. Die Signatur von SHA256SUMS wurde beim Eintragen mit dem
// Release-Schlüssel von restic geprüft (Fingerabdruck
// CF8F 18F2 8445 7597 3F79 D4E1 91A6 868B D3F7 A907).
var Checksums = map[string]string{
	"linux/amd64": "f415415624dcc452f2a02b8c33641791a8c6d6d3b65bbb3543fcf9a25151585c",
	"linux/arm64": "a5f64aaab53d51e311fa3829124c5b703f2d14cf187d8640b6be3b2b49376465",
}

// MinVersion ist die älteste restic-Version, mit der HostDeck arbeitet: erst ab
// 0.17 gibt es --stdin-from-command.
var MinVersion = [3]int{0, 17, 0}

// Installer lädt restic herunter, wenn es noch fehlt.
type Installer struct {
	Dir     string       // Ordner für das Programm, etwa /root/.hostdeck/bin
	BaseURL string       // Standard: GitHub-Releases von restic
	HTTP    *http.Client // Standard: 5 Minuten Zeitlimit
	GOOS    string       // Standard: runtime.GOOS
	GOARCH  string       // Standard: runtime.GOARCH
	Sums    map[string]string
}

const defaultBaseURL = "https://github.com/restic/restic/releases/download"

// Path ist der Ort, an dem Install restic ablegt.
func (in Installer) Path() string {
	return filepath.Join(in.Dir, "restic-"+Version)
}

// Install gibt den Pfad zu restic zurück und lädt es vorher herunter, falls
// es noch nicht da ist. Die heruntergeladene Datei muss genau die
// eingetragene Prüfsumme haben.
func (in Installer) Install(ctx context.Context) (string, error) {
	target := in.Path()
	if v, err := BinaryVersion(ctx, target); err == nil && v == Version {
		return target, nil
	}
	goos, goarch := in.GOOS, in.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	sums := in.Sums
	if sums == nil {
		sums = Checksums
	}
	want, ok := sums[goos+"/"+goarch]
	if !ok {
		return "", fmt.Errorf("für %s/%s hat HostDeck keine geprüfte restic-Version; restic %s selbst installieren und mit --restic angeben", goos, goarch, Version)
	}
	base := in.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	name := fmt.Sprintf("restic_%s_%s_%s.bz2", Version, goos, goarch)
	url := fmt.Sprintf("%s/v%s/%s", strings.TrimRight(base, "/"), Version, name)
	client := in.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("restic herunterladen: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("restic herunterladen: %s antwortet %s", url, resp.Status)
	}
	compressed, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20))
	if err != nil {
		return "", fmt.Errorf("restic herunterladen: %w", err)
	}
	sum := sha256.Sum256(compressed)
	if got := hex.EncodeToString(sum[:]); got != want {
		return "", fmt.Errorf("restic-Download hat die falsche Prüfsumme (%s statt %s); nichts installiert", got, want)
	}
	if err := os.MkdirAll(in.Dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(in.Dir, ".restic-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, bzip2.NewReader(bytes.NewReader(compressed))); err != nil {
		tmp.Close()
		return "", fmt.Errorf("restic entpacken: %w", err)
	}
	if err := tmp.Chmod(0o700); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return "", err
	}
	if v, err := BinaryVersion(ctx, target); err != nil || v != Version {
		return "", fmt.Errorf("restic lässt sich nach dem Herunterladen nicht starten (%v)", err)
	}
	return target, nil
}

var versionLine = regexp.MustCompile(`^restic (\d+)\.(\d+)\.(\d+)`)

// BinaryVersion fragt ein restic-Programm nach seiner Version.
func BinaryVersion(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	if err != nil {
		return "", err
	}
	m := versionLine.FindStringSubmatch(string(out))
	if m == nil {
		return "", fmt.Errorf("unbekannte Ausgabe von %s version", bin)
	}
	return m[1] + "." + m[2] + "." + m[3], nil
}

// CheckVersion meldet einen Fehler, wenn v älter als MinVersion ist.
func CheckVersion(v string) error {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return fmt.Errorf("restic-Version %q ist unlesbar", v)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return fmt.Errorf("restic-Version %q ist unlesbar", v)
		}
		if n > MinVersion[i] {
			return nil
		}
		if n < MinVersion[i] {
			return fmt.Errorf("restic %s ist zu alt; HostDeck braucht mindestens %d.%d.%d", v, MinVersion[0], MinVersion[1], MinVersion[2])
		}
	}
	return nil
}
