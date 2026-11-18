package dockerapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCleanerNurFesteListe(t *testing.T) {
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		switch {
		case r.Method == "DELETE" && r.URL.Path == "/volumes/in_use":
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"message":"volume is in use - [abc]"}`))
		case r.Method == "DELETE" && r.URL.Path == "/volumes/weg":
			http.NotFound(w, r)
		case r.Method == "DELETE":
			w.Write([]byte(`[{"Untagged":"x"}]`))
		case r.URL.Path == "/build/prune":
			w.Write([]byte(`{"CachesDeleted":["a"],"SpaceReclaimed":12345}`))
		case r.URL.Path == "/volumes/create":
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), `"Driver":"local"`) || !strings.Contains(string(b), `"Name":"neu"`) {
				t.Errorf("Anlegen: %s", b)
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"Name":"neu","Mountpoint":"/var/lib/docker/volumes/neu/_data"}`))
		case r.URL.Path == "/volumes":
			w.Write([]byte(`{"Volumes":[{"Name":"a","Driver":"local","Mountpoint":"/var/lib/docker/volumes/a/_data","Options":null}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newCleaner(srv.URL, http.DefaultTransport)
	ctx := context.Background()

	if err := c.RemoveImage(ctx, "nginx:1.25"); err != nil {
		t.Errorf("Image über Namen: %v", err)
	}
	if err := c.RemoveImage(ctx, "ghcr.io/foo/bar:1"); err != nil {
		t.Errorf("Image mit Registry: %v", err)
	}
	if err := c.RemoveImage(ctx, "sha256:"+strings.Repeat("a", 64)); err != nil {
		t.Errorf("Image über ID: %v", err)
	}
	if err := c.RemoveVolume(ctx, "in_use"); !errors.Is(err, ErrInUse) || !strings.Contains(err.Error(), "volume is in use") {
		t.Errorf("benutzt: %v", err)
	}
	if err := c.RemoveVolume(ctx, "weg"); !errors.Is(err, ErrGone) {
		t.Errorf("weg: %v", err)
	}
	if n, err := c.PruneBuildCache(ctx); err != nil || n != 12345 {
		t.Errorf("Build-Cache: %d %v", n, err)
	}
	if mp, err := c.CreateVolume(ctx, "neu", map[string]string{"a": "b"}); err != nil || mp != "/var/lib/docker/volumes/neu/_data" {
		t.Errorf("Anlegen: %q %v", mp, err)
	}
	if v, err := c.Volumes(ctx); err != nil || len(v) != 1 || v[0].Mountpoint == "" {
		t.Errorf("Volumes: %+v %v", v, err)
	}
	before := len(hits)

	refused := []struct{ method, path string }{
		{"DELETE", "/images/hostdeck-rueckfall/shop-web:vorher"},
		{"DELETE", "/images/nginx:1.25?force=1"},
		{"DELETE", "/images/nginx:1.25?force=true"},
		{"DELETE", "/volumes/a?force=1"},
		{"DELETE", "/containers/abc"},
		{"POST", "/containers/abc/stop"},
		{"POST", "/images/prune"},
		{"POST", "/volumes/prune"},
		{"POST", "/system/prune"},
		{"POST", "/build/prune?filters=%7B%7D"},
		{"POST", "/containers/create"},
		{"GET", "/containers/abc/logs"},
		{"GET", "/images/get"},
		{"PUT", "/volumes/a"},
	}
	for _, x := range refused {
		path, q, _ := strings.Cut(x.path, "?")
		req, _ := http.NewRequest(x.method, srv.URL+path+"?"+q, nil)
		if _, err := c.http.Do(req); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("%s %s nicht abgelehnt: %v", x.method, x.path, err)
		}
	}
	if err := c.RemoveImage(ctx, "hostdeck-rueckfall/x:vorher"); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("Rückfall-Image: %v", err)
	}
	if len(hits) != before {
		t.Errorf("abgelehnte Anfragen kamen bei Docker an: %v", hits[before:])
	}
}

func TestPlattenBelegungBeideFormate(t *testing.T) {
	alt := `{"LayersSize":1000,"Images":[{"Id":"sha256:a","Size":600,"SharedSize":100}],
	"Containers":[{"SizeRw":5}],
	"Volumes":[{"Name":"v1","UsageData":{"Size":300,"RefCount":0}},{"Name":"v2","UsageData":{"Size":-1,"RefCount":1}}],
	"BuildCache":[{"ID":"c1","Size":50,"InUse":false,"Shared":false},{"ID":"c2","Size":20,"InUse":true,"Shared":false},{"ID":"c3","Size":7,"InUse":false,"Shared":true}]}`
	neu := `{"ImageUsage":{"TotalSize":1000,"Items":[{"Id":"sha256:a","Size":600}]},
	"ContainerUsage":{"TotalSize":5,"Items":[{"SizeRw":5}]},
	"VolumeUsage":{"TotalSize":300,"Items":[{"Name":"v1","UsageData":{"Size":300,"RefCount":0}},{"Name":"v2","UsageData":{"Size":-1,"RefCount":1}}]},
	"BuildCacheUsage":{"TotalSize":70,"Items":[{"ID":"c1","Size":50,"InUse":false},{"ID":"c2","Size":20,"InUse":true},{"ID":"c3","Size":7,"Shared":true}]}}`
	for name, in := range map[string]string{"alt": alt, "neu": neu} {
		u, err := parseDF([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		if u.Images != 1000 || u.Containers != 5 || u.Volumes != 300 || u.BuildCache != 70 || u.CacheFree != 50 {
			t.Errorf("%s: %+v", name, u)
		}
		if u.VolumeSizes["v1"] != 300 || u.VolumeSizes["v2"] != -1 || u.ImageSizes["sha256:a"] != 600 {
			t.Errorf("%s: Einzelwerte %+v", name, u)
		}
	}
}
