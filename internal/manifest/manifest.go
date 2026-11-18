// Package manifest liest und schreibt manifest.yaml (Schema v1, siehe
// docs/manifest-v1.md). Das Manifest enthält nur Entscheidungen und verweist
// über stabile Schlüssel auf das Inventar.
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

const Version = 1

type Manifest struct {
	Version    int                    `yaml:"version"`
	Created    string                 `yaml:"created,omitempty"`
	Inventory  string                 `yaml:"inventory,omitempty"`
	Source     Source                 `yaml:"source"`
	Projects   map[string]*Project    `yaml:"projects,omitempty"`
	Unassigned map[string]*Data       `yaml:"unassigned,omitempty"`
	Containers map[string]*Standalone `yaml:"containers,omitempty"`
	Host       *Host                  `yaml:"host,omitempty"`
	System     []SystemEntry          `yaml:"system,omitempty"`
}

type Source struct {
	Hostname string `yaml:"hostname"`
	OS       string `yaml:"os,omitempty"`
}

type Project struct {
	Decision string              `yaml:"decision"`
	Reason   string              `yaml:"reason,omitempty"`
	Note     string              `yaml:"note,omitempty"`
	Path     string              `yaml:"path,omitempty"`
	Domains  []string            `yaml:"domains,omitempty"`
	Services map[string]*Service `yaml:"services,omitempty"`
	Managed  map[string]*Managed `yaml:"managed,omitempty"`
	Data     map[string]*Data    `yaml:"data,omitempty"`
}

type Service struct {
	Image string `yaml:"image"`
	Note  string `yaml:"note,omitempty"`
}

type Managed struct {
	Manager string `yaml:"manager"`
	Stop    string `yaml:"stop"`
	Note    string `yaml:"note,omitempty"`
}

type Data struct {
	Strategy string `yaml:"strategy"`
	Engine   string `yaml:"engine,omitempty"`
	Reason   string `yaml:"reason,omitempty"`
	Note     string `yaml:"note,omitempty"`
}

type Standalone struct {
	Decision string `yaml:"decision"`
	Reason   string `yaml:"reason,omitempty"`
	Note     string `yaml:"note,omitempty"`
}

type Host struct {
	Caddy *Standalone `yaml:"caddy,omitempty"`
}

type SystemEntry struct {
	Container string `yaml:"container"`
	Source    string `yaml:"source"`
}

var (
	decisions     = []string{"take", "archive", "skip", "open"}
	strategies    = []string{"dump", "stop-copy", "live-copy", "rebuild", "archive", "skip", "open"}
	imageChoices  = []string{"image-save", "rebuild", "skip"}
	engineChoices = []string{"postgres", "mysql", "mariadb", "mongo", "redis", "valkey", "sqlite"}
	stopChoices   = []string{"manager"}
)

func oneOf(v string, allowed []string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// Parse liest ein Manifest streng: unbekannte Felder, doppelte Schlüssel und
// ungültige Werte sind Fehler.
func Parse(r io.Reader) (*Manifest, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("manifest.yaml ist leer")
		}
		return nil, fmt.Errorf("manifest.yaml: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("manifest.yaml: nur ein YAML-Dokument erlaubt")
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// ValidationError sammelt alle Fehler eines Manifests.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "manifest.yaml ist ungültig:\n  - " + strings.Join(e.Problems, "\n  - ")
}

func (m *Manifest) Validate() error {
	var p []string
	add := func(format string, args ...any) { p = append(p, fmt.Sprintf(format, args...)) }
	if m.Version != Version {
		add("version: %d wird nicht unterstützt, erwartet %d", m.Version, Version)
	}
	checkDecision := func(where, d, reason string) {
		if !oneOf(d, decisions) {
			add("%s: decision %q ist ungültig (erlaubt: %s)", where, d, strings.Join(decisions, ", "))
		}
		if d == "skip" && strings.TrimSpace(reason) == "" {
			add("%s: skip braucht eine Begründung in reason", where)
		}
	}
	checkData := func(where, key string, d *Data, unassigned bool) {
		if d == nil {
			add("%s.%s: Eintrag ist leer", where, key)
			return
		}
		if err := checkDataKey(key); err != nil {
			add("%s.%s: %v", where, key, err)
		}
		if !oneOf(d.Strategy, strategies) {
			add("%s.%s: strategy %q ist ungültig (erlaubt: %s)", where, key, d.Strategy, strings.Join(strategies, ", "))
		}
		if d.Strategy == "skip" && strings.TrimSpace(d.Reason) == "" {
			add("%s.%s: skip braucht eine Begründung in reason", where, key)
		}
		if d.Strategy == "archive" && !unassigned {
			add("%s.%s: archive gibt es nur unter unassigned; Projektdaten folgen der decision des Projekts", where, key)
		}
		if d.Engine != "" && !oneOf(d.Engine, engineChoices) {
			add("%s.%s: engine %q ist unbekannt", where, key, d.Engine)
		}
		if d.Strategy == "dump" && d.Engine == "" {
			add("%s.%s: dump braucht eine engine", where, key)
		}
	}
	for _, name := range sortedKeys(m.Projects) {
		pr := m.Projects[name]
		where := "projects." + name
		if pr == nil {
			add("%s: Eintrag ist leer", where)
			continue
		}
		checkDecision(where, pr.Decision, pr.Reason)
		for _, svc := range sortedKeys(pr.Services) {
			s := pr.Services[svc]
			if s == nil || !oneOf(s.Image, imageChoices) {
				img := ""
				if s != nil {
					img = s.Image
				}
				add("%s.services.%s: image %q ist ungültig (erlaubt: %s)", where, svc, img, strings.Join(imageChoices, ", "))
			}
		}
		for _, mn := range sortedKeys(pr.Managed) {
			mg := pr.Managed[mn]
			if mg == nil || mg.Manager == "" {
				add("%s.managed.%s: manager fehlt", where, mn)
				continue
			}
			if !oneOf(mg.Stop, stopChoices) {
				add("%s.managed.%s: stop %q ist ungültig (erlaubt: %s)", where, mn, mg.Stop, strings.Join(stopChoices, ", "))
			}
		}
		for _, k := range sortedKeys(pr.Data) {
			checkData(where+".data", k, pr.Data[k], false)
		}
	}
	for _, k := range sortedKeys(m.Unassigned) {
		checkData("unassigned", k, m.Unassigned[k], true)
	}
	for _, k := range sortedKeys(m.Containers) {
		c := m.Containers[k]
		if !strings.HasPrefix(k, "container:") {
			add("containers.%s: Schlüssel muss mit container: beginnen", k)
		}
		if c == nil {
			add("containers.%s: Eintrag ist leer", k)
			continue
		}
		checkDecision("containers."+k, c.Decision, c.Reason)
	}
	if m.Host != nil && m.Host.Caddy != nil {
		checkDecision("host.caddy", m.Host.Caddy.Decision, m.Host.Caddy.Reason)
	}
	if len(p) > 0 {
		return &ValidationError{Problems: p}
	}
	return nil
}

func checkDataKey(key string) error {
	switch {
	case strings.HasPrefix(key, "volume:"):
		if len(key) == len("volume:") {
			return errors.New("Volume-Name fehlt")
		}
	case strings.HasPrefix(key, "anon:"):
		if len(key) == len("anon:") {
			return errors.New("anonymes Volume ohne Container")
		}
	case strings.HasPrefix(key, "path:"):
		p := strings.TrimPrefix(key, "path:")
		if !path.IsAbs(p) || path.Clean(p) != p || strings.Contains(p, "/../") {
			return fmt.Errorf("Pfad %q muss absolut und ohne .. sein", p)
		}
	default:
		return errors.New("Schlüssel muss mit volume:, anon: oder path: beginnen")
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

const header = `# manifest.yaml – deine Entscheidungen für HostDeck (Schema v1, siehe docs/manifest-v1.md)
#
# Am einfachsten bearbeitest du sie mit "./hostdeck serve" im Browser. Von Hand geht es
# auch; hostdeck discover überschreibt sie nie, sondern meldet nur neue oder weggefallene
# Einträge. Kommentare gehen beim Speichern über hostdeck serve verloren.
#
#   decision: take | archive | skip | open      (Projekte, Container, Caddy)
#   strategy: dump | stop-copy | live-copy | rebuild | skip | open   (Daten)
#             archive                           (nur unter unassigned)
#   image:    image-save | rebuild | skip       (Dienste)
#
# skip braucht immer eine Begründung in reason. "note" ist ein Hinweis von HostDeck.
# Das Manifest enthält Domains und Pfade: nicht in ein öffentliches Repo legen.
`

// Marshal schreibt das Manifest als YAML mit erklärendem Kopf.
func (m *Manifest) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(header)
	buf.WriteString("\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
