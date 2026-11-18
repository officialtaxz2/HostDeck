//go:build !linux

package dienst

import "net"

// PeerUID gibt es nur unter Linux; anderswo läuft der Dienst nicht.
func PeerUID(net.Conn) (int, bool) { return -1, false }
