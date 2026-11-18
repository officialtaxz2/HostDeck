package caddy

import (
	"reflect"
	"strings"
	"testing"
)

const beispiel = `{
	email admin@example.org
}

(logging) {
	log {
		output file /var/log/caddy/{args[0]}.log
	}
}

# Arbeitszeit, alt
pensum.example.org {
	reverse_proxy 127.0.0.1:3004
}

work.example.org, www.work.example.org {
	import logging work
	reverse_proxy 127.0.0.1:3005 {
		header_up X-Real-IP {remote_host}
	}
}

a.example.org,
b.example.org {
	respond "Hallo { du }" 200
}

import /opt/docker/cloudgamingvpn/deploy/caddy/cloudgamingvpn.caddy
import hostdeck.caddy

text.example.org {
	respond <<TXT
		{ nicht zählen
		TXT 200
}
# Ende
`

func TestSegmente(t *testing.T) {
	segs, err := Segments([]byte(beispiel))
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	var all strings.Builder
	for _, s := range segs {
		kinds = append(kinds, s.Kind)
		all.WriteString(s.Text)
	}
	if all.String() != beispiel {
		t.Fatalf("Segmente ergeben nicht die Datei:\n%s", all.String())
	}
	want := []string{SegGlobal, SegSnippet, SegSite, SegSite, SegSite, SegImport, SegImport, SegSite, SegTrail}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("Arten %v, erwartet %v", kinds, want)
	}
	if !strings.HasPrefix(segs[2].Text, "\n# Arbeitszeit, alt\npensum") {
		t.Errorf("Kommentar gehört zum Block danach: %q", segs[2].Text)
	}
	if !reflect.DeepEqual(segs[3].Addrs, []string{"work.example.org", "www.work.example.org"}) {
		t.Errorf("Adressen: %v", segs[3].Addrs)
	}
	if !reflect.DeepEqual(segs[4].Addrs, []string{"a.example.org", "b.example.org"}) {
		t.Errorf("Adressen über zwei Zeilen: %v", segs[4].Addrs)
	}
	if segs[5].Args[1] != "/opt/docker/cloudgamingvpn/deploy/caddy/cloudgamingvpn.caddy" {
		t.Errorf("Import: %v", segs[5].Args)
	}
	ds := Directives(segs[3].Text)
	var top []string
	for _, d := range ds {
		if d.Depth == 1 {
			top = append(top, d.Words[0])
		}
	}
	if !reflect.DeepEqual(top, []string{"import", "reverse_proxy"}) {
		t.Errorf("Zeilen im Block: %+v", ds)
	}
}

func TestSegmenteFehler(t *testing.T) {
	for _, bad := range []string{"a.de {\n", "}\n", "a.de {\n respond <<X\n"} {
		if _, err := Segments([]byte(bad)); err == nil {
			t.Errorf("%q: kein Fehler", bad)
		}
	}
	segs, err := Segments([]byte("example.org\nreverse_proxy :8080\n"))
	if err != nil || segs[0].Kind != SegOther {
		t.Errorf("Caddyfile ohne Blöcke: %v %+v", err, segs)
	}
}

func TestHostOf(t *testing.T) {
	for in, want := range map[string]string{
		"Example.org":           "example.org",
		"https://a.de:8443/x":   "a.de",
		"http://b.de":           "b.de",
		":8080":                 "",
		"{$DOMAIN}":             "",
		"*.example.org":         "*.example.org",
		"[::1]:443":             "::1",
		"panel.example.net:443": "panel.example.net",
	} {
		if got := HostOf(in); got != want {
			t.Errorf("HostOf(%q) = %q, erwartet %q", in, got, want)
		}
	}
}

func TestGleicheKonfiguration(t *testing.T) {
	a := `{"level":"info"}
{"apps":{"http":{"servers":{"srv0":{"listen":[":443"],"routes":[{"match":[{"host":["a.de"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"127.0.0.1:1"}]}]},{"match":[{"host":["b.de"]}],"handle":[{"handler":"a"},{"handler":"b"}]}]}}},"tls":{"automation":{"policies":[{"subjects":["b.de","a.de"]}]}}}}`
	// Andere Reihenfolge der Seiten, anderer Servername: gleich.
	b := `{"apps":{"http":{"servers":{"srv1":{"listen":[":443"],"routes":[{"match":[{"host":["b.de"]}],"handle":[{"handler":"a"},{"handler":"b"}]},{"match":[{"host":["a.de"]}],"handle":[{"handler":"reverse_proxy","upstreams":[{"dial":"127.0.0.1:1"}]}]}]}}},"tls":{"automation":{"policies":[{"subjects":["a.de","b.de"]}]}}}}`
	ja, err := configJSON([]byte(a))
	if err != nil {
		t.Fatal(err)
	}
	if same, where, err := SameConfig(ja, []byte(b)); err != nil || !same {
		t.Fatalf("gleich erwartet: %v %s", err, where)
	}
	// Reihenfolge der Handler innerhalb einer Seite zählt.
	c := strings.Replace(b, `{"handler":"a"},{"handler":"b"}`, `{"handler":"b"},{"handler":"a"}`, 1)
	if same, where, _ := SameConfig(ja, []byte(c)); same || !strings.Contains(where, "routes") {
		t.Errorf("Handler-Reihenfolge: %v %s", same, where)
	}
	// Anderes Ziel.
	d := strings.Replace(b, "127.0.0.1:1", "127.0.0.1:2", 1)
	if same, where, _ := SameConfig(ja, []byte(d)); same {
		t.Errorf("anderes Ziel nicht erkannt (%s)", where)
	}
	// HTTP/3 aus ist eine Abweichung.
	e := strings.Replace(b, `"listen":[":443"]`, `"listen":[":443"],"protocols":["h1","h2"]`, 1)
	if same, _, _ := SameConfig(ja, []byte(e)); same {
		t.Error("protocols nicht erkannt")
	}
	// Überschneiden sich Seiten (Platzhalter und feste Domain), zählt die
	// Reihenfolge: Die erste passende gewinnt.
	w1 := `{"apps":{"http":{"servers":{"srv0":{"listen":[":443"],"routes":[{"match":[{"host":["*.a.de"]}],"handle":[{"handler":"x"}]},{"match":[{"host":["b.a.de"]}],"handle":[{"handler":"y"}]}]}}}}}`
	w2 := `{"apps":{"http":{"servers":{"srv0":{"listen":[":443"],"routes":[{"match":[{"host":["b.a.de"]}],"handle":[{"handler":"y"}]},{"match":[{"host":["*.a.de"]}],"handle":[{"handler":"x"}]}]}}}}}`
	if same, _, _ := SameConfig([]byte(w1), []byte(w2)); same {
		t.Error("Reihenfolge bei Platzhalter nicht beachtet")
	}
	// Gleiche Domain mit Pfad: ebenso.
	p1 := `{"apps":{"http":{"servers":{"srv0":{"listen":[":443"],"routes":[{"match":[{"host":["a.de"],"path":["/api/*"]}],"handle":[{"handler":"x"}]},{"match":[{"host":["a.de"]}],"handle":[{"handler":"y"}]}]}}}}}`
	p2 := `{"apps":{"http":{"servers":{"srv0":{"listen":[":443"],"routes":[{"match":[{"host":["a.de"]}],"handle":[{"handler":"y"}]},{"match":[{"host":["a.de"],"path":["/api/*"]}],"handle":[{"handler":"x"}]}]}}}}}`
	if same, _, _ := SameConfig([]byte(p1), []byte(p2)); same {
		t.Error("Reihenfolge bei Pfad nicht beachtet")
	}
	// TLS-Regel ohne Subjekte (für alle) und eine feste: Reihenfolge zählt.
	t1 := `{"apps":{"tls":{"automation":{"policies":[{"subjects":["a.de"],"issuers":[{"module":"acme"}]},{"issuers":[{"module":"internal"}]}]}}}}`
	t2 := `{"apps":{"tls":{"automation":{"policies":[{"issuers":[{"module":"internal"}]},{"subjects":["a.de"],"issuers":[{"module":"acme"}]}]}}}}`
	if same, _, _ := SameConfig([]byte(t1), []byte(t2)); same {
		t.Error("Reihenfolge der TLS-Regeln nicht beachtet")
	}
}

func TestGleicheKonfigurationMitLogs(t *testing.T) {
	a := `{"apps":{"http":{"servers":{"srv0":{"listen":[":443"],"logs":{"logger_names":{"a.de":["log0"],"b.de":["log1"]}},"routes":[]}}}},"logging":{"logs":{"default":{"exclude":["http.log.access.log0","http.log.access.log1"]},"log0":{"writer":{"output":"file","filename":"/var/log/a.log"},"include":["http.log.access.log0"]},"log1":{"writer":{"output":"file","filename":"/var/log/b.log"},"include":["http.log.access.log1"]}}}}`
	// Nach dem Umordnen kommt b.de zuerst und heißt log0.
	b := `{"apps":{"http":{"servers":{"srv0":{"listen":[":443"],"logs":{"logger_names":{"b.de":["log0"],"a.de":["log1"]}},"routes":[]}}}},"logging":{"logs":{"default":{"exclude":["http.log.access.log0","http.log.access.log1"]},"log1":{"writer":{"output":"file","filename":"/var/log/a.log"},"include":["http.log.access.log1"]},"log0":{"writer":{"output":"file","filename":"/var/log/b.log"},"include":["http.log.access.log0"]}}}}`
	if same, where, err := SameConfig([]byte(a), []byte(b)); err != nil || !same {
		t.Fatalf("gleich erwartet: %v %s", err, where)
	}
	// Vertauschte Dateien: nicht gleich.
	c := strings.Replace(b, `"b.de":["log0"],"a.de":["log1"]`, `"b.de":["log1"],"a.de":["log0"]`, 1)
	if same, _, _ := SameConfig([]byte(a), []byte(c)); same {
		t.Error("vertauschte Logs nicht erkannt")
	}
}
