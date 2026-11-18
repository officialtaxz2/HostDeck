package dockerapi

import (
	"encoding/json"
	"strings"
)

// Version ist die Antwort von GET /version.
type Version struct {
	Version    string `json:"Version"`
	APIVersion string `json:"ApiVersion"`
}

// Info ist ein Ausschnitt der Antwort von GET /info.
type Info struct {
	Name            string `json:"Name"`
	ServerVersion   string `json:"ServerVersion"`
	DockerRootDir   string `json:"DockerRootDir"`
	Driver          string `json:"Driver"`
	OperatingSystem string `json:"OperatingSystem"`
}

// ContainerSummary ist ein Eintrag aus GET /containers/json.
type ContainerSummary struct {
	ID     string   `json:"Id"`
	Names  []string `json:"Names"`
	State  string   `json:"State"`
	SizeRw int64    `json:"SizeRw"`
}

// Container ist ein Ausschnitt der Antwort von GET /containers/{id}/json.
type Container struct {
	ID              string          `json:"Id"`
	Name            string          `json:"Name"`
	RestartCount    int             `json:"RestartCount"`
	Image           string          `json:"Image"` // Image-ID
	State           ContainerState  `json:"State"`
	Config          ContainerConfig `json:"Config"`
	HostConfig      HostConfig      `json:"HostConfig"`
	Mounts          []MountPoint    `json:"Mounts"`
	NetworkSettings NetworkSettings `json:"NetworkSettings"`
	// LogPath ist die Log-Datei bei json-file; sonst leer.
	LogPath string `json:"LogPath"`
}

type ContainerState struct {
	Status     string `json:"Status"`
	Running    bool   `json:"Running"`
	Restarting bool   `json:"Restarting"`
	ExitCode   int    `json:"ExitCode"`
	OOMKilled  bool   `json:"OOMKilled"` // vom Kernel beendet, weil der Speicher ausging
	Pid        int    `json:"Pid"`
	StartedAt  string `json:"StartedAt"`
	FinishedAt string `json:"FinishedAt"`
	// Health gibt es nur bei Containern mit Healthcheck.
	Health *struct {
		Status string `json:"Status"` // starting, healthy, unhealthy
	} `json:"Health,omitempty"`
}

type ContainerConfig struct {
	Image  string            `json:"Image"` // Referenz wie konfiguriert
	Tty    bool              `json:"Tty"`
	Labels map[string]string `json:"Labels"`
	Env    EnvNames          `json:"Env"`
	// EnvSet sind die Namen der Variablen mit einem Wert, der nicht leer
	// ist. Nur der Name und ob etwas drinsteht, nie der Wert: So merkt der
	// Umzug, wenn ein Wert drüben fehlt, etwa weil ihn beim Start ein
	// Skript aus der Shell gesetzt hat.
	EnvSet []string `json:"-"`
}

// UnmarshalJSON liest Env einmal und behält daraus nur Namen und, welche
// davon einen Wert haben.
func (c *ContainerConfig) UnmarshalJSON(b []byte) error {
	type plain ContainerConfig
	var aux struct {
		plain
		Env []string `json:"Env"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	*c = ContainerConfig(aux.plain)
	c.Env = make(EnvNames, 0, len(aux.Env))
	c.EnvSet = nil
	for _, kv := range aux.Env {
		name, val, _ := strings.Cut(kv, "=")
		c.Env = append(c.Env, name)
		if val != "" {
			c.EnvSet = append(c.EnvSet, name)
		}
	}
	return nil
}

type HostConfig struct {
	NetworkMode   string                   `json:"NetworkMode"`
	PortBindings  map[string][]PortBinding `json:"PortBindings"`
	RestartPolicy struct {
		Name string `json:"Name"`
	} `json:"RestartPolicy"`
	Privileged bool `json:"Privileged"`
	// Memory ist die Speichergrenze in Byte; 0: keine.
	Memory    int64     `json:"Memory"`
	LogConfig LogConfig `json:"LogConfig"`
}

// LogConfig ist der Log-Treiber eines Containers samt Einstellungen
// (etwa max-size). Die Werte sind Größen und Zahlen, keine Geheimnisse.
type LogConfig struct {
	Type   string            `json:"Type"`
	Config map[string]string `json:"Config"`
}

type PortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type MountPoint struct {
	Type        string `json:"Type"` // bind, volume, tmpfs
	Name        string `json:"Name"` // Volume-Name
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
}

type NetworkSettings struct {
	Networks map[string]struct{} `json:"Networks"`
}

// EnvNames nimmt die Umgebungsvariablen eines Containers entgegen und behält
// nur die Namen. Die Werte verlassen den JSON-Decoder nie, so können sie auch
// nicht versehentlich in Inventar, Manifest, Bericht oder Log landen.
type EnvNames []string

func (e *EnvNames) UnmarshalJSON(b []byte) error {
	var raw []string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	names := make([]string, 0, len(raw))
	for _, kv := range raw {
		name, _, _ := strings.Cut(kv, "=")
		names = append(names, name)
	}
	*e = names
	return nil
}

// Volume ist ein Eintrag aus GET /volumes.
type Volume struct {
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver"`
	Mountpoint string            `json:"Mountpoint"`
	Labels     map[string]string `json:"Labels"`
	Options    map[string]string `json:"Options"`
}

type volumeList struct {
	Volumes []Volume `json:"Volumes"`
}

// Image ist ein Eintrag aus GET /images/json.
type Image struct {
	ID          string            `json:"Id"`
	RepoTags    []string          `json:"RepoTags"`
	RepoDigests []string          `json:"RepoDigests"`
	Size        int64             `json:"Size"`
	Labels      map[string]string `json:"Labels"`
	// Manifests gibt es nur mit dem containerd-Image-Speicher (Standard ab
	// Docker 29) und nur, wenn Images sie anfordert.
	Manifests []ImageManifest `json:"Manifests"`
}

// ImageManifest ist ein Eintrag aus dem Image-Index: ein Image für eine
// Plattform oder eine Build-Bescheinigung (attestation).
type ImageManifest struct {
	Kind      string     `json:"Kind"` // image, attestation, unknown
	Available bool       `json:"Available"`
	ImageData *ImageData `json:"ImageData"`
}

type ImageData struct {
	Identity *ImageIdentity `json:"Identity"`
}

// ImageIdentity sagt, woher Docker ein Image hat (API 1.54 und neuer). Docker
// leitet sie aus eigenen Daten ab, ein Umbenennen per docker tag ändert sie
// nicht.
type ImageIdentity struct {
	Pull  []PullIdentity  `json:"Pull"`
	Build []BuildIdentity `json:"Build"`
}

type PullIdentity struct {
	Repository string `json:"Repository"`
}

type BuildIdentity struct {
	Ref string `json:"Ref"`
}

// Network ist ein Eintrag aus GET /networks.
type Network struct {
	Name     string            `json:"Name"`
	Driver   string            `json:"Driver"`
	Scope    string            `json:"Scope"`
	Internal bool              `json:"Internal"`
	Labels   map[string]string `json:"Labels"`
	IPAM     struct {
		Config []struct {
			Subnet string `json:"Subnet"`
		} `json:"Config"`
	} `json:"IPAM"`
}
