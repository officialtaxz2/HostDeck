//go:build linux

package platz

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"

	"github.com/officialtaxz2/hostdeck/internal/ohnelink"
	"golang.org/x/sys/unix"
)

// Remove löscht den Eintrag s unter root (dem echten Ordner für /). Es
// folgt keiner Verknüpfung, steigt in keine andere Platte und keinen
// eingehängten Ordner und löscht nur, wenn Gerät und Inode noch zur
// Vorschau passen. Was nicht geht, bleibt und steht in Result. Ohne
// openat2 (Kernel vor 5.6) löscht es nichts.
func Remove(ctx context.Context, root string, s Sel) (Result, error) {
	if root == "" {
		root = "/"
	}
	host := path.Clean("/" + s.Path)
	if host == "/" {
		return Result{}, errors.New("/ löscht HostDeck nicht")
	}
	var rst unix.Stat_t
	if err := unix.Stat(root, &rst); err != nil {
		return Result{}, fmt.Errorf("%s: %w", root, err)
	}
	r := &remover{ctx: ctx, dev: uint64(rst.Dev)}

	// Den Ordner darüber Stück für Stück öffnen, ohne Verknüpfungen und
	// ohne Einhängepunkte.
	dirfd, base, bad, err := ohnelink.OpenParent(root, host, true)
	switch {
	case errors.Is(err, unix.ENOENT):
		return Result{}, &PathError{host, ErrGone}
	case err != nil:
		return Result{}, &PathError{bad, explain(err)}
	}
	defer unix.Close(dirfd)
	if err := r.sameDev(dirfd, false); err != nil {
		return Result{}, &PathError{path.Dir(host), explain(err)}
	}

	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, base, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return Result{}, &PathError{host, ErrGone}
		}
		return Result{}, &PathError{host, explain(err)}
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		if uint64(st.Dev) != s.Ident.Dev || uint64(st.Ino) != s.Ident.Ino {
			return Result{}, &PathError{host, ErrChanged}
		}
		if uint64(st.Dev) != r.dev {
			return Result{}, &PathError{host, ErrOtherDisk}
		}
		if err := unix.Unlinkat(dirfd, base, 0); err != nil {
			return Result{}, &PathError{host, explain(err)}
		}
		if st.Mode&unix.S_IFMT != unix.S_IFLNK {
			r.res.Files = 1
		}
		return r.res, nil
	}
	// Ordner: einmal öffnen und Gerät und Inode am offenen Ordner prüfen;
	// geleert wird genau dieser.
	fd, err := ohnelink.OpenDir(dirfd, base, true)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return Result{}, &PathError{host, ErrGone}
		}
		return Result{}, &PathError{host, explain(err)}
	}
	var ost unix.Stat_t
	if err := unix.Fstat(fd, &ost); err != nil {
		unix.Close(fd)
		return Result{}, &PathError{host, err}
	}
	if uint64(ost.Dev) != s.Ident.Dev || uint64(ost.Ino) != s.Ident.Ino {
		unix.Close(fd)
		return Result{}, &PathError{host, ErrChanged}
	}
	if uint64(ost.Dev) != r.dev {
		unix.Close(fd)
		return Result{}, &PathError{host, ErrOtherDisk}
	}
	r.empty(fd, host)
	unix.Close(fd)
	if r.res.Left == 0 && ctx.Err() == nil {
		if err := unix.Unlinkat(dirfd, base, unix.AT_REMOVEDIR); err != nil {
			r.fail(host, err)
		}
	}
	if ctx.Err() != nil {
		return r.res, ctx.Err()
	}
	return r.res, nil
}

type remover struct {
	ctx context.Context
	dev uint64
	res Result
}

// openDir öffnet den Ordner name in dirfd, ohne einer Verknüpfung zu
// folgen und ohne einen Einhängepunkt zu überqueren.
func (r *remover) openDir(dirfd int, name string) (int, error) {
	fd, err := ohnelink.OpenDir(dirfd, name, true)
	if err != nil {
		return -1, err
	}
	return fd, r.sameDev(fd, true)
}

// sameDev prüft, dass fd auf der Platte von / liegt; closeOnErr schließt
// ihn sonst.
func (r *remover) sameDev(fd int, closeOnErr bool) error {
	var st unix.Stat_t
	err := unix.Fstat(fd, &st)
	if err == nil && uint64(st.Dev) != r.dev {
		err = unix.EXDEV
	}
	if err != nil && closeOnErr {
		unix.Close(fd)
	}
	return err
}

func (r *remover) fail(host string, err error) {
	r.res.Left++
	if len(r.res.Errors) < 20 {
		r.res.Errors = append(r.res.Errors, host+": "+explain(err).Error())
	}
}

// tree leert den Ordner name in dirfd (host ist sein Pfad).
func (r *remover) tree(dirfd int, name, host string) {
	fd, err := r.openDir(dirfd, name)
	if err != nil {
		r.fail(host, err)
		return
	}
	defer unix.Close(fd)
	r.empty(fd, host)
}

// empty leert den offenen Ordner fd.
func (r *remover) empty(fd int, host string) {
	names, err := readNames(fd)
	if err != nil {
		r.fail(host, err)
		return
	}
	for _, n := range names {
		if r.ctx.Err() != nil {
			return
		}
		ch := path.Join(host, n)
		var st unix.Stat_t
		if err := unix.Fstatat(fd, n, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			if !errors.Is(err, unix.ENOENT) {
				r.fail(ch, err)
			}
			continue
		}
		if uint64(st.Dev) != r.dev {
			r.fail(ch, unix.EXDEV)
			continue
		}
		if st.Mode&unix.S_IFMT == unix.S_IFDIR {
			before := r.res.Left
			r.tree(fd, n, ch)
			if r.res.Left != before || r.ctx.Err() != nil {
				continue // der Ordner ist nicht leer geworden
			}
			if err := unix.Unlinkat(fd, n, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
				r.fail(ch, err)
			}
			continue
		}
		if err := unix.Unlinkat(fd, n, 0); err != nil {
			if !errors.Is(err, unix.ENOENT) {
				r.fail(ch, err)
			}
			continue
		}
		if st.Mode&unix.S_IFMT != unix.S_IFLNK {
			r.res.Files++
		}
	}
}

func readNames(fd int) ([]string, error) {
	dup, err := unix.Dup(fd)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(dup), "")
	defer f.Close()
	return f.Readdirnames(-1)
}

// explain macht aus Fehlern des Kernels einen verständlichen Satz.
func explain(err error) error {
	switch {
	case errors.Is(err, unix.EXDEV):
		return ErrOtherDisk
	case errors.Is(err, unix.ELOOP):
		return errors.New("ist eine Verknüpfung; HostDeck folgt ihr nicht")
	case errors.Is(err, unix.EBUSY):
		return errors.New("ist eingehängt oder in Benutzung")
	case errors.Is(err, unix.EACCES), errors.Is(err, unix.EPERM):
		return errors.New("keine Berechtigung (vielleicht schreibgeschützt)")
	case errors.Is(err, unix.ENOTEMPTY):
		return errors.New("ist nicht leer geworden")
	case errors.Is(err, unix.ENOTDIR):
		return errors.New("liegt hinter einer Verknüpfung oder einer Datei; HostDeck folgt ihr nicht")
	}
	return err
}
