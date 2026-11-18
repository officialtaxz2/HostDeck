package discover

import (
	"context"
	"errors"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/hostfs"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

const (
	labelProject     = "com.docker.compose.project"
	labelWorkDir     = "com.docker.compose.project.working_dir"
	labelConfigFiles = "com.docker.compose.project.config_files"
	labelEnvFiles    = "com.docker.compose.project.environment_file"
	labelService     = "com.docker.compose.service"
	labelNumber      = "com.docker.compose.container-number"
	labelOneoff      = "com.docker.compose.oneoff"
	labelAnonVolume  = "com.docker.volume.anonymous"
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// containerKey bildet den stabilen Schlüssel eines Containers: Compose-Projekt
// plus Dienst, sonst der Name. Die Container-ID kommt nie vor.
func containerKey(name string, labels map[string]string) string {
	p, svc := labels[labelProject], labels[labelService]
	if p == "" || svc == "" || strings.EqualFold(labels[labelOneoff], "true") {
		return "container:" + name
	}
	k := "compose:" + p + "/" + svc
	if n := labels[labelNumber]; n != "" && n != "1" {
		k += "#" + n
	}
	return k
}

func (s *scan) containers(ctx context.Context) error {
	sums, err := s.d.Docker.Containers(ctx, !s.o.NoSizes)
	if err != nil && !s.o.NoSizes {
		// Die Größenberechnung kann bei vielen Containern zu lange dauern.
		s.addFinding(inventory.LevelInfo, "container-groesse", "", "Docker konnte die Größe der Container-Schichten nicht berechnen; sie fehlt im Bericht.")
		sums, err = s.d.Docker.Containers(ctx, false)
		for i := range sums {
			sums[i].SizeRw = inventory.Unknown
		}
	}
	if err != nil {
		return err
	}
	seen := map[string]int{}
	for _, sum := range sums {
		c, err := s.d.Docker.Inspect(ctx, sum.ID)
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(c.Name, "/")
		labels := c.Config.Labels
		key := containerKey(name, labels)
		if seen[key] > 0 {
			s.addFinding(inventory.LevelWarn, "schluessel-doppelt", key, "Zwei Container haben denselben Schlüssel "+key+"; der zweite heißt jetzt "+key+"@"+name+". Gibt es zwei Compose-Projekte mit gleichem Namen?")
			key += "@" + name
		}
		seen[key]++

		ic := inventory.Container{
			Key:           key,
			Name:          name,
			ID:            c.ID,
			State:         c.State.Status,
			Running:       c.State.Running,
			ExitCode:      c.State.ExitCode,
			Image:         c.Config.Image,
			ImageID:       c.Image,
			NetworkMode:   c.HostConfig.NetworkMode,
			HostNetwork:   c.HostConfig.NetworkMode == "host",
			EnvNames:      append([]string{}, c.Config.Env...),
			EnvSet:        append([]string(nil), c.Config.EnvSet...),
			RestartPolicy: c.HostConfig.RestartPolicy.Name,
			Privileged:    c.HostConfig.Privileged,
			SizeRw:        sum.SizeRw,
			Networks:      []string{},
			Ports:         []inventory.Port{},
			Mounts:        []inventory.Mount{},
		}
		if s.o.NoSizes {
			ic.SizeRw = inventory.Unknown
		}
		sort.Strings(ic.EnvNames)
		sort.Strings(ic.EnvSet)
		if p := labels[labelProject]; p != "" && !strings.HasPrefix(key, "container:") {
			ic.Project = "project:" + p
			ic.Service = labels[labelService]
			pb := s.projects[p]
			if pb == nil {
				pb = &projectBuild{name: p, workDirs: map[string]int{}, configFiles: map[string]bool{}, envFiles: map[string]bool{}}
				s.projects[p] = pb
			}
			if wd := labels[labelWorkDir]; wd != "" {
				pb.workDirs[hostfs.Clean(wd)]++
			}
			for _, f := range strings.Split(labels[labelConfigFiles], ",") {
				if f = strings.TrimSpace(f); f != "" {
					pb.configFiles[f] = true
				}
			}
			for _, f := range strings.Split(labels[labelEnvFiles], ",") {
				if f = strings.TrimSpace(f); f != "" {
					pb.envFiles[f] = true
				}
			}
		}
		for n := range c.NetworkSettings.Networks {
			ic.Networks = append(ic.Networks, n)
		}
		sort.Strings(ic.Networks)
		for spec, binds := range c.HostConfig.PortBindings {
			cport, proto, _ := strings.Cut(spec, "/")
			if proto == "" {
				proto = "tcp"
			}
			cp, _ := strconv.Atoi(cport)
			for _, b := range binds {
				hp, err := strconv.Atoi(b.HostPort)
				if err != nil {
					continue
				}
				ic.Ports = append(ic.Ports, inventory.Port{HostIP: b.HostIP, HostPort: hp, ContainerPort: cp, Proto: proto})
			}
		}
		sort.Slice(ic.Ports, func(i, j int) bool {
			if ic.Ports[i].HostPort != ic.Ports[j].HostPort {
				return ic.Ports[i].HostPort < ic.Ports[j].HostPort
			}
			if ic.Ports[i].Proto != ic.Ports[j].Proto {
				return ic.Ports[i].Proto < ic.Ports[j].Proto
			}
			return ic.Ports[i].HostIP < ic.Ports[j].HostIP
		})
		for _, m := range c.Mounts {
			im := inventory.Mount{Type: m.Type, Destination: m.Destination, RW: m.RW}
			switch m.Type {
			case "bind":
				im.Source = hostfs.Clean(m.Source)
				if isDockerSocket(im.Source) {
					ic.DockerSocket = true
				}
			case "volume":
				im.Volume = m.Name
				im.Source = m.Source
			default:
				im.Source = m.Source
			}
			ic.Mounts = append(ic.Mounts, im)
		}
		sort.Slice(ic.Mounts, func(i, j int) bool { return ic.Mounts[i].Destination < ic.Mounts[j].Destination })
		s.inv.Containers = append(s.inv.Containers, ic)
		s.raw[key] = c
		s.byID[c.ID] = key
	}
	sort.Slice(s.inv.Containers, func(i, j int) bool { return s.inv.Containers[i].Key < s.inv.Containers[j].Key })
	return nil
}

// managed erkennt Container, die ein anderer Container anlegt, etwa
// Spielserver, die Calagopus/Wings startet. Merkmal: Der Verwalter hat den
// Docker-Socket und bindet einen Ordner am selben Pfad ein, und alle
// eingebundenen Ordner des verwalteten Containers liegen darunter. Die
// Container-ID solcher Container ändert sich bei jedem Neustart, daher
// bekommen sie einen Schlüssel nach ihrem Namen.
func (s *scan) managed() {
	for i := range s.inv.Containers {
		x := &s.inv.Containers[i]
		if !strings.HasPrefix(x.Key, "container:") {
			continue
		}
		j, root := ManagerOf(*x, s.inv.Containers)
		if j < 0 || j == i {
			continue
		}
		m := &s.inv.Containers[j]
		oldKey := x.Key
		x.Key = "managed:" + x.Name
		x.ManagedBy = m.Key
		x.ManagerPath = root
		x.Project = m.Project
		s.raw[x.Key] = s.raw[oldKey]
		delete(s.raw, oldKey)
		s.byID[x.ID] = x.Key
	}
	sort.Slice(s.inv.Containers, func(i, j int) bool { return s.inv.Containers[i].Key < s.inv.Containers[j].Key })
}

// ManagerOf sucht den Container in all, der x anlegt (etwa Wings einen
// Spielserver): Er hat den Docker-Socket und bindet einen Ordner am selben
// Pfad ein, unter dem alle eingebundenen Ordner von x liegen. Ergebnis: sein
// Index in all und dieser Ordner, oder -1. Die Bestandsaufnahme und der
// Betrieb im Leitstand nutzen dieselbe Regel.
func ManagerOf(x inventory.Container, all []inventory.Container) (int, string) {
	var binds []string
	for _, m := range x.Mounts {
		if m.Type == "bind" && !isSystemPath(m.Source) {
			binds = append(binds, m.Source)
		}
	}
	if len(binds) == 0 {
		return -1, ""
	}
	for j := range all {
		m := &all[j]
		if !m.DockerSocket || m.Name == x.Name {
			continue
		}
		if root := identityRoot(m, binds); root != "" {
			return j, root
		}
	}
	return -1, ""
}

// IsDockerSocket meldet, ob p der Socket von Docker oder containerd ist.
func IsDockerSocket(p string) bool { return isDockerSocket(p) }

// identityRoot gibt den Ordner zurück, den m am selben Pfad einbindet und
// unter dem alle binds liegen.
func identityRoot(m *inventory.Container, binds []string) string {
	for _, mm := range m.Mounts {
		if mm.Type != "bind" || mm.Source != hostfs.Clean(mm.Destination) || isSystemPath(mm.Source) || mm.Source == "/" {
			continue
		}
		all := true
		for _, b := range binds {
			if !hostfs.Under(b, mm.Source) {
				all = false
				break
			}
		}
		if all {
			return mm.Source
		}
	}
	return ""
}

// serverDirName: Verwalter wie Wings und Pterodactyl benennen die Ordner
// ihrer Server nach einer UUID. Nur solche Ordner gelten als Server, damit
// Hilfsordner des Verwalters nicht als Server auftauchen.
var serverDirName = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// managedServers listet alle Server, die ein Verwalter anlegt: die mit
// Container und die, deren Container gerade fehlt. Wings löscht den
// Container eines Servers, der aus ist; dann bleibt nur sein Ordner in
// <Verwalter-Ordner>/volumes. Gesucht wird dort und neben den Ordnern der
// Server, die gerade einen Container haben.
func (s *scan) managedServers() {
	s.inv.Managed = []inventory.Managed{}
	known := map[string]bool{}
	for _, c := range s.inv.Containers {
		known[c.Name] = true
	}
	type place struct{ dir, manager, root, project string }
	var places []place
	seen := map[string]bool{}
	addPlace := func(p place) {
		if !seen[p.dir] {
			seen[p.dir] = true
			places = append(places, p)
		}
	}
	for _, c := range s.inv.Containers {
		if c.ManagedBy == "" {
			continue
		}
		m := inventory.Managed{
			Key: c.Key, Name: c.Name, Project: c.Project, Manager: c.ManagedBy, Root: c.ManagerPath,
			Path: serverDir(c), Container: true, Running: c.Running, State: c.State, Size: inventory.Unknown,
		}
		s.inv.Managed = append(s.inv.Managed, m)
		if m.Path != "" && serverDirName.MatchString(path.Base(m.Path)) {
			addPlace(place{path.Dir(m.Path), m.Manager, m.Root, m.Project})
		}
	}
	// Verwalter ohne laufende Server: Container mit Docker-Socket, die einen
	// Ordner am selben Pfad einbinden, in dem es volumes/ gibt.
	for _, c := range s.inv.Containers {
		if !c.DockerSocket || c.ManagedBy != "" {
			continue
		}
		for _, mm := range c.Mounts {
			if mm.Type != "bind" || mm.Source != hostfs.Clean(mm.Destination) || isSystemPath(mm.Source) || mm.Source == "/" {
				continue
			}
			addPlace(place{mm.Source + "/volumes", c.Key, mm.Source, c.Project})
		}
	}
	for _, pl := range places {
		entries, err := s.d.FS.ReadDir(pl.dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || !serverDirName.MatchString(name) || known[name] {
				continue
			}
			known[name] = true
			s.inv.Managed = append(s.inv.Managed, inventory.Managed{
				Key: "managed:" + name, Name: name, Project: pl.project, Manager: pl.manager, Root: pl.root,
				Path: pl.dir + "/" + name, Size: inventory.Unknown,
			})
		}
	}
	for i := range s.inv.Managed {
		if m := &s.inv.Managed[i]; m.Path != "" {
			m.Label = s.serverLabel(m.Path)
		}
	}
	sort.Slice(s.inv.Managed, func(i, j int) bool { return s.inv.Managed[i].Key < s.inv.Managed[j].Key })
}

// serverDir ist der Datenordner eines verwalteten Containers: der
// beschreibbar eingebundene Ordner, der nach dem Container heißt, sonst der
// erste beschreibbar eingebundene Ordner unter dem Verwalter-Ordner.
func serverDir(c inventory.Container) string {
	first := ""
	for _, m := range c.Mounts {
		if m.Type != "bind" || !m.RW || !hostfs.StrictlyUnder(m.Source, c.ManagerPath) {
			continue
		}
		if path.Base(m.Source) == c.Name {
			return m.Source
		}
		if first == "" {
			first = m.Source
		}
	}
	return first
}

func (s *scan) volumes(ctx context.Context) error {
	vols, err := s.d.Docker.Volumes(ctx)
	if err != nil {
		return err
	}
	sizes := map[string]int64{}
	if !s.o.NoSizes {
		if sz, err := s.d.Docker.VolumeSizes(ctx); err == nil {
			sizes = sz
		}
	}
	type user struct {
		key, dest, project string
	}
	users := map[string][]user{}
	for _, c := range s.inv.Containers {
		for _, m := range c.Mounts {
			if m.Type == "volume" {
				users[m.Volume] = append(users[m.Volume], user{c.Key, m.Destination, c.Project})
			}
		}
	}
	keyOf := map[string]string{}
	for _, v := range vols {
		iv := inventory.Volume{
			Name:       v.Name,
			Driver:     v.Driver,
			Mountpoint: v.Mountpoint,
			FormerProj: v.Labels[labelProject],
			UsedBy:     []string{},
			Size:       inventory.Unknown,
		}
		if sz, ok := sizes[v.Name]; ok && sz >= 0 {
			iv.Size = sz
		}
		_, anonLabel := v.Labels[labelAnonVolume]
		iv.Anonymous = anonLabel || hex64.MatchString(v.Name)
		us := users[v.Name]
		for _, u := range us {
			iv.UsedBy = appendUnique(iv.UsedBy, u.key)
			if iv.Project == "" {
				iv.Project = u.project
			}
		}
		iv.Orphan = len(us) == 0
		if iv.FormerProj != "" {
			iv.FormerProj = "project:" + iv.FormerProj
		}
		iv.Key = "volume:" + v.Name
		if iv.Anonymous && len(iv.UsedBy) == 1 {
			// Anonyme Volumes heißen nach einem Zufallswert, der beim
			// Neuanlegen wechselt. Stabil ist nur „welcher Container, wo“.
			iv.Key = "anon:" + us[0].key + ":" + us[0].dest
		}
		keyOf[v.Name] = iv.Key
		s.inv.Volumes = append(s.inv.Volumes, iv)
	}
	for i := range s.inv.Containers {
		for j := range s.inv.Containers[i].Mounts {
			m := &s.inv.Containers[i].Mounts[j]
			if m.Type == "volume" {
				m.DataKey = keyOf[m.Volume]
			}
		}
	}
	return nil
}

func (s *scan) networks(ctx context.Context) error {
	nets, err := s.d.Docker.Networks(ctx)
	if err != nil {
		return err
	}
	for _, n := range nets {
		in := inventory.Network{
			Name:     n.Name,
			Driver:   n.Driver,
			Scope:    n.Scope,
			Internal: n.Internal,
			Subnets:  []string{},
			Builtin:  n.Name == "bridge" || n.Name == "host" || n.Name == "none",
		}
		if p := n.Labels[labelProject]; p != "" {
			in.Project = "project:" + p
		}
		for _, c := range n.IPAM.Config {
			if c.Subnet != "" {
				in.Subnets = append(in.Subnets, c.Subnet)
			}
		}
		s.inv.Networks = append(s.inv.Networks, in)
	}
	return nil
}

// buildProjects legt die Compose-Projekte an und bestimmt ihren Ordner.
func (s *scan) buildProjects(ctx context.Context) {
	names := make([]string, 0, len(s.projects))
	for n := range s.projects {
		names = append(names, n)
	}
	sort.Strings(names)

	workDirs := map[string]string{}
	for _, n := range names {
		workDirs[n] = mostCommon(s.projects[n].workDirs)
	}
	gitMissing := false
	for _, n := range names {
		pb := s.projects[n]
		p := inventory.Project{
			Key:         "project:" + n,
			Name:        n,
			WorkingDir:  workDirs[n],
			ConfigFiles: []string{},
			Services:    []string{},
			Containers:  []string{},
			Managed:     []string{},
			Domains:     []string{},
		}
		for f := range pb.configFiles {
			p.ConfigFiles = append(p.ConfigFiles, f)
		}
		sort.Strings(p.ConfigFiles)
		for f := range pb.envFiles {
			p.EnvFiles = append(p.EnvFiles, f)
		}
		sort.Strings(p.EnvFiles)
		p.Path = p.WorkingDir
		p.DirExists = p.WorkingDir != "" && s.d.FS.IsDir(p.WorkingDir)
		if p.DirExists && s.d.Git != nil && !gitMissing {
			g, err := s.d.Git.Inspect(ctx, p.WorkingDir)
			switch {
			case errors.Is(err, ErrGitNotInstalled):
				gitMissing = true
				s.addFinding(inventory.LevelWarn, "git-fehlt", "", "Git ist nicht installiert; der Git-Stand der Projekte fehlt im Bericht.")
			case err != nil:
				s.addFinding(inventory.LevelWarn, "git-fehler", p.Key, "Git-Stand von "+n+" nicht lesbar: "+err.Error())
			case g != nil:
				p.Git = g
				if s.useGitRoot(g.Root, p.WorkingDir, n, workDirs) {
					p.Path = g.Root
				}
			}
		}
		for _, m := range s.inv.Managed {
			if m.Project == p.Key {
				p.Managed = append(p.Managed, m.Key)
			}
		}
		for _, c := range s.inv.Containers {
			if c.Project != p.Key || c.ManagedBy != "" {
				continue
			}
			p.Containers = append(p.Containers, c.Key)
			p.Services = appendUnique(p.Services, c.Service)
			if c.Running {
				p.Running++
			}
		}
		s.inv.Projects = append(s.inv.Projects, p)
	}
}

// useGitRoot entscheidet, ob statt des Compose-Ordners das Git-Repo als
// Projektordner gilt. Das Repo darf keine anderen Projekte enthalten und kein
// Sammelordner wie /opt/docker sein.
func (s *scan) useGitRoot(root, wd, self string, workDirs map[string]string) bool {
	root = hostfs.Clean(root)
	if root == wd || !hostfs.StrictlyUnder(wd, root) {
		return false
	}
	if strings.Count(root, "/") < 2 {
		return false
	}
	for _, sr := range s.o.ScanRoots {
		if root == hostfs.Clean(sr) {
			return false
		}
	}
	for n, other := range workDirs {
		if n != self && other != "" && hostfs.Under(other, root) {
			return false
		}
	}
	return true
}

func mostCommon(m map[string]int) string {
	best, n := "", -1
	for k, v := range m {
		if v > n || (v == n && k < best) {
			best, n = k, v
		}
	}
	return best
}
