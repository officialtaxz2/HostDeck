// Package archiv packt einen Ordner als tar-Datei zum Herunterladen, ohne
// sie auf der Platte anzulegen. Die Datei hat eine feste Größe und lässt
// sich an jeder Stelle lesen (ReadAt). So kann ein Browser einen
// abgebrochenen Download fortsetzen, und für 20 GB Backup braucht der
// Server keine 20 GB zusätzlich.
package archiv

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const block = 512

// Extra ist eine Datei, die nicht im Ordner liegt, etwa eine Anleitung.
type Extra struct {
	Name string // Pfad im Archiv, unterhalb von Prefix
	Body []byte
}

// Archive ist ein tar-Archiv über einem Ordner. Es liest die Dateien erst,
// wenn jemand diese Stelle anfordert.
type Archive struct {
	Size int64
	// ETag ändert sich, sobald sich eine Datei im Ordner ändert. Ein
	// Browser setzt einen Download nur fort, wenn er gleich geblieben ist.
	ETag string

	parts []part
	open  *os.File
	opath string
}

type part struct {
	off  int64  // Beginn des Kopfs im Archiv
	hdr  []byte // tar-Kopf
	size int64  // Länge des Inhalts
	path string // Datei auf der Platte; leer: body
	body []byte
}

// end ist das Ende des Teils samt Auffüllung auf 512 Bytes.
func (p *part) end() int64 { return p.off + int64(len(p.hdr)) + pad(p.size) }

func pad(n int64) int64 { return (n + block - 1) / block * block }

// Build legt das Archiv für dir an. Im Archiv liegt alles unter prefix.
// skip nennt Ordner (relativ zu dir), deren Inhalt fehlen darf, etwa die
// Sperrdateien von restic. Symlinks und Sonderdateien lehnt Build ab.
func Build(dir, prefix string, skip []string, extras ...Extra) (*Archive, error) {
	a := &Archive{}
	sum := sha256.New()
	var off int64
	add := func(h *tar.Header, path string, body []byte) error {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		if err := tw.WriteHeader(h); err != nil {
			return fmt.Errorf("%s: %w", h.Name, err)
		}
		p := part{off: off, hdr: bytes.Clone(buf.Bytes()), size: h.Size, path: path, body: body}
		a.parts = append(a.parts, p)
		off = p.end()
		fmt.Fprintf(sum, "%s\x00%d\x00%d\x00%o\n", h.Name, h.Size, h.ModTime.Unix(), h.Mode)
		return nil
	}
	skipped := func(rel string) bool {
		for _, s := range skip {
			if strings.HasPrefix(rel, s+"/") {
				return true
			}
		}
		return false
	}
	root := strings.TrimSuffix(prefix, "/")
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if skipped(rel) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		name := root
		if rel != "." {
			name = path.Join(root, rel)
		}
		h := &tar.Header{Name: name, ModTime: fi.ModTime().Truncate(time.Second), Format: tar.FormatPAX}
		switch {
		case fi.IsDir():
			h.Typeflag, h.Name, h.Mode = tar.TypeDir, name+"/", 0o700
			return add(h, "", nil)
		case fi.Mode().IsRegular():
			h.Typeflag, h.Mode, h.Size = tar.TypeReg, 0o600, fi.Size()
			return add(h, p, nil)
		default:
			return fmt.Errorf("%s ist keine normale Datei; so packt HostDeck das nicht ein", p)
		}
	})
	if err != nil {
		return nil, err
	}
	for _, e := range extras {
		h := &tar.Header{Name: path.Join(root, e.Name), Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(e.Body)), ModTime: time.Unix(0, 0), Format: tar.FormatPAX}
		if err := add(h, "", e.Body); err != nil {
			return nil, err
		}
		sum.Write(e.Body)
	}
	a.Size = off + 2*block // zwei leere Blöcke beenden ein tar-Archiv
	a.ETag = `"` + hex.EncodeToString(sum.Sum(nil))[:32] + `"`
	return a, nil
}

// Close schließt die zuletzt gelesene Datei.
func (a *Archive) Close() error {
	if a.open != nil {
		err := a.open.Close()
		a.open, a.opath = nil, ""
		return err
	}
	return nil
}

// ErrChanged heißt: Eine Datei im Ordner hat sich geändert, seit Build sie
// gesehen hat. Das Archiv taugt dann nicht mehr.
var ErrChanged = errors.New("eine Datei im Backup hat sich während des Herunterladens geändert")

// ReadAt liest das Archiv ab off. Nicht für mehrere Leser gleichzeitig.
func (a *Archive) ReadAt(b []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("archiv: negative Stelle")
	}
	n := 0
	for n < len(b) {
		pos := off + int64(n)
		if pos >= a.Size {
			return n, io.EOF
		}
		k, err := a.fill(b[n:], pos)
		n += k
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// fill liest ab pos, höchstens bis zum Ende des Teils, in dem pos liegt.
func (a *Archive) fill(b []byte, pos int64) (int, error) {
	i := sort.Search(len(a.parts), func(i int) bool { return a.parts[i].end() > pos })
	if i == len(a.parts) {
		// Die beiden Schlussblöcke.
		return zero(b, a.Size-pos), nil
	}
	p := &a.parts[i]
	in := pos - p.off
	if in < int64(len(p.hdr)) {
		return copy(b, p.hdr[in:]), nil
	}
	in -= int64(len(p.hdr))
	if in >= p.size {
		return zero(b, pad(p.size)-in), nil
	}
	want := b
	if rest := p.size - in; int64(len(want)) > rest {
		want = want[:rest]
	}
	if p.path == "" {
		return copy(want, p.body[in:]), nil
	}
	f, err := a.file(p.path)
	if err != nil {
		return 0, err
	}
	k, err := f.ReadAt(want, in)
	if err == io.EOF && k < len(want) {
		return k, ErrChanged
	}
	if err != nil && err != io.EOF {
		return k, err
	}
	return k, nil
}

func (a *Archive) file(p string) (*os.File, error) {
	if a.opath == p {
		return a.open, nil
	}
	a.Close()
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrChanged
		}
		return nil, err
	}
	a.open, a.opath = f, p
	return f, nil
}

func zero(b []byte, n int64) int {
	if int64(len(b)) > n {
		b = b[:n]
	}
	clear(b)
	return len(b)
}
