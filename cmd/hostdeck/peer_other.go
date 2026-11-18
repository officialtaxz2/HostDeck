//go:build !linux

package main

import (
	"errors"
	"net"
)

var errNoPeerInfo = errors.New("Absender unbekannt")

func tcpPeerUID(net.Conn) (int, error) { return 0, errNoPeerInfo }
