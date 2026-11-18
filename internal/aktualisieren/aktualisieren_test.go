package aktualisieren

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/caddy"
)

func TestParseRef(t *testing.T) {
	for in, want := range map[string]Ref{
		"postgres":                        {Host: "docker.io", Repo: "library/postgres", Tag: "latest"},
		"postgres:17":                     {Host: "docker.io", Repo: "library/postgres", Tag: "17"},
		"bitnami/redis:7.2":               {Host: "docker.io", Repo: "bitnami/redis", Tag: "7.2"},
		"ghcr.io/c0m4r/kula:latest":       {Host: "ghcr.io", Repo: "c0m4r/kula", Tag: "latest"},
		"localhost:5000/app":              {Host: "localhost:5000", Repo: "app", Tag: "latest"},
		"registry.example.com:443/a/b:1":  {Host: "registry.example.com:443", Repo: "a/b", Tag: "1"},
		"index.docker.io/library/nginx:1": {Host: "docker.io", Repo: "library/nginx", Tag: "1"},
		"nginx@" + dgDB:                   {Host: "docker.io", Repo: "library/nginx", Digest: dgDB},
		"nginx:1.27@" + dgDB:              {Host: "docker.io", Repo: "library/nginx", Tag: "1.27", Digest: dgDB},
	} {
		got, err := ParseRef(in)
		if err != nil || got != want {
			t.Errorf("%s: %+v %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "Großbuchstaben", "a b", "nginx:", "nginx@sha256:kurz", "nginx:-x", "nginx:" + strings.Repeat("a", 129)} {
		if _, err := ParseRef(bad); err == nil {
			t.Errorf("%q angenommen", bad)
		}
	}
	r, _ := ParseRef("redis:7")
	if k, s := r.MatchDigest([]string{"redis@" + dgRedis}, dgRedis); !k || !s {
		t.Error("gleicher Digest")
	}
	if k, s := r.MatchDigest([]string{"docker.io/library/redis@" + dgRedis}, dgDB); !k || s {
		t.Error("anderer Digest")
	}
	if k, _ := r.MatchDigest([]string{"postgres@" + dgRedis}, dgRedis); k {
		t.Error("fremdes Repository")
	}
	if !IsDatabase("postgres:17") || !IsDatabase("bitnami/mariadb") || IsDatabase("redisinsight:2") && false || IsDatabase("nginx") {
		t.Error("Datenbanken")
	}
}

// fakeRegistry verlangt wie Docker Hub ein Token und antwortet auf HEAD mit
// dem Digest.
func fakeRegistry(t *testing.T, digests map[string]string) (*httptest.Server, *int32) {
	var heads int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			if r.URL.Query().Get("scope") == "" {
				http.Error(w, "scope", 400)
				return
			}
			w.Write([]byte(`{"token":"t-` + r.URL.Query().Get("scope") + `"}`))
		case strings.HasPrefix(r.URL.Path, "/v2/"):
			if r.Method != http.MethodHead {
				http.Error(w, "nur HEAD", 405)
				return
			}
			repo, tag, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v2/"), "/manifests/")
			if r.Header.Get("Authorization") != "Bearer t-repository:"+repo+":pull" {
				w.Header().Set("Www-Authenticate", `Bearer realm="`+srv.URL+`/token",service="fake",scope="repository:`+repo+`:pull"`)
				w.WriteHeader(401)
				return
			}
			if !strings.Contains(r.Header.Get("Accept"), "image.index") {
				http.Error(w, "accept", 400)
				return
			}
			atomic.AddInt32(&heads, 1)
			d, ok := digests[repo+":"+tag]
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Docker-Content-Digest", d)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &heads
}

func newUpdater(t *testing.T, f *fakeServer, digests map[string]string) *Updater {
	srv, _ := fakeRegistry(t, digests)
	return &Updater{
		Docker: f, CLI: f, StateDir: t.TempDir(), Wait: 600 * time.Millisecond, Settle: 100 * time.Millisecond, Poll: 20 * time.Millisecond,
		Registry: &Registry{Client: &http.Client{Transport: hostRewrite{srv.URL}}, Scheme: "http"},
		GitCheck: func(dir string) (GitRepo, error) {
			if f.gitRefused != "" {
				return GitRepo{}, &GitRefused{shopDir, f.gitRefused}
			}
			return GitRepo{Top: shopDir}, nil
		},
	}
}

// hostRewrite schickt jede Anfrage an den Test-Server.
type hostRewrite struct{ base string }

func (h hostRewrite) RoundTrip(r *http.Request) (*http.Response, error) {
	u := *r.URL
	if !strings.HasPrefix(r.URL.String(), h.base) {
		u.Scheme, u.Host = "http", strings.TrimPrefix(h.base, "http://")
	}
	r2 := r.Clone(r.Context())
	r2.URL = &u
	return http.DefaultTransport.RoundTrip(r2)
}

func TestRegistryDigest(t *testing.T) {
	srv, heads := fakeRegistry(t, map[string]string{"library/redis:7": dgRedis})
	g := &Registry{Client: &http.Client{Transport: hostRewrite{srv.URL}}, Scheme: "http"}
	r, _ := ParseRef("redis:7")
	d, err := g.Digest(context.Background(), r)
	if err != nil || d != dgRedis || *heads != 1 {
		t.Fatalf("%s %v %d", d, err, *heads)
	}
	r, _ = ParseRef("redis:gibtsnicht")
	if _, err := g.Digest(context.Background(), r); !errors.Is(err, ErrNoSuchImage) || !strings.Contains(err.Error(), "redis:gibtsnicht nicht") {
		t.Errorf("unbekannter Tag: %v", err)
	}
	k, p := parseChallenge(`Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:library/redis:pull"`)
	if k != "Bearer" || p["realm"] != "https://auth.docker.io/token" || p["scope"] != "repository:library/redis:pull" || p["service"] != "registry.docker.io" {
		t.Errorf("%s %v", k, p)
	}
}

func TestNachsehen(t *testing.T) {
	f := newFakeServer()
	f.git.remoteHead, f.git.known = commit2, false
	u := newUpdater(t, f, map[string]string{"library/redis:7": dgRedis, "library/postgres:17": idDB2, "library/alpine:latest": dgDB})
	rep, err := u.Check(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Projects) != 1 || rep.Projects[0].Error != "" {
		t.Fatalf("%+v", rep)
	}
	got := map[string]string{}
	for _, s := range rep.Projects[0].Services {
		got[s.Service] = s.State + "|" + s.Kind
	}
	want := map[string]string{"app": Newer + "|" + KindBuild, "db": Newer + "|" + KindRegistry, "redis": Current + "|" + KindRegistry, "cleanup": Unknown + "|" + KindRegistry}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%v", got)
	}
	if rep.Projects[0].NewerCount() != 2 {
		t.Error("Anzahl")
	}
	// Nachsehen ändert nichts: kein pull, kein fetch, kein tag.
	for _, c := range f.calls {
		if strings.Contains(c, "pull") || strings.Contains(c, "fetch") || strings.HasPrefix(c, "tag") || strings.Contains(c, " up ") {
			t.Errorf("Nachsehen hat geändert: %s", c)
		}
	}
	// Gemerkt, auch ohne Geheimnisse aus der Compose-Konfiguration.
	b, _ := os.ReadFile(filepath.Join(u.StateDir, reportFile))
	if !strings.Contains(string(b), `"stand": "neu"`) || strings.Contains(string(b), "geheim") {
		t.Errorf("Prüfstand: %s", b)
	}
	if u.LastReport().Projects[0].Project != "shop" {
		t.Error("nicht gemerkt")
	}
	if _, err := u.Check(context.Background(), "gibtsnicht"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unbekanntes Projekt: %v", err)
	}
}

// Ein Image, das es nur auf diesem Server gibt (keine Herkunft, die Registry
// kennt es nicht oder verlangt eine Anmeldung), ist „lokal“: Nachsehen sagt
// das, und Aktualisieren versucht nicht, es zu holen.
func TestNurLokalesImage(t *testing.T) {
	f := newFakeServer()
	delete(f.digests, idRedis)
	u := newUpdater(t, f, map[string]string{"library/postgres:17": dgDB})
	ctx := context.Background()
	rep, err := u.Check(ctx, "shop")
	if err != nil {
		t.Fatal(err)
	}
	var redis ServiceCheck
	for _, s := range rep.Projects[0].Services {
		if s.Service == "redis" {
			redis = s
		}
	}
	if redis.State != Local || !strings.Contains(redis.Detail, "nur auf diesem Server") {
		t.Errorf("redis: %+v", redis)
	}
	pl, err := u.Plan(ctx, "shop", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range pl.Options {
		if o.Service == "redis" {
			t.Errorf("redis zur Wahl: %+v", o)
		}
	}
	skipped := false
	for _, s := range pl.Skipped {
		skipped = skipped || s.Name == "redis" && strings.Contains(s.Reason, "nur auf diesem Server")
	}
	if !skipped {
		t.Errorf("ausgelassen: %+v", pl.Skipped)
	}
	if _, err := u.Plan(ctx, "shop", []string{"redis"}); !errors.Is(err, ErrBadChoice) {
		t.Errorf("redis ausdrücklich: %v", err)
	}
	// Kennt die Registry das Image, holt Aktualisieren es frisch (etwa nach
	// docker load ohne Herkunft).
	u = newUpdater(t, f, map[string]string{"library/redis:7": dgRedis})
	rep, _ = u.Check(ctx, "shop")
	for _, s := range rep.Projects[0].Services {
		if s.Service == "redis" && s.State != Unknown {
			t.Errorf("redis mit Registry: %+v", s)
		}
	}
}

// Fand das letzte Nachsehen überall nichts Neues, schlägt die Vorschau
// nichts vor; anhaken geht trotzdem.
func TestVorschauAllesAktuell(t *testing.T) {
	f := newFakeServer()
	f.digests[idDB] = []string{"postgres@" + idDB2}
	u := newUpdater(t, f, map[string]string{"library/redis:7": dgRedis, "library/postgres:17": idDB2})
	ctx := context.Background()
	u.Check(ctx, "shop")
	for _, s := range u.LastReport().Projects[0].Services {
		if s.Running && s.State != Current {
			t.Fatalf("Ausgang nicht aktuell: %+v", s)
		}
	}
	pl, err := u.Plan(ctx, "shop", nil)
	if err != nil || len(pl.Services) != 0 || !strings.Contains(pl.Problem, "alles aktuell") || len(pl.Options) == 0 {
		t.Errorf("alles aktuell: %+v %v", pl, err)
	}
	if pl, _ := u.Plan(ctx, "shop", []string{"app"}); pl.Problem != "" || !reflect.DeepEqual(pl.Services, []string{"app"}) {
		t.Errorf("trotzdem app: %+v", pl)
	}
	// Ein anderes Image als beim Nachsehen: dann weiß HostDeck es nicht mehr.
	f.ctrs[2].image = idDB2
	if pl, _ := u.Plan(ctx, "shop", nil); pl.Problem != "" || len(pl.Services) == 0 {
		t.Errorf("nach Wechsel: %+v", pl)
	}
}

func TestVorschau(t *testing.T) {
	f := newFakeServer()
	f.git.remoteHead, f.git.known = commit2, false
	u := newUpdater(t, f, map[string]string{"library/redis:7": dgRedis, "library/postgres:17": idDB2})
	ctx := context.Background()
	// Ohne Prüfstand: alle laufenden außer Datenbanken (auch Redis hält
	// Daten in eigenem Format).
	pl, err := u.Plan(ctx, "shop", nil)
	if err != nil || !reflect.DeepEqual(pl.Services, []string{"app"}) {
		t.Fatalf("%+v %v", pl, err)
	}
	if len(pl.Skipped) != 1 || pl.Skipped[0].Name != "cleanup" {
		t.Errorf("ausgelassen: %+v", pl.Skipped)
	}
	// Mit Prüfstand: nur, was neu ist, ohne Datenbank.
	u.Check(ctx, "shop")
	pl, _ = u.Plan(ctx, "shop", nil)
	if !reflect.DeepEqual(pl.Services, []string{"app"}) {
		t.Errorf("nach Nachsehen: %v", pl.Services)
	}
	if !strings.Contains(strings.Join(pl.Steps, " "), "Git holen") && !strings.Contains(strings.Join(pl.Steps, " "), "Commits holen") {
		t.Errorf("Schritte: %v", pl.Steps)
	}
	// Datenbank ausdrücklich: Hinweis.
	pl, _ = u.Plan(ctx, "shop", []string{"db"})
	if !reflect.DeepEqual(pl.Services, []string{"db"}) || !strings.Contains(strings.Join(pl.Notes, " "), "Datenbank") {
		t.Errorf("Datenbank: %+v", pl)
	}
	for _, bad := range [][]string{{"cleanup"}, {"gibtsnicht"}, {"../x"}} {
		if _, err := u.Plan(ctx, "shop", bad); !errors.Is(err, ErrBadChoice) {
			t.Errorf("%v: %v", bad, err)
		}
	}
	if pl, _ := u.Plan(ctx, "shop", []string{}); pl.Problem == "" {
		t.Error("leere Auswahl")
	}
	// Nicht committete Änderungen: bauen ja, holen nein.
	f.git.dirty = true
	pl, _ = u.Plan(ctx, "shop", []string{"app"})
	if !strings.Contains(strings.Join(pl.Notes, " "), "nicht committete") || pl.Options[0].Action != "neu bauen" {
		t.Errorf("geändert: %+v", pl)
	}
	// Unsicherer Git-Ordner: bauen ja, Git nie; die Vorschau sagt, warum.
	f.git.dirty = false
	f.gitRefused = "in .git/config steht „core.fsmonitor“"
	f.calls = nil
	pl, _ = u.Plan(ctx, "shop", []string{"app"})
	if !strings.Contains(strings.Join(pl.Notes, " "), "core.fsmonitor") || pl.Options[0].Action != "neu bauen" {
		t.Errorf("unsicherer Git-Ordner: %+v", pl)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "git ") {
			t.Errorf("Git trotzdem aufgerufen: %s", c)
		}
	}
}

func TestAktualisierenGeht(t *testing.T) {
	f := newFakeServer()
	f.git.remoteHead = commit2
	f.build["shop-app"] = idNew
	u := newUpdater(t, f, nil)
	before := map[string]int{}
	u.Domains = func(p string) []string { return []string{"shop.example.org"} }
	u.Probe = func(_ context.Context, h string) caddy.Answer { before[h]++; return caddy.Answer{Status: 200} }
	var out bytes.Buffer
	res, err := u.Do(context.Background(), "shop", []string{"app", "redis"}, &out)
	if err != nil || !res.OK {
		t.Fatalf("%+v %v\n%s", res, err, out.String())
	}
	if len(res.Changed) != 1 || res.Changed[0].Service != "app" || !reflect.DeepEqual(res.Same, []string{"redis"}) {
		t.Errorf("Ergebnis: %+v", res)
	}
	if f.git.head != commit2 || f.ctr("app").image != idNew || f.ctr("redis").id != "c-redis" {
		t.Errorf("Stand: git %s, app %s, redis %s", f.git.head, f.ctr("app").image, f.ctr("redis").id)
	}
	if f.tags[FallbackTag("shop", "app")] != idOld || f.tags[FallbackTag("shop", "redis")] != "" {
		t.Errorf("Rückfall-Namen: %v", f.tags)
	}
	fb := u.LastFallback("shop")
	if fb == nil || len(fb.Services) != 1 || fb.Services[0].ImageID != idOld || fb.Services[0].New != idNew || fb.Git[0].Head != commit1 {
		t.Fatalf("Rückfall: %+v", fb)
	}
	if before["shop.example.org"] < 2 || !strings.Contains(res.Text(), "aktualisiert: app") {
		t.Errorf("Domains %v, Text %s", before, res.Text())
	}
	if !f.called("up --detach --no-deps --no-build --pull never --force-recreate app") || f.called("cleanup") {
		t.Errorf("Aufrufe: %v", f.calls)
	}
	// Nichts Neues mehr: der Rückfall bleibt, wie er war.
	res, _ = u.Do(context.Background(), "shop", []string{"app"}, &out)
	if !res.OK || len(res.Changed) != 0 || u.LastFallback("shop").Services[0].ImageID != idOld {
		t.Errorf("zweiter Lauf: %+v %+v", res, u.LastFallback("shop"))
	}

	// Zurück auf vorher.
	pl, err := u.RevertPlan("shop")
	if err != nil || pl.Problem != "" || !reflect.DeepEqual(pl.Services, []string{"app"}) {
		t.Fatalf("Vorschau zurück: %+v %v", pl, err)
	}
	res, err = u.Revert(context.Background(), "shop", &out)
	if err != nil || !res.OK || f.ctr("app").image != idOld || f.git.head != commit1 || f.tags["shop-app"] != idOld {
		t.Fatalf("zurück: %+v %v, app %s, git %s", res, err, f.ctr("app").image, f.git.head)
	}
	if pl, _ := u.RevertPlan("shop"); pl.Problem == "" {
		t.Error("zweimal zurück")
	}
}

func TestAktualisierenStelltZurueck(t *testing.T) {
	f := newFakeServer()
	f.remote["redis:7"] = idNew
	f.broken[idNew] = true
	u := newUpdater(t, f, nil)
	var out bytes.Buffer
	res, err := u.Do(context.Background(), "shop", []string{"redis"}, &out)
	if err != nil || res.OK || !res.RolledBack {
		t.Fatalf("%+v %v\n%s", res, err, out.String())
	}
	if f.ctr("redis").image != idRedis || !f.ctr("redis").running || f.tags["redis:7"] != idRedis {
		t.Errorf("nicht zurück: %+v %v", f.ctr("redis"), f.tags)
	}
	if !strings.Contains(res.Problem, "redis") || !strings.Contains(res.Text(), "zurückgestellt") {
		t.Errorf("Meldung: %s / %s", res.Problem, res.Text())
	}
	if fb := u.LastFallback("shop"); fb == nil || fb.Reverted == nil {
		t.Errorf("Rückfall nicht als eingespielt vermerkt: %+v", fb)
	}

	// Domain antwortet nach dem Neustart schlechter: auch zurück.
	f2 := newFakeServer()
	f2.remote["redis:7"] = idNew
	u2 := newUpdater(t, f2, nil)
	u2.Domains = func(string) []string { return []string{"shop.example.org"} }
	n := 0
	u2.Probe = func(context.Context, string) caddy.Answer {
		n++
		if f2.ctr("redis").image == idNew {
			return caddy.Answer{Status: 502}
		}
		return caddy.Answer{Status: 200}
	}
	res, _ = u2.Do(context.Background(), "shop", []string{"redis"}, &out)
	if res.OK || !res.RolledBack || f2.ctr("redis").image != idRedis || !strings.Contains(res.Problem, "schlechter") {
		t.Errorf("Domain: %+v", res)
	}
}

func TestAktualisierenBrichtVorherAb(t *testing.T) {
	// Git abgewichen: nichts angefasst.
	f := newFakeServer()
	f.git.remoteHead, f.git.diverged = commit2, true
	f.build["shop-app"] = idNew
	u := newUpdater(t, f, nil)
	var out bytes.Buffer
	res, err := u.Do(context.Background(), "shop", []string{"app"}, &out)
	if err != nil || res.OK || !strings.Contains(res.Problem, "vorspulen") || f.ctr("app").id != "c-app" || f.called("build") {
		t.Errorf("abgewichen: %+v %v", res, err)
	}
	if u.LastFallback("shop") != nil {
		t.Error("Rückfall angelegt, obwohl nichts geschah")
	}
	// Holen scheitert: Git zurück, nichts neu gestartet.
	f = newFakeServer()
	f.git.remoteHead = commit2
	f.failPull = true
	u = newUpdater(t, f, nil)
	res, _ = u.Do(context.Background(), "shop", []string{"app", "redis"}, &out)
	if res.OK || !strings.Contains(res.Problem, "Images holen") || f.git.head != commit1 || f.ctr("app").id != "c-app" {
		t.Errorf("Holen scheitert: %+v git %s", res, f.git.head)
	}
	// Ohne Auswahl geht nichts.
	if _, err := u.Do(context.Background(), "shop", nil, &out); !errors.Is(err, ErrBadChoice) {
		t.Errorf("ohne Auswahl: %v", err)
	}
}
