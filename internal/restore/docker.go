package restore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Docker ist, was restore von Docker braucht. Die Umsetzung ruft das
// docker-Programm auf, weil Compose ohnehin dessen Plugin braucht.
type Docker interface {
	Version(ctx context.Context) (engine, compose string, err error)
	ComposeCreate(ctx context.Context, g Group) error
	// ComposeUp startet die Dienste samt allem, wovon sie abhängen; mit
	// alone nur genau diese.
	ComposeUp(ctx context.Context, g Group, services []string, alone bool) error
	// Load lädt ein Image und liefert die IDs, die docker load meldet. Mit
	// dem containerd-Speicher (Standard ab Docker 29 auf frischen Servern)
	// weicht sie von der ID auf dem alten Server ab.
	Load(ctx context.Context, r io.Reader) ([]string, error)
	Tag(ctx context.Context, id, ref string) error
	ImageExists(ctx context.Context, ref string) bool
	// ImageID ist die ID des Images unter diesem Namen; leer: gibt es nicht.
	ImageID(ctx context.Context, ref string) string
	NetworkExists(ctx context.Context, name string) bool
	NetworkCreate(ctx context.Context, n Network) error
	VolumeCreate(ctx context.Context, name string) error
	// VolumeMountpoint liefert den Ordner eines Volumes; ok ist false,
	// wenn es das Volume nicht gibt.
	VolumeMountpoint(ctx context.Context, name string) (dir string, ok bool, err error)
	// EnvSet liefert die Namen der Variablen eines Containers, die einen
	// Wert haben. Die Werte verlassen Docker dabei nicht.
	EnvSet(ctx context.Context, id string) ([]string, error)
	// ServiceContainer findet den Container eines Compose-Dienstes.
	ServiceContainer(ctx context.Context, project, service string) (string, error)
	// MountSource liefert den Host-Ordner, der im Container unter dest hängt.
	MountSource(ctx context.Context, id, dest string) (string, error)
	Running(ctx context.Context, id string) (bool, error)
	// VolumeUsers sind die laufenden Container, die ein Volume nutzen.
	VolumeUsers(ctx context.Context, name string) ([]string, error)
	// Stop hält Container an, damit nichts in Dateien schreibt, die HostDeck
	// gerade zurückspielt.
	Stop(ctx context.Context, ids []string) error
	Exec(ctx context.Context, id, script string, stdin io.Reader, stdout, stderr io.Writer) (int, error)
	ProjectContainers(ctx context.Context, project string) ([]ContainerState, error)
}

// ContainerState ist ein Container eines Projekts.
type ContainerState struct {
	ID      string
	Name    string
	Service string
	Running bool
	State   string
}

// CLI ruft docker auf.
type CLI struct {
	Bin string // Standard: docker
	Log io.Writer
}

func (c *CLI) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	return "docker"
}

func (c *CLI) run(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	cmd.Stdin = stdin
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 600 {
			msg = "…" + msg[len(msg)-600:]
		}
		if msg == "" {
			msg = err.Error()
		}
		return out, fmt.Errorf("docker %s: %s", args[0], msg)
	}
	return out, nil
}

// runLog behält die Ausgabe für Fehlermeldungen und zeigt nur, was lange
// dauern kann: das Herunterladen von Images.
func (c *CLI) runLog(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	var tailb tailBuf
	w := io.Writer(&tailb)
	if c.Log != nil {
		w = io.MultiWriter(&tailb, &pullLines{w: c.Log, seen: map[string]bool{}})
	}
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker %s: %v: %s", strings.Join(args[:min(2, len(args))], " "), err, oneLine(tailb.String()))
	}
	return nil
}

func (c *CLI) Version(ctx context.Context) (string, string, error) {
	out, err := c.run(ctx, nil, "version", "--format", "{{.Server.Version}}")
	if err != nil {
		return "", "", err
	}
	engine := strings.TrimSpace(string(out))
	out, err = c.run(ctx, nil, "compose", "version", "--short")
	if err != nil {
		return engine, "", errors.New("das Compose-Plugin von Docker fehlt (Paket docker-compose-plugin)")
	}
	return engine, strings.TrimSpace(string(out)), nil
}

func composeArgs(g Group, rest ...string) []string {
	args := []string{"compose", "--project-name", g.Name, "--project-directory", g.Dir}
	for _, f := range g.Files {
		args = append(args, "--file", f)
	}
	for _, f := range g.EnvFiles {
		if _, err := os.Stat(f); err == nil {
			args = append(args, "--env-file", f)
		}
	}
	return append(args, rest...)
}

// EnvSet fragt Docker nur nach Name und Länge jeder Variablen; der Wert
// selbst kommt nicht bei HostDeck an.
func (c *CLI) EnvSet(ctx context.Context, id string) ([]string, error) {
	out, err := c.run(ctx, nil, "inspect", "--format", `{{range .Config.Env}}{{index (split . "=") 0}} {{len .}}{{println}}{{end}}`, id)
	if err != nil {
		return nil, err
	}
	return parseEnvSet(string(out)), nil
}

// parseEnvSet liest Zeilen „NAME LÄNGE“, wobei die Länge für „NAME=Wert“
// steht: Ein Wert ist da, wenn sie größer ist als der Name plus „=“.
func parseEnvSet(out string) []string {
	var set []string
	for _, l := range strings.Split(out, "\n") {
		name, n, ok := strings.Cut(strings.TrimSpace(l), " ")
		if !ok || name == "" {
			continue
		}
		if total, err := strconv.Atoi(n); err == nil && total > len(name)+1 {
			set = append(set, name)
		}
	}
	return set
}

// ComposeCreate legt die Container an. Fehlende Images lädt Compose
// herunter oder baut sie, wenn das Projekt sie selbst baut.
func (c *CLI) ComposeCreate(ctx context.Context, g Group) error {
	return c.runLog(ctx, composeArgs(g, "create", "--pull", "missing", "--quiet-pull")...)
}

func (c *CLI) ComposeUp(ctx context.Context, g Group, services []string, alone bool) error {
	args := composeArgs(g, "up", "--detach", "--no-build", "--no-recreate", "--quiet-pull")
	if alone {
		args = append(args, "--no-deps")
	}
	return c.runLog(ctx, append(args, services...)...)
}

func (c *CLI) Load(ctx context.Context, r io.Reader) ([]string, error) {
	out, err := c.run(ctx, r, "load", "--quiet")
	return loadedIDs(out), err
}

// loadedIDs liest „Loaded image ID: sha256:…“ aus der Ausgabe von docker load.
func loadedIDs(out []byte) []string {
	var ids []string
	for _, l := range strings.Split(string(out), "\n") {
		if id, ok := strings.CutPrefix(strings.TrimSpace(l), "Loaded image ID: "); ok && id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func (c *CLI) Tag(ctx context.Context, id, ref string) error {
	_, err := c.run(ctx, nil, "tag", id, ref)
	return err
}

func (c *CLI) ImageExists(ctx context.Context, ref string) bool {
	_, err := c.run(ctx, nil, "image", "inspect", "--format", "{{.Id}}", ref)
	return err == nil
}

func (c *CLI) ImageID(ctx context.Context, ref string) string {
	out, err := c.run(ctx, nil, "image", "inspect", "--format", "{{.Id}}", ref)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (c *CLI) NetworkExists(ctx context.Context, name string) bool {
	_, err := c.run(ctx, nil, "network", "inspect", "--format", "{{.Id}}", name)
	return err == nil
}

func (c *CLI) NetworkCreate(ctx context.Context, n Network) error {
	args := []string{"network", "create"}
	if n.Driver != "" {
		args = append(args, "--driver", n.Driver)
	}
	_, err := c.run(ctx, nil, append(args, n.Name)...)
	return err
}

func (c *CLI) VolumeCreate(ctx context.Context, name string) error {
	_, err := c.run(ctx, nil, "volume", "create", name)
	return err
}

func (c *CLI) VolumeMountpoint(ctx context.Context, name string) (string, bool, error) {
	out, err := c.run(ctx, nil, "volume", "ls", "--quiet", "--filter", "name=^"+name+"$")
	if err != nil {
		return "", false, err
	}
	if !containsLine(out, name) {
		return "", false, nil
	}
	out, err = c.run(ctx, nil, "volume", "inspect", "--format", "{{.Mountpoint}}", name)
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(out)), true, nil
}

func containsLine(b []byte, s string) bool {
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == s {
			return true
		}
	}
	return false
}

func (c *CLI) ServiceContainer(ctx context.Context, project, service string) (string, error) {
	out, err := c.run(ctx, nil, "ps", "--all", "--quiet", "--no-trunc",
		"--filter", "label=com.docker.compose.project="+project,
		"--filter", "label=com.docker.compose.service="+service)
	if err != nil {
		return "", err
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return "", fmt.Errorf("kein Container für den Dienst %s/%s", project, service)
	}
	return ids[0], nil
}

func (c *CLI) MountSource(ctx context.Context, id, dest string) (string, error) {
	out, err := c.run(ctx, nil, "inspect", "--format", "{{json .Mounts}}", id)
	if err != nil {
		return "", err
	}
	var ms []struct {
		Source, Destination string
	}
	if err := json.Unmarshal(out, &ms); err != nil {
		return "", err
	}
	for _, m := range ms {
		if m.Destination == dest {
			return m.Source, nil
		}
	}
	return "", fmt.Errorf("im Container ist nichts unter %s eingebunden", dest)
}

func (c *CLI) Running(ctx context.Context, id string) (bool, error) {
	out, err := c.run(ctx, nil, "inspect", "--format", "{{.State.Running}}", id)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

func (c *CLI) VolumeUsers(ctx context.Context, name string) ([]string, error) {
	out, err := c.run(ctx, nil, "ps", "--quiet", "--no-trunc", "--filter", "volume="+name)
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

func (c *CLI) Stop(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := c.run(ctx, nil, append([]string{"stop", "--time", "60"}, ids...)...)
	return err
}

func (c *CLI) Exec(ctx context.Context, id, script string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	args := []string{"exec"}
	if stdin != nil {
		args = append(args, "--interactive")
	}
	args = append(args, id, "sh", "-c", script)
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

func (c *CLI) ProjectContainers(ctx context.Context, project string) ([]ContainerState, error) {
	out, err := c.run(ctx, nil, "ps", "--all", "--no-trunc", "--filter", "label=com.docker.compose.project="+project,
		"--format", `{{.ID}}|{{.Names}}|{{.Label "com.docker.compose.service"}}|{{.State}}`)
	if err != nil {
		return nil, err
	}
	var cs []ContainerState
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(l, "|")
		if len(f) != 4 {
			continue
		}
		cs = append(cs, ContainerState{ID: f[0], Name: f[1], Service: f[2], State: f[3], Running: f[3] == "running"})
	}
	return cs, nil
}

// pullLines gibt von der Ausgabe von docker compose nur die Zeilen zum
// Herunterladen weiter, jede einmal.
type pullLines struct {
	w    io.Writer
	part []byte
	seen map[string]bool
}

func (p *pullLines) Write(b []byte) (int, error) {
	p.part = append(p.part, b...)
	for {
		i := bytes.IndexAny(p.part, "\r\n")
		if i < 0 {
			break
		}
		l := strings.TrimSpace(string(p.part[:i]))
		p.part = p.part[i+1:]
		if strings.Contains(l, "Pull") && !p.seen[l] {
			p.seen[l] = true
			fmt.Fprintln(p.w, l)
		}
	}
	return len(b), nil
}

// tailBuf behält das Ende einer Ausgabe für Fehlermeldungen.
type tailBuf struct{ b []byte }

func (t *tailBuf) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > 2000 {
		t.b = append(t.b[:0], t.b[len(t.b)-2000:]...)
	}
	return len(p), nil
}

func (t *tailBuf) String() string { return strings.TrimSpace(string(t.b)) }

// SetRestart setzt die Neustart-Regel von Containern, etwa „no“.
func (c *CLI) SetRestart(ctx context.Context, ids []string, policy string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := c.run(ctx, nil, append([]string{"update", "--restart=" + policy}, ids...)...)
	return err
}

// ComposeDown entfernt die Container, Netze und Volumes eines Projekts.
// Es braucht nur den Projektnamen; Compose findet alles an den Etiketten.
func (c *CLI) ComposeDown(ctx context.Context, project string) error {
	cmd := exec.CommandContext(ctx, c.bin(), "compose", "--project-name", project, "down", "--volumes", "--remove-orphans", "--timeout", "60")
	cmd.Dir = "/" // keine Compose-Datei aus dem Arbeitsordner aufgreifen
	var tailb tailBuf
	cmd.Stdout, cmd.Stderr = &tailb, &tailb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose down: %v: %s", err, tailb.String())
	}
	return nil
}

// ProjectVolumes sind die Volumes mit dem Etikett eines Compose-Projekts.
func (c *CLI) ProjectVolumes(ctx context.Context, project string) ([]string, error) {
	out, err := c.run(ctx, nil, "volume", "ls", "--quiet", "--filter", "label=com.docker.compose.project="+project)
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// ProjectDir ist der Ordner eines Compose-Projekts laut Etikett seiner
// Container; leer, wenn unbekannt.
func (c *CLI) ProjectDir(ctx context.Context, project string) string {
	out, err := c.run(ctx, nil, "ps", "--all", "--filter", "label=com.docker.compose.project="+project,
		"--format", `{{.Label "com.docker.compose.project.working_dir"}}`)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// VolumeRemove löscht ein Volume; gibt es das nicht, ist das kein Fehler.
func (c *CLI) VolumeRemove(ctx context.Context, name string) error {
	if _, ok, err := c.VolumeMountpoint(ctx, name); err != nil || !ok {
		return err
	}
	_, err := c.run(ctx, nil, "volume", "rm", name)
	return err
}

// ImageRemove löscht ein Image; gibt es das nicht, ist das kein Fehler.
func (c *CLI) ImageRemove(ctx context.Context, ref string) error {
	if !c.ImageExists(ctx, ref) {
		return nil
	}
	_, err := c.run(ctx, nil, "image", "rm", ref)
	return err
}

// MountUsers sind alle Container (auch gestoppte) mit ihren Host-Ordnern,
// Name ohne führenden Schrägstrich.
func (c *CLI) MountUsers(ctx context.Context) (map[string][]string, error) {
	ids, err := c.run(ctx, nil, "ps", "--all", "--quiet", "--no-trunc")
	if err != nil {
		return nil, err
	}
	f := strings.Fields(string(ids))
	out := map[string][]string{}
	if len(f) == 0 {
		return out, nil
	}
	b, err := c.run(ctx, nil, append([]string{"inspect", "--format", `{{.Name}}{{range .Mounts}}|{{.Source}}{{end}}`}, f...)...)
	if err != nil {
		return nil, err
	}
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		parts := strings.Split(l, "|")
		name := strings.TrimPrefix(parts[0], "/")
		if name != "" {
			out[name] = parts[1:]
		}
	}
	return out, nil
}

// RemoveContainers entfernt Container samt anonymer Volumes.
func (c *CLI) RemoveContainers(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return nil
	}
	_, err := c.run(ctx, nil, append([]string{"rm", "--force", "--volumes"}, names...)...)
	return err
}

// oneLine macht aus der Ausgabe von Docker eine Zeile. Der Grund steht oft
// in der letzten Zeile („error from registry: …“); auf eigener Zeile fiel er
// aus der Fehlerliste des Lotsen heraus (Labor S4).
func oneLine(s string) string {
	var parts []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, " · ")
}
