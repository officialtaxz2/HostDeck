package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/dnscheck"
)

func TestLineWatch(t *testing.T) {
	var buf bytes.Buffer
	l := &lineWatch{w: &buf, needle: []byte("✓ Caddy läuft")}
	l.Write([]byte("  ✓ Cad"))
	l.Write([]byte("dy läuft\n"))
	if !l.seen || buf.String() != "  ✓ Caddy läuft\n" {
		t.Errorf("über zwei Schreibvorgänge: %v %q", l.seen, buf.String())
	}
	l = &lineWatch{w: &buf, needle: []byte("✓ Caddy läuft")}
	l.Write([]byte("  ✗ Caddy startet nicht\n"))
	if l.seen {
		t.Error("falsch erkannt")
	}
	// Fehlerzeilen für den Lotsen, auch über zwei Schreibvorgänge, höchstens fünf.
	if len(l.errs) != 1 || l.errs[0] != "Caddy startet nicht" {
		t.Errorf("Fehlerzeile: %q", l.errs)
	}
	l.Write([]byte("    ✗ web: MODUS ist le"))
	l.Write([]byte("er\n  ✓ gut\n"))
	for i := 0; i < 9; i++ {
		l.Write([]byte("✗ noch einer\n"))
	}
	if len(l.errs) != maxErrLines || l.errs[1] != "web: MODUS ist leer" {
		t.Errorf("Fehlerzeilen: %q", l.errs)
	}
}

func TestSeitenUeberNeueAdresse(t *testing.T) {
	here := map[string]caddy.Answer{"a.de": {Status: 200}, "b.de": {Status: 200}, "leit.de": {Status: 200}, "kaputt.de": {Status: 502}}
	tries := map[string]int{}
	var mu sync.Mutex
	there := func(_ context.Context, h string) caddy.Answer {
		mu.Lock()
		tries[h]++
		n := tries[h]
		mu.Unlock()
		switch h {
		case "a.de":
			if n == 1 {
				return caddy.Answer{Err: "connection refused"} // Caddy lädt noch
			}
			return caddy.Answer{Status: 200}
		case "b.de":
			return caddy.Answer{Err: "tls: failed to verify certificate: x509: certificate is valid for c.de, not b.de"}
		case "leit.de":
			return caddy.Answer{Status: 502}
		}
		return caddy.Answer{Status: 502}
	}
	m := &serverMoves{
		Sites:     func(context.Context) ([]string, error) { return []string{"leit.de", "b.de", "a.de", "kaputt.de"}, nil },
		Own:       func() string { return "leit.de" },
		SiteHere:  func(_ context.Context, h string) caddy.Answer { return here[h] },
		SiteRetry: time.Millisecond,
	}
	m.SiteThere = func(ctx context.Context, h string) caddy.Answer { return there(ctx, h) }
	var out bytes.Buffer
	m.checkSites(context.Background(), dienst.MoveTarget{Host: "203.0.113.7", Port: 22}, nil, &out)
	s := out.String()
	for _, want := range []string{
		"4 Domains über 203.0.113.7",
		"  ✓ a.de: Zertifikat gültig, Status 200 wie hier",
		"  ✗ b.de: tls: failed to verify certificate",
		"  ✓ kaputt.de: Zertifikat gültig, Status 502 (hier Status 502)",
		"  ! leit.de: Zertifikat gültig, aber Status 502 (hier Status 200)\n    Das ist der Leitstand selbst",
		"2 von 4 Seiten antworten über die neue Adresse wie hier.",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("Ausgabe ohne %q:\n%s", want, s)
		}
	}
	if tries["b.de"] != 3 {
		t.Errorf("Versuche b.de: %d", tries["b.de"])
	}
}

func TestDNSListe(t *testing.T) {
	dir := t.TempDir()
	m := &serverMoves{
		Dir:      dir,
		Sites:    func(context.Context) ([]string, error) { return []string{"neu.de", "alt.de", "halb.de", "cf.de"}, nil },
		LocalIPs: func() []net.IP { return []net.IP{net.ParseIP("203.0.113.1"), net.ParseIP("2001:db8::1")} },
		DNSResolver: &dnscheck.Resolver{
			NS: func(context.Context, string) ([]string, error) { return nil, errors.New("aus") },
			Addr: func(_ context.Context, h string) ([]net.IP, error) {
				return map[string][]net.IP{
					"neu.de":  {net.ParseIP("198.51.100.20")},
					"alt.de":  {net.ParseIP("203.0.113.1"), net.ParseIP("2001:db8::1")},
					"halb.de": {net.ParseIP("198.51.100.20"), net.ParseIP("2001:db8::1")},
					"cf.de":   {net.ParseIP("104.16.0.1")},
				}[h], nil
			},
		},
	}
	m.saveLast(dienst.MoveTarget{Host: "198.51.100.20", Port: 22, User: "root", Password: "geheim"})
	rep := m.DNS(context.Background())
	if rep.Target != "198.51.100.20" || len(rep.Here) != 2 || len(rep.Domains) != 4 {
		t.Fatalf("%+v", rep)
	}
	got := map[string]string{}
	for _, d := range rep.Domains {
		got[d.Name] = d.State
	}
	want := map[string]string{"neu.de": dienst.DNSStateNew, "alt.de": dienst.DNSStateOld, "halb.de": dienst.DNSStateMixed, "cf.de": dienst.DNSStateOther}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s statt %s", k, got[k], v)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, lastFile)); strings.Contains(string(b), "geheim") {
		t.Error("Passwort gespeichert")
	}
}

// Lotse Schritt 5: Zonen mit Nameservern, Testadresse des
// Leitstands und Vorschläge für Namen, die noch hierher zeigen, aber in
// keiner Liste stehen (Zone selbst, www, MX- und SRV-Ziele).
func TestDNSVorschlaege(t *testing.T) {
	old, fresh := net.ParseIP("203.0.113.1"), net.ParseIP("198.51.100.20")
	addrs := map[string][]net.IP{
		"app.beispiel.de":          {old},
		"hostdeck.beispiel.de":     {old},
		"hostdeck-neu.beispiel.de": {fresh},
		"beispiel.de":              {old},                       // Zone selbst: Vorschlag
		"www.beispiel.de":          {net.ParseIP("192.0.2.99")}, // woanders: kein Vorschlag
		"mail.beispiel.de":         {old},                       // MX-Ziel: Vorschlag
		"spiel.beispiel.de":        {old},                       // SRV-Ziel: Vorschlag
		"mc.beispiel.de":           {old},                       // schon von Hand: kein Vorschlag
		"andere.org":               {old},
	}
	m := &serverMoves{
		Dir: t.TempDir(),
		Sites: func(context.Context) ([]string, error) {
			return []string{"app.beispiel.de", "hostdeck.beispiel.de", "andere.org"}, nil
		},
		Own:      func() string { return "hostdeck.beispiel.de" },
		LocalIPs: func() []net.IP { return []net.IP{old} },
		DNSResolver: &dnscheck.Resolver{
			NS: func(_ context.Context, z string) ([]string, error) {
				switch z {
				case "beispiel.de":
					return []string{"ns2.anbieter.test", "ns1.anbieter.test"}, nil
				case "andere.org":
					return []string{"ns.andere.test"}, nil
				}
				return nil, errors.New("keine")
			},
			Addr: func(_ context.Context, h string) ([]net.IP, error) {
				if ips, ok := addrs[h]; ok {
					return ips, nil
				}
				return nil, errors.New("no such host")
			},
		},
		DNSNames: func(_ context.Context, z string) []string {
			if z == "beispiel.de" {
				return []string{"mail.beispiel.de.", "spiel.beispiel.de.", "mc.beispiel.de."}
			}
			return nil
		},
	}
	m.saveLast(dienst.MoveTarget{Host: "198.51.100.20", Port: 22, User: "root"})
	if err := m.SetExtraDomains([]string{"mc.beispiel.de"}); err != nil {
		t.Fatal(err)
	}
	rep := m.DNS(context.Background())
	if rep.Own != "hostdeck.beispiel.de" || rep.Test == nil || rep.Test.Name != "hostdeck-neu.beispiel.de" || rep.Test.State != dienst.DNSStateNew {
		t.Fatalf("Testadresse: %+v %+v", rep.Own, rep.Test)
	}
	if len(rep.TargetIPs) != 1 || rep.TargetIPs[0] != "198.51.100.20" {
		t.Errorf("Ziel-IPs: %v", rep.TargetIPs)
	}
	if len(rep.Zones) != 2 || rep.Zones[0].Name != "beispiel.de" || rep.Zones[0].Domains != 3 || strings.Join(rep.Zones[0].Servers, ",") != "ns1.anbieter.test,ns2.anbieter.test" {
		t.Errorf("Zonen: %+v", rep.Zones)
	}
	var got []string
	for _, d := range rep.Suggest {
		got = append(got, d.Name)
	}
	if strings.Join(got, ",") != "beispiel.de,mail.beispiel.de,spiel.beispiel.de" {
		t.Errorf("Vorschläge: %v", got)
	}
}
