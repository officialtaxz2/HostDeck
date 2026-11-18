//go:build linux

package aufraeumen

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/officialtaxz2/hostdeck/internal/ohnelink"
	"golang.org/x/sys/unix"
)

// placeBack legt src (fertig zurückgespielt, im eigenen Ordner des
// Papierkorbs) an den Ort dst. Die Ordner über dst öffnet es ohne
// Verknüpfungen; danach geht alles über Deskriptoren, ohne etwas zu
// überschreiben. Gibt es dst schon: mit merge als echten Ordner werden nur
// fehlende Einträge hineingelegt (kept zählt, was schon da war), sonst
// os.ErrExist.
func placeBack(src, dst string, merge bool) (kept int, err error) {
	sp, err := unix.Open(filepath.Dir(src), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, err
	}
	defer unix.Close(sp)
	dp, base, bad, err := ohnelink.OpenParent("/", dst, false)
	if err != nil {
		return 0, parentErr(bad, err)
	}
	defer unix.Close(dp)
	return place(sp, filepath.Base(src), dp, base, merge)
}

func parentErr(bad string, err error) error {
	switch {
	case errors.Is(err, unix.ENOENT):
		return fmt.Errorf("%w: den Ordner %s gibt es nicht mehr; lege ihn erst wieder an", ErrBlocked, bad)
	case errors.Is(err, unix.ELOOP), errors.Is(err, unix.ENOTDIR):
		return fmt.Errorf("%w: %s ist inzwischen eine Verknüpfung oder keine Ordner mehr; HostDeck folgt ihr nicht", ErrBlocked, bad)
	}
	return fmt.Errorf("%s: %w", bad, err)
}

func place(sp int, sname string, dp int, dname string, merge bool) (int, error) {
	var st unix.Stat_t
	err := unix.Fstatat(dp, dname, &st, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		err = unix.Renameat2(sp, sname, dp, dname, unix.RENAME_NOREPLACE)
		switch {
		case err == nil:
			return 0, nil
		case errors.Is(err, unix.EEXIST):
			return 0, os.ErrExist
		case errors.Is(err, unix.EXDEV):
			return 0, errors.New("der Papierkorb liegt auf einer anderen Platte als der alte Ort")
		}
		return 0, err
	}
	if err != nil {
		return 0, err
	}
	if !merge {
		return 0, os.ErrExist
	}
	var sst unix.Stat_t
	if err := unix.Fstatat(sp, sname, &sst, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return 0, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR || sst.Mode&unix.S_IFMT != unix.S_IFDIR {
		return 1, nil // steht schon da; bleibt
	}
	sfd, err := unix.Openat(sp, sname, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, err
	}
	defer unix.Close(sfd)
	dfd, err := ohnelink.OpenDir(dp, dname, false)
	if err != nil {
		return 0, err
	}
	defer unix.Close(dfd)
	dup, err := unix.Dup(sfd)
	if err != nil {
		return 0, err
	}
	f := os.NewFile(uintptr(dup), "")
	names, err := f.Readdirnames(-1)
	f.Close()
	if err != nil {
		return 0, err
	}
	kept := 0
	for _, n := range names {
		k, err := place(sfd, n, dfd, n, true)
		if err != nil {
			return kept, err
		}
		kept += k
	}
	return kept, nil
}

// createNew legt die Datei dst neu an, ohne Verknüpfungen in den Ordnern
// darüber zu folgen und ohne etwas zu überschreiben. finish(false) löscht
// sie wieder (bei einem Fehler beim Schreiben); finish muss immer kommen.
func createNew(dst string, mode os.FileMode) (f *os.File, finish func(keep bool), err error) {
	dp, base, bad, err := ohnelink.OpenParent("/", dst, false)
	if err != nil {
		return nil, nil, parentErr(bad, err)
	}
	fd, err := unix.Openat(dp, base, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, uint32(mode.Perm()))
	if err != nil {
		unix.Close(dp)
		if errors.Is(err, unix.EEXIST) {
			return nil, nil, os.ErrExist
		}
		return nil, nil, err
	}
	finish = func(keep bool) {
		if !keep {
			_ = unix.Unlinkat(dp, base, 0)
		}
		unix.Close(dp)
	}
	f = os.NewFile(uintptr(fd), dst)
	return f, finish, nil
}
