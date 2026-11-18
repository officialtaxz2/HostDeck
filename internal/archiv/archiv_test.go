package archiv

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"config":           "konfig",
		"data/ab/abcdef":   strings.Repeat("x", 1000),
		"data/cd/cdef01":   strings.Repeat("y", 512),
		"index/0001":       "",
		"keys/k1":          "schlüssel",
		"locks/l1":         "sperre",
		"snapshots/s1":     strings.Repeat("z", 3),
		"data/ef/ef00ab12": strings.Repeat("q", 70000),
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func readAll(t *testing.T, a *Archive) []byte {
	t.Helper()
	b, err := io.ReadAll(io.NewSectionReader(a, 0, a.Size))
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(b)) != a.Size {
		t.Fatalf("gelesen %d, Größe %d", len(b), a.Size)
	}
	return b
}

func TestArchivLaesstSichEntpacken(t *testing.T) {
	dir := tree(t)
	a, err := Build(dir, "hostdeck-backup", []string{"locks"}, Extra{Name: "LIESMICH.txt", Body: []byte("hallo")})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	got := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(readAll(t, a)))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		got[h.Name] = string(b)
	}
	for name, want := range map[string]string{
		"hostdeck-backup/config":           "konfig",
		"hostdeck-backup/data/ef/ef00ab12": strings.Repeat("q", 70000),
		"hostdeck-backup/index/0001":       "",
		"hostdeck-backup/LIESMICH.txt":     "hallo",
		"hostdeck-backup/locks/":           "",
	} {
		if g, ok := got[name]; !ok || g != want {
			t.Errorf("%s: %q (da: %v)", name, trunc(g), ok)
		}
	}
	if _, ok := got["hostdeck-backup/locks/l1"]; ok {
		t.Error("Sperrdatei von restic im Archiv")
	}
}

func trunc(s string) string {
	if len(s) > 20 {
		return s[:20] + "…"
	}
	return s
}

func TestArchivAnJederStelleGleich(t *testing.T) {
	dir := tree(t)
	a, err := Build(dir, "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	full := readAll(t, a)
	b, err := Build(dir, "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.ETag != b.ETag || !bytes.Equal(full, readAll(t, b)) {
		t.Fatal("zweimal gebaut, verschieden")
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 300; i++ {
		off := rng.Int63n(a.Size)
		n := rng.Intn(5000) + 1
		buf := make([]byte, n)
		k, err := b.ReadAt(buf, off)
		want := full[off:min(off+int64(n), a.Size)]
		if k != len(want) || !bytes.Equal(buf[:k], want) {
			t.Fatalf("ab %d (%d): %d Bytes, falsch", off, n, k)
		}
		if k < n && err != io.EOF {
			t.Fatalf("ab %d: kurz ohne EOF: %v", off, err)
		}
	}
}

func TestArchivMerktAenderungen(t *testing.T) {
	dir := tree(t)
	a, err := Build(dir, "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data/ef/ef00ab12"), []byte("kurz"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(io.NewSectionReader(a, 0, a.Size))
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("Fehler %v, will ErrChanged", err)
	}
	b, err := Build(dir, "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.ETag == b.ETag {
		t.Error("ETag gleich nach Änderung")
	}
}

func TestArchivOhneSymlinks(t *testing.T) {
	dir := tree(t)
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "data", "boese")); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(dir, "x", nil); err == nil {
		t.Fatal("Symlink eingepackt")
	}
}
