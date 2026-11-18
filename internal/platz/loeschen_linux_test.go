//go:build linux

package platz

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func inspectOne(t *testing.T, root, host string) Sel {
	t.Helper()
	s, err := Inspect(context.Background(), root, []string{host}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return s[0]
}

func TestRemoveTree(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "opt/app/backups/a/b/c.tar"), 1000)
	write(t, filepath.Join(root, "opt/app/backups/d.tar"), 1000)
	write(t, filepath.Join(root, "opt/app/bleibt"), 10)
	s := inspectOne(t, root, "/opt/app/backups")
	res, err := Remove(context.Background(), root, s)
	if err != nil || res.Left != 0 || res.Files != 2 {
		t.Fatalf("Remove: %+v %v", res, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "opt/app/backups")); !os.IsNotExist(err) {
		t.Error("Ordner ist noch da")
	}
	if _, err := os.Lstat(filepath.Join(root, "opt/app/bleibt")); err != nil {
		t.Error("Nachbar ist weg")
	}
}

func TestRemoveDoesNotFollowLinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write(t, filepath.Join(outside, "wichtig"), 10)
	write(t, filepath.Join(root, "opt/x/datei"), 10)
	if err := os.Symlink(outside, filepath.Join(root, "opt/x/link")); err != nil {
		t.Fatal(err)
	}
	// Verknüpfung als Ordner im Weg: /opt/umweg -> outside
	if err := os.Symlink(outside, filepath.Join(root, "opt/umweg")); err != nil {
		t.Fatal(err)
	}
	s := inspectOne(t, root, "/opt/x")
	if res, err := Remove(context.Background(), root, s); err != nil || res.Left != 0 {
		t.Fatalf("Remove: %+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "wichtig")); err != nil {
		t.Fatal("Ziel der Verknüpfung gelöscht")
	}
	// Ein Pfad durch eine Verknüpfung wird nicht aufgelöst.
	s = Sel{Path: "/opt/umweg/wichtig"}
	fi, _ := os.Lstat(filepath.Join(outside, "wichtig"))
	_, s.Ident.Dev, s.Ident.Ino, _ = statOf(fi)
	if _, err := Remove(context.Background(), root, s); err == nil {
		t.Fatal("durch die Verknüpfung gelöscht")
	}
	if _, err := os.Stat(filepath.Join(outside, "wichtig")); err != nil {
		t.Fatal("Ziel der Verknüpfung gelöscht")
	}
	// Die Verknüpfung selbst lässt sich löschen, ihr Ziel bleibt.
	s = inspectOne(t, root, "/opt/umweg")
	if _, err := Remove(context.Background(), root, s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "wichtig")); err != nil {
		t.Fatal("Ziel der Verknüpfung gelöscht")
	}
}

func TestRemoveChecksIdentity(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a/datei"), 10)
	s := inspectOne(t, root, "/a/datei")
	// Ersetzt: neue Datei (andere Inode) an derselben Stelle.
	write(t, filepath.Join(root, "a/neu"), 10)
	if err := os.Rename(filepath.Join(root, "a/neu"), filepath.Join(root, "a/datei")); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(context.Background(), root, s); !errors.Is(err, ErrChanged) {
		t.Fatalf("ersetzte Datei: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a/datei")); err != nil {
		t.Fatal("ersetzte Datei gelöscht")
	}
	os.RemoveAll(filepath.Join(root, "a"))
	if _, err := Remove(context.Background(), root, s); !errors.Is(err, ErrGone) {
		t.Errorf("fehlt: %v", err)
	}
}

func TestRemoveStopsAtMount(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("braucht root")
	}
	root := t.TempDir()
	write(t, filepath.Join(root, "daten/a"), 10)
	mp := filepath.Join(root, "daten/platte")
	if err := os.MkdirAll(mp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount("tmpfs", mp, "tmpfs", 0, "size=1m"); err != nil {
		t.Skip("tmpfs geht hier nicht: ", err)
	}
	defer unix.Unmount(mp, unix.MNT_DETACH)
	write(t, filepath.Join(mp, "drin"), 10)
	s := Sel{Path: "/daten"}
	fi, _ := os.Lstat(filepath.Join(root, "daten"))
	_, s.Ident.Dev, s.Ident.Ino, _ = statOf(fi)
	res, err := Remove(context.Background(), root, s)
	if err != nil {
		t.Fatal(err)
	}
	if res.Left == 0 {
		t.Error("Einhängepunkt nicht gemeldet")
	}
	if _, err := os.Stat(filepath.Join(mp, "drin")); err != nil {
		t.Fatal("auf der eingehängten Platte gelöscht")
	}
	if _, err := os.Stat(filepath.Join(root, "daten/a")); !os.IsNotExist(err) {
		t.Error("übriger Inhalt nicht gelöscht")
	}
	// Bind-Mount auf derselben Platte.
	src := filepath.Join(root, "quelle")
	write(t, filepath.Join(src, "echt"), 10)
	bind := filepath.Join(root, "ziel/bind")
	os.MkdirAll(bind, 0o755)
	if err := unix.Mount(src, bind, "", unix.MS_BIND, ""); err != nil {
		t.Skip("bind geht hier nicht: ", err)
	}
	defer unix.Unmount(bind, unix.MNT_DETACH)
	s = Sel{Path: "/ziel"}
	fi, _ = os.Lstat(filepath.Join(root, "ziel"))
	_, s.Ident.Dev, s.Ident.Ino, _ = statOf(fi)
	if res, _ := Remove(context.Background(), root, s); res.Left == 0 {
		t.Error("Bind-Mount nicht gemeldet")
	}
	if _, err := os.Stat(filepath.Join(src, "echt")); err != nil {
		t.Fatal("durch den Bind-Mount gelöscht")
	}
}
