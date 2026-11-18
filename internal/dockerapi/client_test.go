package dockerapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func testServer(t *testing.T, hits *atomic.Int32) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/containers/abc/json":
			w.Write([]byte(`{"Id":"abc","Name":"/pensum-db-1","Config":{"Image":"postgres:17","Env":["POSTGRES_PASSWORD=geheim123","PATH=/usr/bin","LEER="]}}`))
		case "/version":
			w.Write([]byte(`{"Version":"28.5.1","ApiVersion":"1.51"}`))
		default:
			w.Write([]byte(`[]`))
		}
	}))
	t.Cleanup(srv.Close)
	return newClient(srv.URL, srv.Client().Transport)
}

func TestNurLesendeMethoden(t *testing.T) {
	var hits atomic.Int32
	c := testServer(t, &hits)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		_, err := c.Do(context.Background(), m, "/containers/abc/json", nil)
		if !errors.Is(err, ErrNotAllowed) {
			t.Errorf("%s: Fehler %v, erwartet ErrNotAllowed", m, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("Server hat %d Anfragen bekommen, erwartet 0", hits.Load())
	}
}

func TestNurFreigegebeneEndpunkte(t *testing.T) {
	var hits atomic.Int32
	c := testServer(t, &hits)
	blocked := []string{
		"/containers/abc/attach/ws",
		"/containers/abc/export",
		"/containers/abc/archive",
		"/containers/abc/logs",
		"/containers/../json",
		"/images/get",
		"/images/abc/get",
		"/events",
		"/exec/abc/json",
		"/v1.47/containers/abc/attach/ws",
	}
	for _, p := range blocked {
		_, err := c.Do(context.Background(), http.MethodGet, p, nil)
		if !errors.Is(err, ErrNotAllowed) {
			t.Errorf("GET %s: Fehler %v, erwartet ErrNotAllowed", p, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("Server hat %d Anfragen bekommen, erwartet 0", hits.Load())
	}

	allowed := []string{"/_ping", "/version", "/info", "/containers/json", "/containers/abc/json", "/volumes", "/system/df", "/images/json", "/networks", "/v1.47/containers/json"}
	for _, p := range allowed {
		resp, err := c.Do(context.Background(), http.MethodGet, p, nil)
		if err != nil {
			t.Errorf("GET %s: %v", p, err)
			continue
		}
		resp.Body.Close()
	}
}

func TestUmgebungsvariablenNurNamen(t *testing.T) {
	var hits atomic.Int32
	c := testServer(t, &hits)
	ct, err := c.Inspect(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"POSTGRES_PASSWORD", "PATH", "LEER"}
	if strings.Join(ct.Config.Env, ",") != strings.Join(want, ",") {
		t.Fatalf("Env = %v, erwartet %v", ct.Config.Env, want)
	}
	for _, n := range ct.Config.Env {
		if strings.Contains(n, "geheim") || strings.Contains(n, "=") {
			t.Fatalf("Wert einer Umgebungsvariable gespeichert: %q", n)
		}
	}
	// Ob ein Wert drinsteht, ja; der Wert selbst nicht.
	if got := strings.Join(ct.Config.EnvSet, ","); got != "POSTGRES_PASSWORD,PATH" {
		t.Fatalf("EnvSet = %q", got)
	}
}

func TestVersion(t *testing.T) {
	var hits atomic.Int32
	c := testServer(t, &hits)
	v, err := c.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.Version != "28.5.1" || v.APIVersion != "1.51" {
		t.Fatalf("Version = %+v", v)
	}
}

func TestImagesFragtHerkunftNachAPIVersion(t *testing.T) {
	var query atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query.Store(r.URL.RawQuery)
		w.Write([]byte(`[{"Id":"sha256:aa","RepoTags":["pensum:lokal"],"RepoDigests":["pensum@sha256:aa"],"Labels":{"com.docker.compose.project":"pensum"},` +
			`"Manifests":[{"Kind":"image","Available":true,"ImageData":{"Identity":{"Build":[{"Ref":"abc","CreatedAt":"2026-10-30T10:00:00Z"}]}}},{"Kind":"attestation","Available":true}]}]`))
	}))
	t.Cleanup(srv.Close)
	c := newClient(srv.URL, srv.Client().Transport)
	for _, tc := range []struct{ api, want string }{
		{"1.56", "identity=1&manifests=1"},
		{"1.54", "identity=1&manifests=1"},
		{"1.51", "manifests=1"},
		{"1.47", "manifests=1"},
		{"1.45", ""},
		{"", ""},
	} {
		imgs, err := c.Images(context.Background(), tc.api)
		if err != nil {
			t.Fatal(err)
		}
		if got := query.Load().(string); got != tc.want {
			t.Errorf("API %q: Anfrage ?%s, erwartet ?%s", tc.api, got, tc.want)
		}
		m := imgs[0].Manifests
		if len(m) != 2 || m[0].ImageData == nil || m[0].ImageData.Identity == nil || len(m[0].ImageData.Identity.Build) != 1 || m[1].Kind != "attestation" {
			t.Fatalf("Manifests falsch gelesen: %+v", m)
		}
		if imgs[0].Labels["com.docker.compose.project"] != "pensum" {
			t.Fatalf("Labels falsch gelesen: %+v", imgs[0].Labels)
		}
	}
}

func TestImagesOhneZusaetzeWennErweiterteAnfrageScheitert(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.RawQuery != "" {
			http.Error(w, "unbekannte Option", http.StatusBadRequest)
			return
		}
		w.Write([]byte(`[{"Id":"sha256:aa","RepoTags":["postgres:17"]}]`))
	}))
	t.Cleanup(srv.Close)
	c := newClient(srv.URL, srv.Client().Transport)
	imgs, err := c.Images(context.Background(), "1.56")
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 1 || calls.Load() != 2 {
		t.Fatalf("%d Images nach %d Anfragen, erwartet 1 nach 2", len(imgs), calls.Load())
	}
}

func TestAtLeast(t *testing.T) {
	for _, tc := range []struct {
		v    string
		want bool
	}{{"1.54", true}, {"1.56", true}, {"2.0", true}, {"1.53", false}, {"1.9", false}, {"", false}, {"x.y", false}, {"1", false}} {
		if got := AtLeast(tc.v, 1, 54); got != tc.want {
			t.Errorf("AtLeast(%q, 1, 54) = %v, erwartet %v", tc.v, got, tc.want)
		}
	}
}
