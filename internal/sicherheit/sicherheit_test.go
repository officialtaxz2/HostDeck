package sicherheit

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/altserver"
	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
)

type fakeDocker struct{ list []dockerapi.OpsContainer }

func (f fakeDocker) List(context.Context) ([]dockerapi.OpsContainer, error) { return f.list, nil }

type fakeRun struct {
	mu    sync.Mutex
	own   bool
	calls []string
	bad   bool // nft -c lehnt ab
}

func (f *fakeRun) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, c)
	switch {
	case c == "sshd -T":
		return []byte("port 22\npasswordauthentication yes\npermitrootlogin yes\nx11forwarding no\n"), nil
	case c == "systemctl is-active firewalld":
		return []byte("inactive\n"), errors.New("exit 3")
	case c == "nft --version":
		return []byte("nftables v1.0.9\n"), nil
	case c == "nft list table inet "+Table:
		if f.own {
			return nil, nil
		}
		return nil, errors.New("No such file or directory")
	case strings.HasPrefix(c, "nft -c -f"):
		if f.bad {
			return []byte("Error: syntax"), errors.New("exit 1")
		}
		return nil, nil
	case strings.HasPrefix(c, "nft -f"):
		f.own = true
		return nil, nil
	case c == "nft delete table inet "+Table:
		f.own = false
		return nil, nil
	}
	return nil, nil
}

func (f *fakeRun) took() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := strings.Join(f.calls, "\n")
	f.calls = nil
	return s
}

// procNet schreibt eine Zeile wie in /proc/net/tcp.
func procLine(local, remote, state, inode string) string {
	return "   0: " + local + " " + remote + " " + state + " 00000000:00000000 00:00000000 00000000     0        0 " + inode + " 1 0000000000000000 100 0 0 10 0\n"
}

const head = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"

func newRig(t *testing.T) (*Service, *fakeRun) {
	t.Helper()
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(proc, "net"), 0o755))
	zero4, zero6 := "00000000:0000", "00000000000000000000000000000000:0000"
	write := func(name, body string) {
		must(os.WriteFile(filepath.Join(proc, "net", name), []byte(head+body), 0o644))
	}
	write("tcp", procLine("00000000:0016", zero4, "0A", "1001")+ // 0.0.0.0:22 sshd
		procLine("00000000:1538", zero4, "0A", "3001")+ // 0.0.0.0:5432 postgres
		procLine("00000000:0BBD", zero4, "0A", "4001")+ // 0.0.0.0:3005 node
		procLine("0100007F:18EB", zero4, "0A", "6001")+ // 127.0.0.1:6379 redis, nur lokal
		procLine("00000000:0DE0", zero4, "0A", "5001")+ // 0.0.0.0:3552 docker-proxy
		procLine("0100007F:0016", "0100007F:A000", "01", "7001")) // verbunden, kein LISTEN
	write("tcp6", procLine("00000000000000000000000000000000:01BB", zero6, "0A", "2001")+ // :::443 caddy
		procLine("00000000000000000000000000000000:0050", zero6, "0A", "2002")) // :::80 caddy
	write("udp6", procLine("00000000000000000000000000000000:01BB", zero6, "07", "2003")) // :::443/udp caddy
	write("udp", procLine("00000000:A1B2", "08080808:0035", "01", "8001"))                // DNS-Anfrage, verbunden
	proc1 := func(pid, comm, cgroup string, inodes ...string) {
		dir := filepath.Join(proc, pid)
		must(os.MkdirAll(filepath.Join(dir, "fd"), 0o755))
		must(os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644))
		must(os.WriteFile(filepath.Join(dir, "cgroup"), []byte(cgroup), 0o644))
		for i, in := range inodes {
			must(os.Symlink("socket:["+in+"]", filepath.Join(dir, "fd", string(rune('3'+i)))))
		}
	}
	proc1("100", "sshd", "0::/system.slice/ssh.service\n", "1001")
	proc1("200", "caddy", "0::/system.slice/caddy.service\n", "2001", "2002", "2003")
	proc1("300", "postgres", "0::/system.slice/postgresql.service\n", "3001")
	proc1("400", "node", "0::/user.slice\n", "4001")
	proc1("500", "docker-proxy", "0::/system.slice/docker.service\n", "5001")

	sock := dockerapi.MountPoint{Type: "bind", Source: "/var/run/docker.sock", Destination: "/var/run/docker.sock"}
	list := []dockerapi.OpsContainer{
		{ID: strings.Repeat("a", 64), Names: []string{"/arcane"}, State: "running", Labels: map[string]string{"com.docker.compose.project": "arcane"},
			Mounts: []dockerapi.MountPoint{sock}, Ports: []dockerapi.ContainerPort{{IP: "0.0.0.0", PrivatePort: 3552, PublicPort: 3552, Type: "tcp"}, {IP: "::", PrivatePort: 3552, PublicPort: 3552, Type: "tcp"}}},
		{ID: strings.Repeat("b", 64), Names: []string{"/neko-neko-1"}, State: "running", Labels: map[string]string{"com.docker.compose.project": "neko"},
			Ports: []dockerapi.ContainerPort{{IP: "0.0.0.0", PrivatePort: 51000, PublicPort: 51000, Type: "udp"}, {IP: "0.0.0.0", PrivatePort: 51001, PublicPort: 51001, Type: "udp"}, {IP: "0.0.0.0", PrivatePort: 51002, PublicPort: 51002, Type: "udp"}, {IP: "127.0.0.1", PrivatePort: 8080, PublicPort: 8082, Type: "tcp"}}},
		{ID: strings.Repeat("c", 64), Names: []string{"/wings"}, State: "running",
			Mounts: []dockerapi.MountPoint{sock, {Type: "bind", Source: "/var/lib/pterodactyl", Destination: "/var/lib/pterodactyl"}},
			Ports:  []dockerapi.ContainerPort{{IP: "0.0.0.0", PrivatePort: 8080, PublicPort: 8080, Type: "tcp"}, {IP: "0.0.0.0", PrivatePort: 2022, PublicPort: 2022, Type: "tcp"}}},
		{ID: strings.Repeat("d", 64), Names: []string{"/0f5e8c1a-1111-2222-3333-444455556666"}, State: "running",
			Mounts: []dockerapi.MountPoint{{Type: "bind", Source: "/var/lib/pterodactyl/volumes/0f5e", Destination: "/home/container"}},
			Ports:  []dockerapi.ContainerPort{{IP: "0.0.0.0", PrivatePort: 25565, PublicPort: 25565, Type: "tcp"}}},
	}
	fr := &fakeRun{}
	data := filepath.Join(root, "var")
	s := &Service{
		Docker:    fakeDocker{list},
		Run:       fr.run,
		Proxied:   func(context.Context) map[int][]string { return map[int][]string{3005: {"app.example.org"}} },
		Proc:      proc,
		Etc:       filepath.Join(root, "etc"),
		RuleFile:  filepath.Join(root, "etc", "hostdeck", "schutz.nft"),
		UnitFile:  filepath.Join(root, "etc", "systemd", "system", "hostdeck-schutz.service"),
		StateFile: filepath.Join(data, "schutz.json"),
		Window:    time.Hour,
	}
	return s, fr
}

func byKey(ov Overview) map[string]Port {
	m := map[string]Port{}
	for _, p := range ov.Ports {
		m[p.Key] = p
	}
	return m
}

func TestUebersicht(t *testing.T) {
	s, _ := newRig(t)
	ov := s.Overview(context.Background())
	if ov.Problem != "" {
		t.Fatal(ov.Problem)
	}
	m := byKey(ov)
	type want struct {
		owner, kind string
		locked      bool
		suggest     bool
		risk        string
	}
	for k, w := range map[string]want{
		"tcp/22":          {"sshd", "ssh", true, false, ""},
		"tcp/80":          {"caddy", "web", true, false, ""},
		"tcp/443":         {"caddy", "web", true, false, ""},
		"udp/443":         {"caddy", "web", true, false, ""},
		"tcp/5432":        {"postgres", "prozess", false, true, "hoch"},
		"tcp/3005":        {"node", "prozess", false, true, "mittel"},
		"tcp/3552":        {"arcane", "container", false, true, "hoch"},
		"udp/51000-51002": {"neko-neko-1", "container", false, false, ""},
		"tcp/8080":        {"wings", "panel", true, false, ""},
		"tcp/2022":        {"wings", "panel", true, false, ""},
		"tcp/25565":       {"0f5e8c1a-1111-2222-3333-444455556666", "panel", true, false, ""},
	} {
		p, ok := m[k]
		if !ok {
			t.Errorf("%s fehlt", k)
			continue
		}
		if p.Owner != w.owner || p.Kind != w.kind || (p.Locked != "") != w.locked || p.Suggest != w.suggest || p.Risk != w.risk {
			t.Errorf("%s: %+v, erwartet %+v", k, p, w)
		}
	}
	if len(m) != 11 {
		t.Errorf("Ports: %v", ov.Ports)
	}
	if d := m["tcp/3005"].Domains; len(d) != 1 || d[0] != "app.example.org" {
		t.Errorf("Domains: %v", d)
	}
	if len(ov.Docker) != 2 || ov.Docker[0].Name != "arcane" || len(ov.Docker[0].Public) != 1 || ov.Docker[1].Name != "wings" || !ov.Docker[1].Panel {
		t.Errorf("Docker-Zugriff: %+v", ov.Docker)
	}
	if !ov.SSH.Known || ov.SSH.Password != "yes" || len(ov.SSH.Advice) != 2 {
		t.Errorf("SSH: %+v", ov.SSH)
	}
	if ov.Firewall.Ufw || ov.Firewall.Firewalld || !ov.Firewall.Nft || ov.Firewall.Own {
		t.Errorf("Firewall: %+v", ov.Firewall)
	}
}

func TestSchliessenBestaetigenOeffnen(t *testing.T) {
	s, fr := newRig(t)
	ctx := context.Background()
	if _, err := s.Plan(ctx, Change{Close: []string{"tcp/22"}}); !errors.Is(err, ErrLocked) {
		t.Errorf("SSH ließe sich schließen: %v", err)
	}
	if _, err := s.Plan(ctx, Change{Close: []string{"tcp/25565"}}); !errors.Is(err, ErrLocked) {
		t.Errorf("Spielserver ließe sich schließen: %v", err)
	}
	if _, err := s.Plan(ctx, Change{Close: []string{"tcp/9999"}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unbekannt: %v", err)
	}
	if _, err := s.Plan(ctx, Change{}); !errors.Is(err, ErrNothing) {
		t.Errorf("leer: %v", err)
	}
	ch := Change{Close: []string{"tcp/3552", "tcp/5432", "udp/51000-51002"}}
	p, err := s.Plan(ctx, ch)
	if err != nil || p.Problem != "" {
		t.Fatal(err, p.Problem)
	}
	for _, w := range []string{"tcp dport { 3552, 5432 } drop", "udp dport { 51000-51002 } drop", "meta l4proto tcp ct original proto-dst { 3552, 5432 } drop", "iif \"lo\" accept"} {
		if !strings.Contains(p.Rules, w) {
			t.Errorf("Regeln ohne %q:\n%s", w, p.Rules)
		}
	}
	fr.took()
	if _, err := s.Apply(ctx, ch, "falsch"); !errors.Is(err, ErrChanged) {
		t.Fatalf("falscher Stand: %v", err)
	}
	msg, err := s.Apply(ctx, ch, p.Stand)
	if err != nil || !strings.Contains(msg, "Bestätigung") {
		t.Fatalf("%q %v", msg, err)
	}
	calls := fr.took()
	if !strings.Contains(calls, "nft -c -f") || !strings.Contains(calls, "nft -f "+s.RuleFile) || strings.Contains(calls, "systemctl enable") {
		t.Errorf("Aufrufe: %s", calls)
	}
	ov := s.Overview(ctx)
	if ov.Pending == nil || !ov.Firewall.Own {
		t.Fatalf("wartet nicht: %+v", ov)
	}
	if !byKey(ov)["tcp/3552"].Closed || byKey(ov)["tcp/3552"].Suggest {
		t.Errorf("3552 nicht als geschlossen gezeigt: %+v", byKey(ov)["tcp/3552"])
	}
	if p, _ := s.Plan(ctx, Change{Close: []string{"tcp/3005"}}); !strings.Contains(p.Problem, "wartet") {
		t.Errorf("zweite Änderung während des Wartens: %+v", p)
	}
	msg, err = s.Keep(ctx)
	if err != nil || !strings.Contains(msg, "Neustart") {
		t.Fatalf("Bestätigen: %q %v", msg, err)
	}
	if !strings.Contains(fr.took(), "systemctl enable hostdeck-schutz.service") {
		t.Error("Dienst nicht eingeschaltet")
	}
	if b, err := os.ReadFile(s.UnitFile); err != nil || !strings.Contains(string(b), "-f "+s.RuleFile) {
		t.Errorf("Dienst-Datei: %s %v", b, err)
	}

	// Öffnen braucht keine Wartezeit.
	ch = Change{Open: []string{"tcp/5432"}}
	p, err = s.Plan(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if msg, err = s.Apply(ctx, ch, p.Stand); err != nil || s.pendingInfo() != nil {
		t.Fatalf("öffnen: %q %v", msg, err)
	}
	if b, _ := os.ReadFile(s.RuleFile); strings.Contains(string(b), "5432") || !strings.Contains(string(b), "3552") {
		t.Errorf("Regeln nach dem Öffnen:\n%s", b)
	}

	// Alles öffnen: Tabelle, Datei und Dienst verschwinden.
	ch = Change{Open: []string{"tcp/3552", "udp/51000-51002"}}
	p, _ = s.Plan(ctx, ch)
	if _, err := s.Apply(ctx, ch, p.Stand); err != nil {
		t.Fatal(err)
	}
	if fr.own {
		t.Error("Tabelle noch geladen")
	}
	for _, f := range []string{s.RuleFile, s.UnitFile} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s noch da", f)
		}
	}
	if len(s.closed()) != 0 {
		t.Errorf("Liste: %v", s.closed())
	}
}

func TestOhneBestaetigungZurueck(t *testing.T) {
	s, fr := newRig(t)
	ctx := context.Background()
	s.Window = 50 * time.Millisecond
	done := make(chan string, 1)
	s.OnExpire = func(text string) { done <- text }
	ch := Change{Close: []string{"tcp/3005"}}
	p, _ := s.Plan(ctx, ch)
	if _, err := s.Apply(ctx, ch, p.Stand); err != nil {
		t.Fatal(err)
	}
	select {
	case text := <-done:
		if !strings.Contains(text, "zurückgenommen") {
			t.Errorf("Protokoll: %s", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nicht zurückgenommen")
	}
	if fr.own || len(s.closed()) != 0 || s.pendingInfo() != nil {
		t.Errorf("nicht wie vorher: own=%v closed=%v", fr.own, s.closed())
	}
	if _, err := os.Stat(s.RuleFile); !os.IsNotExist(err) {
		t.Error("Regeldatei noch da")
	}
}

func TestNftLehntAbNichtsPassiert(t *testing.T) {
	s, fr := newRig(t)
	ctx := context.Background()
	fr.bad = true
	ch := Change{Close: []string{"tcp/3005"}}
	p, _ := s.Plan(ctx, ch)
	if _, err := s.Apply(ctx, ch, p.Stand); err == nil {
		t.Fatal("kein Fehler")
	}
	if fr.own || len(s.closed()) != 0 {
		t.Error("trotzdem geschlossen")
	}
}

// TestRegelnMitEchtemNft prüft die erzeugten Regeln mit nft selbst, wo es
// das gibt (als root).
func TestRegelnMitEchtemNft(t *testing.T) {
	nft, err := exec.LookPath("nft")
	if err != nil || os.Geteuid() != 0 {
		t.Skip("nft oder root fehlt")
	}
	f := filepath.Join(t.TempDir(), "r.nft")
	text := rules([]Closed{{Proto: "tcp", From: 3552, To: 3552}, {Proto: "tcp", From: 5432, To: 5432}, {Proto: "udp", From: 51000, To: 51100}})
	if err := os.WriteFile(f, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(nft, "-c", "-f", f).CombinedOutput(); err != nil {
		t.Fatalf("nft lehnt ab: %v %s\n%s", err, out, text)
	}
}

// Umzug: Was HostDeck auf dem alten Server geschlossen hatte, schlägt
// es auf dem neuen wieder vor, sobald der Port dort offen ist.
func TestWieAufDemAltenServer(t *testing.T) {
	// Der alte Server schreibt schutz.json; altserver liest dasselbe Format.
	old, _ := newRig(t)
	if err := old.saveClosed([]Closed{{Proto: "tcp", From: 3552, To: 3552, Owner: "arcane"}, {Proto: "udp", From: 51000, To: 51100}, {Proto: "tcp", From: 22, To: 22}, {Proto: "tcp", From: 7777, To: 7777}}); err != nil {
		t.Fatal(err)
	}
	info := altserver.Build(filepath.Dir(old.StateFile), "vps1")
	if len(info.Closed) != 4 || info.From != "vps1" {
		t.Fatalf("vom alten Server: %+v", info)
	}

	s, _ := newRig(t)
	s.Old = func() *altserver.Info { return &info }
	m := byKey(s.Overview(context.Background()))
	if p := m["tcp/3552"]; !p.WasClosed || !p.Suggest || !strings.HasPrefix(p.Advice, "Hattest du auf dem alten Server (vps1) geschlossen.") {
		t.Errorf("arcane: %+v", p)
	}
	if p := m["udp/51000-51002"]; !p.WasClosed || !p.Suggest || p.Risk != "mittel" {
		t.Errorf("Bereich im alten Bereich: %+v", p)
	}
	if p := m["tcp/22"]; p.WasClosed || p.Suggest {
		t.Errorf("SSH schlägt HostDeck nie vor: %+v", p)
	}
	if _, ok := m["tcp/7777"]; ok {
		t.Errorf("Port, der hier nicht lauscht, taucht auf: %+v", m["tcp/7777"])
	}
	if p := m["tcp/5432"]; p.WasClosed {
		t.Errorf("nicht vom alten Server: %+v", p)
	}
}

// Ohne offene Ports und ohne Docker ist die Liste leer, nicht null (sonst
// bleibt die Seite beim Laden stehen).
func TestUebersichtOhnePortsUndDocker(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "proc", "net"), 0o755)
	s := &Service{Run: (&fakeRun{}).run, Proc: filepath.Join(root, "proc"), Etc: filepath.Join(root, "etc"),
		StateFile: filepath.Join(root, "schutz.json")}
	b, err := json.Marshal(s.Overview(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"ports":[]`) || strings.Contains(string(b), `"problem"`) {
		t.Errorf("Übersicht: %s", b)
	}
}
