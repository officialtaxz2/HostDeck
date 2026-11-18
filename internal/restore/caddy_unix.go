//go:build !windows

package restore

import (
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

func lookupCaddy() (uid, gid int, err error) {
	u, err := user.Lookup("caddy")
	if err != nil {
		return 0, 0, err
	}
	uid, err = strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, err
	}
	gid, err = strconv.Atoi(u.Gid)
	return uid, gid, err
}

func (s *SystemCaddy) Owner(paths []string, uid, gid int) error {
	nu, ng, err := lookupCaddy()
	if err != nil {
		return err
	}
	return remapOwner(s.Root, paths, uid, gid, nu, ng)
}

// remapOwner gibt allem unter paths und ihren Elternordnern, was der
// Nummer from gehört, die Nummer to. restic legt die Nummern vom alten
// Server an; der Benutzer caddy kann hier eine andere haben.
func remapOwner(root string, paths []string, fromUID, fromGID, toUID, toGID int) error {
	if fromUID == toUID && fromGID == toGID {
		return nil
	}
	fix := func(p string) error {
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return nil
		}
		uid, gid := int(st.Uid), int(st.Gid)
		nu, ng := -1, -1
		if fromUID > 0 && uid == fromUID {
			nu = toUID
		}
		if fromGID > 0 && gid == fromGID {
			ng = toGID
		}
		if nu < 0 && ng < 0 {
			return nil
		}
		return os.Lchown(p, nu, ng)
	}
	done := map[string]bool{}
	for _, p := range paths {
		full := filepath.Join(root, p)
		for d := filepath.Dir(full); d != root && d != "/" && d != "." && !done[d]; d = filepath.Dir(d) {
			done[d] = true
			if err := fix(d); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		err := filepath.WalkDir(full, func(q string, _ fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			return fix(q)
		})
		if err != nil {
			return err
		}
	}
	return nil
}
