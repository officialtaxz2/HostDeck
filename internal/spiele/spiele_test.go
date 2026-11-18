package spiele

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
	"github.com/officialtaxz2/hostdeck/internal/fixture"
	"github.com/officialtaxz2/hostdeck/internal/hostfs"
)

// Spielserver sind Container wie alle anderen. HostDeck fragt
// kein Spiel ab und geht dafür nicht ins Netz; das Paket darf deshalb weder
// "net" noch eine Abfrage-Bibliothek einbinden.
func TestKeineAbfrageInsNetz(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range af.Imports {
			if p, _ := strconv.Unquote(im.Path.Value); p == "net" || strings.HasPrefix(p, "net/") {
				t.Errorf("%s bindet %s ein", f, p)
			}
		}
	}
}

// --- Erkennung ---

type fakeList struct {
	mu   sync.Mutex
	list []dockerapi.OpsContainer
}

func (f *fakeList) List(context.Context) ([]dockerapi.OpsContainer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.list, nil
}

func write(t *testing.T, root, p, content string) {
	t.Helper()
	full := filepath.Join(root, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mount(src, dst string) dockerapi.MountPoint {
	return dockerapi.MountPoint{Type: "bind", Source: src, Destination: dst, RW: true}
}

const (
	uMC1  = "11111111-1111-4111-8111-111111111111"
	uMC2  = "22222222-2222-4222-8222-222222222222"
	uL4D  = "33333333-3333-4333-8333-333333333333"
	uOff  = "44444444-4444-4444-8444-444444444444"
	uNone = "55555555-5555-4555-8555-555555555555"
	vol   = "/var/lib/pterodactyl/volumes/"
)

func panelRig(t *testing.T) (*Service, *fakeList) {
	root := t.TempDir()
	write(t, root, vol+uMC1+"/server.properties", "motd=\\u00a7aHallo\nlevel-name=world\nserver-port=25565\nrcon.password=geheim\n")
	write(t, root, vol+uMC2+"/server.properties", "motd=A Minecraft Server\nlevel-name=Kreativ\n")
	write(t, root, vol+uL4D+"/srcds_run", "#!/bin/sh\n")
	write(t, root, vol+uL4D+"/left4dead2/gameinfo.txt", "\"GameInfo\" {}\n")
	write(t, root, vol+uL4D+"/left4dead2/cfg/server.cfg", "hostname \"Coop am Abend\"\nrcon_password \"geheim\"\n")
	write(t, root, vol+uOff+"/server.properties", "motd=Aus\nserver-port=25599\n")
	write(t, root, vol+uNone+"/start.sh", "#!/bin/sh\n")
	write(t, root, "/srv/mc/data/server.properties", "motd=Compose-Welt\n")
	const mcPort, l4dPort, composePort, routerPort = 25565, 27015, 25566, 25570
	wings := dockerapi.OpsContainer{ID: "w", Names: []string{"/wings"}, Image: "ghcr.io/calagopus/wings:latest", State: "running",
		Labels: map[string]string{"com.docker.compose.project": "mcserver"},
		Mounts: []dockerapi.MountPoint{mount("/var/run/docker.sock", "/var/run/docker.sock"), mount("/var/lib/pterodactyl", "/var/lib/pterodactyl")}}
	srv := func(id, uuid, image, state string, ports ...dockerapi.ContainerPort) dockerapi.OpsContainer {
		return dockerapi.OpsContainer{ID: id, Names: []string{"/" + uuid}, Image: image, State: state, Ports: ports,
			Mounts: []dockerapi.MountPoint{mount(vol+uuid, "/home/container")}}
	}
	fl := &fakeList{list: []dockerapi.OpsContainer{
		wings,
		srv("a", uMC1, "ghcr.io/pterodactyl/yolks:java_21", "running", dockerapi.ContainerPort{IP: "127.0.0.1", PrivatePort: 25565, PublicPort: mcPort, Type: "tcp"}),
		srv("b", uMC2, "ghcr.io/pterodactyl/yolks:java_21", "exited"),
		srv("c", uL4D, "ghcr.io/parkervcp/games:source", "running", dockerapi.ContainerPort{IP: "0.0.0.0", PrivatePort: 27015, PublicPort: l4dPort, Type: "udp"},
			dockerapi.ContainerPort{IP: "0.0.0.0", PrivatePort: 27015, PublicPort: l4dPort, Type: "tcp"}),
		srv("d", uNone, "ghcr.io/parkervcp/yolks:debian", "running"),
		{ID: "e", Names: []string{"/mc-mc-1"}, Image: "itzg/minecraft-server:latest", State: "running", Labels: map[string]string{"com.docker.compose.project": "mc"},
			Mounts: []dockerapi.MountPoint{mount("/srv/mc/data", "/data")}, Ports: []dockerapi.ContainerPort{{IP: "0.0.0.0", PrivatePort: 25565, PublicPort: composePort, Type: "tcp"}}},
		{ID: "f", Names: []string{"/router"}, Image: "itzg/mc-router", State: "running", Ports: []dockerapi.ContainerPort{{IP: "0.0.0.0", PrivatePort: 25565, PublicPort: routerPort, Type: "tcp"}}},
		{ID: "g", Names: []string{"/web"}, Image: "nginx:1.27", State: "running", Labels: map[string]string{"com.docker.compose.project": "blog"},
			Mounts: []dockerapi.MountPoint{mount("/srv/blog", "/usr/share/nginx/html")}, Ports: []dockerapi.ContainerPort{{IP: "0.0.0.0", PrivatePort: 80, PublicPort: 8080, Type: "tcp"}}},
	}}
	s := &Service{Docker: fl, FS: hostfs.New(root)}
	return s, fl
}

func TestErkennungMitPanelUndCompose(t *testing.T) {
	s, _ := panelRig(t)
	s.Scan(context.Background())
	got := map[string]Server{}
	for _, x := range s.List() {
		got[x.ID] = x
	}
	if len(got) != 5 {
		b, _ := json.MarshalIndent(s.List(), "", " ")
		t.Fatalf("%d Spielserver: %s", len(got), b)
	}
	if mc := got[uMC1]; mc.Game != "Minecraft Java" || mc.Name != "Hallo" || mc.State != StateRunning || mc.Panel != "wings" || mc.Short != "MC" || mc.Found != ByFiles {
		t.Errorf("Minecraft im Panel: %+v", mc)
	}
	if off := got[uMC2]; off.State != StateOff || off.Name != "Welt Kreativ" || off.Container != uMC2 {
		t.Errorf("gestoppter Container: %+v", off)
	}
	if l := got[uL4D]; l.Game != "Left 4 Dead 2" || l.Name != "Coop am Abend" || l.Short != "L4D2" {
		t.Errorf("Left 4 Dead 2: %+v", l)
	}
	if gone := got[uOff]; gone.State != StateOff || gone.Container != "" || gone.Name != "Aus" || gone.Panel != "wings" {
		t.Errorf("Server ohne Container: %+v", gone)
	}
	if c := got["mc-mc-1"]; c.Project != "mc" || c.Panel != "" || c.Name != "Compose-Welt" {
		t.Errorf("Compose-Minecraft: %+v", c)
	}
	if _, ok := got[uNone]; ok {
		t.Error("Panel-Server ohne Spiel als Spielserver erkannt")
	}
	if _, ok := got["router"]; ok {
		t.Error("mc-router als Spielserver")
	}
	// Keine Passwörter und keine Spieldaten in der Liste: kein Spieler, kein
	// Höchstwert, keine Antwort, keine Version, kein Port.
	b, _ := json.Marshal(s.List())
	for _, w := range []string{"geheim", `"spieler"`, `"max"`, `"antwort"`, `"version"`, `"port"`, `"welt"`, `"karte"`, `"bots"`} {
		if strings.Contains(string(b), w) {
			t.Errorf("%s in der Liste: %s", w, b)
		}
	}
}

// Ohne Spiele: keine Einträge.
func TestErkennungOhneSpiele(t *testing.T) {
	fl := &fakeList{list: []dockerapi.OpsContainer{
		{ID: "g", Names: []string{"/web"}, Image: "nginx", State: "running", Mounts: []dockerapi.MountPoint{mount("/srv/blog", "/usr/share/nginx/html")},
			Ports: []dockerapi.ContainerPort{{IP: "0.0.0.0", PrivatePort: 80, PublicPort: 8080, Type: "tcp"}}},
		{ID: "p", Names: []string{"/db"}, Image: "postgres:16", State: "running", Mounts: []dockerapi.MountPoint{{Type: "volume", Source: "/var/lib/docker/volumes/x/_data", Destination: "/var/lib/postgresql/data", RW: true}}},
	}}
	s := &Service{Docker: fl, FS: hostfs.New(t.TempDir())}
	s.Scan(context.Background())
	if len(s.List()) != 0 {
		t.Errorf("%d Spielserver", len(s.List()))
	}
	if b, _ := json.Marshal(s.Servers()); string(b) != "[]" {
		t.Errorf("leere Liste: %s", b)
	}
}

// Eine Symlink-Datei liest HostDeck nicht.
func TestVerknuepfungNichtGelesen(t *testing.T) {
	root := t.TempDir()
	write(t, root, "/etc/secret", "motd=Geheim\n")
	os.MkdirAll(filepath.Join(root, "/srv/mc"), 0o755)
	os.Symlink(filepath.Join(root, "/etc/secret"), filepath.Join(root, "/srv/mc/server.properties"))
	write(t, root, "/srv/mc2/server.properties", "motd=Zwei\n")
	fl := &fakeList{list: []dockerapi.OpsContainer{
		{ID: "a", Names: []string{"/mc1"}, Image: "custom", State: "running", Mounts: []dockerapi.MountPoint{mount("/srv/mc", "/data")}},
		{ID: "b", Names: []string{"/mc2"}, Image: "custom", State: "running", Mounts: []dockerapi.MountPoint{mount("/srv/mc2", "/data")}},
	}}
	s := &Service{Docker: fl, FS: hostfs.New(root)}
	s.Scan(context.Background())
	if l := s.List(); len(l) != 1 || l[0].ID != "mc2" || l[0].Name != "Zwei" {
		t.Errorf("Liste: %+v", l)
	}
}

// Der nachgebaute Beispielserver (fixture.Beispiel): ein laufender
// Minecraft-Server im Calagopus-Panel und ein gestoppter ohne Container;
// mc-router (Netz des Hosts) und die übrigen Projekte sind keine
// Spielserver.
func TestErkennungNachgebauterServer(t *testing.T) {
	fx := fixture.Beispiel(t, 0)
	s := &Service{Docker: fx.Docker, FS: hostfs.New(fx.Root)}
	s.Scan(context.Background())
	l := s.List()
	if len(l) != 2 {
		t.Fatalf("Spielserver: %+v", l)
	}
	if l[0].ID != fixture.GameServer || l[0].Panel != "calagopus-web" || l[0].State != StateRunning {
		t.Errorf("laufender Server: %+v", l[0])
	}
	if l[1].ID != fixture.IdleServer || l[1].State != StateOff || l[1].Name != "Alt" || l[1].Container != "" {
		t.Errorf("gestoppter Server: %+v", l[1])
	}
}

// Ein Minecraft-Proxy im Panel ist kein Spielserver; er bleibt
// ein verwalteter Server.
func TestProxyIstKeinSpielserver(t *testing.T) {
	root := t.TempDir()
	const uVel, uBun, uAns, uMC = "66666666-6666-4666-8666-666666666666", "77777777-7777-4777-8777-777777777777", "88888888-8888-4888-8888-888888888888", "99999999-9999-4999-8999-999999999999"
	write(t, root, vol+uVel+"/velocity.toml", "bind = \"0.0.0.0:25577\"\n")
	write(t, root, vol+uBun+"/config.yml", "listeners:\n- host: 0.0.0.0:25577\nservers:\n  lobby:\n    address: localhost:25566\n")
	write(t, root, vol+uAns+"/start.sh", "#!/bin/sh\n")
	write(t, root, vol+uMC+"/server.properties", "motd=Lobby\n")
	write(t, root, vol+uMC+"/config.yml", "listeners: servers:\n") // ein Plugin-Rest: zählt nicht
	const velPort, ansPort, mcPort = 25577, 25578, 25566
	wings := dockerapi.OpsContainer{ID: "w", Names: []string{"/wings"}, Image: "ghcr.io/pterodactyl/wings:latest", State: "running",
		Mounts: []dockerapi.MountPoint{mount("/var/run/docker.sock", "/var/run/docker.sock"), mount("/var/lib/pterodactyl", "/var/lib/pterodactyl")}}
	srv := func(id, uuid string, port int) dockerapi.OpsContainer {
		return dockerapi.OpsContainer{ID: id, Names: []string{"/" + uuid}, Image: "ghcr.io/pterodactyl/yolks:java_21", State: "running",
			Ports:  []dockerapi.ContainerPort{{IP: "127.0.0.1", PrivatePort: 25565, PublicPort: port, Type: "tcp"}},
			Mounts: []dockerapi.MountPoint{mount(vol+uuid, "/home/container")}}
	}
	fl := &fakeList{list: []dockerapi.OpsContainer{wings, srv("a", uVel, velPort), srv("b", uBun, velPort), srv("c", uAns, ansPort), srv("d", uMC, mcPort)}}
	s := &Service{Docker: fl, FS: hostfs.New(root)}
	s.Scan(context.Background())
	l := s.List()
	if len(l) != 1 || l[0].ID != uMC {
		t.Errorf("nur die Lobby ist ein Spielserver: %+v", l)
	}
}
