package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/betrieb"
	"github.com/officialtaxz2/hostdeck/internal/caddy"
	"github.com/officialtaxz2/hostdeck/internal/dienst"
	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/domains"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/ort"
)

// pauseDocker ist ein kleiner Docker für die Verdrahtung: Container mit
// Namen, Compose-Labels, Zustand und veröffentlichtem Port.
type pauseDocker struct {
	cs []pauseCtr
}

type pauseCtr struct {
	name, project, service, state string
	port                          int
}

func (f *pauseDocker) List(context.Context) ([]dockerapi.OpsContainer, error) {
	var out []dockerapi.OpsContainer
	for _, c := range f.cs {
		out = append(out, dockerapi.OpsContainer{ID: c.name, Names: []string{"/" + c.name}, State: c.state,
			Labels: map[string]string{"com.docker.compose.project": c.project, "com.docker.compose.service": c.service}})
	}
	return out, nil
}

func (f *pauseDocker) Inspect(_ context.Context, id string) (dockerapi.Container, error) {
	var v dockerapi.Container
	for _, c := range f.cs {
		if c.name != id {
			continue
		}
		v.Name = "/" + c.name
		v.State.Status = c.state
		v.State.Running = c.state == "running"
		v.HostConfig.RestartPolicy.Name = "unless-stopped"
		if c.port > 0 {
			v.HostConfig.PortBindings = map[string][]dockerapi.PortBinding{"80/tcp": {{HostIP: "127.0.0.1", HostPort: itoa(c.port)}}}
		}
	}
	return v, nil
}

func itoa(n int) string { return strconv.Itoa(n) }

func (f *pauseDocker) Stats(context.Context, string) (dockerapi.Usage, error) {
	return dockerapi.Usage{}, nil
}
func (f *pauseDocker) Logs(context.Context, string, int, bool, int64) ([]byte, error) {
	return nil, nil
}
func (f *pauseDocker) Start(context.Context, string) error               { return nil }
func (f *pauseDocker) Stop(context.Context, string, time.Duration) error { return nil }
func (f *pauseDocker) Restart(context.Context, string, time.Duration) error {
	return nil
}

// pauseSetup baut einen Server mit zwei Projekten: blog (von dir gestoppt)
// und shop (läuft, antwortet aber nicht). Die Bestandsaufnahme ist älter als
// der Stopp und führt die Ziele mit ihren Schlüsseln (compose:…), der
// Betrieb mit ihren Docker-Namen.
func pauseSetup(t *testing.T, blogState string) (*dienst.Dienst, *betrieb.Service, *domains.Service, string, string) {
	t.Helper()
	dir := t.TempDir()
	result, data := filepath.Join(dir, "ergebnis"), filepath.Join(dir, "daten")
	for _, d := range []string{result, data} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(cfg, []byte("blog.beispiel.test {\n\treverse_proxy 127.0.0.1:8081\n}\n\nshop.beispiel.test {\n\treverse_proxy 127.0.0.1:8082\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inv := inventory.Inventory{
		Containers: []inventory.Container{
			{Key: "compose:blog/web", Name: "blog-web-1", Project: "blog", Service: "web", Running: true, State: "running",
				Ports: []inventory.Port{{HostIP: "127.0.0.1", HostPort: 8081, ContainerPort: 80, Proto: "tcp"}}},
			{Key: "compose:shop/web", Name: "shop-web-1", Project: "shop", Service: "web", Running: true, State: "running",
				Ports: []inventory.Port{{HostIP: "127.0.0.1", HostPort: 8082, ContainerPort: 80, Proto: "tcp"}}},
		},
		Caddy: &inventory.Caddy{Found: true, Config: cfg, Routes: []inventory.Route{
			{Hosts: []string{"blog.beispiel.test"}, Handler: "reverse_proxy", Upstream: "127.0.0.1:8081", Port: 8081, Target: "compose:blog/web", Status: inventory.RouteOK},
			{Hosts: []string{"shop.beispiel.test"}, Handler: "reverse_proxy", Upstream: "127.0.0.1:8082", Port: 8082, Target: "compose:shop/web", Status: inventory.RouteOK},
		}},
	}
	b, _ := json.Marshal(inv)
	if err := os.WriteFile(filepath.Join(result, "inventory.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	stoppedFile := filepath.Join(data, ort.StoppedFile)
	if blogState != "running" {
		if err := betrieb.SaveStopped(stoppedFile, map[string]time.Time{"blog-web-1": time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	dock := &pauseDocker{cs: []pauseCtr{
		{name: "blog-web-1", project: "blog", service: "web", state: blogState, port: 8081},
		{name: "shop-web-1", project: "shop", service: "web", state: "running", port: 8082},
	}}
	cont := &betrieb.Service{Docker: dock, StoppedFile: stoppedFile}
	dm := &domains.Service{
		Find: func(context.Context) (*caddy.Caddy, error) { return &caddy.Caddy{Config: cfg}, nil },
		Probe: func(context.Context, string) caddy.Answer {
			return caddy.Answer{Err: "connection refused"}
		},
		StateFile: filepath.Join(data, "domains.json"),
	}
	return &dienst.Dienst{}, cont, dm, result, data
}

// K1: Die Pause greift mit echten Schlüsseln (Bestandsaufnahme) und echten
// Docker-Namen (Betrieb). Vor dem Fix war Paused immer leer.
func TestUeberwachungPausiertGestopptesProjekt(t *testing.T) {
	d, cont, dm, result, data := pauseSetup(t, "exited")
	r := monitorRules(d, cont, dm, nil, result, data)
	sites, err := r.Sites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range sites {
		got[s.Host] = s.Paused
	}
	if got["blog.beispiel.test"] == "" {
		t.Errorf("blog ist von dir gestoppt, Pause fehlt: %+v", sites)
	}
	if got["shop.beispiel.test"] != "" {
		t.Errorf("shop läuft und antwortet nicht, darf nicht pausiert sein: %+v", sites)
	}
}

// Nach dem Starten ist die Seite nicht mehr pausiert, auch wenn die
// Bestandsaufnahme nichts davon weiß.
func TestUeberwachungNachStartNichtPausiert(t *testing.T) {
	d, cont, dm, result, data := pauseSetup(t, "running")
	sites, err := monitorRules(d, cont, dm, nil, result, data).Sites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sites {
		if s.Paused != "" {
			t.Errorf("%s pausiert, läuft aber: %+v", s.Host, s)
		}
	}
}
