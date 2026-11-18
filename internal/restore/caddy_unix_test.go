//go:build !windows

package restore

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRemapOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("braucht root")
	}
	root := t.TempDir()
	data := filepath.Join(root, "var/lib/caddy/.local/share/caddy")
	_ = os.MkdirAll(filepath.Join(data, "certificates"), 0o700)
	_ = os.WriteFile(filepath.Join(data, "certificates", "a.key"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(data, "fremd"), []byte("x"), 0o600)
	for _, p := range []string{filepath.Join(root, "var/lib/caddy"), filepath.Join(root, "var/lib/caddy/.local"), filepath.Join(root, "var/lib/caddy/.local/share"), data, filepath.Join(data, "certificates"), filepath.Join(data, "certificates", "a.key")} {
		_ = os.Lchown(p, 998, 997)
	}
	_ = os.Lchown(filepath.Join(data, "fremd"), 1000, 997)
	if err := remapOwner(root, []string{"/var/lib/caddy/.local/share/caddy"}, 998, 997, 991, 990); err != nil {
		t.Fatal(err)
	}
	owner := func(p string) (int, int) {
		fi, _ := os.Lstat(p)
		st := fi.Sys().(*syscall.Stat_t)
		return int(st.Uid), int(st.Gid)
	}
	for _, p := range []string{filepath.Join(root, "var/lib/caddy"), data, filepath.Join(data, "certificates", "a.key")} {
		if u, g := owner(p); u != 991 || g != 990 {
			t.Errorf("%s: %d:%d", p, u, g)
		}
	}
	if u, g := owner(filepath.Join(data, "fremd")); u != 1000 || g != 990 {
		t.Errorf("fremd: %d:%d", u, g)
	}
	if u, _ := owner(filepath.Join(root, "var")); u != 0 {
		t.Errorf("/var: %d", u)
	}
}
