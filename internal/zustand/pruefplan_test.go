package zustand

import (
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/betrieb"
	"github.com/officialtaxz2/hostdeck/internal/domains"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// T5: Jede Domain genau einmal, mit
// ihrer eigenen Prüfung, auch weitere Domains eines Caddy-Blocks und die
// Adresse des Leitstands. Eine Domain mit zwei Zielen ist eine Zeile mit
// beiden Zielen, das eigentliche (ohne Pfad) zuerst.
func TestT5JedeDomainEinmal(t *testing.T) {
	now := time.Now()
	ok := domains.Live{OK: true, Answer: "Status 200"}
	bad := domains.Live{Answer: "Status 502"}
	conts := []betrieb.Container{
		{Name: "shop-web-1", Project: "shop", State: "running", Running: true, HostPorts: []int{8082}},
		{Name: "app-web-1", Project: "app", State: "running", Running: true, HostPorts: []int{8081}},
	}
	sites := []domains.Site{
		{Hosts: []string{"shop.example", "laden.example"}, Upstreams: []string{"127.0.0.1:8082"},
			Live: &ok, Each: map[string]domains.Live{"shop.example": ok, "laden.example": bad}},
		{Hosts: []string{"doppel.example"}, Upstreams: []string{"127.0.0.1:8082", "127.0.0.1:8081"}, Main: []string{"127.0.0.1:8081"},
			Live: &ok, Each: map[string]domains.Live{"doppel.example": ok}},
		{Hosts: []string{"hostdeck.example"}, Upstreams: []string{"unix//run/hostdeck-caddy/web.sock"}, Live: &ok, Each: map[string]domains.Live{"hostdeck.example": ok}},
		// Dieselbe Domain noch einmal in einem zweiten Block: Caddy nimmt den ersten.
		{Hosts: []string{"shop.example"}, Upstreams: []string{"127.0.0.1:9999"}, Live: &bad},
	}
	got := Domains(sites, conts, nil, now)
	seen := map[string]int{}
	for _, d := range got {
		seen[d.Host]++
		if len(d.Hosts) != 1 || d.Hosts[0] != d.Host {
			t.Errorf("%s: Hosts %v", d.Host, d.Hosts)
		}
	}
	for _, h := range []string{"shop.example", "laden.example", "doppel.example", "hostdeck.example"} {
		if seen[h] != 1 {
			t.Errorf("%s steht %d-mal in der Liste", h, seen[h])
		}
	}
	if len(got) != 4 {
		t.Errorf("%d Einträge, erwartet 4", len(got))
	}
	by := ByHost(got)
	if by["laden.example"].State == Antwortet || by["shop.example"].State != Antwortet {
		t.Errorf("Prüfung je Domain: shop %s, laden %s", by["shop.example"].State, by["laden.example"].State)
	}
	if b := by["laden.example"].Block; len(b) != 2 {
		t.Errorf("Block von laden: %v", b)
	}
	d := by["doppel.example"]
	if d.Target != "app-web-1" || d.Project != "app" || len(d.Targets) != 2 || d.Targets[1] != "shop-web-1" {
		t.Errorf("zwei Ziele: Ziel %q, Projekt %q, Ziele %v", d.Target, d.Project, d.Targets)
	}
	if s := by["hostdeck.example"]; !s.Own || s.Target != "hostdeck" {
		t.Errorf("Leitstand: %+v", s)
	}
}

// T6: Ein Ziel, das ein Prozess außerhalb von Docker ist, heißt
// „Dienst <Prozess>“, nicht „kein Ziel“; auch ohne Route in der
// Bestandsaufnahme.
func TestT6ZielDienst(t *testing.T) {
	ok := domains.Live{OK: true, Answer: "Status 200"}
	inv := &inventory.Inventory{Listeners: []inventory.Listener{{Proto: "tcp", Addr: "127.0.0.1", Port: 9090, Process: "python3"}}}
	sites := []domains.Site{{Hosts: []string{"dienst.example"}, Upstreams: []string{"127.0.0.1:9090"}, Live: &ok, Each: map[string]domains.Live{"dienst.example": ok}}}
	got := Domains(sites, nil, inv, time.Now())
	if len(got) != 1 || got[0].Target != "Dienst python3" || got[0].State != Antwortet {
		t.Errorf("Dienst: %+v", got)
	}
}

// Pausiert seit: wann du das Ziel gestoppt hast; beim ganzen Projekt der
// letzte Stopp (erst dann war alles aus).
func TestPausiertSeit(t *testing.T) {
	a := time.Date(2026, 11, 12, 18, 0, 0, 0, time.UTC)
	b := a.Add(time.Hour)
	bad := domains.Live{Answer: "Status 502"}
	conts := []betrieb.Container{
		{Name: "p-web-1", Project: "p", State: "exited", StoppedByYou: &a, HostPorts: []int{8080}},
		{Name: "p-db-1", Project: "p", State: "exited", StoppedByYou: &b},
	}
	sites := []domains.Site{{Hosts: []string{"p.example"}, Upstreams: []string{"127.0.0.1:8080"}, Live: &bad}}
	got := Domains(sites, conts, nil, time.Now())
	if got[0].State != Pausiert || got[0].PausedSince == nil || !got[0].PausedSince.Equal(b) {
		t.Errorf("pausiert seit: %+v", got[0])
	}
}
