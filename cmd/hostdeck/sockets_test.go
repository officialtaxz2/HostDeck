//go:build linux

package main

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

func TestSocketVonSystemd(t *testing.T) {
	dir, _ := os.MkdirTemp("", "hostdecks")
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "web.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	f, err := ln.(*net.UnixListener).File()
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	// listenersFrom übernimmt die Nummer und schließt sie, wie bei systemd.
	// Darum eine eigene Kopie, sonst schließt f später eine fremde Datei,
	// die inzwischen dieselbe Nummer hat.
	fd, err := syscall.Dup(int(f.Fd()))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"LISTEN_PID": strconv.Itoa(os.Getpid()), "LISTEN_FDS": "1", "LISTEN_FDNAMES": "caddy"}
	// Ein anderer Prozess war gemeint: nichts übernehmen.
	got, err := listenersFrom(func(k string) string {
		if k == "LISTEN_PID" {
			return "1"
		}
		return env[k]
	}, os.Getpid(), fd)
	if err != nil || got != nil {
		t.Fatalf("fremde PID: %v %v", got, err)
	}
	got, err = listenersFrom(func(k string) string { return env[k] }, os.Getpid(), fd)
	if err != nil || got["caddy"] == nil {
		t.Fatalf("%v %v", got, err)
	}
	defer got["caddy"].Close()
	go func() {
		c, err := got["caddy"].Accept()
		if err == nil {
			io.WriteString(c, "hallo")
			c.Close()
		}
	}()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(c)
	if string(b) != "hallo" {
		t.Fatalf("%q", b)
	}
}
