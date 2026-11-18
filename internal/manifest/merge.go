package manifest

import (
	"sort"
	"strings"
)

// AddNew übernimmt aus dem Vorschlag alle Einträge, deren Schlüssel im
// Manifest noch fehlen. Bestehende Entscheidungen bleiben unverändert. Die
// Rückgabe nennt die übernommenen Schlüssel.
func AddNew(m, proposal *Manifest) []string {
	have := map[string]bool{}
	before := map[string]bool{}
	for _, k := range m.Keys() {
		have[k] = true
		before[k] = true
	}
	proposed := proposal.Keys()
	var added []string
	add := func(k string) {
		have[k] = true
		added = append(added, k)
	}
	// Projekte zuerst: ein neues Projekt bringt seine Dienste und Daten mit.
	for _, name := range sortedKeys(proposal.Projects) {
		if k := "project:" + name; !have[k] {
			if m.Projects == nil {
				m.Projects = map[string]*Project{}
			}
			m.Projects[name] = cloneProject(proposal.Projects[name])
			for _, sub := range (&Manifest{Projects: map[string]*Project{name: m.Projects[name]}}).Keys() {
				have[sub] = true
			}
			added = append(added, k)
		}
	}
	for _, name := range sortedKeys(proposal.Projects) {
		pp, mp := proposal.Projects[name], m.Projects[name]
		if pp == nil || mp == nil {
			continue
		}
		for _, svc := range sortedKeys(pp.Services) {
			if k := "compose:" + name + "/" + svc; !have[k] {
				if mp.Services == nil {
					mp.Services = map[string]*Service{}
				}
				s := *pp.Services[svc]
				mp.Services[svc] = &s
				add(k)
			}
		}
		for _, mn := range sortedKeys(pp.Managed) {
			if k := "managed:" + mn; !have[k] {
				if mp.Managed == nil {
					mp.Managed = map[string]*Managed{}
				}
				mg := *pp.Managed[mn]
				mp.Managed[mn] = &mg
				add(k)
			}
		}
		for _, k := range sortedKeys(pp.Data) {
			if !have[k] {
				if mp.Data == nil {
					mp.Data = map[string]*Data{}
				}
				d := *pp.Data[k]
				mp.Data[k] = &d
				add(k)
			}
		}
	}
	for _, k := range sortedKeys(proposal.Unassigned) {
		// Vorgeschlagene Unterordner (etwa Caches in /root) kommen nur
		// zusammen mit ihrem Eltern-Eintrag. Hat jemand sie später entfernt,
		// bleibt das so.
		if parent := splitParent(k, proposed); parent != "" && before[parent] {
			continue
		}
		if !have[k] {
			if m.Unassigned == nil {
				m.Unassigned = map[string]*Data{}
			}
			d := *proposal.Unassigned[k]
			m.Unassigned[k] = &d
			add(k)
		}
	}
	for _, k := range sortedKeys(proposal.Containers) {
		if !have[k] {
			if m.Containers == nil {
				m.Containers = map[string]*Standalone{}
			}
			c := *proposal.Containers[k]
			m.Containers[k] = &c
			add(k)
		}
	}
	if proposal.Host != nil && proposal.Host.Caddy != nil && !have["host:caddy"] {
		if m.Host == nil {
			m.Host = &Host{}
		}
		c := *proposal.Host.Caddy
		m.Host.Caddy = &c
		add("host:caddy")
	}
	sort.Strings(added)
	return added
}

// RefreshNotes setzt bei allen Einträgen, die auch im Vorschlag stehen, den
// Hinweis (note) auf den aktuellen Stand. Hinweise schreibt HostDeck selbst; sie
// nennen etwa Größen, die Zahl geänderter Dateien oder ob ein Server gerade
// aus ist und veralten sonst nach dem nächsten discover. Entscheidungen,
// Strategien und Begründungen bleiben unverändert, ebenso Einträge, die der
// Vorschlag nicht kennt.
func RefreshNotes(m, proposal *Manifest) {
	current := map[string]string{}
	proposal.eachNote(func(key string, note *string) { current[key] = *note })
	m.eachNote(func(key string, note *string) {
		if n, ok := current[key]; ok {
			*note = n
		}
	})
}

// eachNote ruft fn für jeden Eintrag mit seinem Schlüssel und einem Zeiger
// auf seinen Hinweis auf.
func (m *Manifest) eachNote(fn func(key string, note *string)) {
	for _, name := range sortedKeys(m.Projects) {
		p := m.Projects[name]
		if p == nil {
			continue
		}
		fn("project:"+name, &p.Note)
		for _, svc := range sortedKeys(p.Services) {
			if s := p.Services[svc]; s != nil {
				fn("compose:"+name+"/"+svc, &s.Note)
			}
		}
		for _, mn := range sortedKeys(p.Managed) {
			if mg := p.Managed[mn]; mg != nil {
				fn("managed:"+mn, &mg.Note)
			}
		}
		for _, k := range sortedKeys(p.Data) {
			if d := p.Data[k]; d != nil {
				fn(k, &d.Note)
			}
		}
	}
	for _, k := range sortedKeys(m.Unassigned) {
		if d := m.Unassigned[k]; d != nil {
			fn(k, &d.Note)
		}
	}
	for _, k := range sortedKeys(m.Containers) {
		if c := m.Containers[k]; c != nil {
			fn(k, &c.Note)
		}
	}
	if m.Host != nil && m.Host.Caddy != nil {
		fn("host:caddy", &m.Host.Caddy.Note)
	}
}

func cloneProject(p *Project) *Project {
	if p == nil {
		return nil
	}
	c := *p
	c.Domains = append([]string(nil), p.Domains...)
	if p.Services != nil {
		c.Services = map[string]*Service{}
		for k, v := range p.Services {
			s := *v
			c.Services[k] = &s
		}
	}
	if p.Managed != nil {
		c.Managed = map[string]*Managed{}
		for k, v := range p.Managed {
			mg := *v
			c.Managed[k] = &mg
		}
	}
	if p.Data != nil {
		c.Data = map[string]*Data{}
		for k, v := range p.Data {
			d := *v
			c.Data[k] = &d
		}
	}
	return &c
}

// FindData sucht einen Daten-Eintrag (volume:, anon:, path:). project ist
// der Name des Projekts, in dem er steht, oder leer für unassigned.
func (m *Manifest) FindData(key string) (d *Data, project string) {
	for _, name := range sortedKeys(m.Projects) {
		if p := m.Projects[name]; p != nil {
			if d := p.Data[key]; d != nil {
				return d, name
			}
		}
	}
	if d := m.Unassigned[key]; d != nil {
		return d, ""
	}
	return nil, ""
}

// Remove entfernt den Eintrag mit diesem Schlüssel und meldet, ob es ihn gab.
func (m *Manifest) Remove(key string) bool {
	switch {
	case strings.HasPrefix(key, "project:"):
		name := strings.TrimPrefix(key, "project:")
		if _, ok := m.Projects[name]; ok {
			delete(m.Projects, name)
			return true
		}
	case strings.HasPrefix(key, "compose:"):
		proj, svc, ok := splitServiceKey(key)
		if p := m.Projects[proj]; ok && p != nil {
			if _, ok := p.Services[svc]; ok {
				delete(p.Services, svc)
				return true
			}
		}
	case strings.HasPrefix(key, "managed:"):
		name := strings.TrimPrefix(key, "managed:")
		for _, p := range m.Projects {
			if p == nil {
				continue
			}
			if _, ok := p.Managed[name]; ok {
				delete(p.Managed, name)
				return true
			}
		}
	case strings.HasPrefix(key, "container:"):
		if _, ok := m.Containers[key]; ok {
			delete(m.Containers, key)
			return true
		}
	case key == "host:caddy":
		if m.Host != nil && m.Host.Caddy != nil {
			m.Host.Caddy = nil
			return true
		}
	default:
		if _, ok := m.Unassigned[key]; ok {
			delete(m.Unassigned, key)
			return true
		}
		for _, p := range m.Projects {
			if p == nil {
				continue
			}
			if _, ok := p.Data[key]; ok {
				delete(p.Data, key)
				return true
			}
		}
	}
	return false
}

// SplitServiceKey zerlegt compose:<projekt>/<dienst>[#n] in Projekt und
// Dienst. Weitere Instanzen (#2, #3) teilen sich den Eintrag des Dienstes.
func SplitServiceKey(key string) (project, service string, ok bool) {
	return splitServiceKey(key)
}

func splitServiceKey(key string) (string, string, bool) {
	rest, ok := strings.CutPrefix(key, "compose:")
	if !ok {
		return "", "", false
	}
	proj, svc, ok := strings.Cut(rest, "/")
	if !ok || proj == "" || svc == "" {
		return "", "", false
	}
	if i := strings.LastIndex(svc, "#"); i > 0 {
		svc = svc[:i]
	}
	return proj, svc, true
}
