package sicherheit

// Früherer Fund: Docker-Netze mit eigenem Brückennamen
// (etwa pterodactyl0 von Wings) und VPN-Schnittstellen (wg0) dürfen nicht
// als „außen“ gelten. Die Tabelle lässt darum Absender aus privaten Netzen
// durch, ganz gleich über welche Schnittstelle.

import (
	"strings"
	"testing"
)

func TestEigeneBrueckenUndVPNBleibenFrei(t *testing.T) {
	r := rules([]Closed{{Proto: "tcp", From: 3306, To: 3306}})
	for _, want := range []string{"ip saddr { 10.0.0.0/8, 100.64.0.0/10, 172.16.0.0/12, 192.168.0.0/16 } accept", "ip6 saddr { fc00::/7, fe80::/10 } accept"} {
		if strings.Count(r, want) != 2 { // Eingang und Weiterleitung
			t.Errorf("fehlt %q:\n%s", want, r)
		}
	}
	// Die Freigaben stehen vor dem Verwerfen.
	if strings.Index(r, "saddr") > strings.Index(r, "drop") {
		t.Errorf("Reihenfolge:\n%s", r)
	}
}
