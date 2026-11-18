package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

var errNoPeerInfo = errors.New("Absender unbekannt")

// tcpPeerUID sagt, welchem Benutzer die Gegenseite einer TCP-Verbindung auf
// 127.0.0.1 gehört. Das steht in /proc/net/tcp: Dort ist die Gegenseite
// ein eigener Eintrag mit local = ihre Adresse, rem = unsere.
func tcpPeerUID(c net.Conn) (int, error) {
	local, ok1 := c.LocalAddr().(*net.TCPAddr)
	remote, ok2 := c.RemoteAddr().(*net.TCPAddr)
	if !ok1 || !ok2 || remote.IP.To4() == nil {
		return 0, errNoPeerInfo
	}
	b, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		return 0, errNoPeerInfo
	}
	want := [2]string{procAddr(remote), procAddr(local)}
	for _, line := range strings.Split(string(b), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 8 || f[1] != want[0] || f[2] != want[1] {
			continue
		}
		uid, err := strconv.Atoi(f[7])
		if err != nil {
			return 0, err
		}
		return uid, nil
	}
	return 0, fmt.Errorf("Verbindung %s nicht in /proc/net/tcp", remote)
}

// procAddr schreibt eine IPv4-Adresse so wie /proc/net/tcp: die vier
// Bytes umgekehrt in Hex, dann der Port.
func procAddr(a *net.TCPAddr) string {
	ip := a.IP.To4()
	return fmt.Sprintf("%02X%02X%02X%02X:%04X", ip[3], ip[2], ip[1], ip[0], a.Port)
}
