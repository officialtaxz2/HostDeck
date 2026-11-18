package domains

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
)

// fakeCaddy spielt caddy validate, adapt und systemctl reload. adapt macht
// aus jedem Site-Block eine Route mit seinem Inhalt (ohne Kommentare), so
// dass jede echte Änderung auffällt.
type fakeCaddy struct {
	mu      sync.Mutex
	loaded  map[string]string // Host -> Ziel, wie Caddy es gerade bedient
	reloads int
	failOn  string // validate lehnt Dateien mit diesem Wort ab
	listen  map[string]bool
	cfgPath string
}

func (f *fakeCaddy) run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	if name == "systemctl" {
		cfg := f.cfgPath
		out, err := f.adapt(cfg)
		if err != nil {
			return []byte(err.Error()), err
		}
		f.mu.Lock()
		f.reloads++
		f.loaded = hostsTargets(out)
		f.mu.Unlock()
		return nil, nil
	}
	cfg := args[2]
	switch args[0] {
	case "validate":
		_, err := f.adapt(cfg)
		if err != nil {
			return []byte(err.Error()), err
		}
		return nil, nil
	case "adapt":
		out, err := f.adapt(cfg)
		if err != nil {
			return []byte(err.Error()), err
		}
		return out, nil
	}
	return nil, fmt.Errorf("unbekannt: %v", args)
}

type site struct {
	hosts []string
	body  []string
}

func (f *fakeCaddy) adapt(cfg string) ([]byte, error) {
	var sites []site
	snippets := map[string]bool{}
	protocols := []string{"h1", "h2", "h3"}
	var walk func(file string, depth int) error
	walk = func(file string, depth int) error {
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		segs, err := caddy.Segments(b)
		if err != nil {
			return err
		}
		for _, sg := range segs {
			switch sg.Kind {
			case caddy.SegGlobal:
				for _, d := range caddy.Directives(sg.Text) {
					if d.Words[0] == "protocols" {
						protocols = d.Words[1:]
					}
				}
			case caddy.SegSnippet:
				snippets[strings.Trim(sg.Args[0], "()")] = true
			case caddy.SegImport:
				p := sg.Args[1]
				if !filepath.IsAbs(p) {
					p = filepath.Join(filepath.Dir(file), p)
				}
				var files []string
				if strings.Contains(p, "*") {
					files, _ = filepath.Glob(p)
					sort.Strings(files)
				} else {
					files = []string{p}
				}
				for _, g := range files {
					if err := walk(g, depth+1); err != nil {
						return err
					}
				}
			case caddy.SegSite:
				var body []string
				for _, d := range caddy.Directives(sg.Text) {
					if f.failOn != "" && d.Words[0] == f.failOn {
						return fmt.Errorf("%s: unbekannte Direktive %s", file, d.Words[0])
					}
					if d.Words[0] == "import" && !snippets[d.Words[1]] {
						if !strings.Contains(d.Words[1], ".") {
							return fmt.Errorf("Snippet %s fehlt", d.Words[1])
						}
						// Datei-Import: relativ zur Datei, in der er steht.
						p := filepath.Join(filepath.Dir(file), d.Words[1])
						inc, err := os.ReadFile(p)
						if err != nil {
							return err
						}
						body = append(body, "datei:"+string(inc))
						continue
					}
					body = append(body, fmt.Sprint(d.Depth, d.Words))
				}
				var hs []string
				for _, a := range sg.Addrs {
					hs = append(hs, caddy.HostOf(a))
				}
				sites = append(sites, site{hs, body})
			}
		}
		return nil
	}
	if err := walk(cfg, 0); err != nil {
		return nil, err
	}
	var routes []any
	for _, s := range sites {
		routes = append(routes, map[string]any{
			"match":  []any{map[string]any{"host": s.hosts}},
			"handle": []any{map[string]any{"handler": "subroute", "body": s.body}},
		})
	}
	out := map[string]any{"apps": map[string]any{"http": map[string]any{"servers": map[string]any{
		"srv0": map[string]any{"listen": []any{":443"}, "routes": routes, "protocols": protocols},
	}}}}
	b, _ := json.Marshal(out)
	return append([]byte(`{"level":"info","msg":"adapted"}`+"\n"), b...), nil
}

// hostsTargets liest aus dem JSON, welche Hosts auf welches Ziel zeigen.
func hostsTargets(b []byte) map[string]string {
	b, _ = caddyConfigJSON(b)
	var v struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Routes []struct {
						Match  []struct{ Host []string } `json:"match"`
						Handle []struct{ Body []string } `json:"handle"`
					} `json:"routes"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	_ = json.Unmarshal(b, &v)
	out := map[string]string{}
	for _, s := range v.Apps.HTTP.Servers {
		for _, r := range s.Routes {
			target := ""
			for _, l := range r.Handle[0].Body {
				if strings.HasPrefix(l, "1 [reverse_proxy ") {
					target = strings.TrimSuffix(strings.TrimPrefix(l, "1 [reverse_proxy "), "]")
				}
			}
			for _, h := range r.Match[0].Host {
				out[h] = target
			}
		}
	}
	return out
}

func caddyConfigJSON(b []byte) ([]byte, error) {
	if i := strings.Index(string(b), "\n"); i >= 0 && strings.Contains(string(b[:i]), "level") {
		return b[i+1:], nil
	}
	return b, nil
}

// cfgPath setzt der Test; reload liest immer das echte Caddyfile.
func (f *fakeCaddy) setCfg(p string) { f.cfgPath = p }

const vorher = `{
	email admin@example.org
}

(logging) {
	log
}

# alte Arbeitszeit
pensum.example.org {
	reverse_proxy 127.0.0.1:3004
}

work.example.org {
	import logging
	reverse_proxy 127.0.0.1:3005
}

import /PROJEKT/cloud.caddy
import hostdeck.caddy

shop.example.org, www.shop.example.org {
	reverse_proxy 127.0.0.1:3010
}
`

type testEnv struct {
	dir  string
	cfg  string
	f    *fakeCaddy
	s    *Service
	proj string
}

func setup(t *testing.T) *testEnv {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "etc", "caddy")
	proj := filepath.Join(root, "opt", "cloud")
	for _, d := range []string{dir, proj} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(dir, "Caddyfile")
	write(t, cfg, strings.Replace(vorher, "/PROJEKT", proj, 1))
	write(t, filepath.Join(proj, "cloud.caddy"), "play.example.org {\n\treverse_proxy 127.0.0.1:18081\n}\n")
	write(t, filepath.Join(dir, "hostdeck.caddy"), "hostdeck.example.org {\n\treverse_proxy unix//run/hostdeck-caddy/web.sock\n}\n")
	f := &fakeCaddy{listen: map[string]bool{"127.0.0.1:3005": true, "127.0.0.1:3010": true, "127.0.0.1:18081": true, "127.0.0.1:4000": true}}
	f.setCfg(cfg)
	out, _ := f.adapt(cfg)
	f.loaded = hostsTargets(out)
	c := &caddy.Caddy{Bin: "caddy", Config: cfg, Service: "caddy.service", Run: f.run, TempDir: root}
	s := &Service{
		Find: func(context.Context) (*caddy.Caddy, error) { return c, nil },
		Probe: func(ctx context.Context, host string) caddy.Answer {
			f.mu.Lock()
			defer f.mu.Unlock()
			t, ok := f.loaded[host]
			if !ok {
				return caddy.Answer{Err: "tls: internal error"}
			}
			if !f.listen[t] && !strings.HasPrefix(t, "unix/") {
				return caddy.Answer{Status: 502}
			}
			return caddy.Answer{Status: 200}
		},
		Listening: func(ctx context.Context, u string) bool { return f.listen[u] },
		Resolve: func(ctx context.Context, h string) ([]net.IP, error) {
			if h == "nirgends.example.org" {
				return nil, errors.New("no such host")
			}
			if h == "fremd.example.org" {
				return []net.IP{net.ParseIP("203.0.113.9")}, nil
			}
			return []net.IP{net.ParseIP("198.51.100.7")}, nil
		},
		Local:     func() []net.IP { return nil },
		ProjectOf: func(p string) string { return map[bool]string{true: "cloudgamingvpn"}[strings.HasPrefix(p, proj)] },
		Ready:     func(ctx context.Context, host string) error { return nil },
		StateFile: filepath.Join(root, "var", "domains.json"),
		Journal:   filepath.Join(root, "var", "caddy-rueckweg.json"),
	}
	return &testEnv{dir: dir, cfg: cfg, f: f, s: s, proj: proj}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUebersichtVorher(t *testing.T) {
	e := setup(t)
	ov := e.s.Overview(context.Background())
	e.s.WaitLive()
	if !ov.Found || ov.Ordered || !ov.HTTP3 {
		t.Fatalf("Stand: %+v", ov)
	}
	kinds := map[string]string{}
	for _, st := range ov.Sites {
		kinds[st.Hosts[0]] = st.Kind
	}
	want := map[string]string{"pensum.example.org": KindInline, "work.example.org": KindInline, "shop.example.org": KindInline, "play.example.org": KindImported, "hostdeck.example.org": KindLeitstand}
	for h, k := range want {
		if kinds[h] != k {
			t.Errorf("%s: %q, erwartet %q", h, kinds[h], k)
		}
	}
	ov = e.s.Overview(context.Background())
	for _, st := range ov.Sites {
		if st.Hosts[0] == "pensum.example.org" && (st.Live == nil || st.Live.OK || st.Live.Listening == nil || *st.Live.Listening) {
			t.Errorf("tote Domain nicht erkannt: %+v", st.Live)
		}
		if st.Hosts[0] == "play.example.org" && st.Project != "cloudgamingvpn" {
			t.Errorf("Projekt fehlt: %+v", st)
		}
	}
}

func TestOrdnen(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	p, err := e.s.ReorgPlan(ctx, nil)
	if err != nil || p.Problem != "" {
		t.Fatalf("Plan: %v %s", err, p.Problem)
	}
	// Vorgeschlagen: die tote Domain abschalten; HTTP/3 bleibt ohne
	// ausdrücklichen Wunsch an.
	if !p.Same || p.Options.HTTP3Off || len(p.Options.Off) != 1 || p.Options.Off[0] != "pensum.example.org" {
		t.Fatalf("Vorschläge: %+v", p.Options)
	}
	if len(p.Choices) != 2 || len(p.Keep) != 2 || p.Choices[0].Key != "http3" || p.Choices[0].Chosen {
		t.Errorf("Auswahl %+v, bleibt %v", p.Choices, p.Keep)
	}
	opts := &Options{HTTP3Off: true, Off: p.Options.Off}
	if p, err = e.s.ReorgPlan(ctx, opts); err != nil || p.Problem != "" {
		t.Fatalf("Plan mit HTTP/3 aus: %v %s", err, p.Problem)
	}
	if _, err := os.Stat(filepath.Join(e.dir, probeDir)); err == nil {
		t.Error("Probe-Ordner blieb liegen")
	}
	if _, err := e.s.Reorg(ctx, opts, "falsch", nil); !errors.Is(err, ErrChanged) {
		t.Fatalf("anderer Stand: %v", err)
	}
	before := read(t, e.cfg)
	// Zwischen Vorschau und Bestätigen vergeht Zeit; der Stand bleibt.
	later := time.Now().Add(90 * time.Second)
	e.s.Now = func() time.Time { return later }
	// Während der Prüfung nach dem Schreiben gelten hostdecks eigene Dateien
	// nicht als „von Hand geändert“.
	shop := filepath.Join(e.dir, SitesDir, "shop.example.org.caddy")
	var during []string
	msg, err := e.s.Reorg(ctx, opts, p.Stand, func(ev caddy.Event) {
		if ev.State != caddy.StateRunning || ev.Step == "" {
			return
		}
		if b, err := os.ReadFile(shop); err == nil && e.s.loadState()[shop] != sum(b) {
			during = append(during, ev.Step)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(during) > 0 {
		t.Errorf("von Hand geändert während: %v", during)
	}
	t.Log(msg)
	main := read(t, e.cfg)
	if strings.Contains(main, "work.example.org") || !strings.Contains(main, "import sites/*.caddy") || !strings.Contains(main, "protocols h1 h2") {
		t.Fatalf("neues Caddyfile:\n%s", main)
	}
	if strings.Index(main, "(logging)") > strings.Index(main, "import sites/*.caddy") {
		t.Errorf("Import vor dem Snippet:\n%s", main)
	}
	if got := read(t, filepath.Join(e.dir, SitesDir, "shop.example.org.caddy")); !strings.Contains(got, "www.shop.example.org") {
		t.Errorf("Datei shop: %s", got)
	}
	if got := read(t, filepath.Join(e.dir, SitesDir, OffDir, "pensum.example.org.caddy")); !strings.Contains(got, "# alte Arbeitszeit") {
		t.Errorf("Kommentar ging verloren: %s", got)
	}
	bk, _ := filepath.Glob(filepath.Join(e.dir, BackupDir, "*", "Caddyfile"))
	if len(bk) != 1 || read(t, bk[0]) != before {
		t.Errorf("Sicherung: %v", bk)
	}
	if _, ok := e.f.loaded["pensum.example.org"]; ok || e.f.loaded["work.example.org"] != "127.0.0.1:3005" {
		t.Errorf("Caddy bedient: %v", e.f.loaded)
	}
	if _, err := os.Stat(e.s.Journal); err == nil {
		t.Error("Journal blieb liegen")
	}
	ov := e.s.Overview(ctx)
	e.s.WaitLive()
	if !ov.Ordered || ov.HTTP3 {
		t.Errorf("danach: geordnet %v, http3 %v", ov.Ordered, ov.HTTP3)
	}
	for _, st := range ov.Sites {
		if st.Hand {
			t.Errorf("%s gilt als von Hand geändert", st.File)
		}
	}
	if p, _ := e.s.ReorgPlan(ctx, nil); p.Problem == "" {
		t.Error("zweites Ordnen ohne Problem")
	}
}

func TestOrdnenOhneVorschlaege(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	opts := &Options{}
	p, err := e.s.ReorgPlan(ctx, opts)
	if err != nil || p.Problem != "" {
		t.Fatal(err, p.Problem)
	}
	if _, err := e.s.Reorg(ctx, opts, p.Stand, nil); err != nil {
		t.Fatal(err)
	}
	if e.f.loaded["pensum.example.org"] != "127.0.0.1:3004" || strings.Contains(read(t, e.cfg), "protocols") {
		t.Errorf("Vorschläge liefen trotzdem: %v", e.f.loaded)
	}
	if _, err := e.s.ReorgPlan(ctx, &Options{Off: []string{"work.example.org"}}); !errors.Is(err, ErrBadChoice) {
		t.Errorf("lebende Domain abschalten: %v", err)
	}
	// Schon geordnet, HTTP/3 noch an: nichts vorgeschlagen, abschalten geht
	// auf Wunsch.
	p, _ = e.s.ReorgPlan(ctx, nil)
	if p.Problem == "" || len(p.Choices) != 1 || p.Choices[0].Key != "http3" || p.Choices[0].Chosen {
		t.Errorf("geordnet, HTTP/3 an: %+v", p)
	}
	if p, err := e.s.ReorgPlan(ctx, &Options{HTTP3Off: true}); err != nil || p.Problem != "" {
		t.Errorf("HTTP/3 auf Wunsch aus: %v %s", err, p.Problem)
	}
}

func TestOrdnenNichtGleichwertig(t *testing.T) {
	e := setup(t)
	// Ein Snippet nach den Seiten, das eine Seite nutzt: Nach dem Umordnen
	// stünde die Import-Zeile hinter dem Snippet; das ist in Ordnung. Ein
	// relativer Import in einer Seite dagegen zeigte danach woandershin.
	cfg := read(t, e.cfg)
	write(t, filepath.Join(e.dir, "extra.caddy"), "")
	cfg = strings.Replace(cfg, "\timport logging\n", "\timport logging\n\timport extra.caddy\n", 1)
	write(t, e.cfg, cfg)
	p, err := e.s.ReorgPlan(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Problem == "" || p.Same {
		t.Fatalf("Abweichung nicht erkannt: %+v", p)
	}
	t.Log(p.Problem)
	if got := read(t, e.cfg); got != cfg {
		t.Error("Caddyfile wurde geändert")
	}
}

func TestOrdnenNimmtZurueck(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	p, err := e.s.ReorgPlan(ctx, nil)
	if err != nil || p.Problem != "" {
		t.Fatal(err, p.Problem)
	}
	before := read(t, e.cfg)
	// Nach dem Neuladen antwortet work nicht mehr.
	e.s.Probe = func(ctx context.Context, host string) caddy.Answer {
		e.f.mu.Lock()
		defer e.f.mu.Unlock()
		if e.f.reloads > 0 && host == "work.example.org" && e.f.reloads == 1 {
			return caddy.Answer{Status: 502}
		}
		return caddy.Answer{Status: 200}
	}
	_, err = e.s.Reorg(ctx, nil, p.Stand, nil)
	var undone *caddy.ErrUndone
	if !errors.As(err, &undone) {
		t.Fatalf("erwartet zurückgelegt: %v", err)
	}
	if read(t, e.cfg) != before {
		t.Error("Caddyfile nicht zurück")
	}
	if _, err := os.Stat(filepath.Join(e.dir, SitesDir)); err == nil {
		t.Error("sites/ blieb liegen")
	}
	if _, err := os.Stat(e.s.Journal); err == nil {
		t.Error("Journal blieb liegen")
	}
}

func TestDomainAendern(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if p, _ := e.s.ChangePlan(ctx, ChangeReq{Action: ActNew, Host: "neu.example.org", Target: "4000"}); p.Problem == "" {
		t.Fatal("vor dem Ordnen ändern")
	}
	p, _ := e.s.ReorgPlan(ctx, nil)
	if _, err := e.s.Reorg(ctx, nil, p.Stand, nil); err != nil {
		t.Fatal(err)
	}

	// Neu anlegen.
	for _, bad := range []ChangeReq{
		{Action: ActNew, Host: "nirgends.example.org", Target: "4000"},
		{Action: ActNew, Host: "fremd.example.org", Target: "4000"},
		{Action: ActNew, Host: "work.example.org", Target: "4000"},
	} {
		if cp, err := e.s.ChangePlan(ctx, bad); err != nil || cp.Problem == "" {
			t.Errorf("%+v: %v %q", bad, err, cp.Problem)
		}
	}
	if _, err := e.s.ChangePlan(ctx, ChangeReq{Action: ActNew, Host: "kein host", Target: "4000"}); !errors.Is(err, ErrBadChoice) {
		t.Errorf("ungültige Domain: %v", err)
	}
	req := ChangeReq{Action: ActNew, Host: "Neu.Example.org", Target: "4000"}
	cp, err := e.s.ChangePlan(ctx, req)
	if err != nil || cp.Problem != "" || cp.Target != "127.0.0.1:4000" {
		t.Fatalf("Plan neu: %v %+v", err, cp)
	}
	if _, err := e.s.Change(ctx, req, cp.Stand, nil); err != nil {
		t.Fatal(err)
	}
	if e.f.loaded["neu.example.org"] != "127.0.0.1:4000" {
		t.Fatalf("neu nicht geladen: %v", e.f.loaded)
	}

	// Ziel ändern: auf einen Port, an dem nichts lauscht, geht nicht.
	if cp, _ := e.s.ChangePlan(ctx, ChangeReq{Action: ActTarget, Host: "neu.example.org", Target: "4001"}); cp.Problem == "" {
		t.Error("Ziel ohne Dienst")
	}
	req = ChangeReq{Action: ActTarget, Host: "neu.example.org", Target: "127.0.0.1:3010"}
	cp, _ = e.s.ChangePlan(ctx, req)
	if _, err := e.s.Change(ctx, req, cp.Stand, nil); err != nil {
		t.Fatal(err)
	}
	if e.f.loaded["neu.example.org"] != "127.0.0.1:3010" {
		t.Fatalf("Ziel: %v", e.f.loaded)
	}

	// Abschalten und wieder an.
	req = ChangeReq{Action: ActOff, Host: "www.shop.example.org"}
	cp, _ = e.s.ChangePlan(ctx, req)
	if cp.Problem != "" || len(cp.Others) != 1 {
		t.Fatalf("Plan aus: %+v", cp)
	}
	if _, err := e.s.Change(ctx, req, cp.Stand, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.f.loaded["shop.example.org"]; ok {
		t.Fatal("shop noch an")
	}
	req = ChangeReq{Action: ActOn, Host: "shop.example.org"}
	cp, _ = e.s.ChangePlan(ctx, req)
	if _, err := e.s.Change(ctx, req, cp.Stand, nil); err != nil {
		t.Fatal(err)
	}
	if e.f.loaded["www.shop.example.org"] != "127.0.0.1:3010" {
		t.Fatalf("shop nicht wieder an: %v", e.f.loaded)
	}

	// Fremde Dateien und der Leitstand bleiben unberührt.
	for _, h := range []string{"play.example.org", "hostdeck.example.org"} {
		if cp, _ := e.s.ChangePlan(ctx, ChangeReq{Action: ActOff, Host: h}); cp.Problem == "" {
			t.Errorf("%s ließe sich abschalten", h)
		}
	}

	// Von Hand geändert: Hinweis in der Vorschau.
	f := filepath.Join(e.dir, SitesDir, "work.example.org.caddy")
	write(t, f, read(t, f)+"# Notiz\n")
	cp, _ = e.s.ChangePlan(ctx, ChangeReq{Action: ActOff, Host: "work.example.org"})
	if len(cp.Notes) == 0 {
		t.Error("kein Hinweis auf Handänderung")
	}
	// Ändert sich die Datei nach der Vorschau, tut HostDeck nichts.
	write(t, f, read(t, f)+"# noch eine\n")
	if _, err := e.s.Change(ctx, ChangeReq{Action: ActOff, Host: "work.example.org"}, cp.Stand, nil); !errors.Is(err, ErrChanged) {
		t.Errorf("Änderung nach der Vorschau: %v", err)
	}
}

func TestHTTP3(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"{\n\temail a@b.c\n}\n", "{\n\temail a@b.c\n\tservers {\n\t\tprotocols h1 h2\n\t}\n}\n"},
		{"{\n\tservers {\n\t\ttrusted_proxies static private_ranges\n\t}\n}\n", "{\n\tservers {\n\t\tprotocols h1 h2\n\t\ttrusted_proxies static private_ranges\n\t}\n}\n"},
		{"{\n\tservers {\n\t\tprotocols h1 h2 h3\n\t}\n}\n", "{\n\tservers {\n\t\tprotocols h1 h2\n\t}\n}\n"},
	} {
		got, on, ok := http3Off(c.in)
		if !on || !ok || got != c.want {
			t.Errorf("%q:\n%q (an %v, geht %v)", c.in, got, on, ok)
		}
	}
	if _, on, ok := http3Off("{\n\tservers {\n\t\tprotocols h1 h2\n\t}\n}\n"); on || !ok {
		t.Error("schon aus")
	}
	if _, _, ok := http3Off("{\n\tservers :443 {\n\t\tprotocols h1\n\t}\n}\n"); ok {
		t.Error("servers mit Adresse")
	}
}

func TestZiel(t *testing.T) {
	for in, want := range map[string]string{"3005": "127.0.0.1:3005", "localhost:80": "localhost:80", ":8080": "127.0.0.1:8080", "http://10.0.0.2:9000": "10.0.0.2:9000"} {
		if got, err := NormTarget(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"0", "70000", "a b:1", "host", "unix//x"} {
		if _, err := NormTarget(bad); err == nil {
			t.Errorf("%q angenommen", bad)
		}
	}
}

// CheckNow prüft sofort und wartet; ohne abgeschaltete Domains.
func TestSofortPruefen(t *testing.T) {
	e := setup(t)
	sites, err := e.s.CheckNow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]*Live{}
	for _, st := range sites {
		seen[st.Hosts[0]] = st.Live
	}
	if lv := seen["pensum.example.org"]; lv == nil || lv.OK || lv.Took < 1 {
		t.Errorf("pensum: %+v", lv)
	}
	if len(sites) < 4 {
		t.Errorf("nur %d Domains geprüft", len(sites))
	}
}

// N21: Wer Caddy von Hand ändert (nicht über HostDeck), dem sagt Overview es
// weiter, damit die Überwachung gleich neu prüft; sonst stand die Warnung
// einer entfernten Domain bis zu fünf Minuten weiter da. Beim ersten Lesen,
// ohne Änderung und nach der eigenen Prüfung der Überwachung bleibt es
// still; eine Änderung über HostDeck meldet changed genau einmal.
func TestOverviewMeldetHandaenderung(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	n := 0
	e.s.Changed = func() { n++ }
	defer e.s.WaitLive()
	step := func(was string, want int) {
		t.Helper()
		e.s.Overview(ctx)
		if n != want {
			t.Fatalf("%s: %d-mal gemeldet, erwartet %d", was, n, want)
		}
	}
	step("erstes Lesen", 0)
	step("ohne Änderung", 0)
	write(t, e.cfg, strings.Replace(read(t, e.cfg), "# alte Arbeitszeit", "# alte Arbeitszeit, bis 2025", 1))
	step("nur ein Kommentar", 0)
	write(t, e.cfg, strings.Replace(read(t, e.cfg), "pensum.example.org {\n\treverse_proxy 127.0.0.1:3004\n}\n", "", 1))
	step("Domain von Hand entfernt", 1)
	step("danach ohne Änderung", 1)
	write(t, e.cfg, strings.Replace(read(t, e.cfg), "127.0.0.1:3005", "127.0.0.1:4000", 1))
	step("Ziel von Hand geändert", 2)

	// Die Überwachung prüft selbst: Sie hat die Änderung schon gesehen.
	write(t, e.cfg, strings.Replace(read(t, e.cfg), "127.0.0.1:4000", "127.0.0.1:3005", 1))
	e.s.WaitLive()
	if _, err := e.s.CheckNow(ctx); err != nil {
		t.Fatal(err)
	}
	step("nach der Prüfung der Überwachung", 2)

	// Über HostDeck geändert: changed meldet, Overview nicht noch einmal.
	write(t, e.cfg, strings.Replace(read(t, e.cfg), "work.example.org {", "arbeit.example.org {", 1))
	e.s.changed()
	if n != 3 {
		t.Fatalf("Änderung über HostDeck: %d-mal gemeldet, erwartet 3", n)
	}
	step("nach der Änderung über HostDeck", 3)
}
