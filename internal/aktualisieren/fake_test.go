package aktualisieren

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
)

// fakeServer spielt Docker, Compose und Git für ein Projekt „shop“ mit
// einer selbst gebauten App, einer Datenbank, einem Redis und einem
// Aufräum-Job.
type fakeServer struct {
	mu      sync.Mutex
	ctrs    []*fakeCtr
	tags    map[string]string   // Referenz → Image-ID
	digests map[string][]string // Image-ID → RepoDigests
	created map[string]string   // Image-ID → Zeit
	remote  map[string]string   // Referenz → Image-ID, das ein pull bringt
	build   map[string]string   // Referenz → Image-ID, das ein build bringt
	broken  map[string]bool     // Image-IDs, mit denen ein Container sofort endet
	calls   []string
	n       int
	git     fakeGit
	// gitRefused: Der Git-Ordner ist nicht sicher (GitCheck lehnt ab).
	gitRefused string
	failPull   bool
}

type fakeCtr struct {
	id, name, project, service, ref, image string
	running, health, job                   bool
	restarts                               int
}

type fakeGit struct {
	head, remoteHead string
	dirty            bool
	known            bool // Commit der Gegenstelle lokal bekannt
	diverged         bool // pull --ff-only scheitert
	time             int64
}

const (
	shopDir = "/opt/shop"
	idOld   = "sha256:" + "a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0"
	idNew   = "sha256:" + "b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1"
	idDB    = "sha256:" + "c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2c2"
	idDB2   = "sha256:" + "d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3"
	idRedis = "sha256:" + "e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4e4"
	idAlp   = "sha256:" + "f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5f5"
	commit1 = "1111111111111111111111111111111111111111"
	commit2 = "2222222222222222222222222222222222222222"
	dgRedis = "sha256:" + "9999999999999999999999999999999999999999999999999999999999999999"
	dgDB    = "sha256:" + "8888888888888888888888888888888888888888888888888888888888888888"
)

func newFakeServer() *fakeServer {
	f := &fakeServer{
		tags:    map[string]string{"shop-app": idOld, "postgres:17": idDB, "redis:7": idRedis, "alpine:latest": idAlp},
		digests: map[string][]string{idDB: {"postgres@" + dgDB}, idRedis: {"redis@" + dgRedis}},
		created: map[string]string{idOld: "2026-11-01T10:00:00Z"},
		remote:  map[string]string{},
		build:   map[string]string{},
		broken:  map[string]bool{},
		git:     fakeGit{head: commit1, remoteHead: commit1, known: true, time: 1761984000}, // 01.11.2025
	}
	f.ctrs = []*fakeCtr{
		{id: "c-app", name: "shop-app-1", project: "shop", service: "app", ref: "shop-app", image: idOld, running: true, health: true},
		{id: "c-db", name: "shop-db-1", project: "shop", service: "db", ref: "postgres:17", image: idDB, running: true},
		{id: "c-redis", name: "shop-redis-1", project: "shop", service: "redis", ref: "redis:7", image: idRedis, running: true},
		{id: "c-clean", name: "shop-cleanup-1", project: "shop", service: "cleanup", ref: "alpine:latest", image: idAlp, job: true},
	}
	return f
}

func (f *fakeServer) List(context.Context) ([]dockerapi.OpsContainer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []dockerapi.OpsContainer
	for _, c := range f.ctrs {
		st := "exited"
		if c.running {
			st = "running"
		}
		out = append(out, dockerapi.OpsContainer{ID: c.id, Names: []string{"/" + c.name}, Image: c.ref, ImageID: c.image, State: st,
			Labels: map[string]string{labelProject: c.project, labelService: c.service, labelWorkingDir: shopDir, labelFiles: shopDir + "/compose.yaml"}})
	}
	return out, nil
}

func (f *fakeServer) find(id string) *fakeCtr {
	for _, c := range f.ctrs {
		if c.id == id {
			return c
		}
	}
	return nil
}

func (f *fakeServer) Inspect(_ context.Context, id string) (dockerapi.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.find(id)
	if c == nil {
		return dockerapi.Container{}, dockerapi.ErrNoSuchContainer
	}
	v := dockerapi.Container{ID: c.id, Name: "/" + c.name, Image: c.image, RestartCount: c.restarts}
	v.Config.Image = c.ref
	v.State.Running = c.running
	v.State.Status = map[bool]string{true: "running", false: "exited"}[c.running]
	if c.job {
		v.HostConfig.RestartPolicy.Name = "no"
	} else {
		v.HostConfig.RestartPolicy.Name = "unless-stopped"
	}
	if !c.running && !c.job {
		v.State.ExitCode = 1
	}
	if c.health {
		v.State.Health = &struct {
			Status string `json:"Status"`
		}{Status: "healthy"}
	}
	return v, nil
}

func (f *fakeServer) ImageInspect(_ context.Context, id string) (dockerapi.ImageInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return dockerapi.ImageInfo{ID: id, RepoDigests: f.digests[id], Created: f.created[id]}, nil
}

// Run spielt docker und git.
func (f *fakeServer) Run(_ context.Context, dir string, out io.Writer, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if name == "git" {
		return f.runGit(args)
	}
	f.calls = append(f.calls, strings.Join(args, " "))
	if len(args) > 0 && args[0] == "compose" {
		i := 1
		for i < len(args) && strings.HasPrefix(args[i], "--") {
			i += 2
		}
		return f.compose(args[i], args[i+1:], out)
	}
	switch {
	case len(args) == 3 && args[0] == "tag":
		id := f.tags[args[1]]
		if strings.HasPrefix(args[1], "sha256:") {
			id = args[1]
		}
		if id == "" {
			return nil, &RunError{Cmd: "docker tag", Msg: "No such image: " + args[1], Code: 1}
		}
		f.tags[args[2]] = id
		return nil, nil
	case len(args) == 5 && args[0] == "image" && args[1] == "inspect":
		id := f.tags[args[4]]
		if id == "" {
			return nil, &RunError{Cmd: "docker image", Msg: "No such image", Code: 1}
		}
		return []byte(id + "\n"), nil
	}
	return nil, fmt.Errorf("unerwartet: docker %v", args)
}

func (f *fakeServer) compose(cmd string, rest []string, out io.Writer) ([]byte, error) {
	var svcs []string
	for _, a := range rest {
		if !strings.HasPrefix(a, "-") && a != "never" && a != "json" {
			svcs = append(svcs, a)
		}
	}
	ref := func(svc string) string {
		for _, c := range f.ctrs {
			if c.service == svc {
				return c.ref
			}
		}
		return ""
	}
	switch cmd {
	case "config":
		b, _ := json.Marshal(map[string]any{"services": map[string]any{
			"app": map[string]any{"build": map[string]any{"context": shopDir + "/app"}, "environment": map[string]string{"SECRET": "geheim"}},
			"db":  map[string]any{"image": "postgres:17"}, "redis": map[string]any{"image": "redis:7"}, "cleanup": map[string]any{"image": "alpine:latest"},
		}})
		return b, nil
	case "pull":
		if f.failPull {
			return nil, &RunError{Cmd: "docker compose", Msg: "pull access denied", Code: 1}
		}
		for _, s := range svcs {
			if id := f.remote[ref(s)]; id != "" {
				f.tags[ref(s)] = id
			}
			fmt.Fprintf(out, " %s Pulled\n", s)
		}
	case "build":
		for _, s := range svcs {
			if id := f.build[ref(s)]; id != "" {
				f.tags[ref(s)] = id
			}
		}
	case "up":
		for _, s := range svcs {
			for _, c := range f.ctrs {
				if c.service != s {
					continue
				}
				f.n++
				c.id = fmt.Sprintf("c-%s-%d", s, f.n)
				c.image = f.tags[c.ref]
				c.running = !f.broken[c.image]
				c.job = false
			}
		}
	default:
		return nil, fmt.Errorf("unerwartet: compose %s", cmd)
	}
	return nil, nil
}

func (f *fakeServer) runGit(args []string) ([]byte, error) {
	// -c … -c … -C dir abschneiden
	for len(args) > 1 && (args[0] == "-c" || args[0] == "-C") {
		args = args[2:]
	}
	g := &f.git
	f.calls = append(f.calls, "git "+strings.Join(args, " "))
	switch strings.Join(args, " ") {
	case "rev-parse --show-toplevel":
		return []byte(shopDir + "\n"), nil
	case "rev-parse HEAD":
		return []byte(g.head + "\n"), nil
	case "log -1 --format=%ct HEAD":
		return []byte(fmt.Sprint(g.time) + "\n"), nil
	case "status --porcelain --untracked-files=no":
		if g.dirty {
			return []byte(" M app/main.go\n"), nil
		}
		return nil, nil
	case "symbolic-ref --short -q HEAD":
		return []byte("main\n"), nil
	case "config --get branch.main.remote":
		return []byte("origin\n"), nil
	case "config --get branch.main.merge":
		return []byte("refs/heads/main\n"), nil
	case "ls-remote -- origin refs/heads/main":
		return []byte(g.remoteHead + "\trefs/heads/main\n"), nil
	case "pull --ff-only --no-rebase --quiet -- origin main":
		if g.diverged {
			return nil, &RunError{Cmd: "git pull", Msg: "fatal: Not possible to fast-forward, aborting.", Code: 128}
		}
		g.head = g.remoteHead
		return nil, nil
	}
	if len(args) == 4 && args[0] == "merge-base" {
		if !g.known {
			return nil, &RunError{Cmd: "git merge-base", Msg: "fatal: Not a valid commit name", Code: 128}
		}
		if args[2] == g.head {
			return nil, nil
		}
		return nil, &RunError{Cmd: "git merge-base", Code: 1}
	}
	if len(args) == 4 && args[0] == "reset" && args[1] == "--keep" {
		g.head = args[3]
		return nil, nil
	}
	return nil, fmt.Errorf("unerwartet: git %v", args)
}

func (f *fakeServer) ctr(service string) fakeCtr {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.ctrs {
		if c.service == service {
			return *c
		}
	}
	return fakeCtr{}
}

func (f *fakeServer) called(sub string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}
