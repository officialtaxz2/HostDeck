package main

import (
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
)

func TestAbsenderDerVerbindung(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	uid, err := tcpPeerUID(s)
	if err != nil || uid != os.Getuid() {
		t.Fatalf("Absender: %d %v (ich bin %d)", uid, err, os.Getuid())
	}
	if !allowedPeers()[uid] {
		t.Fatal("eigener Benutzer abgewiesen")
	}
}

// Als root: Eine Verbindung eines anderen Benutzers erkennt HostDeck.
func TestAbsenderAndererBenutzer(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("nur als root")
	}
	if _, err := exec.LookPath("runuser"); err != nil {
		t.Skip("runuser fehlt")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	cmd := exec.Command("runuser", "-u", "nobody", "--", "bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/"+port+"; sleep 2")
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	defer cmd.Wait()
	s, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	uid, err := tcpPeerUID(s)
	if err != nil || uid == 0 || allowedPeers()[uid] {
		t.Fatalf("anderer Benutzer: %d %v", uid, err)
	}
}
