package dockerapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOpsNurFesteListe(t *testing.T) {
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		switch {
		case r.URL.Path == "/containers/json":
			w.Write([]byte(`[{"Id":"a1","Names":["/pensum-app"],"State":"running","Labels":{"com.docker.compose.project":"pensum"},"Mounts":[{"Type":"bind","Source":"/opt/x","Destination":"/x"}]}]`))
		case r.URL.Path == "/containers/pensum-app/json":
			w.Write([]byte(`{"Id":"a1","RestartCount":2,"State":{"Running":true,"StartedAt":"2026-11-02T08:00:00Z"},"Config":{"Tty":false},"HostConfig":{"RestartPolicy":{"Name":"always"}}}`))
		case r.URL.Path == "/containers/pensum-app/stats":
			w.Write([]byte(`{"cpu_stats":{"cpu_usage":{"total_usage":3000},"system_cpu_usage":200000,"online_cpus":4},"precpu_stats":{"cpu_usage":{"total_usage":1000},"system_cpu_usage":100000},"memory_stats":{"usage":300,"limit":1000,"stats":{"inactive_file":100}}}`))
		case r.URL.Path == "/containers/pensum-app/logs":
			w.Write(frame(1, "2026-11-03T09:00:00Z hallo\n"))
			w.Write(frame(2, "2026-11-03T09:00:01Z fehler\n"))
		case r.URL.Path == "/containers/pensum-app/top":
			w.Write([]byte(`{"Titles":["PID","USER","%CPU","RSS","ELAPSED","COMMAND"],"Processes":[["4711","app","1.5","20480","01:02:03","node"]]}`))
		case r.URL.Path == "/images/sha256:abababababababababababababababababababababababababababababababab/json":
			w.Write([]byte(`{"Id":"sha256:abababababababababababababababababababababababababababababababab","RepoTags":["headscale/headscale:0.26.1"],"RepoDigests":["headscale/headscale@sha256:11"],"Created":"2026-10-01T00:00:00Z"}`))
		case strings.HasSuffix(r.URL.Path, "/stop"), strings.HasSuffix(r.URL.Path, "/start"), strings.HasSuffix(r.URL.Path, "/restart"):
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newOps(srv.URL, http.DefaultTransport)
	ctx := context.Background()

	list, err := c.List(ctx)
	if err != nil || len(list) != 1 || list[0].Name() != "pensum-app" || list[0].Mounts[0].Source != "/opt/x" {
		t.Fatalf("Liste: %v %+v", err, list)
	}
	v, err := c.Inspect(ctx, "pensum-app")
	if err != nil || v.RestartCount != 2 || v.HostConfig.RestartPolicy.Name != "always" || v.State.StartedAt == "" {
		t.Fatalf("Inspect: %v %+v", err, v)
	}
	u, err := c.Stats(ctx, "pensum-app")
	if err != nil || u.CPU != 8 || u.Mem != 200 || u.MemLimit != 1000 {
		t.Fatalf("Messwert: %v %+v", err, u)
	}
	b, err := c.Logs(ctx, "pensum-app", 100, false, 1<<20)
	if err != nil || string(b) != "2026-11-03T09:00:00Z hallo\n"+StderrMark+"2026-11-03T09:00:01Z fehler\n" {
		t.Fatalf("Logs: %v %q", err, b)
	}
	b, _ = c.Logs(ctx, "pensum-app", 100, false, 10)
	if len(b) != 10 {
		t.Errorf("Grenze: %q", b)
	}
	top, err := c.Top(ctx, "pensum-app")
	if err != nil || len(top.Titles) != 6 || top.Processes[0][5] != "node" {
		t.Fatalf("Top: %v %+v", err, top)
	}
	if !strings.Contains(strings.Join(hits, "\n"), "GET /containers/pensum-app/top?ps_args=-o+pid%2Cuser%2Cpcpu%2Crss%2Cetime%2Ccomm") {
		t.Errorf("Top ohne feste Spalten: %v", hits)
	}
	for _, f := range []func() error{
		func() error { return c.Start(ctx, "pensum-app") },
		func() error { return c.Stop(ctx, "pensum-app", 20*time.Second) },
		func() error { return c.Restart(ctx, "pensum-app", 0) },
	} {
		if err := f(); err != nil {
			t.Fatal(err)
		}
	}
	im, err := c.ImageInspect(ctx, "sha256:abababababababababababababababababababababababababababababababab")
	if err != nil || im.ID != "sha256:abababababababababababababababababababababababababababababababab" || len(im.RepoDigests) != 1 {
		t.Fatalf("Image: %v %+v", err, im)
	}
	if !strings.Contains(strings.Join(hits, "\n"), "POST /containers/pensum-app/stop?t=20") {
		t.Errorf("Stopp ohne Frist: %v", hits)
	}

	// Alles andere lehnt der Transport ab, bevor Docker etwas sieht.
	n := len(hits)
	for _, bad := range []struct{ method, path string }{
		{"POST", "/containers/create"},
		{"POST", "/containers/pensum-app/kill"},
		{"POST", "/containers/pensum-app/exec"},
		{"POST", "/containers/pensum-app/update"},
		{"DELETE", "/containers/pensum-app"},
		{"POST", "/images/create"},
		{"POST", "/containers/prune"},
		{"GET", "/containers/pensum-app/export"},
		{"GET", "/containers/pensum-app/attach/ws"},
		{"GET", "/images/get"},
		{"GET", "/images/headscale/headscale:0.26.1/json"},
		{"GET", "/images/sha256:abc/json"},
		{"DELETE", "/images/sha256:abababababababababababababababababababababababababababababababab"},
		{"POST", "/images/sha256:abababababababababababababababababababababababababababababababab/tag"},
		{"GET", "/containers/pensum-app/logs?follow=1"},
		{"GET", "/containers/pensum-app/stats?stream=true"},
		{"GET", "/containers/pensum-app/stats"},
		{"GET", "/containers/pensum-app/top"},
		{"GET", "/containers/pensum-app/top?ps_args=aux"},
		{"GET", "/containers/pensum-app/top?ps_args=-o+pid,args"},
		{"GET", "/containers/pensum-app/changes"},
	} {
		p, q, _ := strings.Cut(bad.path, "?")
		qv, _ := url.ParseQuery(q)
		resp, err := c.do(ctx, bad.method, p, qv)
		if err == nil {
			resp.Body.Close()
		}
		if !errors.Is(err, ErrNotAllowed) {
			t.Errorf("%s %s: %v", bad.method, bad.path, err)
		}
	}
	if len(hits) != n {
		t.Errorf("abgelehnte Anfragen kamen bei Docker an: %v", hits[n:])
	}
}

func TestOpsNurNeustartRegel(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.URL.Path+" "+string(b))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"Warnings":[]}`))
	}))
	defer srv.Close()
	c := newOps(srv.URL, http.DefaultTransport)
	ctx := context.Background()
	if err := c.SetRestartPolicy(ctx, "arcane", "unless-stopped"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != `/containers/arcane/update {"RestartPolicy":{"Name":"unless-stopped"}}` {
		t.Fatalf("Anfrage: %v", got)
	}
	if err := c.SetRestartPolicy(ctx, "arcane", "immer"); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("unbekannte Regel: %v", err)
	}
	for _, body := range []string{
		`{"Memory":1}`,
		`{"RestartPolicy":{"Name":"always"},"Memory":1}`,
		`{"RestartPolicy":{"Name":"always","MaximumRetryCount":5}}`,
		`{"RestartPolicy":{"Name":"sonstwas"}}`,
		`kaputt`,
	} {
		req, _ := http.NewRequest("POST", srv.URL+"/containers/arcane/update", strings.NewReader(body))
		if _, err := c.http.Do(req); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("%s nicht abgelehnt: %v", body, err)
		}
	}
	if len(got) != 1 {
		t.Errorf("abgelehnte Anfragen kamen an: %v", got[1:])
	}
}
