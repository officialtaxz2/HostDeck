package web

import (
	"fmt"
	"path"
	"strings"

	"github.com/officialtaxz2/hostdeck/internal/inventory"
	"github.com/officialtaxz2/hostdeck/internal/manifest"
)

// Edit ist eine Änderung an einem Eintrag. Nur die gesetzten Felder werden
// geändert. Erlaubte Werte prüft danach manifest.Validate.
type Edit struct {
	Key      string  `json:"key"`
	Decision *string `json:"decision,omitempty"` // project:, container:, host:caddy
	Image    *string `json:"image,omitempty"`    // compose:
	Strategy *string `json:"strategy,omitempty"` // volume:, anon:, path:
	Reason   *string `json:"reason,omitempty"`
	SplitOf  string  `json:"split_of,omitempty"` // neuer eigener Unterpfad unter diesem Eintrag oder Projekt
	Remove   bool    `json:"remove,omitempty"`   // eigenen Unterpfad oder weggefallenen Eintrag entfernen
}

func apply(l *loaded, e Edit) error {
	m := l.m
	k := e.Key
	if e.Reason != nil && len(*e.Reason) > 500 {
		return fmt.Errorf("%s: Begründung ist zu lang (höchstens 500 Zeichen)", k)
	}
	switch {
	case e.Remove:
		return remove(l, k)
	case e.SplitOf != "":
		return addSplit(l, e)
	case strings.HasPrefix(k, "project:"):
		p := m.Projects[strings.TrimPrefix(k, "project:")]
		if p == nil {
			return unknown(k)
		}
		setDecision(&p.Decision, &p.Reason, e)
	case strings.HasPrefix(k, "compose:"):
		proj, svc, ok := manifest.SplitServiceKey(k)
		p := m.Projects[proj]
		if !ok || p == nil || p.Services[svc] == nil {
			return unknown(k)
		}
		if e.Image != nil {
			p.Services[svc].Image = *e.Image
		}
	case strings.HasPrefix(k, "container:"):
		c := m.Containers[k]
		if c == nil {
			return unknown(k)
		}
		setDecision(&c.Decision, &c.Reason, e)
	case k == "host:caddy":
		if m.Host == nil || m.Host.Caddy == nil {
			return unknown(k)
		}
		setDecision(&m.Host.Caddy.Decision, &m.Host.Caddy.Reason, e)
	case strings.HasPrefix(k, "volume:"), strings.HasPrefix(k, "anon:"), strings.HasPrefix(k, "path:"):
		d, _ := m.FindData(k)
		if d == nil {
			return unknown(k)
		}
		if e.Strategy != nil {
			if settingsArea(l.inv, k) && *e.Strategy != "archive" && *e.Strategy != "skip" {
				return fmt.Errorf("%s: Systemeinstellungen lassen sich nur archivieren oder weglassen; 1:1 zurückgespielt könnten sie das neue System beschädigen", k)
			}
			d.Strategy = *e.Strategy
		}
		if e.Reason != nil {
			d.Reason = strings.TrimSpace(*e.Reason)
		}
	default:
		return unknown(k)
	}
	return nil
}

func settingsArea(inv *inventory.Inventory, key string) bool {
	for _, u := range inv.Unclaimed {
		if u.Key == key {
			return u.Area == inventory.AreaSettings
		}
	}
	return false
}

func setDecision(decision, reason *string, e Edit) {
	if e.Decision != nil {
		*decision = *e.Decision
	}
	if e.Reason != nil {
		*reason = strings.TrimSpace(*e.Reason)
	}
}

func unknown(k string) error {
	return fmt.Errorf("%s: Eintrag nicht gefunden; bitte Seite neu laden", k)
}

// remove entfernt nur, was HostDeck nicht selbst vorschlägt: eigene Unterpfade und
// Einträge, die es auf dem Server nicht mehr gibt.
func remove(l *loaded, k string) error {
	for _, pk := range l.proposal.Keys() {
		if pk == k {
			return fmt.Errorf("%s: gibt es auf dem Server noch und kann nicht entfernt werden; wähle stattdessen „Weglassen“", k)
		}
	}
	if !l.m.Remove(k) {
		// Schon mit seinem Projekt entfernt: Was beim Laden als
		// weggefallen galt, darf in derselben Änderung fehlen.
		for _, g := range l.gone {
			if g == k {
				return nil
			}
		}
		return unknown(k)
	}
	return nil
}

// addSplit legt einen eigenen Unterpfad an, etwa um die Spielserver-Backups
// in /opt/calagopus/wings/backups auszunehmen.
func addSplit(l *loaded, e Edit) error {
	m := l.m
	p, ok := strings.CutPrefix(e.Key, "path:")
	if !ok || !path.IsAbs(p) || path.Clean(p) != p {
		return fmt.Errorf("%s: Unterordner muss ein absoluter Pfad ohne .. und ohne / am Ende sein", e.Key)
	}
	var parentPath, project string
	if name, isProject := strings.CutPrefix(e.SplitOf, "project:"); isProject {
		ip := l.inv.Project(e.SplitOf)
		if ip == nil || m.Projects[name] == nil || ip.Path == "" {
			return unknown(e.SplitOf)
		}
		parentPath, project = ip.Path, name
	} else {
		pp, ok := strings.CutPrefix(e.SplitOf, "path:")
		d, proj := m.FindData(e.SplitOf)
		if !ok || d == nil {
			return fmt.Errorf("%s: Unterordner gehen nur bei Ordnern, nicht bei Volumes", e.Key)
		}
		parentPath, project = pp, proj
	}
	if !strings.HasPrefix(p, parentPath+"/") {
		return fmt.Errorf("%s liegt nicht in %s", p, parentPath)
	}
	if d, _ := m.FindData(e.Key); d != nil {
		return fmt.Errorf("%s hat schon einen eigenen Eintrag", p)
	}
	d := &manifest.Data{Strategy: "skip"}
	if e.Strategy != nil {
		d.Strategy = *e.Strategy
	}
	if e.Reason != nil {
		d.Reason = strings.TrimSpace(*e.Reason)
	}
	if project == "" {
		if m.Unassigned == nil {
			m.Unassigned = map[string]*manifest.Data{}
		}
		m.Unassigned[e.Key] = d
		return nil
	}
	pr := m.Projects[project]
	if pr.Data == nil {
		pr.Data = map[string]*manifest.Data{}
	}
	pr.Data[e.Key] = d
	return nil
}
