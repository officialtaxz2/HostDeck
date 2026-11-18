// Package zustand rechnet aus, in welchem Zustand eine Domain gerade ist:
// antwortet, pausiert (du hast ihr Ziel gestoppt), aus, antwortet nicht oder
// zeigt ins Leere. Es ist die eine Quelle dafür: Die
// Überwachung nimmt sie für die Pause, der Leitstand für jede Anzeige. So
// bewertet keine Stelle denselben Zustand anders als eine andere.
//
// Grundlage sind die Live-Prüfung der Domains, der Live-Stand der Container
// (mit „von dir gestoppt“) und, nur um Ziele zuzuordnen, die letzte
// Bestandsaufnahme. Deren Routen-Status gilt nie als aktueller Zustand.
package zustand

import (
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/officialtaxz2/hostdeck/internal/betrieb"
	"github.com/officialtaxz2/hostdeck/internal/domains"
	"github.com/officialtaxz2/hostdeck/internal/inventory"
)

// Zustände einer Domain; ein Wort je Zustand in der Oberfläche.
const (
	Antwortet      = "antwortet"
	Pausiert       = "pausiert"
	Aus            = "aus"
	AntwortetNicht = "antwortet-nicht"
	InsLeere       = "ins-leere"
	Ungeprueft     = "wird-geprueft"
	Startet        = "startet"
)

// StartGrace: So lange nach dem Start gilt eine Seite, die noch nicht
// antwortet, als „startet gerade“ und nicht als Fehler.
const StartGrace = 2 * time.Minute

// Stufen wie in der Oberfläche (ls-st ok, idle, warn, err).
const (
	LevelOK   = "ok"
	LevelIdle = "idle"
	LevelWarn = "warn"
	LevelErr  = "err"
)

// Words sind die kurzen Wörter für die Marken; überall dieselben.
var Words = map[string]string{
	Antwortet:      "antwortet",
	Pausiert:       "pausiert",
	Aus:            "aus",
	AntwortetNicht: "antwortet nicht",
	InsLeere:       "zeigt ins Leere",
	Ungeprueft:     "wird geprüft",
	Startet:        "startet gerade",
}

// Domain ist der Zustand einer Seite.
type Domain struct {
	Host  string   `json:"domain"`
	Hosts []string `json:"domains"`
	State string   `json:"zustand"`
	Level string   `json:"stufe"`
	Word  string   `json:"wort"`
	// Text sagt in einem Satz, warum; leer, wenn alles gut ist.
	Text string `json:"text,omitempty"`
	// Target ist der Container dahinter (Docker-Name), sonst etwa
	// „Dienst caddy“ oder leer. Hat die Domain mehrere Ziele (etwa /api/*
	// auf einen anderen Port), steht das eigentliche zuerst und alle in
	// Targets.
	Target  string   `json:"ziel,omitempty"`
	Targets []string `json:"ziele,omitempty"`
	// Upstreams sind die Adressen der Ziele aus Caddy, das eigentliche zuerst.
	Upstreams []string `json:"adressen,omitempty"`
	Project   string   `json:"projekt,omitempty"`
	// Block: alle Domains desselben Caddy-Blocks (Hosts ist nur diese eine).
	Block []string `json:"block,omitempty"`
	// Own: die Adresse des Leitstands von HostDeck selbst.
	Own bool `json:"leitstand,omitempty"`
	// PausedSince: seit wann die Pause gilt (du hast das Ziel gestoppt),
	// sonst nil. Daraus zählt die Überwachung Pausen nicht als Ausfall.
	PausedSince *time.Time `json:"pausiert_seit,omitempty"`
	// Live ist die letzte Prüfung genau dieser Domain.
	Live *domains.Live `json:"-"`
	// Stopped: von dir gestoppte Container, die diese Seite betreffen.
	Stopped []string `json:"von_dir_gestoppt,omitempty"`
	Answer  string   `json:"antwort,omitempty"`
}

// Domains rechnet den Zustand aller Seiten aus. Abgeschaltete Seiten stehen
// als „aus“ darin, Seiten ohne Namen oder mit Platzhalter (*.example) nicht.
// conts ist der Live-Stand von Docker; inv darf fehlen.
func Domains(sites []domains.Site, conts []betrieb.Container, inv *inventory.Inventory, now time.Time) []Domain {
	ix := newIndex(conts, inv)
	ix.now = now
	out := make([]Domain, 0, len(sites))
	seen := map[string]bool{}
	// Eine Zeile je Domain: Stehen mehrere in einem Block, bekommt jede
	// ihren eigenen Zustand aus ihrer eigenen Prüfung. Steht eine
	// Domain in zwei Blöcken, gilt der erste, wie bei Caddy.
	for _, st := range sites {
		for _, h := range st.Hosts {
			if h == "" || strings.HasPrefix(h, "*") || seen[h] {
				continue
			}
			seen[h] = true
			one := st
			one.Hosts = []string{h}
			one.Live = nil
			if lv, ok := st.Each[h]; ok {
				lv := lv
				one.Live = &lv
			} else if h == st.Hosts[0] {
				one.Live = st.Live
			}
			d := ix.domain(one)
			if len(st.Hosts) > 1 {
				d.Block = st.Hosts
			}
			out = append(out, d)
		}
	}
	return out
}

// OwnUpstream ist das Ziel, über das Caddy den Leitstand von HostDeck erreicht
// (ort.CaddySocket); zustand kennt nur die Endung, damit es ort nicht braucht.
const OwnUpstream = "/web.sock"

type index struct {
	byName  map[string]*betrieb.Container
	byPort  map[int][]*betrieb.Container
	byProj  map[string][]*betrieb.Container
	inv     *inventory.Inventory
	invRte  map[string]string // Domain -> Schlüssel des Ziels laut Bestandsaufnahme
	invPort map[int]string    // Port -> Schlüssel laut Bestandsaufnahme (Container und Lauscher)
	now     time.Time
}

func newIndex(conts []betrieb.Container, inv *inventory.Inventory) *index {
	ix := &index{byName: map[string]*betrieb.Container{}, byPort: map[int][]*betrieb.Container{}, byProj: map[string][]*betrieb.Container{},
		inv: inv, invRte: map[string]string{}, invPort: map[int]string{}}
	for i := range conts {
		c := &conts[i]
		ix.byName[c.Name] = c
		for _, p := range c.HostPorts {
			ix.byPort[p] = append(ix.byPort[p], c)
		}
		if c.Project != "" {
			ix.byProj[c.Project] = append(ix.byProj[c.Project], c)
		}
	}
	// Läuft einer der Container auf einem Port, gilt er; sonst der erste.
	for p, cs := range ix.byPort {
		sort.SliceStable(cs, func(a, b int) bool { return cs[a].Running && !cs[b].Running })
		ix.byPort[p] = cs
	}
	if inv != nil {
		for _, c := range inv.Containers {
			for _, p := range c.Ports {
				if p.Proto == "tcp" && p.HostPort > 0 && ix.invPort[p.HostPort] == "" {
					ix.invPort[p.HostPort] = c.Key
				}
			}
		}
		for _, l := range inv.Listeners {
			if l.Proto == "tcp" && l.Port > 0 && ix.invPort[l.Port] == "" {
				switch {
				case l.Container != "":
					ix.invPort[l.Port] = l.Container
				case l.Process != "":
					ix.invPort[l.Port] = "host:" + l.Process
				}
			}
		}
		if inv.Caddy != nil {
			for _, r := range inv.Caddy.Routes {
				if r.Target == "" {
					continue
				}
				for _, h := range r.Hosts {
					ix.invRte[h] = r.Target
				}
			}
		}
	}
	return ix
}

// name übersetzt einen Schlüssel der Bestandsaufnahme (compose:p/d,
// container:n) in den Docker-Namen.
func (ix *index) name(key string) string {
	if ix.inv != nil {
		if c := ix.inv.Container(key); c != nil {
			return c.Name
		}
	}
	if n, ok := strings.CutPrefix(key, "container:"); ok {
		return n
	}
	return ""
}

// target sucht den Container hinter einer Seite: zuerst live über den
// veröffentlichten Port oder den Docker-Namen im Ziel, dann über die
// Bestandsaufnahme. host ist gesetzt, wenn ein Dienst auf dem Host
// dahinter steht.
func (ix *index) target(st domains.Site) (c *betrieb.Container, host string) {
	return ix.targetOf(st, ix.ordered(st))
}

// ordered sind die Ziele, das eigentliche (ohne Pfad-Matcher) zuerst.
func (ix *index) ordered(st domains.Site) []string {
	ups := append([]string(nil), st.Main...)
	for _, u := range st.Upstreams {
		dup := false
		for _, x := range ups {
			dup = dup || x == u
		}
		if !dup {
			ups = append(ups, u)
		}
	}
	return ups
}

// targets nennt alle Ziele einer Seite (Container oder Dienst), das
// eigentliche zuerst, ohne doppelte.
func (ix *index) targets(st domains.Site) []string {
	var out []string
	for _, u := range ix.ordered(st) {
		c, host := ix.targetOf(st, []string{u})
		n := host
		if c != nil {
			n = c.Name
		}
		dup := n == ""
		for _, x := range out {
			dup = dup || x == n
		}
		if !dup {
			out = append(out, n)
		}
	}
	return out
}

func (ix *index) targetOf(st domains.Site, ups []string) (c *betrieb.Container, host string) {
	for _, u := range ups {
		h, port := splitUpstream(u)
		if port > 0 && local(h) {
			if cs := ix.byPort[port]; len(cs) > 0 {
				return cs[0], ""
			}
			if k := ix.invPort[port]; k != "" {
				if p, ok := strings.CutPrefix(k, "host:"); ok {
					return nil, "Dienst " + p
				}
				if x := ix.byName[ix.name(k)]; x != nil {
					return x, ""
				}
			}
			continue
		}
		// Caddy im Container: Das Ziel ist ein Docker-Name oder ein Dienst.
		if h != "" && net.ParseIP(h) == nil {
			if x := ix.byName[h]; x != nil {
				return x, ""
			}
			// Ein Dienst dieses Namens; heißen mehrere so, bleibt es offen.
			var hit *betrieb.Container
			n := 0
			for _, x := range ix.byName {
				if x.Service == h {
					hit = x
					n++
				}
			}
			if n == 1 {
				return hit, ""
			}
		}
	}
	if k := ix.invRte[st.Hosts[0]]; k != "" {
		if p, ok := strings.CutPrefix(k, "host:"); ok {
			return nil, "Dienst " + p
		}
		if x := ix.byName[ix.name(k)]; x != nil {
			return x, ""
		}
	}
	return nil, ""
}

func (ix *index) domain(st domains.Site) Domain {
	d := Domain{Host: st.Hosts[0], Hosts: st.Hosts, Project: st.Project, Live: st.Live}
	for _, u := range st.Upstreams {
		if strings.HasSuffix(u, OwnUpstream) && strings.HasPrefix(u, "unix/") {
			d.Own, d.Target = true, "hostdeck"
		}
	}
	c, host := ix.target(st)
	switch {
	case d.Own:
	case c != nil:
		d.Target = c.Name
		if c.Project != "" {
			d.Project = c.Project
		}
	case host != "":
		d.Target = host
	}
	if ts := ix.targets(st); len(ts) > 1 {
		d.Targets = ts
	}
	d.Upstreams = ix.ordered(st)
	set := func(state, level, text string) Domain {
		d.State, d.Level, d.Word, d.Text = state, level, Words[state], text
		return d
	}
	if st.Kind == domains.KindOff {
		return set(Aus, LevelIdle, "Im Leitstand abgeschaltet; Caddy bedient sie nicht.")
	}
	lv := st.Live
	if lv == nil {
		return set(Ungeprueft, LevelIdle, "")
	}
	d.Answer = lv.Answer
	if lv.OK {
		return set(Antwortet, LevelOK, "")
	}
	// Was du gestoppt hast, das diese Seite betrifft.
	proj := ix.byProj[d.Project]
	var mine []string
	all := false
	if c != nil && c.StoppedByYou != nil {
		mine = append(mine, c.Name)
	}
	moved := c != nil && c.StoppedByMove
	if len(proj) > 0 {
		own, byYou, byMove := 0, 0, 0
		for _, x := range proj {
			if x.ManagedBy != "" || x.Job {
				continue
			}
			own++
			if x.StoppedByMove {
				byMove++
				continue
			}
			if x.StoppedByYou != nil {
				byYou++
				if c == nil || x.Name != c.Name {
					mine = append(mine, x.Name)
				}
			}
		}
		all = own > 0 && byYou == own
		moved = moved || (own > 0 && byMove == own)
	}
	d.Stopped = mine
	switch {
	// Der letzte Lauf des Umzugstags hat das Ziel gestoppt: absichtlich, bis
	// die Domains auf den neuen Server zeigen (oder bis zum Rückweg).
	case moved:
		return set(Pausiert, LevelIdle, "Seit dem letzten Lauf des Umzugs aus; die Seite läuft jetzt auf dem neuen Server, sobald ihre Domain dorthin zeigt.")
	// Ganzes Projekt gestoppt: so nennen, wie du es im Leitstand getan hast.
	// Die Pause gilt, seit der letzte Container gestoppt wurde.
	case all:
		for _, x := range proj {
			if x.StoppedByYou != nil && (d.PausedSince == nil || x.StoppedByYou.After(*d.PausedSince)) {
				t := *x.StoppedByYou
				d.PausedSince = &t
			}
		}
		return set(Pausiert, LevelIdle, "Du hast das Projekt "+d.Project+" gestoppt; die Seite ist pausiert, bis du es wieder startest.")
	case c != nil && c.StoppedByYou != nil:
		t := *c.StoppedByYou
		d.PausedSince = &t
		return set(Pausiert, LevelIdle, "Du hast "+c.Name+" gestoppt; die Seite ist pausiert, bis du ihn wieder startest.")
	case len(mine) > 0:
		return set(AntwortetNicht, LevelWarn, "Du hast "+list(mine)+" gestoppt; liegt es daran?")
	case c != nil && !c.Running:
		return set(AntwortetNicht, LevelErr, c.Name+" ist aus, ohne dass du ihn im Leitstand gestoppt hast.")
	case c != nil && c.Since != nil && ix.now.Sub(*c.Since) < StartGrace:
		return set(Startet, LevelIdle, c.Name+" läuft erst seit kurzem; die Seite antwortet meist gleich.")
	case lv.Listening != nil && !*lv.Listening:
		return set(InsLeere, LevelErr, "Am Ziel "+strings.Join(st.Upstreams, ", ")+" lauscht nichts.")
	}
	return set(AntwortetNicht, LevelErr, strings.TrimPrefix(lv.Answer, "Fehler: "))
}

func list(xs []string) string {
	if len(xs) <= 1 {
		return strings.Join(xs, "")
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " und " + xs[len(xs)-1]
}

// splitUpstream liest Host und Port aus einem Ziel von reverse_proxy
// (127.0.0.1:3000, http://app:8080, localhost:80).
func splitUpstream(u string) (string, int) {
	for _, p := range []string{"http://", "https://", "h2c://"} {
		u = strings.TrimPrefix(u, p)
	}
	if strings.ContainsAny(u, "{}/") {
		return "", 0
	}
	h, ps, err := net.SplitHostPort(u)
	if err != nil {
		return u, 0
	}
	p, err := strconv.Atoi(ps)
	if err != nil {
		return h, 0
	}
	return h, p
}

func local(h string) bool {
	if h == "" || h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

// ByHost macht aus der Liste eine Zuordnung nach Domain.
func ByHost(ds []Domain) map[string]Domain {
	m := make(map[string]Domain, len(ds))
	for _, d := range ds {
		m[d.Host] = d
	}
	return m
}
