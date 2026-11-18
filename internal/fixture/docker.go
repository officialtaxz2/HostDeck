// Package fixture baut für Tests einen Server nach, der vps1 ähnelt:
// Docker-Antworten, ein Dateibaum unter einem temporären Root, Git-Stände und
// die Ausgabe von caddy adapt. Alle Geheimnisse darin sind erfunden und
// beginnen mit „geheim-“, damit Tests prüfen können, dass sie nirgends
// auftauchen.
package fixture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/dockerapi"
)

// Docker ist eine nachgebaute Docker-API.
type Docker struct {
	Ctrs     []*Ctr
	Vols     []dockerapi.Volume
	VolSizes map[string]int64
	Imgs     []dockerapi.Image
	Nets     []dockerapi.Network

	// Containerd stellt den containerd-Image-Speicher nach, Standard ab
	// Docker 29: Jedes Image hat einen Digest gleich seiner ID, die Herkunft
	// steht im Image-Index (Manifests).
	Containerd bool
	// API überschreibt die gemeldete API-Version.
	API string
	// How sagt pro Image-ID, wie das Image im containerd-Modus auf den
	// Server kam: pull, build (BuildKit mit Herkunftsangabe), compose (nur
	// die Compose-Labels) oder load (keinerlei Angabe).
	How map[string]string
}

// Ctr ist ein Container im Aufbau.
type Ctr struct {
	C      dockerapi.Container
	SizeRw int64
}

func id(seed string) string {
	h := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(h[:])
}

// ImageID gibt die erfundene ID eines Images zurück.
func ImageID(ref string) string { return "sha256:" + id("image:"+ref) }

func (d *Docker) add(name, image string, labels map[string]string, running bool, gen int) *Ctr {
	c := &Ctr{}
	c.C.ID = id(fmt.Sprintf("container:%s:%d", name, gen))
	c.C.Name = "/" + name
	c.C.Image = ImageID(image)
	c.C.Config.Image = image
	c.C.Config.Labels = labels
	if c.C.Config.Labels == nil {
		c.C.Config.Labels = map[string]string{}
	}
	c.C.HostConfig.NetworkMode = "bridge"
	c.C.HostConfig.PortBindings = map[string][]dockerapi.PortBinding{}
	c.C.HostConfig.RestartPolicy.Name = "unless-stopped"
	c.C.NetworkSettings.Networks = map[string]struct{}{}
	c.C.State.Running = running
	c.C.State.Status = "running"
	if !running {
		c.C.State.Status = "exited"
	}
	d.Ctrs = append(d.Ctrs, c)
	return c
}

func compose(project, service, wd string) map[string]string {
	return map[string]string{
		"com.docker.compose.project":              project,
		"com.docker.compose.service":              service,
		"com.docker.compose.container-number":     "1",
		"com.docker.compose.project.working_dir":  wd,
		"com.docker.compose.project.config_files": wd + "/docker-compose.yml",
		"com.docker.compose.config-hash":          "geheim-hash-wird-nicht-gebraucht",
	}
}

func (c *Ctr) Bind(src, dst string, rw bool) *Ctr {
	c.C.Mounts = append(c.C.Mounts, dockerapi.MountPoint{Type: "bind", Source: src, Destination: dst, RW: rw})
	return c
}

func (c *Ctr) Vol(name, dst string) *Ctr {
	c.C.Mounts = append(c.C.Mounts, dockerapi.MountPoint{Type: "volume", Name: name, Source: "/var/lib/docker/volumes/" + name + "/_data", Destination: dst, RW: true})
	return c
}

func (c *Ctr) Port(hostIP string, hostPort, ctrPort int, proto string) *Ctr {
	k := fmt.Sprintf("%d/%s", ctrPort, proto)
	c.C.HostConfig.PortBindings[k] = append(c.C.HostConfig.PortBindings[k], dockerapi.PortBinding{HostIP: hostIP, HostPort: fmt.Sprint(hostPort)})
	return c
}

func (c *Ctr) Env(kv ...string) *Ctr {
	c.C.Config.Env = append(c.C.Config.Env, kv...)
	return c
}

func (c *Ctr) Net(names ...string) *Ctr {
	for _, n := range names {
		c.C.NetworkSettings.Networks[n] = struct{}{}
	}
	return c
}

func (c *Ctr) HostNet() *Ctr {
	c.C.HostConfig.NetworkMode = "host"
	c.C.NetworkSettings.Networks = map[string]struct{}{"host": {}}
	return c
}

func (c *Ctr) Exit(code int) *Ctr {
	c.C.State.ExitCode = code
	return c
}

func (c *Ctr) ImageID(id string) *Ctr {
	c.C.Image = id
	return c
}

func (c *Ctr) Size(n int64) *Ctr {
	c.SizeRw = n
	return c
}

// Find gibt den Container mit diesem Namen zurück.
func (d *Docker) Find(name string) *Ctr {
	for _, c := range d.Ctrs {
		if c.C.Name == "/"+name {
			return c
		}
	}
	return nil
}

func (d *Docker) Version(context.Context) (dockerapi.Version, error) {
	v := dockerapi.Version{Version: "28.5.1", APIVersion: "1.51"}
	if d.Containerd {
		v = dockerapi.Version{Version: "29.8.0", APIVersion: "1.56"}
	}
	if d.API != "" {
		v.APIVersion = d.API
	}
	return v, nil
}

func (d *Docker) Info(context.Context) (dockerapi.Info, error) {
	if d.Containerd {
		return dockerapi.Info{Name: "vps1", ServerVersion: "29.8.0", DockerRootDir: "/var/lib/docker", Driver: "overlayfs"}, nil
	}
	return dockerapi.Info{Name: "vps1", ServerVersion: "28.5.1", DockerRootDir: "/var/lib/docker", Driver: "overlay2"}, nil
}

func (d *Docker) Containers(_ context.Context, withSize bool) ([]dockerapi.ContainerSummary, error) {
	var out []dockerapi.ContainerSummary
	for _, c := range d.Ctrs {
		s := dockerapi.ContainerSummary{ID: c.C.ID, Names: []string{c.C.Name}, State: c.C.State.Status}
		if withSize {
			s.SizeRw = c.SizeRw
		}
		out = append(out, s)
	}
	return out, nil
}

func (d *Docker) Inspect(_ context.Context, cid string) (dockerapi.Container, error) {
	for _, c := range d.Ctrs {
		if c.C.ID == cid {
			// Wie bei der echten API geht die Antwort durch den
			// JSON-Decoder; dort kürzt dockerapi.EnvNames die
			// Umgebungsvariablen auf ihre Namen.
			b, err := json.Marshal(c.C)
			if err != nil {
				return dockerapi.Container{}, err
			}
			var out dockerapi.Container
			err = json.Unmarshal(b, &out)
			return out, err
		}
	}
	return dockerapi.Container{}, fmt.Errorf("no such container: %s", cid)
}

func (d *Docker) Volumes(context.Context) ([]dockerapi.Volume, error) { return d.Vols, nil }

func (d *Docker) VolumeSizes(context.Context) (map[string]int64, error) { return d.VolSizes, nil }

// Images antwortet wie GET /images/json; im containerd-Modus abhängig von der
// API-Version mit Image-Index und Herkunft.
func (d *Docker) Images(_ context.Context, apiVersion string) ([]dockerapi.Image, error) {
	if !d.Containerd {
		return d.Imgs, nil
	}
	out := make([]dockerapi.Image, 0, len(d.Imgs))
	for _, img := range d.Imgs {
		how := d.How[img.ID]
		c := dockerapi.Image{ID: img.ID, RepoTags: img.RepoTags, Size: img.Size}
		for _, t := range img.RepoTags {
			c.RepoDigests = append(c.RepoDigests, repoOf(t)+"@"+img.ID)
		}
		if how == "build" || how == "compose" {
			c.Labels = map[string]string{"com.docker.compose.project": "x", "com.docker.compose.service": "y"}
		}
		if dockerapi.AtLeast(apiVersion, 1, 47) {
			own := dockerapi.ImageManifest{Kind: "image", Available: true}
			if dockerapi.AtLeast(apiVersion, 1, 54) {
				switch how {
				case "pull":
					repo := "docker.io/library/caddy" // mc-packs: altes caddy:2-alpine ohne Namen
					if len(img.RepoTags) > 0 {
						repo = fullRepo(img.RepoTags[0])
					}
					own.ImageData = &dockerapi.ImageData{Identity: &dockerapi.ImageIdentity{Pull: []dockerapi.PullIdentity{{Repository: repo}}}}
				case "build":
					own.ImageData = &dockerapi.ImageData{Identity: &dockerapi.ImageIdentity{Build: []dockerapi.BuildIdentity{{Ref: "geheim-build-ref"}}}}
				}
			}
			c.Manifests = []dockerapi.ImageManifest{own}
			switch how {
			case "pull":
				// Andere Plattformen und Bescheinigungen lädt docker pull nicht.
				c.Manifests = append(c.Manifests,
					dockerapi.ImageManifest{Kind: "image"},
					dockerapi.ImageManifest{Kind: "attestation"})
			case "build", "compose":
				c.Manifests = append(c.Manifests, dockerapi.ImageManifest{Kind: "attestation", Available: true})
			}
		}
		out = append(out, c)
	}
	return out, nil
}

// repoOf schneidet den Tag ab: ghcr.io/a/b:1 wird ghcr.io/a/b.
func repoOf(ref string) string {
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[:i]
	}
	return ref
}

// fullRepo ergänzt Registry und library/ wie Docker in der Herkunftsangabe.
func fullRepo(ref string) string {
	r := repoOf(ref)
	first, _, ok := strings.Cut(r, "/")
	switch {
	case !ok:
		return "docker.io/library/" + r
	case !strings.ContainsAny(first, ".:"):
		return "docker.io/" + r
	}
	return r
}

func (d *Docker) Networks(context.Context) ([]dockerapi.Network, error) { return d.Nets, nil }

// RawEnv gibt alle Umgebungsvariablen mit Werten zurück, damit Tests prüfen
// können, dass keiner der Werte in einer Ausgabe steht.
func (d *Docker) RawEnv() []string {
	var out []string
	for _, c := range d.Ctrs {
		out = append(out, c.C.Config.Env...)
	}
	sort.Strings(out)
	return out
}

// List ist die Liste der Container wie GET /containers/json?all=1
// (dockerapi.Ops.List), etwa für die Erkennung der Spielserver.
func (d *Docker) List(context.Context) ([]dockerapi.OpsContainer, error) {
	var out []dockerapi.OpsContainer
	for _, c := range d.Ctrs {
		o := dockerapi.OpsContainer{ID: c.C.ID, Names: []string{c.C.Name}, Image: c.C.Config.Image, ImageID: c.C.Image, State: c.C.State.Status,
			Labels: c.C.Config.Labels, Mounts: c.C.Mounts}
		o.HostConfig.NetworkMode = c.C.HostConfig.NetworkMode
		for spec, bs := range c.C.HostConfig.PortBindings {
			var priv int
			var proto string
			fmt.Sscanf(strings.Replace(spec, "/", " ", 1), "%d %s", &priv, &proto)
			for _, b := range bs {
				var pub int
				fmt.Sscan(b.HostPort, &pub)
				o.Ports = append(o.Ports, dockerapi.ContainerPort{IP: b.HostIP, PrivatePort: priv, PublicPort: pub, Type: proto})
			}
		}
		out = append(out, o)
	}
	return out, nil
}
