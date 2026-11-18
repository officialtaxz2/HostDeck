// Package inventory beschreibt inventory.json: alles, was hostdeck discover auf
// dem Server gefunden hat. Die Datei wird bei jedem Lauf neu geschrieben und
// enthält keine Entscheidungen; die stehen in manifest.yaml.
package inventory

import (
	"encoding/json"
	"time"
)

const SchemaVersion = 1

// Unknown steht bei Größen für „nicht ermittelt“.
const Unknown int64 = -1

type Inventory struct {
	Version    int           `json:"version"`
	Tool       string        `json:"tool"`
	CreatedAt  time.Time     `json:"created_at"`
	Host       Host          `json:"host"`
	Docker     Docker        `json:"docker"`
	Projects   []Project     `json:"projects"`
	Containers []Container   `json:"containers"`
	Managed    []Managed     `json:"managed"`
	Volumes    []Volume      `json:"volumes"`
	Images     []Image       `json:"images"`
	Networks   []Network     `json:"networks"`
	Paths      []Path        `json:"paths"`
	System     []SystemMount `json:"system"`
	Unclaimed  []Unclaimed   `json:"unclaimed"`
	Caddy      *Caddy        `json:"caddy,omitempty"`
	Listeners  []Listener    `json:"listeners"`
	Tasks      []Task        `json:"tasks"`    // Cronjobs, eigene systemd-Dienste und -Timer
	Settings   []Setting     `json:"settings"` // selbst geänderte oder angelegte Dateien in /etc
	Checks     []Check       `json:"checks"`   // Checkliste für den neuen Server
	Space      *Space        `json:"space,omitempty"`
	Findings   []Finding     `json:"findings"`
}

type Host struct {
	Hostname  string `json:"hostname"`
	OS        string `json:"os"`
	OSID      string `json:"os_id"`
	OSVersion string `json:"os_version"`
	Kernel    string `json:"kernel"`
	Arch      string `json:"arch"`
	Root      bool   `json:"root"`
}

type Docker struct {
	// Missing: Auf dem Server gibt es kein Docker (dann keine Container).
	Missing       bool   `json:"fehlt,omitempty"`
	Version       string `json:"version"`
	APIVersion    string `json:"api_version"`
	RootDir       string `json:"root_dir"`
	StorageDriver string `json:"storage_driver"`
}

type Project struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	WorkingDir  string   `json:"working_dir"`
	ConfigFiles []string `json:"config_files"`
	// EnvFiles: Env-Dateien, mit denen Compose das Projekt angelegt hat
	// (Label com.docker.compose.project.environment_file). Nur Pfade.
	EnvFiles   []string `json:"env_files,omitempty"`
	Path       string   `json:"path"`
	DirExists  bool     `json:"dir_exists"`
	Git        *Git     `json:"git,omitempty"`
	Services   []string `json:"services"`
	Containers []string `json:"containers"`
	Managed    []string `json:"managed"` // Schlüssel aus Inventory.Managed
	Running    int      `json:"running"` // laufende Container ohne die verwalteten
	Domains    []string `json:"domains"`
}

type Git struct {
	Root   string `json:"root"`
	Remote string `json:"remote,omitempty"`
	Branch string `json:"branch,omitempty"`
	Commit string `json:"commit,omitempty"`
	Dirty  int    `json:"dirty"`
}

type Container struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	ID          string   `json:"id"` // nur zur Info, ändert sich bei Neuanlage
	Project     string   `json:"project,omitempty"`
	Service     string   `json:"service,omitempty"`
	ManagedBy   string   `json:"managed_by,omitempty"`
	ManagerPath string   `json:"manager_path,omitempty"`
	State       string   `json:"state"`
	Running     bool     `json:"running"`
	ExitCode    int      `json:"exit_code"`
	Image       string   `json:"image"`
	ImageID     string   `json:"image_id"`
	ImageLocal  bool     `json:"image_local"`  // Image per docker save sichern
	ImageOrigin string   `json:"image_origin"` // registry, gebaut oder unklar
	NetworkMode string   `json:"network_mode"`
	HostNetwork bool     `json:"host_network"`
	Networks    []string `json:"networks"`
	Ports       []Port   `json:"ports"`
	Mounts      []Mount  `json:"mounts"`
	EnvNames    []string `json:"env_names"`
	// EnvSet: die Namen mit einem Wert, der nicht leer ist (nie Werte).
	// Fehlt in Bestandsaufnahmen vor dem 05.11.2026.
	EnvSet        []string `json:"env_set,omitempty"`
	RestartPolicy string   `json:"restart_policy"`
	Privileged    bool     `json:"privileged"`
	DockerSocket  bool     `json:"docker_socket"`
	SizeRw        int64    `json:"size_rw"`
	DB            *DB      `json:"db,omitempty"`
}

// Managed ist ein Server, den ein anderer Container anlegt, etwa ein
// Spielserver von Wings. Er steht hier auch dann, wenn es gerade keinen
// Container für ihn gibt: Wings löscht den Container, wenn der Server aus
// ist, seine Daten bleiben aber im Ordner des Verwalters.
type Managed struct {
	Key       string `json:"key"` // managed:<Name>
	Name      string `json:"name"`
	Project   string `json:"project,omitempty"`
	Manager   string `json:"manager"`        // Schlüssel des Verwalter-Containers
	Root      string `json:"root"`           // Ordner des Verwalters
	Path      string `json:"path,omitempty"` // Datenordner dieses Servers
	Container bool   `json:"container"`      // gerade gibt es einen Container
	Running   bool   `json:"running"`
	State     string `json:"state"` // Zustand des Containers, „“ ohne Container
	Size      int64  `json:"size"`
	// Label hilft, Server auseinanderzuhalten, etwa die MOTD aus der
	// server.properties eines Minecraft-Servers.
	Label string `json:"label,omitempty"`
}

type Port struct {
	HostIP        string `json:"host_ip"`
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Proto         string `json:"proto"`
}

type Mount struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	Volume      string `json:"volume,omitempty"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
	DataKey     string `json:"data_key,omitempty"` // Eintrag, der diese Daten sichert
	System      bool   `json:"system,omitempty"`
}

// DB beschreibt eine erkannte Datenbank.
type DB struct {
	Engine  string `json:"engine"`
	Version string `json:"version,omitempty"`
	Via     string `json:"via"` // image, pg_version, signatur
	DataKey string `json:"data_key,omitempty"`
}

type Volume struct {
	Key        string   `json:"key"`
	Name       string   `json:"name"`
	Anonymous  bool     `json:"anonymous"`
	Driver     string   `json:"driver"`
	Mountpoint string   `json:"mountpoint"`
	Project    string   `json:"project,omitempty"`       // Projekt der nutzenden Container
	FormerProj string   `json:"label_project,omitempty"` // Compose-Label am Volume
	UsedBy     []string `json:"used_by"`
	Orphan     bool     `json:"orphan"`
	Size       int64    `json:"size"`
	DB         *DB      `json:"db,omitempty"`
	SQLite     []string `json:"sqlite,omitempty"`
}

// Herkunft eines Images.
const (
	OriginRegistry = "registry" // aus einer Registry geladen, lässt sich neu laden
	OriginBuilt    = "gebaut"   // auf diesem Server gebaut oder per docker load geladen
	OriginUnknown  = "unklar"   // Docker sagt nichts Sicheres darüber
)

type Image struct {
	ID      string   `json:"id"`
	Tags    []string `json:"tags"`
	Digests []string `json:"digests"`
	// Local heißt: per docker save sichern, weil das Image nicht sicher aus
	// einer Registry kommt (gebaut oder Herkunft unklar) oder keinen Namen
	// mehr hat.
	Local      bool     `json:"local"`
	Origin     string   `json:"origin"`
	OriginNote string   `json:"origin_note"`      // woran HostDeck die Herkunft erkannt hat
	Source     string   `json:"source,omitempty"` // Registry-Repository laut Docker
	Size       int64    `json:"size"`
	UsedBy     []string `json:"used_by"`
	Running    bool     `json:"running"`
}

type Network struct {
	Name     string   `json:"name"`
	Driver   string   `json:"driver"`
	Scope    string   `json:"scope"`
	Internal bool     `json:"internal"`
	Subnets  []string `json:"subnets"`
	Project  string   `json:"project,omitempty"`
	Builtin  bool     `json:"builtin"`
}

// Path-Arten.
const (
	KindProject = "projekt" // Projektordner
	KindData    = "daten"   // eingebundener Datenordner mit eigener Strategie
	KindConfig  = "config"  // Datei oder nur lesend eingebundener Ordner
	KindParent  = "elternordner"
)

type Path struct {
	Key         string     `json:"key"`
	Path        string     `json:"path"`
	Kind        string     `json:"kind"`
	Owner       string     `json:"owner,omitempty"`
	CoveredBy   string     `json:"covered_by,omitempty"` // wird mit diesem Eintrag gesichert
	Exists      bool       `json:"exists"`
	IsDir       bool       `json:"is_dir"`
	Mounts      []MountRef `json:"mounts"`
	Excludes    []string   `json:"excludes"`
	Contains    []string   `json:"contains,omitempty"` // bei Elternordnern: enthaltene Projekte
	Size        int64      `json:"size"`
	SizePartial bool       `json:"size_partial,omitempty"`
	Largest     []SubSize  `json:"largest,omitempty"`
	DB          *DB        `json:"db,omitempty"`
	SQLite      []string   `json:"sqlite,omitempty"`
}

type MountRef struct {
	Container   string `json:"container"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
}

type SubSize struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type SystemMount struct {
	Container   string `json:"container"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

// Arten nicht zugeordneter Ordner.
const (
	AreaScan     = ""              // in /opt oder /srv gefunden
	AreaHome     = "home"          // Home-Ordner
	AreaWeb      = "web"           // Webseite in /var/www
	AreaPrograms = "programme"     // eigene Programme in /usr/local
	AreaSettings = "einstellungen" // /etc und Crontabs
	AreaFound    = "fund"          // außerhalb der üblichen Orte, bei der Platzprüfung gefunden
)

type Unclaimed struct {
	Key  string `json:"key"`
	Path string `json:"path"`
	Area string `json:"area,omitempty"`
	Note string `json:"note,omitempty"` // was darin auffällt, nie Inhalte
	Size int64  `json:"size"`
	Git  *Git   `json:"git,omitempty"`
	// Caches sind Unterordner, die sich neu laden lassen; der Vorschlag
	// nimmt sie aus.
	Caches []SubSize `json:"caches,omitempty"`
}

// Task ist eine zeitgesteuerte Aufgabe oder ein eigener Dienst.
type Task struct {
	Kind        string `json:"kind"`   // cron, timer, dienst
	Source      string `json:"source"` // Datei, in der sie steht
	Name        string `json:"name,omitempty"`
	User        string `json:"user,omitempty"`
	Schedule    string `json:"schedule,omitempty"` // wie in der Datei, etwa „30 3 * * *“
	When        string `json:"when,omitempty"`     // lesbar, etwa „täglich 03:30“; leer, wenn HostDeck es nicht übersetzen kann
	Timer       string `json:"timer,omitempty"`    // Timer, der diesen Dienst startet
	Command     string `json:"command,omitempty"`  // gekürzt, mutmaßliche Geheimnisse durch … ersetzt
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
}

// Setting ist eine Datei in /etc, die nicht so ist, wie sie ein Paket
// mitbringt.
type Setting struct {
	Path    string `json:"path"`
	Change  string `json:"change"`            // geaendert oder neu
	Package string `json:"package,omitempty"` // Paket, dem die Datei gehört
}

const (
	ChangeModified = "geaendert"
	ChangeAdded    = "neu"
)

// Check ist ein Punkt auf der Checkliste für den neuen Server.
type Check struct {
	Area   string `json:"area"` // swap, firewall, ssh, docker, benutzer, pakete, zeitzone, kernel, platten
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

// Space beschreibt, wie sich der belegte Platz auf der Systemplatte
// verteilt.
type Space struct {
	Total   int64 `json:"total"`
	Used    int64 `json:"used"`
	Walked  int64 `json:"walked"`  // außerhalb von Docker gezählt
	Data    int64 `json:"data"`    // in Einträgen dieses Inventars, ohne Volumes
	Volumes int64 `json:"volumes"` // Docker-Volumes
	System  int64 `json:"system"`  // Programme, Logs, Paket-Caches
	Swap    int64 `json:"swap"`
	Other   int64 `json:"other"`  // kleinteilig an anderen Orten
	Docker  int64 `json:"docker"` // Rest: Images, Build-Cache, Container-Schichten
	Partial bool  `json:"partial,omitempty"`
	// Top sind die größten Ordner außerhalb von Docker, größte zuerst,
	// damit man sieht, was hinter System und Daten steckt.
	Top []SpacePart `json:"top,omitempty"`
}

// SpacePart ist ein Ordner (oder eine Datei) mit seinem Platz.
type SpacePart struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	// Kind: "daten" (in der Auswahl), "system", "hostdeck" (Daten von HostDeck),
	// "swap" oder "sonst".
	Kind string `json:"kind"`
}

type Caddy struct {
	Config string `json:"config"`
	Found  bool   `json:"found"`
	// DataDir ist der Datenordner von Caddy (Zertifikate, ACME-Konto),
	// leer wenn nicht gefunden.
	DataDir string `json:"data_dir,omitempty"`
	// UID und GID des Benutzers caddy auf diesem Server: Beim Umzug
	// bekommen seine Dateien die Nummern des Benutzers auf dem neuen.
	UID     int           `json:"uid,omitempty"`
	GID     int           `json:"gid,omitempty"`
	Imports []CaddyImport `json:"imports"`
	Files   []string      `json:"files"`
	Routes  []Route       `json:"routes"`
	Error   string        `json:"error,omitempty"`
}

type CaddyImport struct {
	Path    string `json:"path"`
	From    string `json:"from"`
	Exists  bool   `json:"exists"`
	Outside bool   `json:"outside"` // liegt nicht unter /etc/caddy
	Project string `json:"project,omitempty"`
}

// Status einer Caddy-Route.
const (
	RouteOK       = "ok"
	RouteStopped  = "container-gestoppt"
	RouteNothing  = "nichts-lauscht"
	RouteExternal = "extern"
	RouteNoProxy  = "kein-proxy"
)

type Route struct {
	Hosts    []string `json:"hosts"`
	Handler  string   `json:"handler"`
	Upstream string   `json:"upstream,omitempty"`
	Port     int      `json:"port,omitempty"`
	Target   string   `json:"target,omitempty"`
	Status   string   `json:"status"`
}

type Listener struct {
	Proto     string `json:"proto"`
	Addr      string `json:"addr"`
	Port      int    `json:"port"`
	Process   string `json:"process,omitempty"`
	Container string `json:"container,omitempty"`
}

const (
	LevelWarn = "warnung"
	LevelInfo = "hinweis"
)

type Finding struct {
	Level   string `json:"level"`
	Code    string `json:"code"`
	Subject string `json:"subject,omitempty"`
	Message string `json:"message"`
}

func (inv *Inventory) Container(key string) *Container {
	for i := range inv.Containers {
		if inv.Containers[i].Key == key {
			return &inv.Containers[i]
		}
	}
	return nil
}

func (inv *Inventory) ManagedServer(key string) *Managed {
	for i := range inv.Managed {
		if inv.Managed[i].Key == key {
			return &inv.Managed[i]
		}
	}
	return nil
}

func (inv *Inventory) Project(key string) *Project {
	for i := range inv.Projects {
		if inv.Projects[i].Key == key {
			return &inv.Projects[i]
		}
	}
	return nil
}

func (inv *Inventory) Path(key string) *Path {
	for i := range inv.Paths {
		if inv.Paths[i].Key == key {
			return &inv.Paths[i]
		}
	}
	return nil
}

func (inv *Inventory) Volume(key string) *Volume {
	for i := range inv.Volumes {
		if inv.Volumes[i].Key == key {
			return &inv.Volumes[i]
		}
	}
	return nil
}

func (inv *Inventory) Image(id string) *Image {
	for i := range inv.Images {
		if inv.Images[i].ID == id {
			return &inv.Images[i]
		}
	}
	return nil
}

func (inv *Inventory) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
