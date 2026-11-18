package zustand

import (
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/betrieb"
	"github.com/officialtaxz2/hostdeck/internal/domains"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

func TestDomainZustaende(t *testing.T) {
	now := time.Now()
	yes, no := true, false
	bad := &domains.Live{Answer: "Status 502", Listening: &yes}
	conts := []betrieb.Container{
		{Name: "blog-web-1", Project: "blog", Service: "web", State: "exited", StoppedByYou: &now, HostPorts: []int{8081}},
		{Name: "shop-web-1", Project: "shop", Service: "web", State: "running", Running: true, HostPorts: []int{8082}},
		{Name: "app-web-1", Project: "app", Service: "web", State: "running", Running: true, HostPorts: []int{8083}},
		{Name: "app-db-1", Project: "app", Service: "db", State: "exited", StoppedByYou: &now},
		{Name: "kaputt-web-1", Project: "kaputt", Service: "web", State: "exited", ExitCode: 1, HostPorts: []int{8084}},
		{Name: "netz-web-1", Project: "netz", Service: "web", State: "exited", StoppedByYou: &now, HostNetwork: true},
		{Name: "docker-ziel", Project: "dn", Service: "api", State: "exited", StoppedByYou: &now},
		{Name: "proj-web-1", Project: "proj", Service: "web", State: "exited", StoppedByYou: &now},
		{Name: "proj-db-1", Project: "proj", Service: "db", State: "exited", StoppedByYou: &now},
		{Name: "proj-init-1", Project: "proj", Service: "init", State: "exited", Job: true},
		// Umzugstag: nach dem letzten Lauf absichtlich aus (Labor S4).
		{Name: "umz-web-1", Project: "umz", Service: "web", State: "exited", StoppedByMove: true, HostPorts: []int{8085}},
		{Name: "umz-db-1", Project: "umz", Service: "db", State: "exited", StoppedByMove: true},
	}
	inv := &inventory.Inventory{
		Containers: []inventory.Container{{Key: "compose:netz/web", Name: "netz-web-1"}},
		Listeners:  []inventory.Listener{{Proto: "tcp", Addr: "0.0.0.0", Port: 9000, Container: "compose:netz/web"}},
		Caddy: &inventory.Caddy{Routes: []inventory.Route{
			// veraltet: Die Bestandsaufnahme sah blog noch laufen.
			{Hosts: []string{"blog.example"}, Target: "compose:blog/web", Status: inventory.RouteOK},
		}},
	}
	sites := []domains.Site{
		{Hosts: []string{"blog.example"}, Upstreams: []string{"127.0.0.1:8081"}, Live: bad},
		{Hosts: []string{"shop.example"}, Upstreams: []string{"localhost:8082"}, Live: &domains.Live{OK: true, Answer: "Status 200"}},
		{Hosts: []string{"app.example"}, Upstreams: []string{"127.0.0.1:8083"}, Live: bad},
		{Hosts: []string{"kaputt.example"}, Upstreams: []string{"127.0.0.1:8084"}, Live: &domains.Live{Answer: "Status 502", Listening: &no}},
		{Hosts: []string{"leer.example"}, Upstreams: []string{"127.0.0.1:8099"}, Live: &domains.Live{Answer: "Status 502", Listening: &no}},
		{Hosts: []string{"netz.example"}, Upstreams: []string{"127.0.0.1:9000"}, Live: bad},
		{Hosts: []string{"dn.example"}, Upstreams: []string{"http://api:3000"}, Live: bad},
		{Hosts: []string{"proj.example"}, Upstreams: []string{"127.0.0.1:7000"}, Project: "proj", Live: bad},
		{Hosts: []string{"umz.example"}, Upstreams: []string{"127.0.0.1:8085"}, Live: bad},
		{Hosts: []string{"aus.example"}, Kind: domains.KindOff, Upstreams: []string{"127.0.0.1:8081"}},
		{Hosts: []string{"neu.example"}, Upstreams: []string{"127.0.0.1:8082"}},
		{Hosts: []string{"*.example"}, Upstreams: []string{"127.0.0.1:8082"}, Live: bad},
	}
	got := ByHost(Domains(sites, conts, inv, now))
	want := map[string][2]string{
		"blog.example":   {Pausiert, LevelIdle},
		"shop.example":   {Antwortet, LevelOK},
		"app.example":    {AntwortetNicht, LevelWarn},
		"kaputt.example": {AntwortetNicht, LevelErr},
		"leer.example":   {InsLeere, LevelErr},
		"netz.example":   {Pausiert, LevelIdle},
		"dn.example":     {Pausiert, LevelIdle},
		"proj.example":   {Pausiert, LevelIdle},
		"aus.example":    {Aus, LevelIdle},
		"umz.example":    {Pausiert, LevelIdle},
		"neu.example":    {Ungeprueft, LevelIdle},
	}
	if len(got) != len(want) {
		t.Errorf("Zahl: %d, will %d: %+v", len(got), len(want), got)
	}
	for h, w := range want {
		d := got[h]
		if d.State != w[0] || d.Level != w[1] || d.Word != Words[w[0]] {
			t.Errorf("%s: %s/%s (%q), will %s/%s", h, d.State, d.Level, d.Text, w[0], w[1])
		}
	}
	if d := got["blog.example"]; d.Target != "blog-web-1" || d.Project != "blog" {
		t.Errorf("blog: %+v", d)
	}
	if d := got["app.example"]; len(d.Stopped) != 1 || d.Stopped[0] != "app-db-1" {
		t.Errorf("app: %+v", d)
	}
	if d := got["kaputt.example"]; d.Text == "" {
		t.Errorf("kaputt ohne Grund: %+v", d)
	}
}

// Ein Projekt, in dem nur der Einmal-Job aus ist, ist nicht „von dir
// gestoppt“; eine Seite, die nicht antwortet, bleibt rot.
func TestNurJobAusIstKeinePause(t *testing.T) {
	conts := []betrieb.Container{
		{Name: "p-web-1", Project: "p", State: "running", Running: true, HostPorts: []int{8000}},
		{Name: "p-init-1", Project: "p", State: "exited", Job: true},
	}
	sites := []domains.Site{{Hosts: []string{"p.example"}, Upstreams: []string{"127.0.0.1:8000"}, Live: &domains.Live{Answer: "Status 500"}}}
	d := Domains(sites, conts, nil, time.Now())[0]
	if d.State != AntwortetNicht || d.Level != LevelErr {
		t.Errorf("%+v", d)
	}
}

// I7: Gerade gestartet, die Seite antwortet noch nicht: kein Fehler.
func TestGeradeGestartet(t *testing.T) {
	now := time.Now()
	started := now.Add(-30 * time.Second)
	conts := []betrieb.Container{{Name: "blog-web-1", Project: "blog", State: "running", Running: true, Since: &started, HostPorts: []int{8081}}}
	sites := []domains.Site{{Hosts: []string{"blog.example"}, Upstreams: []string{"127.0.0.1:8081"}, Live: &domains.Live{Answer: "Status 502"}}}
	if d := Domains(sites, conts, nil, now)[0]; d.State != Startet || d.Level != LevelIdle {
		t.Errorf("gerade gestartet: %+v", d)
	}
	if d := Domains(sites, conts, nil, now.Add(5*time.Minute))[0]; d.State != AntwortetNicht || d.Level != LevelErr {
		t.Errorf("nach 5 Minuten: %+v", d)
	}
}
