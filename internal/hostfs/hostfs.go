// Package hostfs gibt nur lesenden Zugriff auf das Dateisystem des Hosts.
// Alle Pfade sind absolute Host-Pfade; in Tests zeigt Root auf einen
// temporären Ordner, der den Server nachbildet.
package hostfs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type FS struct {
	Root string // "/" auf dem echten Server
}

func New(root string) FS {
	if root == "" {
		root = "/"
	}
	return FS{Root: root}
}

// Real übersetzt einen Host-Pfad in den Pfad, unter dem er für dieses
// Programm erreichbar ist.
func (f FS) Real(hostPath string) string {
	return filepath.Join(f.Root, Clean(hostPath))
}

// Clean macht aus p einen sauberen absoluten Pfad.
func Clean(p string) string {
	return path.Clean("/" + p)
}

func (f FS) Stat(p string) (fs.FileInfo, error)  { return os.Stat(f.Real(p)) }
func (f FS) Lstat(p string) (fs.FileInfo, error) { return os.Lstat(f.Real(p)) }
func (f FS) Readlink(p string) (string, error)   { return os.Readlink(f.Real(p)) }

func (f FS) ReadDir(p string) ([]fs.DirEntry, error) { return os.ReadDir(f.Real(p)) }

func (f FS) Exists(p string) bool {
	_, err := f.Lstat(p)
	return err == nil
}

func (f FS) IsDir(p string) bool {
	fi, err := f.Stat(p)
	return err == nil && fi.IsDir()
}

// ReadFile liest höchstens limit Byte. Die Datei wird nur zum Lesen geöffnet.
func (f FS) ReadFile(p string, limit int64) ([]byte, error) {
	fh, err := os.OpenFile(f.Real(p), os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	return io.ReadAll(io.LimitReader(fh, limit))
}

// ReadHead liest die ersten n Byte einer Datei.
func (f FS) ReadHead(p string, n int) ([]byte, error) {
	fh, err := os.OpenFile(f.Real(p), os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	buf := make([]byte, n)
	m, err := io.ReadFull(fh, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:m], nil
}

// Glob sucht nach Host-Pfaden, die auf pattern passen.
func (f FS) Glob(pattern string) ([]string, error) {
	matches, err := filepath.Glob(f.Real(pattern))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, f.host(m))
	}
	return out, nil
}

func (f FS) host(real string) string {
	if f.Root == "/" {
		return real
	}
	rel := strings.TrimPrefix(real, filepath.Clean(f.Root))
	return Clean(rel)
}

// Under meldet, ob p gleich parent ist oder darunter liegt.
func Under(p, parent string) bool {
	if parent == "/" {
		return true
	}
	return p == parent || strings.HasPrefix(p, parent+"/")
}

// StrictlyUnder meldet, ob p echt unterhalb von parent liegt.
func StrictlyUnder(p, parent string) bool {
	return p != parent && Under(p, parent)
}
