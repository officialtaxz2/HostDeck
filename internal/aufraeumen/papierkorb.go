package aufraeumen

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/officialtaxz2/hostdeck/internal/restic"
)

// ResticTrash ist der Papierkorb als restic-Repository in Dir. Den
// Schlüssel erzeugt er beim ersten Mal selbst; er liegt nur in Dir (nur für
// root lesbar) und verlässt den Server nie, auch nicht in den Leitstand.
type ResticTrash struct {
	Dir  string
	Bin  func(ctx context.Context) (string, error) // restic
	Host string
	Log  io.Writer

	mu   sync.Mutex
	repo *restic.Repo
}

func (t *ResticTrash) open(ctx context.Context) (*restic.Repo, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.repo != nil {
		return t.repo, nil
	}
	if t.Bin == nil {
		return nil, errors.New("restic fehlt")
	}
	bin, err := t.Bin(ctx)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(t.Dir, 0o700); err != nil {
		return nil, err
	}
	_ = os.Chmod(t.Dir, 0o700)
	key := filepath.Join(t.Dir, "schluessel")
	if _, err := os.Stat(key); errors.Is(err, os.ErrNotExist) {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(key, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		_, err = f.WriteString(hex.EncodeToString(b) + "\n")
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(key)
			return nil, err
		}
	}
	r := &restic.Repo{Bin: bin, URL: filepath.Join(t.Dir, "repo"), PasswordFile: key, CacheDir: filepath.Join(t.Dir, "cache"), Host: t.Host, Log: t.Log}
	ok, err := r.Exists(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		if err := r.Init(ctx); err != nil {
			return nil, err
		}
	}
	t.repo = r
	return r, nil
}

// Save sichert paths in den Papierkorb.
func (t *ResticTrash) Save(ctx context.Context, paths, tags []string) (string, []string, error) {
	r, err := t.open(ctx)
	if err != nil {
		return "", nil, err
	}
	res, err := r.Backup(ctx, restic.BackupArgs{Paths: paths, Tags: tags})
	if err != nil {
		return "", nil, err
	}
	return res.SnapshotID, res.Unreadable, nil
}

// Check liest den ganzen Papierkorb und prüft ihn.
func (t *ResticTrash) Check(ctx context.Context) error {
	r, err := t.open(ctx)
	if err != nil {
		return err
	}
	return r.Check(ctx, "100%")
}

// Restore spielt sub aus dem Snapshot id nach target zurück.
func (t *ResticTrash) Restore(ctx context.Context, id, sub, target string) error {
	r, err := t.open(ctx)
	if err != nil {
		return err
	}
	return r.Restore(ctx, id, sub, target)
}

// Forget vergisst Snapshots; den Platz gibt erst Prune frei.
func (t *ResticTrash) Forget(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	r, err := t.open(ctx)
	if err != nil {
		return err
	}
	return r.Forget(ctx, ids)
}

// Prune gibt den Platz vergessener Snapshots frei.
func (t *ResticTrash) Prune(ctx context.Context) error {
	r, err := t.open(ctx)
	if err != nil {
		return err
	}
	return r.Prune(ctx)
}

// List liefert alle Snapshots des Papierkorbs.
func (t *ResticTrash) List(ctx context.Context) ([]Snap, error) {
	if _, err := os.Stat(filepath.Join(t.Dir, "repo")); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	r, err := t.open(ctx)
	if err != nil {
		return nil, err
	}
	ss, err := r.Snapshots(ctx, trashTag)
	if err != nil {
		return nil, err
	}
	out := make([]Snap, len(ss))
	for i, s := range ss {
		out[i] = Snap{ID: s.ID, Time: s.Time}
	}
	return out, nil
}
